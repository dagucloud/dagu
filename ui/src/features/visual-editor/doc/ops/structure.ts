// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Pair,
  Scalar,
  YAMLMap,
  YAMLSeq,
  isMap,
  isNode,
  isPair,
  isScalar,
  isSeq,
} from 'yaml';
import type { Document } from 'yaml';
import type {
  ApplyEditOptions,
  DagEditOp,
  EditErrorCode,
  EditResult,
  NewStep,
  StepKey,
} from '../../contracts';
import {
  uniqueCommandName,
  uniqueStepName,
  validateStepId,
  validateStepName,
} from '../names';
import { editDocument } from '../patch';
import type { MutateOutcome } from '../patch';
import {
  explicitNameOf,
  getDagType,
  getStepsNode,
  idOf,
  indexSteps,
  findStep,
} from '../stepIndex';
import type { IndexedStep } from '../stepIndex';
import {
  detectDependsStyle,
  readDepends,
  referenceNameFor,
  writeDepends,
} from './depends';
import { outputReferenceIds } from './references';

type Failure = { ok: false; code: EditErrorCode; message: string };

function fail(code: EditErrorCode, message: string): Failure {
  return { ok: false, code, message };
}

const CONVERT_HINT = 'Use convertStepsToExplicitForm first.';

/**
 * Refuses the DAG types that do not let the editor change the graph. The
 * steps of an agent DAG are the actions that the agent chooses from: they can
 * be added and deleted, but they have no order to edit.
 */
function checkDagType(
  doc: Document,
  chainToo: boolean,
  agentOk = false
): Failure | null {
  const type = getDagType(doc);
  if (type === 'agent' && !agentOk) {
    return fail(
      'agentType',
      'The steps of a type: agent DAG have no order: the agent chooses which one to run.'
    );
  }
  if (type === 'chain' && chainToo) {
    return fail(
      'chainType',
      'A type: chain DAG runs the steps in order. It has no depends to edit.'
    );
  }
  return null;
}

/** Finds a step that a structural op can change: a block entry or a map entry. */
function structuralStep(
  doc: Document,
  key: StepKey
): { ok: true; step: IndexedStep } | Failure {
  const step = findStep(doc, key);
  if (!step) return fail('unknownStep', `No step has the key ${key}.`);
  const { form } = step.loc;
  if ((form !== 'block' && form !== 'mapEntry') || !isMap(step.node)) {
    return fail(
      'unsupportedForm',
      `Step ${key} is a shorthand, parallel-group or handler entry. ${CONVERT_HINT}`
    );
  }
  return { ok: true, step };
}

/** Names and ids that are in use. The server names of unnamed steps count too. */
export function takenNames(
  doc: Document,
  options: ApplyEditOptions
): Set<string> {
  const taken = new Set<string>();
  const { steps, handlers } = indexSteps(doc);
  for (const step of [...steps, ...handlers]) {
    const name = explicitNameOf(step);
    const id = idOf(step);
    if (name !== undefined) taken.add(name.trim());
    if (id !== undefined) taken.add(id.trim());
  }
  for (const name of Object.keys(options.names?.byName ?? {})) taken.add(name);
  return taken;
}

/** The texts that `depends` can use for a step: its name, its id and its server name. */
function referencesOf(
  step: IndexedStep,
  options: ApplyEditOptions
): Set<string> {
  const refs = new Set<string>();
  const name = explicitNameOf(step);
  const id = idOf(step);
  const server = options.names?.byKey[step.key];
  for (const ref of [name, id, server]) {
    if (ref !== undefined && ref.trim() !== '') refs.add(ref);
  }
  return refs;
}

/** The ids that the step-output references below a node name. */
function outputProducers(node: unknown, ids: Set<string>): void {
  if (isMap(node)) {
    for (const pair of node.items) outputProducers(pair.value, ids);
  } else if (isSeq(node)) {
    for (const item of node.items) outputProducers(item, ids);
  } else if (isScalar(node) && typeof node.value === 'string') {
    for (const id of outputReferenceIds(node.value)) ids.add(id);
  }
}

