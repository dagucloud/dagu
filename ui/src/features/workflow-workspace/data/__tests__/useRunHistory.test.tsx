// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Status } from '@/api/v1/schema';
import { useQuery } from '@/hooks/api';
import { TEST_RUN_TTL_MS } from '../testRun';
import {
  noteTestRunStarted,
  resetReadGates,
  resetTestRunsStarted,
  SERVICE_READ_GAP_MS,
  TEST_RUN_POLL_MS,
  TEST_RUN_REMOVAL_SLACK_MS,
  useRunHistory,
} from '../useRunHistory';
import { dagPage, FILE_NAME, idleSSE, run } from './fixtures';
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

const HISTORY_PATH = '/dags/{fileName}/dag-runs';

let server: FakeServer;

function historyCalls() {
  return h.GET.mock.calls.filter((call) => call[0] === HISTORY_PATH);
}

beforeEach(() => {
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

// parity:api-dag-history
describe('useRunHistory', () => {
  it('makes no request while disabled', async () => {
    const view = renderWorkspace(() => useRunHistory({ enabled: false }));
    await waitFor(() => expect(view.result.current?.value).toBeDefined());
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20));
    });
    expect(historyCalls()).toHaveLength(0);
    expect(h.historySSE).toHaveBeenCalledWith(FILE_NAME, false, 'local');
    expect(view.result.current.value).toEqual({
      runs: [],
      gridData: [],
      nextCursor: null,
      isLoading: false,
    });
  });

  // A run whose workflow file is gone has no history to read: it is the only
  // run of the page.
  it('lists only a run without its workflow file and reads no history', async () => {
    const orphan = run('run-9');
    const view = renderWorkspace(() => useRunHistory({ enabled: true }), {
      standaloneRun: orphan,
    });
    await waitFor(() => expect(view.result.current?.value).toBeDefined());
    expect(view.result.current.value).toEqual({
      runs: [orphan],
      gridData: [],
      nextCursor: null,
      isLoading: false,
    });
    expect(historyCalls()).toHaveLength(0);
  });

  it('shares the plain history key and follows the history SSE', async () => {
    const view = renderWorkspace(() => {
      const history = useRunHistory({ enabled: true });
      // The same history query with the same params, as another reader.
      useQuery(HISTORY_PATH, {
        params: {
          query: { remoteNode: 'local' },
          path: { fileName: FILE_NAME },
        },
      });
      return history;
    });
    await waitFor(() =>
      expect(view.result.current?.value.runs).toHaveLength(2)
    );
    expect(historyCalls()).toEqual([
      [
        HISTORY_PATH,
        {
          params: {
            query: { remoteNode: 'local' },
            path: { fileName: FILE_NAME },
          },
        },
      ],
    ]);
    expect(h.historySSE).toHaveBeenLastCalledWith(FILE_NAME, true, 'local');

    h.historySSE.mockReturnValue({
      ...idleSSE,
      isConnected: true,
      data: { dagRuns: [run('run-2')], gridData: [] },
    });
    view.rerender();
    await waitFor(() =>
      expect(view.result.current.value.runs.map((r) => r.dagRunId)).toEqual([
        'run-2',
      ])
    );
  });
});

