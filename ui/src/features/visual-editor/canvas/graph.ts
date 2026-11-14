// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { readRepeatPolicy } from '@/components/ui/repeat-summary';
import type {
  ApiDAGDetails,
  CanvasEdge,
  CanvasGraph,
  CanvasGraphInput,
  CanvasNode,
  DagDocModel,
  StepKey,
  StepNameMap,
  StepView,
  ValidationIssue,
} from '../contracts';
import { NodeStatus } from '@/api/v1/schema';
import { previousOfExecuted } from '../run/upstreamSteps';
import { readAgentHeader } from './agentHeader';
import {
  AGENT_NODE_KEY,
  type AgentTaskView,
  type CanvasEdgeWithRef,
  type CanvasNodeWithRoutes,
} from './types';

function own<T>(record: Record<string, T>, key: string): T | undefined {
  return Object.prototype.hasOwnProperty.call(record, key)
    ? record[key]
    : undefined;
}

/**
 * Build a function that resolves a step name or id, as written in the YAML,
 * to the key of a main-lane step. Order: explicit name, id, server name.
 * Handlers are never resolved.
 */
export function buildStepResolver(
  model: DagDocModel,
  names: StepNameMap
): (ref: string) => StepKey | null {
  const byExplicit = new Map<string, StepKey>();
  const byId = new Map<string, StepKey>();
  const mainKeys = new Set<StepKey>();
  for (const step of model.steps) {
    mainKeys.add(step.key);
    if (step.explicitName !== undefined && !byExplicit.has(step.explicitName)) {
      byExplicit.set(step.explicitName, step.key);
    }
    if (step.id !== undefined && !byId.has(step.id)) {
      byId.set(step.id, step.key);
    }
  }
  return (ref) => {
    const key =
      byExplicit.get(ref) ?? byId.get(ref) ?? own(names.byName, ref) ?? null;
    return key !== null && mainKeys.has(key) ? key : null;
  };
}

/** Messages of the errors and warnings of a node, in the order of the issues. */
export type IssueMessages = NonNullable<CanvasNode['issueMessages']>;

function countIssues(
  list: ValidationIssue[] | undefined
): CanvasNode['issues'] {
  const counts = { errors: 0, warnings: 0 };
  for (const issue of list ?? []) {
    if (issue.severity === 'error') {
      counts.errors++;
    } else if (issue.severity === 'warning') {
      counts.warnings++;
    }
  }
  return counts;
}

