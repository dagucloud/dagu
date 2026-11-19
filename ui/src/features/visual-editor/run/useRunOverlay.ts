// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useMemo, useState } from 'react';
import type { Status } from '@/api/v1/schema';
import { useQuery } from '@/hooks/api';
import { useDAGRunSSE } from '@/hooks/useDAGRunSSE';
import { sseFallbackOptions } from '@/hooks/useSSECacheSync';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import type { RunOverlayResult } from '../contracts';
import type { AgentRunInfo } from '../canvas/types';
import { buildAgentRunInfo } from './agentRun';
import { buildRunOverlay } from './runOverlay';
import {
  buildToolCalls,
  buildToolSubRuns,
  type ToolSubRuns,
} from './toolCalls';

/** The overlay of a run, with the agent facts of an agent DAG run. */
export interface RunOverlayWithAgent extends RunOverlayResult {
  /** The status and the end time of the run that the overlay shows. */
  status?: Status;
  finishedAt?: string;
  /** Set only for the run of an agent DAG. */
  agent?: AgentRunInfo;
  /** Calls of each tool DAG by chat step, from the sub-runs of the run. */
  toolCalls?: Record<string, Record<string, number>>;
  /** The sub-runs of each tool DAG by chat step. */
  toolSubRuns?: ToolSubRuns;
}

const NO_RUN: RunOverlayWithAgent = {
  overlay: {},
  runId: null,
  isLoading: false,
};

/**
 * Step states of one run. `runId` may be 'latest'; the result carries the
 * resolved run ID. 'latest' resolves at each read of the query, which polls
 * only as the fallback of SSE: with SSE up, a newer run shows at the next
 * read (a remount or a revalidation). A null `runId` makes no request.
 */
export function useRunOverlay({
  dagName,
  runId,
}: {
  dagName: string;
  runId: string | null;
}): RunOverlayWithAgent {
  const remoteNode = useRemoteNode();
  const enabled = runId !== null && dagName !== '';

  // The run ID that SSE follows. 'latest' is known after the first response.
  const [queriedId, setQueriedId] = useState<string | null>(null);
  const sseRunId = runId === 'latest' ? (queriedId ?? '') : (runId ?? '');
  const sse = useDAGRunSSE(
    dagName,
    sseRunId,
    enabled && sseRunId !== '',
    remoteNode
  );

  const { data, isLoading } = useQuery(
    '/dag-runs/{name}/{dagRunId}',
    enabled
      ? {
          params: {
            path: { name: dagName, dagRunId: runId },
            query: { remoteNode },
          },
        }
      : null,
    sseFallbackOptions(sse)
  );

  const queried = enabled ? data?.dagRunDetails : undefined;
  const queriedRunId = queried?.dagRunId ?? null;
  useEffect(() => {
    setQueriedId(runId === 'latest' ? queriedRunId : null);
  }, [runId, queriedRunId]);

  const live = sse.data?.dagRunDetails;
  const useLive =
    enabled &&
    live != null &&
    live.dagRunId === sseRunId &&
    !sse.shouldUseFallback;
  const details = useLive ? live : queried;
  const overlay = useMemo(() => buildRunOverlay(details), [details]);
  const agent = useMemo(() => buildAgentRunInfo(details), [details]);
  const toolCalls = useMemo(() => buildToolCalls(details), [details]);
  const toolSubRuns = useMemo(() => buildToolSubRuns(details), [details]);

  if (!enabled) return NO_RUN;
  return {
    overlay,
    runId: details?.dagRunId ?? (runId === 'latest' ? null : runId),
    isLoading,
    ...(details
      ? { status: details.status, finishedAt: details.finishedAt }
      : {}),
    ...(agent ? { agent } : {}),
    ...(Object.keys(toolCalls).length > 0 ? { toolCalls, toolSubRuns } : {}),
  };
}
