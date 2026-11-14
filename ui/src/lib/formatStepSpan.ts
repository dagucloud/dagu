// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { formatElapsed, isUnsetTime } from './dagRunTiming';

type SpanOptions = NonNullable<Parameters<typeof formatElapsed>[2]> & {
  /** The text of a span under one second; "<1s" when not set. */
  belowSecond?: string;
  /**
   * Tells whether the times have a resolution of one second. By default the
   * text of the timestamps decides: they have it when neither has a fraction.
   * Pass it when the caller has turned the times into other values.
   */
  wholeSecond?: boolean;
};

/** True when the seconds of the timestamp text carry a fraction. */
export function hasFraction(value: string | null | undefined): boolean {
  return !!value && /\d{2}:\d{2}:\d{2}[.,]\d/.test(value);
}

/**
 * True when neither timestamp has a fraction: the times of a run that an
 * older version stored, with a resolution of one second.
 */
export function isWholeSecondSpan(
  startedAt: string | null | undefined,
  finishedAt: string | null | undefined
): boolean {
  return !hasFraction(startedAt) && !hasFraction(finishedAt);
}

/**
 * The duration text of a step. It is formatElapsed, except for one case: a
 * finished span of 0 ms between two timestamps with no fraction. A run that
 * an older version stored has a resolution of one second, so such a step ran
 * for less than one second and "0ms" would claim an exact value. That span
 * reads "<1s", or `belowSecond` for a translated text. A new run has
 * millisecond timestamps, so its span reads in ms, even when it is "0ms".
 * Pass the translation from the caller.
 */
export function formatStepSpan(
  startedAt: string | null | undefined,
  finishedAt: string | null | undefined,
  { belowSecond = '<1s', wholeSecond, ...options }: SpanOptions = {}
): string | null {
  const text = formatElapsed(startedAt, finishedAt, options);
  if (text !== '0ms' || isUnsetTime(startedAt) || isUnsetTime(finishedAt)) {
    return text;
  }
  return (wholeSecond ?? isWholeSecondSpan(startedAt, finishedAt))
    ? belowSecond
    : text;
}
