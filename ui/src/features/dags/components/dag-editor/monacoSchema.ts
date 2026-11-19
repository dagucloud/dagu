// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { JSONSchema } from '@/lib/schema-utils';

/*
 * monaco-yaml 5.4 reads a YAML boolean as its source text. For the `const`
 * and `enum` checks, `true` is then the string "true". A rule such as
 * { "const": true } never holds, and the editor flags `read: true` with
 * "Value must be true". The schema is correct: the Go validation and the
 * client checks accept the value. So the editor gets a copy that its language
 * service reads correctly. The schema file does not change.
 */

/** The spellings of the two booleans in YAML 1.2 (the core schema). */
const SOURCES = {
  true: ['true', 'True', 'TRUE'],
  false: ['false', 'False', 'FALSE'],
} as const;

// Keywords whose value is data, not a schema: a boolean inside them stays.
const DATA_KEYWORDS = new Set(['const', 'enum', 'default', 'examples']);
// Keywords whose value maps a name to a schema.
const NAMED_KEYWORDS = new Set([
  'properties',
  'patternProperties',
  'definitions',
  '$defs',
]);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/**
 * A boolean and its YAML texts. The language service reads a boolean as its
 * text. The boolean stays in the list, so the rule holds if this changes.
 */
function spellings(value: boolean): (string | boolean)[] {
  return [value, ...(value ? SOURCES.true : SOURCES.false)];
}

/**
 * The rule "the value is `value`" for a boolean. It has the type, and a
 * condition that the other boolean meets. When the condition is met, a `then`
 * fails. The `then` has an empty `enum`, so no value meets it. Its
 * `errorMessage` is the text of the marker, and it names the key when the
 * rule has one. The condition lists `const` rules, not an `enum`, because the
 * hover shows an `enum` as the allowed values.
 */
function booleanRule(
  value: boolean,
  key: string | undefined
): Record<string, unknown> {
  const errorMessage =
    key === undefined ? `Value must be ${value}.` : `${key} must be ${value}.`;
  return {
    type: 'boolean',
    errorMessage,
    if: { anyOf: spellings(!value).map((spelling) => ({ const: spelling })) },
    then: { enum: [], errorMessage },
  };
}

/** The values of an `enum`, where each boolean also stands as its YAML texts. */
function expandedEnum(values: unknown[]): unknown[] {
  const expanded: unknown[] = [];
  for (const value of values) {
    for (const item of typeof value === 'boolean'
      ? spellings(value)
      : [value]) {
      if (!expanded.includes(item)) expanded.push(item);
    }
  }
  return expanded;
}

/** What a walk keeps: the results by node, and the nodes that it is in. */
interface Walk {
  done: Map<object, unknown>;
  open: Set<object>;
}

function convertNode(
  node: unknown,
  key: string | undefined,
  walk: Walk
): unknown {
  if (Array.isArray(node)) {
    let changed = false;
    const items = node.map((item) => {
      const next = convertNode(item, undefined, walk);
      changed ||= next !== item;
      return next;
    });
    return changed ? items : node;
  }
  if (!isRecord(node)) return node;
  // The text of a rule names its key: such a node has a result for each key.
  // The result of any other node does not depend on the key, so it is shared.
  const shared = key === undefined || typeof node.const !== 'boolean';
  if (shared && walk.done.has(node)) return walk.done.get(node);
  // A node that holds itself stays as it is where it comes back.
  if (walk.open.has(node)) return node;
  walk.open.add(node);

  let out: Record<string, unknown> = node;
  const set = (name: string, value: unknown) => {
    if (out === node) out = { ...node };
    out[name] = value;
  };
  for (const [name, child] of Object.entries(node)) {
    if (DATA_KEYWORDS.has(name)) continue;
    if (NAMED_KEYWORDS.has(name) && isRecord(child)) {
      let named = child;
      for (const [entry, schema] of Object.entries(child)) {
        const next = convertNode(schema, entry, walk);
        if (next === schema) continue;
        if (named === child) named = { ...child };
        named[entry] = next;
      }
      if (named !== child) set(name, named);
      continue;
    }
    const next = convertNode(child, undefined, walk);
    if (next !== child) set(name, next);
  }

  if (typeof node.const === 'boolean') {
    const { const: value, ...rest } = out;
    const allOf = Array.isArray(rest.allOf) ? rest.allOf : [];
    out = { ...rest, allOf: [...allOf, booleanRule(value as boolean, key)] };
  } else if (
    Array.isArray(node.enum) &&
    node.enum.some((item) => typeof item === 'boolean')
  ) {
    set('enum', expandedEnum(node.enum));
  }

  walk.open.delete(node);
  if (shared) walk.done.set(node, out);
  return out;
}

const converted = new WeakMap<object, JSONSchema>();

/**
 * The schema in a form that monaco-yaml reads without a false mismatch. A
 * `const` with a boolean becomes a rule on the type and on the other boolean.
 * Each boolean of an `enum` also stands as its YAML texts. The message of the
 * rule of a property is `<key> must be <value>.`. The input does not change.
 * A part with no boolean `const` or `enum` is not copied, so a schema without
 * them comes back as the same object.
 */
export function forMonacoYaml(schema: JSONSchema): JSONSchema {
  let result = converted.get(schema);
  if (result === undefined) {
    result = convertNode(schema, undefined, {
      done: new Map(),
      open: new Set(),
    }) as JSONSchema;
    converted.set(schema, result);
  }
  return result;
}
