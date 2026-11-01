// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Pair, Scalar, YAMLSeq, isMap, isNode, isScalar, isSeq } from 'yaml';
import type { Document, YAMLMap } from 'yaml';
import type { ApplyEditOptions, EditErrorCode } from '../../contracts';
import { explicitNameOf, idOf, indexSteps } from '../stepIndex';
import type { IndexedStep } from '../stepIndex';

/** How a file writes the `depends` of a step. */
export interface DependsStyle {
  /** Style of a list with two or more entries. */
  multi: 'flow' | 'block';
  /** True when a list with one entry is written as a scalar. */
  singleScalar: boolean;
}

const DEFAULT_STYLE: DependsStyle = { multi: 'flow', singleScalar: false };

function textOf(value: unknown): string | undefined {
  return typeof value === 'string' ||
    typeof value === 'number' ||
    typeof value === 'bigint' ||
    typeof value === 'boolean'
    ? String(value)
    : undefined;
}

/**
 * Reads a `depends` value (a YAML node or a plain value) as a list of names.
 * A scalar gives one name, a sequence gives its scalar items, anything else
 * gives an empty list. Like the server, numbers and booleans become text.
 */
export function readDepends(entry: unknown): string[] {
  const value = isNode(entry) && isScalar(entry) ? entry.value : entry;
  if (isSeq(entry)) {
    const out: string[] = [];
    for (const item of entry.items) {
      const text = textOf(isScalar(item) ? item.value : item);
      if (text !== undefined) out.push(text);
    }
    return out;
  }
  if (Array.isArray(entry)) {
    return entry.flatMap((item) => {
      const text = textOf(item);
      return text === undefined ? [] : [text];
    });
  }
  const text = textOf(value);
  return text === undefined ? [] : [text];
}

/**
 * Finds how the file writes `depends`: the flow or block style of the first
 * list of two or more entries (else of the first list), and whether the first
 * one-entry value is a scalar. The defaults are a flow list and no scalar.
 */
export function detectDependsStyle(doc: Document): DependsStyle {
  let multi: 'flow' | 'block' | undefined;
  let anyList: 'flow' | 'block' | undefined;
  let singleScalar: boolean | undefined;
  for (const step of indexSteps(doc).steps) {
    if (!isMap(step.node)) continue;
    const depends = step.node.get('depends', true);
    if (isSeq(depends)) {
      const kind = depends.flow ? 'flow' : 'block';
      anyList ??= kind;
      if (depends.items.length >= 2) multi ??= kind;
      if (depends.items.length === 1) singleScalar ??= false;
    } else if (isScalar(depends) && typeof depends.value === 'string') {
      singleScalar ??= true;
    }
  }
  return {
    multi: multi ?? anyList ?? DEFAULT_STYLE.multi,
    singleScalar: singleScalar ?? DEFAULT_STYLE.singleScalar,
  };
}

function pairIndex(entry: YAMLMap, key: string): number {
  return entry.items.findIndex((p) => isScalar(p.key) && p.key.value === key);
}

/** Index where a new `depends` key goes: after the last of name, id and description, else first. */
function dependsPosition(entry: YAMLMap): number {
  let at = 0;
  for (const key of ['name', 'id', 'description']) {
    const i = pairIndex(entry, key);
    if (i >= 0) at = Math.max(at, i + 1);
  }
  return at;
}

function newDependsNode(names: string[], style: DependsStyle) {
  if (names.length === 1 && style.singleScalar) return new Scalar(names[0]);
  const seq = new YAMLSeq();
  seq.items = names.map((n) => new Scalar(n));
  seq.flow = style.multi === 'flow';
  return seq;
}

/**
 * Sets the `depends` of a step entry to `names`. An empty list removes the
 * key. An existing value keeps its node and style; a new key goes after
 * `name`, `id` and `description` when present, else first, in the given style.
 */
export function writeDepends(
  _doc: Document,
  entry: YAMLMap,
  names: string[],
  style: DependsStyle
): void {
  const at = pairIndex(entry, 'depends');
  if (names.length === 0) {
    if (at >= 0) entry.items.splice(at, 1);
    return;
  }
  const current = at >= 0 ? entry.items[at]?.value : undefined;
  if (isSeq(current)) {
    // Keep the scalar nodes of the names that stay: they hold quotes and comments.
    const spare = new Map<string, Scalar[]>();
    for (const item of current.items) {
      const text = isScalar(item) ? textOf(item.value) : undefined;
      if (text !== undefined && isScalar(item)) {
        spare.set(text, [...(spare.get(text) ?? []), item]);
      }
    }
    current.items = names.map((n) => spare.get(n)?.shift() ?? new Scalar(n));
    return;
  }
  if (
    isScalar(current) &&
    typeof current.value === 'string' &&
    names.length === 1
  ) {
    if (current.value !== names[0]) current.value = names[0];
    return;
  }
  const value = newDependsNode(names, style);
  // A trailing comment of the old scalar stays on the line.
  if (isScalar(current) && current.comment) value.comment = current.comment;
  if (at >= 0) {
    (entry.items[at] as Pair).value = value;
  } else {
    entry.items.splice(
      dependsPosition(entry),
      0,
      new Pair(new Scalar('depends'), value)
    );
  }
}

/** Result of `referenceNameFor`. */
export type ReferenceName =
  | { ok: true; name: string }
  | { ok: false; code: EditErrorCode; message: string };

/**
 * Returns the text that `depends` uses to name a step: the explicit name, else
 * the id. A step with neither gets `name: <server name>` as its first key,
 * when `options.names` knows the server name; else the result is `invalidName`.
 * A shorthand, parallel-group or handler entry gives `unsupportedForm`.
 */
export function referenceNameFor(
  _doc: Document,
  step: IndexedStep,
  options: ApplyEditOptions
): ReferenceName {
  const { form } = step.loc;
  if (form !== 'block' && form !== 'mapEntry') {
    return {
      ok: false,
      code: 'unsupportedForm',
      message: `Step ${step.key} is a shorthand, parallel-group or handler entry and cannot be referenced. Use convertStepsToExplicitForm first.`,
    };
  }
  const entry = step.node;
  if (!isMap(entry)) {
    return {
      ok: false,
      code: 'unsupportedForm',
      message: `Step ${step.key} has no mapping entry.`,
    };
  }
  const name = explicitNameOf(step);
  if (name !== undefined && name.trim() !== '') return { ok: true, name };
  const id = idOf(step);
  if (id !== undefined && id.trim() !== '') return { ok: true, name: id };
  const server = options.names?.byKey[step.key];
  if (server === undefined || server.trim() === '') {
    return {
      ok: false,
      code: 'invalidName',
      message: `Step ${step.key} has no name or id and its server name is unknown. Give it a name first.`,
    };
  }
  const at = pairIndex(entry, 'name');
  if (at >= 0) (entry.items[at] as Pair).value = new Scalar(server);
  else entry.items.unshift(new Pair(new Scalar('name'), new Scalar(server)));
  return { ok: true, name: server };
}
