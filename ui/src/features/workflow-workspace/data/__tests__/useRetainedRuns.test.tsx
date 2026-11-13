// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  resetReadGates,
  resetTestRunsStarted,
  SERVICE_READ_GAP_MS,
} from '../useRunHistory';
import { RETAINED_READ_MS, useRetainedRuns } from '../useServiceDag';
import { dagPage, idleSSE, run } from './fixtures';
import { renderWorkspace, serve, type FakeServer } from './harness';

const h = vi.hoisted(() => ({
  GET: vi.fn(),
  historySSE: vi.fn(),
}));

vi.mock('@/hooks/api', async () => {
  const { createQueryHook } = await import('swr-openapi');
  return {
    useQuery: createQueryHook({ GET: h.GET } as never, '/'),
    useClient: () => ({ GET: h.GET }),
  };
});
vi.mock('@/hooks/useDAGRunSSE', () => ({ useDAGRunSSE: () => idleSSE }));
vi.mock('@/hooks/useDAGHistorySSE', () => ({
  useDAGHistorySSE: h.historySSE,
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

let server: FakeServer;

// The reads of the retained runs: the only ones with a limit.
function retainedCalls() {
  return h.GET.mock.calls.filter(
    (call) =>
      call[0] === '/dags/{fileName}/dag-runs' &&
      call[1]?.params?.query?.limit === 100
  );
}

const wait = async (ms: number) => {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
};
const message = (id: string) => ({
  ...idleSSE,
  isConnected: true,
  data: { dagRuns: [run(id)], gridData: [] },
});

function setPolicy(restartPolicy?: string) {
  server.dag = {
    ...server.dag,
    dag: { name: 'demo', restartPolicy },
  } as typeof server.dag;
}

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  resetTestRunsStarted();
  resetReadGates();
  h.GET.mockReset();
  h.historySSE.mockReset();
  h.historySSE.mockReturnValue(idleSSE);
  server = {
    dag: dagPage(run('run-1')),
    runs: {},
    history: { dagRuns: [run('run-1'), run('run-0')], gridData: [] },
    subRuns: [],
  };
  h.GET.mockImplementation(serve(server));
});
afterEach(() => {
  vi.useRealTimers();
});

describe('useRetainedRuns of a service DAG with a connected feed', () => {
  it('reads once at the opening and not again without an event', async () => {
    setPolicy('always');
    h.historySSE.mockReturnValue(message('run-1'));
    renderWorkspace(() => useRetainedRuns(true, '24h'));
    await waitFor(() => expect(retainedCalls()).toHaveLength(1));
    await wait(RETAINED_READ_MS * 3);
    expect(retainedCalls()).toHaveLength(1);
  });

  it('reads after an event, at most once in RETAINED_READ_MS', async () => {
    setPolicy('always');
    h.historySSE.mockReturnValue(message('run-1'));
    const view = renderWorkspace(() => useRetainedRuns(true, '24h'));
    await waitFor(() => expect(retainedCalls()).toHaveLength(1));
    await wait(RETAINED_READ_MS + 100);

    h.historySSE.mockReturnValue(message('run-2'));
    view.rerender();
    await waitFor(() => expect(retainedCalls()).toHaveLength(2));

    // A burst of events makes one read, at the end of the gap.
    for (const id of ['run-3', 'run-4', 'run-5']) {
      h.historySSE.mockReturnValue(message(id));
      view.rerender();
      await wait(1000);
    }
    expect(retainedCalls()).toHaveLength(2);
    await wait(RETAINED_READ_MS);
    expect(retainedCalls()).toHaveLength(3);
    await wait(RETAINED_READ_MS * 3);
    expect(retainedCalls()).toHaveLength(3);
  });

  it('reads when the window changes, at most once in the gap', async () => {
    setPolicy('always');
    h.historySSE.mockReturnValue(message('run-1'));
    let windowKey = '24h';
    const view = renderWorkspace(() => useRetainedRuns(true, windowKey));
    await waitFor(() => expect(retainedCalls()).toHaveLength(1));
    await wait(SERVICE_READ_GAP_MS + 100);

    windowKey = '7d';
    view.rerender();
    await waitFor(() => expect(retainedCalls()).toHaveLength(2));
    windowKey = '30d';
    view.rerender();
    await wait(SERVICE_READ_GAP_MS / 2);
    expect(retainedCalls()).toHaveLength(2);
    await wait(SERVICE_READ_GAP_MS);
    expect(retainedCalls()).toHaveLength(3);
  });
});

describe('useRetainedRuns without a connected feed', () => {
  it('polls each RETAINED_READ_MS when the feed is down', async () => {
    setPolicy('always');
    h.historySSE.mockReturnValue({ ...idleSSE, shouldUseFallback: true });
    renderWorkspace(() => useRetainedRuns(true, '24h'));
    await waitFor(() => expect(retainedCalls()).toHaveLength(1));
    await wait(RETAINED_READ_MS + 1000);
    await waitFor(() => expect(retainedCalls()).toHaveLength(2));
  });

  // A DAG that is not a service keeps its poll and uses no feed.
  it('polls a DAG that is not a service, as before', async () => {
    setPolicy(undefined);
    h.historySSE.mockReturnValue(message('run-1'));
    let windowKey = '24h';
    const view = renderWorkspace(() => useRetainedRuns(true, windowKey));
    await waitFor(() => expect(retainedCalls()).toHaveLength(1));
    expect(h.historySSE).toHaveBeenLastCalledWith('demo', false, 'local');
    await wait(RETAINED_READ_MS + 1000);
    await waitFor(() => expect(retainedCalls()).toHaveLength(2));
    windowKey = '7d';
    view.rerender();
    await wait(SERVICE_READ_GAP_MS * 2);
    expect(retainedCalls()).toHaveLength(2);
  });

  it('reads nothing while disabled', async () => {
    setPolicy('always');
    renderWorkspace(() => useRetainedRuns(false));
    await wait(RETAINED_READ_MS * 2);
    expect(retainedCalls()).toHaveLength(0);
  });
});
