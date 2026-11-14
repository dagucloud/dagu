// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { EditorCustomActionHint, JSONSchema } from '../contracts';
import { readActionRules, ruleMatches } from './actionRules';
import { flattenObjectSchema } from './flattenSchema';

const rulesCache = new WeakMap<
  JSONSchema,
  ReturnType<typeof readActionRules>
>();

function cachedRules(schema: JSONSchema) {
  let rules = rulesCache.get(schema);
  if (!rules) {
    rules = readActionRules(schema);
    rulesCache.set(schema, rules);
  }
  return rules;
}

/**
 * Gives the flat object schema of the `with` block of an action.
 *
 * A builtin action merges every step rule that matches its name: rules that
 * only add `required` keys add them to the base schema, unless the rule
 * also tests other fields. A custom action that
 * no builtin rule matches gives its `inputSchema`. The result has one
 * `properties` map and no top-level cross-field constraints, so a form can
 * render it. Gives null for `run`, for an unknown action and for an action
 * without a `with` schema.
 */
export function getActionWithSchema(
  schema: JSONSchema,
  action: string,
  customActions: EditorCustomActionHint[]
): JSONSchema | null {
  const name = action.trim();
  if (!name || name === 'run') {
    return null;
  }
  const matching = cachedRules(schema).filter(
    (rule) => rule.withSchema && ruleMatches(rule, name)
  );
  if (matching.length === 0) {
    const custom = customActions.find((hint) => hint.name.trim() === name);
    // The refs of an input schema point into that schema.
    return custom
      ? flattenObjectSchema(custom.inputSchema, custom.inputSchema)
      : null;
  }
  // A conditional rule applies to some steps only: its `required` keys
  // are not required for every step of the action.
  const parts = matching.map((rule) => {
    const part = rule.withSchema as JSONSchema;
    if (!rule.conditional) {
      return part;
    }
    const optional: JSONSchema = { ...part };
    delete optional.required;
    return optional;
  });
  const flat = flattenObjectSchema(
    schema,
    parts.length === 1 ? parts[0]! : { allOf: parts }
  );
  return flat.properties ||
    flat.required ||
    flat.additionalProperties !== undefined
    ? flat
    : null;
}

const LARGE_ACTION = /^(k8s\.run|kubernetes\.run|browser\..+|computer\..+)$/;

/** True for actions whose `with` schema is too large for a full form. */
export function isLargeActionSchema(action: string): boolean {
  return LARGE_ACTION.test(action.trim());
}

const SCALAR_TYPES = ['string', 'number', 'integer', 'boolean'];

function isScalarSchema(schema: JSONSchema): boolean {
  if (Array.isArray(schema.enum)) {
    return true;
  }
  const types = Array.isArray(schema.type) ? schema.type : [schema.type];
  if (types.length > 0 && types.every((t) => t && SCALAR_TYPES.includes(t))) {
    return true;
  }
  const branches = schema.oneOf ?? schema.anyOf;
  return (
    Array.isArray(branches) &&
    branches.length > 0 &&
    branches.every((branch) => isScalarSchema(branch))
  );
}

/**
 * Keeps the required properties and the top-level scalar properties of a
 * flat `with` schema. The form of a large action shows these fields only;
 * the YAML section edits the other fields.
 */
export function reduceToEssentialFields(withSchema: JSONSchema): JSONSchema {
  const required = new Set(withSchema.required ?? []);
  const properties: Record<string, JSONSchema> = {};
  for (const [name, property] of Object.entries(withSchema.properties ?? {})) {
    if (required.has(name) || isScalarSchema(property)) {
      properties[name] = property;
    }
  }
  return { ...withSchema, properties };
}
