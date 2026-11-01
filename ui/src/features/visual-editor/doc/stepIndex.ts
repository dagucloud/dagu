// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { isAlias, isMap, isNode, isScalar, isSeq } from 'yaml';
import type { Document, Node, Pair, Scalar, YAMLMap, YAMLSeq } from 'yaml';
import type { DagType, HandlerEvent, StepKey } from '../contracts';

/** Where the entry of a step lives in the YAML tree. */
export type StepLocator =
  | { form: 'block' | 'shorthand'; seq: YAMLSeq; seqIndex: number } // entry = seq.items[seqIndex]
  | {
      form: 'parallelGroup';
      seq: YAMLSeq; // top-level steps seq
      seqIndex: number;
      group: YAMLSeq;
      groupIndex: number;
      groupNumber: number;
    } // entry = group.items[groupIndex]
  | { form: 'mapEntry'; map: YAMLMap; pair: Pair } // map = steps map; entry = pair.value
  | { form: 'handler'; map: YAMLMap; pair: Pair; event: HandlerEvent }; // map = handler_on map

/** One step of document 0, with the key that the server gives it. */
export interface IndexedStep {
  key: StepKey; // "steps[3]" or "handler_on.failure"
  index: number | null; // null for handlers
  handler?: HandlerEvent;
  loc: StepLocator;
  node: Node | null; // the step entry: a YAMLMap, a Scalar for shorthand, an Alias; null if empty
  /** For an alias entry: the node that the alias refers to. `node` stays the Alias. */
  target?: Node;
}

/** True for an entry that is an alias (`- *a`): it counts as a step but ops must not edit it. */
export function isAliasStep(step: IndexedStep): boolean {
  return isAlias(step.node);
}

/** Returns the `steps` collection of document 0, or null when it is absent or a scalar. */
export function getStepsNode(doc: Document): YAMLSeq | YAMLMap | null {
  if (!isMap(doc.contents)) return null;
  const steps = doc.contents.get('steps', true);
  return isSeq(steps) || isMap(steps) ? steps : null;
}

const DAG_TYPES: readonly DagType[] = ['graph', 'chain', 'agent', 'build'];

/**
 * Returns the DAG type. Follows `canonicalDAGType` in `internal/spec/dag.go`:
 * the value is trimmed and the legacy name "controller" means "agent".
 * An absent or unknown value gives "graph".
 */
export function getDagType(doc: Document): DagType {
  if (!isMap(doc.contents)) return 'graph';
  const raw = doc.contents.get('type');
  if (typeof raw !== 'string') return 'graph';
  const type = raw.trim() === 'controller' ? 'agent' : raw.trim();
  return DAG_TYPES.find((t) => t === type) ?? 'graph';
}

/**
 * Lists the steps of document 0 in the order of the server.
 * `steps[i]` is the i-th step after the server has built the list.
 */
export function indexSteps(doc: Document): {
  steps: IndexedStep[];
  handlers: IndexedStep[];
} {
  const steps: IndexedStep[] = [];
  const node = getStepsNode(doc);
  if (isSeq(node)) {
    // `buildStepsFromArray` and `stepGroupFromRaw` in internal/spec/dag.go:
    // a nested array adds each of its items, in order, at its position.
    // `normalizeStepData` in internal/spec/builder.go turns a string into a step.
    let groupNumber = 0;
    node.items.forEach((item, seqIndex) => {
      if (isSeq(item)) {
        const number = groupNumber++;
        item.items.forEach((entry, groupIndex) => {
          const found = entryOf(doc, entry);
          if (!found) return;
          steps.push({
            key: stepKey(steps.length),
            index: steps.length,
            loc: {
              form: 'parallelGroup',
              seq: node,
              seqIndex,
              group: item,
              groupIndex,
              groupNumber: number,
            },
            ...found,
          });
        });
        return;
      }
      const found = entryOf(doc, item);
      if (!found) return;
      steps.push({
        key: stepKey(steps.length),
        index: steps.length,
        loc: {
          form: isShorthand(item) ? 'shorthand' : 'block',
          seq: node,
          seqIndex,
        },
        ...found,
      });
    });
  } else if (isMap(node)) {
    // `buildStepsFromMap` in internal/spec/dag.go sorts steps by name (byte
    // order), so `steps[i]` is the i-th key in that order, not in file order.
    const entries = node.items
      .map((pair) => ({ pair, name: pairKeyText(pair) }))
      .filter((e): e is { pair: Pair; name: string } => e.name !== undefined)
      .sort((a, b) => compareBytes(a.name, b.name));
    for (const { pair } of entries) {
      steps.push({
        key: stepKey(steps.length),
        index: steps.length,
        loc: { form: 'mapEntry', map: node, pair },
        ...entryFields(doc, pair.value),
      });
    }
  }
  return { steps, handlers: indexHandlers(doc) };
}

