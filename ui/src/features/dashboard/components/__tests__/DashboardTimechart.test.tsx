// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
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
    on() {}
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
  () => ({ default: () => null })
);

type DAGRunSummary = components['schemas']['DAGRunSummary'];

beforeEach(() => {
  timelineState.dataSet = null;
});

describe('DashboardTimeChart', () => {
  it('renders a running DAG run without a finished timestamp', () => {
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
    expect(items[0]?.end.getTime()).not.toBeNaN();
  });
});