// The test runs of the DAG are in no list but the history that asks for them.
describe('useRunHistory with the test runs', () => {
  const withTest = (status: Status = Status.Running, finishedAt?: string) => {
    server.history = {
      dagRuns: [
        {
          ...run('test-1', undefined, status, { finishedAt }),
          ephemeral: true,
        },
        run('run-1'),
        run('run-0'),
      ],
      gridData: [],
    };
  };

  it('asks for the test runs with a key of its own', async () => {
    withTest();
    const view = renderWorkspace(() => ({
      plain: useRunHistory({ enabled: true }),
      tests: useRunHistory({ enabled: true, includeEphemeral: true }),
    }));
    await waitFor(() =>
      expect(view.result.current?.value.tests.runs).toHaveLength(3)
    );
    const queries = historyCalls().map((call) => call[1].params.query);
    expect(queries).toContainEqual({ remoteNode: 'local' });
    expect(queries).toContainEqual({
      remoteNode: 'local',
      includeEphemeral: true,
    });
  });

  // The feed of events has no test run: it must not replace the list that
  // has them. An event only reads the list again.
  it('keeps the test runs when the history SSE sends its list, and reads again', async () => {
    withTest();
    const view = renderWorkspace(() =>
      useRunHistory({ enabled: true, includeEphemeral: true })
    );
    await waitFor(() =>
      expect(view.result.current?.value.runs).toHaveLength(3)
    );
    const before = historyCalls().length;
    h.historySSE.mockReturnValue({
      ...idleSSE,
      isConnected: true,
      data: { dagRuns: [run('run-2')], gridData: [] },
    });
    view.rerender();
    await waitFor(() => expect(historyCalls().length).toBeGreaterThan(before));
    expect(view.result.current.value.runs.map((r) => r.dagRunId)).toEqual([
      'test-1',
      'run-1',
      'run-0',
    ]);
  });

  // Every reader of the list gets the same event object from the feed: one
  // request answers all of them.
  it('reads the list once for an event, however many hooks read it', async () => {
    withTest();
    h.historySSE.mockReturnValue({ ...idleSSE, isConnected: true });
    const view = renderWorkspace(() => [
      useRunHistory({ enabled: true, includeEphemeral: true }),
      useRunHistory({ enabled: true, includeEphemeral: true }),
      useRunHistory({ enabled: true, includeEphemeral: true }),
    ]);
    await waitFor(() =>
      expect(view.result.current?.value[0]?.runs).toHaveLength(3)
    );
    const before = historyCalls().length;
    h.historySSE.mockReturnValue({
      ...idleSSE,
      isConnected: true,
      data: { dagRuns: [run('run-2')], gridData: [] },
    });
    view.rerender();
    await waitFor(() => expect(historyCalls().length).toBeGreaterThan(before));
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20));
    });
    expect(historyCalls()).toHaveLength(before + 1);
  });

  describe('polling', () => {
    beforeEach(() => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      // With the SSE connected, only the timeline polls: the test runs have
      // no event.
      h.historySSE.mockReturnValue({ ...idleSSE, isConnected: true });
    });
    afterEach(() => {
      vi.useRealTimers();
    });

    const poll = async (ms: number) => {
      await act(async () => {
        await vi.advanceTimersByTimeAsync(ms);
      });
    };

    it('reads the list again every 5 s while a test run in it is active', async () => {
      withTest();
      renderWorkspace(() =>
        useRunHistory({
          enabled: true,
          includeEphemeral: true,
          pollTestRuns: true,
        })
      );
      await waitFor(() => expect(historyCalls().length).toBeGreaterThan(0));
      const first = historyCalls().length;
      await poll(TEST_RUN_POLL_MS + 100);
      expect(historyCalls().length).toBeGreaterThan(first);
    });

    // SWR restarts the poll timer when its interval changes: a caller that
    // renders more often than the poll must not hold the poll off.
    it('keeps polling while the caller renders more often than the poll', async () => {
      withTest();
      const view = renderWorkspace(() =>
        useRunHistory({
          enabled: true,
          includeEphemeral: true,
          pollTestRuns: true,
        })
      );
      await waitFor(() => expect(historyCalls().length).toBeGreaterThan(0));
      const first = historyCalls().length;
      for (let elapsed = 0; elapsed <= TEST_RUN_POLL_MS; elapsed += 1000) {
        await poll(1000);
        view.rerender();
      }
      expect(historyCalls().length).toBeGreaterThan(first);
    });

    // A test run that ended changes no more: the server only removes it, 10
    // minutes later and with no event. The list is read once at that time.
    it('reads the list once when an ended test run is removed', async () => {
      withTest(Status.Success, new Date().toISOString());
      const view = renderWorkspace(() =>
        useRunHistory({
          enabled: true,
          includeEphemeral: true,
          pollTestRuns: true,
        })
      );
      await waitFor(() =>
        expect(view.result.current?.value.runs).toHaveLength(3)
      );
      const first = historyCalls().length;
      await poll(TEST_RUN_TTL_MS + TEST_RUN_REMOVAL_SLACK_MS - 1000);
      expect(historyCalls().length).toBe(first);

      server.history = { dagRuns: [run('run-1'), run('run-0')], gridData: [] };
      await poll(2000);
      expect(historyCalls().length).toBe(first + 1);
      await waitFor(() =>
        expect(view.result.current.value.runs.map((r) => r.dagRunId)).toEqual([
          'run-1',
          'run-0',
        ])
      );
    });

    // The server can remove a test run late: a list that still holds it is
    // read up to 3 more times, a minute apart, then left alone.
    it('reads the list a few more times while a removed test run stays', async () => {
      withTest(Status.Success, new Date().toISOString());
      const view = renderWorkspace(() =>
        useRunHistory({
          enabled: true,
          includeEphemeral: true,
          pollTestRuns: true,
        })
      );
      await waitFor(() =>
        expect(view.result.current?.value.runs).toHaveLength(3)
      );
      const first = historyCalls().length;
      await poll(TEST_RUN_TTL_MS + TEST_RUN_REMOVAL_SLACK_MS + 1000);
      expect(historyCalls().length).toBe(first + 1);
      for (let retry = 1; retry <= 3; retry++) {
        await poll(60_000);
        expect(historyCalls().length).toBe(first + 1 + retry);
      }
      await poll(60_000 * 3);
      expect(historyCalls().length).toBe(first + 4);
    });

    it('reads the list again a minute after a failed read at the removal', async () => {
      withTest(Status.Success, new Date().toISOString());
      const view = renderWorkspace(() =>
        useRunHistory({
          enabled: true,
          includeEphemeral: true,
          pollTestRuns: true,
        })
      );
      await waitFor(() =>
        expect(view.result.current?.value.runs).toHaveLength(3)
      );
      const first = historyCalls().length;
      const answer = serve(server);
      h.GET.mockImplementation(async (path: string, init?: object) =>
        path === HISTORY_PATH
          ? {
              error: { code: 'internal', message: 'unavailable' },
              response: { status: 500 },
            }
          : answer(path, init)
      );
      await poll(TEST_RUN_TTL_MS + TEST_RUN_REMOVAL_SLACK_MS + 1000);
      expect(historyCalls().length).toBe(first + 1);

      h.GET.mockImplementation(answer);
      server.history = { dagRuns: [run('run-1'), run('run-0')], gridData: [] };
      await poll(60_000);
      expect(historyCalls().length).toBe(first + 2);
      await waitFor(() =>
        expect(view.result.current.value.runs.map((r) => r.dagRunId)).toEqual([
          'run-1',
          'run-0',
        ])
      );
      await poll(60_000 * 3);
      expect(historyCalls().length).toBe(first + 2);
    });

    it('does not poll without a test run in the list or in this tab', async () => {
      renderWorkspace(() =>
        useRunHistory({
          enabled: true,
          includeEphemeral: true,
          pollTestRuns: true,
        })
      );
      await waitFor(() => expect(historyCalls().length).toBeGreaterThan(0));
      const first = historyCalls().length;
      await poll(TEST_RUN_POLL_MS * 2);
      expect(historyCalls().length).toBe(first);
    });

    it('does not poll a list that the timeline does not own', async () => {
      withTest();
      renderWorkspace(() =>
        useRunHistory({ enabled: true, includeEphemeral: true })
      );
      await waitFor(() => expect(historyCalls().length).toBeGreaterThan(0));
      const first = historyCalls().length;
      await poll(TEST_RUN_POLL_MS * 2);
      expect(historyCalls().length).toBe(first);
    });

    it('polls once this tab started a test run, before the list shows it', async () => {
      renderWorkspace(() =>
        useRunHistory({
          enabled: true,
          includeEphemeral: true,
          pollTestRuns: true,
        })
      );
      await waitFor(() => expect(historyCalls().length).toBeGreaterThan(0));
      act(() => noteTestRunStarted(FILE_NAME));
      const first = historyCalls().length;
      await poll(TEST_RUN_POLL_MS + 100);
      expect(historyCalls().length).toBeGreaterThan(first);
    });

    // A start that the list never shows (the run failed to start, or it
    // ended and went) keeps the polling only for a while.
    it('stops polling a while after the start when the list shows no test run', async () => {
      renderWorkspace(() =>
        useRunHistory({
          enabled: true,
          includeEphemeral: true,
          pollTestRuns: true,
        })
      );
      await waitFor(() => expect(historyCalls().length).toBeGreaterThan(0));
      act(() => noteTestRunStarted(FILE_NAME));
      await poll(60_000);
      const settled = historyCalls().length;
      await poll(TEST_RUN_POLL_MS * 2);
      expect(historyCalls().length).toBe(settled);
    });
  });
});

