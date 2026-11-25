// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Status } from '@/api/v1/schema';
import { useClient, useQuery } from '@/hooks/api';
import { useDAGListActivitySSE } from '@/hooks/useDAGListActivitySSE';
import { RequestTimeoutError } from '@/lib/requestTimeout';
import { useDAGListActivity } from '../useDAGListActivity';

vi.mock('@/hooks/api', () => ({ useQuery: vi.fn(), useClient: vi.fn() }));
vi.mock('@/hooks/useDAGListActivitySSE', () => ({
  useDAGListActivitySSE: vi.fn(),
}));

const useQueryMock = vi.mocked(
  useQuery as unknown as (
    path: string,
    init: unknown,
    config: Record<string, unknown>
  ) => unknown
);
vi.mocked(useClient).mockReturnValue({ GET: vi.fn() } as never);

const FIRST_PAGE_ONLY = { through: 1, readWith: '' };

type ListQuery = { remoteNode?: string; name?: string };

function lastQueryConfig(): Record<string, unknown> | undefined {
  const calls = useQueryMock.mock.calls;
  return calls[calls.length - 1]?.[2];
}

function activityPage(fileName: string, runs: number) {
  return {
    dags: [
      {
        fileName,
        activity: {
          runs: Array.from({ length: runs }, (_, index) => ({
            dagRunId: `run-${index}`,
            status: Status.Success,
            startedAt: '2026-11-25T00:00:00Z',
          })),
          truncated: false,
        },
      },
    ],
    errors: [],
  };
}

// A model of SWR: the data of the query that the hook asks for, keyed here
// by node and name, or the data that it gave before when the hook asks to
// keep it.
function answerQueries(pages: Record<string, unknown>) {
  let previous: unknown;
  useQueryMock.mockImplementation((_path, init, config) => {
    const query = (init as { params: { query: ListQuery } } | null)?.params
      .query;
    const own = query
      ? pages[`${query.remoteNode}/${query.name ?? ''}`]
      : undefined;
    if (own !== undefined) previous = own;
    return {
      data: own ?? (config.keepPreviousData ? previous : undefined),
      mutate: vi.fn(),
    };
  });
}

function renderActivity(query: ListQuery) {
  return renderHook(
    (props: ListQuery) => useDAGListActivity(props, FIRST_PAGE_ONLY, true),
    { initialProps: query }
  );
}

describe('useDAGListActivity', () => {
  beforeEach(() => {
    vi.mocked(useDAGListActivitySSE).mockReturnValue({
      data: null,
      error: null,
      isConnected: false,
      isConnecting: false,
      shouldUseFallback: true,
    } as never);
    useQueryMock.mockReturnValue({ data: undefined, mutate: vi.fn() });
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  // SWR does not poll a key that holds an error: without a retry, one failed
  // read would stop the fallback poll.
  it('retries a failed read, but not on a node without the endpoint', () => {
    renderHook(() =>
      useDAGListActivity({ remoteNode: 'local' }, FIRST_PAGE_ONLY, true)
    );
    const retries = lastQueryConfig()?.shouldRetryOnError as (
      error: unknown
    ) => boolean;

    expect(retries({ code: 'internal_error', message: 'failed' })).toBe(true);
    expect(retries(new RequestTimeoutError(10_000))).toBe(true);
    expect(retries({ code: 'not_found', message: 'no such path' })).toBe(false);
    expect(retries({ status: 501, message: 'not implemented' })).toBe(false);
  });

  it('keeps the activity of a node while a new query of it loads', () => {
    answerQueries({ 'a/': activityPage('deploy.yaml', 2) });
    const { result, rerender } = renderActivity({ remoteNode: 'a' });
    expect(result.current?.get('deploy.yaml')?.runs).toHaveLength(2);

    rerender({ remoteNode: 'a', name: 'dep' });

    expect(result.current?.get('deploy.yaml')?.runs).toHaveLength(2);
  });

  // Two nodes can each hold a deploy.yaml, which are not the same workflow.
  it('drops the activity of another node while the new node loads', () => {
    answerQueries({ 'a/': activityPage('deploy.yaml', 2) });
    const { result, rerender } = renderActivity({ remoteNode: 'a' });
    expect(result.current?.get('deploy.yaml')?.runs).toHaveLength(2);

    rerender({ remoteNode: 'b' });

    expect(result.current?.get('deploy.yaml')).toBeUndefined();
  });
});
