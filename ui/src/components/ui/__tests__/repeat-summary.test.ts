// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  readRepeatPolicy,
  repeatIteration,
  repeatSummary,
  type RepeatPolicyView,
} from '../repeat-summary';

// The English catalog: the key is the text.
const ts = (source: string, values?: Record<string, string | number>) =>
  source.replace(/\{(\w+)\}/g, (_, key: string) => String(values?.[key]));

function summary(raw: unknown): string {
  const policy = readRepeatPolicy(raw);
  if (!policy) throw new Error('no policy');
  return repeatSummary(policy, ts);
}

describe('readRepeatPolicy', () => {
  it('reads the YAML form', () => {
    expect(
      readRepeatPolicy({
        repeat: 'until',
        interval_sec: 30,
        limit: 5,
        condition: '${STATE}',
        expected: 'ready',
        exit_code: [0, 2],
      })
    ).toEqual({
      mode: 'until',
      intervalSec: 30,
      limit: 5,
      condition: '${STATE} = ready',
      exitCodes: [0, 2],
    });
  });

  it('reads the API form', () => {
    expect(
      readRepeatPolicy({
        repeat: 'while',
        interval: 10,
        limit: 3,
        condition: { condition: 'x', expected: 'y' },
        exitCode: [1],
        backoff: 2,
      })
    ).toEqual({
      mode: 'while',
      intervalSec: 10,
      limit: 3,
      condition: 'x = y',
      exitCodes: [1],
      backoff: true,
    });
  });

  it('reads the boolean and unknown forms as while', () => {
    expect(readRepeatPolicy({ repeat: true })?.mode).toBe('while');
    expect(readRepeatPolicy({ repeat: 'sometimes' })?.mode).toBe('while');
  });

  it('infers the mode as the server does when repeat is absent', () => {
    expect(readRepeatPolicy({ condition: 'a', expected: 'b' })?.mode).toBe(
      'until'
    );
    expect(readRepeatPolicy({ condition: 'a' })?.mode).toBe('while');
    expect(readRepeatPolicy({ exit_code: [1] })?.mode).toBe('while');
  });

  it('gives null for a step that does not repeat', () => {
    expect(readRepeatPolicy(undefined)).toBeNull();
    expect(readRepeatPolicy('while')).toBeNull();
    expect(readRepeatPolicy({})).toBeNull();
    expect(readRepeatPolicy({ interval_sec: 5 })).toBeNull();
  });

  it('keeps only fixed whole numbers for the interval and the limit', () => {
    expect(
      readRepeatPolicy({ repeat: 'while', interval_sec: '15', limit: '${N}' })
    ).toEqual({ mode: 'while', intervalSec: 15 });
    expect(
      readRepeatPolicy({ repeat: 'while', interval_sec: 0, limit: -1 })
    ).toEqual({ mode: 'while' });
  });
});

describe('repeatSummary', () => {
  it('writes an unconditional while with an interval and no limit', () => {
    expect(summary({ repeat: 'while', interval_sec: 10 })).toBe(
      'repeat while exit 0 · every 10s · no limit'
    );
  });

  it('writes until with a condition, an interval and a limit', () => {
    expect(
      summary({
        repeat: 'until',
        interval_sec: 30,
        limit: 5,
        condition: 'ready',
      })
    ).toBe('repeat until "ready" · every 30s · max 5');
  });

  it('writes the exit codes', () => {
    expect(summary({ repeat: 'while', exit_code: [1, 2] })).toBe(
      'repeat while exit 1, 2 · no limit'
    );
  });

  it('writes no interval when the policy has none', () => {
    expect(summary({ repeat: 'until', limit: 2 })).toBe(
      'repeat until exit 0 · max 2'
    );
  });

  it('reads the boolean form as while', () => {
    expect(summary({ repeat: true, interval_sec: 120 })).toBe(
      'repeat while exit 0 · every 2m · no limit'
    );
  });

  it('cuts a long condition to 24 characters', () => {
    const text = summary({
      repeat: 'while',
      condition: 'abcdefghijklmnopqrstuvwxyz0123456789',
    });
    expect(text).toContain('"abcdefghijklmnopqrstuvw…"');
  });
});

describe('repeatIteration', () => {
  const limited: RepeatPolicyView = { mode: 'while', limit: 5 };
  const open: RepeatPolicyView = { mode: 'while' };

  it('counts the running iteration after the finished ones', () => {
    expect(repeatIteration(limited, 2, true, ts)).toBe('iteration 3 of 5');
    expect(repeatIteration(open, 2, true, ts)).toBe('iteration 3');
  });

  it('counts the finished ones for a step that stopped', () => {
    expect(repeatIteration(limited, 3, false, ts)).toBe('iteration 3 of 5');
  });

  it('gives nothing before the first run', () => {
    expect(repeatIteration(limited, 0, false, ts)).toBeNull();
    expect(repeatIteration(limited, 0, true, ts)).toBe('iteration 1 of 5');
  });
});
