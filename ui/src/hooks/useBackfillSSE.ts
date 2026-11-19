// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '../api/v1/schema';
import { SSEState, useSSE } from './useSSE';

type Backfill = components['schemas']['Backfill'];

/** Streams one backfill: its status, the slots in each state, and why it waits. */
export function useBackfillSSE(
  backfillId: string,
  enabled: boolean = true,
  remoteNode?: string
): SSEState<Backfill> {
  const endpoint = `/events/backfills/${encodeURIComponent(backfillId)}`;
  return useSSE<Backfill>(endpoint, enabled, remoteNode);
}
