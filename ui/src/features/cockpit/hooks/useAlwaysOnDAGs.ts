// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext, useMemo } from 'react';
import { components } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';
import { useAlwaysOnDAGsSSE } from '@/hooks/useAlwaysOnDAGsSSE';
import { workspaceSelectionQuery } from '@/lib/workspace';

type AlwaysOnDAG = components['schemas']['AlwaysOnDAG'];

/**
 * Lists the DAGs that a restart policy keeps alive. The live stream carries
 * the list itself and sends it again only when a state changes, so an always-on DAG
 * that keeps running costs no traffic.
 */
export function useAlwaysOnDAGs(): {
  alwaysOnDAGs: AlwaysOnDAG[];
  error: Error | null;
} {
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const workspaceQuery = useMemo(
    () => workspaceSelectionQuery(appBarContext.workspaceSelection),
    [appBarContext.workspaceSelection]
  );
  const sse = useAlwaysOnDAGsSSE(workspaceQuery);
  const { data, error, mutate } = useQuery(
    '/always-on-dags',
    { params: { query: { remoteNode, ...workspaceQuery } } },
    sseFallbackOptions(sse, 5000)
  );
  useSSECacheSync(sse, mutate);

  return {
    alwaysOnDAGs: data?.dags ?? [],
    error: error instanceof Error ? error : null,
  };
}
