// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import type { NodeStatus } from '@/api/v1/schema';
import { updateDAGRunNodeStatus } from '@/features/dags/lib/nodeStatus';
import { buildRunOverlay } from '@/features/visual-editor/run/runOverlay';
import { isRunActive } from '@/features/visual-editor/run/stepRunInfo';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { useDAGRunSSE } from '@/hooks/useDAGRunSSE';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';
import { isNotFoundQueryError } from '@/lib/queryErrors';
import { RUN_REDIRECT_STATE } from '../state/workspaceUrl';
import { useWorkspaceData } from './context';
import { runHandlers } from './handlerMapping';
import { sameContent } from './sameContent';
import {
  firstFailedStep,
  isNeverRun,
  isNewerRun,
  runCreatedAt,
  nodesByStep,
  waitingCounts,
} from './runFacts';
import type {
  ApiDAGRunDetails,
  DAGPageData,
  RunRef,
  RunSelection,
  WorkspaceRunValue,
} from './types';

/** How often a selected test run is read while it is active. */
const TEST_RUN_READ_MS = 2000;

/** The selected run of the workspace. Needs WorkspaceDataProvider. */
export function useWorkspaceRun(): WorkspaceRunValue {
  return useWorkspaceData('useWorkspaceRun').run;
}

const RUN_PARAM = 'run';

interface NodePatch {
  selKey: string;
  /** The server details that the patch was made from. */
  base: ApiDAGRunDetails;
  patched: ApiDAGRunDetails;
}

export interface SelectedRun {
  run: Omit<WorkspaceRunValue, 'refresh'>;
  /** Revalidates the pinned run; null while the latest run is selected. */
  mutatePinned: (() => Promise<unknown>) | null;
}

/**
 * The run state behind WorkspaceDataProvider. The URL always names the
 * shown run: without ?run the page replaces the URL with ?run=<latest run>
 * as soon as the latest run is known, and a run that starts later does not
 * move the page. The latest run comes from the DAG query of the page (kept
 * live by the DAG SSE); any other run has its own query and SSE.
 */
