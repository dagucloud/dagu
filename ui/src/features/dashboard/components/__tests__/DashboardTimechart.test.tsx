// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { components, Status, StatusLabel } from '@/api/v1/schema';
import DashboardTimeChart from '../DashboardTimechart';

type TimelineDataSet = {
  get: () => Array<{
    id: string;
    content: string;
    start: Date;
    end: Date;
  }>;
};

const timelineState = vi.hoisted(() => ({
  dataSet: null as TimelineDataSet | null,
  onClick: null as ((properties: { item: string }) => void) | null,
}));

vi.mock('vis-timeline/standalone', () => ({
  Timeline: class {
    constructor(_element: HTMLElement, dataSet: TimelineDataSet) {
      timelineState.dataSet = dataSet;
    }

    getWindow() {
      return {
        start: new Date('2026-08-01T00:00:00Z'),
        end: new Date('2026-08-02T00:00:00Z'),
      };
    }

    setOptions() {}
    on(event: string, callback: (properties: { item: string }) => void) {
      if (event === 'click') {
        timelineState.onClick = callback;
      }
    }
    off() {}
    destroy() {}
    setWindow() {}
    zoomIn() {}
    zoomOut() {}
    fit() {}
  },
}));

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ tz: 'UTC' }),
}));

vi.mock(
  '@/features/dag-runs/components/dag-run-details/DAGRunDetailsModal',
  () => ({
    default: ({
      dagRunId,
      onNavigate,
    }: {
      dagRunId: string;
      onNavigate?: (direction: 'up' | 'down') => void;
    }) => (
      <div role="dialog">
        {dagRunId}
        <button onClick={() => onNavigate?.('down')}>Next history</button>
        <button onClick={() => onNavigate?.('up')}>Previous history</button>
      </div>
    ),
  })
);

type DAGRunSummary = components['schemas']['DAGRunSummary'];

beforeEach(() => {
  timelineState.dataSet = null;
  timelineState.onClick = null;
});

afterEach(() => {
  vi.useRealTimers();
});

describe('DashboardTimeChart', () => {
  it('navigates the displayed timeline histories without wrapping', () => {
    const runs = ['run-1', 'run-2'].map((dagRunId) => ({
      name: 'example',
      dagRunId,
      status: Status.Success,
      statusLabel: StatusLabel.succeeded,
      startedAt: '2026-08-01T01:00:00Z',
      finishedAt: '2026-08-01T01:01:00Z',
      artifactsAvailable: false,
      autoRetryCount: 0,
    }));
    render(<DashboardTimeChart data={runs} />);
    act(() => timelineState.onClick?.({ item: 'example_run-1' }));
    expect(screen.getByRole('dialog')).toHaveTextContent('run-1');
    fireEvent.click(screen.getByRole('button', { name: 'Next history' }));
    expect(screen.getByRole('dialog')).toHaveTextContent('run-2');
    fireEvent.click(screen.getByRole('button', { name: 'Next history' }));
    expect(screen.getByRole('dialog')).toHaveTextContent('run-2');
    fireEvent.click(screen.getByRole('button', { name: 'Previous history' }));
    expect(screen.getByRole('dialog')).toHaveTextContent('run-1');
  });

  it('renders a running DAG run without a finished timestamp', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-01T02:00:00Z'));

    const runningDAGRun: DAGRunSummary = {
      dagRunId: 'run-1',
      name: 'long-running-dag',
      status: Status.Running,
      statusLabel: StatusLabel.running,
      startedAt: '2026-08-01T01:00:00Z',
      finishedAt: '',
      artifactsAvailable: false,
      autoRetryCount: 0,
    };

    render(
      <DashboardTimeChart
        data={[runningDAGRun]}
        selectedDate={{
          startTimestamp: 1785542400,
          endTimestamp: 1785628800,
        }}
      />
    );

    const items = timelineState.dataSet?.get() ?? [];
    expect(items).toHaveLength(1);
    expect(items[0]).toEqual(
      expect.objectContaining({
        id: 'long-running-dag_run-1',
        content: 'long-running-dag',
      })
    );
    expect(items[0]?.end.getTime()).toBe(
      new Date('2026-08-01T02:00:00Z').getTime()
    );
  });
});
