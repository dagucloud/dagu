// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Status, StatusLabel, TriggerType } from '@/api/v1/schema';
import { PeekContext } from '@/features/peek/PeekContext';
import DAGRunGroupedView from '../DAGRunGroupedView';

vi.mock('../StepDetailsTooltip', () => ({
  StepDetailsTooltip: ({ children }: { children: React.ReactNode }) => (
    <>{children}</>
  ),
}));

afterEach(() => {
  cleanup();
});

function LocationProbe() {
  const location = useLocation();
  return (
    <div data-testid="location">{`${location.pathname}${location.search}`}</div>
  );
}

function renderView(view: React.ReactElement) {
  return render(
    <MemoryRouter initialEntries={['/dag-runs']}>
      {view}
      <LocationProbe />
    </MemoryRouter>
  );
}

describe('DAGRunGroupedView', () => {
  it('shows a loading row instead of the empty state while the first page loads', () => {
    renderView(<DAGRunGroupedView dagRuns={[]} isLoading />);

    expect(screen.getByText('Loading runs...')).toBeInTheDocument();
    expect(screen.queryByText('No runs found')).not.toBeInTheDocument();
  });

  it('sorts grouped runs by schedule time before queued time', () => {
    renderView(
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

  it('toggles grouped bulk selection without opening the run', () => {
    const onToggleBulkSelect = vi.fn();

    renderView(
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
    expect(screen.getByTestId('location')).toHaveTextContent('/dag-runs');
  });

  it('opens an expanded run on its workflow page', () => {
    renderView(
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
      />
    );

    fireEvent.click(screen.getByText('grouped-dag'));
    const runLink = screen.getByRole('link', { name: 'run-1' });
    expect(runLink).toHaveAttribute(
      'href',
      '/dags/grouped-dag?remoteNode=local&run=run-1'
    );
    fireEvent.click(runLink.closest('div[class*="cursor-pointer"]') ?? runLink);

    expect(screen.getByTestId('location')).toHaveTextContent(
      '/dags/grouped-dag?remoteNode=local&run=run-1'
    );
  });

  it('opens artifacts of an expanded run on the Artifacts tab', () => {
    renderView(
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
      />
    );

    fireEvent.click(screen.getByText('artifact-dag'));
    fireEvent.click(
      screen.getByRole('link', {
        name: 'View artifacts for artifact-dag run-1',
      })
    );

    expect(screen.getByTestId('location')).toHaveTextContent(
      '/dags/artifact-dag?remoteNode=local&run=run-1&dock=artifacts'
    );
  });

  // One group of one run, expanded, under a peek host.
  const renderExpandedUnderPeek = () => {
    const peek = { open: vi.fn(), isOpen: false };
    renderView(
      <PeekContext.Provider value={peek}>
        <DAGRunGroupedView
          dagRuns={[
            {
              dagRunId: 'run-1',
              name: 'grouped-dag',
              status: Status.Failed,
              statusLabel: StatusLabel.failed,
              artifactsAvailable: false,
              autoRetryCount: 0,
              triggerType: TriggerType.manual,
              queuedAt: '2026-03-13T10:00:00Z',
              startedAt: '2026-03-13T10:01:00Z',
              finishedAt: '2026-03-13T10:02:00Z',
            },
          ]}
        />
      </PeekContext.Provider>
    );
    fireEvent.click(screen.getByText('grouped-dag'));
    const runLink = screen.getByRole('link', { name: 'run-1' });
    const row = runLink.closest<HTMLElement>('div[class*="cursor-pointer"]')!;
    return { peek, runLink, row };
  };

  it('shows the peek of an expanded run under a peek host', () => {
    const { peek, row } = renderExpandedUnderPeek();

    fireEvent.click(row);

    expect(peek.open).toHaveBeenCalledWith({
      dag: 'grouped-dag',
      run: 'run-1',
    });
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/dag-runs$/);
  });

  // Enter on the focused row shows its peek; the run ID link keeps its own
  // Enter, and a plain click on it shows the same peek.
  it('shows the peek of a focused run row on Enter', () => {
    const { peek, runLink, row } = renderExpandedUnderPeek();

    row.focus();
    expect(row).toHaveFocus();
    fireEvent.keyDown(row, { key: 'Enter' });
    fireEvent.keyDown(runLink, { key: 'Enter' });
    expect(peek.open).toHaveBeenCalledTimes(1);
    expect(peek.open).toHaveBeenCalledWith({
      dag: 'grouped-dag',
      run: 'run-1',
    });

    fireEvent.click(runLink);
    expect(peek.open).toHaveBeenCalledTimes(2);
    expect(peek.open).toHaveBeenLastCalledWith({
      dag: 'grouped-dag',
      run: 'run-1',
    });
    expect(runLink).toHaveAttribute(
      'href',
      '/dags/grouped-dag?remoteNode=local&run=run-1'
    );
  });
});
