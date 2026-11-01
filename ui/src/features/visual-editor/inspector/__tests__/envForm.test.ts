// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { envOps, readEnv } from '../sections/flow/envForm';

const op = (path: (string | number)[], value: unknown) => ({
  kind: 'setStepField',
  step: 'steps[2]',
  path,
  value,
});

describe('readEnv', () => {
  it.each<[unknown, ReturnType<typeof readEnv>]>([
    [undefined, { form: 'none', entries: {} }],
    [[{ A: '1' }, { B: 2 }], { form: 'maps', entries: { A: '1', B: '2' } }],
    [['A=1', 'B=x=y'], { form: 'strings', entries: { A: '1', B: 'x=y' } }],
    [{ A: '1' }, { form: 'map', entries: { A: '1' } }],
    [[{ A: '1', B: '2' }], { form: 'other', entries: {} }],
    [['A=1', { B: '2' }], { form: 'other', entries: {} }],
    ['A=1', { form: 'other', entries: {} }],
  ])('%j', (value, expected) => {
    expect(readEnv(value)).toEqual(expected);
  });

  // An env name is user text: `__proto__` stays an entry of its own.
  it('keeps an entry named __proto__', () => {
    const maps = JSON.parse('[{ "__proto__": "1" }, { "B": "2" }]');
    expect(Object.entries(readEnv(maps).entries)).toEqual([
      ['__proto__', '1'],
      ['B', '2'],
    ]);
    expect(Object.entries(readEnv(['__proto__=1', 'B=2']).entries)).toEqual([
      ['__proto__', '1'],
      ['B', '2'],
    ]);
  });
});

describe('envOps', () => {
  it('sets one item key of a list of maps', () => {
    expect(
      envOps('steps[2]', [{ A: '1' }, { B: '2' }], { A: '1', B: '3' })
    ).toEqual([op(['env', 1, 'B'], '3')]);
  });

  // A later item wins over an earlier one with the same key.
  it('sets the item that gives the value of a key', () => {
    const list = [{ A: '1' }, { A: '2' }, { B: '3' }];
    expect(envOps('steps[2]', list, { A: '2', B: '4' })).toEqual([
      op(['env', 2, 'B'], '4'),
    ]);
    expect(envOps('steps[2]', list, { A: '5', B: '3' })).toEqual([
      op(['env', 1, 'A'], '5'),
    ]);
  });

  it('rebuilds a list of maps when the keys change', () => {
    expect(envOps('steps[2]', [{ A: '1' }], { A: '1', C: '4' })).toEqual([
      op(['env'], [{ A: '1' }, { C: '4' }]),
    ]);
    expect(envOps('steps[2]', [{ A: '1' }], {})).toEqual([
      op(['env'], undefined),
    ]);
  });

  it('keeps the KEY=value form', () => {
    expect(envOps('steps[2]', ['A=1'], { A: '2' })).toEqual([
      op(['env'], ['A=2']),
    ]);
  });

  it('edits a map key by key', () => {
    expect(envOps('steps[2]', { A: '1', B: '2' }, { A: '1' })).toEqual([
      op(['env', 'B'], undefined),
    ]);
  });

  it('creates a list of maps for a new env', () => {
    expect(envOps('steps[2]', undefined, { A: '1' })).toEqual([
      op(['env'], [{ A: '1' }]),
    ]);
    expect(envOps('steps[2]', undefined, {})).toEqual([]);
  });

  it('does not change another shape', () => {
    expect(envOps('steps[2]', 'A=1', { A: '2' })).toEqual([]);
  });
});
