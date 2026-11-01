// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo, useSyncExternalStore } from 'react';
import {
  adjacentVisibleRunId,
  cachedMsOf,
  HISTORY_LIMIT,
  isTestRun,
  parseStoredCount,
  parseStoredHiddenTypes,
  parseStoredMode,
  parseStoredRange,
  parseStoredShowCached,
  parseStoredShowTests,
  RANGE_DEFAULT,
  rangeBounds,
  runInRange,
  runTimeMs,
  timelineRuns,
  triggerOf,
  TRIGGER_TYPES,
  timelineWindow,
  type RunRange,
  type RunViewMode,
} from '../dock/runTimelineModel';
import type { TriggerType } from '@/api/v1/schema';
import { useClockNow } from '@/hooks/useClock';
import type { ApiDAGRunDetails } from './types';
import { useRunHistory } from './useRunHistory';
import { useWorkspaceRun } from './useWorkspaceRun';

export const RUN_COUNT_KEY = 'dagu.workspace.runTimelineCount';
/** Whether the timeline shows the test runs: "1" (the default) or "0". */
export const SHOW_TESTS_KEY = 'dagu.workspace.runTimelineTests';
/** How the timeline picks its runs: "count" (the default) or "range". */
export const RUN_MODE_KEY = 'dagu.workspace.runTimelineMode';
/** The time range, as JSON: a preset, or a custom range with its ends. */
export const RUN_RANGE_KEY = 'dagu.workspace.runTimelineRange';
/** Whether the timeline counts the cached time: "1" (the default) or "0". */
export const SHOW_CACHED_KEY = 'dagu.workspace.runTimelineCached';
/** The trigger types that the timeline hides, as a JSON list. */
export const HIDDEN_TYPES_KEY = 'dagu.workspace.runTimelineHiddenTypes';

/** What the user chose for the runs of the timeline. */
export interface RunViewSettings {
  mode: RunViewMode;
  /** The number of the newest runs, in the 'count' mode. */
  count: number;
  /** The time range, in the 'range' mode. */
  range: RunRange;
  showTests: boolean;
  showCached: boolean;
  /** The trigger types whose runs the timeline hides. */
  hiddenTypes: readonly TriggerType[];
}

export interface RunViewActions {
  /** Shows the `count` newest runs. */
  setCount(count: number): void;
  /** Shows the runs of a time range. */
  setRange(range: RunRange): void;
  setShowTests(show: boolean): void;
  setShowCached(show: boolean): void;
  setHiddenTypes(types: readonly TriggerType[]): void;
}

const STORAGE_KEYS = [
  RUN_COUNT_KEY,
  SHOW_TESTS_KEY,
  RUN_MODE_KEY,
  RUN_RANGE_KEY,
  SHOW_CACHED_KEY,
  HIDDEN_TYPES_KEY,
] as const;

function readKey(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

function writeKey(key: string, value: string): void {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    // Storage is blocked: the choice lasts while a view of it is mounted.
  }
}

// One store for the timeline and the keys, so that they read one choice.
// While a view is mounted the store is the truth; with none, the browser
// storage is read again at each use (and the result stays the same object
// while the stored text does not change).
const listeners = new Set<() => void>();
let current: RunViewSettings | null = null;
let signature = '';

function snapshot(): RunViewSettings {
  if (listeners.size > 0 && current) return current;
  const raws = STORAGE_KEYS.map(readKey);
  const sign = raws
    .map((raw) => (raw === null ? '\u0000' : `1${raw}`))
    .join('\u0001');
  if (current && sign === signature) return current;
  signature = sign;
  current = {
    count: parseStoredCount(raws[0]!),
    showTests: parseStoredShowTests(raws[1]!),
    mode: parseStoredMode(raws[2]!),
    range: parseStoredRange(raws[3]!),
    showCached: parseStoredShowCached(raws[4]!),
    hiddenTypes: parseStoredHiddenTypes(raws[5]!),
  };
  return current;
}

function update(patch: Partial<RunViewSettings>): void {
  current = { ...snapshot(), ...patch };
  // With no view mounted, the next read parses the storage again.
  signature = '\u0002';
  if (patch.count !== undefined) writeKey(RUN_COUNT_KEY, String(patch.count));
  if (patch.showTests !== undefined) {
    writeKey(SHOW_TESTS_KEY, patch.showTests ? '1' : '0');
  }
  if (patch.mode !== undefined) writeKey(RUN_MODE_KEY, patch.mode);
  if (patch.range !== undefined) {
    writeKey(RUN_RANGE_KEY, JSON.stringify(patch.range));
  }
  if (patch.showCached !== undefined) {
    writeKey(SHOW_CACHED_KEY, patch.showCached ? '1' : '0');
  }
  if (patch.hiddenTypes !== undefined) {
    writeKey(HIDDEN_TYPES_KEY, JSON.stringify(patch.hiddenTypes));
  }
  listeners.forEach((listener) => listener());
}

const SERVER_SETTINGS: RunViewSettings = {
  mode: 'count',
  count: 30,
  range: RANGE_DEFAULT,
  showTests: true,
  showCached: true,
  hiddenTypes: [],
};

const ACTIONS: RunViewActions = {
  setCount: (count) => update({ mode: 'count', count }),
  setRange: (range) => update({ mode: 'range', range }),
  setShowTests: (showTests) => update({ showTests }),
  setShowCached: (showCached) => update({ showCached }),
  setHiddenTypes: (hiddenTypes) => update({ hiddenTypes }),
};

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

