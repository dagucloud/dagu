// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { JSONSchema } from '@/lib/schema-utils';

/**
 * Sets examples on every calendar-name field of schema, recognized by the
 * pattern of its calendarName definition. A dereferenced schema holds a copy
 * of that definition at each field, so updating the definition alone does
 * not reach the editor. Examples complete names without restricting them.
 */
export function applyCalendarNameExamples(
  schema: JSONSchema,
  examples: string[]
): void {
  const pattern = schema.definitions?.calendarName?.pattern;
  if (!pattern || examples.length === 0) {
    return;
  }
  const visited = new Set<object>();
  const visit = (node: unknown): void => {
    if (!node || typeof node !== 'object' || visited.has(node)) {
      return;
    }
    visited.add(node);
    if (Array.isArray(node)) {
      node.forEach(visit);
      return;
    }
    const record = node as Record<string, unknown>;
    if (record.pattern === pattern) {
      record.examples = examples;
    }
    Object.values(record).forEach(visit);
  };
  visit(schema);
}
