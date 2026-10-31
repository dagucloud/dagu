// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo, useRef } from 'react';
import type { components } from '@/api/v1/schema';
import { useBoundedDAGRunDetails } from '@/features/dag-runs/hooks/useBoundedDAGRunDetails';
import { useQuery } from '@/hooks/api';
import { useDAGHistorySSE } from '@/hooks/useDAGHistorySSE';
import { useDAGSSE } from '@/hooks/useDAGSSE';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';
import type { PeekTarget } from './peekParams';

type DAGRunDetails = components['schemas']['DAGRunDetails'];

/** How many runs of the workflow the peek lists. */
const RECENT_RUN_COUNT = 8;
/** How often a run that has not ended is read again without a live feed. */
const RUN_POLL_MS = 2000;

const NO_RUNS: DAGRunDetails[] = [];

/**
 * The reads of a peek. The workflow and its run history use the SWR keys of
 * the workflow page, so a peek warms the page that it opens. The data loads
 * while the peek is mounted, which is while it is open.
 */
export function usePeekData(target: PeekTarget, remoteNode: string) {
  const runTarget = useMemo(
    () =>
      target.run
        ? { remoteNode, name: target.dag, dagRunId: target.run }
        : null,
    [remoteNode, target.dag, target.run]
  );
  const run = useBoundedDAGRunDetails({
    target: runTarget,
    enabled: runTarget !== null,
    pollIntervalMs: RUN_POLL_MS,
  });
  const loadedRun =
    run.data && run.data.dagRunId === target.run ? run.data : null;

  // A run names its workflow by its DAG name, and its source file can have
  // another name. The file stays while the next run of the peek loads, so
  // that the workflow does not load again.
  const fileRef = useRef<string | null>(null);
  const fileName = !target.run
    ? target.dag
    : loadedRun
      ? loadedRun.sourceFileName || target.dag
      : (fileRef.current ?? target.dag);
  fileRef.current = fileName;

  const workflowSSE = useDAGSSE(fileName, true, remoteNode);
  const workflow = useQuery(
    '/dags/{fileName}',
    { params: { query: { remoteNode }, path: { fileName } } },
    sseFallbackOptions(workflowSSE)
  );
  useSSECacheSync(workflowSSE, workflow.mutate);

  const historySSE = useDAGHistorySSE(fileName, true, remoteNode);
  const history = useQuery(
    '/dags/{fileName}/dag-runs',
    { params: { query: { remoteNode }, path: { fileName } } },
    sseFallbackOptions(historySSE)
  );
  useSSECacheSync(historySSE, history.mutate);
  const historyRuns = history.data?.dagRuns ?? NO_RUNS;

  // A run of the history shows at once while its own read loads.
  const latest = workflow.data?.latestDAGRun;
  const shown: DAGRunDetails | undefined = target.run
    ? (loadedRun ?? historyRuns.find((item) => item.dagRunId === target.run))
    : latest?.dagRunId
      ? latest
      : undefined;

  return {
    fileName,
    workflow,
    /** The run on show: the latest run, or the run of the target. */
    shown,
    runError: run.error,
    refreshRun: run.refresh,
    recentRuns: historyRuns.slice(0, RECENT_RUN_COUNT),
  };
}