export function useSelectedRun({
  dagData,
  remoteNode,
}: {
  dagData: DAGPageData;
  remoteNode: string;
}): SelectedRun {
  const [searchParams, setSearchParams] = useSearchParams();
  const runParam = searchParams.get(RUN_PARAM) || null;
  // A legacy history link (?idx) names its run itself, a little later.
  const legacyIdx = searchParams.has('idx');
  const dagName = dagData.dag?.name ?? '';
  const [notFound, setNotFound] = useState(false);

  const latest = dagData.latestDAGRun;
  const latestDetails = isNeverRun(latest) ? null : latest;
  const latestRunId = latestDetails?.dagRunId ?? null;
  const latestStatus = latestDetails?.status;
  const latestAt = runCreatedAt(latestDetails);
  const latestRef = useMemo(
    () =>
      latestRunId !== null && latestStatus !== undefined
        ? {
            runId: latestRunId,
            status: latestStatus,
            at: latestAt ?? undefined,
          }
        : null,
    [latestRunId, latestStatus, latestAt]
  );
  const latestIdRef = useRef(latestRunId);
  latestIdRef.current = latestRunId;

  // The latest run needs no query of its own: the DAG query has it.
  const pinnedEnabled =
    runParam !== null && dagName !== '' && runParam !== latestRunId;
  const runSSE = useDAGRunSSE(
    dagName,
    runParam ?? '',
    pinnedEnabled,
    remoteNode
  );
  const fallback = sseFallbackOptions(runSSE);
  const fallbackInterval = fallback.refreshInterval;
  // A test run writes no events, so its stream never reports a change: the
  // run is read again while it is active, whatever the stream. SWR reads the
  // interval only when its timer starts or fires, so the function changes
  // when the run turns into an active test run: a timer that stopped starts.
  const [readsTestRun, setReadsTestRun] = useState(false);
  const pinnedInterval = useCallback(
    (latest?: { dagRunDetails?: ApiDAGRunDetails }) => {
      const run = latest?.dagRunDetails;
      return run?.ephemeral && isRunActive(run)
        ? TEST_RUN_READ_MS
        : fallbackInterval;
    },
    [fallbackInterval, readsTestRun]
  );
  const {
    data: pinnedData,
    error: pinnedError,
    mutate: mutatePinnedQuery,
  } = useQuery(
    '/dag-runs/{name}/{dagRunId}',
    whenEnabled(pinnedEnabled, {
      params: {
        path: { name: dagName, dagRunId: runParam ?? '' },
        query: { remoteNode },
      },
    }),
    { ...fallback, refreshInterval: pinnedInterval }
  );
  // An event of the previous run must not land in the cache of the new one.
  useSSECacheSync(runSSE, mutatePinnedQuery, (event) =>
    event.dagRunDetails?.dagRunId === runParam ? event : undefined
  );

  const pinnedServer = pinnedEnabled
    ? (pinnedData?.dagRunDetails ?? null)
    : null;
  const activeTestRun = !!pinnedServer?.ephemeral && isRunActive(pinnedServer);
  if (activeTestRun !== readsTestRun) setReadsTestRun(activeTestRun);
  const missing =
    pinnedEnabled &&
    ((pinnedError != null && isNotFoundQueryError(pinnedError)) ||
      (pinnedServer != null && pinnedServer.dagRunId !== runParam));

  const writeRun = useCallback(
    (runId: string | null, replace?: boolean, state?: unknown) => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          if (runId === null) next.delete(RUN_PARAM);
          else next.set(RUN_PARAM, runId);
          return next;
        },
        { replace: !!replace, ...(state !== undefined ? { state } : {}) }
      );
    },
    [setSearchParams]
  );

  // The run that the page fell back to after a 404. Its ?run is no new
  // selection, so the notice of the fall back stays.
  const fallbackRun = useRef<string | null>(null);
  // Runs that the server does not have: the redirect never picks them.
  const missingRuns = useRef<Set<string>>(new Set());

  // A new ?run, also one written without select() (link, Back), is a new
  // selection. This effect runs before the next one, so a 404 still wins.
  useEffect(() => {
    if (runParam !== null && runParam !== fallbackRun.current) {
      setNotFound(false);
    }
  }, [runParam]);

  const pinnedRunId = runParam !== null && !missing ? runParam : null;

  // The details of the latest run while it becomes an older run: the query
  // of that run needs a moment, and the page does not blink.
  const heldLatest = useRef<ApiDAGRunDetails | null>(null);
  if (latestDetails !== null && latestDetails.dagRunId === runParam) {
    heldLatest.current = latestDetails;
  }
  let serverDetails: ApiDAGRunDetails | null;
  if (pinnedRunId === null) serverDetails = latestDetails;
  else if (latestDetails?.dagRunId === pinnedRunId) {
    serverDetails = latestDetails;
  } else if (pinnedServer?.dagRunId === pinnedRunId) {
    serverDetails = pinnedServer;
  } else if (heldLatest.current?.dagRunId === pinnedRunId) {
    serverDetails = heldLatest.current;
  } else serverDetails = null;

  // The run of the link was shown once: a later 404 means that it was
  // removed (dequeued, deleted), which is no bad link.
  const shownRunId = useRef<string | null>(null);
  if (
    runParam !== null &&
    serverDetails !== null &&
    serverDetails.dagRunId === runParam
  ) {
    shownRunId.current = runParam;
  }

  useEffect(() => {
    if (!missing || runParam === null) return;
    if (shownRunId.current !== runParam) setNotFound(true);
    missingRuns.current.add(runParam);
    const fallback =
      latestRunId !== null && latestRunId !== runParam ? latestRunId : null;
    fallbackRun.current = fallback;
    writeRun(fallback, true);
  }, [missing, runParam, latestRunId, writeRun]);

  // No ?run: the URL names the latest run, with no history entry.
  useEffect(() => {
    if (runParam !== null || latestRunId === null || legacyIdx) return;
    if (dagName === '' || missingRuns.current.has(latestRunId)) return;
    writeRun(latestRunId, true, RUN_REDIRECT_STATE);
  }, [runParam, latestRunId, legacyIdx, dagName, writeRun]);

  const select = useCallback(
    (sel: 'latest' | string, opts?: { replace?: boolean }) => {
      setNotFound(false);
      fallbackRun.current = null;
      writeRun(sel === 'latest' ? latestIdRef.current : sel, opts?.replace);
    },
    [writeRun]
  );

  const refRunId = pinnedRunId ?? latestRunId;
  const selKey = refRunId === null ? 'none' : `run:${refRunId}`;

  // The API can name an older active run as the latest; a shown run that
  // is newer than it is the newest one.
  const isLatest =
    pinnedRunId === null ||
    latestRunId === pinnedRunId ||
    (serverDetails !== null && !isNewerRun(latestRef, serverDetails));
  // Pinned: the URL names a run that is not the latest one.
  const pinnedSelection = pinnedRunId !== null && !isLatest;
  const selection: RunSelection = useMemo(
    () =>
      pinnedSelection && pinnedRunId !== null
        ? { runId: pinnedRunId }
        : 'latest',
    [pinnedSelection, pinnedRunId]
  );

  const [patch, setPatch] = useState<NodePatch | null>(null);
  const currentRef = useRef({ selKey, serverDetails });
  currentRef.current = { selKey, serverDetails };
  useEffect(() => {
    setPatch(null);
  }, [selKey]);

  const patchLive = useMemo(
    () =>
      patch !== null &&
      patch.selKey === selKey &&
      serverDetails !== null &&
      sameContent(patch.base, serverDetails),
    [patch, selKey, serverDetails]
  );
  const details = patch && patchLive ? patch.patched : serverDetails;

  const applyNodeStatus = useCallback(
    (stepName: string, status: NodeStatus) => {
      setPatch((prev) => {
        const { selKey: key, serverDetails: base } = currentRef.current;
        if (!base) return prev;
        const from =
          prev && prev.selKey === key && sameContent(prev.base, base)
            ? prev.patched
            : base;
        return {
          selKey: key,
          base,
          patched: updateDAGRunNodeStatus(from, stepName, status),
        };
      });
    },
    []
  );

  const refName = details?.name || dagName;
  const ref: RunRef | null = useMemo(
    () => (refRunId === null ? null : { name: refName, runId: refRunId }),
    [refName, refRunId]
  );

  const overlay = useMemo(
    () => buildRunOverlay(details ?? undefined),
    [details]
  );
  const nodeByStep = useMemo(() => nodesByStep(details), [details]);
  const handlers = useMemo(() => runHandlers(details), [details]);
  const waiting = useMemo(() => waitingCounts(details), [details]);
  const failedStep = useMemo(() => firstFailedStep(details), [details]);

  const mutatePinned = useMemo(
    () =>
      pinnedEnabled && pinnedRunId !== null ? () => mutatePinnedQuery() : null,
    [mutatePinnedQuery, pinnedEnabled, pinnedRunId]
  );

  const hasRun = ref !== null;
  const isActive = details !== null && isRunActive(details);
  const run = useMemo(
    () => ({
      selection,
      ref,
      details,
      hasRun,
      isLatest,
      isActive,
      notFound,
      latestRef,
      overlay,
      nodeByStep,
      handlers,
      waiting,
      failedStep,
      select,
      applyNodeStatus,
    }),
    [
      selection,
      ref,
      details,
      hasRun,
      isLatest,
      isActive,
      notFound,
      latestRef,
      overlay,
      nodeByStep,
      handlers,
      waiting,
      failedStep,
      select,
      applyNodeStatus,
    ]
  );
  return { run, mutatePinned };
}
