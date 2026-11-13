// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo } from 'react';
import { ServiceDAGState, Status, type components } from '@/api/v1/schema';
import { useQuery } from '@/hooks/api';
import { useServiceDAGsSSE } from '@/hooks/useServiceDAGsSSE';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';

export type ServiceDAG = components['schemas']['ServiceDAG'];

/** The DAGs that fail go first. The order of the server stays inside a group. */
export function failingFirst(dags: readonly ServiceDAG[]): ServiceDAG[] {
  const failing = (dag: ServiceDAG) =>
    dag.state === ServiceDAGState.failing ? 0 : 1;
  return [...dags].sort((a, b) => failing(a) - failing(b));
}

// The chip takes the colour of the run status that looks like the state: a
// DAG that runs is blue, one that waits to restart is green, one that fails
// is red, one that is paused or waits for a person is amber.
export const SERVICE_STATE_CHIP: Record<ServiceDAGState, [Status, string]> = {
  [ServiceDAGState.pending]: [Status.Queued, 'Pending'],
  [ServiceDAGState.running]: [Status.Success, 'Healthy'],
  [ServiceDAGState.waiting]: [Status.Waiting, 'Waiting'],
  [ServiceDAGState.restarting]: [Status.Success, 'Healthy'],
  [ServiceDAGState.failing]: [Status.Failed, 'Failing'],
  [ServiceDAGState.suspended]: [Status.Waiting, 'Suspended'],
  [ServiceDAGState.stopped]: [Status.Aborted, 'Stopped'],
};

const NONE: readonly ServiceDAG[] = [];

/**
 * The service DAGs of a node and a workspace, failing DAGs first. The list
 * is live: the server sends it at most every 2 s. It reads nothing while
 * `enabled` is false.
 */
export function useServiceDAGs(
  query: { remoteNode: string; workspace?: string },
  enabled: boolean
): readonly ServiceDAG[] {
  const { remoteNode, workspace } = query;
  const sse = useServiceDAGsSSE({ workspace }, enabled, remoteNode);
  const { data, mutate } = useQuery(
    '/service-dags',
    enabled ? { params: { query: { remoteNode, workspace } } } : null,
    sseFallbackOptions(sse)
  );
  useSSECacheSync(sse, mutate);
  const dags = data?.dags;
  return useMemo(() => (dags ? failingFirst(dags) : NONE), [dags]);
}
