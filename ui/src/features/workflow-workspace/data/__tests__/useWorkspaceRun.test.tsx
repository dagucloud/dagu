// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, waitFor } from '@testing-library/react';
import { useSearchParams } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, Status } from '@/api/v1/schema';
import { useWorkspaceRun } from '../useWorkspaceRun';
import {
  connectedSSE,
  dagPage,
  DAG_NAME,
  idleSSE,
  node,
  run,
  zeroRun,
} from './fixtures';
import { renderWorkspace, serve, type FakeServer } from './harness';

const h = vi.hoisted(() => ({
  GET: vi.fn(),
  runSSE: vi.fn(),
  querySpy: vi.fn(),
}));

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
vi.mock('@/hooks/useDAGRunSSE', () => ({ useDAGRunSSE: h.runSSE }));
vi.mock('@/hooks/useDAGHistorySSE', () => ({
  useDAGHistorySSE: () => ({
    data: null,
    error: null,
    isConnected: true,
    isConnecting: false,
    shouldUseFallback: false,
  }),
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

const RUN_PATH = '/dag-runs/{name}/{dagRunId}';

let server: FakeServer;

function getPaths(): string[] {
  return h.GET.mock.calls.map((call) => call[0] as string);
}

/** The options of the last pinned-run useQuery call that had a key. */
function lastRunQueryOptions() {
  const calls = h.querySpy.mock.calls.filter(
    (call) => call[0] === RUN_PATH && call[1] !== null
  );
  return calls[calls.length - 1]?.[2] as {
    refreshInterval: (latest?: unknown) => number;
  };
}

beforeEach(() => {
  h.GET.mockReset();
  h.querySpy.mockReset();
  h.runSSE.mockReset();
  h.runSSE.mockReturnValue(idleSSE);
  server = {
    dag: dagPage(run('run-1', [node('a', NodeStatus.Running)], Status.Running)),
    runs: {},
    history: { dagRuns: [], gridData: [] },
    subRuns: [],
  };
  h.GET.mockImplementation(serve(server));
});

describe('useWorkspaceRun, latest', () => {
  // parity:overview-live-updates parity:shell-sse-dag parity:chg-run-url-latest
  it('shows the latest run of the DAG cache and names it in the URL', async () => {
    const view = renderWorkspace(() => useWorkspaceRun(), {
      url: '/dags/demo?dock=steps',
    });
    await waitFor(() =>
      expect(view.result.current.location.search).toBe('?dock=steps&run=run-1')
    );
    // The redirect replaces the entry: Back does not return to a URL without ?run.
    expect(view.result.current.navigationType).toBe('REPLACE');
    const first = view.result.current.value;
    expect(first.selection).toBe('latest');
    expect(first.ref).toEqual({ name: DAG_NAME, runId: 'run-1' });
    expect(first.isLatest).toBe(true);
    expect(first.isActive).toBe(true);
    expect(first.overlay.a?.status).toBe(NodeStatus.Running);
    // The latest run is in the DAG query: it has no query of its own.
    expect(getPaths()).not.toContain(RUN_PATH);
    expect(h.runSSE).toHaveBeenLastCalledWith(
      DAG_NAME,
      'run-1',
      false,
      'local'
    );
  });

  // parity:chg-run-url-latest
  it('keeps the shown run when a newer run appears', async () => {
    server.runs['run-1'] = run(
      'run-1',
      [node('a', NodeStatus.Running)],
      Status.Running
    );
    const view = renderWorkspace(() => useWorkspaceRun(), {
      url: '/dags/demo?run=run-1',
    });
    await waitFor(() => expect(view.result.current?.value).toBeDefined());
    expect(view.result.current.value.selection).toBe('latest');

    // Same write as useSSECacheSync does for a DAG SSE event.
    await act(async () => {
      await view.page.mutate!(
        dagPage(run('run-2', [node('a', NodeStatus.Failed)], Status.Failed)),
        { revalidate: false }
      );
    });

    const next = view.result.current.value;
    expect(view.result.current.location.search).toBe('?run=run-1');
    expect(next.details?.dagRunId).toBe('run-1');
    expect(next.ref).toEqual({ name: DAG_NAME, runId: 'run-1' });
    expect(next.latestRef).toEqual({ runId: 'run-2', status: Status.Failed });
    expect(next.selection).toEqual({ runId: 'run-1' });
    expect(next.isLatest).toBe(false);
    // The query of the older run takes over from the DAG query.
    await waitFor(() => expect(getPaths()).toContain(RUN_PATH));
    expect(h.runSSE).toHaveBeenLastCalledWith(DAG_NAME, 'run-1', true, 'local');

    act(() => next.select('latest'));
    expect(view.result.current.location.search).toBe('?run=run-2');
    expect(view.result.current.value.details?.dagRunId).toBe('run-2');
    expect(view.result.current.value.selection).toBe('latest');
    expect(view.result.current.value.isLatest).toBe(true);
  });

  // parity:chg-run-url-latest
  it('adds no ?run for a workflow that never ran, and waits for a legacy ?idx', async () => {
    server.dag = dagPage(zeroRun());
    const empty = renderWorkspace(() => useWorkspaceRun(), {
      url: '/dags/demo?dock=steps',
    });
    await waitFor(() => expect(empty.result.current?.value).toBeDefined());
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20));
    });
    expect(empty.result.current.location.search).toBe('?dock=steps');
    empty.unmount();

    server.dag = dagPage(run('run-1'));
    const legacy = renderWorkspace(() => useWorkspaceRun(), {
      url: '/dags/demo/executions?idx=2',
    });
    await waitFor(() => expect(legacy.result.current?.value).toBeDefined());
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20));
    });
    expect(legacy.result.current.location.search).toBe('?idx=2');
  });

  // parity:run-page-states
  it('reports a zero-value latestDAGRun as never run', async () => {
    server.dag = dagPage(zeroRun());
    const view = renderWorkspace(() => useWorkspaceRun());
    await waitFor(() => expect(view.result.current?.value).toBeDefined());
    const value = view.result.current.value;
    expect(value.hasRun).toBe(false);
    expect(value.ref).toBeNull();
    expect(value.details).toBeNull();
    expect(value.latestRef).toBeNull();
    expect(value.overlay).toEqual({});
    expect(value.nodeByStep.size).toBe(0);
    expect(value.failedStep).toBeNull();
    expect(value.isActive).toBe(false);
  });
});

