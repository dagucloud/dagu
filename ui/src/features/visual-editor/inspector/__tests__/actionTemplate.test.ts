// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { JSONSchema } from '../../contracts';
import { actionTemplate, initialFieldValue } from '../actionTemplate';

describe('initialFieldValue', () => {
  it.each<[JSONSchema | undefined, unknown]>([
    [{ type: 'string' }, ''],
    [{ type: 'string', default: 'GET' }, 'GET'],
    [{ type: 'string', enum: ['json', 'text'] }, 'json'],
    [{ type: 'boolean' }, false],
    [{ type: 'object' }, {}],
    [{ type: 'array' }, []],
    [{ type: 'integer' }, undefined],
    [{ type: 'number', default: 3 }, 3],
    [{ type: ['string', 'null'] }, ''],
    [{ oneOf: [{ type: 'object' }, { type: 'string' }] }, {}],
    [{}, ''],
    [undefined, ''],
  ])('%j gives %j', (schema, expected) => {
    expect(initialFieldValue(schema)).toEqual(expected);
  });
});

describe('actionTemplate', () => {
  const withSchema: JSONSchema = {
    type: 'object',
    required: ['method', 'url', 'timeout', 'headers'],
    properties: {
      method: { type: 'string' },
      url: { type: 'string' },
      timeout: { type: 'integer' },
      headers: { type: 'object' },
      body: { type: 'string' },
    },
  };

  it('fills the required fields only and leaves numbers out', () => {
    expect(actionTemplate('http.call', withSchema)).toEqual({
      action: 'http.call',
      with: { method: '', url: '', headers: {} },
    });
  });

  // An empty method or url does not validate, and the schema has no default.
  it('starts an http.request step with a valid request', () => {
    expect(actionTemplate('http.request', withSchema)).toEqual({
      action: 'http.request',
      with: { method: 'GET', url: 'https://example.com' },
    });
  });

  it('leaves the starter values out for a custom action', () => {
    expect(actionTemplate('http.request', withSchema, true).with).toEqual({
      method: '',
      url: '',
      headers: {},
    });
  });

  it('leaves with out when no field is required', () => {
    expect(actionTemplate('noop', null)).toEqual({ action: 'noop' });
    expect(actionTemplate('state.list', { type: 'object' })).toEqual({
      action: 'state.list',
    });
  });

  it('keeps an empty with block on request', () => {
    expect(actionTemplate('send-slack', { type: 'object' }, true)).toEqual({
      action: 'send-slack',
      with: {},
    });
  });
});
