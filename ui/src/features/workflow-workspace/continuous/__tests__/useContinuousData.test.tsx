// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  AVAILABILITY_POLL_MS,
  EVENTS_POLL_MS,
  LONG_WINDOW_POLL_MS,
  SHORT_WINDOW_POLL_MS,
  useSampleAvailability,
  useEventsPage,
  useSamples,
  type SamplesWindow,
} from '../useContinuousData';
import { runHistorySource, stepSource } from '../sampleSource';

const h = vi.hoisted(() => ({ GET: vi.fn() }));

vi.mock('@/hooks/api', async () => {
  const { createQueryHook } = await import('swr-openapi');
  return {
    useClient: () => ({ GET: h.GET }),
    useQuery: createQueryHook({ GET: h.GET } as never, '/'),
  };
});
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

const DAY = 86_400_000;
const target = stepSource('demo', 'run-1', 'heartbeat');

type Query = { remoteNode: string; from: string; to: string; points: number };

function requests(): Query[] {
  return h.GET.mock.calls.map(
    (call) => (call[1] as { params: { query: Query } }).params.query
  );
}

function renderWithCache(hook: () => unknown) {
  const cache = new Map();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <SWRConfig value={{ provider: () => cache, dedupingInterval: 0 }}>
      {children}
    </SWRConfig>
  );
  const view = renderHook(hook, { wrapper });
  return { cache, rerender: view.rerender, unmount: view.unmount };
}

function renderSamples(window: SamplesWindow, poll: boolean) {
  return renderWithCache(() => useSamples(target, window, 100, poll));
}

const tick = async (ms: number) => {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
};

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  vi.setSystemTime(Date.parse('2026-10-28T12:00:00Z'));
  h.GET.mockReset();
  h.GET.mockResolvedValue({
    data: { resolutionSeconds: 60, from: '', to: '', buckets: [] },
  });
});
afterEach(() => {
  vi.useRealTimers();
});

describe('useSamples', () => {
  // A live preset has one cache key: a tab left open on a running step keeps
  // one entry, and each poll asks for the 24 hours that end at its own time.
  it('polls a preset under one key, with the window that ends at each read', async () => {
    const { cache, rerender } = renderSamples({ preset: '24h' }, true);
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(1));
    await tick(LONG_WINDOW_POLL_MS);
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(2));
    // A render after the clock moved keeps the key.
    rerender();
    await tick(0);

    const [first, second] = requests();
    expect(Date.parse(first!.to) - Date.parse(first!.from)).toBe(DAY);
    expect(Date.parse(second!.to) - Date.parse(second!.from)).toBe(DAY);
    expect(Date.parse(second!.from)).toBeGreaterThanOrEqual(
      Date.parse(first!.from) + LONG_WINDOW_POLL_MS
    );
    expect([...cache.keys()]).toHaveLength(1);
  });

  // 10 s moves the 1h window visibly; a longer window barely moves in that
  // time, so it reads once a minute.
  it('polls the 1h window every 10 s and a longer one every minute', async () => {
    const hour = renderSamples({ preset: '1h' }, true);
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(1));
    await tick(SHORT_WINDOW_POLL_MS);
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(2));

    hour.unmount();
    h.GET.mockClear();
    renderSamples({ preset: '24h' }, true);
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(1));
    await tick(SHORT_WINDOW_POLL_MS);
    expect(h.GET).toHaveBeenCalledTimes(1);
    await tick(LONG_WINDOW_POLL_MS - SHORT_WINDOW_POLL_MS);
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(2));
  });

  it('asks for the times of a fixed window and does not poll it', async () => {
    const from = Date.parse('2026-10-20T00:00:00Z');
    const to = from + DAY;
    renderSamples({ from, to }, false);
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(1));
    await tick(LONG_WINDOW_POLL_MS * 3);

    expect(h.GET).toHaveBeenCalledTimes(1);
    expect(requests()[0]).toEqual({
      remoteNode: 'local',
      from: new Date(from).toISOString(),
      to: new Date(to).toISOString(),
      points: 100,
    });
  });
});

