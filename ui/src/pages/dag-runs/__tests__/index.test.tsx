// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
import dayjs from 'dayjs';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  RunDateMode,
  RunDatePreset,
  RunSpecificPeriod,
  ViewSpecType,
  ViewWorkspaceScope,
} from '@/api/v1/schema';
import type { View } from '@/hooks/useViews';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { WorkspaceKind, type WorkspaceSelection } from '@/lib/workspace';
import DAGRuns from '..';

const {
  createRunViewMock,
  dagsListState,
  deleteRunViewMock,
  readSearchStateMock,
  searchStateMock,
  sharedRunViewState,
  updateRunViewMock,
  viewsLoadingState,
  writeSearchStateMock,
} = vi.hoisted(() => {
  const readState = vi.fn((): unknown => null);
  const writeState = vi.fn();
  return {
    createRunViewMock: vi.fn(),
    dagsListState: {
      dags: [] as { fileName: string; dag: { name: string } }[],
      get: vi.fn(),
    },
    deleteRunViewMock: vi.fn(),
    updateRunViewMock: vi.fn(),
    readSearchStateMock: readState,
    searchStateMock: { readState, writeState },
    sharedRunViewState: { views: [] as View[] },
    viewsLoadingState: { current: false },
    writeSearchStateMock: writeState,
  };
});

vi.mock('@/contexts/SearchStateContext', () => ({
  useSearchState: () => searchStateMock,
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanWrite: () => true,
  useCanWriteForWorkspace: () => true,
}));

// The page reads its views through useSharedViews, which calls useViews once
// for the workflow type and once for the run type and merges both lists. Like
// the real hook, the mock keeps the same array while the views do not change.
const viewsOfTypeCache = vi.hoisted(() => new Map<string, unknown[]>());

function viewsOfType(type: ViewSpecType): View[] {
  const next = sharedRunViewState.views.filter((view) => view.type === type);
  const cached = viewsOfTypeCache.get(type) as View[] | undefined;
  if (
    cached &&
    cached.length === next.length &&
    cached.every((view, i) => view === next[i])
  ) {
    return cached;
  }
  viewsOfTypeCache.set(type, next);
  return next;
}

vi.mock('@/hooks/useViews', () => ({
  useViews: (type: ViewSpecType) => ({
    views: viewsOfType(type),
    isLoading: viewsLoadingState.current,
    error: undefined,
    createView: createRunViewMock,
    updateView: updateRunViewMock,
    deleteView: deleteRunViewMock,
    refresh: vi.fn(),
  }),
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({
    POST: vi.fn(),
    GET: dagsListState.get,
  }),
  useQuery: (path: string, init?: unknown) => ({
    data:
      init === null
        ? undefined
        : path === '/dags/labels'
          ? { labels: [] }
          : path === '/dags'
            ? { dags: dagsListState.dags }
            : undefined,
  }),
}));

const usePaginatedDAGRunsMock = vi.hoisted(() => vi.fn());

vi.mock('@/features/dag-runs/hooks/dagRunPagination', () => ({
  usePaginatedDAGRuns: usePaginatedDAGRunsMock,
}));

const useServiceDAGsMock = vi.hoisted(() => vi.fn());

vi.mock('@/features/dag-runs/hooks/useServiceDAGs', () => ({
  useServiceDAGs: useServiceDAGsMock,
}));

vi.mock('@/features/dag-runs/hooks/useBulkDAGRunSelection', () => ({
  useBulkDAGRunSelection: () => ({
    clearSelection: vi.fn(),
    replaceSelection: vi.fn(),
    selectAllLoaded: vi.fn(),
    selectedKeys: new Set(),
    selectedRuns: [],
    toggleSelection: vi.fn(),
  }),
}));

vi.mock('@/features/dag-runs/components/common/DAGRunBatchActions', () => ({
  default: () => null,
}));

const dagRunTableProps = vi.hoisted(() => ({
  current: {} as {
    isLoading?: boolean;
    dagRuns?: { name: string; dagRunId: string }[];
    serviceDags?: { name: string; fileName: string }[];
    serviceWindow?: { from: number; to?: number };
  },
}));

