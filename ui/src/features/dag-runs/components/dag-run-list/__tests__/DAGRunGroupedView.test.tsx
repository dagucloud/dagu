// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Status, StatusLabel, TriggerType } from '@/api/v1/schema';
import DAGRunGroupedView from '../DAGRunGroupedView';

vi.mock('../StepDetailsTooltip', () => ({
  StepDetailsTooltip: ({ children }: { children: React.ReactNode }) => (
    <>{children}</>
  ),
}));

afterEach(() => {
  cleanup();
});

describe('DAGRunGroupedView', () => {
  it('shows a loading row instead of the empty state while the first page loads', () => {
    render(<DAGRunGroupedView dagRuns={[]} isLoading />);

    expect(screen.getByText('Loading DAG runs...')).toBeInTheDocument();
    expect(screen.queryByText('No DAG runs found')).not.toBeInTheDocument();
  });

  it('sorts grouped runs by schedule time before queued time', () => {
    render(
      <DAGRunGroupedView
        dagRuns={[
          {
            dagRunId: 'run-scheduled-later',
            name: 'scheduled-dag',
            status: Status.Queued,
            statusLabel: StatusLabel.queued,
            artifactsAvailable: false,
            autoRetryCount: 0,
            autoRetryLimit: 0,
            triggerType: TriggerType.scheduler,
            queuedAt: '2026-03-13T11:00:00Z',
            scheduleTime: '2026-03-13T10:00:00Z',
            startedAt: '',
            finishedAt: '',
          },
          {
            dagRunId: 'run-queued-later',
            name: 'scheduled-dag',
            status: Status.Queued,
            statusLabel: StatusLabel.queued,
            artifactsAvailable: false,
            autoRetryCount: 0,
            autoRetryLimit: 0,
            triggerType: TriggerType.scheduler,
            queuedAt: '2026-03-13T12:00:00Z',
            scheduleTime: '2026-03-13T09:00:00Z',
            startedAt: '',
            finishedAt: '',
          },
        ]}
      />
    );

    fireEvent.click(screen.getByText('scheduled-dag'));

    const runIds = screen
      .getAllByText(/^run-/)
      .map((element) => element.textContent);
    expect(runIds).toEqual(['run-scheduled-later', 'run-queued-later']);
  });

  it('toggles grouped bulk selection without opening the details panel', () => {
    const onSelectDAGRun = vi.fn();
    const onToggleBulkSelect = vi.fn();

    render(
      <DAGRunGroupedView
        dagRuns={[
          {
            dagRunId: 'run-1',
            name: 'grouped-dag',
            status: Status.Failed,
            statusLabel: StatusLabel.failed,
            artifactsAvailable: false,
            autoRetryCount: 0,
            autoRetryLimit: 0,
            triggerType: TriggerType.manual,
            queuedAt: '2026-03-13T10:00:00Z',
            startedAt: '2026-03-13T10:01:00Z',
            finishedAt: '2026-03-13T10:02:00Z',
          },
        ]}
        onSelectDAGRun={onSelectDAGRun}
        onToggleBulkSelect={onToggleBulkSelect}
      />
    );

    fireEvent.click(screen.getByText('grouped-dag'));
    fireEvent.click(
      screen.getByRole('checkbox', { name: 'Select DAG run grouped-dag run-1' })
    );

    expect(onToggleBulkSelect).toHaveBeenCalledWith({
      name: 'grouped-dag',
      dagRunId: 'run-1',
    });
    expect(onSelectDAGRun).not.toHaveBeenCalled();
  });

  it('opens artifacts from an expanded run without opening its status view', () => {
    const onSelectDAGRun = vi.fn();
    const onViewArtifacts = vi.fn();

    render(
      <DAGRunGroupedView
        dagRuns={[
          {
            dagRunId: 'run-1',
            name: 'artifact-dag',
            status: Status.Success,
            statusLabel: StatusLabel.succeeded,
            artifactsAvailable: true,
            autoRetryCount: 0,
            triggerType: TriggerType.manual,
            queuedAt: '2026-03-13T10:00:00Z',
            startedAt: '2026-03-13T10:01:00Z',
            finishedAt: '2026-03-13T10:02:00Z',
          },
        ]}
        onSelectDAGRun={onSelectDAGRun}
        onViewArtifacts={onViewArtifacts}
      />
    );

    fireEvent.click(screen.getByText('artifact-dag'));
    fireEvent.click(
      screen.getByRole('button', {
        name: 'View artifacts for artifact-dag run-1',
      })
    );

    expect(onViewArtifacts).toHaveBeenCalledWith(
      expect.objectContaining({ name: 'artifact-dag', dagRunId: 'run-1' })
    );
    expect(onSelectDAGRun).not.toHaveBeenCalled();
  });
});
