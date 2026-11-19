// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Pair, Scalar, YAMLSeq, isMap, isScalar, isSeq } from 'yaml';
import type { Document, YAMLMap } from 'yaml';
import { ScheduleKind, type components } from '@/api/v1/schema';
import type { DagEditOp, EditResult } from '../../contracts';
import { editDocument } from '../patch';
import type { MutateOutcome } from '../patch';
import { parseAllDocumentsAsServer } from '../serverYaml';

type Schedule = components['schemas']['Schedule'];

/** One start entry of the schedule. */
export interface ScheduleEntryView {
  /** Index in `expressions` of an entry that the panel edits as cron. */
  expressionIndex?: number;
  /** The entry, in the fields of the server's schedule. */
  schedule: Schedule;
  /** Parameter overrides, or null when one of them is not a plain value. */
  params: Record<string, string> | null;
}

/** The `schedule` of a DAG, as the YAML writes it. */
export interface ScheduleView {
  /** True when the top-level `schedule` key has a value. */
  present: boolean;
  /** Start expressions of the entries that the panel edits, in the YAML order. */
  expressions: string[];
  /** Entries that the panel does not edit as cron, such as a one-off or a profile entry. */
  others: number;
  /** Every start entry, in the YAML order, which is the server's numbering. */
  entries: ScheduleEntryView[];
}

const NO_SCHEDULE: ScheduleView = {
  present: false,
  expressions: [],
  others: 0,
  entries: [],
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

/** The start entries that the server numbers, with their positions in the list. */
function startItems(node: unknown): Array<{ item: unknown; position: number }> {
  return entryItems(node).flatMap((item, position) => {
    const expression = entryExpression(item);
    const counted =
      expression === null
        ? isSeq(node) || isMap(item)
        : expression.trim() !== '';
    return counted ? [{ item, position }] : [];
  });
}

/** An entry the panel does not edit as cron, in the fields of the server's schedule. */
function scheduleOf(item: unknown): Schedule {
  if (!isMap(item)) {
    return { expression: isScalar(item) ? String(item.value ?? '') : '' };
  }
  const text = (key: string): string | undefined => {
    const node = item.get(key, true);
    return isScalar(node) && node.value !== null
      ? String(node.value)
      : undefined;
  };
  const schedule: Schedule = {
    kind:
      text('kind') === ScheduleKind.at || text('at') !== undefined
        ? ScheduleKind.at
        : ScheduleKind.cron,
    expression: text('expression') ?? '',
  };
  const at = text('at');
  if (at !== undefined) schedule.at = at;
  const calendar = text('calendar');
  if (calendar !== undefined) schedule.calendar = calendar;
  const profile = text('profile');
  if (profile !== undefined) schedule.profile = profile;
  const days = item.get('business_days', true);
  if (isSeq(days)) {
    schedule.businessDays = days.items.flatMap((day) =>
      isScalar(day) && typeof day.value === 'number' ? [day.value] : []
    );
  }
  return schedule;
}

/** `schedule[1].params`, `schedule.start.params.mode` and the like. */
const SCHEDULE_PARAMS_PATH =
  /^schedule(?:\.start)?(?:\[(\d+)\])?\.params(?:$|[.[])/;

/**
 * The index of the start entry, numbered as `ScheduleView.entries`, whose
 * overrides a server issue path names, or undefined for another path.
 */
export function scheduleParamIssueIndex(fieldPath: string): number | undefined {
  const match = SCHEDULE_PARAMS_PATH.exec(fieldPath);
  if (!match) return undefined;
  return match[1] === undefined ? 0 : Number(match[1]);
}

function readEntries(node: unknown): Omit<ScheduleView, 'present'> {
  const expressions: string[] = [];
  const entries: ScheduleEntryView[] = [];
  for (const { item } of startItems(node)) {
    const expression = entryExpression(item);
    const params = isMap(item) ? paramsOf(item.get('params', true)) : {};
    if (expression === null) {
      entries.push({
        schedule: scheduleOf(item),
        params: isMap(item) ? params : null,
      });
      continue;
    }
    entries.push({
      expressionIndex: expressions.length,
      schedule: { kind: ScheduleKind.cron, expression: expression.trim() },
      params: params ?? {},
    });
    expressions.push(expression.trim());
  }
  return { expressions, others: entries.length - expressions.length, entries };
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
    // Entries that the panel does not edit stay, and so do `stop` and `restart`.
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
 * Sets the start expressions of the `schedule` key. Plain strings and
 * `expression` entries with `params` take the new expressions, and an entry
 * keeps its params while it stays. Other entries, and the `stop` and
 * `restart` of the map form, stay. An empty list removes the key when nothing
 * else is left. The other lines of the text do not change.
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
  value: string | number | boolean | null
): MutateOutcome {
  const root = doc.contents;
  const schedule = isMap(root) ? root.get('schedule', true) : undefined;
  const holder = isMap(schedule) ? schedule : root;
  const key = isMap(schedule) ? 'start' : 'schedule';
  const node = isMap(holder) ? holder.get(key, true) : undefined;
  const target = startItems(node)[index];
  if (
    !isMap(holder) ||
    !target ||
    (!isMap(target.item) && entryExpression(target.item) === null)
  ) {
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
 * start entry, numbered as `ScheduleView.entries`. A plain string becomes an
 * object on its first override, and a string again when its last override
 * goes. A number or boolean is written as a YAML number or boolean. Other
 * entries and the order of the entries do not change.
 */
export function opSetScheduleParam(
  text: string,
  op: Extract<DagEditOp, { kind: 'setScheduleParam' }>
): EditResult {
  return editDocument(text, (doc) =>
    setParam(doc, op.index, op.name, op.value)
  );
}