describe('useWorkspaceRun, pinned', () => {
  // parity:shell-sse-dagrun parity:shell-sse-fallback parity:api-dag-run-details parity:run-live-details
  it('queries the pinned run with its own SSE and the SSE fallback', async () => {
    server.runs['run-0'] = run('run-0', [node('a', NodeStatus.Success)]);
    const view = renderWorkspace(() => useWorkspaceRun(), {
      url: '/dags/demo?run=run-0',
    });
    await waitFor(() =>
      expect(view.result.current?.value.details?.dagRunId).toBe('run-0')
    );
    const value = view.result.current.value;
    expect(value.selection).toEqual({ runId: 'run-0' });
    expect(value.ref).toEqual({ name: DAG_NAME, runId: 'run-0' });
    expect(value.isLatest).toBe(false);
    expect(value.latestRef).toEqual({ runId: 'run-1', status: Status.Running });
    expect(h.runSSE).toHaveBeenLastCalledWith(DAG_NAME, 'run-0', true, 'local');
    expect(h.GET).toHaveBeenCalledWith(RUN_PATH, {
      params: {
        path: { name: DAG_NAME, dagRunId: 'run-0' },
        query: { remoteNode: 'local' },
      },
    });
    // The SSE is down: the query polls every 2 s, as useSSECacheSync gives.
    expect(lastRunQueryOptions().refreshInterval()).toBe(2000);

    // The SSE connects and pushes new details into the SWR cache.
    h.runSSE.mockReturnValue({
      ...connectedSSE,
      data: { dagRunDetails: run('run-0', [node('a', NodeStatus.Failed)]) },
    });
    view.rerender();
    await waitFor(() =>
      expect(view.result.current.value.overlay.a?.status).toBe(
        NodeStatus.Failed
      )
    );
    expect(lastRunQueryOptions().refreshInterval()).toBe(0);
  });

  // A test run writes no events: with the SSE up, its stream never reports
  // that it ended, so the run is read again while it is active.
  it('reads an active test run again with the SSE up', async () => {
    const testRun = (status: Status) => ({
      ...run('run-0', [node('a', NodeStatus.Running)], status),
      ephemeral: true,
    });
    server.runs['run-0'] = testRun(Status.Running);
    h.runSSE.mockReturnValue(connectedSSE);
    const view = renderWorkspace(() => useWorkspaceRun(), {
      url: '/dags/demo?run=run-0',
    });
    await waitFor(() =>
      expect(view.result.current?.value.details?.dagRunId).toBe('run-0')
    );
    const interval = lastRunQueryOptions().refreshInterval;
    expect(interval({ dagRunDetails: testRun(Status.Running) })).toBe(2000);
    expect(interval({ dagRunDetails: testRun(Status.Success) })).toBe(0);
    expect(interval({ dagRunDetails: run('run-0', [], Status.Running) })).toBe(
      0
    );
  });

  it('keeps an SSE event of another run out of the pinned cache', async () => {
    server.runs['run-0'] = run('run-0', [node('a', NodeStatus.Success)]);
    const view = renderWorkspace(() => useWorkspaceRun(), {
      url: '/dags/demo?run=run-0',
    });
    await waitFor(() =>
      expect(view.result.current?.value.details?.dagRunId).toBe('run-0')
    );
    h.runSSE.mockReturnValue({
      ...connectedSSE,
      data: { dagRunDetails: run('run-7', [node('a', NodeStatus.Failed)]) },
    });
    view.rerender();
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20));
    });
    const value = view.result.current.value;
    expect(value.notFound).toBe(false);
    expect(value.details?.dagRunId).toBe('run-0');
    expect(value.overlay.a?.status).toBe(NodeStatus.Success);
  });

  // parity:shell-run-fallback-latest
  it('falls back to latest on a 404 and keeps notFound until select', async () => {
    const view = renderWorkspace(() => useWorkspaceRun(), {
      url: '/dags/demo?run=gone&dock=steps',
    });
    server.runs['run-1'] = run('run-1', [node('a', NodeStatus.Running)]);
    await waitFor(() => expect(view.result.current?.value.notFound).toBe(true));
    await waitFor(() =>
      expect(view.result.current.location.search).toBe('?run=run-1&dock=steps')
    );
    expect(view.result.current.navigationType).toBe('REPLACE');
    const value = view.result.current.value;
    expect(value.selection).toBe('latest');
    expect(value.details?.dagRunId).toBe('run-1');
    // The notice stays: the fall back is no new selection.
    expect(value.notFound).toBe(true);

    // A newer run does not move the page either.
    await act(async () => {
      await view.page.mutate!(dagPage(run('run-3')), { revalidate: false });
    });
    expect(view.result.current.value.details?.dagRunId).toBe('run-1');
    expect(view.result.current.value.notFound).toBe(true);

    act(() => view.result.current.value.select('latest'));
    expect(view.result.current.value.notFound).toBe(false);
    expect(view.result.current.location.search).toBe('?run=run-3&dock=steps');
  });

  // The API names an older active run as the latest: a newer pinned run is
  // the latest one, so the bar shows no 'Back to latest'.
  it('counts a pinned run newer than the API latest as the latest', async () => {
    server.dag = dagPage(
      run('run-1', [node('a', NodeStatus.Running)], Status.Running, {
        startedAt: '2026-01-01T10:00:00Z',
      })
    );
    server.runs['run-2'] = run('run-2', [node('a')], Status.Success, {
      startedAt: '2026-01-01T10:00:05Z',
    });
    const view = renderWorkspace(() => useWorkspaceRun(), {
      url: '/dags/demo?run=run-2',
    });
    await waitFor(() =>
      expect(view.result.current?.value.details?.dagRunId).toBe('run-2')
    );
    expect(view.result.current.value.isLatest).toBe(true);
  });

  it('keeps an older pinned run off the latest', async () => {
    server.dag = dagPage(
      run('run-1', [node('a', NodeStatus.Running)], Status.Running, {
        startedAt: '2026-01-01T10:00:05Z',
      })
    );
    server.runs['run-2'] = run('run-2', [node('a')], Status.Success, {
      startedAt: '2026-01-01T10:00:00Z',
    });
    const view = renderWorkspace(() => useWorkspaceRun(), {
      url: '/dags/demo?run=run-2',
    });
    await waitFor(() =>
      expect(view.result.current?.value.details?.dagRunId).toBe('run-2')
    );
    expect(view.result.current.value.isLatest).toBe(false);
  });

  // The start times have a resolution of one second: a run that started in
  // the same second as the latest run is not the latest one.
  it('keeps a pinned run of the same second off the latest', async () => {
    server.dag = dagPage(
      run('run-1', [node('a')], Status.Success, {
        startedAt: '2026-01-01T10:00:05Z',
      })
    );
    server.runs['run-2'] = run('run-2', [node('a')], Status.Success, {
      startedAt: '2026-01-01T10:00:05Z',
    });
    const view = renderWorkspace(() => useWorkspaceRun(), {
      url: '/dags/demo?run=run-2',
    });
    await waitFor(() =>
      expect(view.result.current?.value.details?.dagRunId).toBe('run-2')
    );
    expect(view.result.current.value.isLatest).toBe(false);
  });

  // A run that was shown and then vanished (dequeued, deleted) is a normal
  // outcome, not a bad link: the latest run shows, with no notice.
  it('falls back without a notice when the shown run vanishes', async () => {
    server.runs.q2 = run('q2', [], Status.Queued);
    const view = renderWorkspace(() => useWorkspaceRun(), {
      url: '/dags/demo?run=q2&dock=steps',
    });
    await waitFor(() =>
      expect(view.result.current?.value.details?.dagRunId).toBe('q2')
    );
    delete server.runs.q2;
    await act(async () => {
      await view.result.current.value.refresh();
    });
    await waitFor(() =>
      expect(view.result.current.location.search).toBe('?run=run-1&dock=steps')
    );
    expect(view.result.current.value.selection).toBe('latest');
    expect(view.result.current.value.notFound).toBe(false);
  });

  it('clears notFound when a new ?run is written without select', async () => {
    server.runs['run-0'] = run('run-0');
    const view = renderWorkspace(
      () => ({ run: useWorkspaceRun(), params: useSearchParams() }),
      { url: '/dags/demo?run=gone' }
    );
    await waitFor(() =>
      expect(view.result.current?.value.run.notFound).toBe(true)
    );
    await waitFor(() =>
      expect(view.result.current.location.search).toBe('?run=run-1')
    );

    act(() => view.result.current.value.params[1]({ run: 'run-0' }));
    await waitFor(() =>
      expect(view.result.current.value.run.details?.dagRunId).toBe('run-0')
    );
    expect(view.result.current.value.run.notFound).toBe(false);
    expect(view.result.current.value.run.selection).toEqual({
      runId: 'run-0',
    });
  });

  it('falls back to latest when the details name another run', async () => {
    server.runs['run-0'] = run('run-9');
    const view = renderWorkspace(() => useWorkspaceRun(), {
      url: '/dags/demo?run=run-0',
    });
    await waitFor(() => expect(view.result.current?.value.notFound).toBe(true));
    await waitFor(() =>
      expect(view.result.current.location.search).toBe('?run=run-1')
    );
    expect(view.result.current.navigationType).toBe('REPLACE');
    expect(view.result.current.value.details?.dagRunId).toBe('run-1');
  });
});

