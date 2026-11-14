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
  useStepAvailability,
  useStepEventsPage,
  useStepSamples,
  type SamplesWindow,
} from '../useContinuousData';

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
const target = { name: 'demo', runId: 'run-1', step: 'heartbeat' };

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
  return renderWithCache(() => useStepSamples(target, window, 100, poll));
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

describe('useStepSamples', () => {
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
describe('useStepAvailability', () => {
  it('polls the availability once a minute', async () => {
    renderWithCache(() => useStepAvailability(target, true));
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(1));
    await tick(AVAILABILITY_POLL_MS - 1000);
    expect(h.GET).toHaveBeenCalledTimes(1);
    await tick(1000);
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(2));
  });
});

describe('useStepEventsPage', () => {
  it('polls the first page every 30 s', async () => {
    renderWithCache(() =>
      useStepEventsPage(target, { window: null, limit: 50 }, true)
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
        useStepEventsPage(target, { before, window: null, limit: 50 }, false),
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
