// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Status } from '@/api/v1/schema';
import { WorkspaceDataContext, type WorkspaceDataValue } from '../context';
import type { ApiDAGRunDetails } from '../types';
import {
  CUSTOM_RANGE_KEY,
  RUN_MODE_KEY,
  RUN_RANGE_KEY,
  SHOW_TESTS_KEY,
  useHistoryRuns,
  useRunViewSettings,
  useVisibleRuns,
} from '../useVisibleRuns';
import { run } from './fixtures';

const h = vi.hoisted(() => ({
  history: { runs: [] as unknown[], isLoading: false },
  args: [] as unknown[],
  selection: 'latest' as 'latest' | { runId: string },
  latestRunId: 'n2' as string | null,
  ref: null as { runId: string } | null,
  details: null as unknown,
  older: { runs: [] as unknown[] },
}));

vi.mock('../useRunHistory', () => ({
  useRunHistory: (args: unknown) => {
    h.args.push(args);
    return h.history;
  },
}));
vi.mock('../useOlderRuns', () => ({
  useOlderRuns: () => h.older,
}));
vi.mock('../useWorkspaceRun', () => ({
  useWorkspaceRun: () => ({
    selection: h.selection,
    latestRef: h.latestRunId ? { runId: h.latestRunId } : null,
    ref: h.ref,
    details: h.details,
  }),
}));

const NOW = Date.parse('2026-03-02T12:00:00Z');

function at(
  id: string,
  startedAt: string,
  extra: Partial<ApiDAGRunDetails> = {}
) {
  return run(id, [], Status.Success, {
    startedAt,
    finishedAt: new Date(Date.parse(startedAt) + 1000).toISOString(),
    ...extra,
  });
}

// Newest first, as the server sends them: a normal run, a test run between
// two normal runs.
const N2 = at('n2', '2026-03-02T11:00:00Z');
const T1 = at('t1', '2026-03-02T10:00:00Z', { ephemeral: true });
const N1 = at('n1', '2026-03-02T09:00:00Z');
const HISTORY = [N2, T1, N1];

beforeEach(() => {
  window.localStorage.clear();
  window.sessionStorage.clear();
  h.history = { runs: HISTORY, isLoading: false };
  h.args = [];
  h.selection = 'latest';
  h.latestRunId = 'n2';
  h.details = null;
  h.ref = null;
  h.older = { runs: [] };
  vi.useFakeTimers({ toFake: ['Date'] });
  vi.setSystemTime(NOW);
});

const ids = (runs: readonly ApiDAGRunDetails[]) => runs.map((r) => r.dagRunId);