// The server reads about a day of raw samples for the availability: an
// active run asks for it once a minute.
describe('useSampleAvailability', () => {
  it('polls the availability once a minute', async () => {
    renderWithCache(() => useSampleAvailability(target, true));
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(1));
    await tick(AVAILABILITY_POLL_MS - 1000);
    expect(h.GET).toHaveBeenCalledTimes(1);
    await tick(1000);
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(2));
  });
});

describe('useEventsPage', () => {
  it('polls the first page every 30 s', async () => {
    renderWithCache(() =>
      useEventsPage(target, { window: null, limit: 50 }, true)
    );
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(1));
    await tick(EVENTS_POLL_MS - 1000);
    expect(h.GET).toHaveBeenCalledTimes(1);
    await tick(1000);
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(2));
  });

  // A poll of the first page moves its cursor: a later page keeps its
  // events on the screen while it asks again with the new cursor.
  it('keeps the events of a page while a new cursor loads', async () => {
    const page = { events: [], nextCursor: 'c3' };
    h.GET.mockImplementation(
      async (
        _path: string,
        init: { params: { query: { before?: string } } }
      ) =>
        init.params.query.before === 'c1'
          ? { data: page }
          : new Promise(() => undefined)
    );
    const view = renderHook(
      ({ before }) =>
        useEventsPage(target, { before, window: null, limit: 50 }, false),
      {
        initialProps: { before: 'c1' },
        wrapper: ({ children }: { children: ReactNode }) => (
          <SWRConfig value={{ provider: () => new Map() }}>
            {children}
          </SWRConfig>
        ),
      }
    );
    await waitFor(() => expect(view.result.current.data).toEqual(page));

    view.rerender({ before: 'c2' });
    await tick(0);
    expect(h.GET).toHaveBeenLastCalledWith(
      '/dag-runs/{name}/{dagRunId}/steps/{stepName}/events',
      expect.objectContaining({
        params: expect.objectContaining({
          query: expect.objectContaining({ before: 'c2' }),
        }),
      })
    );
    expect(view.result.current.data).toEqual(page);
  });
});

// The run history of a service DAG answers with the same series. It reads
// its own routes, at the periods of the step.
describe('with the run history source', () => {
  const history = runHistorySource('svc.yaml');
  const SAMPLES = '/dags/{fileName}/run-history/samples';

  const paths = () => h.GET.mock.calls.map((call) => call[0]);

  it('reads the samples of the history, 10 s for 1h and a minute for more', async () => {
    const hour = renderWithCache(() =>
      useSamples(history, { preset: '1h' }, 100, true)
    );
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(1));
    expect(h.GET).toHaveBeenCalledWith(
      SAMPLES,
      expect.objectContaining({
        params: expect.objectContaining({ path: { fileName: 'svc.yaml' } }),
      })
    );
    await tick(SHORT_WINDOW_POLL_MS);
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(2));

    hour.unmount();
    h.GET.mockClear();
    renderWithCache(() => useSamples(history, { preset: '7d' }, 100, true));
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(1));
    await tick(LONG_WINDOW_POLL_MS - 1000);
    expect(h.GET).toHaveBeenCalledTimes(1);
    await tick(1000);
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(2));
    expect(new Set(paths())).toEqual(new Set([SAMPLES]));
  });

  it('polls the availability once a minute and the events every 30 s', async () => {
    renderWithCache(() => useSampleAvailability(history, true));
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(1));
    expect(paths()).toEqual(['/dags/{fileName}/run-history/availability']);
    await tick(AVAILABILITY_POLL_MS - 1000);
    expect(h.GET).toHaveBeenCalledTimes(1);
    await tick(1000);
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(2));

    h.GET.mockClear();
    renderWithCache(() =>
      useEventsPage(history, { window: null, limit: 50 }, true)
    );
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(1));
    expect(paths()).toEqual(['/dags/{fileName}/run-history/events']);
    await tick(EVENTS_POLL_MS - 1000);
    expect(h.GET).toHaveBeenCalledTimes(1);
    await tick(1000);
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(2));
  });

  // A step and a history never share a cache entry.
  it('keeps the cache entry of a history apart from that of a step', async () => {
    const view = renderWithCache(() => [
      useSamples(history, { preset: '24h' }, 100, false),
      useSamples(target, { preset: '24h' }, 100, false),
    ]);
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(2));
    expect([...view.cache.keys()]).toHaveLength(2);
  });
});
