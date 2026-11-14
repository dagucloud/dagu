// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Pair, Scalar, YAMLSeq, isMap, isScalar, isSeq } from 'yaml';
import type { Document, YAMLMap } from 'yaml';
import type { DagEditOp, EditResult } from '../../contracts';
import { editDocument } from '../patch';
import type { MutateOutcome } from '../patch';
import { parseAllDocumentsAsServer } from '../serverYaml';

/** The `schedule` of a DAG, as the YAML writes it. */
export interface ScheduleView {
  /** True when the top-level `schedule` key has a value. */
  present: boolean;
  /** Start expressions that are plain strings, in the YAML order. */
  expressions: string[];
  /** Entries that are not plain strings, such as a one-off or a profile entry. */
  others: number;
}

const NO_SCHEDULE: ScheduleView = {
  present: false,
  expressions: [],
  others: 0,
};

/** Keys that the DAG writes before `schedule`, in the order of the schema docs. */
const BEFORE_SCHEDULE = [
  'name',
  'description',
  'type',
  'group',
  'labels',
  'tags',
  'schedule',
];

// The start of the map form can be one object, such as a one-off run.
function readEntries(node: unknown): Omit<ScheduleView, 'present'> {
  if (isMap(node)) return { expressions: [], others: 1 };
  if (isScalar(node)) {
    return typeof node.value === 'string' && node.value.trim() !== ''
      ? { expressions: [node.value.trim()], others: 0 }
      : { expressions: [], others: 0 };
  }
  if (!isSeq(node)) return { expressions: [], others: 0 };
  const expressions: string[] = [];
  let others = 0;
  for (const item of node.items) {
    if (isScalar(item) && typeof item.value === 'string') {
      if (item.value.trim() !== '') expressions.push(item.value.trim());
    } else {
      others++;
    }
  }
  return { expressions, others };
}

/**
 * Reads the schedule of document 0. The map form (`start`, `stop`,
 * `restart`) gives its `start` entries. Never throws.
 */
export function readSchedule(text: string): ScheduleView {
  try {
    const doc = parseAllDocumentsAsServer(text)[0];
    if (!doc || doc.errors.length > 0 || !isMap(doc.contents)) {
      return NO_SCHEDULE;
    }
    const node = doc.contents.get('schedule', true);
    if (node === undefined || node === null) return NO_SCHEDULE;
    if (isMap(node)) {
      return { present: true, ...readEntries(node.get('start', true)) };
    }
    const entries = readEntries(node);
    return {
      present: entries.expressions.length + entries.others > 0,
      ...entries,
    };
  } catch {
    return NO_SCHEDULE;
  }
}

function seqOf(doc: Document, values: unknown[]): YAMLSeq {
  const seq = doc.createNode(values) as YAMLSeq;
  seq.flow = false;
  return seq;
}

/** Keeps object entries in place when the Cron launcher edits plain strings. */
function startNode(
  doc: Document,
  expressions: string[],
  existing: unknown,
  removedIndex?: number
): unknown {
  const others = keepOthers(existing);
  if (others.length === 0) {
    return expressions.length === 1
      ? new Scalar(expressions[0])
      : seqOf(doc, expressions);
  }

  const seq = seqOf(doc, []);
  if (isMap(existing)) {
    seq.items.push(existing, ...seqOf(doc, expressions).items);
    return seq;
  }
  if (!isSeq(existing)) return seq;

  const previous = readEntries(existing).expressions;
  const removed = Math.max(0, previous.length - expressions.length);
  let firstChange = 0;
  while (
    firstChange < expressions.length &&
    previous[firstChange] === expressions[firstChange]
  ) {
    firstChange++;
  }
  let oldIndex = 0;
  let nextIndex = 0;
  for (const item of existing.items) {
    if (!isScalar(item) || typeof item.value !== 'string') {
      seq.items.push(item);
      continue;
    }
    if (item.value.trim() === '') continue;
    const index = oldIndex++;
    const removalStart = removedIndex ?? firstChange;
    if (index >= removalStart && index < removalStart + removed) continue;
    const expression = expressions[nextIndex++];
    // A stale removedIndex leaves more old strings than new expressions.
    if (expression === undefined) continue;
    seq.items.push(
      item.value.trim() === expression ? item : new Scalar(expression)
    );
  }
  seq.items.push(...seqOf(doc, expressions.slice(nextIndex)).items);
  return seq;
}

function keepOthers(node: unknown): unknown[] {
  if (isMap(node)) return [node];
  return isSeq(node)
    ? node.items.filter(
        (item) => !(isScalar(item) && typeof item.value === 'string')
      )
    : [];
}

/** Drops the plain strings of a start list. False when nothing is left. */
function clearExpressions(node: unknown): boolean {
  if (isMap(node)) return true;
  if (!isSeq(node)) return false;
  node.items = keepOthers(node);
  return node.items.length > 0;
}

function setSchedule(
  doc: Document,
  expressions: string[],
  removedIndex?: number
): MutateOutcome {
  if (!isMap(doc.contents)) {
    return {
      ok: false,
      code: 'unsupportedForm',
      message: 'The document is not a mapping.',
    };
  }
  const root: YAMLMap = doc.contents;
  const existing = root.get('schedule', true);
  if (expressions.length === 0) {
    // Entries that are not plain strings stay, and so do `stop` and `restart`.
    if (isMap(existing)) {
      if (!clearExpressions(existing.get('start', true))) {
        existing.delete('start');
      }
      if (existing.items.length === 0) root.delete('schedule');
    } else if (!clearExpressions(existing)) {
      root.delete('schedule');
    }
    return;
  }
  if (isMap(existing)) {
    // The map form: only `start` changes.
    const start = existing.get('start', true);
    existing.set('start', startNode(doc, expressions, start, removedIndex));
    return;
  }
  const value = startNode(doc, expressions, existing, removedIndex);
  if (existing !== undefined) {
    root.set('schedule', value);
    return;
  }
  // A new key goes after the header keys, before the first other key.
  const pair = new Pair(new Scalar('schedule'), value);
  let at = 0;
  root.items.forEach((item, index) => {
    const key = isScalar(item.key) ? String(item.key.value) : '';
    if (BEFORE_SCHEDULE.includes(key)) at = index + 1;
  });
  root.items.splice(at, 0, pair);
  return;
}

/**
 * Sets the start expressions of the `schedule` key. Entries that are not
 * plain strings, and the `stop` and `restart` of the map form, stay. An empty
 * list removes the key when nothing else is left. The other lines of the
 * text do not change.
 */
export function opSetSchedule(
  text: string,
  op: Extract<DagEditOp, { kind: 'setSchedule' }>
): EditResult {
  const expressions = op.expressions
    .map((expression) => expression.trim())
    .filter((expression) => expression !== '');
  return editDocument(text, (doc) =>
    setSchedule(doc, expressions, op.removedIndex)
  );
}
