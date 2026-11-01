// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useRunHistory } from '../useRunHistory';
import { useWorkspaceRefresh } from '../useWorkspaceRefresh';
import { useWorkspaceRun } from '../useWorkspaceRun';
import { dagPage, deferred, idleSSE, run } from './fixtures';
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
  useDAGHistorySSE: () => ({ ...idleSSE, isConnected: true }),
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

const RUN_PATH = '/dag-runs/{name}/{dagRunId}';
const HISTORY_PATH = '/dags/{fileName}/dag-runs';

let server: FakeServer;

function count(path: string) {
  return h.GET.mock.calls.filter((call) => call[0] === path).length;
}

beforeEach(() => {
  h.GET.mockReset();
  server = {
    dag: dagPage(run('run-1')),
    runs: { 'run-0': run('run-0') },
    history: { dagRuns: [run('run-1'), run('run-0')], gridData: [] },
    subRuns: [],
  };
  h.GET.mockImplementation(serve(server));
});

// parity:shell-refresh-data parity:run-header-refresh
describe('useWorkspaceRefresh', () => {
  it('refreshes the DAG, the pinned run and the history in parallel', async () => {
    const refreshDag = vi.fn(() => Promise.resolve());
    const view = renderWorkspace(
      () => ({
        refresh: useWorkspaceRefresh(),
        run: useWorkspaceRun(),
        history: useRunHistory({ enabled: true }),
      }),
      { url: '/dags/demo?run=run-0', refreshDag }
    );
    await waitFor(() => {
      expect(view.result.current?.value.run.details?.dagRunId).toBe('run-0');
      expect(view.result.current.value.history.runs).toHaveLength(2);
    });
    expect(view.result.current.value.run.refresh).toBe(
      view.result.current.value.refresh
    );

    const dagRead = deferred<void>();
    const runRead = deferred();
    const historyRead = deferred();
    refreshDag.mockImplementationOnce(() => dagRead.promise);
    const answer = serve(server);
    h.GET.mockImplementation(async (path: string, init: never) => {
      if (path === RUN_PATH) await runRead.promise;
      if (path === HISTORY_PATH) await historyRead.promise;
      return answer(path, init);
    });
    const runsBefore = count(RUN_PATH);
    const historyBefore = count(HISTORY_PATH);

    let settled = false;
    let done!: Promise<void>;
    act(() => {
      done = view.result.current.value.refresh().then(() => {
        settled = true;
      });
    });
    // All three reads start before any of them finishes.
    await waitFor(() => {
      expect(refreshDag).toHaveBeenCalledTimes(1);
      expect(count(RUN_PATH)).toBe(runsBefore + 1);
      expect(count(HISTORY_PATH)).toBe(historyBefore + 1);
    });

    await act(async () => {
      runRead.resolve(undefined);
      historyRead.resolve(undefined);
      await Promise.resolve();
    });
    expect(settled).toBe(false);
    await act(async () => {
      dagRead.resolve(undefined);
      await done;
    });
    expect(settled).toBe(true);
  });

  it('refreshes only the DAG on latest without a history list', async () => {
    const refreshDag = vi.fn(() => Promise.resolve());
    const view = renderWorkspace(
      () => ({
        refresh: useWorkspaceRefresh(),
        history: useRunHistory({ enabled: false }),
      }),
      { refreshDag }
    );
    await waitFor(() => expect(view.result.current?.value).toBeDefined());
    const callsBefore = h.GET.mock.calls.length;
    await act(async () => {
      await view.result.current.value.refresh();
    });
    expect(refreshDag).toHaveBeenCalledTimes(1);
    expect(h.GET.mock.calls.length).toBe(callsBefore);
    expect(count(RUN_PATH)).toBe(0);
    expect(count(HISTORY_PATH)).toBe(0);
  });

  it('stops the history refresh when the history list unmounts', async () => {
    const refreshDag = vi.fn(() => Promise.resolve());
    let showHistory = true;
    function useBoth() {
      const refresh = useWorkspaceRefresh();
      const history = useRunHistory({ enabled: showHistory });
      return { refresh, history };
    }
    const view = renderWorkspace(useBoth, { refreshDag });
    await waitFor(() =>
      expect(view.result.current?.value.history.runs).toHaveLength(2)
    );
    showHistory = false;
    view.rerender();
    const historyBefore = count(HISTORY_PATH);
    await act(async () => {
      await view.result.current.value.refresh();
    });
    expect(count(HISTORY_PATH)).toBe(historyBefore);
  });

  it('settles after every read and rejects with the first error', async () => {
    const refreshDag = vi.fn(() => Promise.reject(new Error('dag down')));
    const view = renderWorkspace(() => useWorkspaceRefresh(), {
      url: '/dags/demo?run=run-0',
      refreshDag,
    });
    await waitFor(() => expect(view.result.current?.value).toBeDefined());
    await act(async () => {
      await expect(view.result.current.value()).rejects.toThrow('dag down');
    });
  });
});
