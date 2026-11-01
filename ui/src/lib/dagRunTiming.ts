// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import dayjs from './dayjs';

type DAGRunTiming = {
  scheduleTime?: string;
  queuedAt?: string;
};

export function getDAGRunScheduleSortValue(run: DAGRunTiming): number {
  const timestamp = run.scheduleTime || run.queuedAt;
  if (!timestamp) {
    return 0;
  }

  const value = dayjs(timestamp).valueOf();
  return Number.isFinite(value) ? value : 0;
}

/** True for "-" and the Go zero time, which both mean "never". */
export function isUnsetTime(value: string | null | undefined): boolean {
  return !value || value === '-' || value.startsWith('0001-01-01');
}

function instant(value: string | null | undefined): number | null {
  if (isUnsetTime(value)) {
    return null;
  }
  const parsed = dayjs(value).valueOf();
  return Number.isFinite(parsed) ? parsed : null;
}

type ElapsedOptions = {
  /**
   * Measures an unset finish time against `now`, for a step or run that
   * still runs. Without it, an unset finish time gives null.
   */
  live?: boolean;
  /** The current time in ms; defaults to the clock. */
  now?: number;
};

/**
 * The one text of an elapsed time: the two largest units, as "850ms", "3s",
 * "2m 5s", "1h 2m", "3d 4h", "2mo 5d" or "3y 1mo". A month counts 30 days
 * and a year 365 days. Milliseconds show only for a finished span under one
 * second. Returns null when the start is unset or invalid, when the span is
 * negative, or when it has no finish time and is not live.
 */
export function formatElapsed(
  startedAt: string | null | undefined,
  finishedAt: string | null | undefined,
  { live = false, now }: ElapsedOptions = {}
): string | null {
  const start = instant(startedAt);
  if (start === null) {
    return null;
  }
  let end = instant(finishedAt);
  const finished = end !== null;
  if (end === null) {
    if (!live || !isUnsetTime(finishedAt)) {
      return null;
    }
    end = now ?? Date.now();
  }
  const ms = end - start;
  if (ms < 0) {
    return null;
  }
  if (ms < 1000) {
    return finished ? `${ms}ms` : '0s';
  }
  const total = Math.floor(ms / 1000);
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const seconds = total % 60;
  if (hours >= 24) {
    const days = Math.floor(hours / 24);
    if (days >= 365) {
      return `${Math.floor(days / 365)}y ${Math.floor((days % 365) / 30)}mo`;
    }
    if (days >= 30) {
      return `${Math.floor(days / 30)}mo ${days % 30}d`;
    }
    return `${days}d ${hours % 24}h`;
  }
  if (hours > 0) {
    return `${hours}h ${minutes}m`;
  }
  if (minutes > 0) {
    return `${minutes}m ${seconds}s`;
  }
  return `${seconds}s`;
}

/**
 * The one text of an absolute time: "2026-01-15 14:03:09", or without the
 * seconds when `seconds` is false. Takes an RFC 3339 string, epoch
 * milliseconds or a Date. Returns null when the value is unset or invalid.
 */
export function formatAbsoluteTime(
  value: string | number | Date | null | undefined,
  { seconds = true }: { seconds?: boolean } = {}
): string | null {
  if (value === null || value === undefined) {
    return null;
  }
  if (typeof value === 'string' && isUnsetTime(value)) {
    return null;
  }
  const parsed = dayjs(value);
  return parsed.isValid()
    ? parsed.format(seconds ? 'YYYY-MM-DD HH:mm:ss' : 'YYYY-MM-DD HH:mm')
    : null;
}
