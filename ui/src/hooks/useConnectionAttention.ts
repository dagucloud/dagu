// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '@/api/v1/schema';
import { useQuery } from '@/hooks/api';
import { useSSE } from '@/hooks/useSSE';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';

type ConnectionAttention = components['schemas']['ConnectionAttention'];

// Each server keeps its own connections, so the badge reads this server's.
const LOCAL_NODE = 'local';

// The stream wakes on each connection status change. Without it, the count
// is read again at this interval while the tab is visible.
const FALLBACK_INTERVAL_MS = 60_000;

/**
 * Returns how many connections the user can see need reconnecting. The live
 * stream sends the count again only when a connection's status changes.
 */
export function useConnectionAttention(): number {
  const sse = useSSE<ConnectionAttention>(
    '/events/connection-attention',
    true,
    LOCAL_NODE
  );
  const { data, mutate } = useQuery(
    '/connection-attention',
    { params: { query: { remoteNode: LOCAL_NODE } } },
    sseFallbackOptions(sse, FALLBACK_INTERVAL_MS)
  );
  useSSECacheSync(sse, mutate);
  return data?.needsReconnect ?? 0;
}
