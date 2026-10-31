// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Pair, Scalar, YAMLMap, isMap, isNode, isScalar, isSeq } from 'yaml';
import type { Document, YAMLSeq } from 'yaml';
import type {
  ApplyEditOptions,
  DagEditOp,
  EditErrorCode,
  EditResult,
  StepKey,
} from '../../contracts';
import { editDocument } from '../patch';
import type { MutateOutcome } from '../patch';
import {
  explicitNameOf,
  getDagType,
  getStepsNode,
  idOf,
  indexSteps,
  isAliasStep,
} from '../stepIndex';
import type { IndexedStep } from '../stepIndex';
import {
  detectDependsStyle,
  readDepends,
  referenceNameFor,
  writeDepends,
} from './depends';

const NOOP = {
  ok: false,
  code: 'noop',
  message: 'All steps are in explicit form already.',
} as const;

/**
 * Converts the steps to explicit form without a change of meaning: a shorthand
 * string becomes a mapping with `run:`, and the items of a parallel group of
 * a graph DAG move into the top-level list. A `type: chain` DAG becomes a
 * `type: graph` DAG with the order written as `depends`. A shorthand step keeps its server name
 * from `options.names`, so that its run history stays. Returns `agentType` for
 * a `type: agent` DAG and `noop` when nothing needs conversion.
 */
export function opConvertStepsToExplicitForm(
  text: string,
  _op: Extract<DagEditOp, { kind: 'convertStepsToExplicitForm' }>,
  options: ApplyEditOptions
): EditResult {
  return editDocument(text, (doc) => convert(doc, options));
}

function convert(doc: Document, options: ApplyEditOptions): MutateOutcome {
  const type = getDagType(doc);
  if (type === 'agent') {
    return {
      ok: false,
      code: 'agentType',
      message: 'Steps of a type "agent" DAG cannot be converted.',
    };
  }
  const chain = type === 'chain';
  const steps = getStepsNode(doc);
  let changed = chain;
  if (isSeq(steps)) {
    for (const step of indexSteps(doc).steps) {
      if (isScalar(step.node) && convertShorthand(doc, step, options)) {
        changed = true;
      }
    }
    const hasGroup = steps.items.some((item) => isSeq(item));
    if (hasGroup) pinGeneratedNames(doc, steps, options);
    const positions = chain ? positionsOf(doc, steps) : [];
    // A group of a graph DAG adds no dependency, so its items can move into
    // the top-level list. A chain DAG gets the dependencies of its groups.
    if (hasGroup) {
      flattenGroups(steps);
      changed = true;
    }
    const failure = chain && addChainDepends(doc, positions, options);
    if (failure) return failure;
  }
  if (chain) setGraphType(doc);
  return changed ? undefined : NOOP;
}

/** StepKeys of each position of the top-level list; a group is one position. */
function positionsOf(doc: Document, steps: YAMLSeq): StepKey[][] {
  const bySeqIndex = new Map<number, StepKey[]>();
  for (const { key, loc } of indexSteps(doc).steps) {
    if (loc.form === 'mapEntry' || loc.form === 'handler') continue;
    bySeqIndex.set(loc.seqIndex, [
      ...(bySeqIndex.get(loc.seqIndex) ?? []),
      key,
    ]);
  }
  const positions: StepKey[][] = [];
  steps.items.forEach((item, i) => {
    const keys = bySeqIndex.get(i) ?? (isSeq(item) ? [] : undefined);
    if (keys) positions.push(keys);
  });
  return positions;
}

/**
 * Makes the order of a chain explicit. As in `injectChainDependencies`
 * (internal/spec/builder.go), each step depends on all steps of the previous
 * position. The steps of the first position get none. A step that others
 * depend on needs a name or an id, else the result is `invalidName`.
 */
