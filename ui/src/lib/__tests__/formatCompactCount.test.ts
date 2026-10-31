// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { formatCompactCount, formatExactCount } from '../formatCompactCount';

describe('formatCompactCount', () => {
  it.each<[number, string]>([
    [0, '0'],
    [742, '742'],
    [999, '999'],
    [1000, '1k'],
    [1234, '1.2k'],
    [1999, '1.9k'],
    [9999, '9.9k'],
    [10000, '10k'],
    [34567, '34k'],
    [345678, '345k'],
    [999999, '999k'],
    [1000000, '1M'],
    [1234567, '1.2M'],
    [9999999, '9.9M'],
    [10000000, '10M'],
    [31999999, '31M'],
    [999999999, '999M'],
    [1000000000, '1G'],
    [1500000000000, '999G+'],
  ])('writes %i as %s', (n, text) => {
    expect(formatCompactCount(n)).toBe(text);
  });

  // The display never shows more than the real count.
  it('rounds down', () => {
    for (const n of [1999, 9999, 19999, 999999, 1999999, 9999999]) {
      const text = formatCompactCount(n);
      const unit = text.endsWith('k') ? 1e3 : 1e6;
      expect(parseFloat(text) * unit).toBeLessThanOrEqual(n);
    }
  });

  it('keeps five characters at most', () => {
    for (let n = 0; n < 1e13; n = n === 0 ? 1 : Math.ceil(n * 1.37)) {
      expect(formatCompactCount(n).length).toBeLessThanOrEqual(5);
    }
  });

  it('reads a value that is not a count as 0', () => {
    expect(formatCompactCount(-5)).toBe('0');
    expect(formatCompactCount(Number.NaN)).toBe('0');
    expect(formatCompactCount(Number.POSITIVE_INFINITY)).toBe('0');
  });

  it('drops the fraction of a count', () => {
    expect(formatCompactCount(12.9)).toBe('12');
  });
});

describe('formatExactCount', () => {
  it('writes the separators of the language', () => {
    expect(formatExactCount(1234567, 'en')).toBe('1,234,567');
    expect(formatExactCount(1234567, 'ja')).toBe('1,234,567');
    expect(formatExactCount(-3, 'en')).toBe('0');
  });
});