// A service DAG runs often and each run sends an event: its runs are read at
// most once in SERVICE_READ_GAP_MS (spec 097).
describe('useRunHistory of a service DAG', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  const serve1 = (restartPolicy?: string) => {
    server.dag = {
      ...server.dag,
      dag: { name: 'demo', restartPolicy },
    } as typeof server.dag;
  };
  const wait = async (ms: number) => {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(ms);
    });
  };
  const event = (id: string) => ({
    ...idleSSE,
    isConnected: true,
    data: { dagRuns: [run(id)], gridData: [] },
  });
  const timeline = () =>
    renderWorkspace(() =>
      useRunHistory({ enabled: true, includeEphemeral: true })
    );

  it('reads once at the end of the gap for a burst of events', async () => {
    serve1('always');
    h.historySSE.mockReturnValue({ ...idleSSE, isConnected: true });
    const view = timeline();
    await waitFor(() => expect(historyCalls()).toHaveLength(1));

    // The first event reads at once or waits for the gap: never twice.
    for (const id of ['run-2', 'run-3', 'run-4']) {
      h.historySSE.mockReturnValue(event(id));
      view.rerender();
      await wait(1000);
    }
    expect(historyCalls().length).toBeLessThanOrEqual(1);
    await wait(SERVICE_READ_GAP_MS);
    expect(historyCalls()).toHaveLength(2);
    // Nothing more is due: the burst made one read.
    await wait(SERVICE_READ_GAP_MS * 2);
    expect(historyCalls()).toHaveLength(2);
  });

  it('reads an event at once when the gap is over', async () => {
    serve1('unless-stopped');
    h.historySSE.mockReturnValue({ ...idleSSE, isConnected: true });
    const view = timeline();
    await waitFor(() => expect(historyCalls()).toHaveLength(1));
    await wait(SERVICE_READ_GAP_MS + 100);

    h.historySSE.mockReturnValue(event('run-2'));
    view.rerender();
    await waitFor(() => expect(historyCalls()).toHaveLength(2));
  });

  it('polls a feed that does not connect at the gap, not every 2 s', async () => {
    serve1('always');
    h.historySSE.mockReturnValue({ ...idleSSE, shouldUseFallback: true });
    timeline();
    await waitFor(() => expect(historyCalls()).toHaveLength(1));
    await wait(SERVICE_READ_GAP_MS - 500);
    expect(historyCalls()).toHaveLength(1);
    await wait(1000);
    await waitFor(() => expect(historyCalls()).toHaveLength(2));
  });

  it('shares the gap between the readers of one list', async () => {
    serve1('always');
    h.historySSE.mockReturnValue({ ...idleSSE, isConnected: true });
    const view = renderWorkspace(() => [
      useRunHistory({ enabled: true, includeEphemeral: true }),
      useRunHistory({ enabled: true, includeEphemeral: true }),
    ]);
    await waitFor(() => expect(historyCalls().length).toBeGreaterThan(0));
    const before = historyCalls().length;
    h.historySSE.mockReturnValue(event('run-2'));
    view.rerender();
    await wait(SERVICE_READ_GAP_MS + 100);
    expect(historyCalls().length - before).toBeLessThanOrEqual(1);
  });

  // A normal DAG keeps its reads: each event reads the list at once.
  it('does not delay the reads of a DAG that is not a service', async () => {
    serve1(undefined);
    h.historySSE.mockReturnValue({ ...idleSSE, isConnected: true });
    const view = timeline();
    await waitFor(() => expect(historyCalls()).toHaveLength(1));
    h.historySSE.mockReturnValue(event('run-2'));
    view.rerender();
    await waitFor(() => expect(historyCalls()).toHaveLength(2));
    h.historySSE.mockReturnValue(event('run-3'));
    view.rerender();
    await waitFor(() => expect(historyCalls()).toHaveLength(3));
  });
});

