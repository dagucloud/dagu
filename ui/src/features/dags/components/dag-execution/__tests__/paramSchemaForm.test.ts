// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { JSONSchema } from '@/lib/schema-utils';
import {
  buildParamSchemaFormData,
  buildParamSchemaUiSchema,
  stringifyParamSchemaFormData,
} from '../paramSchemaForm';

describe('paramSchemaForm helpers', () => {
  it('coerces defaultParams into typed schema form data', () => {
    const schema: JSONSchema = {
      type: 'object',
      properties: {
        region: { type: 'string' },
        count: { type: 'integer' },
        debug: { type: 'boolean' },
      },
    };

    expect(
      buildParamSchemaFormData(
        schema,
        'region="us-west-2" count="5" debug="true"'
      )
    ).toEqual({
      region: 'us-west-2',
      count: 5,
      debug: true,
    });
  });

  it('preserves blank string defaults for numeric schema fields', () => {
    const schema: JSONSchema = {
      type: 'object',
      properties: {
        count: { type: 'integer' },
      },
    };

    expect(buildParamSchemaFormData(schema, 'count=""')).toEqual({
      count: '',
    });
  });

  it('uses radio widgets only for short fixed choice lists', () => {
    const schema: JSONSchema = {
      type: 'object',
      properties: {
        region: {
          type: 'string',
          enum: ['us-east-1', 'us-west-2', 'eu-central-1', 'ap-northeast-1'],
        },
        backend: {
          type: 'string',
          enum: ['a', 'b', 'c', 'd', 'e'],
        },
      },
    };

    const uiSchema = buildParamSchemaUiSchema(schema);

    expect(uiSchema.region?.['ui:widget']).toBe('radio');
    expect(uiSchema.backend?.['ui:widget']).toBeUndefined();
  });

  it('uses textarea widgets for free text schema fields', () => {
    const schema: JSONSchema = {
      type: 'object',
      properties: {
        message: { type: 'string' },
        date: { type: 'string', format: 'date' },
        region: {
          type: 'string',
          enum: ['us-east-1', 'us-west-2'],
        },
      },
    };

    const uiSchema = buildParamSchemaUiSchema(schema);

    expect(uiSchema.message?.['ui:widget']).toBe('textarea');
    expect(uiSchema.date).toBeUndefined();
    expect(uiSchema.region?.['ui:widget']).toBe('radio');
  });

  it('serializes schema-backed form data as a JSON object payload', () => {
    expect(
      stringifyParamSchemaFormData({
        region: 'us-west-2',
        count: 5,
        debug: false,
      })
    ).toBe('{"region":"us-west-2","count":5,"debug":false}');
  });

  it('omits empty string and null values so eval-backed params are not overridden', () => {
    expect(
      stringifyParamSchemaFormData({
        sharedWorkDir: '',
        region: 'us-east-1',
        optional: null,
      } as ParamSchemaFormData)
    ).toBe('{"region":"us-east-1"}');
  });
});
