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
  /** Entries that the panel cannot show, such as a one-off or a profile entry. */
  others: number;
  /** Parameter overrides of each expression, by the index of `expressions`. */
  params?: Array<Record<string, string>>;
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

/** A cron entry that the panel edits: a plain string, or `expression` with `params`. */
const ENTRY_KEYS = ['expression', 'params'];

function paramsOf(node: unknown): Record<string, string> | null {
  if (node === undefined || node === null) return {};
  if (!isMap(node)) return null;
  const params: Record<string, string> = {};
  for (const pair of node.items) {
    if (!isScalar(pair.key) || !isScalar(pair.value)) return null;
    params[String(pair.key.value)] = String(pair.value.value ?? '');
  }
  return params;
}

/** The expression of an entry the panel edits, or null for any other entry. */
function entryExpression(item: unknown): string | null {
  if (isScalar(item)) return typeof item.value === 'string' ? item.value : null;
  if (!isMap(item)) return null;
  const expression = item.get('expression', true);
  const keys = item.items.map((pair) =>
    String(isScalar(pair.key) ? pair.key.value : '')
  );
  if (
    !isScalar(expression) ||
    typeof expression.value !== 'string' ||
    expression.value.trim() === '' ||
    !keys.every((key) => ENTRY_KEYS.includes(key)) ||
    paramsOf(item.get('params', true)) === null
  ) {
    return null;
  }
  return expression.value;
}

function entryItems(node: unknown): unknown[] {
  if (isSeq(node)) return node.items;
  return node === undefined || node === null ? [] : [node];
}

function readEntries(node: unknown): Omit<ScheduleView, 'present'> {
  const expressions: string[] = [];
  const params: Array<Record<string, string>> = [];
  let others = 0;
  for (const item of entryItems(node)) {
    const expression = entryExpression(item);
    if (expression === null) {
      if (isSeq(node) || isMap(item)) others++;
      continue;
    }
    if (expression.trim() === '') continue;
    expressions.push(expression.trim());
    params.push(isMap(item) ? (paramsOf(item.get('params', true)) ?? {}) : {});
  }
  return { expressions, others, params };
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

/** Keeps the other entries in place when the Cron launcher edits its own. */
function startNode(
  doc: Document,
  expressions: string[],
  existing: unknown,
  removedIndex?: number
): unknown {
  const items = entryItems(existing);
  const others = keepOthers(existing);
  const hasMapEntry = items.some(
    (item) => isMap(item) && entryExpression(item) !== null
  );
  if (others.length === 0 && !hasMapEntry) {
    return expressions.length === 1
      ? new Scalar(expressions[0])
      : seqOf(doc, expressions);
  }

  const seq = seqOf(doc, []);
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
  for (const item of items) {
    const current = entryExpression(item);
    if (current === null) {
      seq.items.push(item);
      continue;
    }
    if (current.trim() === '') continue;
    const index = oldIndex++;
    const removalStart = removedIndex ?? firstChange;
    if (index >= removalStart && index < removalStart + removed) continue;
    const expression = expressions[nextIndex++];
    // A stale removedIndex leaves more old entries than new expressions.
    if (expression === undefined) continue;
    if (current.trim() === expression) {
      seq.items.push(item);
      continue;
    }
    if (isMap(item)) {
      item.set('expression', expression);
      seq.items.push(item);
      continue;
    }
    seq.items.push(new Scalar(expression));
  }
  seq.items.push(...seqOf(doc, expressions.slice(nextIndex)).items);
  return seq;
}

function keepOthers(node: unknown): unknown[] {
  return entryItems(node).filter((item) => entryExpression(item) === null);
}

/** Drops the entries that the panel edits from a start list. False when nothing is left. */
function clearExpressions(node: unknown): boolean {
  const kept = keepOthers(node);
  if (isSeq(node)) node.items = kept;
  return kept.length > 0;
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

function setParam(
  doc: Document,
  index: number,
  name: string,
  value: string | null
): MutateOutcome {
  const root = doc.contents;
  const schedule = isMap(root) ? root.get('schedule', true) : undefined;
  const holder = isMap(schedule) ? schedule : root;
  const key = isMap(schedule) ? 'start' : 'schedule';
  const node = isMap(holder) ? holder.get(key, true) : undefined;
  const entries = entryItems(node)
    .map((item, position) => ({ item, position }))
    .filter(({ item }) => {
      const expression = entryExpression(item);
      return expression !== null && expression.trim() !== '';
    });
  const target = entries[index];
  if (!isMap(holder) || !target) {
    return {
      ok: false,
      code: 'unsupportedForm',
      message: 'The schedule entry does not exist.',
    };
  }
  // The entries stay a list, so that an entry can become an object.
  const seq = isSeq(node) ? node : seqOf(doc, []);
  if (!isSeq(node)) {
    seq.items.push(target.item);
    holder.set(key, seq);
  }
  const position = isSeq(node) ? target.position : 0;

  const entry = isMap(target.item)
    ? target.item
    : (doc.createNode({
        expression: (entryExpression(target.item) ?? '').trim(),
      }) as YAMLMap);
  const params = entry.get('params', true);
  if (value !== null) {
    if (isMap(params)) params.set(name, value);
    else entry.set('params', doc.createNode({ [name]: value }));
  } else if (isMap(params)) {
    params.delete(name);
    if (params.items.length === 0) entry.delete('params');
  }
  // An entry without overrides goes back to its plain form.
  const plain = entry.items.length === 1 && entry.has('expression');
  seq.items[position] = plain
    ? new Scalar((entryExpression(entry) ?? '').trim())
    : entry;
  return;
}

/**
 * Sets (value) or clears (null) one parameter override of the index-th
 * start expression. A plain string becomes an object on its first override,
 * and a string again when its last override goes. Other entries and the
 * order of the entries do not change.
 */
export function opSetScheduleParam(
  text: string,
  op: Extract<DagEditOp, { kind: 'setScheduleParam' }>
): EditResult {
  return editDocument(text, (doc) =>
    setParam(doc, op.index, op.name, op.value)
  );
}