vi.mock('@/features/dag-runs/components/dag-run-list/DAGRunTable', async () => {
  const { usePeekOrOpen } = await import('@/features/peek/usePeekOrOpen');
  return {
    default: function RunTable(props: typeof dagRunTableProps.current) {
      dagRunTableProps.current = props;
      const peekOrOpen = usePeekOrOpen();
      return (
        <>
          <div>Run Table</div>
          <button
            type="button"
            onClick={() => peekOrOpen({ dag: 'alpha', run: 'run-1' })}
          >
            Peek alpha
          </button>
        </>
      );
    },
  };
});

const config = {
  tzOffsetInSec: undefined,
} as Config;

beforeEach(() => {
  readSearchStateMock.mockReset();
  readSearchStateMock.mockReturnValue(null);
  writeSearchStateMock.mockReset();
  createRunViewMock.mockReset();
  viewsLoadingState.current = false;
  updateRunViewMock.mockReset();
  deleteRunViewMock.mockReset();
  sharedRunViewState.views = [];
  dagsListState.dags = [];
  dagsListState.get.mockReset();
  useServiceDAGsMock.mockReset();
  useServiceDAGsMock.mockReturnValue([]);
  usePaginatedDAGRunsMock.mockReset();
  usePaginatedDAGRunsMock.mockReturnValue({
    dagRuns: [],
    isInitialLoading: false,
    isLoadingMore: false,
    loadMoreError: null,
    hasMore: false,
    refresh: vi.fn(),
    loadMore: vi.fn(),
  });
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
});

function makeRunView(overrides: Partial<View> = {}): View {
  return {
    id: 'failed-runs',
    name: 'Failed runs',
    type: ViewSpecType.run,
    intervalDays: 1,
    dagName: '',
    labels: [],
    runStatus: '5',
    dateMode: RunDateMode.preset,
    datePreset: RunDatePreset.today,
    specificPeriod: RunSpecificPeriod.date,
    specificValue: '',
    pinned: false,
    workspace: '',
    workspaceScope: ViewWorkspaceScope.all,
    createdAt: '2026-09-15T00:00:00Z',
    updatedAt: '2026-09-15T00:00:00Z',
    ...overrides,
  };
}

function lastRunQuery(): Record<string, unknown> {
  const calls = usePaginatedDAGRunsMock.mock.calls;
  return calls[calls.length - 1]?.[0]?.query ?? {};
}

function LocationProbe(): React.JSX.Element {
  const location = useLocation();
  return (
    <>
      <output data-testid="location-path">{location.pathname}</output>
      <output data-testid="location-search">{location.search}</output>
    </>
  );
}

function locationSearchParams(): URLSearchParams {
  return new URLSearchParams(
    screen.getByTestId('location-search').textContent ?? ''
  );
}

async function choosePeriod(
  user: ReturnType<typeof userEvent.setup>,
  name: string
): Promise<void> {
  await user.click(screen.getByRole('combobox', { name: 'Period' }));
  await user.click(await screen.findByRole('option', { name }));
}

function renderPage(
  setTitle = vi.fn(),
  initialEntry = '/dag-runs',
  configOverrides: Partial<Config> = {},
  workspaceSelection: WorkspaceSelection = { kind: WorkspaceKind.all }
): void {
  render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <LocationProbe />
      <ConfigContext.Provider
        value={
          {
            ...config,
            ...configOverrides,
          } as Config
        }
      >
        <AppBarContext.Provider
          value={
            {
              setTitle,
              selectedRemoteNode: 'local',
              workspaceSelection,
            } as never
          }
        >
          <DAGRuns />
        </AppBarContext.Provider>
      </ConfigContext.Provider>
    </MemoryRouter>
  );
}

