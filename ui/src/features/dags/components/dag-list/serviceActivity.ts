// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import type { StatusTone } from '@/lib/statusPalette';
import { ACTIVITY_WINDOW_SECONDS } from './activityBars';

type Bucket = components['schemas']['SampleBucket'];

/** The most cells that the strip of a service DAG draws (spec 097). */
export const SERVICE_MAX_CELLS = 60;
/**
 * The share of failed runs from which a cell reads as an error. A cell with
 * fewer failures than that, but at least one, reads as a warning, so that a
 * rare failure stays visible.
 */
export const SERVICE_ERROR_SHARE = 0.25;

/** One slot of the strip: the runs that the history counted in it. */
export interface ServiceCell {
  /** The start of the slot, in ms since the epoch. */
  start: number;
  count: number;
  failures: number;
  /** The time in ms in which the service was up, from the history. */
  upMs: number;
  /** The colour group of the cell; null for a slot with no run and no up time. */
  tone: StatusTone | null;
}

export interface ServiceActivity {
  /** Oldest slot first; the last slot holds `now`. */
  cells: ServiceCell[];
  /** The width of a slot, in seconds. */
  resolutionSeconds: number;
  /** The time that the cells cover, in seconds. */
  windowSeconds: number;
  /** The runs of the window. */
  count: number;
  failures: number;
  /** From 0 to 1; undefined when the window has no run. */
  availability: number | undefined;
  /**
   * The longest series of empty slots after the first run, in seconds. 0
   * when no slot is empty after a run.
   */
  longestGapSeconds: number;
}

/**
 * The colour group of a bucket from the share of its failed runs. A bucket
 * with no failure is up when it has a run or an up time.
 */
export function serviceTone(
  count: number,
  failures: number,
  upMs = 0
): StatusTone | null {
  if (failures <= 0) return count > 0 || upMs > 0 ? 'success' : null;
  return failures / count >= SERVICE_ERROR_SHARE ? 'error' : 'warning';
}

/**
 * The cells of the strip of a service DAG for the window that ends at `now`.
 * The strip has one slot for each `resolutionSeconds` of the window, at most
 * SERVICE_MAX_CELLS. A bucket stands in the slot of its start; a slot that no
 * bucket names is empty. A bucket before the window is left out of the
 * cells, and a bucket after `now` (a clock that is late) stands in the last
 * slot. The sums count every bucket that the server gave. The up time colours
 * a cell but never changes the count of runs, the availability or the gaps.
 */
export function serviceActivity(
  buckets: readonly Bucket[],
  resolutionSeconds: number | undefined,
  now: number,
  windowSeconds: number = ACTIVITY_WINDOW_SECONDS
): ServiceActivity {
  const resolution =
    resolutionSeconds && resolutionSeconds > 0
      ? resolutionSeconds
      : Math.ceil(windowSeconds / SERVICE_MAX_CELLS);
  const wanted = Math.ceil(windowSeconds / resolution);
  const slots = Math.max(1, Math.min(SERVICE_MAX_CELLS, wanted));
  const slotMs = resolution * 1000;
  const last = Math.floor(now / slotMs) * slotMs;
  const first = last - (slots - 1) * slotMs;
  const cells: ServiceCell[] = Array.from({ length: slots }, (_, i) => ({
    start: first + i * slotMs,
    count: 0,
    failures: 0,
    upMs: 0,
    tone: null,
  }));
  let count = 0;
  let failures = 0;
  for (const bucket of buckets) {
    count += bucket.count;
    failures += bucket.failures;
    const start = Date.parse(bucket.time);
    if (!Number.isFinite(start)) continue;
    const cell =
      cells[Math.min(slots - 1, Math.floor((start - first) / slotMs))];
    if (!cell) continue;
    cell.count += bucket.count;
    cell.failures += bucket.failures;
    cell.upMs += bucket.upMs ?? 0;
  }
  let gap = 0;
  let longestGap = 0;
  let seen = false;
  for (const cell of cells) {
    cell.tone = serviceTone(cell.count, cell.failures, cell.upMs);
    if (cell.count > 0) {
      seen = true;
      gap = 0;
    } else if (seen) {
      longestGap = Math.max(longestGap, ++gap);
    }
  }
  return {
    cells,
    resolutionSeconds: resolution,
    windowSeconds: wanted > slots ? slots * resolution : windowSeconds,
    count,
    failures,
    availability: count > 0 ? Math.max(0, 1 - failures / count) : undefined,
    longestGapSeconds: longestGap * resolution,
  };
}
