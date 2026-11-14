// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  TEST_RUN_TTL_MINUTES,
  TEST_RUN_TTL_MS,
  testRunExpiry,
} from '../testRun';

describe('testRunExpiry', () => {
  it('is the end of the run plus ten minutes', () => {
    expect(TEST_RUN_TTL_MS).toBe(10 * 60 * 1000);
    expect(TEST_RUN_TTL_MINUTES).toBe(10);
    expect(testRunExpiry({ finishedAt: '2026-10-26T10:00:00Z' })).toBe(
      Date.parse('2026-10-26T10:10:00Z')
    );
  });

  it('is null while the run has not ended', () => {
    expect(testRunExpiry({ finishedAt: '-' })).toBeNull();
    expect(testRunExpiry({ finishedAt: '' })).toBeNull();
    expect(testRunExpiry({ finishedAt: '0001-01-01T00:00:00Z' })).toBeNull();
    expect(testRunExpiry({ finishedAt: 'not a time' })).toBeNull();
    expect(testRunExpiry(null)).toBeNull();
  });
});
