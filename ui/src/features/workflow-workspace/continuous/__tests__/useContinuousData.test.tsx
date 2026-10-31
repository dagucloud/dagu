// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  POLL_MS,
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

function renderSamples(window: SamplesWindow, poll: boolean) {
  const cache = new Map();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <SWRConfig value={{ provider: () => cache, dedupingInterval: 0 }}>
      {children}
    </SWRConfig>
  );
  const view = renderHook(() => useStepSamples(target, window, 100, poll), {
    wrapper,
  });
  return { cache, rerender: view.rerender };
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
    await tick(POLL_MS);
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(2));
    // A render after the clock moved keeps the key.
    rerender();
    await tick(0);

    const [first, second] = requests();
    expect(Date.parse(first!.to) - Date.parse(first!.from)).toBe(DAY);
    expect(Date.parse(second!.to) - Date.parse(second!.from)).toBe(DAY);
    expect(Date.parse(second!.from)).toBeGreaterThanOrEqual(
      Date.parse(first!.from) + POLL_MS
    );
    expect([...cache.keys()]).toHaveLength(1);
  });

  it('asks for the times of a fixed window and does not poll it', async () => {
    const from = Date.parse('2026-10-20T00:00:00Z');
    const to = from + DAY;
    renderSamples({ from, to }, false);
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(1));
    await tick(POLL_MS * 3);

    expect(h.GET).toHaveBeenCalledTimes(1);
    expect(requests()[0]).toEqual({
      remoteNode: 'local',
      from: new Date(from).toISOString(),
      to: new Date(to).toISOString(),
      points: 100,
    });
  });
});

describe('useStepEventsPage', () => {
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
