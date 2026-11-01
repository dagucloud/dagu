// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Status, TriggerType } from '@/api/v1/schema';
import {
  reusedTimeMs,
  type ProvenanceRun,
} from '@/features/visual-editor/run/inputProvenance';
import dayjs from '@/lib/dayjs';
import { HATCH_PATTERN } from '@/lib/hatchPattern';
import { statusTone, type StatusTone } from '@/lib/statusPalette';
import type { ApiDAGRunDetails } from '../contracts';
import {
  EXTENT_MIN_PX,
  medianMs,
  overlapIds,
  packLanes,
  runSpan,
  spanOverlaps,
  type RunSpan,
} from './runExtents';

/** The numbers of runs that the user can choose; the server sends 30 at the most. */
export const TIMELINE_COUNTS: readonly number[] = [5, 10, 20, 30];
/** The number of runs that the timeline shows until the user chooses. */
export const TIMELINE_COUNT_DEFAULT = 30;
/** The most runs that the timeline keeps, whatever the server sends. */
export const TIMELINE_RUN_MAX = 100;
/** The width of a bar. */
export const BAR_PX = 4;
/** The space between two bars that touch each other. */
export const BAR_GAP_PX = 1;
/** The room at both ends of the time axis, so that an end bar stays whole. */
export const EDGE_PAD_PX = 10;
/** A bar takes the clicks that are this near at the most, in pixels. */
export const HIT_MAX_PX = 14;
/** The time axis spans one minute at the least. */
export const MIN_SPAN_MS = 60_000;
/** The width of a track that is not measured (tests, first render). */
export const DEFAULT_WIDTH_PX = 640;
/**
 * The height of the shortest bar, so that an instant run stays visible. A
 * bar under this height is drawn taller than its place on the duration axis.
 */
export const BAR_MIN_PX = 3;
/** The height of the longest bar: the height of the plot. */
export const BAR_MAX_PX = 48;
/** The width of the gutter of the duration axis. It is always reserved. */
export const Y_AXIS_PX = 40;
/** Two labels of the duration axis are at least this far apart, in pixels. */
export const Y_TICK_GAP_PX = 14;
/** The most labels above zero that the duration axis draws. */
export const Y_TICK_MAX = 3;
/**
 * The bars use the linear scale when the median run is at least this share
 * of the longest run. Under it, one very long run would flatten the others,
 * and the bars use the square root scale.
 */
export const LINEAR_MIN_SHARE = 0.1;
/** An extent shows its duration text when it is this wide at the least. */
export const EXTENT_LABEL_MIN_PX = 56;
/** ... and it covers this share of the axis, or starts before the view. */
export const EXTENT_LABEL_SHARE = 0.9;

/**
 * One layer of CSS background that hatches the bar or the extent of a test
 * run: diagonal lines in the page colour over the status colour of the run,
 * which stays as it is.
 */
export const TEST_RUN_HATCH = HATCH_PATTERN;

/** True for a test run: the server lists it only when asked and drops it soon. */
export function isTestRun(run: Pick<ApiDAGRunDetails, 'ephemeral'>): boolean {
  return run.ephemeral === true;
}

/**
 * The background layers of the fill of a bar or an extent: the hatch of a
 * test run on top of the layer that the tone has (the mix of a partial
 * success), when it has one. undefined when there is no layer at all.
 */
export function fillBackground(
  run: Pick<ApiDAGRunDetails, 'ephemeral'>,
  toneLayer?: string
): string | undefined {
  const layers = [
    ...(isTestRun(run) ? [TEST_RUN_HATCH] : []),
    ...(toneLayer ? [toneLayer] : []),
  ];
  return layers.length > 0 ? layers.join(', ') : undefined;
}

/**
 * The median duration that decides which run is long. A test run does not
 * count: it is not a run of the workflow, and a short one would make every
 * other run look long.
 */
export function longRunMedianMs(
  runs: readonly ApiDAGRunDetails[],
  now: number,
  showCached = false
): number {
  return medianMs(
    runs
      .filter((run) => !isTestRun(run))
      .map((run) => totalDurationMs(run, now, showCached))
      .filter((d): d is number => d !== null)
  );
}

/**
 * The time that the layout of the axis uses while a run runs: `now` rounded
 * down to a step of 1 s, 2 s, 4 s ... that is at most one pixel of the axis
 * (`spanMs` over `plotPx`). The layout moves with the step, not with each
 * second, so the ticks, the labels and the bars keep their style between two
 * steps. The texts of a running run read the true time.
 */
export function axisClock(now: number, spanMs: number, plotPx: number): number {
  const msPerPx = spanMs / Math.max(1, plotPx);
  const doublings = Math.max(0, Math.floor(Math.log2(msPerPx / 1000)));
  const step = 1000 * 2 ** (Number.isFinite(doublings) ? doublings : 0);
  return Math.floor(now / step) * step;
}

