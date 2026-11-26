// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Status, type components } from '@/api/v1/schema';
import {
  barHeights,
  barTone,
  BAR_MAX_PX,
  durationAxis,
  type BarTone,
} from '@/features/workflow-workspace/dock/runTimelineModel';

export type ActivityRun = components['schemas']['DAGListActivityRun'];

/** The window of the activity that the list asks for, in seconds. */
export const ACTIVITY_WINDOW_SECONDS = 24 * 60 * 60;
/** The width of a strip that does not fill its cell, in pixels. */
export const ACTIVITY_WIDTH_PX = 196;
/** The height of the strip of bars, in pixels. */
export const ACTIVITY_HEIGHT_PX = 24;
/** The width of one bar. */
export const ACTIVITY_BAR_PX = 5;
/** The least width of the slot of a bar: the bar and the space after it. */
export const ACTIVITY_SLOT_PX = 7;
/** The height of the shortest bar, so that an instant run stays visible. */
export const ACTIVITY_BAR_MIN_PX = 7;

/** One bar of the strip. */
export interface ActivityBar {
  id: string;
  status: Status;
  tone: BarTone;
  /** The pixel of the left edge of the bar in the strip. */
  left: number;
  height: number;
  startedAt: string;
  /** The time of the run, in ms; up to now for a running run. */
  durationMs: number;
}

function time(value: string | undefined): number | null {
  if (!value) return null;
  const ms = Date.parse(value);
  return Number.isFinite(ms) ? ms : null;
}

/**
 * The runs of the window that ends at `now`: those that started in it and
 * those still in progress. A list may draw its activity long after it read
 * it, by when a finished run can have left the window.
 */
export function runsInWindow(
  runs: readonly ActivityRun[],
  now: number
): ActivityRun[] {
  const from = now - ACTIVITY_WINDOW_SECONDS * 1000;
  return runs.filter((run) => {
    const start = time(run.startedAt);
    return start === null || start >= from || time(run.finishedAt) === null;
  });
}

/**
 * The bars of the runs in the window that ends at `now`. The strip is cut in
 * slots of ACTIVITY_SLOT_PX, one run to a slot. A run stands in the slot of
 * its start. When
 * that slot is taken by a newer run, it moves to the next free slot toward the
 * past, so that bars never overlap; a run that finds no free slot is left out.
 * A workflow that runs every few minutes thus draws its latest runs side by
 * side, not a solid block. A run that started before the window stands in the
 * first slot. The height follows the time of a run, on the scale of the
 * timeline of the workflow page.
 */
export function activityBars(
  runs: readonly ActivityRun[],
  now: number,
  widthPx: number = ACTIVITY_WIDTH_PX
): ActivityBar[] {
  const windowMs = ACTIVITY_WINDOW_SECONDS * 1000;
  // As many slots as the strip holds; they share its width, so the last bar
  // meets the right edge. A wider column holds more slots, not wider bars.
  const slots = Math.max(1, Math.floor(widthPx / ACTIVITY_SLOT_PX));
  const slotPx = widthPx / slots;
  const parts = runs.flatMap((run) => {
    const start = time(run.startedAt);
    if (start === null) return [];
    const finish = time(run.finishedAt);
    const running = run.status === Status.Running && finish === null;
    const durationMs = Math.max(0, (running ? now : (finish ?? start)) - start);
    return [{ run, start, durationMs }];
  });
  const durations = parts.map((p) => p.durationMs);
  const heights = barHeights(durations, durationAxis(durations));
  const bySlot = new Map<number, ActivityBar>();
  // The runs come newest first: the newest keeps the slot of its start.
  parts.forEach((p, i) => {
    const height = Math.max(
      ACTIVITY_BAR_MIN_PX,
      Math.round(((heights[i] ?? 0) * ACTIVITY_HEIGHT_PX) / BAR_MAX_PX)
    );
    let slot = Math.min(
      slots - 1,
      Math.max(0, Math.floor(((p.start - (now - windowMs)) / windowMs) * slots))
    );
    while (slot >= 0 && bySlot.has(slot)) slot -= 1;
    if (slot < 0) return;
    bySlot.set(slot, {
      id: p.run.dagRunId,
      status: p.run.status,
      tone: barTone(p.run.status),
      left: slot * slotPx,
      height,
      startedAt: p.run.startedAt,
      durationMs: p.durationMs,
    });
  });
  return [...bySlot.values()];
}
