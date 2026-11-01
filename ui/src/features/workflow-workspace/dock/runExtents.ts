// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Status } from '@/api/v1/schema';
import type { ApiDAGRunDetails } from '../contracts';

/** The height of one lane of extents, in pixels. */
export const LANE_PX = 5;
/** The space between two lanes. */
export const LANE_GAP_PX = 2;
/** The most lanes that the timeline draws. */
export const MAX_LANES = 4;
/** The height of the segment of an extent, inside its lane. */
export const EXTENT_PX = 3;
/** Two extents share a lane only when this many pixels lie between them. */
export const LANE_SPACE_PX = 4;
/** The width of the shortest extent, so that an instant run stays visible. */
export const EXTENT_MIN_PX = 2;
/** A run is long when it lasts more than this many times the median run. */
export const LONG_RUN_FACTOR = 10;
/** A run is long only when it lasts more than ten minutes. */
export const LONG_RUN_MIN_MS = 10 * 60_000;

/** The height of a block of `lanes` lanes. */
export function laneBlockHeight(lanes: number): number {
  return lanes * LANE_PX + Math.max(0, lanes - 1) * LANE_GAP_PX;
}

/**
 * The time span of a run: from its start to its finish, or to now. `start`
 * is the start of the total time: the own start of the run, moved back by
 * the cached time (`cached`, in ms) that the run took from other runs.
 */
export interface RunSpan {
  id: string;
  /** The start of the total time: `ownStart` minus `cached`. */
  start: number;
  /** The own start of the run. */
  ownStart: number;
  end: number;
  running: boolean;
  /** The cached time before the own start, in ms; 0 for none. */
  cached: number;
}

function time(value: string | undefined): number | null {
  if (!value || value === '-' || value.startsWith('0001-01-01')) return null;
  const ms = Date.parse(value);
  return Number.isFinite(ms) ? ms : null;
}

/**
 * The span of a run. A run that runs ends at `now`. A run without a finish
 * time that does not run is an instant at its start. null for a run that
 * has not started: it has no extent. With `cachedMs`, the span begins that
 * much earlier, as if the run had started when its cached time began.
 */
export function runSpan(
  run: ApiDAGRunDetails,
  now: number,
  cachedMs = 0
): RunSpan | null {
  const ownStart = time(run.startedAt);
  if (ownStart === null) return null;
  const running = run.status === Status.Running && !time(run.finishedAt);
  const finish = time(run.finishedAt);
  const end = running ? now : (finish ?? ownStart);
  const cached = Math.max(0, cachedMs);
  return {
    id: run.dagRunId,
    start: ownStart - cached,
    ownStart,
    end: Math.max(ownStart, end),
    running,
    cached,
  };
}

/** True when the span shares time with the range [from, to]. */
export function spanOverlaps(
  span: Pick<RunSpan, 'start' | 'end'>,
  from: number,
  to: number
): boolean {
  return span.end >= from && span.start <= to;
}

/** An extent that wants a lane: its pixel range. */
export interface LaneItem {
  id: string;
  x0: number;
  x1: number;
}

export interface LanePlace {
  /** The row, from 0 (top). */
  lane: number;
  /** True for an extent that needed a lane beyond the cap. */
  overflow: boolean;
}

export interface LanePacking {
  places: Map<string, LanePlace>;
  /** The number of extents that did not get a lane of their own. */
  hidden: number;
}

/**
 * Packs extents into lanes by greedy interval assignment: in the order of
 * their start, an extent takes the first lane whose last extent ended at
 * least LANE_SPACE_PX before. An extent that needs a lane beyond `maxLanes`
 * goes in the last lane and is marked as overflow; `hidden` counts them.
 */
export function packLanes(
  items: readonly LaneItem[],
  maxLanes: number = MAX_LANES,
  space: number = LANE_SPACE_PX
): LanePacking {
  const ends: number[] = [];
  const places = new Map<string, LanePlace>();
  let hidden = 0;
  const ordered = [...items].sort((a, b) => a.x0 - b.x0 || a.x1 - b.x1);
  for (const item of ordered) {
    let lane = ends.findIndex((end) => end + space <= item.x0);
    let overflow = false;
    if (lane < 0) {
      if (ends.length < maxLanes) {
        lane = ends.length;
        ends.push(item.x1);
      } else {
        lane = maxLanes - 1;
        overflow = true;
        hidden += 1;
        ends[lane] = Math.max(ends[lane]!, item.x1);
      }
    } else {
      ends[lane] = item.x1;
    }
    places.set(item.id, { lane, overflow });
  }
  return { places, hidden };
}

/**
 * For each span, the IDs of the other spans that overlap it in time. Spans
 * that only touch (one ends when the other starts) do not overlap.
 */
export function overlapIds(
  spans: readonly Pick<RunSpan, 'id' | 'start' | 'end'>[]
): Map<string, readonly string[]> {
  const out = new Map<string, readonly string[]>();
  for (const a of spans) {
    out.set(
      a.id,
      spans
        .filter((b) => b.id !== a.id && a.start < b.end && b.start < a.end)
        .map((b) => b.id)
    );
  }
  return out;
}

/** The median of the durations in milliseconds; 0 for an empty list. */
export function medianMs(durations: readonly number[]): number {
  if (durations.length === 0) return 0;
  const sorted = [...durations].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  return sorted.length % 2
    ? sorted[mid]!
    : (sorted[mid - 1]! + sorted[mid]!) / 2;
}

/**
 * True when a run lasts more than LONG_RUN_FACTOR times the median run of
 * the view and more than LONG_RUN_MIN_MS.
 */
export function isLongRun(durationMs: number, medianDurationMs: number) {
  return (
    durationMs > LONG_RUN_MIN_MS &&
    durationMs > LONG_RUN_FACTOR * medianDurationMs
  );
}
