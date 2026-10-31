// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  formatAbsoluteTime,
  formatElapsed,
  getDAGRunScheduleSortValue,
} from '../dagRunTiming';

describe('getDAGRunScheduleSortValue', () => {
  it('prefers schedule time over queued time', () => {
    expect(
      getDAGRunScheduleSortValue({
        scheduleTime: '2026-03-13T10:00:00Z',
        queuedAt: '2026-03-13T10:05:00Z',
      })
    ).toBeLessThan(
      getDAGRunScheduleSortValue({
        queuedAt: '2026-03-13T10:05:00Z',
      })
    );
  });

  it('falls back to queued time when schedule time is missing', () => {
    expect(
      getDAGRunScheduleSortValue({
        queuedAt: '2026-03-13T10:05:00Z',
      })
    ).toBeGreaterThan(0);
  });

  it('returns zero for missing or invalid timestamps', () => {
    expect(getDAGRunScheduleSortValue({})).toBe(0);
    expect(
      getDAGRunScheduleSortValue({
        scheduleTime: 'not-a-timestamp',
      })
    ).toBe(0);
  });
});

describe('formatElapsed', () => {
  const T0 = '2026-01-01T00:00:00Z';
  const at = (ms: number) => new Date(Date.parse(T0) + ms).toISOString();

  it('keeps the two largest units', () => {
    expect(formatElapsed(T0, at(850))).toBe('850ms');
    expect(formatElapsed(T0, at(3000))).toBe('3s');
    expect(formatElapsed(T0, at(125000))).toBe('2m 5s');
    expect(formatElapsed(T0, at(3723000))).toBe('1h 2m');
    // From 24 hours the largest units are days and hours.
    expect(formatElapsed(T0, at(23 * 3600000 + 59 * 60000))).toBe('23h 59m');
    expect(formatElapsed(T0, at(26 * 3600000))).toBe('1d 2h');
    // From 30 days, months and days; from a year, years and months.
    expect(formatElapsed(T0, at(29 * 86400000))).toBe('29d 0h');
    expect(formatElapsed(T0, at(45 * 86400000))).toBe('1mo 15d');
    expect(formatElapsed(T0, at(364 * 86400000))).toBe('12mo 4d');
    expect(formatElapsed(T0, at(1095 * 86400000))).toBe('3y 0mo');
    expect(formatElapsed(T0, at(800 * 86400000))).toBe('2y 2mo');
    expect(formatElapsed(T0, T0)).toBe('0ms');
  });

  it('returns null without a finish time unless it is live', () => {
    expect(formatElapsed(T0, undefined)).toBeNull();
    expect(formatElapsed(T0, '-')).toBeNull();
    expect(formatElapsed(T0, '0001-01-01T00:00:00Z')).toBeNull();
  });

  it('measures a live span against now and never shows ms', () => {
    const now = Date.parse(T0);
    expect(formatElapsed(at(-125000), undefined, { live: true, now })).toBe(
      '2m 5s'
    );
    expect(formatElapsed(at(-300), '-', { live: true, now })).toBe('0s');
  });

  it('returns null for an unset, invalid or negative span', () => {
    expect(formatElapsed(undefined, T0)).toBeNull();
    expect(formatElapsed('-', T0, { live: true })).toBeNull();
    expect(formatElapsed('nope', T0)).toBeNull();
    expect(formatElapsed(T0, 'nope', { live: true })).toBeNull();
    expect(formatElapsed(at(10), T0)).toBeNull();
    // A sub-second gap before the start is not a valid "0s".
    expect(
      formatElapsed('2026-08-08T10:00:00.500Z', '2026-08-08T10:00:00.100Z')
    ).toBeNull();
  });
});

describe('formatAbsoluteTime', () => {
  it('writes the date and the time with seconds', () => {
    expect(formatAbsoluteTime('2026-01-15T12:00:09Z')).toMatch(
      /^2026-01-1[45] \d\d:\d\d:09$/
    );
  });

  it('can drop the seconds', () => {
    expect(
      formatAbsoluteTime('2026-01-15T12:00:09Z', { seconds: false })
    ).toMatch(/^2026-01-1[45] \d\d:\d\d$/);
  });

  it('takes a Date or epoch milliseconds', () => {
    const date = new Date(2026, 9, 26, 8, 5, 9);
    expect(formatAbsoluteTime(date)).toBe('2026-10-26 08:05:09');
    expect(formatAbsoluteTime(date.getTime(), { seconds: false })).toBe(
      '2026-10-26 08:05'
    );
  });

  it('returns null for an unset or invalid value', () => {
    expect(formatAbsoluteTime(undefined)).toBeNull();
    expect(formatAbsoluteTime(null)).toBeNull();
    expect(formatAbsoluteTime('')).toBeNull();
    expect(formatAbsoluteTime('-')).toBeNull();
    expect(formatAbsoluteTime('0001-01-01T00:00:00Z')).toBeNull();
    expect(formatAbsoluteTime('not a date')).toBeNull();
  });
});