/** The colour of a bar: a status tone, or the mix of 'partial' successes. */
export type BarTone = StatusTone | 'partial';

/** The tone of the bar of a run status. Queued runs wait like Waiting ones. */
export function barTone(status: Status): BarTone {
  switch (status) {
    case Status.Queued:
      return 'warning';
    case Status.PartialSuccess:
      return 'partial';
    case Status.NotStarted:
      return 'neutral';
    default:
      return statusTone(status);
  }
}

function time(value: string | undefined): number | null {
  if (!value || value === '-') return null;
  const ms = Date.parse(value);
  return Number.isFinite(ms) ? ms : null;
}

/**
 * The wall time of a run in milliseconds: finished minus started. A run
 * that is running counts up to `now`. null when the run has no start, or
 * when it finished before it started.
 */
export function runDurationMs(
  run: Pick<ApiDAGRunDetails, 'status'> &
    Partial<Pick<ApiDAGRunDetails, 'startedAt' | 'finishedAt'>>,
  now: number
): number | null {
  const start = time(run.startedAt);
  if (start === null) return null;
  const finish = time(run.finishedAt);
  const end = finish ?? (run.status === Status.Running ? now : null);
  if (end === null || end < start) return null;
  return end - start;
}

/**
 * The cached time of a run in ms: the time that the steps it reused from
 * other runs took there. It is the SUM of the durations of the reused steps
 * (`finishedAt - startedAt` of each, whole seconds for an old record), not
 * the union of their intervals: the order in which the source runs chained
 * the steps is not known. 0 for a run that reused nothing.
 */
export function cachedMsOf(run: ProvenanceRun): number {
  return reusedTimeMs(run) ?? 0;
}

/** The own time, the cached time and the total time of a run, in ms. */
export interface DurationParts {
  own: number;
  cached: number;
  total: number;
}

/**
 * The parts of the total time of a run: its own wall time, plus its cached
 * time when `showCached`. null when the run has no own time.
 */
export function durationParts(
  run: ApiDAGRunDetails,
  now: number,
  showCached: boolean
): DurationParts | null {
  const own = runDurationMs(run, now);
  if (own === null) return null;
  const cached = showCached ? cachedMsOf(run) : 0;
  return { own, cached, total: own + cached };
}

/** The total time of a run in ms (see durationParts); null without an own time. */
export function totalDurationMs(
  run: ApiDAGRunDetails,
  now: number,
  showCached: boolean
): number | null {
  return durationParts(run, now, showCached)?.total ?? null;
}

/**
 * The height of the cached segment of a bar of `height` pixels: the share of
 * the cached time in the total time, whole pixels. At least one pixel for a
 * cached part and one for the own part, so that both stay visible.
 */
export function cachedBarPx(height: number, parts: DurationParts | null) {
  if (!parts || parts.cached <= 0 || parts.total <= 0 || height < 2) return 0;
  const raw = Math.round((height * parts.cached) / parts.total);
  return Math.min(height - 1, Math.max(1, raw));
}

/** How the bar height follows the duration. */
export type DurationScale = 'linear' | 'sqrt';

/** A label of the duration axis: a duration and its height above the baseline. */
export interface DurationTick {
  /** The duration in milliseconds. */
  v: number;
  /** The height of the tick above the baseline, in pixels. */
  y: number;
}

/** The duration axis: the scale of the bar heights and its labels. */
export interface DurationAxis {
  scale: DurationScale;
  /** The duration of the longest run: the top of the plot. */
  max: number;
  /** The labels above zero, from the top. */
  ticks: DurationTick[];
  /** The height above the baseline of a duration, in pixels. */
  heightOf(durationMs: number): number;
}

/**
 * The duration axis of a set of runs. The longest run takes the whole plot
 * (BAR_MAX_PX). The scale is linear, so that a bar twice as tall is a run
 * twice as long, unless the median run would be under LINEAR_MIN_SHARE of
 * the plot: then the height grows with the square root of the duration and
 * the labels stand at their square root positions. The labels are round
 * durations from 100 ms to 3 days, the largest that fits first, at least
 * Y_TICK_GAP_PX apart and from the baseline.
 */
