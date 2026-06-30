// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from '@testing-library/react';
import * as React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { SearchStateProvider } from '@/contexts/SearchStateContext';
import { useQuery } from '@/hooks/api';
import { WorkspaceKind } from '@/lib/workspace';
import DagsPage from '../index';

const { clientGetMock } = vi.hoisted(() => ({
  clientGetMock: vi.fn(),
}));

vi.mock('@/contexts/UserPreference', () => ({
  useUserPreferences: () => ({
    preferences: {
      pageLimit: 200,
    },
    updatePreference: vi.fn(),
  }),
}));

vi.mock('@/features/dags/components/dag-details', () => ({
  DAGDetailsModal: ({ fileName }: { fileName: string }) => (
    <div role="dialog">Workflow modal for {fileName}</div>
  ),
}));

vi.mock('@/features/dags/components/dag-editor', () => ({
  DAGErrors: () => null,
}));

vi.mock('@/features/dags/components/dag-list', () => ({
  DAGTable: ({
    dags,
    searchText,
    handleSearchTextChange,
    selectedDAG,
    onSelectDAG,
  }: {
    dags: Array<{ fileName: string; dag: { name: string } }>;
    searchText: string;
    handleSearchTextChange: (value: string) => void;
    selectedDAG?: string | null;
    onSelectDAG?: (fileName: string, title: string) => void;
  }) => (
    <div>
      <input
        aria-label="Search DAGs"
        value={searchText}
        onChange={(event) => handleSearchTextChange(event.target.value)}
      />
      <button
        type="button"
        aria-pressed={selectedDAG === 'demo.yaml'}
        onClick={() => onSelectDAG?.('demo.yaml', 'demo')}
      >
        Open demo workflow
      </button>
      <ul>
        {dags.map((dag) => (
          <li key={dag.fileName}>{dag.fileName}</li>
        ))}
      </ul>
    </div>
  ),
}));

vi.mock('@/features/dags/components/dag-list/DAGListHeader', () => ({
  default: () => null,
}));

vi.mock('@/hooks/api', () => ({
  useQuery: vi.fn(),
  useClient: () => ({
    GET: clientGetMock,
  }),
}));

vi.mock('@/hooks/useDAGsListSSE', () => ({
  useDAGsListSSE: () => ({
    isConnected: true,
    shouldUseFallback: false,
  }),
}));

vi.mock('@/hooks/useSSECacheSync', () => ({
  sseFallbackOptions: () => ({}),
  useSSECacheSync: () => undefined,
}));

type QueryCall = {
  path: string;
  init: unknown;
  config: unknown;
};

type DagsPageResponse = {
  dags: Array<{
    fileName: string;
    dag: {
      name: string;
    };
    latestDAGRun: Record<string, unknown>;
  }>;
  errors: string[];
  pagination: {
    totalRecords: number;
    currentPage: number;
    totalPages: number;
    nextPage: number;
    prevPage: number;
  };
};

const useQueryMock = useQuery as unknown as {
  mockImplementation: (
    fn: (path: string, init?: unknown, config?: unknown) => unknown
  ) => void;
};

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

function renderPage(setTitle = vi.fn()) {
  return render(
    <MemoryRouter initialEntries={['/dags']}>
      <ConfigContext.Provider value={makeConfig()}>
        <SearchStateProvider>
          <AppBarContext.Provider
            value={{
              title: '',
              setTitle,
              remoteNodes: ['local', 'remote-a'],
              setRemoteNodes: () => undefined,
              selectedRemoteNode: 'remote-a',
              selectRemoteNode: () => undefined,
              workspaces: [],
              workspaceSelection: { kind: WorkspaceKind.all },
              selectWorkspace: () => undefined,
            }}
          >
            <DagsPage />
          </AppBarContext.Provider>
        </SearchStateProvider>
      </ConfigContext.Provider>
    </MemoryRouter>
  );
}