/**
 * Edges of the step graph: each key maps to the keys of its direct downstream
 * steps. An entry of `depends` points from the step it names to the step that
 * lists it. A router points to each target of its routes. A step that reads
 * `${steps.<id>.outputs.<name>}` comes after the step with that id, as the
 * server infers (`inferStepOutputDependencies` in
 * internal/spec/inferred_depends.go); its description runs nowhere.
 * References that no step has are ignored.
 */
function downstreamGraph(
  doc: Document,
  options: ApplyEditOptions
): Map<StepKey, Set<StepKey>> {
  const { steps } = indexSteps(doc);
  const byReference = new Map<string, StepKey>();
  const byId = new Map<string, StepKey>();
  const graph = new Map<StepKey, Set<StepKey>>();
  for (const step of steps) {
    graph.set(step.key, new Set());
    for (const ref of referencesOf(step, options))
      byReference.set(ref, step.key);
    const id = idOf(step);
    if (id !== undefined) byId.set(id, step.key);
  }
  const link = (from: StepKey | undefined, to: StepKey) => {
    if (from !== undefined) graph.get(from)?.add(to);
  };
  for (const step of steps) {
    const producers = new Set<string>();
    if (isMap(step.node)) {
      for (const pair of step.node.items) {
        if (!(isScalar(pair.key) && pair.key.value === 'description')) {
          outputProducers(pair.value, producers);
        }
      }
    } else {
      outputProducers(step.node, producers);
    }
    for (const id of producers) {
      const producer = byId.get(id);
      if (producer !== step.key) link(producer, step.key);
    }
    if (!isMap(step.node)) continue;
    for (const d of readDepends(step.node.get('depends', true))) {
      link(byReference.get(d), step.key);
    }
    for (const pair of routesOf(step)?.items ?? []) {
      const targets = isSeq(pair.value) ? pair.value.items : [pair.value];
      for (const target of targets) {
        const name = textOf(target);
        const key = name === undefined ? undefined : byReference.get(name);
        if (key !== undefined) link(step.key, key);
      }
    }
  }
  return graph;
}

/** True when `to` is `from` or downstream of it. */
function reaches(
  graph: Map<StepKey, Set<StepKey>>,
  from: StepKey,
  to: StepKey
): boolean {
  const seen = new Set<StepKey>();
  const stack = [from];
  while (stack.length > 0) {
    const key = stack.pop() as StepKey;
    if (key === to) return true;
    if (seen.has(key)) continue;
    seen.add(key);
    stack.push(...(graph.get(key) ?? []));
  }
  return false;
}

function cycleError(step: StepKey, upstream: StepKey): Failure {
  return fail(
    'cycle',
    `Step ${step} cannot depend on ${upstream}: the dependencies would form a cycle.`
  );
}

function newEntry(
  doc: Document,
  name: string,
  mapForm: boolean,
  step: NewStep,
  depends: string[]
): YAMLMap {
  const entry = new YAMLMap();
  if (!mapForm)
    entry.items.push(new Pair(new Scalar('name'), new Scalar(name)));
  if (step.id !== undefined) {
    entry.items.push(new Pair(new Scalar('id'), new Scalar(step.id)));
  }
  writeDepends(doc, entry, depends, detectDependsStyle(doc));
  if (step.run !== undefined) {
    entry.items.push(new Pair(new Scalar('run'), new Scalar(step.run)));
  }
  if (step.action !== undefined) {
    entry.items.push(new Pair(new Scalar('action'), new Scalar(step.action)));
  }
  if (step.with !== undefined) {
    entry.items.push(new Pair(new Scalar('with'), doc.createNode(step.with)));
  }
  return entry;
}

