// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import { WorkspaceKind } from '@/lib/workspace';
import DAGDetails from '..';

vi.mock('@/features/dags/components/dag-details', () => ({
  DAGHeader: () => <div>Header</div>,
  DAGDetailsContent: vi.fn(
    ({ activeTab, fillHeight, currentDAGRun, dagRunId }) => (
      <div
        data-active-tab={activeTab}
        data-fill-height={String(fillHeight)}
        data-testid="dag-details-content"
      >
        {currentDAGRun
          ? `Output for ${currentDAGRun.dagRunId}`
          : `Loading ${dagRunId}`}
      </div>
    )
  ),
}));

vi.mock('@/hooks/api', () => ({
  useQuery: vi.fn(),
}));

vi.mock('@/components/MonitoringOffer', () => ({
  MonitoringOffer: () => <p>Monitoring offer</p>,
}));

vi.mock('@/hooks/useDAGSSE', () => ({
  useDAGSSE: vi.fn(() => ({})),
}));

vi.mock('@/hooks/useDAGRunSSE', () => ({
  useDAGRunSSE: vi.fn(() => ({})),
}));

vi.mock('@/hooks/useSubDAGRunSSE', () => ({
  useSubDAGRunSSE: vi.fn(() => ({})),
}));

vi.mock('@/hooks/useSSECacheSync', () => ({
  sseFallbackOptions: vi.fn(() => ({})),
  useSSECacheSync: vi.fn(),
}));

const useQueryMock = useQuery as unknown as {
  mockImplementation: (fn: (path: string) => unknown) => void;
};

const appBarValue = {
  selectedRemoteNode: 'local',
  setTitle: vi.fn(),
  workspaceSelection: { kind: WorkspaceKind.all },
} as never;

const dagData = {
  dag: {
    name: 'release-notes',
    artifacts: { enabled: true },
  },
  latestDAGRun: {
    name: 'release-notes',
    dagRunId: 'run-1',
    artifactsAvailable: true,
  },
  localDags: [],
};

function LocationProbe() {
  const location = useLocation();
  return (
    <span data-testid="location">{location.pathname + location.search}</span>
  );
}

function renderPage(initialEntry = '/dags/release-notes') {
  render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <AppBarContext.Provider value={appBarValue}>
        <LocationProbe />
        <Routes>
          <Route path="/dags/:fileName" element={<DAGDetails />} />
          <Route path="/dags/:fileName/:tab" element={<DAGDetails />} />
          <Route path="*" element={null} />
        </Routes>
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

describe('DAGDetails page', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useQueryMock.mockImplementation((path) => {
      if (path === '/dags/{fileName}') {
        return {
          data: dagData,
          mutate: vi.fn(),
        } as never;
      }

      return {
        data: undefined,
        mutate: vi.fn(),
      } as never;
    });
  });

  it('uses full-height layout for DAG details content', () => {
    renderPage();

    const content = screen.getByTestId('dag-details-content');
    expect(content).toHaveAttribute('data-fill-height', 'true');
    expect(content.parentElement).toHaveClass('min-h-0');
    expect(content.parentElement).toHaveClass('flex-1');
  });

  it('falls back to the latest run when a pinned run is missing', () => {
    renderPage(
      '/dags/release-notes/log?dagRunId=missing&dagRunName=release-notes&step=build'
    );

    expect(screen.getByText('Output for run-1')).toBeVisible();
  });

  it('explains when the workflow does not exist', () => {
    useQueryMock.mockImplementation(
      () =>
        ({
          data: undefined,
          error: { code: 'not_found', message: 'DAG is not found' },
          mutate: vi.fn(),
        }) as never
    );

    renderPage('/dags/missing-flow');

    expect(
      screen.getByRole('heading', { name: 'Workflow not found' })
    ).toBeVisible();
    expect(screen.getByText('missing-flow')).toBeVisible();
    expect(
      screen.getByRole('link', { name: 'Back to Workflows' })
    ).toHaveAttribute('href', '/dags');
    expect(screen.queryByTestId('dag-details-content')).toBeNull();
  });

  it('reports a failed load with the server message and a retry', () => {
    const mutate = vi.fn();
    useQueryMock.mockImplementation(
      () =>
        ({
          data: undefined,
          error: { code: 'internal_error', message: 'disk is full' },
          mutate,
        }) as never
    );

    renderPage();

    expect(screen.getByRole('alert')).toHaveTextContent(
      "Couldn't load this workflow"
    );
    expect(screen.getByText('disk is full')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(mutate).toHaveBeenCalled();
  });

  // Runs are selected on the run page; older links that name a run on the
  // workflow page land there instead.
  it('sends a run named on the workflow page to the run page', async () => {
    renderPage(
      '/dags/release-notes?dagRunId=run-7&subDAGRunId=child-2&dagRunName=release-notes&step=deploy'
    );

    await waitFor(() => {
      expect(screen.getByTestId('location')).toHaveTextContent(
        '/dag-runs/release-notes/run-7?remoteNode=local&subDAGRunId=child-2&dagRunId=run-7&dagRunName=release-notes&step=deploy'
      );
    });
  });

  // A schedule runs unattended, which is when an alert matters.
  describe('monitoring offer', () => {
    function mockScheduled(suspended: boolean) {
      useQueryMock.mockImplementation(
        (path) =>
          ({
            data:
              path === '/dags/{fileName}'
                ? {
                    ...dagData,
                    dag: {
                      ...dagData.dag,
                      schedule: [{ expression: '0 9 * * *' }],
                    },
                    suspended,
                  }
                : undefined,
            mutate: vi.fn(),
          }) as never
      );
    }

    it('appears on the Overview of a scheduled workflow', () => {
      mockScheduled(false);

      renderPage();

      expect(screen.getByText('Monitoring offer')).toBeVisible();
    });

    it.each([
      ['a suspended schedule', '/dags/release-notes', true],
      ['another tab', '/dags/release-notes/spec', false],
    ])('stays out of %s', (_, path, suspended) => {
      mockScheduled(suspended);

      renderPage(path);

      expect(screen.queryByText('Monitoring offer')).not.toBeInTheDocument();
    });
  });

  it('redirects the legacy docs tab to the Wiki tab', async () => {
    renderPage('/dags/release-notes/docs?remoteNode=edge');

    expect(screen.getByTestId('dag-details-content')).toHaveAttribute(
      'data-active-tab',
      'wiki'
    );
    await waitFor(() => {
      expect(screen.getByTestId('location')).toHaveTextContent(
        '/dags/release-notes/wiki?remoteNode=edge'
      );
    });
  });

  // The old History tab maps to the Overview. Its redirect keeps the run as
  // ?run=, and the redirect to the run page, which is for a run named on the
  // Overview, must not win over it.
  it('sends the old History link to the Overview with its run', async () => {
    renderPage('/dags/release-notes/history?dagRunId=run-7&remoteNode=edge');

    await waitFor(() => {
      expect(screen.getByTestId('location')).toHaveTextContent(
        '/dags/release-notes?run=run-7&remoteNode=edge'
      );
    });
  });
});
