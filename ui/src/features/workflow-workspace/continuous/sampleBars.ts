// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// The model of the samples chart: which window the chart shows, and how the
// buckets of the API become bars. The model has no React and no DOM. It never
// makes an item for a sample: the API gives at most `points` buckets, and a
// bar is a bucket.

import type { components } from '@/api/v1/schema';

type SampleBucket = components['schemas']['SampleBucket'];

/** A bar of the chart is this wide, in px, at the most: the unit of `points`. */
export const POINT_PX = 4;
/** The bounds of `points` in the API (spec 083). */
export const POINTS_MIN = 10;
export const POINTS_MAX = 2000;

/** The `points` to ask for a chart of this width: one bucket per POINT_PX. */
export function pointsFor(widthPx: number): number {
  const raw = Math.floor((Number.isFinite(widthPx) ? widthPx : 0) / POINT_PX);
  return Math.min(POINTS_MAX, Math.max(POINTS_MIN, raw));
}

/**
 * The `points` to ask for a window of `spanMs`. It is pointsFor, unless even
 * the day tier needs more buckets than that for the window: then one for each
 * day bucket that the window touches, up to POINTS_MAX, so that a window of
 * years is not cut by the server.
 */
export function pointsForWindow(widthPx: number, spanMs: number): number {
  const base = pointsFor(widthPx);
  // The day buckets start at midnight UTC: a window that starts at another
  // time touches one bucket more than its span holds.
  const days = Math.ceil(spanMs / 86_400_000) + 1;
  return days > base ? Math.min(POINTS_MAX, days) : base;
}

const MINUTE_MS = 60_000;
const HOUR_MS = 60 * MINUTE_MS;
const DAY_MS = 24 * HOUR_MS;

/** The presets of the window, shortest first. */
export const RANGE_PRESETS = [
  { id: '1h', ms: HOUR_MS, label: '1h' },
  { id: '24h', ms: DAY_MS, label: '24h' },
  { id: '7d', ms: 7 * DAY_MS, label: '7d' },
  { id: '30d', ms: 30 * DAY_MS, label: '30d' },
  { id: '1y', ms: 365 * DAY_MS, label: '1y' },
  { id: '3y', ms: 3 * 365 * DAY_MS, label: '3y' },
] as const;

export type RangePresetId = (typeof RANGE_PRESETS)[number]['id'] | 'all';

/** The presets whose window ends now and that the page polls while the run is active. */
export const POLLED_PRESETS: readonly RangePresetId[] = ['1h', '24h'];

/** The shortest and the longest window that a zoom reaches. */
export const MIN_SPAN_MS = 10_000;
export const MAX_SPAN_MS = 10 * 365 * DAY_MS;

/**
 * The window of the chart. `preset` is set while the right end follows the
 * clock: the window is [now - span, now]. A zoom or a pan clears it.
 */
export interface View {
  from: number;
  to: number;
  preset: RangePresetId | null;
}

/**
 * The window of a preset. 'all' starts at `startedAt` (the start of the run),
 * and never shows less than one hour.
 */
export function presetView(
  id: RangePresetId,
  now: number,
  startedAt?: number
): View {
  if (id === 'all') {
    const from =
      startedAt !== undefined && Number.isFinite(startedAt) && startedAt < now
        ? startedAt
        : now - HOUR_MS;
    return { from: Math.min(from, now - HOUR_MS), to: now, preset: 'all' };
  }
  const preset = RANGE_PRESETS.find((p) => p.id === id)!;
  return { from: now - preset.ms, to: now, preset: id };
}

/** The window as it is at `now`: a preset window slides with the clock. */
export function viewAt(view: View, now: number): { from: number; to: number } {
  return view.preset === null || view.preset === 'all'
    ? { from: view.from, to: view.to }
    : { from: now - (view.to - view.from), to: now };
}

/**
 * A window zoomed by `factor` (under 1 zooms in) around the point at `ratio`
 * of its width (0 is the left end). The window keeps inside [MIN_SPAN_MS,
 * MAX_SPAN_MS] and does not go after `now`.
 */
export function zoomView(
  view: View,
  factor: number,
  ratio: number,
  now: number
): View {
  const span = view.to - view.from;
  const next = Math.min(MAX_SPAN_MS, Math.max(MIN_SPAN_MS, span * factor));
  const anchor = view.from + span * Math.min(1, Math.max(0, ratio));
  return clampToNow(
    {
      from: anchor - next * ratio,
      to: anchor + next * (1 - ratio),
      preset: null,
    },
    now
  );
}

/** The window moved by `deltaMs`; it does not go after `now`. */
export function panView(view: View, deltaMs: number, now: number): View {
  return clampToNow(
    { from: view.from + deltaMs, to: view.to + deltaMs, preset: null },
    now
  );
}

function clampToNow(view: View, now: number): View {
  if (view.to <= now) return view;
  const shift = view.to - now;
  return { from: view.from - shift, to: now, preset: null };
}

/** One bar: a bucket at its place in the plot, in px. */
export interface Bar {
  /** The bucket. */
  bucket: SampleBucket;
  /** The start of the bucket, in ms. */
  time: number;
  x: number;
  width: number;
  /** The height of the p95 bar. */
  height: number;
  /** The height of its failed part, from the baseline. 0 without a failure. */
  failHeight: number;
  /** The height of the p50 mark. */
  p50Height: number;
}

