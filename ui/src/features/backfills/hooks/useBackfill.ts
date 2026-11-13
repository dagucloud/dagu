// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef, useState } from 'react';
import type { BackfillSlotState } from '@/api/v1/schema';
import { useQuery } from '@/hooks/api';
import { useBackfillSSE } from '@/hooks/useBackfillSSE';
import { usePageVisible } from '@/hooks/usePageVisible';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';
import { type Backfill, isOpenBackfill } from '../lib/backfillState';

/** How often an open backfill is read while the live feed is down. */
const FALLBACK_POLL_MS = 5000;

/** Slots per page of the slot grid. */
export const SLOT_PAGE_SIZE = 200;

const READ_ONCE = {
  revalidateOnFocus: false,
  revalidateIfStale: false,
  refreshInterval: 0,
} as const;

/**
 * A page of slots is read when it is shown again, as it may have changed
 * while another page was, and else only when the backfill's counts change.
 */
const READ_ON_SHOW = {
  revalidateOnFocus: false,
  revalidateIfStale: true,
  refreshInterval: 0,
  keepPreviousData: true,
} as const;

/**
 * A backfill. While it is open and the tab is visible, the live feed
 * refreshes it when one of its runs changes; a read every 5s stands in only
 * while the feed is down. An ended backfill is read once, and so is one
 * whose tab is hidden until the tab shows again.
 */
export function useBackfill(backfillId: string, remoteNode: string) {
  const visible = usePageVisible();
  const [open, setOpen] = useState(true);
  const live = open && visible && backfillId !== '';
  const sse = useBackfillSSE(backfillId, live, remoteNode);
  const query = useQuery(
    '/backfills/{backfillId}',
    backfillId
      ? { params: { path: { backfillId }, query: { remoteNode } } }
      : null,
    live ? sseFallbackOptions(sse, FALLBACK_POLL_MS) : READ_ONCE
  );
  useSSECacheSync(sse, query.mutate);
  const { data } = query;
  useEffect(() => {
    if (data) setOpen(isOpenBackfill(data));
  }, [data]);
  return query;
}

/**
 * A page of a backfill's slots, in a state or in all. It is read when it is
 * shown, and again when the backfill's counts or status change, which is
 * when its slots can; never on a timer.
 */
export function useBackfillSlots(
  backfill: Backfill | undefined,
  remoteNode: string,
  state: BackfillSlotState | null,
  page: number
) {
  const query = useQuery(
    '/backfills/{backfillId}/slots',
    backfill
      ? {
          params: {
            path: { backfillId: backfill.id },
            query: {
              remoteNode,
              offset: page * SLOT_PAGE_SIZE,
              limit: SLOT_PAGE_SIZE,
              ...(state ? { state } : {}),
            },
          },
        }
      : null,
    READ_ON_SHOW
  );
  const signature = backfill
    ? `${backfill.status}:${JSON.stringify(backfill.counts)}`
    : '';
  // The first signature comes with the first read of the page.
  const seen = useRef<string | null>(null);
  const { mutate } = query;
  useEffect(() => {
    if (signature === '' || signature === seen.current) return;
    const first = seen.current === null;
    seen.current = signature;
    if (!first) void mutate();
  }, [signature, mutate]);
  return query;
}

/**
 * A backfill known from a list or a details read, kept current while it is
 * open and the tab is visible: by its live feed while the feed is up, and
 * by a read every 5s while it is down. Once it is seen to end, nothing
 * follows it. A new `known` from the caller replaces what was seen.
 */
export function useLiveBackfill(
  known: Backfill | undefined,
  remoteNode: string
): Backfill | undefined {
  const visible = usePageVisible();
  // What the caller knows, by content: a read that brings newer counts
  // replaces what was seen, the same counts in a new object do not.
  const knownKey = known
    ? `${known.id}:${known.status}:${JSON.stringify(known.counts)}`
    : '';
  const knownId = known?.id;
  const [seen, setSeen] = useState<{ key: string; value: Backfill }>();
  const current = known && seen?.key === knownKey ? seen.value : known;
  const live = !!current && visible && isOpenBackfill(current);
  const sse = useBackfillSSE(current?.id ?? '', live, remoteNode);
  const down =
    live && (sse.shouldUseFallback || (!sse.isConnected && !sse.isConnecting));
  const { data } = useQuery(
    '/backfills/{backfillId}',
    down && current
      ? { params: { path: { backfillId: current.id }, query: { remoteNode } } }
      : null,
    {
      revalidateOnFocus: false,
      revalidateIfStale: true,
      refreshInterval: FALLBACK_POLL_MS,
    }
  );
  const streamed = sse.isConnected ? sse.data : undefined;
  useEffect(() => {
    if (streamed && streamed.id === knownId) {
      setSeen({ key: knownKey, value: streamed });
    }
  }, [knownId, knownKey, streamed]);
  useEffect(() => {
    if (data && data.id === knownId) setSeen({ key: knownKey, value: data });
  }, [knownId, knownKey, data]);
  return current;
}