describe('useVisibleRuns', () => {
  it('asks for the history with the test runs', () => {
    renderHook(() => useVisibleRuns({ enabled: true, pollTestRuns: true }));
    expect(h.args[0]).toEqual({
      enabled: true,
      includeEphemeral: true,
      pollTestRuns: true,
      newestOnly: true,
    });
  });

  it('lists the runs oldest first, the test runs merged by time', () => {
    const { result } = renderHook(() => useVisibleRuns({ enabled: true }));
    expect(ids(result.current.runs)).toEqual(['n1', 't1', 'n2']);
    expect(result.current.hasTestRuns).toBe(true);
  });

  it('moves normal, test, normal, in the order of the timeline', () => {
    h.selection = { runId: 'n1' };
    const { result } = renderHook(() => useVisibleRuns({ enabled: true }));
    expect(result.current.adjacentId('newer')).toBe('t1');
    expect(result.current.adjacentId('older')).toBeNull();
  });

  it('moves from the latest run to the test run that is older', () => {
    const { result } = renderHook(() => useVisibleRuns({ enabled: true }));
    expect(result.current.adjacentId('older')).toBe('t1');
    expect(result.current.adjacentId('newer')).toBeNull();
  });

  // A test run that is newer than the latest run reads as 'latest' in the
  // selection, but the shown run is the test run.
  it('selects the shown run when it is a test run that reads as latest', () => {
    h.ref = { runId: 't1' };
    const { result } = renderHook(() => useVisibleRuns({ enabled: true }));
    expect(result.current.selectedId).toBe('t1');
    expect(result.current.adjacentId('older')).toBe('n1');
    expect(result.current.adjacentId('newer')).toBe('n2');
  });

  it('skips the test runs that the timeline hides', () => {
    window.localStorage.setItem(SHOW_TESTS_KEY, '0');
    const { result } = renderHook(() => useVisibleRuns({ enabled: true }));
    expect(ids(result.current.runs)).toEqual(['n1', 'n2']);
    expect(result.current.adjacentId('older')).toBe('n1');
  });

  it('follows the toggle at once, in every view of it', () => {
    const both = renderHook(() => ({
      visible: useVisibleRuns({ enabled: true }),
      settings: useRunViewSettings(),
    }));
    expect(ids(both.result.current.visible.runs)).toHaveLength(3);
    act(() => both.result.current.settings[1].setShowTests(false));
    expect(ids(both.result.current.visible.runs)).toEqual(['n1', 'n2']);
    expect(window.localStorage.getItem(SHOW_TESTS_KEY)).toBe('0');
  });

  // The selected test run expired: the page selects another run, or the
  // list lacks the selected run for a moment. Its time puts it in the list.
  it('keeps the arrows working when the selected run left the list', () => {
    h.selection = { runId: 't-gone' };
    h.details = at('t-gone', '2026-03-02T09:30:00Z', { ephemeral: true });
    const { result } = renderHook(() => useVisibleRuns({ enabled: true }));
    expect(result.current.adjacentId('older')).toBe('n1');
    expect(result.current.adjacentId('newer')).toBe('t1');
  });

  it('cuts the runs by the count, and moves the window back to the selection', () => {
    window.localStorage.setItem('dagu.workspace.runTimelineCount', '5');
    h.history = {
      // Newest first: x11 at 11:00 ... x0 at 00:00.
      runs: Array.from({ length: 12 }, (_, i) =>
        at(`x${11 - i}`, `2026-03-02T${String(11 - i).padStart(2, '0')}:00:00Z`)
      ),
      isLoading: false,
    };
    h.latestRunId = 'x11';
    const { result } = renderHook(() => useVisibleRuns({ enabled: true }));
    expect(result.current.runs).toHaveLength(5);
    expect(result.current.pool).toHaveLength(12);
    // The oldest run of the window has no older run in the view.
    h.selection = { runId: ids(result.current.runs)[0]! };
    const again = renderHook(() => useVisibleRuns({ enabled: true }));
    expect(again.result.current.adjacentId('older')).toBeNull();
  });

  it('keeps the runs that overlap a time range', () => {
    window.localStorage.setItem(RUN_MODE_KEY, 'range');
    window.localStorage.setItem(RUN_RANGE_KEY, '{"preset":"1h"}');
    // The last hour: 11:00 to 12:00.
    const { result } = renderHook(() => useVisibleRuns({ enabled: true }));
    expect(ids(result.current.runs)).toEqual(['n2']);
    expect(result.current.range).toEqual({ from: NOW - 3_600_000, to: NOW });
    expect(result.current.adjacentId('older')).toBeNull();
  });

  // The clock of a preset ticks once a minute, but the preset ends now: a
  // run that starts between two ticks is in the range at once.
  it('keeps a run that started after the last tick of a preset', () => {
    window.localStorage.setItem(RUN_MODE_KEY, 'range');
    window.localStorage.setItem(RUN_RANGE_KEY, '{"preset":"1h"}');
    const view = renderHook(() => useVisibleRuns({ enabled: true }));
    vi.setSystemTime(NOW + 20_000);
    const fresh = at('n3', new Date(NOW + 10_000).toISOString());
    h.history = { runs: [fresh, ...HISTORY], isLoading: false };
    view.rerender();
    expect(ids(view.result.current.runs)).toEqual(['n2', 'n3']);
  });

  it('cuts a custom range at its end', () => {
    window.localStorage.setItem(RUN_MODE_KEY, 'range');
    window.sessionStorage.setItem(
      CUSTOM_RANGE_KEY,
      JSON.stringify({
        preset: 'custom',
        from: Date.parse('2026-03-02T08:30:00Z'),
        to: Date.parse('2026-03-02T10:30:00Z'),
      })
    );
    const { result } = renderHook(() => useVisibleRuns({ enabled: true }));
    expect(ids(result.current.runs)).toEqual(['n1', 't1']);
  });

  // Its fixed ends would hide the newer runs of every workflow later on: it
  // stays in the tab, and the browser keeps the last preset.
  it('keeps a custom range in the tab, and a preset in the browser', () => {
    const view = renderHook(() => useRunViewSettings());
    const custom = { preset: 'custom' as const, from: 1, to: 2 };
    act(() => view.result.current[1].setRange({ preset: '7d' }));
    act(() => view.result.current[1].setRange(custom));
    expect(view.result.current[0].range).toEqual(custom);
    expect(window.sessionStorage.getItem(CUSTOM_RANGE_KEY)).toBe(
      JSON.stringify(custom)
    );
    expect(window.localStorage.getItem(RUN_RANGE_KEY)).toBe('{"preset":"7d"}');
    act(() => view.result.current[1].setRange({ preset: '1h' }));
    expect(window.sessionStorage.getItem(CUSTOM_RANGE_KEY)).toBeNull();
    expect(view.result.current[0].range).toEqual({ preset: '1h' });
  });

  // A running run lasts until now, not until the page opened.
  it('keeps a running run in a custom range that it reaches now', () => {
    const live = run('live', [], Status.Running, {
      startedAt: '2026-03-02T11:50:00Z',
    });
    h.history = { runs: [live, ...HISTORY], isLoading: false };
    const view = renderHook(() => ({
      visible: useVisibleRuns({ enabled: true }),
      settings: useRunViewSettings(),
    }));
    vi.setSystemTime(NOW + 3_600_000);
    act(() =>
      view.result.current.settings[1].setRange({
        preset: 'custom',
        from: Date.parse('2026-03-02T12:30:00Z'),
        to: Date.parse('2026-03-02T13:00:00Z'),
      })
    );
    expect(ids(view.result.current.visible.runs)).toEqual(['live']);
  });

  it('reads nothing while disabled', () => {
    renderHook(() => useVisibleRuns({ enabled: false }));
    expect(h.args[0]).toMatchObject({ enabled: false });
  });

  // A service DAG offers the presets up to three years; another DAG does
  // not, and the stored one of a service DAG gives way to the default.
  it('keeps a long preset for a service DAG only', () => {
    window.localStorage.setItem(RUN_MODE_KEY, 'range');
    window.localStorage.setItem(RUN_RANGE_KEY, '{"preset":"1y"}');
    const onPage =
      (restartPolicy?: string) =>
      ({ children }: { children: ReactNode }) => (
        <WorkspaceDataContext.Provider
          value={
            {
              dagData: { dag: { name: 'demo', restartPolicy } },
            } as unknown as WorkspaceDataValue
          }
        >
          {children}
        </WorkspaceDataContext.Provider>
      );
    const service = renderHook(() => useVisibleRuns({ enabled: true }), {
      wrapper: onPage('always'),
    });
    expect(service.result.current.settings.range).toEqual({ preset: '1y' });
    const { from, to } = service.result.current.range!;
    expect(to).toBe(NOW);
    expect(to - from).toBe(365 * 86_400_000);

    const other = renderHook(() => useVisibleRuns({ enabled: true }), {
      wrapper: onPage(),
    });
    expect(other.result.current.settings.range).toEqual({ preset: '24h' });
    const span = other.result.current.range!;
    expect(span.to - span.from).toBe(86_400_000);
  });

  // A service DAG has no count mode: a stored count, or a stored mode of an
  // older version, does not apply to it. The stored mode stays for a normal DAG.
  it('uses the range mode for a service DAG whatever is stored', () => {
    window.localStorage.setItem(RUN_MODE_KEY, 'count');
    window.localStorage.setItem(
      'dagu.workspace.runTimelineServiceMode',
      'count'
    );
    const onPage =
      (restartPolicy?: string) =>
      ({ children }: { children: ReactNode }) => (
        <WorkspaceDataContext.Provider
          value={
            {
              dagData: { dag: { name: 'demo', restartPolicy } },
            } as unknown as WorkspaceDataValue
          }
        >
          {children}
        </WorkspaceDataContext.Provider>
      );
    const service = renderHook(() => useVisibleRuns({ enabled: true }), {
      wrapper: onPage('always'),
    });
    expect(service.result.current.settings.mode).toBe('range');
    expect(service.result.current.range).not.toBeNull();

    const other = renderHook(() => useVisibleRuns({ enabled: true }), {
      wrapper: onPage(),
    });
    expect(other.result.current.settings.mode).toBe('count');
    expect(other.result.current.range).toBeNull();
  });
});

