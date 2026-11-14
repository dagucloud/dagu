// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { issuesSample } from '../../testing/fixtures';
import {
  formSchemaOf,
  issuesToErrorSchema,
  splitWithValue,
} from '../paramsForm';

const issue = issuesSample[0]!;

describe('splitWithValue', () => {
  it('splits shown, hidden and unknown keys', () => {
    expect(
      splitWithValue(
        { image: 'a', pod: { x: 1 }, legacy: true },
        ['image', 'pod'],
        ['image']
      )
    ).toEqual({ formData: { image: 'a' }, other: [['legacy', true]] });
  });

  it('gives empty lists for a value that is not a map', () => {
    expect(splitWithValue('text', ['a'], ['a'])).toEqual({
      formData: {},
      other: [],
    });
    expect(splitWithValue(undefined, [], [])).toEqual({
      formData: {},
      other: [],
    });
  });

  it('keeps a __proto__ field as a key of its own', () => {
    const value = JSON.parse('{"__proto__": {"a": 1}}');
    const { formData } = splitWithValue(value, ['__proto__'], ['__proto__']);
    expect(Object.keys(formData)).toEqual(['__proto__']);
  });
});

describe('formSchemaOf', () => {
  const schema = {
    type: 'object',
    additionalProperties: false,
    required: ['value', 'routes'],
    properties: { value: { type: 'string' }, routes: { type: 'object' } },
  };

  it('removes the extra-key editor', () => {
    expect(formSchemaOf(schema)).toEqual({
      type: 'object',
      required: ['value', 'routes'],
      properties: schema.properties,
    });
  });

  it('drops the generic configuration line and keeps a real description', () => {
    expect(
      formSchemaOf({
        ...schema,
        description: 'Configuration for action: router.route.',
      }).description
    ).toBeUndefined();
    expect(
      formSchemaOf({
        ...schema,
        description:
          'Configuration for action: log.write. Writes a message to stdout.',
      }).description
    ).toBe('Writes a message to stdout.');
  });

  it('keeps only the listed properties', () => {
    expect(formSchemaOf(schema, ['value'])).toEqual({
      type: 'object',
      required: ['value'],
      properties: { value: { type: 'string' } },
    });
  });
});

describe('issuesToErrorSchema', () => {
  it('puts each message under its field', () => {
    expect(
      issuesToErrorSchema({
        'with.url': [issue],
        'with.headers.X': [issue, { ...issue, message: 'second' }],
        'with.items[0].name': [issue],
        run: [issue],
      })
    ).toEqual({
      url: { __errors: [issue.message] },
      headers: { X: { __errors: [issue.message, 'second'] } },
      items: { '0': { name: { __errors: [issue.message] } } },
    });
  });

  it('puts an issue of the whole with block at the root', () => {
    expect(issuesToErrorSchema({ with: [issue] })).toEqual({
      __errors: [issue.message],
    });
  });

  it('keeps a __proto__ field as a key of its own', () => {
    const schema = issuesToErrorSchema({ 'with.__proto__': [issue] });
    expect(({} as Record<string, unknown>).__errors).toBeUndefined();
    expect(Object.keys(schema)).toEqual(['__proto__']);
  });
});
