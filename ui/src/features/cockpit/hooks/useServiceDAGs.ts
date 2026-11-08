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

function isFailing(dag: ServiceDAG): boolean {
  return dag.state === ServiceDAGState.failing || dag.consecutiveFailures > 0;
}

/**
 * Lists the DAGs that a restart policy keeps alive, the failing ones first,
 * each group in the server's order. The live stream carries
 * the list itself and sends it again only when a state changes, so a service DAG
 * that keeps running costs no traffic.
 */
export function useServiceDAGs(): {
  serviceDAGs: ServiceDAG[];
  error: Error | null;
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
        (a, b) => Number(isFailing(b)) - Number(isFailing(a))
      ),
    [data]
  );

  return {
    serviceDAGs,
    error: error instanceof Error ? error : null,
  };
}
