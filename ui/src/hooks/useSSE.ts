// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useConfig } from '../contexts/ConfigContext';
import { useRemoteNode } from '../contexts/RemoteNodeContext';
import { useEffect, useRef, useState } from 'react';
import { sseManager } from './SSEManager';

export interface SSEState<T> {
  data: T | null;
  error: Error | null;
  isConnected: boolean;
  isConnecting: boolean;
  shouldUseFallback: boolean;
}

/**
 * Builds an SSE endpoint URL with query parameters from an object.
 * Filters out null/undefined values and converts values to strings.
 */
export function buildSSEEndpoint(basePath: string, params: object): string {
  const searchParams = new URLSearchParams();

  for (const [key, value] of Object.entries(params)) {
    if (value == null) {
      continue;
    }
    if (Array.isArray(value)) {
      for (const item of value) {
        if (item != null) {
          searchParams.append(key, String(item));
        }
      }
      continue;
    }
    searchParams.set(key, String(value));
  }

  const queryString = searchParams.toString();
  return queryString ? `${basePath}?${queryString}` : basePath;
}

const INITIAL_STATE: SSEState<unknown> = {
  data: null,
  error: null,
  isConnected: false,
  isConnecting: false,
  shouldUseFallback: false,
};

export function useSSE<T>(
  endpoint: string,
  enabled: boolean = true,
  remoteNodeOverride?: string
): SSEState<T> {
  const config = useConfig();
  const remoteNode = useRemoteNode(remoteNodeOverride);

  const [state, setState] = useState<SSEState<T>>(INITIAL_STATE as SSEState<T>);

  // Reset state synchronously during render when connection parameters change.
  // This prevents stale data from the old connection being returned in the first
  // render after a change — critical because consumers use isConnected to gate
  // SWR polling, and stale isConnected=true blocks SWR fetches.
  const connectionKey = `${endpoint}|${remoteNode}|${config.apiURL}|${enabled}`;
  const prevConnectionKeyRef = useRef(connectionKey);
  if (prevConnectionKeyRef.current !== connectionKey) {
    prevConnectionKeyRef.current = connectionKey;
    setState(INITIAL_STATE as SSEState<T>);
  }

  useEffect(() => {
    if (!enabled) {
      setState((prev) => ({
        ...prev,
        isConnected: false,
        shouldUseFallback: true,
      }));
      return;
    }

    let unsubscribe: (() => void) | undefined;
    try {
      unsubscribe = sseManager.subscribe(endpoint, remoteNode, config.apiURL, {
        onData: (data) =>
          setState((prev) => ({
            ...prev,
            data: data as T,
          })),
        onStateChange: (connState) =>
          setState((prev) => ({
            ...prev,
            ...connState,
          })),
      });
    } catch (error) {
      setState((prev) => ({
        ...prev,
        isConnected: false,
        isConnecting: false,
        shouldUseFallback: true,
        error:
          error instanceof Error
            ? error
            : new Error('Failed to subscribe to SSE'),
      }));
      return;
    }

    return () => unsubscribe?.();
  }, [endpoint, remoteNode, config.apiURL, enabled]);

  return state;
}
