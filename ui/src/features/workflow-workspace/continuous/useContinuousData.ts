// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef } from 'react';
import useSWR, { unstable_serialize, type Middleware } from 'swr';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient, useQuery } from '@/hooks/api';
import { isAbortLikeError, shouldRetryQueryError } from '@/lib/requestTimeout';
import { presetView, type RangePresetId } from './sampleBars';
import type { SampleSource } from './sampleSource';

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

const STEP_AVAILABILITY =
  '/dag-runs/{name}/{dagRunId}/steps/{stepName}/availability';
const STEP_EVENTS = '/dag-runs/{name}/{dagRunId}/steps/{stepName}/events';
const HISTORY_AVAILABILITY = '/dags/{fileName}/run-history/availability';
const HISTORY_EVENTS = '/dags/{fileName}/run-history/events';

function stepPathOf(source: Extract<SampleSource, { kind: 'step' }>) {
  return {
    name: source.name,
    dagRunId: source.runId,
    stepName: source.step,
  };
}

/**
 * The availability and the streak of the source. Polled while `poll` is
 * set. The query of the other kind of source has a null init: it asks for
 * nothing.
 */
export function useSampleAvailability(source: SampleSource, poll: boolean) {
  const remoteNode = useRemoteNode();
  const config = { refreshInterval: poll ? AVAILABILITY_POLL_MS : 0 };
  const step = useQuery(
    STEP_AVAILABILITY,
    source.kind === 'step'
      ? { params: { path: stepPathOf(source), query: { remoteNode } } }
      : null,
    config
  );
  const history = useQuery(
    HISTORY_AVAILABILITY,
    source.kind === 'run-history'
      ? {
          params: {
            path: { fileName: source.fileName },
            query: { remoteNode },
          },
        }
      : null,
    config
  );
  return source.kind === 'step' ? step : history;
}

const SAMPLES_PATH = '/dag-runs/{name}/{dagRunId}/steps/{stepName}/samples';
const HISTORY_SAMPLES_PATH = '/dags/{fileName}/run-history/samples';

/** The window of the samples: fixed times, or a preset that ends now. */
export type SamplesWindow =
  | { from: number; to: number }
  | { preset: RangePresetId; startedAt?: number };

/** The ends of a window at `now`, in ms. */
function windowAt(window: SamplesWindow, now: number) {
  if ('preset' in window) {
    return presetView(window.preset, now, window.startedAt);
  }
  return window;
}

function samplesPollMs(window: SamplesWindow): number {
  return 'preset' in window && window.preset === '1h'
    ? SHORT_WINDOW_POLL_MS
    : LONG_WINDOW_POLL_MS;
}

type RetryOptions = { retryCount: number; dedupe: boolean };

// The retry that waits for each samples key. A key has one at a time, so a
// read that the host asks for during an error starts no second chain.
const pendingRetries = new Map<string, () => void>();

/** The period at which SWR polls a key, or 0 when it does not poll it. */
function pollPeriod(config: { refreshInterval?: unknown }): number {
  const ms = config.refreshInterval;
  return typeof ms === 'number' && ms > 0 ? ms : 0;
}

/** Drops the retry that waits for `key`, if any. */
function cancelRetry(key: string): void {
  pendingRetries.get(key)?.();
}

// Drops the pending retry of a key once its poll stops, also when a host
// such as a closed dock turns the poll off. The middleware of a hook runs
// inside the middleware of its host, so it sees the poll that SWR runs.
const dropRetryWhenStopped: Middleware = (useSWRNext) =>
  function useRetryWhilePolled(key, fetcher, config) {
    const id = unstable_serialize(key);
    const polled = pollPeriod(config) > 0;
    useEffect(() => {
      if (!polled) cancelRetry(id);
    }, [id, polled]);
    return useSWRNext(key, fetcher, config);
  };

/**
 * Calls `retry` once after `ms`, or once the tab shows again when it is
 * hidden then. It replaces the retry that waits for the same key.
 */
