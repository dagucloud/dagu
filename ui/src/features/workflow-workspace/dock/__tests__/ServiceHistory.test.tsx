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
  SERVICE_RANGE_KEY,
} from '../../data/useVisibleRuns';
import { RunTimeline } from '../RunTimeline';

// A service DAG always shows its run history, for a time range: it has no
// count of runs. The panel of the history has tests of its own: here it is a
// stub that shows what the dock gives it.
const h = vi.hoisted(() => ({
  select: vi.fn(),
  restartPolicy: undefined as string | undefined,
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
  useQuery: () => ({ data: undefined, mutate: vi.fn() }),
}));
vi.mock('../../continuous/SamplesPanel', () => ({
  SamplesPanel: (props: {
    testId: string;
    source: unknown;
    range: unknown;
  }) => (
    <div data-testid={props.testId}>
      <span data-testid="stub-source">{JSON.stringify(props.source)}</span>
      <span data-testid="stub-range">{JSON.stringify(props.range)}</span>
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
      dag: { name: 'svc', restartPolicy: h.restartPolicy },
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
  });

  // The choice of a service DAG is its own: the other DAGs keep their count
  // of runs and their range.
  it('keeps its range apart from that of the other DAGs', () => {
    window.localStorage.setItem(RUN_MODE_KEY, 'count');
    window.localStorage.setItem(RUN_RANGE_KEY, '{"preset":"6h"}');
    renderDock();
    choose('Last year');
    expect(window.localStorage.getItem(SERVICE_RANGE_KEY)).toBe(
      '{"preset":"1y"}'
    );
    expect(window.localStorage.getItem(RUN_MODE_KEY)).toBe('count');
    expect(window.localStorage.getItem(RUN_RANGE_KEY)).toBe('{"preset":"6h"}');
    cleanup();

    h.restartPolicy = undefined;
    renderDock();
    expect(screen.getByTestId('ww-run-timeline')).toBeInTheDocument();
    expect(runsButton()).not.toHaveAttribute('data-value', 'range:1y');
  });

  // A count that a user chose for a normal DAG does not hide the history.
  it('ignores a stored count mode', () => {
    window.localStorage.setItem(RUN_MODE_KEY, 'count');
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
    window.localStorage.setItem(SERVICE_RANGE_KEY, '{"preset":"30d"}');
    renderDock();
    expect(screen.getByTestId('ww-service-history')).toBeInTheDocument();
    expect(screen.queryByTestId('ww-run-timeline-view')).toBeNull();
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
