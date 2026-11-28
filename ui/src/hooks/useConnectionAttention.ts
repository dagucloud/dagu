// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { useSSE } from '@/hooks/useSSE';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';

type ConnectionAttention = components['schemas']['ConnectionAttention'];

// Each server keeps its own connections, so the badge reads this server's.
const LOCAL_NODE = 'local';

// The stream wakes on each connection status change. Without it, the count
// is read again at this interval while the tab is visible.
const FALLBACK_INTERVAL_MS = 60_000;

const NO_ATTENTION: ConnectionAttention = { needsReconnect: 0, workspaces: [] };

/**
 * Returns how many connections the user can see need reconnecting, and
 * their workspaces. The live stream sends it again only when it changes.
 * While another node is selected, the tab's stream is that node's, so the
 * count is polled rather than held on a second stream.
 */
export function useConnectionAttention(): ConnectionAttention {
  const streamLocal = useRemoteNode() === LOCAL_NODE;
  const sse = useSSE<ConnectionAttention>(
    '/events/connection-attention',
    streamLocal,
    LOCAL_NODE
  );
  const { data, mutate } = useQuery(
    '/connection-attention',
    { params: { query: { remoteNode: LOCAL_NODE } } },
    sseFallbackOptions(sse, FALLBACK_INTERVAL_MS)
  );
  useSSECacheSync(sse, mutate);
  return data ?? NO_ATTENTION;
}