function retryWhenShown(key: string, ms: number, retry: () => void): void {
  cancelRetry(key);
  const run = () => {
    pendingRetries.delete(key);
    retry();
  };
  const timer = setTimeout(() => {
    if (document.visibilityState !== 'hidden') {
      run();
      return;
    }
    document.addEventListener('visibilitychange', run, { once: true });
  }, ms);
  pendingRetries.set(key, () => {
    clearTimeout(timer);
    document.removeEventListener('visibilitychange', run);
    pendingRetries.delete(key);
  });
}

/**
 * The buckets of a window. A preset has one key: each read asks for the
 * window that ends at the time of the read, so `poll` reads it again (each
 * SHORT_WINDOW_POLL_MS for 1h, LONG_WINDOW_POLL_MS for a longer one) and the
 * cache keeps one entry however long the page stays open. A zoomed or panned
 * window has its times in the key. The old buckets stay on the screen until
 * the new ones come.
 */
export function useSamples(
  source: SampleSource,
  window: SamplesWindow,
  points: number,
  poll: boolean
) {
  const remoteNode = useRemoteNode();
  const client = useClient();
  const path =
    source.kind === 'step' ? stepPathOf(source) : { fileName: source.fileName };
  const at =
    'preset' in window
      ? [window.preset, window.startedAt ?? null]
      : [window.from, window.to];
  const pollMs = samplesPollMs(window);
  // The key has the samples path at [1], like a swr-openapi key, so that the
  // workspace revalidation of App.tsx (isWorkspaceScopedSWRKey) reaches it.
  return useSWR(
    [
      'continuous-samples',
      source.kind === 'step' ? SAMPLES_PATH : HISTORY_SAMPLES_PATH,
      { path, remoteNode, points, at },
    ] as const,
    async () => {
      const { from, to } = windowAt(window, Date.now());
      const query = { remoteNode, from: iso(from), to: iso(to), points };
      const { data, error } =
        source.kind === 'step'
          ? await client.GET(SAMPLES_PATH, {
              params: { path: stepPathOf(source), query },
            })
          : await client.GET(HISTORY_SAMPLES_PATH, {
              params: { path: { fileName: source.fileName }, query },
            });
      if (error) throw error;
      return data;
    },
    {
      keepPreviousData: true,
      refreshInterval: poll ? pollMs : 0,
      use: [dropRetryWhenStopped],
      ...(poll
        ? {
            // SWR does not poll a key that holds an error, and the app does
            // not retry a timeout. A polled window retries at each period,
            // whatever the error, except an expired session. A hidden tab
            // retries once it shows again. A host that stops the poll, such
            // as a closed dock, stops the retries too.
            shouldRetryOnError: (error: unknown) =>
              isAbortLikeError(error) || shouldRetryQueryError(error),
            onErrorRetry: (
              _error: unknown,
              key: string,
              config: { refreshInterval?: unknown },
              revalidate: (opts: RetryOptions) => unknown,
              opts: RetryOptions
            ) => {
              const ms = pollPeriod(config);
              if (ms > 0) retryWhenShown(key, ms, () => void revalidate(opts));
            },
          }
        : {}),
    }
  );
}

/**
 * One page of events. `before` is the cursor of the page before; `window`
 * limits the times. The old events stay on the screen until the new ones
 * come.
 */
export function useEventsPage(
  source: SampleSource,
  page: {
    before?: string;
    window: { from: number; to: number } | null;
    limit: number;
  },
  poll: boolean
) {
  const remoteNode = useRemoteNode();
  const query = {
    remoteNode,
    limit: page.limit,
    ...(page.before ? { before: page.before } : {}),
    ...(page.window
      ? { from: iso(page.window.from), to: iso(page.window.to) }
      : {}),
  };
  const config = {
    keepPreviousData: true,
    refreshInterval: poll && !page.before ? EVENTS_POLL_MS : 0,
  };
  const step = useQuery(
    STEP_EVENTS,
    source.kind === 'step'
      ? { params: { path: stepPathOf(source), query } }
      : null,
    config
  );
  const history = useQuery(
    HISTORY_EVENTS,
    source.kind === 'run-history'
      ? { params: { path: { fileName: source.fileName }, query } }
      : null,
    config
  );
  return source.kind === 'step' ? step : history;
}
