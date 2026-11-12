// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, Status, StatusLabel } from '@/api/v1/schema';
import { useWorkspaceRun } from '../useWorkspaceRun';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { useSubRuns } from '../useSubRuns';
import { dagPage, DAG_NAME, idleSSE, node, run } from './fixtures';
import { renderWorkspace, serve, type FakeServer } from './harness';

const h = vi.hoisted(() => ({ GET: vi.fn(), querySpy: vi.fn() }));

vi.mock('@/hooks/api', async () => {
  const { createQueryHook } = await import('swr-openapi');
  const realUseQuery = createQueryHook({ GET: h.GET } as never, '/');
  return {
    useQuery: (...args: unknown[]) => {
      h.querySpy(...args);
      return (realUseQuery as (...a: unknown[]) => unknown)(...args);
    },
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

const SUB_RUNS_PATH = '/dag-runs/{name}/{dagRunId}/sub-dag-runs';

let server: FakeServer;

/** The refreshInterval of the last useSubRuns query that had a key. */
function lastSubRunsInterval() {
  const calls = h.querySpy.mock.calls.filter(
    (call) => call[0] === SUB_RUNS_PATH && call[1] !== null
  );
  return (calls[calls.length - 1]?.[2] as { refreshInterval: number })
    .refreshInterval;
}

function subRunCalls() {
  return h.GET.mock.calls.filter((call) => call[0] === SUB_RUNS_PATH);
}

/** The sub-run query of TimelineChart.tsx, with the same params. */
function useTimelineChartSubRuns(
  rootDagName: string,
  rootDagRunId: string,
  parentSubDAGRunId?: string
) {
  return useQuery(
    SUB_RUNS_PATH,
    whenEnabled(true, {
      params: {
        path: { name: rootDagName, dagRunId: rootDagRunId },
        query: { remoteNode: 'local', parentSubDAGRunId },
      },
    }),
    { refreshInterval: 0 }
  );
}

beforeEach(() => {
  h.GET.mockReset();
  h.querySpy.mockReset();
  server = {
    dag: dagPage(run('run-1')),
    runs: {},
    history: { dagRuns: [], gridData: [] },
    subRuns: [
      {
        dagRunId: 'child-1',
        dagName: 'child',
        status: Status.Success,
        statusLabel: StatusLabel.succeeded,
        startedAt: '2026-01-15T02:00:00Z',
      },
    ],
  };
  h.GET.mockImplementation(serve(server));
});

// parity:api-sub-dag-runs parity:dup-11
describe('useSubRuns', () => {
  it('shares one request with another consumer and TimelineChart', async () => {
    const ref = { name: DAG_NAME, runId: 'run-1' };
    const view = renderWorkspace(() => {
      const first = useSubRuns(ref);
      const second = useSubRuns(ref);
      useTimelineChartSubRuns(DAG_NAME, 'run-1');
      return { first, second };
    });
    await waitFor(() =>
      expect(view.result.current?.value.first.subRuns).toHaveLength(1)
    );
    expect(view.result.current.value.second.subRuns).toHaveLength(1);
    expect(subRunCalls()).toEqual([
      [
        SUB_RUNS_PATH,
        {
          params: {
            path: { name: DAG_NAME, dagRunId: 'run-1' },
            query: { remoteNode: 'local', parentSubDAGRunId: undefined },
          },
        },
      ],
    ]);
  });

  it('passes parentSubDAGRunId as TimelineChart does', async () => {
    const ref = { name: DAG_NAME, runId: 'run-1' };
    const view = renderWorkspace(() => {
      const subRuns = useSubRuns(ref, 'child-1');
      useTimelineChartSubRuns(DAG_NAME, 'run-1', 'child-1');
      return subRuns;
    });
    await waitFor(() =>
      expect(view.result.current?.value.subRuns).toHaveLength(1)
    );
    expect(subRunCalls()).toHaveLength(1);
    expect(subRunCalls()[0]?.[1]).toEqual({
      params: {
        path: { name: DAG_NAME, dagRunId: 'run-1' },
        query: { remoteNode: 'local', parentSubDAGRunId: 'child-1' },
      },
    });
  });

  it('makes no request without a run', async () => {
    const view = renderWorkspace(() => useSubRuns(null));
    await waitFor(() => expect(view.result.current?.value).toBeDefined());
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20));
    });
    expect(subRunCalls()).toHaveLength(0);
    expect(view.result.current.value).toEqual({
      subRuns: [],
      isLoading: false,
    });
  });

  // TimelineChart polls every 3 s only while the run is running or queued.
  it.each([
    [Status.Running, 3000],
    [Status.Queued, 3000],
    [Status.Waiting, 0],
    [Status.Success, 0],
  ])('polls the selected run in status %s every %i ms', async (status, ms) => {
    server.dag = dagPage(run('run-1', [node('a', NodeStatus.Running)], status));
    const view = renderWorkspace(() => {
      const selected = useWorkspaceRun();
      return useSubRuns(selected.ref);
    });
    await waitFor(() =>
      expect(view.result.current?.value.subRuns).toHaveLength(1)
    );
    expect(lastSubRunsInterval()).toBe(ms);
  });

  it('does not poll a run that is not the selected one', async () => {
    server.dag = dagPage(
      run('run-1', [node('a', NodeStatus.Running)], Status.Running)
    );
    const view = renderWorkspace(() =>
      useSubRuns({ name: DAG_NAME, runId: 'run-0' })
    );
    await waitFor(() =>
      expect(view.result.current?.value.subRuns).toHaveLength(1)
    );
    expect(lastSubRunsInterval()).toBe(0);
  });
});
