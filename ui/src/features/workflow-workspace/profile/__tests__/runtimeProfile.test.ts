// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { RuntimeProfileEntryKind } from '@/api/v1/schema';
import { mergeProfileLayers, selectProfile } from '../runtimeProfile';

const at = '2026-10-01T00:00:00Z';

function variable(key: string, value: string) {
  return {
    key,
    kind: RuntimeProfileEntryKind.variable,
    value,
    createdAt: at,
    updatedAt: at,
  };
}

function secret(key: string) {
  return {
    key,
    kind: RuntimeProfileEntryKind.secret,
    secretId: `id-${key}`,
    createdAt: at,
    updatedAt: at,
  };
}

describe('mergeProfileLayers', () => {
  it('gives each key the layer that wins and lists the layers it beats', () => {
    const rows = mergeProfileLayers([
      { kind: 'global', entries: [variable('TZ', 'UTC'), variable('A', '1')] },
      { kind: 'workspace', entries: [variable('TZ', 'CET')] },
      { kind: 'profile', entries: [variable('TZ', 'JST'), variable('B', '2')] },
    ]);
    expect(rows.map((r) => [r.key, r.value, r.layer, r.overrides])).toEqual([
      ['A', '1', 'global', []],
      ['B', '2', 'profile', []],
      ['TZ', 'JST', 'profile', ['global', 'workspace']],
    ]);
  });

  // The API never returns a secret value; a variable of a weaker layer with
  // the same key does not leak through.
  it('has no value for a secret, also when it replaces a variable', () => {
    const rows = mergeProfileLayers([
      { kind: 'global', entries: [variable('TOKEN', 'plain')] },
      { kind: 'profile', entries: [secret('TOKEN'), secret('KEY')] },
    ]);
    expect(rows).toEqual([
      {
        key: 'KEY',
        kind: RuntimeProfileEntryKind.secret,
        value: undefined,
        layer: 'profile',
        overrides: [],
      },
      {
        key: 'TOKEN',
        kind: RuntimeProfileEntryKind.secret,
        value: undefined,
        layer: 'profile',
        overrides: ['global'],
      },
    ]);
  });

  it('gives nothing for no layer', () => {
    expect(mergeProfileLayers([])).toEqual([]);
    expect(mergeProfileLayers([{ kind: 'profile', entries: [] }])).toEqual([]);
  });
});

describe('selectProfile', () => {
  it('prefers the default of the workflow to the default of the workspace', () => {
    expect(selectProfile('prod', 'staging')).toEqual({
      name: 'prod',
      source: 'workflow',
    });
    expect(selectProfile(undefined, 'staging')).toEqual({
      name: 'staging',
      source: 'workspace',
    });
    expect(selectProfile('', '')).toEqual({ name: '', source: 'none' });
  });
});
