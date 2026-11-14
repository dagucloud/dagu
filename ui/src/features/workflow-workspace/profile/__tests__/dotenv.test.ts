// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { RuntimeProfileEntryKind } from '@/api/v1/schema';
import {
  DOTENV_MASK,
  dotenvText,
  dotenvValueTokens,
  formatDotenvValue,
  visibleEntries,
} from '../dotenv';
import type { EffectiveEntry } from '../runtimeProfile';

function variable(key: string, value: string): EffectiveEntry {
  return {
    key,
    kind: RuntimeProfileEntryKind.variable,
    value,
    layer: 'profile',
    overrides: [],
  };
}

const secret: EffectiveEntry = {
  key: 'TOKEN',
  kind: RuntimeProfileEntryKind.secret,
  value: undefined,
  layer: 'profile',
  overrides: [],
};

describe('formatDotenvValue', () => {
  it('leaves a simple value as it is', () => {
    expect(formatDotenvValue('eu-west-1')).toBe('eu-west-1');
    expect(formatDotenvValue('https://x.io/a?b=1')).toBe('https://x.io/a?b=1');
    expect(formatDotenvValue('')).toBe('');
  });

  it('quotes a value with a space, a quote, a hash or a line end', () => {
    expect(formatDotenvValue('a b')).toBe('"a b"');
    expect(formatDotenvValue('say "hi"')).toBe('"say \\"hi\\""');
    expect(formatDotenvValue('a#b')).toBe('"a#b"');
    expect(formatDotenvValue('l1\nl2')).toBe('"l1\\nl2"');
    expect(formatDotenvValue('c:\\dir')).toBe('"c:\\\\dir"');
  });
});

describe('dotenvText', () => {
  it('writes NAME=value', () => {
    expect(dotenvText(variable('REGION', 'eu'))).toBe('REGION=eu');
    expect(dotenvText(variable('EMPTY', ''))).toBe('EMPTY=');
  });

  it('masks a secret', () => {
    expect(DOTENV_MASK).toBe('******');
    expect(dotenvText(secret)).toBe('TOKEN=******');
  });
});

describe('dotenvValueTokens', () => {
  it('colours a string, a number, a keyword and a reference', () => {
    expect(dotenvValueTokens(variable('A', 'eu'))).toEqual([
      { kind: 'string', text: 'eu' },
    ]);
    expect(dotenvValueTokens(variable('A', '42'))).toEqual([
      { kind: 'number', text: '42' },
    ]);
    expect(dotenvValueTokens(variable('A', 'true'))).toEqual([
      { kind: 'keyword', text: 'true' },
    ]);
    expect(dotenvValueTokens(variable('A', '${HOME}/x'))).toEqual([
      { kind: 'variable', text: '${HOME}' },
      { kind: 'string', text: '/x' },
    ]);
  });

  it('returns no token for an empty value and a comment for the mask', () => {
    expect(dotenvValueTokens(variable('A', ''))).toEqual([]);
    expect(dotenvValueTokens(secret)).toEqual([
      { kind: 'comment', text: '******' },
    ]);
  });
});

describe('visibleEntries', () => {
  const ten = Array.from({ length: 10 }, (_, i) => i);

  it('keeps the first 8 and counts the hidden ones', () => {
    expect(visibleEntries(ten, false)).toEqual({
      shown: ten.slice(0, 8),
      hidden: 2,
    });
  });

  it('shows all when expanded or when the list is short', () => {
    expect(visibleEntries(ten, true)).toEqual({ shown: ten, hidden: 0 });
    expect(visibleEntries(ten.slice(0, 8), false).hidden).toBe(0);
    expect(visibleEntries([], false)).toEqual({ shown: [], hidden: 0 });
  });
});
