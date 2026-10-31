// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useState, useSyncExternalStore } from 'react';
import type { components } from '@/api/v1/schema';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { useDAGHistorySSE } from '@/hooks/useDAGHistorySSE';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';
import { isRunActive } from '@/features/visual-editor/run/stepRunInfo';
import { useWorkspaceData } from './context';
import { testRunExpiry } from './testRun';
import type { ApiDAGRunDetails } from './types';

type DAGGridItem = components['schemas']['DAGGridItem'];

export interface RunHistory {
  /** The runs, newest first. With `includeEphemeral`, the test runs too. */
  runs: ApiDAGRunDetails[];
  gridData: DAGGridItem[];
  isLoading: boolean;
}

const NO_RUNS: ApiDAGRunDetails[] = [];
const NO_GRID: DAGGridItem[] = [];

/**
 * When this tab last started a test run, in ms, by workflow file. The SSE
 * feed does not carry test runs: the timeline polls the history for a while
 * after a start, until the started run shows in it.
 */
const startedTestRuns = new Map<string, number>();
const startedListeners = new Set<() => void>();

/** Records that this tab started a test run of the workflow. */
export function noteTestRunStarted(fileName: string): void {
  startedTestRuns.set(fileName, Date.now());
  startedListeners.forEach((listener) => listener());
}

/** When this tab last started a test run of the workflow, in ms. */
export function useTestRunStartedAt(fileName: string): number | undefined {
  return useSyncExternalStore(
    (listener) => {
      startedListeners.add(listener);
      return () => {
        startedListeners.delete(listener);
      };
    },
    () => startedTestRuns.get(fileName),
    () => undefined
  );
}

/** How often the history with test runs is read while a test run is active. */
export const TEST_RUN_POLL_MS = 5000;

/** How long after a start the history is polled for a run it does not list yet. */
const START_GRACE_MS = 30_000;

/**
 * How long after the TTL of an ended test run the history is read again. The
 * server removes ended test runs once a minute; the rest covers clock skew.
 */
export const TEST_RUN_REMOVAL_SLACK_MS = 70_000;

const isActiveTestRun = (run: ApiDAGRunDetails) =>
  !!run.ephemeral && isRunActive(run);

/**
 * When the first ended test run of the list is gone from the server, in ms
 * since the epoch, or null without one.
 */
function firstTestRunRemoval(
  runs: ApiDAGRunDetails[] | undefined
): number | null {
  let first: number | null = null;
  for (const run of runs ?? []) {
    if (!run.ephemeral || isRunActive(run)) continue;
    const expiry = testRunExpiry(run);
    if (expiry !== null && (first === null || expiry < first)) first = expiry;
  }
  return first === null ? null : first + TEST_RUN_REMOVAL_SLACK_MS;
}

/**
 * The run history of the workflow. Without `includeEphemeral` it has the SWR
 * key of DAGExecutionHistory, so both share one cache entry. With it, the
 * server also lists the test runs (they leave no trace elsewhere): the query
 * has a key of its own and the SSE feed cannot fill it, so an event only
 * revalidates it. With `pollTestRuns`, the list is read again every
 * `TEST_RUN_POLL_MS` while a test run in it is active or this tab started one
 * a moment ago. The server drops an ended test run without any event, so the
 * list is read once more when the first one is gone. Disabled, it makes no
 * request.
 */
export function useRunHistory({
  enabled,
  includeEphemeral = false,
  pollTestRuns = false,
}: {
  enabled: boolean;
  includeEphemeral?: boolean;
  pollTestRuns?: boolean;
}): RunHistory {
  const { fileName, remoteNode, registerHistory } =
    useWorkspaceData('useRunHistory');
  const active = enabled && fileName !== '';
  const historySSE = useDAGHistorySSE(fileName, active, remoteNode);
  const startedAt = useTestRunStartedAt(fileName);
  const base = sseFallbackOptions(historySSE);
  const baseInterval = base.refreshInterval;
  // SWR restarts its poll timer when this function changes, so it changes
  // only when the poll can: a list that gains an active test run
  // (holdsActiveTestRun) must restart a poll that stopped. A new function at
  // each render would hold the poll off while the caller renders more often
  // than the poll.
  const [holdsActiveTestRun, setHoldsActiveTestRun] = useState(false);
  const testRunInterval = useCallback(
    (latest?: { dagRuns?: ApiDAGRunDetails[] }) =>
      (startedAt !== undefined && Date.now() - startedAt < START_GRACE_MS) ||
      latest?.dagRuns?.some(isActiveTestRun)
        ? TEST_RUN_POLL_MS
        : baseInterval,
    [startedAt, baseInterval, holdsActiveTestRun]
  );
  const { data, isLoading, mutate } = useQuery(
    '/dags/{fileName}/dag-runs',
    whenEnabled(active, {
      params: {
        query: includeEphemeral
          ? { remoteNode, includeEphemeral: true }
          : { remoteNode },
        path: { fileName },
      },
    }),
    {
      ...base,
      ...(includeEphemeral && pollTestRuns
        ? { refreshInterval: testRunInterval }
        : {}),
    }
  );
  const holds = data?.dagRuns?.some(isActiveTestRun) ?? false;
  if (holds !== holdsActiveTestRun) setHoldsActiveTestRun(holds);
  const removal =
    includeEphemeral && pollTestRuns
      ? firstTestRunRemoval(data?.dagRuns)
      : null;
  useEffect(() => {
    if (removal === null) return;
    const timer = setTimeout(
      () => void mutate(),
      Math.max(0, removal - Date.now())
    );
    return () => clearTimeout(timer);
  }, [removal, mutate]);
  // The feed has no test run: it must not replace the list that has them.
  useSSECacheSync(
    historySSE,
    (includeEphemeral ? noSync : mutate) as typeof mutate
  );
  const events = historySSE.data;
  useEffect(() => {
    // An event of the feed means that the history changed: read it again.
    if (includeEphemeral && events != null) void mutate();
  }, [events, includeEphemeral, mutate]);

  useEffect(() => {
    if (!active) return;
    return registerHistory(() => mutate());
  }, [active, mutate, registerHistory]);

  return {
    runs: data?.dagRuns ?? NO_RUNS,
    gridData: data?.gridData ?? NO_GRID,
    isLoading: active && isLoading,
  };
}

// A mutator that does nothing, for a query that the SSE feed cannot fill.
const noSync = () => Promise.resolve(undefined);

/** Forgets the test runs that this tab started (for tests). */
export function resetTestRunsStarted(): void {
  startedTestRuns.clear();
}
