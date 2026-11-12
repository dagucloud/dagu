// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { JSONSchema } from '../contracts';
import { resolveSchemaRef } from './actionRules';

/** Keys that hold cross-field constraints; a form cannot show them. */
const CONSTRAINT_KEYS = [
  'allOf',
  'anyOf',
  'oneOf',
  'not',
  'if',
  'then',
  'else',
] as const;

function collectParts(
  root: JSONSchema,
  node: JSONSchema,
  depth: number
): JSONSchema[] {
  const resolved = resolveSchemaRef(root, node);
  const parts = [resolved];
  if (depth < 8) {
    for (const part of resolved.allOf ?? []) {
      parts.push(...collectParts(root, part, depth + 1));
    }
  }
  return parts;
}

/**
 * Merges an object schema and its `allOf` parts into one object schema:
 * one `properties` map, the union of `required`, and `additionalProperties:
 * false` when one part forbids extra keys. Top-level constraint keywords
 * (`allOf`, `anyOf`, `oneOf`, `not`, `if`, `then`, `else`) are removed.
 * Property schemas are kept as they are.
 */
export function flattenObjectSchema(
  root: JSONSchema,
  schema: JSONSchema
): JSONSchema {
  const parts = collectParts(root, schema, 0);
  const result: JSONSchema = {};
  const properties: Record<string, JSONSchema> = {};
  const required = new Set<string>();
  let closed = false;
  let open: JSONSchema | true | undefined;

  for (const part of parts) {
    for (const [key, value] of Object.entries(part)) {
      if (
        (CONSTRAINT_KEYS as readonly string[]).includes(key) ||
        key === 'properties' ||
        key === 'required' ||
        key === 'additionalProperties' ||
        key === '$ref' ||
        key === 'type'
      ) {
        continue;
      }
      if (result[key] === undefined) {
        result[key] = value;
      }
    }
    for (const [name, property] of Object.entries(part.properties ?? {})) {
      properties[name] = properties[name]
        ? { ...properties[name], ...resolveSchemaRef(root, property) }
        : resolveSchemaRef(root, property);
    }
    for (const name of part.required ?? []) {
      required.add(name);
    }
    if (part.additionalProperties === false) {
      closed = true;
    } else if (part.additionalProperties !== undefined && open === undefined) {
      open = part.additionalProperties;
    }
  }

  result.type = 'object';
  if (Object.keys(properties).length > 0) {
    result.properties = properties;
  }
  if (required.size > 0) {
    result.required = [...required];
  }
  if (closed) {
    result.additionalProperties = false;
  } else if (open !== undefined) {
    result.additionalProperties = open;
  }
  return result;
}
