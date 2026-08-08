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

/**
 * Formats the elapsed time between two RFC3339 timestamps as "1h 2m 3s".
 * A missing or "-" finish time measures against now (for running steps);
 * a missing or invalid start yields "-".
 */
export function formatRunDuration(
  startedAt: string | undefined,
  finishedAt: string | undefined
): string {
  if (!startedAt || startedAt === '-') {
    return '-';
  }

  const start = dayjs(startedAt);
  if (!start.isValid()) {
    return '-';
  }

  const end = finishedAt && finishedAt !== '-' ? dayjs(finishedAt) : dayjs();
  if (!end.isValid()) {
    return '-';
  }

  // Millisecond precision: second-level diff truncates toward zero, so a
  // finish up to 999ms before the start would read as a valid "0s".
  if (end.diff(start) < 0) {
    return '-';
  }
  const diff = end.diff(start, 'second');

  const hours = Math.floor(diff / 3600);
  const minutes = Math.floor((diff % 3600) / 60);
  const seconds = diff % 60;

  if (hours > 0) {
    return `${hours}h ${minutes}m ${seconds}s`;
  }
  if (minutes > 0) {
    return `${minutes}m ${seconds}s`;
  }
  return `${seconds}s`;
}
