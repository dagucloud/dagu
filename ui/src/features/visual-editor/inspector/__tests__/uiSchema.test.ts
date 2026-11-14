// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { JSONSchema } from '../../contracts';
import { getActionWithSchema } from '../withSchema';
import { buildUiSchema, isStringMap } from '../widgets';
import { schemaSample } from './fixtures';

describe('isStringMap', () => {
  it.each<[JSONSchema, boolean]>([
    [{ type: 'object', additionalProperties: { type: 'string' } }, true],
    [{ additionalProperties: { type: 'string' } }, true],
    [{ type: 'object', additionalProperties: true }, false],
    [{ type: 'object', additionalProperties: { type: 'array' } }, false],
    [
      {
        type: 'object',
        properties: { a: {} },
        additionalProperties: { type: 'string' },
      },
      false,
    ],
  ])('%j gives %s', (schema, expected) => {
    expect(isStringMap(schema)).toBe(expected);
  });
});

describe('buildUiSchema', () => {
  it('selects the fields of http.request', () => {
    const http = getActionWithSchema(schemaSample, 'http.request', [])!;
    expect(buildUiSchema(http)).toEqual({
      headers: { 'ui:field': 'KeyValue' },
      body: { 'ui:widget': 'textarea' },
    });
  });

  it('selects the fields of jq.filter', () => {
    const jq = getActionWithSchema(schemaSample, 'jq.filter', [])!;
    expect(buildUiSchema(jq)).toEqual({
      data: { 'ui:field': 'FreeValue' },
      raw: {
        'ui:field': 'ValueOrReference',
        'ui:fieldReplacesAnyOrOneOf': true,
      },
    });
  });

  it('walks nested objects and keeps enums on the default widget', () => {
    expect(
      buildUiSchema({
        type: 'object',
        properties: {
          format: { type: 'string', enum: ['json'] },
          pod: {
            type: 'object',
            properties: {
              labels: {
                type: 'object',
                additionalProperties: { type: 'string' },
              },
              name: { type: 'string' },
            },
          },
          plain: { type: 'object', properties: { name: { type: 'string' } } },
        },
      })
    ).toEqual({ pod: { labels: { 'ui:field': 'KeyValue' } } });
  });

  it('uses a text area when the description asks for long text', () => {
    expect(
      buildUiSchema({
        type: 'object',
        properties: {
          message: { type: 'string', description: 'Multi-line message text.' },
        },
      })
    ).toEqual({ message: { 'ui:widget': 'textarea' } });
  });
});
