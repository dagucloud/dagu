// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import validator from '@rjsf/validator-ajv8';
import type { JSONSchema } from '../contracts';
import { resolveSchemaRef, stepSchemaOf } from './actionRules';
import { getActionWithSchema } from './withSchema';

interface CompiledCheck {
  (data: unknown): boolean;
  errors?:
    | {
        instancePath: string;
        keyword?: string;
        message?: string;
        params?: Record<string, unknown>;
      }[]
    | null;
}

interface AjvLike {
  compile(schema: object): CompiledCheck;
}

const compiled = new WeakMap<JSONSchema, Map<string, CompiledCheck | null>>();

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** The `with` schema of a `run` step, or null when the schema has none. */
function runWithSchema(schema: JSONSchema): JSONSchema | null {
  const config = schema.definitions?.runWithConfig;
  return isRecord(config) ? resolveSchemaRef(schema, config) : null;
}

/**
 * A small schema for one step value: the step properties that the value
 * uses (the inlined action rules are slow to compile), with the flat
 * `with` schema of the step action or of `run`, and the step rule on
 * unknown keys.
 */
function lightStepSchema(
  schema: JSONSchema,
  keys: string[],
  action: string
): JSONSchema | null {
  const step = stepSchemaOf(schema);
  if (!step?.properties) {
    return null;
  }
  const properties: Record<string, JSONSchema> = {};
  for (const key of keys) {
    // A key such as `constructor` is not a step property.
    const property = Object.prototype.hasOwnProperty.call(step.properties, key)
      ? step.properties[key]
      : undefined;
    if (property) {
      properties[key] = property;
    }
  }
  const withSchema = action
    ? getActionWithSchema(schema, action, [])
    : keys.includes('run')
      ? runWithSchema(schema)
      : null;
  if (withSchema && properties.with) {
    properties.with = withSchema;
  }
  const light: JSONSchema = { type: 'object', properties };
  if (step.additionalProperties === false) {
    light.additionalProperties = false;
  }
  return light;
}

function compileStepCheck(
  schema: JSONSchema,
  value: Record<string, unknown>
): CompiledCheck | null {
  const keys = Object.keys(value).sort();
  const action = typeof value.action === 'string' ? value.action : '';
  const cacheKey = `${action}|${keys.join(',')}`;
  let cache = compiled.get(schema);
  if (!cache) {
    cache = new Map();
    compiled.set(schema, cache);
  }
  if (cache.has(cacheKey)) {
    return cache.get(cacheKey) ?? null;
  }
  let check: CompiledCheck | null = null;
  const ajv = (validator as unknown as { ajv?: AjvLike }).ajv;
  const light = lightStepSchema(schema, keys, action);
  if (ajv && light) {
    try {
      check = ajv.compile({ ...light, definitions: schema.definitions });
    } catch {
      check = null;
    }
  }
  cache.set(cacheKey, check);
  return check;
}

/** Most messages that the panel shows for one check. */
export const MAX_SCHEMA_MESSAGES = 5;

/** One failed schema rule of a step value. */
export interface StepSchemaError {
  /** JSON pointer inside the step value (`/with`), empty for the step itself. */
  instancePath: string;
  /** The keyword of the rule that failed (`required`, `type`, `enum`...). */
  keyword: string;
  /** The message of the validator (`must be string`). */
  message: string;
  /** The key that is missing (`required`) or not allowed (`additionalProperties`). */
  property?: string;
}

/**
 * Checks one step value like `checkStepSchema`, and gives every failed rule
 * with its path, with no limit on their number. Gives none when the schema
 * cannot compile.
 */
export function checkStepSchemaErrors(
  schema: JSONSchema,
  value: unknown
): StepSchemaError[] {
  if (!isRecord(value)) {
    return [];
  }
  const check = compileStepCheck(schema, value);
  if (!check || check(value)) {
    return [];
  }
  return (check.errors ?? []).map((error) => {
    const params = error.params ?? {};
    const property =
      typeof params.missingProperty === 'string'
        ? params.missingProperty
        : typeof params.additionalProperty === 'string'
          ? params.additionalProperty
          : undefined;
    return {
      instancePath: error.instancePath,
      keyword: (error as { keyword?: string }).keyword ?? '',
      message: error.message ?? 'is not valid',
      ...(property !== undefined ? { property } : {}),
    };
  });
}

/**
 * Checks one step value against `definitions.step` of the DAG schema and
 * gives short messages (`/with/url must be string`). The check uses the
 * properties of the step, the `with` schema of its action or of `run` and
 * the rule on unknown keys; the cross-field rules are left to the server
 * validation. Gives no message when the schema cannot compile.
 */
export function checkStepSchema(schema: JSONSchema, value: unknown): string[] {
  const messages = new Set<string>();
  for (const error of checkStepSchemaErrors(schema, value)) {
    const where = error.instancePath || '/';
    const extra =
      error.keyword === 'additionalProperties' ? error.property : undefined;
    messages.add(
      `${where} ${error.message}${typeof extra === 'string' ? `: ${extra}` : ''}`
    );
    if (messages.size >= MAX_SCHEMA_MESSAGES) {
      break;
    }
  }
  return [...messages];
}