export function durationAxis(
  durations: readonly (number | null)[]
): DurationAxis {
  const known = durations
    .filter((d): d is number => d !== null && d > 0)
    .sort((a, b) => a - b);
  const max = known[known.length - 1] ?? 0;
  const mid = Math.floor(known.length / 2);
  const median =
    known.length === 0
      ? 0
      : known.length % 2
        ? known[mid]!
        : (known[mid - 1]! + known[mid]!) / 2;
  const scale: DurationScale =
    max > 0 && median / max < LINEAR_MIN_SHARE ? 'sqrt' : 'linear';
  const heightOf = (d: number) => {
    if (d <= 0 || max <= 0) return 0;
    const share = Math.min(1, d / max);
    return BAR_MAX_PX * (scale === 'sqrt' ? Math.sqrt(share) : share);
  };
  const ticks: DurationTick[] = [];
  let above = Infinity;
  for (let i = NICE_DURATIONS_MS.length - 1; i >= 0; i -= 1) {
    const v = NICE_DURATIONS_MS[i]!;
    if (v > max) continue;
    const y = heightOf(v);
    if (y < Y_TICK_GAP_PX || above - y < Y_TICK_GAP_PX) continue;
    ticks.push({ v, y });
    above = y;
    if (ticks.length === Y_TICK_MAX) break;
  }
  return { scale, max, ticks, heightOf };
}

/**
 * The bar heights in pixels, in the order of the durations: the height of
 * the duration on the axis, at least BAR_MIN_PX. A run without a duration
 * gets BAR_MIN_PX.
 */
export function barHeights(
  durations: readonly (number | null)[],
  axis: DurationAxis = durationAxis(durations)
): number[] {
  return durations.map((d) =>
    d === null || d <= 0
      ? BAR_MIN_PX
      : Math.max(BAR_MIN_PX, Math.round(axis.heightOf(d)))
  );
}

/**
 * The runs of the timeline: the `max` newest of a newest-first list, oldest
 * first.
 */
export function timelineRuns(
  runsNewestFirst: readonly ApiDAGRunDetails[],
  max: number = TIMELINE_RUN_MAX
): ApiDAGRunDetails[] {
  return runsNewestFirst.slice(0, max).reverse();
}

/** The time that places a run on the timeline: its start, else its queue time. */
export function runTime(run: ApiDAGRunDetails): string | undefined {
  if (run.startedAt && run.startedAt !== '-') return run.startedAt;
  return run.queuedAt || undefined;
}

/**
 * The counts that the control offers: the fixed ones, and the number of runs
 * that the server sends when it is more than the largest fixed one.
 */
export function countOptions(available: number): number[] {
  const largest = TIMELINE_COUNTS[TIMELINE_COUNTS.length - 1]!;
  const more = Math.min(available, TIMELINE_RUN_MAX);
  return more > largest ? [...TIMELINE_COUNTS, more] : [...TIMELINE_COUNTS];
}

/** The stored choice to show the test runs: on unless the text says "0". */
export function parseStoredShowTests(raw: string | null): boolean {
  return raw !== '0';
}

/** The trigger types, in the order of the filter menu. */
export const TRIGGER_TYPES: readonly TriggerType[] = [
  TriggerType.manual,
  TriggerType.scheduler,
  TriggerType.webhook,
  TriggerType.subdag,
  TriggerType.retry,
  TriggerType.catchup,
  TriggerType.unknown,
];

/** The trigger type of a run: a run with none is Unknown. */
export function triggerOf(
  run: Pick<ApiDAGRunDetails, 'triggerType'>
): TriggerType {
  return run.triggerType ?? TriggerType.unknown;
}

/**
 * The stored trigger types that the timeline hides: a JSON list. Anything
 * else, and any name that is not a trigger type, hides nothing.
 */
export function parseStoredHiddenTypes(raw: string | null): TriggerType[] {
  if (raw === null) return [];
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return TRIGGER_TYPES.filter((type) => parsed.includes(type));
  } catch {
    return [];
  }
}

/** The stored count when it is one of the fixed counts, else the default. */
export function parseStoredCount(raw: string | null): number {
  const value = raw === null ? NaN : Number(raw);
  return TIMELINE_COUNTS.includes(value) ? value : TIMELINE_COUNT_DEFAULT;
}

/** The most runs that the server lists for one workflow. */
export const HISTORY_LIMIT = 30;

/** The stored choice to show the cached time: on unless the text says "0". */
export function parseStoredShowCached(raw: string | null): boolean {
  return raw !== '0';
}

/** How the timeline picks its runs: the newest ones, or those in a time range. */
export type RunViewMode = 'count' | 'range';

/** The stored mode: 'range' only when the text says so. */
export function parseStoredMode(raw: string | null): RunViewMode {
  return raw === 'range' ? 'range' : 'count';
}

const HOUR_MS_PRESET = 60 * 60_000;

/** The presets of the time range, shortest first. */
export const RANGE_PRESETS = [
  { id: '1h', ms: HOUR_MS_PRESET, label: 'Last hour' },
  { id: '6h', ms: 6 * HOUR_MS_PRESET, label: 'Last 6 hours' },
  { id: '24h', ms: 24 * HOUR_MS_PRESET, label: 'Last 24 hours' },
  { id: '7d', ms: 7 * 24 * HOUR_MS_PRESET, label: 'Last 7 days' },
] as const;

