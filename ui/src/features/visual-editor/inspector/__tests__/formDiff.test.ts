// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { diffFormData, sameValue, toFieldOps } from '../formDiff';
import type { FieldChange } from '../types';

describe('sameValue', () => {
  it.each<[unknown, unknown, boolean]>([
    [1, 1, true],
    ['a', 'b', false],
    [{ a: 1, b: undefined }, { a: 1 }, true],
    [[1, { x: 2 }], [1, { x: 2 }], true],
    [[1], [1, 2], false],
    [{ a: [] }, { a: {} }, false],
    [null, undefined, false],
  ])('%j and %j give %s', (a, b, expected) => {
    expect(sameValue(a, b)).toBe(expected);
  });
});

describe('diffFormData', () => {
  const http = {
    method: 'GET',
    url: 'https://a',
    headers: { X: '1', Y: '2' },
  };

  it.each<[string, unknown, unknown, FieldChange[]]>([
    ['equal values', http, structuredClone(http), []],
    [
      'one changed field',
      http,
      { ...http, url: 'https://b' },
      [{ path: ['with', 'url'], value: 'https://b' }],
    ],
    [
      'a removed header',
      http,
      { ...http, headers: { Y: '2' } },
      [{ path: ['with', 'headers', 'X'], value: undefined }],
    ],
    [
      'an added header',
      http,
      { ...http, headers: { X: '1', Y: '2', Z: '3' } },
      [{ path: ['with', 'headers', 'Z'], value: '3' }],
    ],
    [
      'a field set to undefined',
      http,
      { ...http, method: undefined },
      [{ path: ['with', 'method'], value: undefined }],
    ],
    [
      'two changed fields in key order',
      http,
      { ...http, url: 'u', method: 'POST' },
      [
        { path: ['with', 'method'], value: 'POST' },
        { path: ['with', 'url'], value: 'u' },
      ],
    ],
    [
      'a new object',
      undefined,
      { url: 'u' },
      [{ path: ['with'], value: { url: 'u' } }],
    ],
    [
      'an array item',
      { list: ['a', 'b'] },
      { list: ['a', 'c'] },
      [{ path: ['with', 'list', 1], value: 'c' }],
    ],
    [
      'a longer array',
      { list: ['a'] },
      { list: ['a', 'b'] },
      [{ path: ['with', 'list'], value: ['a', 'b'] }],
    ],
    [
      'a kind change',
      { body: { a: 1 } },
      { body: 'text' },
      [{ path: ['with', 'body'], value: 'text' }],
    ],
    [
      'a nested object field',
      { pod: { spec: { image: 'a' } } },
      { pod: { spec: { image: 'b' } } },
      [{ path: ['with', 'pod', 'spec', 'image'], value: 'b' }],
    ],
    ['an undefined key that was absent', { a: 1 }, { a: 1, b: undefined }, []],
    // Names of the prototype of an object are keys like any other.
    [
      'a new key named constructor',
      { a: '1' },
      { a: '1', constructor: '2' },
      [{ path: ['with', 'constructor'], value: '2' }],
    ],
    [
      'a removed key named toString',
      { toString: '1' },
      {},
      [{ path: ['with', 'toString'], value: undefined }],
    ],
  ])('%s', (_, prev, next, expected) => {
    expect(diffFormData(prev, next, ['with'])).toEqual(expected);
  });

  it('uses an empty base path by default', () => {
    expect(diffFormData({ a: 1 }, { a: 2 })).toEqual([
      { path: ['a'], value: 2 },
    ]);
  });
});

describe('toFieldOps', () => {
  it('gives one setStepField per change', () => {
    expect(
      toFieldOps('steps[0]', [
        { path: ['with', 'url'], value: 'u' },
        { path: ['with', 'headers', 'X'], value: undefined },
      ])
    ).toEqual([
      {
        kind: 'setStepField',
        step: 'steps[0]',
        path: ['with', 'url'],
        value: 'u',
      },
      {
        kind: 'setStepField',
        step: 'steps[0]',
        path: ['with', 'headers', 'X'],
        value: undefined,
      },
    ]);
  });
});
