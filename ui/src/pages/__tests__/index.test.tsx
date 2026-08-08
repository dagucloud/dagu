// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import * as React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Status } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { SearchStateProvider } from '@/contexts/SearchStateContext';
import { WorkspaceKind } from '@/lib/workspace';
import { usePaginatedDAGRuns } from '../../features/dag-runs/hooks/dagRunPagination';
import { useClient } from '../../hooks/api';
import DashboardPage from '../index';

vi.mock('../../features/dashboard/components/DashboardTimechart', () => ({
  default: () => <div data-testid="dashboard-timechart" />,
}));

vi.mock('../../features/dag-runs/components/dag-run-details', () => ({
  DAGRunDetailsModal: () => null,
}));

vi.mock('../../features/dags/components/common', () => ({
  CreateDAGModal: () => <button type="button">New workflow</button>,
}));

vi.mock('../../features/dag-runs/hooks/dagRunPagination', () => ({
  usePaginatedDAGRuns: vi.fn(),
}));

vi.mock('../../hooks/api', () => ({
  useClient: vi.fn(),
}));

const useClientMock = vi.mocked(useClient);
const usePaginatedDAGRunsMock = vi.mocked(usePaginatedDAGRuns);

function makeConfig(overrides: Partial<Config> = {}): Config {
  return {
    apiURL: '/api/v1',
    basePath: '/',
    title: 'Dagu',
    navbarColor: '',
    tz: 'UTC',
    tzOffsetInSec: 0,
    version: 'test',
    maxDashboardPageLimit: 100,
    remoteNodes: 'local,remote-a',
    initialWorkspaces: [],
    authMode: 'none',
    setupRequired: false,
    oidcEnabled: false,
    oidcButtonLabel: '',
    proxyEnabled: false,
    proxyButtonLabel: '',
    terminalEnabled: false,
    gitSyncEnabled: false,
    updateAvailable: false,
    latestVersion: '',
    permissions: {
      writeDags: true,
      runDags: true,
    },
    license: {
      valid: true,
      plan: 'community',
      expiry: '',
      features: [],
      gracePeriod: false,
      community: true,
      source: 'test',
      warningCode: '',
    },
    paths: {
      dagsDir: '',
      logDir: '',
      suspendFlagsDir: '',
      adminLogsDir: '',
      baseConfig: '',
      dagRunsDir: '',
      queueDir: '',
      procDir: '',
      serviceRegistryDir: '',
      configFileUsed: '',
      gitSyncDir: '',
      auditLogsDir: '',
    },
    ...overrides,
  };
}

function renderPage({
  selectedWorkspace = '',
}: { selectedWorkspace?: string } = {}) {
  return render(
    <MemoryRouter initialEntries={['/dashboard']}>
      <ConfigContext.Provider value={makeConfig()}>
        <SearchStateProvider>
          <AppBarContext.Provider
            value={{
              title: '',
              setTitle: () => undefined,
              remoteNodes: ['local', 'remote-a'],
              setRemoteNodes: () => undefined,
              selectedRemoteNode: 'remote-a',
              selectRemoteNode: () => undefined,
              workspaces: selectedWorkspace
                ? [{ id: 'workspace-1', name: selectedWorkspace }]
                : [],
              workspaceSelection: selectedWorkspace
                ? {
                    kind: WorkspaceKind.workspace,
                    workspace: selectedWorkspace,
                  }
                : { kind: WorkspaceKind.all },
              selectWorkspace: () => undefined,
            }}
          >
            <DashboardPage />
          </AppBarContext.Provider>
        </SearchStateProvider>
      </ConfigContext.Provider>
    </MemoryRouter>
  );
}

