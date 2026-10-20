// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext, useMemo } from 'react';
import { components } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';
import { useServiceDAGsSSE } from '@/hooks/useServiceDAGsSSE';
import { workspaceSelectionQuery } from '@/lib/workspace';

type ServiceDAG = components['schemas']['ServiceDAG'];

/**
 * Lists the DAGs that a restart policy keeps alive. The live stream carries
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

  return {
    serviceDAGs: data?.dags ?? [],
    error: error instanceof Error ? error : null,
  };
}
