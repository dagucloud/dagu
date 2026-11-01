// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { formatRoughDurationMs } from '../formatDuration';

const SECOND = 1000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

describe('formatRoughDurationMs', () => {
  it('counts to the second within an hour', () => {
    expect(formatRoughDurationMs(53 * MINUTE + 22 * SECOND)).toBe('53m 22s');
    expect(formatRoughDurationMs(0)).toBe('0s');
  });

  it('counts to the minute within a day', () => {
    expect(formatRoughDurationMs(6 * HOUR + 53 * MINUTE + 22 * SECOND)).toBe(
      '6h 53m'
    );
  });

  it('counts to the hour from a day on', () => {
    expect(formatRoughDurationMs(DAY + 2 * HOUR + 53 * MINUTE)).toBe('1d 2h');
  });
});