describe('DashboardPage', () => {
  const clientGetMock = vi.fn();

  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    clientGetMock.mockReset();
    useClientMock.mockReturnValue({
      GET: clientGetMock.mockResolvedValue({
        data: {
          dags: [],
          pagination: {
            totalPages: 1,
            totalRecords: 0,
          },
        },
      }),
    } as never);
    usePaginatedDAGRunsMock.mockReturnValue({
      dagRuns: [],
      headPage: undefined,
      error: null,
      isInitialLoading: false,
      isLoadingMore: false,
      loadMoreError: null,
      hasMore: false,
      refresh: vi.fn(),
      loadMore: vi.fn(),
    });
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it('requests dashboard DAG runs without queued status', async () => {
    renderPage();

    await waitFor(() => {
      expect(usePaginatedDAGRunsMock).toHaveBeenCalled();
    });

    const latestCall =
      usePaginatedDAGRunsMock.mock.calls[
        usePaginatedDAGRunsMock.mock.calls.length - 1
      ]?.[0];
    expect(latestCall).toBeDefined();
    if (!latestCall) {
      throw new Error('Expected dashboard to request paginated DAG runs');
    }

    const latestQuery = latestCall.query;
    expect(latestQuery).toBeDefined();
    if (!latestQuery) {
      throw new Error('Expected dashboard DAG run query to be defined');
    }

    expect(latestQuery).toEqual(
      expect.objectContaining({
        remoteNode: 'remote-a',
        status: [
          Status.Success,
          Status.Failed,
          Status.Running,
          Status.Aborted,
          Status.NotStarted,
          Status.PartialSuccess,
          Status.Waiting,
          Status.Rejected,
        ],
      })
    );
    expect(latestQuery.status).not.toContain(Status.Queued);
  });

  it('scopes dashboard DAG and DAG-run requests by selected workspace', async () => {
    renderPage({ selectedWorkspace: 'ops' });

    await waitFor(() => {
      expect(usePaginatedDAGRunsMock).toHaveBeenCalled();
      expect(clientGetMock).toHaveBeenCalled();
    });

    const latestCall =
      usePaginatedDAGRunsMock.mock.calls[
        usePaginatedDAGRunsMock.mock.calls.length - 1
      ]?.[0];
    expect(latestCall?.query).toEqual(
      expect.objectContaining({
        remoteNode: 'remote-a',
        workspace: 'ops',
      })
    );

    expect(clientGetMock).toHaveBeenCalledWith(
      '/dags',
      expect.objectContaining({
        params: {
          query: expect.objectContaining({
            remoteNode: 'remote-a',
            workspace: 'ops',
          }),
        },
      })
    );
  });

  function mockDAGInventory(names: string[], totalRecords: number) {
    clientGetMock.mockResolvedValue({
      data: {
        dags: names.map((name) => ({ fileName: `${name}.yaml`, dag: { name } })),
        pagination: {
          totalPages: 1,
          totalRecords,
        },
      },
    });
  }

  it('invites creating the first workflow when no workflows exist', async () => {
    renderPage();

    expect(
      await screen.findByText('Create your first workflow')
    ).toBeVisible();
    expect(screen.getByText('New workflow')).toBeVisible();
    expect(screen.queryByTestId('dashboard-timechart')).not.toBeInTheDocument();
  });

  it('points at the Workflows page when workflows exist but nothing ran', async () => {
    mockDAGInventory(['etl', 'backup'], 2);

    renderPage();

    expect(await screen.findByText(/No runs on /)).toBeVisible();
    expect(screen.getByText(/Start a workflow from the/)).toBeVisible();
    expect(screen.queryByTestId('dashboard-timechart')).not.toBeInTheDocument();
    expect(
      screen.queryByText('Create your first workflow')
    ).not.toBeInTheDocument();
  });

  it('suggests the example workflows when only seeded examples exist', async () => {
    mockDAGInventory(['example-01-basic-sequential'], 1);

    renderPage();

    expect(
      await screen.findByText(/Run one of the example workflows/)
    ).toBeVisible();
  });

  it('keeps the timechart when runs exist', async () => {
    mockDAGInventory(['etl'], 1);
    usePaginatedDAGRunsMock.mockReturnValue({
      dagRuns: [{ name: 'etl', status: Status.Success } as never],
      headPage: undefined,
      error: null,
      isInitialLoading: false,
      isLoadingMore: false,
      loadMoreError: null,
      hasMore: false,
      refresh: vi.fn(),
      loadMore: vi.fn(),
    });

    renderPage();

    expect(await screen.findByTestId('dashboard-timechart')).toBeVisible();
    expect(screen.queryByText(/No runs on /)).not.toBeInTheDocument();
  });

  it('shows placeholders instead of zeros while runs load', async () => {
    mockDAGInventory(['etl'], 1);
    usePaginatedDAGRunsMock.mockReturnValue({
      dagRuns: [],
      headPage: undefined,
      error: null,
      isInitialLoading: true,
      isLoadingMore: false,
      loadMoreError: null,
      hasMore: false,
      refresh: vi.fn(),
      loadMore: vi.fn(),
    });

    renderPage();

    await waitFor(() => {
      expect(screen.getAllByText('-').length).toBeGreaterThanOrEqual(4);
    });
    expect(screen.queryByText(/No runs on /)).not.toBeInTheDocument();
  });
});