describe('DagsPage', () => {
  const calls: QueryCall[] = [];
  let dagsPageResponse: DagsPageResponse;

  beforeEach(() => {
    vi.useFakeTimers();
    localStorage.clear();
    sessionStorage.clear();
    calls.length = 0;
    clientGetMock.mockReset();
    dagsPageResponse = {
      dags: [
        {
          fileName: 'demo.yaml',
          dag: {
            name: 'demo',
          },
          latestDAGRun: {},
        },
      ],
      errors: [],
      pagination: {
        totalRecords: 1,
        currentPage: 1,
        totalPages: 1,
        nextPage: 0,
        prevPage: 0,
      },
    };

    useQueryMock.mockImplementation((path, init, config) => {
      calls.push({ path, init, config });

      if (path === '/dags/labels') {
        return {
          data: { labels: [] },
          isLoading: false,
          mutate: vi.fn(),
        };
      }

      if (path === '/dags') {
        const query = (
          init as {
            params?: { query?: { name?: string } };
          }
        )?.params?.query;
        const name = query?.name ?? '';
        const keepPreviousData = Boolean(
          (config as { keepPreviousData?: boolean } | undefined)
            ?.keepPreviousData
        );

        return {
          data: dagsPageResponse,
          isLoading: name.length > 0,
          mutate: vi.fn(),
          ...(name.length > 0 && !keepPreviousData ? { data: undefined } : {}),
        };
      }

      return {
        data: undefined,
        isLoading: false,
        mutate: vi.fn(),
      };
    });
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  it('keeps the search input focused while incremental search refreshes results', async () => {
    renderPage();

    const input = screen.getByRole('textbox', { name: 'Search DAGs' });
    input.focus();
    expect(input).toHaveFocus();

    await act(async () => {
      fireEvent.change(input, { target: { value: 'demo' } });
      vi.advanceTimersByTime(500);
    });

    const latestDagsCall = [...calls]
      .reverse()
      .find((call) => call.path === '/dags');
    expect(latestDagsCall).toBeDefined();
    expect(latestDagsCall?.init).toEqual(
      expect.objectContaining({
        params: {
          query: expect.objectContaining({
            name: 'demo',
          }),
        },
      })
    );

    expect(screen.getByRole('textbox', { name: 'Search DAGs' })).toHaveFocus();
  });

  it('uses the Workflows app bar title', () => {
    const setTitle = vi.fn();

    renderPage(setTitle);

    expect(setTitle).toHaveBeenCalledWith('Workflows');
  });

  it('opens workflow details in the page-level modal when a table row is selected', () => {
    renderPage();

    fireEvent.click(screen.getByRole('button', { name: 'Open demo workflow' }));

    expect(screen.getByRole('dialog')).toHaveTextContent(
      'Workflow modal for demo.yaml'
    );
    expect(
      screen.getByRole('button', { name: 'Open demo workflow' })
    ).toHaveAttribute('aria-pressed', 'true');
  });

  it('loads and appends the next workflow page from the footer control', async () => {
    dagsPageResponse = {
      dags: [
        {
          fileName: 'demo.yaml',
          dag: {
            name: 'demo',
          },
          latestDAGRun: {},
        },
      ],
      errors: [],
      pagination: {
        totalRecords: 2,
        currentPage: 1,
        totalPages: 2,
        nextPage: 2,
        prevPage: 0,
      },
    };
    clientGetMock.mockResolvedValueOnce({
      data: {
        dags: [
          {
            fileName: 'next.yaml',
            dag: {
              name: 'next',
            },
            latestDAGRun: {},
          },
        ],
        errors: [],
        pagination: {
          totalRecords: 2,
          currentPage: 2,
          totalPages: 2,
          nextPage: 0,
          prevPage: 1,
        },
      },
    });

    renderPage();

    expect(screen.getByText('demo.yaml')).toBeVisible();

    await act(async () => {
      fireEvent.click(
        screen.getByRole('button', { name: 'Load more workflows' })
      );
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(screen.getByText('next.yaml')).toBeVisible();
    expect(screen.getByText('demo.yaml')).toBeVisible();
    expect(clientGetMock).toHaveBeenCalledWith(
      '/dags',
      expect.objectContaining({
        params: {
          query: expect.objectContaining({
            remoteNode: 'remote-a',
            page: 2,
            perPage: 200,
          }),
        },
      })
    );
  });
});
