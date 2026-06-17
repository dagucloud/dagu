// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef } from 'react';
import type { KeyedMutator } from 'swr';
import type { SSEState } from './useSSE';

export function sseFallbackOptions(
  sseResult: SSEState<unknown>,
  fallbackInterval: number = 2000
) {
  const shouldPoll = sseResult.shouldUseFallback;
  return {
    revalidateOnMount: true,
    revalidateIfStale: shouldPoll,
    revalidateOnFocus: shouldPoll,
    refreshInterval: shouldPoll ? fallbackInterval : 0,
  };
}

/**
 * Pushes SSE data into the SWR cache so that SWR is the single source of truth.
 * When SSE disconnects, the SWR cache already has the latest data — no stale
 * fallback. Uses reference equality for change detection; since JSON.parse
 * creates new objects each SSE event, this triggers mutate on every data event.
 * This is acceptable because mutate(data, { revalidate: false }) is cheap
 * (no network request).
 *
 * An optional `transform` function maps SSE data to the SWR cache shape when
 * the SSE type differs from the SWR endpoint type (e.g., DAGSpec).
 * Return `undefined` from `transform` to skip the cache update (e.g., when
 * the SSE payload is missing fields that would overwrite valid cached data).
 *
 * `transform` is stored in a ref so inline arrow functions don't cause
 * unnecessary effect re-runs — the latest transform is always used when
 * new SSE data arrives.
 */
export function useSSECacheSync<S, T = S>(
  sseResult: SSEState<S>,
  mutate: KeyedMutator<T>,
  transform?: (data: S) => T | undefined
) {
  const prevDataRef = useRef<S | null>(null);
  const transformRef = useRef(transform);
  transformRef.current = transform;

  useEffect(() => {
    if (
      sseResult.isConnected &&
      sseResult.data != null &&
      sseResult.data !== prevDataRef.current
    ) {
      prevDataRef.current = sseResult.data;
      const cacheData = transformRef.current
        ? transformRef.current(sseResult.data)
        : (sseResult.data as unknown as T);
      if (cacheData !== undefined) {
        mutate(cacheData, { revalidate: false });
      }
    }
  }, [sseResult.data, sseResult.isConnected, mutate]);
}
