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
import { MemoryRouter, useLocation } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { SearchStateProvider } from '@/contexts/SearchStateContext';
import {
  ViewSortField,
  ViewSortOrder,
  ViewSpecType,
  ViewWorkspaceScope,
} from '@/api/v1/schema';
import { useQuery } from '@/hooks/api';
import type { View, ViewSpec } from '@/hooks/useViews';
import { WorkspaceKind, workspaceSelectionKey } from '@/lib/workspace';
import DagsPage from '../index';

const {
  clientGetMock,
  createViewMock,
  deleteViewMock,
  sharedWorkflowViewState,
  updateViewMock,
  userPreferences,
} = vi.hoisted(() => ({
  clientGetMock: vi.fn(),
  createViewMock: vi.fn(),
  deleteViewMock: vi.fn(),
  sharedWorkflowViewState: { views: [] as View[] },
  updateViewMock: vi.fn(),
  userPreferences: {
    pageLimit: 200,
  },
}));

vi.mock('@/contexts/UserPreference', () => ({
  useUserPreferences: () => ({
    preferences: userPreferences,
    updatePreference: vi.fn(),
  }),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteForWorkspace: () => true,
}));

vi.mock('@/hooks/useViews', () => ({
  useViews: () => ({
    views: sharedWorkflowViewState.views,
    isLoading: false,
    error: undefined,
    createView: createViewMock,
    updateView: updateViewMock,
    deleteView: deleteViewMock,
    refresh: vi.fn(),
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
    activeWorkflowViewId,
    onSaveWorkflowView,
    onShowAllWorkflows,
    onUpdateWorkflowView,
    onResetWorkflowView,
    onSetDefaultWorkflowView,
    onDeleteWorkflowView,
  }: {
    dags: Array<{ fileName: string; dag: { name: string } }>;
    searchText: string;
    handleSearchTextChange: (value: string) => void;
    selectedDAG?: string | null;
    onSelectDAG?: (fileName: string, title: string) => void;
    activeWorkflowViewId: string | null;
    onSaveWorkflowView: (name: string, makeDefault: boolean) => Promise<void>;
    onShowAllWorkflows: () => void;
    onUpdateWorkflowView: () => Promise<void>;
    onResetWorkflowView: () => void;
    onSetDefaultWorkflowView: (viewId: string | undefined) => Promise<void>;
    onDeleteWorkflowView: (viewId: string) => Promise<void>;
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
      <span data-testid="active-workflow-view">
        {activeWorkflowViewId ?? 'none'}
      </span>
      <button
        type="button"
        onClick={() => void onSaveWorkflowView('Production operations', true)}
      >
        Save production view
      </button>
      <button type="button" onClick={onShowAllWorkflows}>
        Show all workflows
      </button>
      <button type="button" onClick={() => void onUpdateWorkflowView()}>
        Update workflow view
      </button>
      <button type="button" onClick={onResetWorkflowView}>
        Reset workflow view
      </button>
      <button
        type="button"
        onClick={() => void onSetDefaultWorkflowView('production')}
      >
        Set production view as default
      </button>
      <button
        type="button"
        onClick={() => void onDeleteWorkflowView('production')}
      >
        Delete production view
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

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location-search">{location.search}</output>;
}

function renderPage(setTitle = vi.fn(), initialEntry = '/dags') {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <LocationProbe />
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

function workflowViewScopeKey() {
  return JSON.stringify({
    remoteNode: 'remote-a',
    workspace: workspaceSelectionKey({ kind: WorkspaceKind.all }),
  });
}

function makeWorkflowView(overrides: Partial<View> = {}): View {
  return {
    id: 'production',
    name: 'Production operations',
    type: ViewSpecType.workflow,
    workspace: '',
    workspaceScope: ViewWorkspaceScope.all,
    labels: ['env=prod'],
    dagName: 'deploy',
    intervalDays: 1,
    sortField: ViewSortField.name,
    sortOrder: ViewSortOrder.asc,
    isDefault: false,
    createdAt: '2026-08-05T00:00:00Z',
    updatedAt: '2026-08-05T00:00:00Z',
    ...overrides,
  };
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
    sharedWorkflowViewState.views = [];
    createViewMock.mockReset();
    updateViewMock.mockReset();
    deleteViewMock.mockReset();
    createViewMock.mockImplementation(async (spec: ViewSpec) => {
      const created = makeWorkflowView({
        id: 'created-view',
        name: spec.name,
        workspace: spec.workspace,
        workspaceScope: spec.workspaceScope,
        labels: spec.labels,
        dagName: spec.dagName,
        sortField: spec.sortField,
        sortOrder: spec.sortOrder,
        isDefault: spec.isDefault,
      });
      sharedWorkflowViewState.views = [
        ...sharedWorkflowViewState.views,
        created,
      ];
      return created;
    });
    updateViewMock.mockImplementation(async (id: string, spec: ViewSpec) => {
      const index = sharedWorkflowViewState.views.findIndex(
        (view) => view.id === id
      );
      const updated = makeWorkflowView({
        ...(index >= 0 ? sharedWorkflowViewState.views[index] : {}),
        id,
        name: spec.name,
        workspace: spec.workspace,
        workspaceScope: spec.workspaceScope,
        labels: spec.labels,
        dagName: spec.dagName,
        sortField: spec.sortField,
        sortOrder: spec.sortOrder,
        isDefault: spec.isDefault,
      });
      if (index >= 0) {
        sharedWorkflowViewState.views = sharedWorkflowViewState.views.map(
          (view) => (view.id === id ? updated : view)
        );
      }
      return updated;
    });
    deleteViewMock.mockImplementation(async (id: string) => {
      sharedWorkflowViewState.views = sharedWorkflowViewState.views.filter(
        (view) => view.id !== id
      );
    });
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

  it('opens the default workflow view when the URL has no filters', () => {
    sharedWorkflowViewState.views.push(makeWorkflowView({ isDefault: true }));
    sharedWorkflowViewState.views.push(
      makeWorkflowView({
        id: 'default-workspace',
        dagName: 'wrong-scope',
        workspaceScope: ViewWorkspaceScope.default,
        isDefault: true,
      })
    );

    renderPage();

    expect(screen.getByRole('textbox', { name: 'Search DAGs' })).toHaveValue(
      'deploy'
    );
    expect(screen.getByTestId('active-workflow-view')).toHaveTextContent(
      'production'
    );
  });

  it('gives explicit URL filters precedence over the default view', () => {
    sharedWorkflowViewState.views.push(makeWorkflowView({ isDefault: true }));

    renderPage(
      vi.fn(),
      '/dags?search=adhoc&labels=env%3Dstage&sort=name&order=desc'
    );

    expect(screen.getByRole('textbox', { name: 'Search DAGs' })).toHaveValue(
      'adhoc'
    );
    expect(screen.getByTestId('active-workflow-view')).toHaveTextContent(
      'none'
    );
  });

  it('restores the existing session filters when no default view is set', () => {
    sessionStorage.setItem(
      'dagu.searchState',
      JSON.stringify({
        [`dagDefinitions:${workflowViewScopeKey()}`]: {
          searchText: 'remembered',
          searchLabels: ['team=ops'],
          sortField: 'name',
          sortOrder: 'desc',
        },
      })
    );

    renderPage();

    expect(screen.getByRole('textbox', { name: 'Search DAGs' })).toHaveValue(
      'remembered'
    );
    expect(screen.getByTestId('active-workflow-view')).toHaveTextContent(
      'none'
    );
  });

  it('lets users leave the default view and show all workflows', () => {
    sharedWorkflowViewState.views.push(makeWorkflowView({ isDefault: true }));
    renderPage();

    fireEvent.click(screen.getByRole('button', { name: 'Show all workflows' }));

    expect(screen.getByRole('textbox', { name: 'Search DAGs' })).toHaveValue(
      ''
    );
    expect(screen.getByTestId('active-workflow-view')).toHaveTextContent(
      'none'
    );
  });

  it('saves the current filters as a shared default view', async () => {
    renderPage();

    fireEvent.change(screen.getByRole('textbox', { name: 'Search DAGs' }), {
      target: { value: 'deploy' },
    });
    await act(async () => {
      fireEvent.click(
        screen.getByRole('button', { name: 'Save production view' })
      );
    });

    expect(createViewMock).toHaveBeenCalledWith({
      name: 'Production operations',
      type: ViewSpecType.workflow,
      workspace: '',
      workspaceScope: ViewWorkspaceScope.all,
      labels: [],
      dagName: 'deploy',
      intervalDays: 1,
      pinned: false,
      sortField: ViewSortField.name,
      sortOrder: ViewSortOrder.asc,
      isDefault: true,
    });
    expect(screen.getByTestId('active-workflow-view')).toHaveTextContent(
      'created-view'
    );
  });

  it('updates and resets an active workflow view', async () => {
    sharedWorkflowViewState.views.push(makeWorkflowView());
    renderPage(
      vi.fn(),
      '/dags?view=production&search=changed&labels=env%3Dprod&sort=name&order=desc'
    );

    fireEvent.click(
      screen.getByRole('button', { name: 'Reset workflow view' })
    );
    expect(screen.getByRole('textbox', { name: 'Search DAGs' })).toHaveValue(
      'deploy'
    );
    expect(screen.getByTestId('active-workflow-view')).toHaveTextContent(
      'production'
    );
    expect(screen.getByTestId('location-search')).toHaveTextContent(
      '?view=production&search=deploy&labels=env%3Dprod&sort=name&order=asc'
    );

    fireEvent.change(screen.getByRole('textbox', { name: 'Search DAGs' }), {
      target: { value: 'changed' },
    });
    await act(async () => {
      fireEvent.click(
        screen.getByRole('button', { name: 'Update workflow view' })
      );
    });

    expect(updateViewMock).toHaveBeenCalledWith(
      'production',
      expect.objectContaining({
        dagName: 'changed',
        labels: ['env=prod'],
        sortField: ViewSortField.name,
        sortOrder: ViewSortOrder.asc,
        isDefault: false,
      })
    );
  });

  it('omits empty workflow filters from saved-view URLs', () => {
    sharedWorkflowViewState.views.push(
      makeWorkflowView({ dagName: '', labels: [] })
    );
    renderPage(
      vi.fn(),
      '/dags?view=production&search=temporary&sort=name&order=asc'
    );

    fireEvent.click(
      screen.getByRole('button', { name: 'Reset workflow view' })
    );

    expect(screen.getByTestId('location-search')).toHaveTextContent(
      '?view=production&sort=name&order=asc'
    );
  });

  it('keeps a saved workflow filter cleared when its URL parameter is omitted', () => {
    sharedWorkflowViewState.views.push(makeWorkflowView({ labels: [] }));
    renderPage(vi.fn(), '/dags?view=production');

    fireEvent.change(screen.getByRole('textbox', { name: 'Search DAGs' }), {
      target: { value: '' },
    });

    expect(screen.getByRole('textbox', { name: 'Search DAGs' })).toHaveValue(
      ''
    );
    expect(screen.getByTestId('location-search')).toHaveTextContent(
      '?view=production&sort=name&order=asc'
    );
  });

  it('sets a shared workflow view as the default', async () => {
    sharedWorkflowViewState.views.push(makeWorkflowView({ labels: [] }));
    renderPage(vi.fn(), '/dags?view=production');

    await act(async () => {
      fireEvent.click(
        screen.getByRole('button', {
          name: 'Set production view as default',
        })
      );
    });

    expect(updateViewMock).toHaveBeenCalledWith(
      'production',
      expect.objectContaining({ isDefault: true })
    );
  });

  it('deletes the active shared workflow view and shows all workflows', async () => {
    sharedWorkflowViewState.views.push(makeWorkflowView({ isDefault: true }));
    renderPage();

    await act(async () => {
      fireEvent.click(
        screen.getByRole('button', { name: 'Delete production view' })
      );
    });

    expect(deleteViewMock).toHaveBeenCalledWith('production');
    expect(screen.getByRole('textbox', { name: 'Search DAGs' })).toHaveValue(
      ''
    );
    expect(screen.getByTestId('active-workflow-view')).toHaveTextContent(
      'none'
    );
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
