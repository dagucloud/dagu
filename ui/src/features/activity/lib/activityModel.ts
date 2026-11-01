// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  type DAGRunActivityQueueBlockedReason,
  Status,
  type components,
} from '@/api/v1/schema';

export type DAGRunActivity = components['schemas']['DAGRunActivity'];
export type ActivityRun = components['schemas']['DAGRunActivityRun'];
export type ActivityQueue = components['schemas']['DAGRunActivityQueue'];

/** A time range in Unix seconds. */
export type TimeRange = { from: number; to: number };

/** One workflow's loaded runs, newest first. */
export type ActivityLane = {
  name: string;
  runs: ActivityRun[];
  latest: ActivityRun;
  /** Failed or rejected runs among the loaded ones. */
  failedCount: number;
};

/** Statuses that leave a workflow needing a look when its latest run ends so. */
const UNSUCCESSFUL = new Set<Status>([
  Status.Failed,
  Status.Rejected,
  Status.PartialSuccess,
  Status.Aborted,
]);

const IN_PROGRESS = new Set<Status>([
  Status.Running,
  Status.Queued,
  Status.Waiting,
]);

/** A running run is flagged once it takes this many times its usual time. */
export const SLOW_RUN_FACTOR = 2;
/** Successful runs needed before a workflow's usual time is trusted. */
const MIN_USUAL_SAMPLES = 3;

export type AttentionItem =
  | { kind: 'failing'; name: string; run: ActivityRun }
  | { kind: 'waiting'; name: string; run: ActivityRun }
  | { kind: 'slow'; name: string; run: ActivityRun; usualSeconds: number };

/** A problem of the system, not of one run, that keeps work from running. */
export type SystemAlert =
  | { kind: 'scheduler' }
  | { kind: 'queue'; queue: string; reason: DAGRunActivityQueueBlockedReason }
  | { kind: 'disk'; usedPercent: number };

function newestFirst(left: ActivityRun, right: ActivityRun): number {
  return (right.startedAt ?? 0) - (left.startedAt ?? 0);
}

/** Groups runs, listed newest first, into one lane per workflow. */
export function groupLanes(runs: ActivityRun[]): ActivityLane[] {
  const lanes = new Map<string, ActivityLane>();
  for (const run of runs) {
    const lane = lanes.get(run.name);
    if (lane) {
      lane.runs.push(run);
    } else {
      lanes.set(run.name, {
        name: run.name,
        runs: [run],
        latest: run,
        failedCount: 0,
      });
    }
  }
  for (const lane of lanes.values()) {
    lane.failedCount = lane.runs.filter(
      (run) => run.status === Status.Failed || run.status === Status.Rejected
    ).length;
  }
  return [...lanes.values()];
}

/** Median duration of each workflow's successful runs, in seconds. */
function usualSeconds(runs: ActivityRun[]): Map<string, number> {
  const durations = new Map<string, number[]>();
  for (const run of runs) {
    if (
      run.status !== Status.Success ||
      run.startedAt === undefined ||
      run.finishedAt === undefined ||
      run.finishedAt < run.startedAt
    ) {
      continue;
    }
    const list = durations.get(run.name) ?? [];
    list.push(run.finishedAt - run.startedAt);
    durations.set(run.name, list);
  }
  const usual = new Map<string, number>();
  for (const [name, list] of durations) {
    if (list.length < MIN_USUAL_SAMPLES) continue;
    list.sort((left, right) => left - right);
    usual.set(name, list[Math.floor(list.length / 2)]!);
  }
  return usual;
}

/**
 * Lists what needs a person: workflows whose latest run did not succeed,
 * runs waiting for input, and runs taking much longer than the loaded runs
 * of their workflow usually take.
 */
