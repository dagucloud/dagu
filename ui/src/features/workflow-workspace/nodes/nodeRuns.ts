// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef } from 'react';
import { NodeRunStatus, NodeStatus } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { isSubDAGRun } from '@/features/dags/hooks/useStepLogQuery';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import type { ApiDAGRunDetails } from '../data/types';
import { stepsWithNodeRuns } from '../data/runFacts';

export const NODE_RUNS_PER_PAGE = 50;

/**
 * The step status a node run reads as. A disabled or removed node reads as
 * skipped.
 */
export const NODE_RUN_STATUS: Record<NodeRunStatus, NodeStatus> = {
  [NodeRunStatus.not_started]: NodeStatus.NotStarted,
  [NodeRunStatus.running]: NodeStatus.Running,
  [NodeRunStatus.waiting]: NodeStatus.Waiting,
  [NodeRunStatus.succeeded]: NodeStatus.Success,
  [NodeRunStatus.partially_succeeded]: NodeStatus.PartialSuccess,
  [NodeRunStatus.failed]: NodeStatus.Failed,
  [NodeRunStatus.aborted]: NodeStatus.Aborted,
  [NodeRunStatus.rejected]: NodeStatus.Rejected,
  [NodeRunStatus.disabled]: NodeStatus.Skipped,
  [NodeRunStatus.removed]: NodeStatus.Skipped,
};

/**
 * Reads one page of the node runs of a run, a root run or a sub-run. The
 * page is read again when the counts in the run details change: the details
 * follow the run live, so the page does too without polling.
 */
export function useNodeRuns(
  details: ApiDAGRunDetails,
  query: { failedOnly: boolean; page: number }
) {
  const remoteNode = useRemoteNode();
  const sub = isSubDAGRun(details);
  const path = {
    name: sub ? (details.rootDAGRunName as string) : details.name,
    dagRunId: sub ? (details.rootDAGRunId as string) : details.dagRunId,
  };
  const queryParams = {
    remoteNode,
    failedOnly: query.failedOnly || undefined,
    page: query.page,
    perPage: NODE_RUNS_PER_PAGE,
  };
  const options = { keepPreviousData: true, revalidateOnFocus: false };
  const rootQuery = useQuery(
    '/dag-runs/{name}/{dagRunId}/node-runs',
    whenEnabled(!sub, { params: { query: queryParams, path } }),
    options
  );
  const subQuery = useQuery(
    '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/node-runs',
    whenEnabled(sub, {
      params: {
        query: queryParams,
        path: { ...path, subDAGRunId: details.dagRunId },
      },
    }),
    options
  );
  const result = sub ? subQuery : rootQuery;

  const counts = JSON.stringify(
    stepsWithNodeRuns(details).map((node) => [node.step.name, node.nodeRuns])
  );
  const seen = useRef(counts);
  const { mutate } = result;
  useEffect(() => {
    if (seen.current === counts) return;
    seen.current = counts;
    void mutate();
  }, [counts, mutate]);
  return result;
}
