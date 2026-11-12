// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// The one text of a count of executions (spec 083, Compact Count). Every
// screen that shows such a count calls this module: a raw number does not
// fit a node card or a bar.

/** The whole count that a display stands for: whole, never negative. */
function wholeCount(n: number): number {
  return Number.isFinite(n) && n > 0 ? Math.floor(n) : 0;
}

/** `value` in units of `unit`: one decimal while the whole part has one digit. */
function scaled(value: number, unit: number, suffix: string): string {
  if (value < unit * 10) {
    const tenths = Math.floor(value / (unit / 10));
    const whole = Math.floor(tenths / 10);
    const decimal = tenths % 10;
    return `${whole}${decimal === 0 ? '' : `.${decimal}`}${suffix}`;
  }
  return `${Math.floor(value / unit)}${suffix}`;
}

/**
 * The compact form of a count: `742`, `1.2k`, `34k`, `1.2M`, `31M`. It
 * rounds down, so it never shows more than the real count, and it has at
 * most five characters. A count of 1 000 000 000 or more reads `1.2G`, and
 * one of 1 000 G or more reads `999G+`. A value that is not a count (NaN, a
 * negative number) reads `0`.
 */
export function formatCompactCount(n: number): string {
  const count = wholeCount(n);
  if (count < 1_000) return String(count);
  if (count < 1_000_000) return scaled(count, 1_000, 'k');
  if (count < 1_000_000_000) return scaled(count, 1_000_000, 'M');
  if (count < 1_000_000_000_000) return scaled(count, 1_000_000_000, 'G');
  return '999G+';
}

/**
 * The whole count with the separators of the language: the text of the
 * tooltip and of the accessible name.
 */
export function formatExactCount(n: number, locale: string): string {
  return new Intl.NumberFormat(locale).format(wholeCount(n));
}
