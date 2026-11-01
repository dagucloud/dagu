// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext } from 'react';
import { Status } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { WorkspaceDataContext } from './context';
import type { ApiSubRunDetail, RunRef } from './types';

const NO_SUB_RUNS: ApiSubRunDetail[] = [];
const ACTIVE_POLL_MS = 3000;

/**
 * The sub runs of a run. Path and query are the ones of TimelineChart, so
 * both share one SWR key. A null ref makes no request.
 */
export function useSubRuns(
  ref: RunRef | null,
  parentSubDAGRunId?: string
): { subRuns: ApiSubRunDetail[]; isLoading: boolean } {
  const remoteNode = useRemoteNode();
  // Poll like TimelineChart: only while the selected run is running or queued.
  const selected = useContext(WorkspaceDataContext)?.run;
  const selectedStatus = selected?.details?.status;
  const polls =
    !!ref &&
    selected?.ref?.runId === ref.runId &&
    (selectedStatus === Status.Running || selectedStatus === Status.Queued);
  const { data, isLoading } = useQuery(
    '/dag-runs/{name}/{dagRunId}/sub-dag-runs',
    whenEnabled(ref !== null, {
      params: {
        path: { name: ref?.name ?? '', dagRunId: ref?.runId ?? '' },
        query: { remoteNode, parentSubDAGRunId },
      },
    }),
    { refreshInterval: polls ? ACTIVE_POLL_MS : 0 }
  );
  return {
    subRuns: data?.subRuns ?? NO_SUB_RUNS,
    isLoading: ref !== null && isLoading,
  };
}
