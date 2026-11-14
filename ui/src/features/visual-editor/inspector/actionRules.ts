// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { JSONSchema } from '../contracts';

/**
 * One `if action ... then with ...` rule of `definitions.step.allOf`.
 * `names` comes from `const` or `enum`, `pattern` from `pattern`.
 * `withSchema` is absent when the rule sets no `with` schema.
 * `conditional` is true when the `if` also tests other fields, so the rule
 * applies to some steps of the action only.
 */
export interface ActionRule {
  names: string[];
  pattern?: RegExp;
  withSchema?: JSONSchema;
  conditional: boolean;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/**
 * Resolves a local `$ref` (`#/a/b`) against the root schema. The
 * dereferenced editor schema keeps a `$ref` only where a cycle starts.
 */
export function resolveSchemaRef(
  root: JSONSchema,
  node: JSONSchema
): JSONSchema {
  let current: JSONSchema = node;
  const seen = new Set<string>();
  while (typeof current.$ref === 'string' && current.$ref.startsWith('#/')) {
    const ref = current.$ref;
    if (seen.has(ref)) {
      return current;
    }
    seen.add(ref);
    let target: unknown = root;
    for (const raw of ref.slice(2).split('/')) {
      const segment = raw.replace(/~1/g, '/').replace(/~0/g, '~');
      target = isRecord(target) ? target[segment] : undefined;
    }
    if (!isRecord(target)) {
      return current;
    }
    const rest: JSONSchema = { ...current };
    delete rest.$ref;
    current = { ...(target as JSONSchema), ...rest };
  }
  return current;
}

/** The step schema (`definitions.step`), or null when the schema has none. */
export function stepSchemaOf(schema: JSONSchema): JSONSchema | null {
  const step = schema.definitions?.step;
  return isRecord(step) ? resolveSchemaRef(schema, step) : null;
}

function toPattern(value: unknown): RegExp | undefined {
  if (typeof value !== 'string') {
    return undefined;
  }
  try {
    return new RegExp(value);
  } catch {
    return undefined;
  }
}

/** Reads the action rules of `definitions.step.allOf` in schema order. */
export function readActionRules(schema: JSONSchema): ActionRule[] {
  const step = stepSchemaOf(schema);
  const rules: ActionRule[] = [];
  for (const raw of step?.allOf ?? []) {
    const rule = resolveSchemaRef(schema, raw);
    const condition = rule.if?.properties?.action;
    if (!isRecord(condition)) {
      continue;
    }
    const names: string[] = [];
    if (typeof condition.const === 'string') {
      names.push(condition.const);
    }
    if (Array.isArray(condition.enum)) {
      names.push(
        ...condition.enum.filter((n): n is string => typeof n === 'string')
      );
    }
    const pattern = toPattern(condition.pattern);
    if (names.length === 0 && !pattern) {
      continue;
    }
    const withNode = rule.then?.properties?.with;
    const ifKeys = Object.keys(rule.if ?? {}).filter(
      (key) => key !== 'properties' && key !== 'required'
    );
    const conditional =
      ifKeys.length > 0 ||
      Object.keys(rule.if?.properties ?? {}).some((key) => key !== 'action') ||
      (rule.if?.required ?? []).some((key) => key !== 'action');
    rules.push({
      names,
      pattern,
      withSchema: isRecord(withNode)
        ? resolveSchemaRef(schema, withNode)
        : undefined,
      conditional,
    });
  }
  return rules;
}

/** True when the rule applies to the action name. */
export function ruleMatches(rule: ActionRule, action: string): boolean {
  return rule.names.includes(action) || (rule.pattern?.test(action) ?? false);
}

/**
 * Builtin action names: the `enum` members of the `action` property of the
 * step schema, in schema order and without duplicates.
 */
export function builtinActionNames(schema: JSONSchema): string[] {
  const step = stepSchemaOf(schema);
  const action = step?.properties?.action;
  if (!action) {
    return [];
  }
  const names = new Set<string>();
  const visit = (node: JSONSchema, depth: number) => {
    const resolved = resolveSchemaRef(schema, node);
    for (const value of resolved.enum ?? []) {
      if (typeof value === 'string') {
        names.add(value);
      }
    }
    if (depth > 3) {
      return;
    }
    for (const branch of [
      ...(resolved.anyOf ?? []),
      ...(resolved.oneOf ?? []),
    ]) {
      visit(branch, depth + 1);
    }
  };
  visit(action, 0);
  return [...names];
}
