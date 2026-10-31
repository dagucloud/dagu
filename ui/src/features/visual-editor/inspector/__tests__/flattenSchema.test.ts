// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { JSONSchema } from '../../contracts';
import { flattenObjectSchema } from '../flattenSchema';

const root: JSONSchema = {
  definitions: {
    base: {
      type: 'object',
      additionalProperties: false,
      properties: { url: { type: 'string' }, method: { type: 'string' } },
      allOf: [
        { if: { required: ['form'] }, then: { not: { required: ['body'] } } },
      ],
    },
    named: { type: 'string', description: 'A name.' },
  },
};

describe('flattenObjectSchema', () => {
  it('merges allOf parts into one object schema', () => {
    const flat = flattenObjectSchema(root, {
      description: 'Configuration for action: http.request.',
      allOf: [
        { $ref: '#/definitions/base' },
        { type: 'object', required: ['method', 'url'] },
      ],
    });
    expect(flat).toEqual({
      type: 'object',
      description: 'Configuration for action: http.request.',
      properties: { url: { type: 'string' }, method: { type: 'string' } },
      required: ['method', 'url'],
      additionalProperties: false,
    });
  });

  it('removes top-level constraint keywords', () => {
    const flat = flattenObjectSchema(root, {
      type: 'object',
      required: ['filter'],
      not: { required: ['data', 'input'] },
      anyOf: [{ required: ['a'] }],
      oneOf: [{ required: ['b'] }],
      if: { required: ['x'] },
      then: { required: ['y'] },
      else: { required: ['z'] },
      properties: { filter: { type: 'string' } },
    });
    expect(Object.keys(flat).sort()).toEqual([
      'properties',
      'required',
      'type',
    ]);
  });

  it('resolves property references and keeps nested constraints', () => {
    const flat = flattenObjectSchema(root, {
      type: 'object',
      properties: {
        name: { $ref: '#/definitions/named' },
        limit: { oneOf: [{ type: 'integer' }, { type: 'string' }] },
      },
    });
    expect(flat.properties?.name).toEqual({
      type: 'string',
      description: 'A name.',
    });
    expect(flat.properties?.limit?.oneOf).toHaveLength(2);
  });

  it('keeps an open additionalProperties schema', () => {
    const flat = flattenObjectSchema(root, {
      type: 'object',
      additionalProperties: { type: 'string' },
    });
    expect(flat).toEqual({
      type: 'object',
      additionalProperties: { type: 'string' },
    });
  });

  it('merges a property that two parts declare', () => {
    const flat = flattenObjectSchema(root, {
      allOf: [
        { properties: { mode: { type: 'string' } } },
        { properties: { mode: { enum: ['a', 'b'] } } },
      ],
    });
    expect(flat.properties?.mode).toEqual({ type: 'string', enum: ['a', 'b'] });
  });
});
