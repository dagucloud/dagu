// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { formatElapsed } from '@/lib/dagRunTiming';

const EPOCH = new Date(0).toISOString();

/**
 * A length of time in ms as the other durations of the UI read: "850ms",
 * "3s", "2m 5s", "1h 2m". formatElapsed owns the text.
 */
export function formatSpanMs(ms: number): string {
  const end = new Date(Math.max(0, Math.round(ms))).toISOString();
  return formatElapsed(EPOCH, end) ?? '0s';
}

/** The width of a tier, as the tooltip of the chart names it. */
export function formatResolution(seconds: number): string {
  if (seconds >= 86_400) return `${Math.round(seconds / 86_400)}d`;
  if (seconds >= 3_600) return `${Math.round(seconds / 3_600)}h`;
  if (seconds >= 60) return `${Math.round(seconds / 60)}m`;
  return `${seconds}s`;
}

/**
 * An availability from 0 to 1 as "99.98 %". The text rounds down, so that a
 * window with a failure never reads 100 %. undefined reads "-".
 */
export function formatAvailability(
  value: number | undefined,
  locale: string
): string {
  if (value === undefined || !Number.isFinite(value)) return '-';
  const percent =
    Math.floor(Math.min(1, Math.max(0, value)) * 10_000 + 1e-7) / 100;
  return `${new Intl.NumberFormat(locale, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(percent)} %`;
}