export type RangePresetId = (typeof RANGE_PRESETS)[number]['id'];

/** A time range: a preset that ends now, or a custom range with its ends. */
export type RunRange =
  | { preset: RangePresetId }
  | { preset: 'custom'; from: number; to: number };

/** The range that the timeline starts with. */
export const RANGE_DEFAULT: RunRange = { preset: '24h' };

/** The stored range; the default for a text that is not a valid range. */
export function parseStoredRange(raw: string | null): RunRange {
  if (raw === null) return RANGE_DEFAULT;
  try {
    const value = JSON.parse(raw) as {
      preset?: unknown;
      from?: unknown;
      to?: unknown;
    } | null;
    const preset = value?.preset;
    if (RANGE_PRESETS.some((p) => p.id === preset)) {
      return { preset: preset as RangePresetId };
    }
    if (
      preset === 'custom' &&
      typeof value?.from === 'number' &&
      typeof value.to === 'number' &&
      Number.isFinite(value.from) &&
      Number.isFinite(value.to) &&
      value.from <= value.to
    ) {
      return { preset, from: value.from, to: value.to };
    }
  } catch {
    // Not JSON: the default applies.
  }
  return RANGE_DEFAULT;
}

/** The ends of a range at `now`, in ms. A preset ends now. */
export function rangeBounds(
  range: RunRange,
  now: number
): { from: number; to: number } {
  if (range.preset === 'custom') return { from: range.from, to: range.to };
  const ms = RANGE_PRESETS.find((p) => p.id === range.preset)!.ms;
  return { from: now - ms, to: now };
}

/** The time that places a run, in ms: its start, else its queue time. */
export function runTimeMs(run: ApiDAGRunDetails): number | null {
  return time(runTime(run));
}

/**
 * True when the extent of a run overlaps the range. The extent starts at the
 * cached time when `showCached`. A run that has not started counts at its
 * queue time, as an instant; one without any time is out.
 */
export function runInRange(
  run: ApiDAGRunDetails,
  now: number,
  bounds: { from: number; to: number },
  showCached: boolean
): boolean {
  const span = runSpan(run, now, showCached ? cachedMsOf(run) : 0);
  if (span) return spanOverlaps(span, bounds.from, bounds.to);
  const at = runTimeMs(run);
  return at !== null && at >= bounds.from && at <= bounds.to;
}

/**
 * The run next to the selected one in the visible runs (oldest first):
 * 'older' is one step to the left on the timeline, 'newer' one step to the
 * right. null at the ends. A selected run that is not in the list (it
 * expired, or it lies outside the range) sits at its own time `at` between
 * the listed runs; without a time, it counts as newer than every listed run.
 */
export function adjacentVisibleRunId(
  runs: readonly ApiDAGRunDetails[],
  selectedId: string | null,
  direction: 'older' | 'newer',
  at: number | null = null
): string | null {
  const index =
    selectedId === null ? -1 : runs.findIndex((r) => r.dagRunId === selectedId);
  if (index >= 0)
    return (
      runs[direction === 'older' ? index - 1 : index + 1]?.dagRunId ?? null
    );
  if (at === null) {
    return direction === 'older'
      ? (runs[runs.length - 1]?.dagRunId ?? null)
      : null;
  }
  const times = runs.map((r) => runTimeMs(r) ?? -Infinity);
  if (direction === 'older') {
    for (let i = runs.length - 1; i >= 0; i -= 1) {
      if (times[i]! < at) return runs[i]!.dagRunId;
    }
    return null;
  }
  const first = times.findIndex((t) => t > at);
  return first < 0 ? null : runs[first]!.dagRunId;
}

/**
 * The runs that the track draws, oldest first: the `count` newest of the
 * list. A selected run that is older than these moves the window back, so
 * that the selected bar is always in view; the window keeps its size and the
 * selected run is its oldest bar.
 */
export function timelineWindow<T extends { dagRunId: string }>(
  runs: readonly T[],
  selectedId: string | null,
  count: number
): T[] {
  if (runs.length <= count) return [...runs];
  let start = runs.length - count;
  const selected = runs.findIndex((run) => run.dagRunId === selectedId);
  if (selected >= 0 && selected < start) start = selected;
  return runs.slice(start, start + count);
}

// --- Time axis --------------------------------------------------------------

const SECOND_MS = 1000;
const MINUTE_MS = 60 * SECOND_MS;
const HOUR_MS = 60 * MINUTE_MS;
const DAY_MS = 24 * HOUR_MS;
const NICE_DURATIONS_MS = [
  100,
  200,
  500,
  SECOND_MS,
  2 * SECOND_MS,
  5 * SECOND_MS,
  10 * SECOND_MS,
  15 * SECOND_MS,
  30 * SECOND_MS,
  MINUTE_MS,
  2 * MINUTE_MS,
  5 * MINUTE_MS,
  10 * MINUTE_MS,
  15 * MINUTE_MS,
  30 * MINUTE_MS,
  HOUR_MS,
  2 * HOUR_MS,
  3 * HOUR_MS,
  6 * HOUR_MS,
  12 * HOUR_MS,
  DAY_MS,
  2 * DAY_MS,
  3 * DAY_MS,
];

