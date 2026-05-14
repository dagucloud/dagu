// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { JSONSchema } from '@/lib/schema-utils';
import { parseParams } from '@/lib/parseParams';

export type ParamSchemaFormData = Record<string, unknown>;
export type ParamSchemaUiSchema = Record<string, Record<string, unknown>>;

const radioChoiceLimit = 4;

/**
 * Builds typed schema form data from the legacy newline-delimited parameter text.
 */
export function buildParamSchemaFormData(
  schema: JSONSchema,
  defaultParams?: string
): ParamSchemaFormData {
  if (!defaultParams) {
    return {};
  }

  const properties = schema.properties ?? {};
  const formData: ParamSchemaFormData = {};

  for (const param of parseParams(defaultParams)) {
    if (!param.Name) {
      continue;
    }

    const propertySchema = properties[param.Name];
    if (!propertySchema) {
      continue;
    }

    formData[param.Name] = coerceParamSchemaValue(param.Value, propertySchema);
  }

  return formData;
}

/**
 * Chooses compact widgets for schema-backed parameters without losing free-text editing.
 */
export function buildParamSchemaUiSchema(
  schema: JSONSchema
): ParamSchemaUiSchema {
  const uiSchema: ParamSchemaUiSchema = {};

  for (const [name, propertySchema] of Object.entries(
    schema.properties ?? {}
  )) {
    const choiceCount = getChoiceCount(propertySchema);
    if (choiceCount > 0 && choiceCount <= radioChoiceLimit) {
      uiSchema[name] = { 'ui:widget': 'radio' };
      continue;
    }
    if (isFreeTextSchema(propertySchema)) {
      uiSchema[name] = { 'ui:widget': 'textarea' };
    }
  }

  return uiSchema;
}

/**
 * Serializes schema form data into the parameter payload expected by the start API.
 * Empty strings and nulls are omitted so the backend does not treat them as explicit
 * overrides (which would skip eval-backed parameters). See dagucloud/dagu#2032.
 */
export function stringifyParamSchemaFormData(
  formData: ParamSchemaFormData
): string {
  const filtered: ParamSchemaFormData = {};
  for (const [key, value] of Object.entries(formData)) {
    if (value === '' || value === null) {
      continue;
    }
    filtered[key] = value;
  }
  return JSON.stringify(filtered);
}

/**
 * Converts default string parameters to the scalar type declared by the JSON schema.
 */
function coerceParamSchemaValue(value: string, schema: JSONSchema): unknown {
  if (value.trim() === '') {
    return value;
  }

  switch (inferScalarType(schema)) {
    case 'integer': {
      const number = Number(value);
      return Number.isInteger(number) ? number : value;
    }
    case 'number': {
      const number = Number(value);
      return Number.isNaN(number) ? value : number;
    }
    case 'boolean':
      if (value === 'true') {
        return true;
      }
      if (value === 'false') {
        return false;
      }
      return value;
    case 'string':
    default:
      return value;
  }
}

/**
 * Infers the effective scalar type from direct, oneOf, or enum schema declarations.
 */
function inferScalarType(schema: JSONSchema): string | undefined {
  if (typeof schema.type === 'string') {
    return schema.type;
  }
  if (schema.oneOf?.length) {
    for (const option of schema.oneOf) {
      if (typeof option.type === 'string') {
        return option.type;
      }
      if (option.const !== undefined) {
        return inferTypeFromValue(option.const);
      }
    }
  }
  if (schema.enum?.length) {
    return inferTypeFromValue(schema.enum[0]);
  }
  return undefined;
}

/**
 * Maps a JavaScript scalar value back to the JSON schema type that represents it.
 */
function inferTypeFromValue(value: unknown): string | undefined {
  switch (typeof value) {
    case 'string':
      return 'string';
    case 'boolean':
      return 'boolean';
    case 'number':
      return Number.isInteger(value) ? 'integer' : 'number';
    default:
      return undefined;
  }
}

/**
 * Counts fixed choices represented as enum values or oneOf constants.
 */
function getChoiceCount(schema: JSONSchema): number {
  if (Array.isArray(schema.enum)) {
    return schema.enum.length;
  }
  if (Array.isArray(schema.oneOf)) {
    return schema.oneOf.filter((option) => option.const !== undefined).length;
  }
  return 0;
}

/**
 * Treats plain string schemas as multiline user input while leaving formatted
 * strings and fixed-choice fields to their specialized widgets.
 */
function isFreeTextSchema(schema: JSONSchema): boolean {
  if (
    schema.const !== undefined ||
    getChoiceCount(schema) > 0 ||
    typeof schema.format === 'string'
  ) {
    return false;
  }
  return inferScalarType(schema) === 'string';
}
