// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Status, StatusLabel, TriggerType } from '@/api/v1/schema';
import type { DAGRunListQuery } from '../dagRunPagination';
import { usePaginatedDAGRuns } from '../dagRunPagination';

const getMock = vi.fn();
const client = {
  GET: getMock,
};
type HeadPage = {
  dagRuns: ReturnType<typeof createRun>[];
  nextCursor: string | null;
};
const useQueryState: {
  data: HeadPage | null;
  error: unknown;
  isLoading: boolean;
  mutate: () => Promise<void>;
} = {
  data: null,
  error: null,
  isLoading: false,
  mutate: vi.fn(async () => {}),
};

vi.mock('@/hooks/api', () => ({
  useClient: () => client,
  useQuery: () => useQueryState,
}));

vi.mock('@/hooks/useDAGRunsListSSE', () => ({
  useDAGRunsListSSE: () => ({
    data: null,
    error: null,
    isConnected: false,
    isConnecting: false,
    shouldUseFallback: true,
  }),
}));

vi.mock('@/hooks/useSSECacheSync', () => ({
  sseFallbackOptions: () => ({ refreshInterval: 0 }),
  useSSECacheSync: () => {},
}));

function createQuery(
  overrides: Partial<DAGRunListQuery> = {}
): DAGRunListQuery {
  return {
    fromDate: 100,
    ...overrides,
  };
}

function createRun(dagRunId: string) {
  return {
    dagRunId,
    name: 'reporter',
    status: Status.Success,
    statusLabel: StatusLabel.succeeded,
    artifactsAvailable: false,
    autoRetryCount: 0,
    autoRetryLimit: 0,
    triggerType: TriggerType.manual,
    queuedAt: '2026-09-15T14:32:07Z',
    scheduleTime: '',
    startedAt: '2026-09-15T14:32:07Z',
    finishedAt: '2026-09-15T14:40:00Z',
  };
}

describe('usePaginatedDAGRuns', () => {
  beforeEach(() => {
    getMock.mockReset();
    useQueryState.data = null;
    useQueryState.error = null;
    useQueryState.isLoading = false;
    useQueryState.mutate = vi.fn(async () => {});
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('re-anchors the row chain when the head page moves', async () => {
    useQueryState.data = {
      dagRuns: [createRun('run-4'), createRun('run-3')],
      nextCursor: 'cursor-1',
    };
    getMock.mockResolvedValueOnce({
      data: {
        dagRuns: [createRun('run-2'), createRun('run-1')],
        nextCursor: 'cursor-2',
      },
    });

    const { result, rerender } = renderHook(() =>
      usePaginatedDAGRuns({ query: createQuery() })
    );

    await act(async () => {
      await result.current.loadMore();
    });
    expect(result.current.dagRuns.map((run) => run.dagRunId)).toEqual([
      'run-4',
      'run-3',
      'run-2',
      'run-1',
    ]);

    // The head page moves (run 5 lands on top) and run 3 slides onto the
    // second page; the previously loaded continuation pages are stale.
    useQueryState.data = {
      dagRuns: [createRun('run-5'), createRun('run-4')],
      nextCursor: 'cursor-3',
    };
    getMock.mockResolvedValueOnce({
      data: {
        dagRuns: [createRun('run-3'), createRun('run-2')],
        nextCursor: 'cursor-4',
      },
    });

    rerender({ query: createQuery() });

    await waitFor(() => {
      expect(result.current.dagRuns.map((run) => run.dagRunId)).toEqual([
        'run-5',
        'run-4',
      ]);
    });

    await act(async () => {
      await result.current.loadMore();
    });

    // Continuation continues from the new head, so run 3 is recovered
    // instead of being lost between stale pages.
    expect(result.current.dagRuns.map((run) => run.dagRunId)).toEqual([
      'run-5',
      'run-4',
      'run-3',
      'run-2',
    ]);
    expect(getMock.mock.calls[1]?.[1]).toMatchObject({
      params: {
        query: expect.objectContaining({ cursor: 'cursor-3' }),
      },
    });
  });

  it('ignores an in-flight page when the head moves', async () => {
    useQueryState.data = {
      dagRuns: [createRun('run-4'), createRun('run-3')],
      nextCursor: 'cursor-1',
    };
    let resolveStalePage!: (value: unknown) => void;
    getMock.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveStalePage = resolve;
      })
    );

    const { result, rerender } = renderHook(() =>
      usePaginatedDAGRuns({ query: createQuery() })
    );
    let pending!: Promise<void>;
    act(() => {
      pending = result.current.loadMore();
    });

    useQueryState.data = {
      dagRuns: [createRun('run-5'), createRun('run-4')],
      nextCursor: 'cursor-3',
    };
    rerender();

    await act(async () => {
      resolveStalePage({
        data: {
          dagRuns: [createRun('run-2'), createRun('run-1')],
          nextCursor: null,
        },
      });
      await pending;
    });

    expect(result.current.dagRuns.map((run) => run.dagRunId)).toEqual([
      'run-5',
      'run-4',
    ]);
    expect(result.current.hasMore).toBe(true);

    getMock.mockResolvedValueOnce({
      data: {
        dagRuns: [createRun('run-3')],
        nextCursor: null,
      },
    });
    await act(async () => {
      await result.current.loadMore();
    });

    expect(result.current.dagRuns.map((run) => run.dagRunId)).toEqual([
      'run-5',
      'run-4',
      'run-3',
    ]);
    expect(getMock.mock.calls[1]?.[1]).toMatchObject({
      params: {
        query: expect.objectContaining({ cursor: 'cursor-3' }),
      },
    });
  });
});