function addChainDepends(
  doc: Document,
  positions: StepKey[][],
  options: ApplyEditOptions
): { ok: false; code: EditErrorCode; message: string } | undefined {
  const byKey = new Map(indexSteps(doc).steps.map((s) => [s.key, s]));
  const style = detectDependsStyle(doc);
  for (let p = 1; p < positions.length; p++) {
    const previous: string[] = [];
    for (const key of positions[p - 1] ?? []) {
      const step = byKey.get(key);
      if (!step) continue;
      const ref = referenceNameFor(doc, step, options);
      if (!ref.ok) return ref;
      previous.push(ref.name);
    }
    for (const key of positions[p] ?? []) {
      const step = byKey.get(key);
      if (step && isAliasStep(step)) {
        return {
          ok: false,
          code: 'unsupportedForm',
          message: `Step ${key} is an alias and cannot get depends. Replace the alias by a mapping first.`,
        };
      }
      const entry = step?.node;
      if (!isMap(entry)) continue;
      const existing = readDepends(entry.get('depends', true));
      const missing = previous.filter((name) => !existing.includes(name));
      writeDepends(doc, entry, [...existing, ...missing], style);
    }
  }
  return undefined;
}

/** Changes the value of `type:` in place, so that quotes and comments stay. */
function setGraphType(doc: Document): void {
  if (!isMap(doc.contents)) return;
  const node = doc.contents.get('type', true);
  if (isScalar(node)) node.value = 'graph';
  else doc.contents.set('type', 'graph');
}

/**
 * The server names an unnamed step from its position in the top-level list
 * (`generateTypedStepName` in internal/spec/builder.go). Flattening moves the
 * position of each step from the first group on, so each of them that has a
 * known server name gets it as `name:`. Without a server name the step stays.
 */
function pinGeneratedNames(
  doc: Document,
  steps: YAMLSeq,
  options: ApplyEditOptions
): void {
  const first = steps.items.findIndex((item) => isSeq(item));
  for (const step of indexSteps(doc).steps) {
    const { loc } = step;
    if (
      (loc.form === 'block' || loc.form === 'parallelGroup') &&
      loc.seqIndex >= first
    ) {
      pinName(step, options);
    }
  }
}

function pinName(step: IndexedStep, options: ApplyEditOptions): void {
  const { node } = step;
  const server = options.names?.byKey[step.key];
  if (!isMap(node) || !server) return;
  if (explicitNameOf(step) !== undefined || idOf(step) !== undefined) return;
  const at = node.items.findIndex(
    (p) => isScalar(p.key) && p.key.value === 'name'
  );
  if (at >= 0) (node.items[at] as Pair).value = new Scalar(server);
  else node.items.unshift(new Pair(new Scalar('name'), new Scalar(server)));
}

/**
 * Moves the items of each nested sequence into the top-level sequence, at the
 * position of the group, in order. The comment above a group moves above its
 * first item.
 */
function flattenGroups(seq: YAMLSeq): void {
  const items: unknown[] = [];
  let pending: { comment?: string; space?: boolean } = {};
  for (const item of seq.items) {
    if (!isSeq(item)) {
      items.push(item);
      continue;
    }
    pending = {
      comment: join(pending.comment, item.commentBefore),
      space: pending.space || item.spaceBefore,
    };
    for (const entry of item.items) {
      if (isNode(entry)) {
        entry.commentBefore = join(pending.comment, entry.commentBefore);
        if (pending.space) entry.spaceBefore = true;
        pending = {};
      }
      items.push(entry);
    }
  }
  seq.items = items;
}

function join(a: string | null | undefined, b: string | null | undefined) {
  if (a == null) return b ?? undefined;
  return b == null ? a : `${a}\n${b}`;
}

/** Replaces the string entry of a sequence by `{ [name:] run: <string> }`. */
function convertShorthand(
  doc: Document,
  step: IndexedStep,
  options: ApplyEditOptions
): boolean {
  const { loc, node } = step;
  if (!isScalar(node)) return false;
  let items: unknown[];
  let at: number;
  if (loc.form === 'shorthand') {
    items = loc.seq.items;
    at = loc.seqIndex;
  } else if (loc.form === 'parallelGroup') {
    items = loc.group.items;
    at = loc.groupIndex;
  } else {
    return false;
  }
  const map = new YAMLMap<Scalar, unknown>(doc.schema);
  const name = options.names?.byKey[step.key];
  if (name !== undefined)
    map.items.push(new Pair(new Scalar('name'), new Scalar(name)));
  // The scalar keeps its quote style and its end-of-line comment.
  const { commentBefore, spaceBefore } = node;
  node.commentBefore = undefined;
  node.spaceBefore = undefined;
  map.items.push(new Pair(new Scalar('run'), node));
  if (commentBefore !== undefined) map.commentBefore = commentBefore;
  if (spaceBefore) map.spaceBefore = true;
  items[at] = map;
  return true;
}
