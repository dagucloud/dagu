// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Shared access to the cluster-wide scheduler pause flag.
 *
 * @module hooks
 */
import { useCallback } from 'react';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient, useQuery } from './api';

/** How often the pause flag is polled, in milliseconds. */
const PAUSE_POLL_INTERVAL = 10000;

/**
 * Reads the scheduler pause state and exposes a setter for it.
 *
 * The read is available to every authenticated role so a paused scheduler can
 * explain itself anywhere in the app. Setting it is rejected by the server for
 * anyone below admin.
 */
export function useSchedulerPause(pollInterval: number = PAUSE_POLL_INTERVAL) {
  const client = useClient();
  const remoteNode = useRemoteNode();

  const { data, error, mutate } = useQuery(
    '/services/scheduler/pause',
    { params: { query: { remoteNode } } },
    { refreshInterval: pollInterval }
  );

  const setPaused = useCallback(
    async (paused: boolean, reason?: string) => {
      const { error: postError } = await client.POST(
        '/services/scheduler/pause',
        {
          params: { query: { remoteNode } },
          body: { paused, ...(reason ? { reason } : {}) },
        }
      );
      if (postError) {
        throw new Error(
          postError.message || 'Failed to update the scheduler pause state'
        );
      }
      await mutate();
    },
    [client, mutate, remoteNode]
  );

  return {
    paused: data?.paused ?? false,
    pausedBy: data?.pausedBy,
    pausedAt: data?.pausedAt,
    reason: data?.reason,
    /** Whether a scheduler is running; undefined when the server cannot tell. */
    schedulerRunning: data?.schedulerRunning,
    error,
    isLoading: !data && !error,
    mutate,
    setPaused,
  };
}
