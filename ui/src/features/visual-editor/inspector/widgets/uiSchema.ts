// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { RJSFSchema, UiSchema } from '@rjsf/utils';
import type { JSONSchema } from '../../contracts';
import { isStringOrMap } from './StringOrMap';
import { scalarUnionKind } from './ValueOrReference';

const LONG_TEXT_NAMES = new Set([
  'body',
  'content',
  'prompt',
  'query',
  'script',
  'sql',
  'template',
]);
const LONG_TEXT_DESCRIPTION = /multi-?line|request body|script|template/i;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** True for an object whose values are strings, such as HTTP headers. */
export function isStringMap(schema: JSONSchema): boolean {
  return (
    (schema.type === 'object' || schema.type === undefined) &&
    !schema.properties &&
    isRecord(schema.additionalProperties) &&
    schema.additionalProperties.type === 'string'
  );
}

function hasNoShape(schema: JSONSchema): boolean {
  return (
    schema.type === undefined &&
    !schema.properties &&
    !schema.enum &&
    !schema.oneOf &&
    !schema.anyOf &&
    !schema.allOf &&
    schema.const === undefined &&
    !schema.items &&
    !schema.additionalProperties
  );
}

function isLongText(name: string, schema: JSONSchema): boolean {
  if (schema.type !== 'string' || schema.enum) {
    return false;
  }
  return (
    LONG_TEXT_NAMES.has(name) ||
    LONG_TEXT_DESCRIPTION.test(
      typeof schema.description === 'string' ? schema.description : ''
    )
  );
}

function propertyUiSchema(
  name: string,
  schema: JSONSchema
): UiSchema | undefined {
  if (isStringMap(schema)) {
    return { 'ui:field': 'KeyValue' };
  }
  if (isStringOrMap(schema as RJSFSchema)) {
    return {
      'ui:field': 'StringOrMap',
      'ui:fieldReplacesAnyOrOneOf': true,
    };
  }
  if (scalarUnionKind(schema as RJSFSchema)) {
    // Without the replace flag rjsf also renders its own oneOf selector.
    return {
      'ui:field': 'ValueOrReference',
      'ui:fieldReplacesAnyOrOneOf': true,
    };
  }
  if (hasNoShape(schema)) {
    return { 'ui:field': 'FreeValue' };
  }
  if (isLongText(name, schema)) {
    return { 'ui:widget': 'textarea' };
  }
  if (schema.type === 'object' && schema.properties) {
    const nested = buildUiSchema(schema);
    return Object.keys(nested).length > 0 ? nested : undefined;
  }
  return undefined;
}

/**
 * Builds the uiSchema of a flat `with` schema: maps of strings use the
 * KeyValue field, string-or-map unions use the StringOrMap field,
 * scalar-or-string unions use the ValueOrReference field,
 * properties without a type use the FreeValue field and long text uses a
 * text area. Enums keep the select widgets of the theme. Nested objects
 * get their own entries.
 */
export function buildUiSchema(withSchema: JSONSchema): UiSchema {
  const uiSchema: UiSchema = {};
  for (const [name, property] of Object.entries(withSchema.properties ?? {})) {
    if (!isRecord(property)) {
      continue;
    }
    const entry = propertyUiSchema(name, property as JSONSchema);
    if (entry) {
      uiSchema[name] = entry;
    }
  }
  return uiSchema;
}
