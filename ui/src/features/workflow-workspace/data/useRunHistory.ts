// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  useSyncExternalStore,
} from 'react';
import type { components } from '@/api/v1/schema';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { useDAGHistorySSE } from '@/hooks/useDAGHistorySSE';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';
import { isRunActive } from '@/features/visual-editor/run/stepRunInfo';
import { runTimeMs } from '../dock/runTimelineModel';
import { useWorkspaceData } from './context';
import { testRunExpiry } from './testRun';
import {
  gatedRead,
  noteRead,
  SERVICE_READ_GAP_MS,
  useIsServiceDag,
} from './useServiceDag';
import type { ApiDAGRunDetails } from './types';

export { resetReadGates, SERVICE_READ_GAP_MS } from './useServiceDag';

type DAGGridItem = components['schemas']['DAGGridItem'];

export interface RunHistory {
  /** The runs, newest first. With `includeEphemeral`, the test runs too. */
  runs: ApiDAGRunDetails[];
  gridData: DAGGridItem[];
  /** The cursor of the next page of older runs; null when none remain. */
  nextCursor: string | null;
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

/**
 * How many more times, and how far apart, the history is read when an ended
 * test run is still listed after its removal or the read failed.
 */
const REMOVAL_RETRIES = 3;
const REMOVAL_RETRY_MS = 60_000;

/**
 * The events of the feed that already read a history with test runs again.
 * Every reader of a feed gets the same event object, so the first one reads
 * the list for all.
 */
const revalidatedEvents = new WeakSet<object>();

/** A poll of a service DAG keeps the gap too. */
function spaced(service: boolean, ms: number): number {
  return service && ms > 0 ? Math.max(ms, SERVICE_READ_GAP_MS) : ms;
}

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
 * The run history of the workflow. Without `includeEphemeral` it has the
 * plain SWR key of the history query, which the history SSE feed fills. With
 * it, the server also lists the test runs (they leave no trace elsewhere):
 * the query has a key of its own and the SSE feed cannot fill it, so an
 * event only revalidates it. With `pollTestRuns`, the list is read again every
 * `TEST_RUN_POLL_MS` while a test run in it is active or this tab started one
 * a moment ago. The server drops an ended test run without any event, so the
 * list is read once more when the first one is gone, and a few more times
 * while it is still listed. Disabled, it makes no request.
 */
export function useRunHistory({
  enabled,
  includeEphemeral = false,
  pollTestRuns = false,
  newestOnly = false,
}: {
  enabled: boolean;
  includeEphemeral?: boolean;
  pollTestRuns?: boolean;
  /**
   * The caller uses only the newest page. While the feed of a service DAG is
   * connected, its runs come from the feed, with the test runs of the last
   * read, and an event does not read the list again. The older pages need a
   * cursor that the feed does not give: they keep the reads.
   */
  newestOnly?: boolean;
}): RunHistory {
  const { fileName, remoteNode, registerHistory, standaloneRun } =
    useWorkspaceData('useRunHistory');
  // A run without its workflow file has no history to read: it is its own.
  const standaloneRuns = useMemo(
    () => (standaloneRun ? [standaloneRun] : null),
    [standaloneRun]
  );
  const active = enabled && fileName !== '' && standaloneRuns === null;
  const historySSE = useDAGHistorySSE(fileName, active, remoteNode);
  const startedAt = useTestRunStartedAt(fileName);
  const service = useIsServiceDag();
  const gateKey = `${remoteNode}\n${fileName}\n${includeEphemeral}`;
  const base = sseFallbackOptions(historySSE);
  const baseInterval = spaced(service, base.refreshInterval);
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
        ? spaced(service, TEST_RUN_POLL_MS)
        : baseInterval,
    [startedAt, baseInterval, holdsActiveTestRun, service]
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
      refreshInterval: baseInterval,
      ...(includeEphemeral && pollTestRuns
        ? { refreshInterval: testRunInterval }
        : {}),
      ...(service
        ? {
            focusThrottleInterval: SERVICE_READ_GAP_MS,
            // A read that SWR makes by itself counts for the gap too.
            onSuccess: () => {
              noteRead(gateKey);
            },
          }
        : {}),
    }
  );
  // An event or a refresh that asks for a read: a service DAG keeps the gap.
  const read = useCallback(
    () => (service ? gatedRead(gateKey, () => mutate()) : mutate()),
    [service, gateKey, mutate]
  );
  const holds = data?.dagRuns?.some(isActiveTestRun) ?? false;
  if (holds !== holdsActiveTestRun) setHoldsActiveTestRun(holds);
  const removal =
    includeEphemeral && pollTestRuns
      ? firstTestRunRemoval(data?.dagRuns)
      : null;
  useEffect(() => {
    if (removal === null) return;
    // A read that drops the run changes the removal and cancels the rest.
    const due = Math.max(0, removal - Date.now());
    const timers = Array.from({ length: 1 + REMOVAL_RETRIES }, (_, i) =>
      setTimeout(() => void read(), due + i * REMOVAL_RETRY_MS)
    );
    return () => timers.forEach(clearTimeout);
  }, [removal, read]);
  // The feed has no test run: it must not replace the list that has them.
  useSSECacheSync(
    historySSE,
    (includeEphemeral ? noSync : mutate) as typeof mutate
  );
  const events = historySSE.data;
  const fromFeed =
    newestOnly && includeEphemeral && service && historySSE.isConnected;
  useEffect(() => {
    // An event of the feed means that the history changed: read it again.
    if (
      !includeEphemeral ||
      fromFeed ||
      events == null ||
      revalidatedEvents.has(events)
    ) {
      return;
    }
    revalidatedEvents.add(events);
    void read();
  }, [events, includeEphemeral, fromFeed, read]);

  // Registered again with each committed list: a render that React drops
  // must not show its runs.
  const fed = fromFeed ? events?.dagRuns : undefined;
  const listed = data?.dagRuns;
  const runs = useMemo(
    () =>
      standaloneRuns ?? (fed ? withTestRuns(fed, listed) : listed) ?? NO_RUNS,
    [standaloneRuns, fed, listed]
  );
  useEffect(() => {
    if (standaloneRuns) {
      return enabled
        ? registerHistory(noSync, () => standaloneRuns)
        : undefined;
    }
    if (!active) return;
    return registerHistory(read, () => runs);
  }, [active, enabled, standaloneRuns, read, registerHistory, runs]);

  return {
    runs,
    gridData: standaloneRuns ? NO_GRID : (data?.gridData ?? NO_GRID),
    nextCursor: standaloneRuns ? null : (data?.nextCursor ?? null),
    isLoading: active && isLoading,
  };
}

const timeOf = (run: ApiDAGRunDetails) =>
  runTimeMs(run) ?? Number.MAX_SAFE_INTEGER;

/** The runs of the feed and the test runs of the last read, newest first. */
function withTestRuns(
  fed: ApiDAGRunDetails[],
  listed: ApiDAGRunDetails[] | undefined
): ApiDAGRunDetails[] {
  const tests = listed?.filter((r) => r.ephemeral);
  return tests?.length
    ? [...fed, ...tests].sort((a, b) => timeOf(b) - timeOf(a))
    : fed;
}

// A mutator that does nothing, for a query that the SSE feed cannot fill.
const noSync = () => Promise.resolve(undefined);

/** Forgets the test runs that this tab started (for tests). */
export function resetTestRunsStarted(): void {
  startedTestRuns.clear();
}
