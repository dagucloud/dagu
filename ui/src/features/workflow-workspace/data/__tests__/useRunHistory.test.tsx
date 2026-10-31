// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useQuery } from '@/hooks/api';
import {
  noteTestRunStarted,
  resetTestRunsStarted,
  TEST_RUN_POLL_MS,
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
      isLoading: false,
    });
  });

  it('shares the DAGExecutionHistory key and follows the history SSE', async () => {
    const view = renderWorkspace(() => {
      const history = useRunHistory({ enabled: true });
      // The query of DAGExecutionHistory.tsx, with the same params.
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
  const withTest = () => {
    server.history = {
      dagRuns: [
        { ...run('test-1'), ephemeral: true },
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

    it('reads the list again every 5 s while it holds a test run', async () => {
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