const SUB_DAY_STEPS_MS = [
  SECOND_MS,
  2 * SECOND_MS,
  5 * SECOND_MS,
  10 * SECOND_MS,
  15 * SECOND_MS,
  30 * SECOND_MS,
  MINUTE_MS,
  2 * MINUTE_MS,
  5 * MINUTE_MS,
  10 * MINUTE_MS,
  15 * MINUTE_MS,
  30 * MINUTE_MS,
  HOUR_MS,
  2 * HOUR_MS,
  3 * HOUR_MS,
  6 * HOUR_MS,
  12 * HOUR_MS,
];
const DAY_STEPS = [1, 2, 7, 14];
const MONTH_STEPS = [1, 2, 3, 6];
const YEAR_STEPS = [1, 2, 5, 10, 100];
/** The width of one character of an axis label, at 11px. */
const CHAR_PX = 6.4;
/** The room between two tick labels that the choice of the step keeps. */
const TICK_SPACE_PX = 22;
/** Two labels are at least this far apart, or the later one is skipped. */
const LABEL_GAP_PX = 6;
/** Two date lines are at least this far apart. */
const CONTEXT_GAP_PX = 10;

const labelWidth = (text: string) => text.length * CHAR_PX;

/** A tick of the time axis: its time and the two lines of its label. */
export interface TimeTick {
  v: number;
  label: string;
  /** The day, under a clock time. */
  date: string;
  /** The year, under a clock time, a day or a month. */
  year: string;
}

function localMidnight(ms: number): number {
  const d = new Date(ms);
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}

function subDayTicks(t0: number, t1: number, step: number): number[] {
  const ticks: number[] = [];
  const day = new Date(localMidnight(t0));
  for (let guard = 0; day.getTime() <= t1 && guard < 4000; guard += 1) {
    const midnight = day.getTime();
    day.setDate(day.getDate() + 1);
    const next = day.getTime();
    if (step >= HOUR_MS) {
      // Wall-clock hours (00:00, 06:00 ...), also on a day that changes the clock.
      const base = new Date(midnight);
      for (let hour = 0; hour < 24; hour += step / HOUR_MS) {
        const t = new Date(
          base.getFullYear(),
          base.getMonth(),
          base.getDate(),
          hour
        ).getTime();
        if (
          t >= t0 &&
          t <= t1 &&
          (ticks.length === 0 || ticks[ticks.length - 1]! < t)
        ) {
          ticks.push(t);
        }
      }
      continue;
    }
    const first =
      midnight + Math.ceil(Math.max(0, t0 - midnight) / step) * step;
    for (let t = first; t < next && t <= t1 && ticks.length < 400; t += step) {
      ticks.push(t);
    }
  }
  return ticks;
}

function dayTicks(t0: number, t1: number, every: number): number[] {
  const ticks: number[] = [];
  const day = new Date(localMidnight(t0));
  for (let guard = 0; day.getTime() <= t1 && guard < 5000; guard += 1) {
    const t = day.getTime();
    const number = Math.round(
      Date.UTC(day.getFullYear(), day.getMonth(), day.getDate()) / DAY_MS
    );
    if (t >= t0 && number % every === 0) ticks.push(t);
    day.setDate(day.getDate() + 1);
  }
  return ticks;
}

function monthTicks(t0: number, t1: number, every: number): number[] {
  const ticks: number[] = [];
  const start = new Date(t0);
  const cursor = new Date(start.getFullYear(), start.getMonth(), 1);
  for (let guard = 0; cursor.getTime() <= t1 && guard < 5000; guard += 1) {
    const t = cursor.getTime();
    if (
      t >= t0 &&
      (cursor.getFullYear() * 12 + cursor.getMonth()) % every === 0
    ) {
      ticks.push(t);
    }
    cursor.setMonth(cursor.getMonth() + 1);
  }
  return ticks;
}

function yearTicks(t0: number, t1: number, every: number): number[] {
  const ticks: number[] = [];
  for (let y = new Date(t0).getFullYear(); ticks.length < 200; y += 1) {
    const t = new Date(y, 0, 1).getTime();
    if (t > t1) break;
    if (t >= t0 && y % every === 0) ticks.push(t);
  }
  return ticks;
}

/**
 * The ticks of a time axis from t0 to t1 in local time, on calendar
 * boundaries: the smallest step whose labels keep apart, from one second to
 * years (seconds, minutes and hours of the day, then days, months, years).
 * Clock ticks carry the day and the year as a second line; day and month
 * ticks carry the year.
 */