describe('useHistoryRuns', () => {
  // The count and the range of the timeline draw its chart; the History tab
  // shows every run that it has read.
  it('ignores the count and the range of the timeline', () => {
    window.localStorage.setItem('dagu.workspace.runTimelineCount', '5');
    h.history = {
      runs: Array.from({ length: 12 }, (_, i) =>
        at(`x${11 - i}`, `2026-03-02T${String(11 - i).padStart(2, '0')}:00:00Z`)
      ),
      isLoading: false,
    };
    h.latestRunId = 'x11';
    expect(renderHook(() => useHistoryRuns()).result.current.runs).toHaveLength(
      12
    );
    window.localStorage.setItem(RUN_MODE_KEY, 'range');
    window.localStorage.setItem(
      RUN_RANGE_KEY,
      JSON.stringify({ preset: '1h' })
    );
    expect(renderHook(() => useHistoryRuns()).result.current.runs).toHaveLength(
      12
    );
  });

  it('adds the older runs read after the history, oldest first', () => {
    const N0 = at('n0', '2026-03-02T08:00:00Z');
    // The head moved: n1 is in both the history and the older runs.
    h.older = { runs: [N1, N0] };
    const { result } = renderHook(() => useHistoryRuns());
    expect(ids(result.current.runs)).toEqual(['n0', 'n1', 't1', 'n2']);
    expect(result.current.selectedId).toBe('n2');
    h.selection = { runId: 'n1' };
    const again = renderHook(() => useHistoryRuns());
    expect(again.result.current.adjacentId('older')).toBe('n0');
  });

  it('skips the test runs that the timeline hides', () => {
    window.localStorage.setItem(SHOW_TESTS_KEY, '0');
    const { result } = renderHook(() => useHistoryRuns());
    expect(ids(result.current.runs)).toEqual(['n1', 'n2']);
  });
});
