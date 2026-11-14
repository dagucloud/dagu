// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useMemo, useSyncExternalStore } from 'react';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { outputCache, type CacheEntries, type CacheScope } from './outputCache';

/** The cache scope of a workflow on the current remote node. */
export function useCacheScope(fileName: string): CacheScope {
  const remoteNode = useRemoteNode();
  return useMemo(() => ({ remoteNode, fileName }), [remoteNode, fileName]);
}

/** The cached entries of a workflow; the component renders again on a change. */
export function useOutputCacheEntries(scope: CacheScope): CacheEntries {
  const snapshot = useCallback(() => outputCache.entries(scope), [scope]);
  return useSyncExternalStore(outputCache.subscribe, snapshot, snapshot);
}