export function timeTicks(
  t0: number,
  t1: number,
  plotWidth: number
): TimeTick[] {
  const span = Math.max(1, t1 - t0);
  const width = Math.max(60, plotWidth);
  const fits = (count: number, sample: string) =>
    count <= 80 &&
    width / Math.max(1, count) >= labelWidth(sample) + TICK_SPACE_PX;
  const year = (t: number) => String(new Date(t).getFullYear());
  const make = (
    values: number[],
    label: (t: number) => string,
    dated: boolean
  ): TimeTick[] =>
    values.map((v) => ({
      v,
      label: label(v),
      date: dated ? dayjs(v).format('MMM D') : '',
      year: year(v),
    }));
  for (const step of SUB_DAY_STEPS_MS) {
    const seconds = step < MINUTE_MS;
    if (!fits(span / step, seconds ? '00:00:00' : '00:00')) continue;
    return make(
      subDayTicks(t0, t1, step),
      (t) => dayjs(t).format(seconds ? 'HH:mm:ss' : 'HH:mm'),
      true
    );
  }
  for (const days of DAY_STEPS) {
    if (!fits(span / (days * DAY_MS), 'Sep 30')) continue;
    return make(dayTicks(t0, t1, days), (t) => dayjs(t).format('MMM D'), false);
  }
  for (const months of MONTH_STEPS) {
    if (!fits(span / (months * 30.44 * DAY_MS), 'Sep')) continue;
    return make(
      monthTicks(t0, t1, months),
      (t) => dayjs(t).format('MMM'),
      false
    );
  }
  for (const years of YEAR_STEPS) {
    const last = years === YEAR_STEPS[YEAR_STEPS.length - 1];
    if (!last && !fits(span / (years * 365.25 * DAY_MS), '2026')) continue;
    return yearTicks(t0, t1, years).map((v) => ({
      v,
      label: year(v),
      date: '',
      year: '',
    }));
  }
  return [];
}

/** A tick that has a place on the axis: where its label is drawn. */
export interface LabelledTick extends TimeTick {
  /** The pixel of the tick line. */
  tickX: number;
  /** The pixel of the centre of the label, kept inside the track. */
  x: number;
  /** The second line (date, year), empty when the label has none. */
  context: string;
  /** The pixel of the centre of the second line. */
  cx: number;
}

function contextText(date: string, year: string, shownYear: string): string {
  if (!date) return year;
  return year && year !== shownYear ? `${date} ${year}` : date;
}

/**
 * The labels to draw, from left to right. A label shows where it keeps
 * LABEL_GAP_PX from the one before; the others are skipped. The first label
 * shows its second line; a later label shows one where the date (or the year)
 * changes and the line has room. The first line gives way to a change that
 * comes too near.
 */
export function layoutLabels(
  ticks: readonly TimeTick[],
  xOf: (t: number) => number,
  width: number
): LabelledTick[] {
  const out: LabelledTick[] = [];
  const centre = (x: number, w: number) =>
    Math.max(w / 2 + 1, Math.min(width - w / 2 - 1, x));
  let lastRight = -Infinity;
  let shown: {
    item: LabelledTick;
    date: string;
    year: string;
    right: number;
  } | null = null;
  let first: LabelledTick | null = null;
  for (const t of ticks) {
    const tickX = xOf(t.v);
    if (!(tickX >= -0.5 && tickX <= width + 0.5)) continue;
    const w = labelWidth(t.label);
    const x = centre(tickX, w);
    if (x - w / 2 < lastRight + LABEL_GAP_PX) continue;
    const item: LabelledTick = { ...t, tickX, x, context: '', cx: x };
    if (
      (!t.date && !t.year) ||
      (shown && t.date === shown.date && t.year === shown.year)
    ) {
      out.push(item);
      lastRight = x + w / 2;
      continue;
    }
    let text = contextText(t.date, t.year, shown ? shown.year : '');
    let cw = labelWidth(text);
    let cx = centre(tickX, cw);
    if (shown && cx - cw / 2 < shown.right + CONTEXT_GAP_PX) {
      if (shown.item !== first) continue;
      first.context = '';
      text = contextText(t.date, t.year, '');
      cw = labelWidth(text);
      cx = centre(tickX, cw);
    }
    out.push(item);
    lastRight = x + w / 2;
    first = shown ? null : item;
    item.context = text;
    item.cx = cx;
    shown = { item, date: t.date, year: t.year, right: cx + cw / 2 };
  }
  return out;
}

// --- Bar layout -------------------------------------------------------------