/** Returns the `steps` collection, creating an empty block array when it is absent. */
export function ensureSteps(doc: Document): YAMLSeq | YAMLMap | Failure {
  const existing = getStepsNode(doc);
  if (existing) {
    // An empty flow collection (`steps: []`) would take the new entry in flow style.
    if (existing.items.length === 0) existing.flow = false;
    return existing;
  }
  if (doc.contents === null || doc.contents === undefined) {
    doc.contents = new YAMLMap();
  }
  const root = doc.contents;
  if (!isMap(root)) {
    return fail('invalidYaml', 'The document is not a mapping.');
  }
  const current = root.get('steps', true);
  if (current !== undefined && !(isScalar(current) && current.value === null)) {
    return fail('invalidYaml', 'The steps key is not a list or a mapping.');
  }
  const seq = new YAMLSeq();
  root.set('steps', seq);
  return seq;
}

/** True when the file separates its step entries (after the first) with a blank line. */
export function usesBlankLines(steps: YAMLSeq | YAMLMap): boolean {
  const nodes = isSeq(steps)
    ? steps.items
    : steps.items.map((pair) => pair.key);
  return nodes.slice(1).some((n) => isNode(n) && n.spaceBefore === true);
}

/**
 * Replaces the references to `old` in the depends of `step` by `name`. A step
 * that lists none of them, as one that only reads the outputs of `old`, gets
 * `name` added.
 */
function rewireDepends(
  doc: Document,
  step: IndexedStep,
  old: Set<string>,
  name: string
): void {
  const entry = step.node;
  if (!isMap(entry)) return;
  const current = readDepends(entry.get('depends', true));
  if (!current.some((d) => old.has(d))) {
    writeDepends(doc, entry, [...current, name], detectDependsStyle(doc));
    return;
  }
  const next: string[] = [];
  for (const d of current) {
    const value = old.has(d) ? name : d;
    if (!next.includes(value)) next.push(value);
  }
  writeDepends(doc, entry, next, detectDependsStyle(doc));
}

/**
 * Adds a step at the end of the steps, or after the entry of `after`. The new
 * name is `step.name` when it is valid and free, else a generated one. With
 * `insertBetween`, the step goes after `from`, depends on it, and replaces it
 * in the depends of `to`, or joins them when `to` only reads the outputs of
 * `from`. The result selects the new step.
 */
