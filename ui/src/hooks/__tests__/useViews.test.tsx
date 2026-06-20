// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ViewSpecType } from '@/api/v1/schema';
import { useQuery } from '@/hooks/api';
import { useViews, type ViewSpec } from '@/hooks/useViews';

const mutate = vi.fn();
const post = vi.fn();
const put = vi.fn();
const del = vi.fn();

vi.mock('@/hooks/api', () => ({
  useQuery: vi.fn(),
  useClient: () => ({ POST: post, PUT: put, DELETE: del }),
}));

// useQuery's swr-openapi return type is too deep for tsc to instantiate
// against mockReturnValue, so the mock is given a minimal shape here.
const useQueryMock = vi.mocked(useQuery) as unknown as {
  mockReturnValue: (value: unknown) => void;
};

function spec(name: string): ViewSpec {
  return { name, type: ViewSpecType.kanban, lookbackDays: 3, pinned: false };
}

beforeEach(() => {
  vi.clearAllMocks();
  useQueryMock.mockReturnValue({
    data: {
      views: [
        {
          id: 'v1',
          name: 'A',
          type: 'kanban',
          lookbackDays: 3,
          createdAt: '',
          updatedAt: '',
        },
      ],
    },
    error: null,
    isLoading: false,
    mutate,
  });
  post.mockResolvedValue({ data: { id: 'new' }, error: undefined });
  put.mockResolvedValue({ data: { id: 'v1' }, error: undefined });
  del.mockResolvedValue({ error: undefined });
});

describe('useViews', () => {
  it('derives the views list from the response', () => {
    const { result } = renderHook(() => useViews());
    expect(result.current.views.map((v) => v.id)).toEqual(['v1']);
  });

  it('creates a view with the remote node and refreshes', async () => {
    const { result } = renderHook(() => useViews());
    await act(async () => {
      await result.current.createView(spec('X'));
    });
    expect(post).toHaveBeenCalledWith('/views', {
      params: { query: { remoteNode: 'local' } },
      body: expect.objectContaining({ name: 'X' }),
    });
    expect(mutate).toHaveBeenCalled();
  });

  it('updates a view by id', async () => {
    const { result } = renderHook(() => useViews());
    await act(async () => {
      await result.current.updateView('v1', spec('Y'));
    });
    expect(put).toHaveBeenCalledWith('/views/{viewId}', {
      params: { path: { viewId: 'v1' }, query: { remoteNode: 'local' } },
      body: expect.objectContaining({ name: 'Y' }),
    });
    expect(mutate).toHaveBeenCalled();
  });

  it('deletes a view by id', async () => {
    const { result } = renderHook(() => useViews());
    await act(async () => {
      await result.current.deleteView('v1');
    });
    expect(del).toHaveBeenCalledWith('/views/{viewId}', {
      params: { path: { viewId: 'v1' }, query: { remoteNode: 'local' } },
    });
    expect(mutate).toHaveBeenCalled();
  });

  it('throws when a mutation returns an API error', async () => {
    post.mockResolvedValue({ error: { message: 'boom' } });
    const { result } = renderHook(() => useViews());
    await expect(result.current.createView(spec('X'))).rejects.toThrow('boom');
  });
});