describe('useWorkspaceRun, select', () => {
  it('pushes ?run, writes the latest run for latest and replaces on request', async () => {
    server.runs['run-5'] = run('run-5');
    server.runs['run-6'] = run('run-6');
    const view = renderWorkspace(() => useWorkspaceRun(), {
      url: '/dags/demo?dock=steps&step=a',
    });
    await waitFor(() =>
      expect(view.result.current.location.search).toContain('run=run-1')
    );

    act(() => view.result.current.value.select('run-5'));
    let params = new URLSearchParams(view.result.current.location.search);
    expect(params.get('run')).toBe('run-5');
    expect(params.get('dock')).toBe('steps');
    expect(params.get('step')).toBe('a');
    expect(view.result.current.navigationType).toBe('PUSH');
    await waitFor(() =>
      expect(view.result.current.value.details?.dagRunId).toBe('run-5')
    );

    act(() => view.result.current.value.select('run-6', { replace: true }));
    params = new URLSearchParams(view.result.current.location.search);
    expect(params.get('run')).toBe('run-6');
    expect(view.result.current.navigationType).toBe('REPLACE');

    act(() => view.result.current.value.select('latest'));
    params = new URLSearchParams(view.result.current.location.search);
    expect(params.get('run')).toBe('run-1');
    expect(params.get('dock')).toBe('steps');
    expect(params.get('step')).toBe('a');
    expect(view.result.current.navigationType).toBe('PUSH');
    expect(view.result.current.value.selection).toBe('latest');
  });
});