export function opAddStep(
  text: string,
  op: Extract<DagEditOp, { kind: 'addStep' }>,
  options: ApplyEditOptions
): EditResult {
  return editDocument(text, (doc): MutateOutcome => {
    const dependsOn = op.dependsOn ?? [];
    const typeError = checkDagType(
      doc,
      dependsOn.length > 0,
      dependsOn.length === 0 && !op.insertBetween
    );
    if (typeError) return typeError;

    let afterStep: IndexedStep | null = null;
    if (op.after) {
      const found = structuralStep(doc, op.after);
      if (!found.ok) return found;
      afterStep = found.step;
    }
    let between: { from: IndexedStep; to: IndexedStep } | null = null;
    if (op.insertBetween) {
      const from = structuralStep(doc, op.insertBetween.from);
      if (!from.ok) return from;
      const to = structuralStep(doc, op.insertBetween.to);
      if (!to.ok) return to;
      if (from.step.key === to.step.key) {
        return cycleError(to.step.key, from.step.key);
      }
      between = { from: from.step, to: to.step };
      afterStep = between.from;
    }
    const upstream: IndexedStep[] = [];
    for (const key of dependsOn) {
      const found = structuralStep(doc, key);
      if (!found.ok) return found;
      upstream.push(found.step);
    }

    const taken = takenNames(doc, options);
    let name: string;
    if (op.step.name !== undefined) {
      const problem = validateStepName(op.step.name);
      if (problem) return fail('invalidName', problem);
      if (taken.has(op.step.name)) {
        return fail('duplicateName', `The name "${op.step.name}" is in use.`);
      }
      name = op.step.name;
    } else {
      name =
        op.step.action !== undefined
          ? uniqueStepName(op.step.action, taken)
          : uniqueCommandName(indexSteps(doc).steps.length + 1, taken);
    }
    if (op.step.id !== undefined) {
      const problem = validateStepId(op.step.id);
      if (problem) return fail('invalidName', problem);
      if (taken.has(op.step.id)) {
        return fail('duplicateName', `The id "${op.step.id}" is in use.`);
      }
    }

    // A chain DAG has no depends: the order of the entries is the graph.
    const writesDepends = getDagType(doc) !== 'chain';
    if (between && writesDepends) upstream.unshift(between.from);
    if (between && writesDepends) {
      // The new step becomes a parent of `to`.
      const graph = downstreamGraph(doc, options);
      const loop = upstream.find((s) => reaches(graph, between.to.key, s.key));
      if (loop) return cycleError(between.to.key, loop.key);
    }
    const depends: string[] = [];
    for (const step of upstream) {
      const ref = referenceNameFor(doc, step, options);
      if (!ref.ok) return ref;
      if (!depends.includes(ref.name)) depends.push(ref.name);
    }

    const steps = ensureSteps(doc);
    if ('ok' in steps) return steps;
    // The server needs an explicit id on a human task.
    const written: NewStep =
      op.step.action === 'human.task' && op.step.id === undefined
        ? { ...op.step, id: uniqueStepName('approval', [...taken, name]) }
        : op.step;
    const entry = newEntry(doc, name, isMap(steps), written, depends);
    const spaced = usesBlankLines(steps);
    const atStart = !!op.atStart && !afterStep && steps.items.length > 0;
    const indexAfter = (at: number) =>
      atStart ? 0 : at >= 0 ? at + 1 : steps.items.length;
    // At the start, the comment above the old first step and the blank line
    // stay with that step.
    const keepWithFirst = (first: unknown): boolean => {
      if (!atStart || !isNode(first)) return false;
      if (steps.commentBefore) {
        first.commentBefore = first.commentBefore
          ? `${steps.commentBefore}\n${first.commentBefore}`
          : steps.commentBefore;
        steps.commentBefore = undefined;
      }
      if (spaced) first.spaceBefore = true;
      return true;
    };
    if (isSeq(steps)) {
      if (!keepWithFirst(steps.items[0]) && spaced) entry.spaceBefore = true;
      const at = afterStep ? steps.items.indexOf(afterStep.node) : -1;
      steps.items.splice(indexAfter(at), 0, entry);
    } else {
      const key = new Scalar(name);
      if (!keepWithFirst(steps.items[0]?.key) && spaced) {
        key.spaceBefore = true;
      }
      const pair = new Pair(key, entry);
      const at =
        afterStep?.loc.form === 'mapEntry'
          ? steps.items.indexOf(afterStep.loc.pair)
          : -1;
      steps.items.splice(indexAfter(at), 0, pair);
    }
    addRouteTarget(doc, steps, entry, name, taken, writesDepends);
    if (between && writesDepends) {
      rewireDepends(doc, between.to, referencesOf(between.from, options), name);
    }
    const added = indexSteps(doc).steps.find((s) => explicitNameOf(s) === name);
    return added ? { ok: true, select: added.key } : { ok: true };
  });
}

/**
 * A router without a route does not pass the validation, and a route needs
 * a step to go to. A new router with no route gets a route `ok` and a step
 * after it that the route leads to.
 */
