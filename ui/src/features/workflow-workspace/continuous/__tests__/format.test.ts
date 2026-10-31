// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { formatAvailability, formatResolution, formatSpanMs } from '../format';

describe('formatAvailability', () => {
  it('writes two decimals and a percent sign', () => {
    expect(formatAvailability(0.9998, 'en')).toBe('99.98 %');
    expect(formatAvailability(1, 'en')).toBe('100.00 %');
    expect(formatAvailability(0, 'en')).toBe('0.00 %');
  });

  // 99.999 % of the samples is not 100 %: the text rounds down.
  it('never reads 100 % for a window with a failure', () => {
    expect(formatAvailability(0.99999, 'en')).toBe('99.99 %');
  });

  it('reads a missing availability as a dash', () => {
    expect(formatAvailability(undefined, 'en')).toBe('-');
  });
});

describe('formatSpanMs and formatResolution', () => {
  it('reads like the other durations', () => {
    expect(formatSpanMs(850)).toBe('850ms');
    expect(formatSpanMs(125_000)).toBe('2m 5s');
    expect(formatResolution(1)).toBe('1s');
    expect(formatResolution(60)).toBe('1m');
    expect(formatResolution(3600)).toBe('1h');
    expect(formatResolution(86_400)).toBe('1d');
  });
});
