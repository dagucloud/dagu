// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ArtifactListQuery } from '../artifactListPagination';
import { usePaginatedArtifacts } from '../artifactListPagination';

const getMock = vi.fn();
const client = {
  GET: getMock,
};
type HeadPage = {
  items: ReturnType<typeof createItem>[];
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

function createQuery(
  overrides: Partial<ArtifactListQuery> = {}
): ArtifactListQuery {
  return {
    name: 'reporter',
    ...overrides,
  };
}

function createItem(name: string, dagRunId: string) {
  return {
    name,
    dagRunId,
    createdAt: '2026-09-15T14:32:07Z',
    startedAt: '2026-09-15T14:40:00Z',
    files: [],
    filesTruncated: false,
  };
}

describe('usePaginatedArtifacts', () => {
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

  it('appends continuation pages after the head page', async () => {
    useQueryState.data = {
      items: [createItem('reporter', 'run-4'), createItem('reporter', 'run-3')],
      nextCursor: 'cursor-1',
    };
    getMock.mockResolvedValueOnce({
      data: {
        items: [createItem('reporter', 'run-2'), createItem('reporter', 'run-1')],
        nextCursor: 'cursor-2',
      },
    });

    const { result } = renderHook(() =>
      usePaginatedArtifacts({ query: createQuery() })
    );

    await act(async () => {
      await result.current.loadMore();
    });

    expect(result.current.items.map((item) => item.dagRunId)).toEqual([
      'run-4',
      'run-3',
      'run-2',
      'run-1',
    ]);
    expect(result.current.hasMore).toBe(true);
    expect(getMock.mock.calls[0]?.[1]).toMatchObject({
      params: {
        query: expect.objectContaining({ cursor: 'cursor-1' }),
      },
    });
  });

  it('re-anchors the row chain when the head page moves', async () => {
    useQueryState.data = {
      items: [createItem('reporter', 'run-4'), createItem('reporter', 'run-3')],
      nextCursor: 'cursor-1',
    };
    getMock.mockResolvedValueOnce({
      data: {
        items: [createItem('reporter', 'run-2'), createItem('reporter', 'run-1')],
        nextCursor: 'cursor-2',
      },
    });

    const { result, rerender } = renderHook(() =>
      usePaginatedArtifacts({ query: createQuery() })
    );

    await act(async () => {
      await result.current.loadMore();
    });
    expect(result.current.items.map((item) => item.dagRunId)).toEqual([
      'run-4',
      'run-3',
      'run-2',
      'run-1',
    ]);

    // A new run lands and the API page boundary shifts: the head page now
    // starts at run 5 and run 3 slid onto the second page.
    useQueryState.data = {
      items: [createItem('reporter', 'run-5'), createItem('reporter', 'run-4')],
      nextCursor: 'cursor-3',
    };
    getMock.mockResolvedValueOnce({
      data: {
        items: [createItem('reporter', 'run-3'), createItem('reporter', 'run-2')],
        nextCursor: 'cursor-4',
      },
    });

    rerender({ query: createQuery() });

    await waitFor(() => {
      expect(result.current.items.map((item) => item.dagRunId)).toEqual([
        'run-5',
        'run-4',
      ]);
    });

    await act(async () => {
      await result.current.loadMore();
    });

    // The continuation continues from the new head, so run 3 is recovered
    // instead of being lost between stale pages.
    expect(result.current.items.map((item) => item.dagRunId)).toEqual([
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
      items: [createItem('reporter', 'run-4'), createItem('reporter', 'run-3')],
      nextCursor: 'cursor-1',
    };
    let resolveStalePage!: (value: unknown) => void;
    getMock.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveStalePage = resolve;
      })
    );

    const { result, rerender } = renderHook(() =>
      usePaginatedArtifacts({ query: createQuery() })
    );
    let pending!: Promise<void>;
    act(() => {
      pending = result.current.loadMore();
    });

    useQueryState.data = {
      items: [createItem('reporter', 'run-5'), createItem('reporter', 'run-4')],
      nextCursor: 'cursor-3',
    };
    rerender();

    await act(async () => {
      resolveStalePage({
        data: {
          items: [
            createItem('reporter', 'run-2'),
            createItem('reporter', 'run-1'),
          ],
          nextCursor: null,
        },
      });
      await pending;
    });

    expect(result.current.items.map((item) => item.dagRunId)).toEqual([
      'run-5',
      'run-4',
    ]);
    expect(result.current.hasMore).toBe(true);

    getMock.mockResolvedValueOnce({
      data: {
        items: [createItem('reporter', 'run-3')],
        nextCursor: null,
      },
    });
    await act(async () => {
      await result.current.loadMore();
    });

    expect(result.current.items.map((item) => item.dagRunId)).toEqual([
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

  it('deduplicates runs that appear in the head and continuation pages', async () => {
    useQueryState.data = {
      items: [createItem('reporter', 'run-4'), createItem('reporter', 'run-3')],
      nextCursor: 'cursor-1',
    };
    getMock.mockResolvedValueOnce({
      data: {
        items: [createItem('reporter', 'run-3'), createItem('reporter', 'run-2')],
        nextCursor: null,
      },
    });

    const { result } = renderHook(() =>
      usePaginatedArtifacts({ query: createQuery() })
    );

    await act(async () => {
      await result.current.loadMore();
    });

    expect(result.current.items.map((item) => item.dagRunId)).toEqual([
      'run-4',
      'run-3',
      'run-2',
    ]);
    expect(result.current.hasMore).toBe(false);
  });
});