function addRouteTarget(
  doc: Document,
  steps: YAMLSeq | YAMLMap,
  entry: YAMLMap,
  name: string,
  taken: Iterable<string>,
  writesDepends: boolean
): void {
  if (textOf(entry.get('action', true)) !== 'router.route') return;
  const withMap = entry.get('with', true);
  const routes = isMap(withMap) ? withMap.get('routes', true) : null;
  if (!isMap(routes) || routes.items.length > 0) return;
  const target = uniqueStepName('ok', [...taken, name]);
  const targets = doc.createNode([target]);
  targets.flow = true;
  routes.set('ok', targets);
  const next = newEntry(
    doc,
    target,
    isMap(steps),
    { run: 'echo "TODO"' },
    writesDepends ? [name] : []
  );
  if (isSeq(steps)) {
    steps.items.splice(steps.items.indexOf(entry) + 1, 0, next);
    return;
  }
  const at = steps.items.findIndex((pair) => pair.value === entry);
  steps.items.splice(at + 1, 0, new Pair(new Scalar(target), next));
}

function textOf(node: unknown): string | undefined {
  const value = isScalar(node) ? node.value : undefined;
  return typeof value === 'string' ? value : undefined;
}

/** The `with.routes` map of an `action: router.route` step, or null. */
function routesOf(step: IndexedStep): YAMLMap | null {
  const entry = step.node;
  if (
    !isMap(entry) ||
    textOf(entry.get('action', true))?.trim() !== 'router.route'
  ) {
    return null;
  }
  const withMap = entry.get('with', true);
  const routes = isMap(withMap) ? withMap.get('routes', true) : null;
  return isMap(routes) ? routes : null;
}

/** Drops the targets in `removed` from a routes map. A pattern with no target left goes too. */
function dropRouteTargets(routes: YAMLMap, removed: Set<string>): void {
  routes.items = routes.items.filter((pair) => {
    const value = pair.value;
    if (isSeq(value)) {
      value.items = value.items.filter((t) => !removed.has(textOf(t) ?? '\0'));
      return value.items.length > 0;
    }
    return !removed.has(textOf(value) ?? '\0');
  });
}

/** Removes the entry of a step from its collection. */
function removeEntry(doc: Document, step: IndexedStep): void {
  const { loc } = step;
  let owner: YAMLSeq | YAMLMap;
  let at: number;
  let flagged: unknown;
  if (loc.form === 'block') {
    owner = loc.seq;
    at = owner.items.indexOf(step.node);
    flagged = step.node;
  } else if (loc.form === 'mapEntry' || loc.form === 'handler') {
    owner = loc.map;
    at = owner.items.indexOf(loc.pair);
    flagged = loc.pair.key;
  } else {
    return;
  }
  if (at < 0) return;
  const items: unknown[] = owner.items;
  items.splice(at, 1);
  // The comment above the first entry belongs to the collection, not to the entry.
  if (at === 0) owner.commentBefore = null;
  // Do not leave a blank line above a first entry that had none.
  const next = items[at];
  const head = isPair(next) ? next.key : next;
  if (at === 0 && isNode(head) && !(isNode(flagged) && flagged.spaceBefore)) {
    head.spaceBefore = false;
  }
  if (
    loc.form === 'handler' &&
    loc.map.items.length === 0 &&
    isMap(doc.contents)
  ) {
    doc.contents.delete('handler_on');
  }
}

/** The new depends of a step that depended on a removed step. */
function withoutReferences(
  current: string[],
  removed: Set<string>,
  replacement: string[],
  itself: Set<string>
): string[] {
  const next: string[] = [];
  const push = (value: string) => {
    if (!next.includes(value) && !itself.has(value)) next.push(value);
  };
  for (const d of current) {
    if (!removed.has(d)) push(d);
    else replacement.forEach(push);
  }
  return next;
}

/**
 * Deletes a step. Each step that depended on it drops the reference. With
 * `rewire`, it depends on the deleted step's own dependencies instead. The
 * name also leaves the targets of the router steps; a pattern with no target
 * left is removed, because the server refuses a route without targets.
 */
