// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { RestartPolicy } from '@/api/v1/schema';
import {
  WorkspaceDataContext,
  type WorkspaceDataValue,
} from '../../data/context';
import {
  CUSTOM_RANGE_KEY,
  RUN_MODE_KEY,
  RUN_RANGE_KEY,
} from '../../data/useVisibleRuns';
import { RunTimeline } from '../RunTimeline';

// A service DAG always shows its run history, for a time range: it has no
// count of runs. The panel of the history has tests of its own: here it is a
// stub that shows what the dock gives it.
const h = vi.hoisted(() => ({
  select: vi.fn(),
  restartPolicy: undefined as string | undefined,
  histRetentionRuns: undefined as number | undefined,
  retained: undefined as unknown,
  chartWindow: { from: 0, to: 0 },
}));

vi.mock('../../data', () => ({
  useWorkspaceRun: () => ({
    selection: 'latest',
    latestRef: null,
    select: h.select,
  }),
}));
vi.mock('../../data/useWorkspaceRun', () => ({
  useWorkspaceRun: () => ({
    selection: 'latest',
    latestRef: null,
    select: h.select,
  }),
}));
vi.mock('../../data/useRunHistory', () => ({
  useRunHistory: () => ({ runs: [], isLoading: false }),
}));
vi.mock('../../bar/RunStartContext', () => ({
  useRunStart: () => ({ canStart: true, requestStart: vi.fn() }),
}));
vi.mock('@/hooks/api', () => ({
  useClient: () => ({ GET: vi.fn() }),
  useQuery: (_path: string, init: unknown) => ({
    data: init === null ? undefined : h.retained,
    mutate: vi.fn(),
  }),
}));
vi.mock('@/hooks/useDAGHistorySSE', () => ({
  useDAGHistorySSE: () => ({
    data: null,
    error: null,
    isConnected: false,
    isConnecting: false,
    shouldUseFallback: false,
  }),
}));
vi.mock('../../continuous/SamplesPanel', () => ({
  SamplesPanel: (props: {
    testId: string;
    source: unknown;
    range: unknown;
    notice?(window: { from: number; to: number }): unknown;
    links?: {
      has(id: string, time: string): boolean;
      open(id: string): void;
    };
  }) => (
    <div data-testid={props.testId}>
      <span data-testid="stub-source">{JSON.stringify(props.source)}</span>
      <span data-testid="stub-range">{JSON.stringify(props.range)}</span>
      <span data-testid="stub-has-kept">
        {String(props.links?.has('kept', '2026-11-08T00:00:00Z'))}
      </span>
      <span data-testid="stub-has-gone">
        {String(props.links?.has('gone', '2026-01-01T00:00:00Z'))}
      </span>
      <button type="button" onClick={() => props.links?.open('kept')}>
        open
      </button>
      {props.notice?.(h.chartWindow) as never}
    </div>
  ),
}));

const NOW = Date.parse('2026-11-08T12:00:00Z');

const runsButton = () => screen.getByTestId('ww-run-timeline-count');
function items(): string[] {
  fireEvent.keyDown(runsButton(), { key: 'Enter' });
  const labels = screen
    .getAllByRole('menuitemradio')
    .map((item) => (item.textContent ?? '').replace('●', ''));
  fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' });
  return labels;
}
function choose(label: string) {
  fireEvent.keyDown(runsButton(), { key: 'Enter' });
  fireEvent.click(
    screen.getByRole('menuitemradio', { name: new RegExp(`${label}$`) })
  );
}
function renderDock() {
  const workspace = {
    fileName: 'svc.yaml',
    remoteNode: 'local',
    dagData: {
      dag: {
        name: 'svc',
        restartPolicy: h.restartPolicy,
        histRetentionRuns: h.histRetentionRuns,
      },
    },
  } as unknown as WorkspaceDataValue;
  return render(
    <MemoryRouter>
      <WorkspaceDataContext.Provider value={workspace}>
        <RunTimeline />
      </WorkspaceDataContext.Provider>
    </MemoryRouter>
  );
}

