// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ErrorSchema } from '@rjsf/utils';
import type { JSONSchema, ValidationIssue } from '../contracts';
import { actionDescription } from './actionText';

type Plain = Record<string, unknown>;

export function isPlainObject(value: unknown): value is Plain {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/**
 * Splits the `with` value of a step into the keys that the form edits and
 * the keys that it shows read-only. `known` lists the properties of the
 * full schema, `shown` those of the form schema. Keys of the full schema
 * that the form hides (large actions) go to neither list: the form never
 * touches them.
 */
export function splitWithValue(
  value: unknown,
  known: string[],
  shown: string[]
): { formData: Plain; other: [string, unknown][] } {
  // No prototype: a field named `__proto__` stays a key of its own.
  const formData: Plain = Object.create(null);
  const other: [string, unknown][] = [];
  if (!isPlainObject(value)) {
    return { formData, other };
  }
  for (const [key, item] of Object.entries(value)) {
    if (shown.includes(key)) {
      formData[key] = item;
    } else if (!known.includes(key)) {
      other.push([key, item]);
    }
  }
  return { formData, other };
}

/** The schema given to rjsf: no extra-key editor, only the `keep` properties when set. */
export function formSchemaOf(schema: JSONSchema, keep?: string[]): JSONSchema {
  const result: JSONSchema = { ...schema };
  delete result.additionalProperties;
  // The generic "Configuration for action: X." line says nothing: drop it.
  if (typeof result.description === 'string') {
    const description = actionDescription(result.description);
    if (description) result.description = description;
    else delete result.description;
  }
  if (keep) {
    result.properties = Object.fromEntries(
      Object.entries(schema.properties ?? {}).filter(([name]) =>
        keep.includes(name)
      )
    );
    result.required = (schema.required ?? []).filter((name) =>
      keep.includes(name)
    );
  }
  return result;
}

/** Splits `with.headers.X` or `with.items[0].name` into path segments after `with`. */
function withSegments(field: string): string[] | null {
  if (
    field !== 'with' &&
    !field.startsWith('with.') &&
    !field.startsWith('with[')
  ) {
    return null;
  }
  return field
    .slice('with'.length)
    .replace(/\[(\d+)\]/g, '.$1')
    .split('.')
    .filter(Boolean);
}

/**
 * rjsf `extraErrors` of the issues under `with`: each message goes to the
 * error list of its field, so that rjsf renders it next to the field.
 */
export function issuesToErrorSchema(
  fieldIssues: Record<string, ValidationIssue[]>
): ErrorSchema {
  // Nodes without a prototype: a `__proto__` segment is a plain key and
  // never reaches Object.prototype.
  const root: Plain = Object.create(null);
  for (const [field, issues] of Object.entries(fieldIssues)) {
    const segments = withSegments(field);
    if (!segments) {
      continue;
    }
    let node = root;
    for (const segment of segments) {
      node[segment] = isPlainObject(node[segment])
        ? node[segment]
        : Object.create(null);
      node = node[segment] as Plain;
    }
    const errors = Array.isArray(node.__errors)
      ? (node.__errors as string[])
      : [];
    node.__errors = [...errors, ...issues.map((issue) => issue.message)];
  }
  return root as ErrorSchema;
}
