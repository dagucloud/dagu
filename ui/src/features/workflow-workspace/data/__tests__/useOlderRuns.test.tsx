// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ApiDAGRunDetails } from '../types';
import { useOlderRuns } from '../useOlderRuns';
import { dagPage, FILE_NAME, idleSSE, run } from './fixtures';
import { renderWorkspace, serve, type FakeServer } from './harness';

const h = vi.hoisted(() => ({ GET: vi.fn() }));

vi.mock('@/hooks/api', async () => {
  const { createQueryHook } = await import('swr-openapi');
  return {
    useQuery: createQueryHook({ GET: h.GET } as never, '/'),
    useClient: () => ({ GET: h.GET }),
  };
});
vi.mock('@/hooks/useDAGRunSSE', () => ({ useDAGRunSSE: () => idleSSE }));
vi.mock('@/hooks/useDAGHistorySSE', () => ({
  useDAGHistorySSE: () => idleSSE,
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

const HISTORY_PATH = '/dags/{fileName}/dag-runs';

// The pages after the head, by cursor. A cursor that is not here fails.
const PAGES: Record<
  string,
  { dagRuns: ApiDAGRunDetails[]; nextCursor?: string }
> = {
  c1: { dagRuns: [run('r3'), run('r2')], nextCursor: 'c2' },
  c2: { dagRuns: [run('r1')] },
};

let server: FakeServer;

function pageCalls(): (string | undefined)[] {
  return h.GET.mock.calls
    .filter((call) => call[0] === HISTORY_PATH)
    .map((call) => call[1]?.params?.query?.cursor);
}

function ids(runs: readonly ApiDAGRunDetails[]): string[] {
  return runs.map((r) => r.dagRunId);
}

beforeEach(() => {
  h.GET.mockReset();
  server = {
    dag: dagPage(run('r5')),
    runs: {},
    history: { dagRuns: [], gridData: [] },
    subRuns: [],
  };
  const base = serve(server);
  h.GET.mockImplementation(
    async (
      path: string,
      init?: { params?: { query?: { cursor?: string } } }
    ) => {
      if (path !== HISTORY_PATH) return base(path, init as never);
      const page = PAGES[init?.params?.query?.cursor ?? ''];
      return page
        ? { data: { ...page, gridData: [] } }
        : { error: { code: 'bad_request', message: 'bad cursor' } };
    }
  );
});

function renderOlder(head: ApiDAGRunDetails[], cursor: string | null) {
  const state = { head, cursor };
  const view = renderWorkspace(() => useOlderRuns(state.head, state.cursor));
  return { view, state };
}

describe('useOlderRuns', () => {
  it('reads the pages after the head from its cursor, on request', async () => {
    const { view } = renderOlder([run('r5'), run('r4')], 'c1');
    await waitFor(() => expect(view.result.current?.value).toBeDefined());
    expect(view.result.current.value.runs).toEqual([]);
    expect(view.result.current.value.hasMore).toBe(true);
    expect(pageCalls()).toEqual([]);

    act(() => view.result.current.value.loadMore());
    await waitFor(() =>
      expect(ids(view.result.current.value.runs)).toEqual(['r3', 'r2'])
    );
    expect(view.result.current.value.hasMore).toBe(true);

    act(() => view.result.current.value.loadMore());
    await waitFor(() =>
      expect(ids(view.result.current.value.runs)).toEqual(['r3', 'r2', 'r1'])
    );
    expect(view.result.current.value.hasMore).toBe(false);
    expect(pageCalls()).toEqual(['c1', 'c2']);
    expect(h.GET).toHaveBeenCalledWith(
      HISTORY_PATH,
      expect.objectContaining({
        params: {
          path: { fileName: FILE_NAME },
          query: { remoteNode: 'local', cursor: 'c1', limit: 30 },
        },
      })
    );
  });

  // A new run moves the head: the run that left it stays, and the next page
  // follows the pages read, not the new cursor of the head.
  it('keeps the runs that a newer run pushes out of the head', async () => {
    const { view, state } = renderOlder([run('r5'), run('r4')], 'c1');
    await waitFor(() => expect(view.result.current?.value).toBeDefined());
    act(() => view.result.current.value.loadMore());
    await waitFor(() =>
      expect(ids(view.result.current.value.runs)).toEqual(['r3', 'r2'])
    );

    state.head = [run('r6'), run('r5')];
    state.cursor = 'moved';
    view.rerender();
    expect(ids(view.result.current.value.runs)).toEqual(['r4', 'r3', 'r2']);

    act(() => view.result.current.value.loadMore());
    await waitFor(() =>
      expect(ids(view.result.current.value.runs)).toEqual([
        'r4',
        'r3',
        'r2',
        'r1',
      ])
    );
    expect(pageCalls()).toEqual(['c1', 'c2']);
  });

  it('waits for a retry after a failed read', async () => {
    const { view } = renderOlder([run('r5')], 'gone');
    await waitFor(() => expect(view.result.current?.value).toBeDefined());
    act(() => view.result.current.value.loadMore());
    await waitFor(() => expect(view.result.current.value.failed).toBe(true));
    expect(view.result.current.value.loading).toBe(false);
    expect(view.result.current.value.runs).toEqual([]);

    PAGES.gone = { dagRuns: [run('r4')] };
    try {
      act(() => view.result.current.value.loadMore());
      await waitFor(() =>
        expect(ids(view.result.current.value.runs)).toEqual(['r4'])
      );
      expect(view.result.current.value.failed).toBe(false);
    } finally {
      delete PAGES.gone;
    }
  });

  it('reads nothing when the head has no cursor', async () => {
    const { view } = renderOlder([run('r1')], null);
    await waitFor(() => expect(view.result.current?.value).toBeDefined());
    expect(view.result.current.value.hasMore).toBe(false);
    act(() => view.result.current.value.loadMore());
    expect(pageCalls()).toEqual([]);
  });
});