/** A label of the latency axis. */
export interface LatencyTick {
  ms: number;
  /** The height above the baseline, in px. */
  y: number;
}

export interface BarModel {
  bars: Bar[];
  /** The width of one bucket on the time axis, in px. */
  pitch: number;
  /** The latency of the top of the plot: the greatest p95. */
  maxMs: number;
  ticks: LatencyTick[];
}

export interface BarModelInput {
  buckets: readonly SampleBucket[];
  resolutionSeconds: number;
  /** The window that the plot shows, in ms. */
  from: number;
  to: number;
  /** The size of the plot, in px. */
  width: number;
  height: number;
}

/** The shortest bar, so that a bucket with a few samples shows. */
export const BAR_MIN_PX = 2;
const BAR_GAP_PX = 1;

/**
 * The bars of a set of buckets. A bar is as wide as its tier on the time
 * axis, with a gap of one pixel when it is 3 px or wider. Its height is the
 * p95 latency against the greatest p95 of the set; the failed part is the
 * share of the failures of the bucket, at least BAR_MIN_PX. A bucket that is
 * wholly outside the window has no bar. The count of bars is the count of
 * buckets, whatever the count of samples.
 */
export function barModel(input: BarModelInput): BarModel {
  const { buckets, resolutionSeconds, from, to, width, height } = input;
  const span = Math.max(1, to - from);
  const bucketMs = Math.max(1, resolutionSeconds) * 1000;
  const maxMs = buckets.reduce((m, b) => Math.max(m, b.p95Ms, b.p50Ms), 0);
  const px = (ms: number) =>
    maxMs > 0
      ? Math.min(
          height,
          Math.max(BAR_MIN_PX, Math.round((ms / maxMs) * height))
        )
      : BAR_MIN_PX;
  const bars: Bar[] = [];
  for (const bucket of buckets) {
    const time = Date.parse(bucket.time);
    if (!Number.isFinite(time)) continue;
    const left = ((time - from) / span) * width;
    const right = ((time + bucketMs - from) / span) * width;
    if (right <= 0 || left >= width) continue;
    const wide = Math.min(width, right) - Math.max(0, left);
    const barWidth = Math.max(1, wide - (wide >= 3 ? BAR_GAP_PX : 0));
    // A bar of a sub-pixel bucket is 1 px wide and stays inside the plot.
    const x = Math.min(Math.max(0, left), Math.max(0, width - barWidth));
    const barHeight = px(bucket.p95Ms);
    bars.push({
      bucket,
      time,
      x,
      width: barWidth,
      height: barHeight,
      failHeight:
        bucket.failures > 0 && bucket.count > 0
          ? Math.min(
              barHeight,
              Math.max(
                BAR_MIN_PX,
                Math.round((barHeight * bucket.failures) / bucket.count)
              )
            )
          : 0,
      p50Height: Math.min(barHeight, px(bucket.p50Ms)),
    });
  }
  bars.sort((a, b) => a.time - b.time);
  const ticks: LatencyTick[] =
    maxMs > 0
      ? [
          { ms: maxMs, y: height },
          { ms: Math.round(maxMs / 2), y: Math.round(height / 2) },
        ]
      : [];
  return { bars, pitch: (bucketMs / span) * width, maxMs, ticks };
}

/**
 * The bar under the pixel `x`: the one whose centre is the nearest, when it
 * is within half its width or 3 px. null when none is near.
 */
export function barAt(bars: readonly Bar[], x: number): Bar | null {
  if (bars.length === 0) return null;
  let low = 0;
  let high = bars.length - 1;
  while (low < high) {
    const mid = (low + high) >> 1;
    const bar = bars[mid]!;
    if (bar.x + bar.width / 2 < x) low = mid + 1;
    else high = mid;
  }
  let best: Bar | null = null;
  let bestDistance = Infinity;
  for (const bar of [bars[low - 1], bars[low]]) {
    if (!bar) continue;
    const distance = Math.abs(bar.x + bar.width / 2 - x);
    if (distance < bestDistance) {
      best = bar;
      bestDistance = distance;
    }
  }
  return best && bestDistance <= Math.max(best.width / 2 + 0.5, 3)
    ? best
    : null;
}

/** The window of a bucket, as the events list asks for it. */
export function bucketWindow(
  time: number,
  resolutionSeconds: number
): { from: number; to: number } {
  return { from: time, to: time + resolutionSeconds * 1000 };
}

/**
 * The path of the thin p50 line, over the bars, for a plot of this height. The
 * line breaks where buckets are missing: a gap of more than two buckets.
 */
export function p50Path(model: BarModel, height: number): string {
  const parts: string[] = [];
  let previous: Bar | null = null;
  for (const bar of model.bars) {
    const x = Math.round((bar.x + bar.width / 2) * 10) / 10;
    const y = height - bar.p50Height;
    const joined =
      previous !== null &&
      bar.x - (previous.x + previous.width) <= model.pitch * 2;
    parts.push(`${joined ? 'L' : 'M'}${x} ${y}`);
    previous = bar;
  }
  return parts.join('');
}
