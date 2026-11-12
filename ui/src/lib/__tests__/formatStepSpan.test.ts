// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { formatStepSpan } from '../formatStepSpan';

describe('formatStepSpan', () => {
  it('reads <1s for a finished span of 0 between whole seconds', () => {
    expect(formatStepSpan('2026-01-01T00:00:05Z', '2026-01-01T00:00:05Z')).toBe(
      '<1s'
    );
  });

  it('uses the given text for the translated label', () => {
    expect(
      formatStepSpan('2026-01-01T00:00:05Z', '2026-01-01T00:00:05Z', {
        belowSecond: '不到 1 秒',
      })
    ).toBe('不到 1 秒');
  });

  it('keeps the milliseconds when the timestamps have them', () => {
    expect(
      formatStepSpan('2026-01-01T00:00:05.100Z', '2026-01-01T00:00:05.100Z')
    ).toBe('0ms');
    expect(
      formatStepSpan('2026-01-01T00:00:05.100Z', '2026-01-01T00:00:05.950Z')
    ).toBe('850ms');
  });

  it('reads 0ms for a new run whose fraction is .000', () => {
    // A millisecond timestamp can have a zero fraction: it is not an old run.
    expect(
      formatStepSpan('2026-01-01T00:00:05.000Z', '2026-01-01T00:00:05.000Z')
    ).toBe('0ms');
    expect(
      formatStepSpan(
        '2026-01-01T00:00:05.000+02:00',
        '2026-01-01T00:00:05.000+02:00'
      )
    ).toBe('0ms');
  });

  it('reads a real small span of a new run', () => {
    expect(
      formatStepSpan('2026-01-01T00:00:05.012Z', '2026-01-01T00:00:05.199Z')
    ).toBe('187ms');
    expect(
      formatStepSpan('2026-01-01T00:00:05.990Z', '2026-01-01T00:00:06.002Z')
    ).toBe('12ms');
  });

  it('reads 0ms when only one timestamp has a fraction', () => {
    expect(
      formatStepSpan('2026-01-01T00:00:05Z', '2026-01-01T00:00:05.000Z')
    ).toBe('0ms');
  });

  it('lets the caller tell the resolution of times it rebuilt', () => {
    expect(
      formatStepSpan('1970-01-01T00:00:00.000Z', '1970-01-01T00:00:00.000Z', {
        wholeSecond: true,
      })
    ).toBe('<1s');
    expect(
      formatStepSpan('2026-01-01T00:00:05Z', '2026-01-01T00:00:05Z', {
        wholeSecond: false,
      })
    ).toBe('0ms');
  });

  it('is formatElapsed for any other span', () => {
    expect(formatStepSpan('2026-01-01T00:00:05Z', '2026-01-01T00:00:08Z')).toBe(
      '3s'
    );
    expect(formatStepSpan('2026-01-01T00:00:00Z', '2026-01-01T00:02:05Z')).toBe(
      '2m 5s'
    );
  });

  it('keeps the live and unset cases', () => {
    const now = Date.parse('2026-01-01T00:00:05Z');
    expect(
      formatStepSpan('2026-01-01T00:00:05Z', undefined, { live: true, now })
    ).toBe('0s');
    expect(formatStepSpan('2026-01-01T00:00:05Z', undefined)).toBeNull();
    expect(formatStepSpan(undefined, undefined)).toBeNull();
  });
});