/** The start time of every run in ms; a run without one takes its neighbour's. */
export function runTimes(
  runs: readonly ApiDAGRunDetails[],
  now: number
): number[] {
  const raw = runs.map((run) => time(runTime(run)));
  const known = raw.find((t) => t !== null);
  if (known === undefined || known === null) return runs.map(() => now);
  let previous: number = known;
  return raw.map((t) => {
    if (t !== null) previous = t;
    return previous;
  });
}

/**
 * Left edges, in whole pixels, of bars that are BAR_PX wide and want to be
 * centred on `centres` (ascending). A bar never overlaps the one before: bars
 * that collide (runs that start in the same pixel) stand side by side, BAR_GAP_PX
 * apart, as a group centred on the mean of their wishes. This is the least
 * squares placement, found by pooling the adjacent bars that collide. The
 * group stays inside [0, width] and keeps the order of the bars.
 */
export function placeBars(centres: readonly number[], width: number): number[] {
  const pitch = BAR_PX + BAR_GAP_PX;
  // With the wish of bar i taken from its slot (i * pitch), no overlap means
  // that the offsets do not decrease: pool the adjacent violators.
  const blocks: { sum: number; count: number }[] = [];
  centres.forEach((centre, i) => {
    blocks.push({ sum: centre - BAR_PX / 2 - i * pitch, count: 1 });
    while (blocks.length > 1) {
      const last = blocks[blocks.length - 1]!;
      const before = blocks[blocks.length - 2]!;
      if (before.sum / before.count <= last.sum / last.count) break;
      before.sum += last.sum;
      before.count += last.count;
      blocks.pop();
    }
  });
  const high = Math.max(0, width - BAR_PX - (centres.length - 1) * pitch);
  const lefts: number[] = [];
  for (const block of blocks) {
    const offset = Math.round(
      Math.min(high, Math.max(0, block.sum / block.count))
    );
    for (let k = 0; k < block.count; k += 1) {
      lefts.push(offset + lefts.length * pitch);
    }
  }
  return lefts;
}

export interface TimelineBar {
  /** Index of the run in the input. */
  index: number;
  /** The start time of the run in ms. */
  at: number;
  /** The pixel of the left edge of the bar, and of its centre. */
  left: number;
  centre: number;
  /** The pixel range that a click or a hover selects this bar in. */
  hitLeft: number;
  hitRight: number;
}

/**
 * The duration extent of a run: a segment from its start to its finish on
 * the time axis of the bars, in a lane.
 */
export interface TimelineExtent {
  id: string;
  /** Index of the run in the input, or -1 for an older run outside it. */
  index: number;
  /**
   * The start and the end of the total time in ms; a running run ends at
   * now. `start` is the own start moved back by the cached time.
   */
  start: number;
  end: number;
  /** The own start of the run in ms. */
  ownStart: number;
  running: boolean;
  /** The pixels of both ends. A run that started before the view starts at 0. */
  x0: number;
  x1: number;
  /** The width of the cached part, at the left end of the extent. */
  cachedPx: number;
  clippedLeft: boolean;
  /** The row, from 0 (top). */
  lane: number;
  /** True for an extent that needed a lane beyond the cap. */
  overflow: boolean;
  /** The IDs of the other runs that overlap this one in time. */
  overlaps: readonly string[];
  /** True for the one extent that carries its duration as text. */
  labelled: boolean;
}

export interface TimelineLayout {
  t0: number;
  t1: number;
  /** The pixel of a time: linear, the same scale for every bar and tick. */
  xOf(t: number): number;
  bars: TimelineBar[];
  ticks: LabelledTick[];
  /** The pixel of `now` when a run in view is running, else null. */
  nowX: number | null;
  /** One extent for each started run in view, then the older runs that reach in. */
  extents: TimelineExtent[];
  /** The number of extents that the lane cap leaves without a lane of their own. */
  hidden: number;
}

/** What changes the layout of the timeline beyond the runs. */
export interface TimelineOptions {
  /** The cached time of a run in ms; none when absent. */
  cachedOf?: (run: ApiDAGRunDetails) => number;
  /** The time range that the axis fits; the runs set the axis when null. */
  range?: { from: number; to: number } | null;
}

/**
 * The layout of `runs` on a time axis of `width` pixels. The x of a run is
 * its start time on a linear scale, so that a pause between runs shows as
 * space. The axis starts at the oldest start and ends at the latest finish
 * (now for a run that runs, or at the newest start); it spans one minute at
 * the least. `older` are runs that the window leaves out: those that end
 * after the axis start reach into the view and get an extent clipped at the
 * left edge. They stretch the axis end like the runs in view. With
 * `options.cachedOf`, the extent of a run begins earlier by its cached time,
 * and the axis, the lanes and the overlaps use these extents. With
 * `options.range`, the axis is that range.
 */