export function attentionItems(
  activity: DAGRunActivity,
  runs: ActivityRun[],
  nowSeconds: number
): AttentionItem[] {
  const failing = activity.attention.map<AttentionItem>((run) => ({
    kind: 'failing',
    name: run.name,
    run,
  }));

  const waiting = activity.active
    .filter((run) => run.status === Status.Waiting)
    .map<AttentionItem>((run) => ({ kind: 'waiting', name: run.name, run }));

  const usual = usualSeconds(runs);
  const slow: AttentionItem[] = [];
  for (const run of activity.active) {
    const seconds = usual.get(run.name);
    if (
      run.status !== Status.Running ||
      !seconds ||
      run.startedAt === undefined
    ) {
      continue;
    }
    if (nowSeconds - run.startedAt > seconds * SLOW_RUN_FACTOR) {
      slow.push({ kind: 'slow', name: run.name, run, usualSeconds: seconds });
    }
  }

  return [...failing, ...waiting, ...slow];
}

/**
 * Runs that are running, and queued runs no queue line covers, such as runs
 * of a queue past the listed ones. Waiting runs are listed as attention.
 */
export function inProgressRuns(activity: DAGRunActivity): ActivityRun[] {
  const summarized = new Set(activity.queues.map((queue) => queue.name));
  return activity.active.filter(
    (run) =>
      run.status === Status.Running ||
      (run.status === Status.Queued &&
        !(run.queue !== undefined && summarized.has(run.queue)))
  );
}

/** A queue's next run counts as stuck once it has waited this long. */
export const STALLED_WAIT_SECONDS = 5 * 60;

/**
 * Reports whether a queue looks stuck: its next run has waited long while
 * the queue has room to run it.
 */
export function isQueueStalled(
  queue: ActivityQueue,
  nowSeconds: number
): boolean {
  if (queue.nextQueuedAt === undefined) return false;
  // A running count that is a lower bound cannot show room.
  const hasRoom =
    queue.maxConcurrency === undefined ||
    (!queue.runningCapped && queue.running < queue.maxConcurrency);
  return hasRoom && nowSeconds - queue.nextQueuedAt > STALLED_WAIT_SECONDS;
}

/**
 * Reports whether a stopped scheduler is holding work back: scheduled or
 * queued runs exist, and no scheduler is running to start them. A node that
 * runs without schedules or queues is not flagged.
 */
export function isSchedulerHoldingWork(
  activity: DAGRunActivity,
  schedulerRunning: boolean | undefined
): boolean {
  if (schedulerRunning !== false) return false;
  return (
    activity.upcoming.length > 0 ||
    activity.active.some((run) => run.status === Status.Queued) ||
    activity.queues.some((queue) => queue.queued > 0 || !!queue.queuedCapped)
  );
}

/**
 * Lists the system problems that keep work from running: a stopped scheduler
 * holding work back, queues whose runs no worker can take, and a nearly full
 * data disk.
 */
export function systemAlerts(
  activity: DAGRunActivity,
  schedulerRunning: boolean | undefined
): SystemAlert[] {
  const alerts: SystemAlert[] = [];
  if (isSchedulerHoldingWork(activity, schedulerRunning)) {
    alerts.push({ kind: 'scheduler' });
  }
  for (const queue of activity.queues) {
    if (queue.blockedReason) {
      alerts.push({
        kind: 'queue',
        queue: queue.name,
        reason: queue.blockedReason,
      });
    }
  }
  if (activity.diskUsedPercent !== undefined) {
    alerts.push({ kind: 'disk', usedPercent: activity.diskUsedPercent });
  }
  return alerts;
}

function laneRank(lane: ActivityLane, activeNames: Set<string>): number {
  if (UNSUCCESSFUL.has(lane.latest.status)) return 0;
  if (
    activeNames.has(lane.name) ||
    lane.runs.some((run) => IN_PROGRESS.has(run.status))
  ) {
    return 1;
  }
  return 2;
}

/**
 * Orders lanes so unresolved failures come first, then workflows with runs
 * in progress, then the most recently active.
 */
export function sortLanes(
  lanes: ActivityLane[],
  active: ActivityRun[]
): ActivityLane[] {
  const activeNames = new Set(active.map((run) => run.name));
  return [...lanes].sort(
    (left, right) =>
      laneRank(left, activeNames) - laneRank(right, activeNames) ||
      newestFirst(left.latest, right.latest) ||
      left.name.localeCompare(right.name)
  );
}

