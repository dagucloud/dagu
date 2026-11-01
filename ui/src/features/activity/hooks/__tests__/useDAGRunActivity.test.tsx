// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Status } from '@/api/v1/schema';
import { useClient, useQuery } from '@/hooks/api';
import { useDAGRunActivitySSE } from '@/hooks/useDAGRunActivitySSE';
import type { ActivityRun, DAGRunActivity } from '../../lib/activityModel';
import { type ActivityWindow, useDAGRunActivity } from '../useDAGRunActivity';

vi.mock('@/hooks/api', () => ({ useQuery: vi.fn(), useClient: vi.fn() }));
vi.mock('@/hooks/useDAGRunActivitySSE', () => ({
  useDAGRunActivitySSE: vi.fn(),
}));

const useQueryMock = vi.mocked(
  useQuery as unknown as (
    path: string,
    init: unknown,
    config: Record<string, unknown>
  ) => unknown
);
const useSSEMock = vi.mocked(useDAGRunActivitySSE);
const getMock = vi.fn();
vi.mocked(useClient).mockReturnValue({ GET: getMock } as never);

function run(dagRunId: string): ActivityRun {
  return { name: 'etl', dagRunId, status: Status.Success, startedAt: 1 };
}

function page(runs: ActivityRun[], nextCursor?: string): DAGRunActivity {
  return {
    from: 100,
    to: 200,
    runs,
    nextCursor,
    attention: [],
    active: [],
    upcoming: [],
    queues: [],
  };
}

function runIDs(runs: ActivityRun[]): string[] {
  return runs.map((item) => item.dagRunId);
}

type QueryResult = { data?: unknown; error?: unknown };
let queryResult: QueryResult = {};

function setVisibility(state: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    get: () => state,
  });
  document.dispatchEvent(new Event('visibilitychange'));
}

function lastSSEEnabled(): boolean | undefined {
  const calls = useSSEMock.mock.calls;
  return calls[calls.length - 1]?.[1];
}

function lastQueryConfig(): Record<string, unknown> | undefined {
  const calls = useQueryMock.mock.calls;
  return calls[calls.length - 1]?.[2];
}

describe('useDAGRunActivity', () => {
  beforeEach(() => {
    queryResult = {};
    setVisibility('visible');
    useSSEMock.mockReturnValue({
      data: null,
      error: null,
      isConnected: true,
      isConnecting: false,
      shouldUseFallback: false,
    } as never);
    useQueryMock.mockImplementation(() => ({
      ...queryResult,
      isLoading: false,
      mutate: vi.fn().mockResolvedValue(undefined),
    }));
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it('streams a live window only while the tab is visible', () => {
    renderHook(() => useDAGRunActivity({ kind: 'live' }, {}, 'local'));
    expect(lastSSEEnabled()).toBe(true);

    act(() => setVisibility('hidden'));
    expect(lastSSEEnabled()).toBe(false);

    act(() => setVisibility('visible'));
    expect(lastSSEEnabled()).toBe(true);
  });

  it('reads a fixed window once without streaming', () => {
    renderHook(() =>
      useDAGRunActivity({ kind: 'fixed', from: 1, to: 2 }, {}, 'local')
    );

    expect(lastSSEEnabled()).toBe(false);
    expect(lastQueryConfig()).toMatchObject({ refreshInterval: 0 });
  });

  // An older remote node answers 404; polling it forever would only repeat
  // the error.
  it('stops streaming and polling a node without the endpoint until refreshed', () => {
    queryResult = { error: { status: 404, message: 'not found' } };
    const { result } = renderHook(() =>
      useDAGRunActivity({ kind: 'live' }, {}, 'old-node')
    );

    expect(result.current.unavailable).toBe(true);
    expect(lastSSEEnabled()).toBe(false);
    expect(lastQueryConfig()).toMatchObject({ refreshInterval: 0 });

    queryResult = { data: page([]) };
    act(() => {
      void result.current.refresh();
    });
    expect(result.current.unavailable).toBe(false);
    expect(lastSSEEnabled()).toBe(true);
  });

  it('keeps a live window streaming through a transient read error', () => {
    queryResult = { error: { status: 503, message: 'unavailable' } };
    const { result } = renderHook(() =>
      useDAGRunActivity({ kind: 'live' }, {}, 'local')
    );

    expect(result.current.unavailable).toBe(false);
    expect(lastSSEEnabled()).toBe(true);
  });

  it('loads older runs with the first page’s window and cursor', async () => {
    queryResult = { data: page([run('new')], 'cursor-1') };
    getMock.mockResolvedValue({ data: page([run('old')], 'cursor-2') });
    const { result } = renderHook(() =>
      useDAGRunActivity({ kind: 'fixed', from: 100, to: 200 }, {}, 'local')
    );

    await act(() => result.current.loadMore());

    expect(getMock).toHaveBeenCalledWith('/dag-run-activity', {
      params: {
        query: {
          fromDate: 100,
          toDate: 200,
          cursor: 'cursor-1',
          remoteNode: 'local',
        },
      },
      signal: expect.any(AbortSignal),
    });
    expect(runIDs(result.current.runs)).toEqual(['new', 'old']);
    expect(result.current.hasMore).toBe(true);
    expect(result.current.paused).toBe(false);
  });

  // Merging a moving first page into older pages would leave gaps, so a live
  // window holds its runs until resumed.
  it('holds a live window’s runs while older ones are shown', async () => {
    queryResult = { data: page([run('b')], 'cursor-1') };
    getMock.mockResolvedValue({ data: page([run('a')]) });
    const { result, rerender } = renderHook(() =>
      useDAGRunActivity({ kind: 'live' }, {}, 'local')
    );
    await act(() => result.current.loadMore());

    queryResult = { data: page([run('c'), run('b')], 'cursor-2') };
    rerender();

    expect(runIDs(result.current.runs)).toEqual(['b', 'a']);
    expect(result.current.paused).toBe(true);
    expect(result.current.hasMore).toBe(false);

    act(() => result.current.resume());

    expect(runIDs(result.current.runs)).toEqual(['c', 'b']);
    expect(result.current.paused).toBe(false);
  });

  it('starts from the first page again when the window changes', async () => {
    queryResult = { data: page([run('b')], 'cursor-1') };
    getMock.mockResolvedValue({ data: page([run('a')]) });
    const { result, rerender } = renderHook(
      ({ window }: { window: ActivityWindow }) =>
        useDAGRunActivity(window, {}, 'local'),
      { initialProps: { window: { kind: 'live' } as ActivityWindow } }
    );
    await act(() => result.current.loadMore());

    rerender({ window: { kind: 'fixed', from: 100, to: 200 } });

    expect(runIDs(result.current.runs)).toEqual(['b']);
    expect(result.current.hasMore).toBe(true);
  });
});