beforeEach(() => {
  window.localStorage.clear();
  window.sessionStorage.clear();
  h.restartPolicy = RestartPolicy.always;
  h.histRetentionRuns = undefined;
  h.retained = undefined;
  h.chartWindow = { from: NOW - 7 * 86_400_000, to: NOW };
  h.select.mockReset();
  vi.useFakeTimers({ toFake: ['Date'] });
  vi.setSystemTime(NOW);
});
afterEach(() => {
  vi.useRealTimers();
});

describe('the Activity panel of a service DAG', () => {
  // The history is the only view of a service DAG, over 24 hours at first.
  it('shows the history, and changes its range', () => {
    renderDock();
    expect(screen.getByTestId('ww-service-history-panel')).toBeInTheDocument();
    expect(screen.queryByTestId('ww-run-timeline')).toBeNull();
    expect(screen.getByTestId('stub-range')).toHaveTextContent(
      '{"preset":"24h"}'
    );

    choose('Last 7 days');
    expect(screen.queryByTestId('ww-run-timeline')).toBeNull();
    expect(screen.getByTestId('ww-service-history-panel')).toBeInTheDocument();
    expect(screen.getByTestId('stub-source')).toHaveTextContent(
      '{"kind":"run-history","fileName":"svc.yaml"}'
    );
    expect(screen.getByTestId('stub-range')).toHaveTextContent(
      '{"preset":"7d"}'
    );
    expect(window.localStorage.getItem(RUN_MODE_KEY)).toBe('range');
  });

  // A count that a user chose for a normal DAG, or a stored count mode of a
  // service DAG from an older version, does not hide the history.
  it('ignores a stored count mode', () => {
    window.localStorage.setItem(RUN_MODE_KEY, 'count');
    renderDock();
    expect(screen.getByTestId('ww-service-history-panel')).toBeInTheDocument();
    cleanup();

    window.localStorage.setItem(
      'dagu.workspace.runTimelineServiceMode',
      'count'
    );
    renderDock();
    expect(screen.getByTestId('ww-service-history-panel')).toBeInTheDocument();
    cleanup();

    h.restartPolicy = undefined;
    renderDock();
    expect(screen.getByTestId('ww-run-timeline')).toBeInTheDocument();
    expect(screen.queryByTestId('ww-service-history')).toBeNull();
  });

  it('offers the ranges up to three years and no count of runs', () => {
    renderDock();
    expect(items()).toEqual([
      'Last hour',
      'Last 6 hours',
      'Last 24 hours',
      'Last 7 days',
      'Last 30 days',
      'Last year',
      'Last 3 years',
      'Custom range',
    ]);
  });

  it('gives the history the custom range and a long preset', () => {
    renderDock();
    choose('Last 3 years');
    expect(screen.getByTestId('stub-range')).toHaveTextContent(
      '{"preset":"3y"}'
    );
    choose('Custom range');
    expect(screen.getByTestId('stub-range')).toHaveTextContent('"from"');
    expect(screen.getByTestId('stub-range')).not.toHaveTextContent('preset');
  });

  // The view menu filters the runs of the timeline: the history has none.
  it('offers no view menu while it shows the history', () => {
    window.localStorage.setItem(RUN_MODE_KEY, 'range');
    window.localStorage.setItem(RUN_RANGE_KEY, '{"preset":"30d"}');
    renderDock();
    expect(screen.getByTestId('ww-service-history')).toBeInTheDocument();
    expect(screen.queryByTestId('ww-run-timeline-view')).toBeNull();
  });

  describe('the retention notice', () => {
    const run = (id: string, startedAt: string) => ({
      dagRunId: id,
      startedAt,
    });

    beforeEach(() => {
      window.localStorage.setItem(RUN_MODE_KEY, 'range');
      window.localStorage.setItem(RUN_RANGE_KEY, '{"preset":"7d"}');
    });

    it('says when the window starts before the oldest retained run', () => {
      // Newest first: the oldest retained run is 10 minutes old.
      h.retained = {
        dagRuns: [
          run('kept', '2026-11-08T11:59:00Z'),
          run('old', '2026-11-08T11:50:00Z'),
        ],
      };
      renderDock();
      expect(screen.getByTestId('ww-service-history-notice')).toHaveTextContent(
        /^The runs before .*11:50.* are not kept\.$/
      );
    });

    it('stays silent when the oldest retained run is older than the window', () => {
      h.retained = { dagRuns: [run('old', '2026-10-01T00:00:00Z')] };
      renderDock();
      expect(screen.queryByTestId('ww-service-history-notice')).toBeNull();
    });

    // More runs remain than one page holds: the oldest one is unknown.
    it('stays silent while the oldest retained run is unknown', () => {
      renderDock();
      expect(screen.queryByTestId('ww-service-history-notice')).toBeNull();
      cleanup();
      h.retained = {
        dagRuns: [run('kept', '2026-11-08T11:59:00Z')],
        nextCursor: 'next',
      };
      renderDock();
      expect(screen.getByTestId('ww-service-history')).toBeInTheDocument();
      expect(screen.queryByTestId('ww-service-history-notice')).toBeNull();
    });

    // A DAG that keeps 100 runs has 101 while a run is in flight: the page
    // still holds the oldest run but one, and the notice stays.
    it('keeps the notice while a run in flight adds a page', () => {
      h.histRetentionRuns = 100;
      h.retained = {
        dagRuns: [
          run('kept', '2026-11-08T11:59:00Z'),
          run('old', '2026-11-08T11:50:00Z'),
        ],
        nextCursor: 'next',
      };
      renderDock();
      expect(screen.getByTestId('ww-service-history-notice')).toHaveTextContent(
        /^The runs before .*11:50.* are not kept\.$/
      );
    });

    it('links the runs that are still kept and opens them in the panel', () => {
      h.retained = {
        dagRuns: [
          run('kept', '2026-11-08T11:59:00Z'),
          run('old', '2026-11-08T11:50:00Z'),
        ],
      };
      renderDock();
      expect(screen.getByTestId('stub-has-kept')).toHaveTextContent('true');
      // A run that started before the oldest one is gone.
      expect(screen.getByTestId('stub-has-gone')).toHaveTextContent('false');
      fireEvent.click(screen.getByRole('button', { name: 'open' }));
      expect(h.select).toHaveBeenCalledWith('kept', { replace: true });
    });
  });
});