function collectIssueMessages(
  list: ValidationIssue[] | undefined
): IssueMessages | undefined {
  const messages: IssueMessages = { errors: [], warnings: [] };
  for (const issue of list ?? []) {
    if (issue.severity === 'error') {
      messages.errors.push(issue.message);
    } else if (issue.severity === 'warning') {
      messages.warnings.push(issue.message);
    }
  }
  return messages.errors.length > 0 || messages.warnings.length > 0
    ? messages
    : undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isRouter(step: StepView): boolean {
  return step.kind === 'router' || step.action === 'router.route';
}

/** Read `with.routes`, or the deprecated top-level `routes`. */
function routeTable(step: StepView): Record<string, unknown> {
  const withBlock = step.value.with;
  if (isRecord(withBlock) && isRecord(withBlock.routes)) {
    return withBlock.routes;
  }
  return isRecord(step.value.routes) ? step.value.routes : {};
}

/** The DAG that a `call` step runs: `call:` or `with.dag`. */
function calledDag(step: StepView): string | undefined {
  if (step.kind !== 'call') {
    return undefined;
  }
  if (typeof step.value.call === 'string') {
    return step.value.call;
  }
  const withBlock = step.value.with;
  return isRecord(withBlock) && typeof withBlock.dag === 'string'
    ? withBlock.dag
    : undefined;
}

/** What a router routes on: `with.value`, or the deprecated `value`. */
function routeValue(step: StepView): string | undefined {
  const withBlock = step.value.with;
  const raw =
    isRecord(withBlock) && withBlock.value !== undefined
      ? withBlock.value
      : step.value.value;
  return typeof raw === 'string' ? raw : undefined;
}

function routeTargets(raw: unknown): string[] {
  if (typeof raw === 'string') {
    return [raw];
  }
  return Array.isArray(raw)
    ? raw.filter((item): item is string => typeof item === 'string')
    : [];
}

/** The text when it is a string with something in it, else undefined. */
function filled(text: unknown): string | undefined {
  return typeof text === 'string' && text.trim() !== '' ? text : undefined;
}

function invalidLabel(step: StepView): string {
  return `Invalid step ${(step.index ?? 0) + 1}`;
}

function buildNode(
  step: StepView,
  lane: 'main' | 'handler',
  input: CanvasGraphInput
): CanvasNodeWithRoutes {
  const { names, issues, overlay } = input;
  const serverName = own(names.byKey, step.key);
  const stepIssues = own(issues.byStep, step.key);
  const name = filled(step.explicitName) ?? filled(serverName);
  const node: CanvasNodeWithRoutes = {
    key: step.key,
    label:
      name ??
      (step.broken !== undefined
        ? invalidLabel(step)
        : (filled(step.id) ?? step.key)),
    kind: step.kind,
    lane,
    issues: countIssues(stepIssues),
  };
  if (name === undefined && step.broken !== undefined) {
    node.placeholderLabel = true;
  }
  const issueMessages = collectIssueMessages(stepIssues);
  if (issueMessages) {
    node.issueMessages = issueMessages;
  }
  // A step that cannot be read, or whose depends has the wrong type, shows
  // the problem even when the server has nothing to say about the step.
  const problem = step.broken ?? step.problem;
  if (
    problem !== undefined &&
    (step.broken !== undefined || !node.issues.errors)
  ) {
    node.issues = { ...node.issues, errors: node.issues.errors + 1 };
    node.issueMessages = {
      errors: [problem, ...(node.issueMessages?.errors ?? [])],
      warnings: node.issueMessages?.warnings ?? [],
    };
  }
  if (serverName !== undefined) {
    node.serverName = serverName;
  }
  const subtitle = step.action ?? step.runPreview;
  if (subtitle !== undefined) {
    node.subtitle = subtitle;
  }
  if (step.action !== undefined) {
    node.action = step.action;
  }
  if (step.broken !== undefined) {
    node.subtitle = 'YAML error';
  }
  const called = calledDag(step);
  if (called !== undefined) {
    node.subDag = called;
    node.subtitle = `→ ${called}`;
  }
  if ('parallel' in step.value) {
    node.parallel = true;
  }
  const repeat = readRepeatPolicy(
    step.value.repeat_policy ?? step.value.repeatPolicy
  );
  if (repeat) {
    node.repeat = repeat;
  }
  if (isRouter(step)) {
    const value = routeValue(step);
    if (value !== undefined) {
      node.routeValue = value;
    }
    const table = routeTable(step);
    const targets: Record<string, string[]> = {};
    for (const pattern of Object.keys(table)) {
      targets[pattern] = routeTargets(table[pattern]);
    }
    node.routes = Object.keys(targets);
    node.routeTargets = targets;
  }
  const runName = serverName ?? step.explicitName;
  const run =
    overlay && runName !== undefined ? own(overlay, runName) : undefined;
  if (run) {
    node.run = run;
  }
  return node;
}

function dependsEdges(
  model: DagDocModel,
  resolve: (ref: string) => StepKey | null
): CanvasEdgeWithRef[] {
  const edges: CanvasEdgeWithRef[] = [];
  const seen = new Set<string>();
  for (const step of model.steps) {
    for (const entry of step.depends) {
      const source = resolve(entry);
      if (source === null || source === step.key) {
        continue;
      }
      const id = `depends:${source}->${step.key}`;
      if (seen.has(id)) {
        continue;
      }
      seen.add(id);
      edges.push({
        id,
        source,
        target: step.key,
        kind: 'depends',
        written: entry,
      });
    }
  }
  return edges;
}

function inferredEdges(
  model: DagDocModel,
  names: StepNameMap,
  details: ApiDAGDetails | undefined,
  known: Set<string>
): CanvasEdgeWithRef[] {
  const edges: CanvasEdgeWithRef[] = [];
  const mainKeys = new Set(model.steps.map((step) => step.key));
  const mainKey = (name: string): StepKey | null => {
    const key = own(names.byName, name);
    return key !== undefined && mainKeys.has(key) ? key : null;
  };
  for (const detail of details?.steps ?? []) {
    const target = mainKey(detail.name);
    if (target === null) {
      continue;
    }
    for (const name of detail.inferredDepends ?? []) {
      const source = mainKey(name);
      if (source === null || source === target) {
        continue;
      }
      const pair = `${source}->${target}`;
      if (known.has(pair)) {
        continue;
      }
      known.add(pair);
      edges.push({
        id: `inferred:${pair}`,
        source,
        target,
        kind: 'inferred',
      });
    }
  }
  return edges;
}

/**
 * Chain DAGs run steps in YAML order. Steps of one parallel group run
 * together, so each step of a group links to each step of the next group.
 */
function orderEdges(model: DagDocModel): CanvasEdgeWithRef[] {
  const groups: StepKey[][] = [];
  let lastGroup: number | undefined;
  for (const step of model.steps) {
    const current = groups[groups.length - 1];
    if (
      current !== undefined &&
      step.parallelGroup !== undefined &&
      step.parallelGroup === lastGroup
    ) {
      current.push(step.key);
    } else {
      groups.push([step.key]);
    }
    lastGroup = step.parallelGroup;
  }
  const edges: CanvasEdgeWithRef[] = [];
  for (let i = 0; i + 1 < groups.length; i++) {
    for (const source of groups[i] ?? []) {
      for (const target of groups[i + 1] ?? []) {
        edges.push({
          id: `order:${source}->${target}`,
          source,
          target,
          kind: 'order',
        });
      }
    }
  }
  return edges;
}

function routeEdges(
  nodes: CanvasNodeWithRoutes[],
  resolve: (ref: string) => StepKey | null
): CanvasEdgeWithRef[] {
  const edges: CanvasEdgeWithRef[] = [];
  const seen = new Set<string>();
  for (const node of nodes) {
    for (const pattern of node.routes ?? []) {
      for (const written of node.routeTargets?.[pattern] ?? []) {
        const target = resolve(written);
        if (target === null) {
          continue;
        }
        const id = `route:${node.key}:${pattern}->${target}`;
        if (seen.has(id)) {
          continue;
        }
        seen.add(id);
        edges.push({
          id,
          source: node.key,
          target,
          kind: 'route',
          label: pattern,
          written,
        });
      }
    }
  }
  return edges;
}

/**
 * Project the document model to the graph that the canvas draws. Nodes follow
 * the YAML order: main steps first, then handlers.
 */
/** The step that stands for the agent in the runs of an agent DAG. */
const AGENT_RUN_STEP = '__agent__';

/** The tasks on the hub: the YAML first, else the ones the server reports. */
function hubTasks(input: CanvasGraphInput): AgentTaskView[] | undefined {
  const written = readAgentHeader(
    input.model.sanitizedText ?? input.model.text
  ).tasks;
  const tasks: AgentTaskView[] | undefined =
    written.length > 0 ? written : input.details?.tasks;
  if (tasks === undefined) {
    return undefined;
  }
  return tasks.map((task) => {
    const view: AgentTaskView = { name: task.name };
    if (task.description) view.description = task.description;
    return view;
  });
}

/** The node of the agent of an agent DAG, with its state in the run. */
function agentNode(input: CanvasGraphInput): CanvasNodeWithRoutes {
  const header = readAgentHeader(input.model.sanitizedText ?? input.model.text);
  const tasks = hubTasks(input);
  const node: CanvasNodeWithRoutes = {
    key: AGENT_NODE_KEY,
    label: 'Agent',
    kind: 'agent',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
    hub: { tasks: tasks ?? [] },
  };
  const hub = node.hub!;
  if (header.provider) hub.provider = header.provider;
  if (header.model) hub.model = header.model;
  if (header.systemFirstLine) hub.systemFirstLine = header.systemFirstLine;
  if (header.maxIterations !== undefined) {
    hub.maxIterations = header.maxIterations;
  }
  if (tasks !== undefined) {
    node.taskCount = tasks.length;
    node.subtitle = tasks.length === 1 ? '1 task' : `${tasks.length} tasks`;
  }
  const run = input.overlay ? own(input.overlay, AGENT_RUN_STEP) : undefined;
  if (run) {
    node.run = run;
  }
  return node;
}

/** Marks a step of an agent DAG as a tool card of the agent. */
function toolNode(
  node: CanvasNodeWithRoutes,
  step: StepView | undefined
): void {
  node.tool = {};
  if (step?.description) node.tool.description = step.description;
}

/**
 * Sets the reuse outline of the steps that a run reused: only the previous
 * steps of the steps that the run executed. A step after them or on another
 * branch has none, even when the run carries its outputs. The previous steps
 * come from the edges of the graph, with the same helper as the reuse source
 * of a step run. Changes the nodes in place.
 */
function markReuseOutlines(
  nodes: CanvasNodeWithRoutes[],
  edges: CanvasEdge[]
): void {
  const main = nodes.filter((node) => node.lane === 'main' && node.run);
  const executed = main
    .filter(
      (node) =>
        !node.run?.reused &&
        node.run?.status !== NodeStatus.Skipped &&
        node.run?.status !== NodeStatus.NotStarted
    )
    .map((node) => node.key);
  if (
    executed.length === 0 ||
    !main.some((node) => node.run?.reused || node.run?.reuseFailed)
  ) {
    return;
  }
  const needs = new Map<string, string[]>();
  for (const edge of edges) {
    if (edge.kind === 'agent') continue;
    needs.set(edge.target, [...(needs.get(edge.target) ?? []), edge.source]);
  }
  const previous = previousOfExecuted(
    nodes.map((node) => ({ name: node.key, depends: needs.get(node.key) })),
    executed
  );
  for (const node of main) {
    if (!previous.has(node.key)) continue;
    if (node.run?.reused) node.reuseOutline = 'success';
    else if (node.run?.reuseFailed) node.reuseOutline = 'failure';
  }
}

export function buildCanvasGraph(input: CanvasGraphInput): CanvasGraph {
  const { model, names } = input;
  const resolve = buildStepResolver(model, names);
  const nodes: CanvasNodeWithRoutes[] = [
    ...model.steps.map((step) => buildNode(step, 'main', input)),
    ...model.handlers.map((step) => buildNode(step, 'handler', input)),
  ];
  const editableEdges = model.dagType !== 'chain' && model.dagType !== 'agent';
  const edges = dependsEdges(model, resolve);
  const known = new Set(edges.map((edge) => `${edge.source}->${edge.target}`));
  edges.push(...inferredEdges(model, names, input.details, known));
  if (model.dagType === 'chain') {
    edges.push(...orderEdges(model));
  }
  edges.push(...routeEdges(nodes, resolve));
  if (model.dagType === 'agent') {
    // The steps are the actions of the agent: each hangs from its node, and
    // draws as a tool card.
    const byKey = new Map(model.steps.map((step) => [step.key, step]));
    for (const node of nodes) {
      if (node.lane === 'main') toolNode(node, byKey.get(node.key));
    }
    nodes.unshift(agentNode(input));
    edges.unshift(
      ...model.steps.map(
        (step): CanvasEdge => ({
          id: `agent:${AGENT_NODE_KEY}->${step.key}`,
          source: AGENT_NODE_KEY,
          target: step.key,
          kind: 'agent',
        })
      )
    );
  }
  markReuseOutlines(nodes, edges);
  return { nodes, edges, editableEdges };
}