export function opDeleteStep(
  text: string,
  op: Extract<DagEditOp, { kind: 'deleteStep' }>,
  options: ApplyEditOptions
): EditResult {
  return editDocument(text, (doc): MutateOutcome => {
    const typeError = checkDagType(doc, false, true);
    if (typeError) return typeError;
    const target = findStep(doc, op.step);
    if (!target) return fail('unknownStep', `No step has the key ${op.step}.`);
    const { form } = target.loc;
    const supported =
      form === 'handler' ||
      form === 'mapEntry' ||
      (form === 'block' && isMap(target.node));
    if (!supported) {
      return fail(
        'unsupportedForm',
        `Step ${op.step} is a shorthand or parallel-group entry. ${CONVERT_HINT}`
      );
    }

    if (form !== 'handler') {
      const removed = referencesOf(target, options);
      const own = isMap(target.node)
        ? readDepends(target.node.get('depends', true))
        : [];
      const style = detectDependsStyle(doc);
      const chain = getDagType(doc) === 'chain';
      for (const step of indexSteps(doc).steps) {
        if (step === target || !isMap(step.node)) continue;
        const routes = routesOf(step);
        if (routes) dropRouteTargets(routes, removed);
        if (chain) continue;
        const current = readDepends(step.node.get('depends', true));
        if (!current.some((d) => removed.has(d))) continue;
        const itself = referencesOf(step, options);
        const next = withoutReferences(
          current,
          removed,
          op.rewire ? own : [],
          itself
        );
        writeDepends(doc, step.node, next, style);
      }
    }
    removeEntry(doc, target);
  });
}

/**
 * Makes `step` depend on `dependsOn`. The reference uses the name of the
 * upstream step, else its id; an unnamed upstream step first gets its server
 * name. Returns `cycle` for a self-dependency or a loop through `depends` and
 * router targets, and `noop` when the dependency exists.
 */
export function opAddDependency(
  text: string,
  op: Extract<DagEditOp, { kind: 'addDependency' }>,
  options: ApplyEditOptions
): EditResult {
  return editDocument(text, (doc): MutateOutcome => {
    const typeError = checkDagType(doc, true);
    if (typeError) return typeError;
    const step = structuralStep(doc, op.step);
    if (!step.ok) return step;
    const upstream = structuralStep(doc, op.dependsOn);
    if (!upstream.ok) return upstream;
    const entry = step.step.node;
    if (!isMap(entry)) return;
    if (step.step.key === upstream.step.key) {
      return cycleError(op.step, op.dependsOn);
    }
    const current = readDepends(entry.get('depends', true));
    const refs = referencesOf(upstream.step, options);
    if (current.some((d) => refs.has(d))) {
      return fail(
        'noop',
        `Step ${op.step} already depends on ${op.dependsOn}.`
      );
    }
    const graph = downstreamGraph(doc, options);
    if (reaches(graph, step.step.key, upstream.step.key)) {
      return cycleError(op.step, op.dependsOn);
    }
    const ref = referenceNameFor(doc, upstream.step, options);
    if (!ref.ok) return ref;
    writeDepends(doc, entry, [...current, ref.name], detectDependsStyle(doc));
  });
}

/** Removes the dependency of `step` on `dependsOn`, by name or by id. Returns `noop` when absent. */
export function opRemoveDependency(
  text: string,
  op: Extract<DagEditOp, { kind: 'removeDependency' }>,
  options: ApplyEditOptions
): EditResult {
  return editDocument(text, (doc): MutateOutcome => {
    const typeError = checkDagType(doc, true);
    if (typeError) return typeError;
    const step = structuralStep(doc, op.step);
    if (!step.ok) return step;
    const upstream = structuralStep(doc, op.dependsOn);
    if (!upstream.ok) return upstream;
    const entry = step.step.node;
    if (!isMap(entry)) return;
    const current = readDepends(entry.get('depends', true));
    const refs = referencesOf(upstream.step, options);
    const remaining = current.filter((d) => !refs.has(d));
    if (remaining.length === current.length) {
      return fail(
        'noop',
        `Step ${op.step} does not depend on ${op.dependsOn}.`
      );
    }
    writeDepends(doc, entry, remaining, detectDependsStyle(doc));
  });
}