/**
 * The step entry of one item of the list: a mapping, a string or an alias of
 * one of them. An alias to a sequence is not indexed.
 */
function entryOf(
  doc: Document,
  item: unknown
): { node: Node; target?: Node } | null {
  if (isMap(item) || isShorthand(item)) return { node: item };
  if (!isAlias(item)) return null;
  const target = item.resolve(doc);
  return isMap(target) || isShorthand(target) ? { node: item, target } : null;
}

const HANDLER_EVENTS: readonly HandlerEvent[] = [
  'init',
  'success',
  'failure',
  'abort',
  'exit',
  'wait',
];

/** Lists the `handler_on` entries with a known event, in file order. */
function indexHandlers(doc: Document): IndexedStep[] {
  if (!isMap(doc.contents)) return [];
  const map = doc.contents.get('handler_on', true);
  if (!isMap(map)) return [];
  const handlers: IndexedStep[] = [];
  for (const pair of map.items) {
    const event = HANDLER_EVENTS.find((e) => e === scalarText(pair.key));
    if (!event) continue;
    handlers.push({
      key: `handler_on.${event}`,
      index: null,
      handler: event,
      loc: { form: 'handler', map, pair, event },
      ...entryFields(doc, pair.value),
    });
  }
  return handlers;
}

const encoder = new TextEncoder();

/** Compares two strings by UTF-8 bytes, like Go `strings.Compare`. */
function compareBytes(a: string, b: string): number {
  const x = encoder.encode(a);
  const y = encoder.encode(b);
  const n = Math.min(x.length, y.length);
  for (let i = 0; i < n; i++) {
    const d = (x[i] ?? 0) - (y[i] ?? 0);
    if (d !== 0) return d;
  }
  return x.length - y.length;
}

function pairKeyText(pair: Pair): string | undefined {
  return scalarText(pair.key);
}

function entryFields(
  doc: Document,
  value: unknown
): { node: Node | null; target?: Node } {
  if (isAlias(value)) {
    const target = value.resolve(doc);
    return target ? { node: value, target } : { node: value };
  }
  const node =
    isNode(value) && !(isScalar(value) && value.value === null) ? value : null;
  return { node };
}

function isShorthand(node: unknown): node is Scalar<string> {
  return isScalar(node) && typeof node.value === 'string';
}

function stepKey(index: number): StepKey {
  return `steps[${index}]`;
}

/** Finds one step or handler by key. Returns null when no step has the key. */
export function findStep(doc: Document, key: StepKey): IndexedStep | null {
  const { steps, handlers } = indexSteps(doc);
  return (
    steps.find((s) => s.key === key) ??
    handlers.find((s) => s.key === key) ??
    null
  );
}

function scalarText(node: unknown): string | undefined {
  if (!isScalar(node)) return undefined;
  const v = node.value;
  return typeof v === 'string' ||
    typeof v === 'number' ||
    typeof v === 'bigint' ||
    typeof v === 'boolean'
    ? String(v)
    : undefined;
}

function fieldOf(step: IndexedStep, field: string): string | undefined {
  const entry = step.target ?? step.node;
  return isMap(entry) ? scalarText(entry.get(field, true)) : undefined;
}

/**
 * Returns the `name:` value of the step, or undefined. In map form the key is
 * the name: the server forces it over a `name:` value. The value is trimmed,
 * as the server trims it (`buildStepName` in internal/spec/step.go).
 */
export function explicitNameOf(step: IndexedStep): string | undefined {
  const name =
    step.loc.form === 'mapEntry'
      ? pairKeyText(step.loc.pair)
      : fieldOf(step, 'name');
  return name?.trim();
}

/** Returns the trimmed `id:` value of the step, or undefined. */
export function idOf(step: IndexedStep): string | undefined {
  return fieldOf(step, 'id')?.trim();
}
