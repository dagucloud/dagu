// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import { useDAGsListSSE } from '@/hooks/useDAGsListSSE';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';
import { workspaceTargetQueryForWorkspace } from '@/lib/workspace';
import React, { useCallback, useContext, useMemo, useState } from 'react';
import { DagLookup, DocLiveContext, DocLiveContextValue } from './context';

type DAGFile = components['schemas']['DAGFile'];

type Props = {
  /** Workspace of the surrounding document; scopes the DAG list. */
  workspace: string | null;
  children: React.ReactNode;
};

// One page covers realistic workspace sizes; refs beyond it resolve
// not-found. All doc-embedded live UI shares this single SSE topic, keeping
// the per-connection topic budget constant regardless of chip count.
const DAG_LIST_PER_PAGE = 1000;

export function DocLiveProvider({ workspace, children }: Props) {
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';

  // The DAG list feed is enabled only while at least one live element
  // (chip, info block, run block) is mounted.
  const [refCount, setRefCount] = useState(0);

  const registerRef = useCallback(() => {
    setRefCount((c) => c + 1);
    return () => {
      setRefCount((c) => Math.max(0, c - 1));
    };
  }, []);

  const queryParams = useMemo(
    () => ({
      remoteNode,
      perPage: DAG_LIST_PER_PAGE,
      ...workspaceTargetQueryForWorkspace(workspace),
    }),
    [remoteNode, workspace]
  );

  const enabled = refCount > 0;
  const sse = useDAGsListSSE(queryParams, enabled);
  const { data, mutate, isLoading } = useQuery(
    '/dags',
    enabled ? { params: { query: queryParams } } : null,
    {
      ...sseFallbackOptions(sse),
      keepPreviousData: true,
      revalidateOnFocus: false,
    }
  );
  useSSECacheSync(sse, mutate);

  // Index by both logical name and file name; authors usually write names.
  const byRef = useMemo(() => {
    const map = new Map<string, DAGFile>();
    data?.dags?.forEach((item) => {
      if (item.dag.name && !map.has(item.dag.name)) {
        map.set(item.dag.name, item);
      }
    });
    data?.dags?.forEach((item) => {
      if (!map.has(item.fileName)) map.set(item.fileName, item);
    });
    return map;
  }, [data]);

  const lookup = useCallback(
    (ref: string): DagLookup => {
      const item = byRef.get(ref);
      if (item) {
        return {
          state: 'found',
          fileName: item.fileName,
          dagName: item.dag.name,
          latestDAGRun: item.latestDAGRun,
          suspended: item.suspended,
          nextRun: item.nextRun,
        };
      }
      return data && !isLoading ? { state: 'not-found' } : { state: 'loading' };
    },
    [byRef, data, isLoading]
  );

  const value = useMemo<DocLiveContextValue>(
    () => ({ workspace, remoteNode, registerRef, lookup }),
    [workspace, remoteNode, registerRef, lookup]
  );

  return (
    <DocLiveContext.Provider value={value}>{children}</DocLiveContext.Provider>
  );
}