describe('DAGRuns page', () => {
  it('uses the Workflows page title with the Runs mode selected', () => {
    const setTitle = vi.fn();

    renderPage(setTitle);

    expect(screen.getByRole('heading', { name: /^workflows$/i })).toBeVisible();
    expect(screen.queryByRole('heading', { name: /executions/i })).toBeNull();
    expect(screen.queryByRole('heading', { name: /dag runs/i })).toBeNull();
    expect(setTitle).toHaveBeenCalledWith('Workflows');
    expect(screen.getByRole('button', { name: 'Runs' })).toBeVisible();
    expect(screen.getByRole('button', { name: 'Workflows' })).toBeVisible();
    expect(screen.getByTestId('workflow-count')).toHaveTextContent('0 runs');
  });

  // A preset range means "relative to now", so a session left open across a
  // date boundary must not keep querying the range it computed back then.
  it('recomputes a preset range restored from session state', async () => {
    const stale = dayjs().subtract(3, 'day').startOf('day');
    readSearchStateMock.mockReturnValue({
      searchText: '',
      dagRunId: '',
      status: 'all',
      labels: [],
      fromDate: stale.format('YYYY-MM-DDTHH:mm'),
      toDate: undefined,
      dateRangeMode: 'preset',
      datePreset: 'today',
      specificPeriod: 'date',
      specificValue: '',
    });

    renderPage();

    await waitFor(() => {
      expect(usePaginatedDAGRunsMock.mock.calls.length).toBeGreaterThan(0);
    });
    expect(lastRunQuery()['fromDate']).toBe(dayjs().startOf('day').unix());
  });

  it('keeps stored session filters while the shared views load', async () => {
    // The page reads and writes the same storage; model that so persistence
    // of the initial defaults while views load is visible to restoration.
    let stored: unknown = {
      searchText: 'adhoc',
      dagRunId: '',
      status: 'all',
      labels: [],
      fromDate: '2026-09-01T00:00',
      toDate: undefined,
      dateRangeMode: 'preset',
      datePreset: 'today',
      specificPeriod: 'date',
      specificValue: '2026-09-15',
    };
    readSearchStateMock.mockImplementation(() => stored);
    writeSearchStateMock.mockImplementation(
      (_key: string, _scope: string, value: unknown) => {
        stored = value;
      }
    );
    viewsLoadingState.current = true;

    renderPage();

    // Once the views finish loading, the restore must apply the stored
    // session filters and persist them, not the initial defaults that were
    // active while loading.
    viewsLoadingState.current = false;
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));

    await waitFor(() => {
      expect(lastRunQuery()['name']).toBe('adhoc');
    });
    await waitFor(() => {
      expect((stored as { searchText?: string }).searchText).toBe('adhoc');
    });
  });

  it('passes the initial-load state to the runs table', () => {
    usePaginatedDAGRunsMock.mockReturnValue({
      dagRuns: [],
      isInitialLoading: true,
      isLoadingMore: false,
      loadMoreError: null,
      hasMore: false,
      refresh: vi.fn(),
      loadMore: vi.fn(),
    });

    renderPage();

    expect(dagRunTableProps.current.isLoading).toBe(true);
  });

  it('uses consistent filter control sizing', () => {
    renderPage();

    expect(
      screen.getByPlaceholderText('Filter by workflow name...').className
    ).toContain('h-9');
    expect(
      screen.getByPlaceholderText('Filter by Run ID...').className
    ).toContain('h-9');
    expect(
      screen.getByRole('combobox', { name: 'Status' }).className
    ).toContain('h-9');
    expect(
      screen.getByRole('combobox', { name: 'Period' }).className
    ).toContain('h-9');
    expect(screen.getByRole('button', { name: 'Search' }).className).toContain(
      'h-9'
    );

    const labelInput = screen.getByRole('combobox', {
      name: 'Filter by labels...',
    });
    expect(labelInput.parentElement?.className).toContain('min-h-9');
    expect(labelInput.parentElement?.className).toContain('bg-card');

    expect(screen.getByRole('combobox', { name: 'Status' })).toHaveTextContent(
      'All Statuses'
    );
  });

  // Without the overlay of the app around it, a run opens on its page.
  it('opens a run of the list on its workflow page', () => {
    renderPage();

    fireEvent.click(screen.getByRole('button', { name: 'Peek alpha' }));

    expect(screen.getByTestId('location-path')).toHaveTextContent(
      '/dags/alpha'
    );
    expect(locationSearchParams().get('run')).toBe('run-1');
  });

  // Links from before the run page replaced the side panel.
  it('opens the run of a legacy ?selectedRunName link on its workflow page', () => {
    renderPage(
      vi.fn(),
      '/dag-runs?selectedRunName=demo&selectedRunId=run-1&selectedRunTab=artifacts'
    );

    expect(screen.getByTestId('location-path')).toHaveTextContent('/dags/demo');
    expect(locationSearchParams().get('run')).toBe('run-1');
    expect(locationSearchParams().get('dock')).toBe('artifacts');
    expect(locationSearchParams().get('remoteNode')).toBe('local');
    expect(locationSearchParams().has('selectedRunName')).toBe(false);
  });

  it('suggests DAG names and applies the selected suggestion as the filter', async () => {
    // File names differ from DAG names; runs are filtered by DAG name, so
    // only DAG names may be suggested.
    dagsListState.dags = [
      { fileName: 'deploy', dag: { name: 'deploy-api' } },
      { fileName: 'backup', dag: { name: 'nightly-backup' } },
    ];
    renderPage();

    const input = screen.getByPlaceholderText('Filter by workflow name...');
    fireEvent.focus(input);
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();

    // Suggestions arrive from /dags once the debounced input is set
    fireEvent.change(input, { target: { value: 'a' } });
    await waitFor(() => {
      expect(screen.getByRole('option', { name: 'deploy-api' })).toBeVisible();
    });
    expect(
      screen.getByRole('option', { name: 'nightly-backup' })
    ).toBeVisible();

    fireEvent.change(input, { target: { value: 'deploy' } });
    expect(
      screen.queryByRole('option', { name: 'nightly-backup' })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('option', { name: 'deploy' })
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('option', { name: 'deploy-api' }));
    expect(input).toHaveValue('deploy-api');

    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() => {
      expect(lastRunQuery()['name']).toBe('deploy-api');
    });
    expect(locationSearchParams().get('name')).toBe('deploy-api');
  });

  it('suggests run IDs from loaded runs and applies the selection', async () => {
    usePaginatedDAGRunsMock.mockReturnValue({
      dagRuns: [
        {
          name: 'demo',
          dagRunId: 'run-abc-1',
          scheduleTime: '2026-09-16T02:00:00Z',
        },
        {
          name: 'demo',
          dagRunId: 'run-abc-2',
          scheduleTime: '2026-09-16T01:00:00Z',
        },
        {
          name: 'other',
          dagRunId: 'run-xyz-9',
          scheduleTime: '2026-09-16T01:30:00Z',
        },
      ],
      isInitialLoading: false,
      isLoadingMore: false,
      loadMoreError: null,
      hasMore: false,
      refresh: vi.fn(),
      loadMore: vi.fn(),
    });
    renderPage();

    const input = screen.getByPlaceholderText('Filter by Run ID...');
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: 'abc' } });

    expect(screen.getByRole('option', { name: 'run-abc-1' })).toBeVisible();
    expect(screen.getByRole('option', { name: 'run-abc-2' })).toBeVisible();
    expect(
      screen.queryByRole('option', { name: 'run-xyz-9' })
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('option', { name: 'run-abc-2' }));
    expect(input).toHaveValue('run-abc-2');

    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() => {
      expect(lastRunQuery()['dagRunId']).toBe('run-abc-2');
    });
  });

  it('keeps only active date-mode parameters after Search', async () => {
    const user = userEvent.setup();
    renderPage();

    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() => {
      const params = locationSearchParams();
      expect(params.get('dateMode')).toBe('preset');
      expect(params.get('preset')).toBe('today');
      expect(params.has('specificValue')).toBe(false);
      expect(params.has('specificPeriod')).toBe(false);
    });

    await choosePeriod(user, 'Specific date…');
    await waitFor(() => {
      const params = locationSearchParams();
      expect(params.get('dateMode')).toBe('specific');
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() => {
      const params = locationSearchParams();
      expect(params.has('preset')).toBe(false);
      expect(params.get('specificValue')).not.toBeNull();
      expect(params.get('specificPeriod')).toBe('date');
    });

    await choosePeriod(user, 'Custom range…');
    await waitFor(() => {
      const params = locationSearchParams();
      expect(params.get('dateMode')).toBe('custom');
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() => {
      const params = locationSearchParams();
      expect(params.has('preset')).toBe(false);
      expect(params.has('specificValue')).toBe(false);
      expect(params.has('specificPeriod')).toBe(false);
    });
  });

  it('interprets custom dates in the configured timezone', async () => {
    const user = userEvent.setup();
    renderPage(vi.fn(), '/dag-runs', { tzOffsetInSec: -5 * 60 * 60 });

    await choosePeriod(user, 'Custom range…');
    const inputs = await screen.findAllByPlaceholderText('YYYY-MM-DD HH:mm:ss');
    const fromInput = inputs[0]!;
    const toInput = inputs[1]!;
    await user.clear(fromInput);
    await user.type(fromInput, '2026-09-15 00:00:00');
    await user.clear(toInput);
    await user.type(toInput, '2026-09-16 00:00:00');
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));

    const query = lastRunQuery();
    expect(query['fromDate']).toBe(Date.UTC(2026, 8, 15, 5, 0, 0) / 1000);
    expect(query['toDate']).toBe(Date.UTC(2026, 8, 16, 5, 0, 0) / 1000);
  });

  it('applies the default run view to the first request', async () => {
    sharedRunViewState.views.push(
      makeRunView({
        isDefault: true,
        dagName: 'deploy',
      })
    );

    renderPage();

    await waitFor(() => {
      expect(lastRunQuery()['name']).toBe('deploy');
      expect(lastRunQuery()['status']).toEqual([5]);
      expect(lastRunQuery()['fromDate']).toBeTypeOf('number');
    });
    expect(
      screen.getByRole('button', { name: 'Run view: Failed runs' })
    ).toBeVisible();
  });

  it('uses the bookmarked run view from the URL', async () => {
    sharedRunViewState.views.push(
      makeRunView({
        id: 'url-view',
        name: 'Nightly runs',
        runStatus: '1',
        dagName: 'nightly',
      })
    );

    renderPage(vi.fn(), '/dag-runs?view=url-view');

    await waitFor(() => {
      expect(lastRunQuery()['name']).toBe('nightly');
      expect(lastRunQuery()['status']).toEqual([1]);
    });
    expect(
      screen.getByRole('button', { name: 'Run view: Nightly runs' })
    ).toBeVisible();
  });

  it('gives explicit URL filters precedence over the requested run view', async () => {
    sharedRunViewState.views.push(
      makeRunView({ id: 'view-a', name: 'View A', runStatus: '5' })
    );

    renderPage(vi.fn(), '/dag-runs?view=view-a&name=adhoc&status=1');

    await waitFor(() => {
      expect(lastRunQuery()['name']).toBe('adhoc');
      expect(lastRunQuery()['status']).toEqual([1]);
    });
    expect(
      screen.getByRole('button', { name: 'Run view: View A' })
    ).toBeVisible();
  });

  it('saves the current filters as a run view and applies it', async () => {
    const user = userEvent.setup();
    createRunViewMock.mockResolvedValue(
      makeRunView({ id: 'nightly-view', name: 'Nightly runs' })
    );

    renderPage();

    fireEvent.change(
      screen.getByPlaceholderText('Filter by workflow name...'),
      {
        target: { value: 'nightly' },
      }
    );
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() => {
      expect(locationSearchParams().get('name')).toBe('nightly');
    });

    expect(
      screen.getByRole('button', { name: 'Run view: Custom view' })
    ).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Save view' }));
    await user.type(
      screen.getByRole('textbox', { name: 'Name' }),
      'Nightly runs'
    );
    await user.click(screen.getByRole('button', { name: 'Save view' }));

    await waitFor(() => {
      expect(createRunViewMock).toHaveBeenCalledWith(
        expect.objectContaining({
          // The two modes share their views, which are created as workflow
          // views.
          type: ViewSpecType.workflow,
          name: 'Nightly runs',
          dagName: 'nightly',
          intervalDays: 1,
        })
      );
    });
    await waitFor(() => {
      expect(locationSearchParams().get('view')).toBe('nightly-view');
    });
    // Preset and specific views keep relative date params and derive the
    // concrete range when applied; only custom ranges persist dates.
    expect(locationSearchParams().has('fromDate')).toBe(false);
  });

  it('marks a run view as edited when its filters change and resets via the menu', async () => {
    const user = userEvent.setup();
    sharedRunViewState.views.push(makeRunView({ dagName: 'deploy' }));

    renderPage(vi.fn(), '/dag-runs?view=failed-runs');

    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: 'Run view: Failed runs' })
      ).toBeVisible();
    });
    expect(screen.queryByText('Edited')).not.toBeInTheDocument();

    fireEvent.change(
      screen.getByPlaceholderText('Filter by workflow name...'),
      {
        target: { value: 'etl' },
      }
    );
    expect(screen.getByText('Edited')).toBeVisible();

    await user.click(
      screen.getByRole('button', { name: 'Run view: Failed runs' })
    );
    await user.click(screen.getByRole('menuitem', { name: 'Reset changes' }));

    await waitFor(() => {
      expect(
        screen.getByPlaceholderText('Filter by workflow name...')
      ).toHaveValue('deploy');
    });
  });

  it('resets edited filters when the view was selected from the dropdown', async () => {
    const user = userEvent.setup();
    sharedRunViewState.views.push(makeRunView({ dagName: 'deploy' }));

    renderPage();

    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: 'Run view: All runs' })
      ).toBeVisible();
    });
    await user.click(
      screen.getByRole('button', { name: 'Run view: All runs' })
    );
    await user.click(screen.getByRole('menuitem', { name: /failed runs/i }));
    await waitFor(() => {
      expect(lastRunQuery()['name']).toBe('deploy');
      expect(
        screen.getByPlaceholderText('Filter by workflow name...')
      ).toHaveValue('deploy');
    });

    // Editing without searching leaves the URL, and therefore the reset
    // target URL, unchanged.
    fireEvent.change(
      screen.getByPlaceholderText('Filter by workflow name...'),
      {
        target: { value: 'etl' },
      }
    );
    expect(screen.getByText('Edited')).toBeVisible();

    await user.click(
      screen.getByRole('button', { name: 'Run view: Failed runs' })
    );
    await user.click(screen.getByRole('menuitem', { name: 'Reset changes' }));

    await waitFor(() => {
      expect(
        screen.getByPlaceholderText('Filter by workflow name...')
      ).toHaveValue('deploy');
    });
    expect(lastRunQuery()['name']).toBe('deploy');
  });

  it('restores concrete dates from a legacy URL without dateMode', async () => {
    renderPage(
      vi.fn(),
      '/dag-runs?fromDate=2026-09-01T00%3A00&toDate=2026-09-15T23%3A59'
    );

    await waitFor(() => {
      expect(lastRunQuery()['fromDate']).toBe(dayjs('2026-09-01T00:00').unix());
      expect(lastRunQuery()['toDate']).toBe(dayjs('2026-09-15T23:59').unix());
    });
  });

  it('derives a fresh range for a standalone preset URL', async () => {
    renderPage(
      vi.fn(),
      '/dag-runs?dateMode=preset&preset=yesterday&fromDate=2026-01-01T00%3A00&toDate=2026-01-01T23%3A59'
    );

    await waitFor(() => {
      const todayStart = dayjs().startOf('day');
      expect(lastRunQuery()['fromDate']).toBe(
        todayStart.subtract(1, 'day').unix()
      );
      expect(lastRunQuery()['toDate']).toBe(todayStart.unix());
    });
  });

  it('derives a fresh range for a standalone specific URL', async () => {
    renderPage(
      vi.fn(),
      '/dag-runs?dateMode=specific&specificPeriod=date&specificValue=2026-09-15'
    );

    await waitFor(() => {
      expect(lastRunQuery()['fromDate']).toBe(dayjs('2026-09-15T00:00').unix());
      expect(lastRunQuery()['toDate']).toBe(dayjs('2026-09-16T00:00').unix());
    });
  });

  it('keeps a legacy concrete range when searching after a legacy restore', async () => {
    renderPage(
      vi.fn(),
      '/dag-runs?fromDate=2026-09-01T00%3A00&toDate=2026-09-15T23%3A59'
    );

    await waitFor(() => {
      expect(lastRunQuery()['fromDate']).toBe(dayjs('2026-09-01T00:00').unix());
    });

    fireEvent.change(
      screen.getByPlaceholderText('Filter by workflow name...'),
      {
        target: { value: 'etl' },
      }
    );
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));

    await waitFor(() => {
      expect(lastRunQuery()['name']).toBe('etl');
      expect(lastRunQuery()['fromDate']).toBe(dayjs('2026-09-01T00:00').unix());
      expect(lastRunQuery()['toDate']).toBe(dayjs('2026-09-15T23:59').unix());
    });
  });

  it('honors cleared filters when a saved view is active', async () => {
    sharedRunViewState.views.push(
      makeRunView({ dagName: 'deploy', dagRunId: 'run-9' })
    );

    renderPage(vi.fn(), '/dag-runs?view=failed-runs');

    await waitFor(() => {
      expect(lastRunQuery()['name']).toBe('deploy');
      expect(lastRunQuery()['dagRunId']).toBe('run-9');
    });

    fireEvent.change(
      screen.getByPlaceholderText('Filter by workflow name...'),
      {
        target: { value: '' },
      }
    );
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() => {
      expect(lastRunQuery()['name']).toBeUndefined();
    });

    fireEvent.change(screen.getByPlaceholderText('Filter by Run ID...'), {
      target: { value: '' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() => {
      expect(lastRunQuery()['dagRunId']).toBeUndefined();
    });
  });

  it('switches workspaces to the destination default run view', async () => {
    sharedRunViewState.views.push(
      makeRunView({
        id: 'a-view',
        name: 'Workspace A view',
        dagName: 'a-dag',
        workspace: 'a',
        workspaceScope: ViewWorkspaceScope.workspace,
        isDefault: true,
      })
    );
    sharedRunViewState.views.push(
      makeRunView({
        id: 'b-view',
        name: 'Workspace B view',
        dagName: 'b-dag',
        workspace: 'b',
        workspaceScope: ViewWorkspaceScope.workspace,
        isDefault: true,
      })
    );

    const setTitle = vi.fn();
    function Harness(): React.JSX.Element {
      const [workspace, setWorkspace] = React.useState('a');
      return (
        <MemoryRouter initialEntries={['/dag-runs?view=a-view']}>
          <LocationProbe />
          <button type="button" onClick={() => setWorkspace('b')}>
            Switch to workspace B
          </button>
          <ConfigContext.Provider value={config}>
            <AppBarContext.Provider
              value={
                {
                  setTitle,
                  selectedRemoteNode: 'local',
                  workspaceSelection: {
                    kind: WorkspaceKind.workspace,
                    workspace,
                  },
                } as never
              }
            >
              <DAGRuns />
            </AppBarContext.Provider>
          </ConfigContext.Provider>
        </MemoryRouter>
      );
    }
    render(<Harness />);

    await waitFor(() => {
      expect(lastRunQuery()['name']).toBe('a-dag');
    });

    fireEvent.click(
      screen.getByRole('button', { name: 'Switch to workspace B' })
    );
    await waitFor(() => {
      expect(lastRunQuery()['name']).toBe('b-dag');
    });
    expect(locationSearchParams().get('view')).toBe('b-view');
  });

  it('ignores stale concrete dates for preset views and derives them fresh', async () => {
    sharedRunViewState.views.push(
      makeRunView({
        dagName: 'deploy',
        dateMode: RunDateMode.preset,
        datePreset: RunDatePreset.today,
      })
    );

    renderPage(
      vi.fn(),
      '/dag-runs?view=failed-runs&dateMode=preset&preset=today&fromDate=2026-01-01T00%3A00'
    );

    await waitFor(() => {
      expect(lastRunQuery()['fromDate']).toBe(dayjs().startOf('day').unix());
      expect(lastRunQuery()['name']).toBe('deploy');
    });
  });

  it('deletes the active run view and falls back to All runs', async () => {
    const user = userEvent.setup();
    sharedRunViewState.views.push(makeRunView({ dagName: 'deploy' }));
    deleteRunViewMock.mockResolvedValue(undefined);

    renderPage(vi.fn(), '/dag-runs?view=failed-runs');

    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: 'Run view: Failed runs' })
      ).toBeVisible();
    });

    await user.click(screen.getByRole('button', { name: 'Delete view' }));
    await user.click(
      await screen.findByRole('button', { name: 'Delete view' })
    );

    await waitFor(() => {
      expect(deleteRunViewMock).toHaveBeenCalledWith('failed-runs');
    });
    await waitFor(() => {
      expect(locationSearchParams().get('view')).toBe('all');
    });
  });
});