/** Shortest range the lanes zoom out to when activity is sparse. */
const MIN_FIT_SECONDS = 60 * 60;
/** Empty time kept before the first run when fitting the range. */
const FIT_LEAD_SECONDS = 15 * 60;

/**
 * Adds in-progress runs that began before the window to the loaded runs. The
 * window lists only runs created inside it, so a long run still going would
 * otherwise have no lane. Those runs are older than every loaded run, so the
 * result stays newest first.
 */
export function withEarlierActiveRuns(
  runs: ActivityRun[],
  active: ActivityRun[],
  windowFrom: number
): ActivityRun[] {
  // A run ID is unique only within its workflow.
  const key = (run: ActivityRun) => JSON.stringify([run.name, run.dagRunId]);
  const loaded = new Set(runs.map(key));
  const earlier = active
    .filter(
      (run) =>
        run.startedAt !== undefined &&
        run.startedAt < windowFrom &&
        !loaded.has(key(run))
    )
    .sort(newestFirst);
  return earlier.length === 0 ? runs : [...runs, ...earlier];
}

/** Start time of the oldest run, or undefined when none has one. */
export function oldestStart(runs: ActivityRun[]): number | undefined {
  let oldest: number | undefined;
  for (const run of runs) {
    if (
      run.startedAt !== undefined &&
      (oldest === undefined || run.startedAt < oldest)
    ) {
      oldest = run.startedAt;
    }
  }
  return oldest;
}

/**
 * Narrows the window to the part that has runs, keeping some empty time
 * before the first run and at least an hour of span.
 */
export function fitRange(window: TimeRange, runs: ActivityRun[]): TimeRange {
  const first = Math.max(window.from, oldestStart(runs) ?? window.to);
  const from = Math.max(
    window.from,
    Math.min(first - FIT_LEAD_SECONDS, window.to - MIN_FIT_SECONDS)
  );
  return { from, to: window.to };
}

const TICK_STEPS = [
  60, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400,
];
const MIN_TICK_SPACING_PX = 72;

/**
 * Returns tick times for an axis of the given width, aligned to whole steps
 * in the server's time zone.
 */
export function timeTicks(
  range: TimeRange,
  widthPx: number,
  tzOffsetSeconds = 0
): number[] {
  const span = range.to - range.from;
  if (span <= 0 || widthPx <= 0) return [];
  const maxTicks = Math.max(1, Math.floor(widthPx / MIN_TICK_SPACING_PX));
  const step =
    TICK_STEPS.find((candidate) => span / candidate <= maxTicks) ??
    TICK_STEPS[TICK_STEPS.length - 1]!;
  const ticks: number[] = [];
  const firstLocal =
    Math.ceil((range.from + tzOffsetSeconds) / step) * step - tzOffsetSeconds;
  for (let tick = firstLocal; tick <= range.to; tick += step) {
    ticks.push(tick);
  }
  return ticks;
}

/** Position of a time inside a range, as a percentage. */
export function percentOf(range: TimeRange, seconds: number): number {
  return ((seconds - range.from) / (range.to - range.from)) * 100;
}

/**
 * Returns the time a run's bar ends at, in Unix seconds: when it finished, or
 * the current time while it is still running. Other runs end where they start.
 */
export function runEnd(run: ActivityRun, nowSeconds: number): number {
  const start = run.startedAt ?? 0;
  // A waiting run keeps the time it paused at as its finish time.
  if (run.status === Status.Running || run.status === Status.Waiting) {
    return Math.max(start, nowSeconds);
  }
  if (run.finishedAt !== undefined) return Math.max(start, run.finishedAt);
  return start;
}

/** Visible part of a run's bar inside the range, as percentages from 0 to 100. */
export function barSpan(
  run: ActivityRun,
  range: TimeRange,
  nowSeconds: number
): { left: number; right: number } {
  return {
    left: Math.max(0, percentOf(range, run.startedAt ?? range.from)),
    right: Math.min(100, percentOf(range, runEnd(run, nowSeconds))),
  };
}
