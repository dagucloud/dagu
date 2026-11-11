// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext, useMemo } from 'react';
import { components, ServiceDAGState } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';
import { useServiceDAGsSSE } from '@/hooks/useServiceDAGsSSE';
import { workspaceSelectionQuery } from '@/lib/workspace';

type ServiceDAG = components['schemas']['ServiceDAG'];

function needsAttention(dag: ServiceDAG): boolean {
  return (
    dag.state === ServiceDAGState.failing ||
    dag.consecutiveFailures > 0 ||
    dag.state === ServiceDAGState.waiting
  );
}

/**
 * Lists the DAGs that a restart policy keeps alive, first the ones that fail or
 * wait for a person, each group in the server's order. The live stream carries
 * the list itself and sends it again only when a state changes, so a service DAG
 * that keeps running costs no traffic.
 */
export function useServiceDAGs(): {
  serviceDAGs: ServiceDAG[];
  /** The error of the latest read, as the query reports it, or null. */
  error: unknown;
} {
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const workspaceQuery = useMemo(
    () => workspaceSelectionQuery(appBarContext.workspaceSelection),
    [appBarContext.workspaceSelection]
  );
  const sse = useServiceDAGsSSE(workspaceQuery);
  const { data, error, mutate } = useQuery(
    '/service-dags',
    { params: { query: { remoteNode, ...workspaceQuery } } },
    sseFallbackOptions(sse, 5000)
  );
  useSSECacheSync(sse, mutate);
  const serviceDAGs = useMemo(
    () =>
      [...(data?.dags ?? [])].sort(
        (a, b) => Number(needsAttention(b)) - Number(needsAttention(a))
      ),
    [data]
  );

  return {
    serviceDAGs,
    error: error ?? null,
  };
}