// The runs of a service DAG leave the list: one row for each service DAG
// takes their place (spec 097), over the window of the filters. A filter on
// the name, the run ID, the status or the labels shows the runs as they are.
describe('DAGRuns page service DAGs', () => {
  const svc = { name: 'svc', fileName: 'svc.yaml' };
  const lastServiceRead = () => {
    const calls = useServiceDAGsMock.mock.calls;
    return calls[calls.length - 1] as [Record<string, unknown>, boolean];
  };

  beforeEach(() => {
    useServiceDAGsMock.mockReturnValue([svc]);
  });

  it('leaves out the runs of service DAGs and shows their rows', async () => {
    renderPage();
    await waitFor(() => {
      expect(dagRunTableProps.current.serviceDags).toEqual([svc]);
    });
    expect(lastRunQuery()['excludeServiceDAGRuns']).toBe(true);
    expect(lastServiceRead()).toEqual([
      { remoteNode: 'local', workspace: 'all' },
      true,
    ]);
    // The window is the date filter: the start of today until now.
    expect(dagRunTableProps.current.serviceWindow).toEqual({
      from: dayjs().startOf('day').unix() * 1000,
      to: undefined,
    });
  });

  it('reads the service DAGs of the workspace', async () => {
    renderPage(
      vi.fn(),
      '/dag-runs',
      {},
      { kind: WorkspaceKind.workspace, workspace: 'ops' }
    );
    await waitFor(() => {
      expect(lastRunQuery()['workspace']).toBe('ops');
    });
    expect(lastRunQuery()['excludeServiceDAGRuns']).toBe(true);
    expect(lastServiceRead()).toEqual([
      { remoteNode: 'local', workspace: 'ops' },
      true,
    ]);
  });

  it.each([
    ['name', '/dag-runs?name=svc'],
    ['run ID', '/dag-runs?dagRunId=svc-3'],
    ['status', '/dag-runs?status=1'],
    ['labels', '/dag-runs?labels=prod'],
  ])('shows the runs as they are with a %s filter', async (_, url) => {
    renderPage(vi.fn(), url);
    await waitFor(() => {
      expect(lastServiceRead()[1]).toBe(false);
    });
    await act(async () => {});
    const query = lastRunQuery();
    expect(query).not.toHaveProperty('excludeServiceDAGRuns', true);
    expect(JSON.stringify(query)).not.toContain('excludeServiceDAGRuns');
  });

  it('gives the end date of the filters to the rows', async () => {
    renderPage(
      vi.fn(),
      '/dag-runs?dateMode=custom&fromDate=2026-11-01T00:00&toDate=2026-11-02T00:00'
    );
    await waitFor(() => {
      expect(dagRunTableProps.current.serviceDags).toEqual([svc]);
    });
    expect(dagRunTableProps.current.serviceWindow).toEqual({
      from: Number(lastRunQuery()['fromDate']) * 1000,
      to: Number(lastRunQuery()['toDate']) * 1000,
    });
  });
});