describe('the Activity panel of another DAG', () => {
  beforeEach(() => {
    h.restartPolicy = undefined;
  });

  it('keeps the four ranges and the timeline of its runs', () => {
    renderDock();
    expect(items()).toEqual([
      'Last 5 runs',
      'Last 10 runs',
      'Last 20 runs',
      'Last 30 runs',
      'Last hour',
      'Last 6 hours',
      'Last 24 hours',
      'Last 7 days',
      'Custom range',
    ]);
    choose('Last 7 days');
    expect(screen.getByTestId('ww-run-timeline')).toBeInTheDocument();
    expect(screen.queryByTestId('ww-service-history')).toBeNull();
  });

  // The range of a service DAG is stored for every DAG: this one cannot show it.
  it('falls back to the default for a range that it does not offer', () => {
    window.localStorage.setItem(RUN_MODE_KEY, 'range');
    window.localStorage.setItem(RUN_RANGE_KEY, '{"preset":"3y"}');
    renderDock();
    expect(runsButton()).toHaveAttribute('data-value', 'range:24h');
    expect(screen.queryByTestId('ww-service-history')).toBeNull();
  });

  it('keeps a custom range', () => {
    window.localStorage.setItem(RUN_MODE_KEY, 'range');
    window.sessionStorage.setItem(
      CUSTOM_RANGE_KEY,
      JSON.stringify({ preset: 'custom', from: 1000, to: 2000 })
    );
    renderDock();
    expect(runsButton()).toHaveAttribute('data-value', 'range:custom');
  });
});
