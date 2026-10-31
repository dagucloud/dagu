// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef } from 'react';
import useSWR from 'swr';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient, useQuery } from '@/hooks/api';
import { presetView, type RangePresetId } from './sampleBars';

/** The step that the panel reads, in a root run. */
export interface ContinuousTarget {
  name: string;
  runId: string;
  step: string;
}

/**
 * The polling periods while the run is active. The server scans about a day
 * of raw samples for the availability, so it polls the least often.
 */
export const AVAILABILITY_POLL_MS = 60_000;
export const EVENTS_POLL_MS = 30_000;
/** The samples of the 1h window, which 10 s moves visibly. */
export const SHORT_WINDOW_POLL_MS = 10_000;
/** The samples of a longer window, which barely moves in 10 s. */
export const LONG_WINDOW_POLL_MS = 60_000;

const iso = (ms: number) => new Date(ms).toISOString();

/**
 * Calls `read` once each time `active` turns false, so that the end of a
 * run that stopped between two polls shows.
 */
export function useReadAtEnd(active: boolean, read: () => unknown): void {
  const wasActive = useRef(active);
  useEffect(() => {
    if (wasActive.current && !active) void read();
    wasActive.current = active;
  }, [active, read]);
}

function pathOf(target: ContinuousTarget) {
  return {
    name: target.name,
    dagRunId: target.runId,
    stepName: target.step,
  };
}

/** The availability and the streak of the step. Polled while `poll` is set. */
export function useStepAvailability(target: ContinuousTarget, poll: boolean) {
  const remoteNode = useRemoteNode();
  return useQuery(
    '/dag-runs/{name}/{dagRunId}/steps/{stepName}/availability',
    { params: { path: pathOf(target), query: { remoteNode } } },
    { refreshInterval: poll ? AVAILABILITY_POLL_MS : 0 }
  );
}

/** The window of the samples: fixed times, or a preset that ends now. */
export type SamplesWindow =
  | { from: number; to: number }
  | { preset: RangePresetId; startedAt?: number };

function samplesPollMs(window: SamplesWindow): number {
  return 'preset' in window && window.preset === '1h'
    ? SHORT_WINDOW_POLL_MS
    : LONG_WINDOW_POLL_MS;
}

/**
 * The buckets of a window. A preset has one key: each read asks for the
 * window that ends at the time of the read, so `poll` reads it again (each
 * SHORT_WINDOW_POLL_MS for 1h, LONG_WINDOW_POLL_MS for a longer one) and the
 * cache keeps one entry however long the page stays open. A zoomed or panned
 * window has its times in the key. The old buckets stay on the screen until
 * the new ones come.
 */
export function useStepSamples(
  target: ContinuousTarget,
  window: SamplesWindow,
  points: number,
  poll: boolean
) {
  const remoteNode = useRemoteNode();
  const client = useClient();
  const at =
    'preset' in window
      ? [window.preset, window.startedAt ?? null]
      : [window.from, window.to];
  return useSWR(
    [
      'continuous-samples',
      target.name,
      target.runId,
      target.step,
      remoteNode,
      points,
      ...at,
    ],
    async () => {
      const { from, to } =
        'preset' in window
          ? presetView(window.preset, Date.now(), window.startedAt)
          : window;
      const { data, error } = await client.GET(
        '/dag-runs/{name}/{dagRunId}/steps/{stepName}/samples',
        {
          params: {
            path: pathOf(target),
            query: { remoteNode, from: iso(from), to: iso(to), points },
          },
        }
      );
      if (error) throw error;
      return data;
    },
    {
      keepPreviousData: true,
      refreshInterval: poll ? samplesPollMs(window) : 0,
    }
  );
}

/**
 * One page of events. `before` is the cursor of the page before; `window`
 * limits the times. The old events stay on the screen until the new ones
 * come.
 */
export function useStepEventsPage(
  target: ContinuousTarget,
  page: {
    before?: string;
    window: { from: number; to: number } | null;
    limit: number;
  },
  poll: boolean
) {
  const remoteNode = useRemoteNode();
  return useQuery(
    '/dag-runs/{name}/{dagRunId}/steps/{stepName}/events',
    {
      params: {
        path: pathOf(target),
        query: {
          remoteNode,
          limit: page.limit,
          ...(page.before ? { before: page.before } : {}),
          ...(page.window
            ? { from: iso(page.window.from), to: iso(page.window.to) }
            : {}),
        },
      },
    },
    {
      keepPreviousData: true,
      refreshInterval: poll && !page.before ? EVENTS_POLL_MS : 0,
    }
  );
}
