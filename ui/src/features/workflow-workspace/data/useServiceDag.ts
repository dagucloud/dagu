// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext, useEffect, useMemo, useRef } from 'react';
import type { components } from '@/api/v1/schema';
import { useServiceDAGs } from '@/features/service-dags/hooks/useServiceDAGs';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { useDAGHistorySSE } from '@/hooks/useDAGHistorySSE';
import { isServiceDAG } from '@/lib/dagSchedule';
import { runTimeMs } from '../dock/runTimelineModel';
import { useWorkspaceData, WorkspaceDataContext } from './context';

/**
 * True when the workflow of the page is a service DAG (restart_policy).
 * False outside a workspace page.
 */
export function useIsServiceDag(): boolean {
  const dag = useContext(WorkspaceDataContext)?.dagData.dag;
  return !!dag && isServiceDAG(dag);
}

/**
 * The service DAG of the page with its state, from the same read as the
 * Workflows list (one request for both). It is undefined for a workflow that
 * is not a service DAG, and while the list does not have the DAG.
 */
export function useServiceDag(
  fileName: string
): components['schemas']['ServiceDAG'] | undefined {
  const isService = useIsServiceDag();
  const { serviceDAGs } = useServiceDAGs({ enabled: isService });
  return serviceDAGs.find((dag) => dag.fileName === fileName);
}

/**
 * A service DAG starts a run each second at the most, and each run sends an
 * event. Its runs are read at most once in this time (spec 100).
 */
export const SERVICE_READ_GAP_MS = 10_000;

interface ReadGate {
  /** When the last read of the list ended, in ms. */
  last: number;
  timer: ReturnType<typeof setTimeout> | null;
  /** When the pending read is due, in ms. */
  due: number;
}

// One gate for each list, so that every reader of it shares the gap.
const readGates = new Map<string, ReadGate>();

function gateOf(key: string): ReadGate {
  let gate = readGates.get(key);
  if (!gate) {
    gate = { last: 0, timer: null, due: 0 };
    readGates.set(key, gate);
  }
  return gate;
}

/** Notes that a read of the list ended now, so that the gap counts from it. */
export function noteRead(key: string): void {
  gateOf(key).last = Date.now();
}

/**
 * Reads now when the last read is older than the gap. Else it reads once
 * when the gap ends, whatever the count of calls before that. A list has
 * one pending read: the earliest that a caller asked for.
 */
export function gatedRead(
  key: string,
  read: () => Promise<unknown>,
  gap: number = SERVICE_READ_GAP_MS
): Promise<unknown> {
  const gate = gateOf(key);
  const wait = gate.last + gap - Date.now();
  if (wait <= 0) {
    if (gate.timer) clearTimeout(gate.timer);
    gate.timer = null;
    gate.last = Date.now();
    return read();
  }
  const due = Date.now() + wait;
  if (gate.timer && gate.due <= due) return Promise.resolve(undefined);
  if (gate.timer) clearTimeout(gate.timer);
  gate.due = due;
  gate.timer = setTimeout(() => {
    gate.timer = null;
    gate.last = Date.now();
    void read();
  }, wait);
  return Promise.resolve(undefined);
}

/** Forgets the gaps between the reads of a service DAG (for tests). */
export function resetReadGates(): void {
  readGates.forEach((gate) => gate.timer && clearTimeout(gate.timer));
  readGates.clear();
}

/** The most runs that one page of the history holds. */
const RETAINED_LIMIT = 100;
/** The retained runs change slowly: one read in 30 s is enough. */
export const RETAINED_READ_MS = 30_000;

/** The runs that the server still keeps for the workflow. */
export interface RetainedRuns {
  /** The IDs of the retained runs that the last read listed. */
  ids: ReadonlySet<string>;
  /**
   * The start of the oldest retained run, in ms. null while unknown: before
   * the first read, or when the DAG keeps more runs than one page holds.
   */
  oldestAt: number | null;
  /**
   * The start of the oldest run that the last read listed, in ms; null
   * before the first read. A run that started later is retained too.
   */
  oldestListedAt: number | null;
}

/**
 * Whether one page holds the oldest retained run, or a run next to it. A DAG
 * that keeps at most a page of runs has one more run only while a run is in
 * flight: the oldest run of the page is then at most one run off.
 */
function pageHoldsOldest(
  nextCursor: string | undefined,
  dag: { histRetentionRuns?: number; histRetentionDays?: number } | undefined
): boolean {
  if (!nextCursor) return true;
  const runs = dag?.histRetentionRuns ?? 0;
  return runs > 0 && runs <= RETAINED_LIMIT && !dag?.histRetentionDays;
}

const NONE: RetainedRuns = {
  ids: new Set(),
  oldestAt: null,
  oldestListedAt: null,
};

/**
 * The retained runs of a service DAG, for the notice and for the links of
 * the events. A DAG whose history feed is connected reads them when the
 * panel opens, when `windowKey` changes (at most once in
 * SERVICE_READ_GAP_MS), and after an event of the feed (at most once in
 * RETAINED_READ_MS: each run sends an event). Without the feed, and for a
 * DAG that is not a service, it reads each RETAINED_READ_MS while `poll` is
 * true. It reads nothing while `enabled` is false.
 */
export function useRetainedRuns(
  enabled: boolean,
  windowKey?: string,
  poll = true
): RetainedRuns {
  const { fileName, remoteNode, dagData } = useWorkspaceData('useRetainedRuns');
  const dag = dagData.dag;
  const service = useIsServiceDag();
  const active = enabled && fileName !== '';
  const feed = useDAGHistorySSE(fileName, active && service, remoteNode);
  const fed = service && feed.isConnected;
  const gateKey = `${remoteNode}\n${fileName}\nretained`;
  const { data, mutate } = useQuery(
    '/dags/{fileName}/dag-runs',
    whenEnabled(active, {
      params: {
        path: { fileName },
        query: { remoteNode, limit: RETAINED_LIMIT },
      },
    }),
    {
      keepPreviousData: true,
      refreshInterval: fed || !poll ? 0 : RETAINED_READ_MS,
      revalidateOnFocus: false,
      revalidateOnReconnect: false,
      onSuccess: () => noteRead(gateKey),
    }
  );
  // The first message of the feed is the state at the opening.
  const opened = useRef(false);
  const events = feed.data;
  useEffect(() => {
    if (!fed || events == null) return;
    if (!opened.current) {
      opened.current = true;
      return;
    }
    void gatedRead(gateKey, () => mutate(), RETAINED_READ_MS);
  }, [fed, events, gateKey, mutate]);
  const firstWindow = useRef(true);
  useEffect(() => {
    if (firstWindow.current) {
      firstWindow.current = false;
      return;
    }
    if (service && active) {
      void gatedRead(gateKey, () => mutate(), SERVICE_READ_GAP_MS);
    }
    // Only a new window reads.
  }, [windowKey]);
  return useMemo(() => {
    const runs = data?.dagRuns;
    if (!runs) return NONE;
    const oldest = runs[runs.length - 1];
    const oldestListedAt = oldest ? runTimeMs(oldest) : null;
    return {
      ids: new Set(runs.map((run) => run.dagRunId)),
      oldestAt: pageHoldsOldest(data.nextCursor, dag) ? oldestListedAt : null,
      oldestListedAt,
    };
  }, [data, dag]);
}
