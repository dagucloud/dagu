// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { readRepeatPolicy } from '@/components/ui/repeat-summary';
import type {
  ApiDAGDetails,
  ApiStep,
  CanvasEdge,
  CanvasGraph,
  CanvasNode,
  StepKind,
} from '../contracts';

const HANDLER_EVENTS = ['init', 'success', 'failure', 'abort', 'exit', 'wait'];

function kindOf(step: ApiStep): StepKind {
  if (step.router) return 'router';
  if (step.call) return 'call';
  if (step.parallel) return 'parallel';
  const type = step.executorConfig?.type;
  if (type && type !== 'command' && type !== 'shell') return 'action';
  return step.commands?.length || step.script ? 'run' : 'other';
}

function commandPreview(step: ApiStep): string | undefined {
  const first = step.commands?.[0] as
    | { command?: unknown; args?: unknown }
    | undefined;
  const text =
    first && typeof first.command === 'string'
      ? [first.command, ...(Array.isArray(first.args) ? first.args : [])].join(
          ' '
        )
      : (step.script ?? '');
  const line = text.split('\n').find((item) => item.trim() !== '');
  return line?.trim();
}

function nodeOf(step: ApiStep, key: string, lane: CanvasNode['lane']) {
  const kind = kindOf(step);
  const node: CanvasNode = {
    key,
    label: step.name,
    kind,
    lane,
    issues: { errors: 0, warnings: 0 },
  };
  const type = step.executorConfig?.type;
  if (kind === 'call' && step.call) {
    node.subDag = step.call;
    node.subtitle = `→ ${step.call}`;
  } else if (kind === 'router' && step.router) {
    node.routes = step.router.routes.map((route) => route.pattern);
    node.routeValue = step.router.value;
    node.action = 'router.route';
  } else if (kind === 'action' && type) {
    node.action = type;
    node.subtitle = type;
  } else {
    const preview = commandPreview(step);
    if (preview) node.subtitle = preview;
  }
  if (step.parallel) node.parallel = true;
  const repeat = readRepeatPolicy(step.repeatPolicy);
  if (repeat) node.repeat = repeat;
  return node;
}

/**
 * Canvas graph of a DAG that the API describes, for a DAG that the editor
 * does not edit, such as a sub-DAG of a file with several documents. The
 * keys are the step names. The graph has no editable edges.
 */
export function buildDetailsGraph(dag: ApiDAGDetails): CanvasGraph {
  const steps = dag.steps ?? [];
  const nodes: CanvasNode[] = steps.map((step) =>
    nodeOf(step, step.name, 'main')
  );
  const known = new Set(steps.map((step) => step.name));
  const edges: CanvasEdge[] = [];
  const routed = new Set<string>();

  for (const step of steps) {
    for (const route of step.router?.routes ?? []) {
      for (const target of route.targets) {
        if (!known.has(target)) continue;
        routed.add(`${step.name}->${target}`);
        edges.push({
          id: `route:${step.name}->${target}:${route.pattern}`,
          source: step.name,
          target,
          kind: 'route',
          label: route.pattern,
        });
      }
    }
  }
  for (const step of steps) {
    for (const dep of step.depends ?? []) {
      if (!known.has(dep) || routed.has(`${dep}->${step.name}`)) continue;
      edges.push({
        id: `depends:${dep}->${step.name}`,
        source: dep,
        target: step.name,
        kind: 'depends',
      });
    }
    for (const dep of step.inferredDepends ?? []) {
      const id = `depends:${dep}->${step.name}`;
      if (!known.has(dep) || edges.some((edge) => edge.id === id)) continue;
      edges.push({
        id: `inferred:${dep}->${step.name}`,
        source: dep,
        target: step.name,
        kind: 'inferred',
      });
    }
  }

  const handlers = dag.handlerOn as
    | Partial<Record<string, ApiStep | undefined>>
    | undefined;
  for (const event of HANDLER_EVENTS) {
    const handler = handlers?.[event];
    if (handler) {
      nodes.push(nodeOf(handler, `handler_on.${event}`, 'handler'));
    }
  }
  return { nodes, edges, editableEdges: false };
}
