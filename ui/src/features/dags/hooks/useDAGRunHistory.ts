// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback } from 'react';
import { components } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { useDAGHistorySSE } from '@/hooks/useDAGHistorySSE';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';

type DAGRunDetails = components['schemas']['DAGRunDetails'];
type DAGGridItem = components['schemas']['DAGGridItem'];

/** How often a history that is not streamed is read again. */
const POLL_INTERVAL_MS = 10_000;

/**
 * Reads the latest runs of a DAG, newest first, with the grid of step statuses.
 *
 * A live stream sends the whole history again at every run event, so a DAG
 * that restarts all the time would send it every few seconds. With `live` off,
 * the history is read at a slow interval instead.
 */
export function useDAGRunHistory(
  fileName: string,
  { enabled = true, live = true }: { enabled?: boolean; live?: boolean } = {}
): {
  dagRuns: DAGRunDetails[];
  gridData: DAGGridItem[];
  refresh: () => void;
} {
  const remoteNode = useRemoteNode();
  const sse = useDAGHistorySSE(fileName, enabled && live, remoteNode);
  const { data, mutate } = useQuery(
    '/dags/{fileName}/dag-runs',
    enabled && fileName
      ? { params: { query: { remoteNode }, path: { fileName } } }
      : null,
    live
      ? sseFallbackOptions(sse)
      : { refreshInterval: POLL_INTERVAL_MS, revalidateOnFocus: false }
  );
  useSSECacheSync(sse, mutate);
  const refresh = useCallback(() => void mutate(), [mutate]);

  return {
    dagRuns: data?.dagRuns ?? [],
    gridData: data?.gridData ?? [],
    refresh,
  };
}