/** Sets the items of a list of names. Scalar nodes of names that stay keep their quotes and comments. */
function setNames(seq: YAMLSeq, names: string[]): void {
  const spare = new Map<string, Scalar[]>();
  for (const item of seq.items) {
    const text = textOf(item);
    if (text !== undefined && isScalar(item)) {
      spare.set(text, [...(spare.get(text) ?? []), item]);
    }
  }
  seq.items = names.map((n) => spare.get(n)?.shift() ?? new Scalar(n));
}

/** Returns the map under `key`, creating it when the key is absent or empty. */
function ensureMap(parent: YAMLMap, key: string): YAMLMap | Failure {
  const current = parent.get(key, true);
  if (isMap(current)) {
    if (current.items.length === 0) current.flow = false;
    return current;
  }
  if (current !== undefined && !(isScalar(current) && current.value === null)) {
    return fail(
      'invalidYaml',
      `The ${key} key of the router is not a mapping.`
    );
  }
  const created = new YAMLMap();
  parent.set(key, created);
  return created;
}

/**
 * Sets the targets of one pattern of the `with.routes` of an
 * `action: router.route` step. An empty list removes the pattern. A target is
 * the name or the id of a step; the YAML gets the name. A target that would
 * make a cycle is refused.
 */
export function opSetRouteTargets(
  text: string,
  op: Extract<DagEditOp, { kind: 'setRouteTargets' }>,
  options: ApplyEditOptions
): EditResult {
  return editDocument(text, (doc): MutateOutcome => {
    const typeError = checkDagType(doc, false);
    if (typeError) return typeError;
    const found = structuralStep(doc, op.step);
    if (!found.ok) return found;
    const router = found.step;
    const entry = router.node;
    if (
      !isMap(entry) ||
      textOf(entry.get('action', true))?.trim() !== 'router.route'
    ) {
      return fail(
        'unsupportedForm',
        `Step ${op.step} is not an action: router.route step.`
      );
    }
    const pattern = op.pattern.trim();
    if (pattern === '') {
      return fail('invalidYaml', 'A route pattern must not be empty.');
    }

    const byReference = new Map<string, IndexedStep>();
    for (const step of indexSteps(doc).steps) {
      for (const ref of referencesOf(step, options)) byReference.set(ref, step);
    }
    const graph = downstreamGraph(doc, options);
    // The server reads a target as a step name only: an id becomes the
    // explicit name of its step.
    const targets: string[] = [];
    for (const target of op.targets) {
      const step = byReference.get(target);
      if (step === undefined) {
        return fail('unknownStep', `No step has the name or id "${target}".`);
      }
      if (reaches(graph, step.key, router.key)) {
        return cycleError(step.key, op.step);
      }
      const name = explicitNameOf(step);
      targets.push(name !== undefined && name.trim() !== '' ? name : target);
    }

    const withMap = ensureMap(entry, 'with');
    if ('ok' in withMap) return withMap;
    const routes = ensureMap(withMap, 'routes');
    if ('ok' in routes) return routes;
    const at = routes.items.findIndex((p) => textOf(p.key)?.trim() === pattern);
    const pair = at >= 0 ? routes.items[at] : undefined;
    if (targets.length === 0) {
      if (!pair) {
        return fail('noop', `The router has no pattern "${pattern}".`);
      }
      routes.items.splice(at, 1);
      return;
    }
    const current = pair?.value;
    if (isSeq(current)) {
      setNames(current, targets);
    } else if (isScalar(current) && targets.length === 1) {
      current.value = targets[0];
    } else {
      const list = new YAMLSeq();
      list.items = targets.map((t) => new Scalar(t));
      const sibling = routes.items.map((p) => p.value).find(isSeq);
      list.flow = sibling ? sibling.flow === true : true;
      if (isScalar(current) && current.comment) list.comment = current.comment;
      if (pair) pair.value = list;
      else routes.items.push(new Pair(new Scalar(pattern), list));
    }
  });
}
