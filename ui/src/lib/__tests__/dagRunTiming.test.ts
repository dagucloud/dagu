// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { formatRunDuration, getDAGRunScheduleSortValue } from '../dagRunTiming';

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

describe('formatRunDuration', () => {
  it('formats elapsed time between two timestamps', () => {
    expect(
      formatRunDuration('2026-08-08T10:00:00Z', '2026-08-08T10:01:05Z')
    ).toBe('1m 5s');
  });

  it('rejects a finish timestamp before the start, including sub-second gaps', () => {
    expect(
      formatRunDuration('2026-08-08T10:00:00.500Z', '2026-08-08T10:00:00.100Z')
    ).toBe('-');
  });
});