describe('useWorkspaceRun, applyNodeStatus', () => {
  // parity:test-run-details-hooks
  it('patches details, overlay and nodeByStep until new server details arrive', async () => {
    server.dag = dagPage(run('run-1', [node('a', NodeStatus.Failed)]));
    const view = renderWorkspace(() => useWorkspaceRun());
    await waitFor(() => expect(view.result.current?.value).toBeDefined());

    act(() =>
      view.result.current.value.applyNodeStatus('a', NodeStatus.Success)
    );
    let value = view.result.current.value;
    expect(value.details?.nodes[0]?.status).toBe(NodeStatus.Success);
    expect(value.overlay.a?.status).toBe(NodeStatus.Success);
    expect(value.nodeByStep.get('a')?.status).toBe(NodeStatus.Success);
    expect(value.failedStep).toBeNull();

    // SWR keeps the old object for equal data, so the new details differ.
    await act(async () => {
      await view.page.mutate!(
        dagPage(run('run-1', [node('a', NodeStatus.Failed)], Status.Failed)),
        { revalidate: false }
      );
    });
    value = view.result.current.value;
    expect(value.details?.nodes[0]?.status).toBe(NodeStatus.Failed);
    expect(value.overlay.a?.status).toBe(NodeStatus.Failed);
    expect(value.nodeByStep.get('a')?.status).toBe(NodeStatus.Failed);
  });

  // The SWR snapshot can hand out another object of equal content, for
  // example the SSE copy of details that the REST read had put in first,
  // when a refresh starts. An equal copy does not confirm anything.
  it('keeps the patch of a pinned run over an equal copy of its details', async () => {
    server.runs['run-0'] = run('run-0', [node('a', NodeStatus.Failed)]);
    const view = renderWorkspace(() => useWorkspaceRun(), {
      url: '/dags/demo?run=run-0',
    });
    await waitFor(() =>
      expect(view.result.current?.value.details?.dagRunId).toBe('run-0')
    );
    h.runSSE.mockReturnValue({
      ...connectedSSE,
      data: { dagRunDetails: run('run-0', [node('a', NodeStatus.Failed)]) },
    });
    view.rerender();
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20));
    });

    const serve = h.GET.getMockImplementation()!;
    let release = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    h.GET.mockImplementation(async (...args: unknown[]) => {
      await gate;
      return (serve as (...a: unknown[]) => unknown)(...args);
    });

    act(() =>
      view.result.current.value.applyNodeStatus('a', NodeStatus.Success)
    );
    let refreshed: Promise<void> = Promise.resolve();
    act(() => {
      refreshed = view.result.current.value.refresh();
    });
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50));
    });
    expect(view.result.current.value.overlay.a?.status).toBe(
      NodeStatus.Success
    );
    await act(async () => {
      release();
      await refreshed;
    });
    expect(view.result.current.value.overlay.a?.status).toBe(
      NodeStatus.Success
    );
  });

  it('drops the patch when the selection changes', async () => {
    server.dag = dagPage(run('run-1', [node('a', NodeStatus.Failed)]));
    server.runs['run-0'] = run('run-0', [node('a', NodeStatus.Failed)]);
    const view = renderWorkspace(() => useWorkspaceRun());
    await waitFor(() => expect(view.result.current?.value).toBeDefined());

    act(() =>
      view.result.current.value.applyNodeStatus('a', NodeStatus.Success)
    );
    expect(view.result.current.value.overlay.a?.status).toBe(
      NodeStatus.Success
    );

    act(() => view.result.current.value.select('run-0'));
    await waitFor(() =>
      expect(view.result.current.value.details?.dagRunId).toBe('run-0')
    );
    expect(view.result.current.value.overlay.a?.status).toBe(NodeStatus.Failed);

    // The latest details object did not change, but the patch is gone.
    act(() => view.result.current.value.select('latest'));
    expect(view.result.current.value.details?.dagRunId).toBe('run-1');
    expect(view.result.current.value.overlay.a?.status).toBe(NodeStatus.Failed);
  });
});

describe('useWorkspaceRun, waiting', () => {
  it('counts approvals, human tasks and waiting agent sessions', async () => {
    server.dag = dagPage(
      run(
        'run-1',
        [
          node('done'),
          node('agent', NodeStatus.Running, {
            agentSession: { provider: 'x', state: 'waiting' },
          }),
          node('approve', NodeStatus.Waiting, { step: { approval: {} } }),
          node('form', NodeStatus.Waiting, { step: { humanTask: {} } }),
          node('broken', NodeStatus.Rejected),
        ],
        Status.Waiting
      )
    );
    const view = renderWorkspace(() => useWorkspaceRun());
    await waitFor(() => expect(view.result.current?.value).toBeDefined());
    expect(view.result.current.value.waiting).toEqual({
      approvals: 1,
      humanTasks: 1,
      agent: 1,
      firstWaitingStep: 'agent',
    });
    expect(view.result.current.value.failedStep).toBe('broken');
  });
});