export function timelineLayout(
  runs: readonly ApiDAGRunDetails[],
  now: number,
  width: number,
  older: readonly ApiDAGRunDetails[] = [],
  options: TimelineOptions = {}
): TimelineLayout {
  const cachedOf = options.cachedOf ?? (() => 0);
  const range = options.range ?? null;
  const times = runTimes(runs, now);
  const shown = runs.map((run) => runSpan(run, now, cachedOf(run)));
  let t0 = times.length ? Math.min(...times) : now;
  let t1 = times.length ? Math.max(...times) : now;
  // The cached time comes before the own start: it can lie left of every bar.
  for (const span of shown) if (span) t0 = Math.min(t0, span.start);
  // A range fixes the ends of the axis, so that the empty time stays visible.
  if (range) t0 = range.from;
  const reaching = older
    .map((run) => runSpan(run, now, cachedOf(run)))
    .filter((span): span is RunSpan => span !== null && span.end > t0);
  for (const span of [...shown, ...reaching]) {
    if (span) t1 = Math.max(t1, span.end);
  }
  const running =
    runs.some((run) => run.status === Status.Running) ||
    reaching.some((span) => span.running);
  if (running) t1 = Math.max(t1, now);
  if (range) t1 = range.to;
  if (t1 - t0 < MIN_SPAN_MS) {
    const mid = (t0 + t1) / 2;
    t0 = mid - MIN_SPAN_MS / 2;
    t1 = mid + MIN_SPAN_MS / 2;
  }
  const inner = Math.max(1, width - 2 * EDGE_PAD_PX);
  const xOf = (t: number) => EDGE_PAD_PX + ((t - t0) / (t1 - t0)) * inner;

  // Place the bars in time order, then give every bar its hit range.
  const order = times
    .map((at, index) => ({ at, index }))
    .sort((a, b) => a.at - b.at || a.index - b.index);
  const lefts = placeBars(
    order.map((o) => xOf(o.at)),
    width
  );
  const bars = order.map((o, k): TimelineBar => {
    const left = lefts[k]!;
    return {
      index: o.index,
      at: o.at,
      left,
      centre: left + BAR_PX / 2,
      hitLeft: 0,
      hitRight: width,
    };
  });
  bars.forEach((bar, k) => {
    const before = bars[k - 1];
    const after = bars[k + 1];
    bar.hitLeft = Math.max(
      0,
      bar.centre - HIT_MAX_PX,
      before ? (before.centre + bar.centre) / 2 : 0
    );
    bar.hitRight = Math.min(
      width,
      bar.centre + HIT_MAX_PX,
      after ? (after.centre + bar.centre) / 2 : width
    );
  });
  bars.sort((a, b) => a.index - b.index);

  // The extents: the runs in view, then the older ones that reach in.
  const entries = [
    ...shown.map((span, index) => ({ span, index })),
    ...reaching.map((span) => ({ span, index: -1 })),
  ].filter((e): e is { span: RunSpan; index: number } => e.span !== null);
  const spans = entries.map((e) => e.span);
  const overlaps = overlapIds(spans);
  const pixels = entries.map(({ span }) => {
    const clippedLeft = span.start < t0;
    const x0 = clippedLeft ? 0 : xOf(span.start);
    const x1 = Math.min(width, Math.max(x0 + EXTENT_MIN_PX, xOf(span.end)));
    const cachedPx =
      span.cached > 0
        ? Math.min(x1 - x0, Math.max(0, xOf(span.ownStart) - x0))
        : 0;
    return { id: span.id, x0, x1, cachedPx, clippedLeft };
  });
  const packing = packLanes(pixels);
  const extents = entries.map(({ span, index }, k): TimelineExtent => {
    const { x0, x1, cachedPx, clippedLeft } = pixels[k]!;
    const place = packing.places.get(span.id)!;
    return {
      id: span.id,
      index,
      start: span.start,
      end: span.end,
      ownStart: span.ownStart,
      running: span.running,
      x0,
      x1,
      cachedPx,
      clippedLeft,
      lane: place.lane,
      overflow: place.overflow,
      overlaps: overlaps.get(span.id) ?? [],
      labelled: false,
    };
  });
  // Only the longest extent that spans the view carries its duration: two
  // labels in neighbouring lanes would cover each other.
  const spanning = extents.filter(
    (e) =>
      e.x1 - e.x0 >= EXTENT_LABEL_MIN_PX &&
      (e.clippedLeft || e.x1 - e.x0 >= EXTENT_LABEL_SHARE * inner)
  );
  const widest = spanning.reduce<TimelineExtent | undefined>(
    (best, e) => (!best || e.end - e.start > best.end - best.start ? e : best),
    undefined
  );
  if (widest) widest.labelled = true;

  const ticks = layoutLabels(timeTicks(t0, t1, inner), xOf, width);
  return {
    t0,
    t1,
    xOf,
    bars,
    ticks,
    nowX: running ? xOf(now) : null,
    extents,
    hidden: packing.hidden,
  };
}