// A caller of the newest page takes the runs of a service DAG from the feed
// while it is connected: an event does not read the list again.
describe('useRunHistory of a service DAG for the newest page', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  const setPolicy = (restartPolicy?: string) => {
    server.dag = {
      ...server.dag,
      dag: { name: 'demo', restartPolicy },
    } as typeof server.dag;
  };
  const wait = async (ms: number) => {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(ms);
    });
  };
  const event = (id: string) => ({
    ...idleSSE,
    isConnected: true,
    data: { dagRuns: [run(id)], gridData: [] },
  });
  const newest = () =>
    renderWorkspace(() =>
      useRunHistory({
        enabled: true,
        includeEphemeral: true,
        newestOnly: true,
      })
    );
  const ids = (view: ReturnType<typeof newest>) =>
    view.result.current.value.runs.map((r) => r.dagRunId);

  it('does not read again while the feed is connected', async () => {
    setPolicy('always');
    h.historySSE.mockReturnValue({ ...idleSSE, isConnected: true });
    const view = newest();
    await waitFor(() => expect(historyCalls()).toHaveLength(1));

    for (const id of ['run-2', 'run-3', 'run-4']) {
      h.historySSE.mockReturnValue(event(id));
      view.rerender();
      await wait(SERVICE_READ_GAP_MS + 1000);
      expect(ids(view)).toEqual([id]);
    }
    await wait(SERVICE_READ_GAP_MS * 3);
    expect(historyCalls()).toHaveLength(1);
  });

  it('keeps the test runs of the last read in the runs of the feed', async () => {
    setPolicy('always');
    const test = run('test-1', [], Status.Success, {
      ephemeral: true,
      startedAt: '2026-01-15T02:00:00Z',
    });
    server.history = { dagRuns: [run('run-1'), test], gridData: [] };
    h.historySSE.mockReturnValue({ ...idleSSE, isConnected: true });
    const view = newest();
    await waitFor(() => expect(historyCalls()).toHaveLength(1));
    await waitFor(() => expect(ids(view)).toContain('test-1'));

    h.historySSE.mockReturnValue({
      ...idleSSE,
      isConnected: true,
      data: {
        dagRuns: [
          run('run-2', [], Status.Success, {
            startedAt: '2026-01-15T03:00:00Z',
          }),
        ],
        gridData: [],
      },
    });
    view.rerender();
    await waitFor(() => expect(ids(view)).toEqual(['run-2', 'test-1']));
    expect(historyCalls()).toHaveLength(1);
  });

  it('reads at the gap when the feed is down', async () => {
    setPolicy('always');
    h.historySSE.mockReturnValue({ ...idleSSE, shouldUseFallback: true });
    newest();
    await waitFor(() => expect(historyCalls()).toHaveLength(1));
    await wait(SERVICE_READ_GAP_MS + 1000);
    await waitFor(() => expect(historyCalls()).toHaveLength(2));
  });

  // A DAG that is not a service keeps its reads: each event reads the list.
  it('reads each event of a DAG that is not a service', async () => {
    setPolicy(undefined);
    h.historySSE.mockReturnValue({ ...idleSSE, isConnected: true });
    const view = newest();
    await waitFor(() => expect(historyCalls()).toHaveLength(1));
    h.historySSE.mockReturnValue(event('run-2'));
    view.rerender();
    await waitFor(() => expect(historyCalls()).toHaveLength(2));
  });

  it('keeps the reads of a caller of the older pages', async () => {
    setPolicy('always');
    h.historySSE.mockReturnValue({ ...idleSSE, isConnected: true });
    const view = renderWorkspace(() =>
      useRunHistory({ enabled: true, includeEphemeral: true })
    );
    await waitFor(() => expect(historyCalls()).toHaveLength(1));
    await wait(SERVICE_READ_GAP_MS + 100);
    h.historySSE.mockReturnValue(event('run-2'));
    view.rerender();
    await waitFor(() => expect(historyCalls()).toHaveLength(2));
  });
});