const serverSnapshot = () => SERVER_SETTINGS;

/** The choices of the run timeline, kept in the browser, and their setters. */
export function useRunViewSettings(): [RunViewSettings, RunViewActions] {
  const settings = useSyncExternalStore(subscribe, snapshot, serverSnapshot);
  return [settings, ACTIONS];
}

/** The runs that a view shows, and how to move between them. */
export interface VisibleRuns {
  /** The runs that the timeline draws, oldest first. */
  runs: ApiDAGRunDetails[];
  /**
   * Every run that the choices let through, oldest first, before the count
   * cuts them: the older ones of it can reach into the view.
   */
  pool: ApiDAGRunDetails[];
  /** The ends of the time range in 'range' mode; null in 'count' mode. */
  range: { from: number; to: number } | null;
  /** The run ID that is selected; null when none is. */
  selectedId: string | null;
  settings: RunViewSettings;
  /** True when the history holds a test run. */
  hasTestRuns: boolean;
  /** The trigger types of the runs of the history (not the test runs). */
  triggerTypes: TriggerType[];
  /** True when a run of the history reused steps of other runs. */
  hasCachedTime: boolean;
  /** True when the range reaches back further than the loaded runs. */
  truncated: boolean;
  isLoading: boolean;
  /** The run next to the selected one: 'older' is to the left. */
  adjacentId(direction: 'older' | 'newer'): string | null;
}

const MINUTE_MS = 60_000;

/** The time of the render, new each minute while `live`. */
function useMinuteClock(live: boolean): number {
  return useClockNow(live ? MINUTE_MS : null);
}

/**
 * The runs that the run timeline shows, and the ones that the arrows of the
 * run chip and the keys `[` and `]` go through. The timeline and the
 * navigation both read this hook, so they never differ. The runs are the
 * history with the test runs, kept when the test runs are shown, and cut by
 * the count of the newest runs or by the time range. `enabled: false` reads
 * nothing. `pollTestRuns` reads the history again while a test run can
 * change it (the timeline only).
 */
export function useVisibleRuns({
  enabled,
  pollTestRuns = false,
}: {
  enabled: boolean;
  pollTestRuns?: boolean;
}): VisibleRuns {
  const run = useWorkspaceRun();
  const history = useRunHistory({
    enabled,
    includeEphemeral: true,
    pollTestRuns,
  });
  const [settings] = useRunViewSettings();
  const preset = settings.range.preset;
  // A custom range has fixed ends, but a running run lasts until now.
  const rangeNow = useMinuteClock(enabled && settings.mode === 'range');

  const { mode, count, range, showTests, showCached, hiddenTypes } = settings;
  const bounds = useMemo(
    () => (mode === 'range' ? rangeBounds(range, rangeNow) : null),
    [mode, range, rangeNow]
  );
  const hasTestRuns = useMemo(
    () => history.runs.some(isTestRun),
    [history.runs]
  );
  const triggerTypes = useMemo(() => {
    const present = new Set(
      history.runs.filter((r) => !isTestRun(r)).map(triggerOf)
    );
    return TRIGGER_TYPES.filter((type) => present.has(type));
  }, [history.runs]);
  const hasCachedTime = useMemo(
    () => history.runs.some((r) => cachedMsOf(r) > 0),
    [history.runs]
  );
  const pool = useMemo(() => {
    // A preset ends now, but its clock ticks once a minute: a run that
    // started after the last tick is in it too.
    const within =
      bounds !== null && preset !== 'custom'
        ? { from: bounds.from, to: Infinity }
        : bounds;
    const kept = history.runs.filter(
      (r) =>
        (isTestRun(r) ? showTests : !hiddenTypes.includes(triggerOf(r))) &&
        (within === null || runInRange(r, rangeNow, within, showCached))
    );
    return timelineRuns(kept);
  }, [
    history.runs,
    showTests,
    hiddenTypes,
    bounds,
    preset,
    rangeNow,
    showCached,
  ]);

  const selection = run.selection;
  // The shown run is the selected one. With 'latest' it is the run of ?run:
  // a test run that is newer than the latest run reads as 'latest' too.
  const selectedId =
    selection === 'latest'
      ? (run.ref?.runId ??
        run.latestRef?.runId ??
        [...pool].reverse().find((r) => !isTestRun(r))?.dagRunId ??
        null)
      : selection.runId;
  const runs = useMemo(
    () => (mode === 'count' ? timelineWindow(pool, selectedId, count) : pool),
    [mode, pool, selectedId, count]
  );

  const oldestLoaded = history.runs[history.runs.length - 1];
  const oldestAt = oldestLoaded ? runTimeMs(oldestLoaded) : null;
  const truncated =
    bounds !== null &&
    history.runs.length >= HISTORY_LIMIT &&
    oldestAt !== null &&
    bounds.from < oldestAt;

  const details = run.details;
  const selectedAt =
    details && details.dagRunId === selectedId ? runTimeMs(details) : null;
  return {
    runs,
    pool,
    range: bounds,
    selectedId,
    settings,
    hasTestRuns,
    triggerTypes,
    hasCachedTime,
    truncated,
    isLoading: history.isLoading,
    adjacentId: (direction) =>
      adjacentVisibleRunId(runs, selectedId, direction, selectedAt),
  };
}
