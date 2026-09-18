// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import dayjs from 'dayjs';
import React from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import {
  RunDateMode,
  RunDatePreset,
  ViewSpecType,
  ViewWorkspaceScope,
} from '@/api/v1/schema';
import type { ArtifactListItem } from '@/features/artifacts/hooks/artifactListPagination';
import type { View } from '@/hooks/useViews';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { WorkspaceKind } from '@/lib/workspace';
import Artifacts from '..';

const {
  createArtifactViewMock,
  deleteArtifactViewMock,
  readSearchStateMock,
  searchStateMock,
  sharedArtifactViewState,
  updateArtifactViewMock,
  viewsLoadingState,
} = vi.hoisted(() => {
  const readState = vi.fn((): unknown => null);
  const writeState = vi.fn();
  return {
    createArtifactViewMock: vi.fn(),
    deleteArtifactViewMock: vi.fn(),
    updateArtifactViewMock: vi.fn(),
    readSearchStateMock: readState,
    searchStateMock: { readState, writeState },
    sharedArtifactViewState: { views: [] as View[] },
    viewsLoadingState: { current: false },
  };
});

vi.mock('@/contexts/SearchStateContext', () => ({
  useSearchState: () => searchStateMock,
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteForWorkspace: () => true,
}));

vi.mock('@/hooks/useViews', () => ({
  useViews: () => ({
    views: sharedArtifactViewState.views,
    isLoading: viewsLoadingState.current,
    error: undefined,
    createView: createArtifactViewMock,
    updateView: updateArtifactViewMock,
    deleteView: deleteArtifactViewMock,
    refresh: vi.fn(),
  }),
}));

const usePaginatedArtifactsMock = vi.hoisted(() => vi.fn());

const usePaginatedArtifactsResult = vi.hoisted(() => ({
  current: {
    items: [] as ArtifactListItem[],
    error: null as Error | null,
    isInitialLoading: false,
    isLoadingMore: false,
    loadMoreError: null,
    hasMore: false,
    refresh: vi.fn(),
    loadMore: vi.fn(),
  },
}));

vi.mock('@/features/artifacts/hooks/artifactListPagination', () => ({
  usePaginatedArtifacts: usePaginatedArtifactsMock,
}));

vi.mock('@/features/dags/components/artifacts/ArtifactFilePreview', () => ({
  ArtifactFilePreview: (props: {
    dagRunName: string;
    dagRunId: string;
    path: string | null;
  }) => (
    <div data-testid="preview-pane">
      {props.path
        ? `preview of ${props.path} in ${props.dagRunName}/${props.dagRunId}`
        : 'no selection'}
    </div>
  ),
}));

const config = {
  tzOffsetInSec: undefined,
} as Config;

function makeItem(overrides: Partial<ArtifactListItem> = {}): ArtifactListItem {
  return {
    name: 'reporter',
    dagRunId: 'run-1',
    createdAt: '2026-09-15T14:32:07Z',
    startedAt: '2026-09-15T14:40:00Z',
    files: [
      { path: 'out/report.md', size: 42 },
      { path: 'out/plot.png', size: 1024 },
    ],
    filesTruncated: false,
    ...overrides,
  };
}

beforeEach(() => {
  usePaginatedArtifactsResult.current = {
    items: [],
    error: null,
    isInitialLoading: false,
    isLoadingMore: false,
    loadMoreError: null,
    hasMore: false,
    refresh: vi.fn(),
    loadMore: vi.fn(),
  };
  usePaginatedArtifactsMock.mockReset();
  usePaginatedArtifactsMock.mockImplementation(
    () => usePaginatedArtifactsResult.current
  );
  sharedArtifactViewState.views = [];
  viewsLoadingState.current = false;
  readSearchStateMock.mockReset();
  readSearchStateMock.mockReturnValue(null);
  createArtifactViewMock.mockReset();
  updateArtifactViewMock.mockReset();
  deleteArtifactViewMock.mockReset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

function makeArtifactView(overrides: Partial<View> = {}): View {
  return {
    id: 'nightly-reports',
    name: 'Nightly reports',
    type: ViewSpecType.artifact,
    intervalDays: 1,
    dagName: '',
    fileName: '',
    dateMode: RunDateMode.preset,
    datePreset: RunDatePreset.all,
    pinned: false,
    workspace: '',
    workspaceScope: ViewWorkspaceScope.all,
    createdAt: '2026-09-15T00:00:00Z',
    updatedAt: '2026-09-15T00:00:00Z',
    ...overrides,
  };
}

function LocationProbe(): React.JSX.Element {
  const location = useLocation();
  return <output data-testid="location-search">{location.search}</output>;
}

function locationSearchParams(): URLSearchParams {
  return new URLSearchParams(
    screen.getByTestId('location-search').textContent ?? ''
  );
}

function renderPage(
  setTitle = vi.fn(),
  configOverrides: Partial<Config> = {},
  initialEntry = '/artifacts'
) {
  const content = () => (
    <MemoryRouter initialEntries={[initialEntry]}>
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
              workspaceSelection: { kind: WorkspaceKind.all },
            } as never
          }
        >
          <Artifacts />
          <LocationProbe />
        </AppBarContext.Provider>
      </ConfigContext.Provider>
    </MemoryRouter>
  );
  const view = render(content());
  return { ...view, rerenderPage: () => view.rerender(content()) };
}

function lastQuery(): Record<string, unknown> {
  const calls = usePaginatedArtifactsMock.mock.calls;
  return calls[calls.length - 1]?.[0]?.query ?? {};
}

describe('Artifacts page', () => {
  it('uses the Artifacts page title', () => {
    const setTitle = vi.fn();
    renderPage(setTitle);

    expect(setTitle).toHaveBeenCalledWith('Artifacts');
  });

  it('passes the workspace and remote node in the query', () => {
    renderPage();

    const query = lastQuery();
    expect(query.remoteNode).toBe('local');
    expect(query.workspace).toBe(WorkspaceKind.all);
    expect(query.limit).toBe(100);
  });

  it('lists recent runs without a date filter by default', () => {
    renderPage();

    const query = lastQuery();
    expect(query.fromDate).toBeUndefined();
    expect(query.toDate).toBeUndefined();
  });

  it('applies the DAG name filter when Enter is pressed', async () => {
    const user = userEvent.setup();
    renderPage();

    const input = screen.getByPlaceholderText('Filter by DAG name...');
    await user.type(input, 'demo');
    fireEvent.keyDown(input, { key: 'Enter' });

    expect(lastQuery().name).toBe('demo');
  });

  it('applies the file name filter when Enter is pressed', async () => {
    const user = userEvent.setup();
    renderPage();

    const input = screen.getByPlaceholderText('Filter by file name...');
    await user.type(input, 'report.md');
    fireEvent.keyDown(input, { key: 'Enter' });

    expect(lastQuery().fileName).toBe('report.md');
  });

  it('interprets custom dates in the configured timezone', async () => {
    const user = userEvent.setup();
    renderPage(vi.fn(), { tzOffsetInSec: -5 * 60 * 60 });

    fireEvent.click(screen.getByRole('button', { name: 'Custom' }));
    const inputs = await screen.findAllByPlaceholderText('YYYY-MM-DD HH:mm:ss');
    const fromInput = inputs[0]!;
    const toInput = inputs[1]!;
    await user.clear(fromInput);
    await user.type(fromInput, '2026-09-15 00:00:00');
    await user.clear(toInput);
    await user.type(toInput, '2026-09-16 00:00:00');
    fireEvent.keyDown(fromInput, { key: 'Enter' });

    const query = lastQuery();
    expect(query.fromDate).toBe(Date.UTC(2026, 8, 15, 5, 0, 0) / 1000);
    expect(query.toDate).toBe(Date.UTC(2026, 8, 16, 5, 0, 0) / 1000);
  });

  it('lists runs with their artifact files and previews the first file', () => {
    usePaginatedArtifactsResult.current.items = [
      makeItem(),
      makeItem({ name: 'ingest', dagRunId: 'run-2', files: [] }),
    ];
    renderPage();

    expect(
      screen.getByRole('treeitem', { name: /reporter/ })
    ).toBeInTheDocument();
    expect(
      screen.getByRole('treeitem', { name: /ingest/ })
    ).toBeInTheDocument();
    // The newest run with files opens automatically; its first file is
    // selected so the preview pane shows something immediately.
    expect(
      screen.getByRole('treeitem', { name: /report\.md/ })
    ).toBeInTheDocument();
    expect(
      screen.getByText('preview of out/report.md in reporter/run-1')
    ).toBeInTheDocument();
    // Each run links to its DAG-run page.
    expect(
      screen.getByRole('link', { name: 'Open DAG run reporter' })
    ).toHaveAttribute('href', '/dag-runs/reporter/run-1');
    expect(
      screen.getByRole('link', { name: 'Open DAG run ingest' })
    ).toHaveAttribute('href', '/dag-runs/ingest/run-2');
  });

  it('previews a file after it is selected', () => {
    usePaginatedArtifactsResult.current.items = [makeItem()];
    renderPage();

    fireEvent.click(screen.getByRole('treeitem', { name: /plot\.png/ }));

    expect(
      screen.getByText('preview of out/plot.png in reporter/run-1')
    ).toBeInTheDocument();
  });

  it('expands a collapsed run to reveal its files', () => {
    usePaginatedArtifactsResult.current.items = [
      makeItem(),
      makeItem({
        name: 'oldest',
        dagRunId: 'run-9',
        files: [{ path: 'top/raw.json', size: 7 }],
      }),
    ];
    renderPage();

    expect(
      screen.queryByRole('treeitem', { name: /raw\.json/ })
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('treeitem', { name: /oldest/ }));
    fireEvent.click(screen.getByRole('treeitem', { name: 'top' }));

    expect(
      screen.getByRole('treeitem', { name: /raw\.json/ })
    ).toBeInTheDocument();
    // The selection is untouched by expanding another run.
    expect(
      screen.getByText('preview of out/report.md in reporter/run-1')
    ).toBeInTheDocument();
  });

  it('marks runs whose file lists were truncated', () => {
    usePaginatedArtifactsResult.current.items = [
      makeItem({ filesTruncated: true }),
    ];
    renderPage();

    expect(
      screen.getByText('· selective list, use the run to view all files')
    ).toBeInTheDocument();
  });

  it('shows an API error instead of an empty state', () => {
    usePaginatedArtifactsResult.current.error = new Error(
      'Invalid file name pattern'
    );
    renderPage();

    expect(screen.getByText('Invalid file name pattern')).toBeInTheDocument();
    expect(screen.queryByText('No artifacts found')).not.toBeInTheDocument();
  });

  it('shows an empty state when no runs produced artifacts', () => {
    renderPage();

    expect(screen.getByText('No artifacts found')).toBeInTheDocument();
  });

  it('shows a load more button when a next cursor exists', () => {
    usePaginatedArtifactsResult.current.items = [makeItem()];
    usePaginatedArtifactsResult.current.hasMore = true;
    renderPage();

    expect(
      screen.getByRole('button', { name: 'Load more' })
    ).toBeInTheDocument();
  });

  it('moves the selected file with the down and up arrows', () => {
    usePaginatedArtifactsResult.current.items = [makeItem()];
    renderPage();

    expect(
      screen.getByText('preview of out/report.md in reporter/run-1')
    ).toBeInTheDocument();

    fireEvent.keyDown(document.activeElement!, { key: 'ArrowDown' });
    expect(
      screen.getByText('preview of out/plot.png in reporter/run-1')
    ).toBeInTheDocument();

    fireEvent.keyDown(document.activeElement!, { key: 'ArrowUp' });
    expect(
      screen.getByText('preview of out/report.md in reporter/run-1')
    ).toBeInTheDocument();
  });

  it('moves the selected file with j and k', () => {
    usePaginatedArtifactsResult.current.items = [makeItem()];
    renderPage();

    fireEvent.keyDown(document.activeElement!, { key: 'j' });
    expect(
      screen.getByText('preview of out/plot.png in reporter/run-1')
    ).toBeInTheDocument();

    fireEvent.keyDown(document.activeElement!, { key: 'k' });
    expect(
      screen.getByText('preview of out/report.md in reporter/run-1')
    ).toBeInTheDocument();
  });

  it('focuses files and collapsed runs with j and k', async () => {
    const user = userEvent.setup();
    usePaginatedArtifactsResult.current.items = [
      makeItem(),
      makeItem({
        name: 'oldest',
        dagRunId: 'run-9',
        files: [{ path: 'top/raw.json', size: 7 }],
      }),
    ];
    renderPage();

    expect(screen.getByRole('treeitem', { name: 'report.md' })).toHaveFocus();
    await user.keyboard('j');
    expect(screen.getByRole('treeitem', { name: 'plot.png' })).toHaveFocus();
    await user.keyboard('j');
    const olderRun = screen.getByRole('treeitem', { name: 'oldest' });
    expect(olderRun).toHaveFocus();
    expect(olderRun).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByTestId('preview-pane')).toHaveTextContent(
      'out/plot.png'
    );

    await user.keyboard('{ArrowRight}{ArrowRight}');
    expect(screen.getByRole('treeitem', { name: 'top' })).toHaveFocus();
    await user.keyboard('{ArrowRight}{ArrowRight}');
    expect(screen.getByRole('treeitem', { name: 'raw.json' })).toHaveFocus();
    expect(screen.getByTestId('preview-pane')).toHaveTextContent(
      'oldest/run-9'
    );
    await user.keyboard('k');
    expect(screen.getByRole('treeitem', { name: 'top' })).toHaveFocus();
  });

  it('returns from the filename filter without navigating while typing', async () => {
    const user = userEvent.setup();
    usePaginatedArtifactsResult.current.items = [makeItem()];
    renderPage();

    await user.keyboard('/');
    const input = screen.getByPlaceholderText('Filter by file name...');
    expect(input).toHaveFocus();
    await user.keyboard('jk');
    expect(input).toHaveValue('jk');
    expect(screen.getByTestId('preview-pane')).toHaveTextContent(
      'out/report.md'
    );
    await user.keyboard('{Escape}');
    expect(screen.getByRole('treeitem', { name: 'report.md' })).toHaveFocus();
  });

  it('leaves and reenters the tree with a single tab stop', async () => {
    const user = userEvent.setup();
    usePaginatedArtifactsResult.current.items = [makeItem()];
    renderPage();

    await user.keyboard('j');
    await user.tab();
    expect(screen.getByRole('link', { name: 'Open DAG run' })).toHaveFocus();
    await user.tab({ shift: true });
    expect(screen.getByRole('treeitem', { name: 'plot.png' })).toHaveFocus();
  });

  it('does not steal focus when loading completes after typing', async () => {
    const user = userEvent.setup();
    usePaginatedArtifactsResult.current.isInitialLoading = true;
    const view = renderPage();
    const input = screen.getByPlaceholderText('Filter by file name...');
    await user.type(input, 'j');

    usePaginatedArtifactsResult.current.isInitialLoading = false;
    usePaginatedArtifactsResult.current.items = [makeItem()];
    view.rerenderPage();
    expect(input).toHaveFocus();
    expect(input).toHaveValue('j');
    expect(screen.getByRole('treeitem', { name: 'report.md' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
  });

  it('focuses the selected file when initial loading completes', () => {
    usePaginatedArtifactsResult.current.isInitialLoading = true;
    const view = renderPage();
    usePaginatedArtifactsResult.current.isInitialLoading = false;
    usePaginatedArtifactsResult.current.items = [makeItem()];
    view.rerenderPage();
    expect(screen.getByRole('treeitem', { name: 'report.md' })).toHaveFocus();
  });

  it('preserves focus and collapsed branches after refresh and pagination', async () => {
    const user = userEvent.setup();
    usePaginatedArtifactsResult.current.items = [makeItem()];
    const view = renderPage();
    await user.keyboard('{ArrowLeft}{ArrowLeft}');
    expect(screen.getByRole('treeitem', { name: 'out' })).toHaveAttribute(
      'aria-expanded',
      'false'
    );

    usePaginatedArtifactsResult.current.items = [
      makeItem(),
      makeItem({ name: 'other', dagRunId: 'run-2' }),
    ];
    view.rerenderPage();
    expect(screen.getByRole('treeitem', { name: 'out' })).toHaveFocus();
    expect(screen.getByRole('treeitem', { name: 'out' })).toHaveAttribute(
      'aria-expanded',
      'false'
    );
    expect(screen.getByTestId('preview-pane')).toHaveTextContent(
      'out/report.md'
    );
    await user.keyboard('j');
    expect(screen.getByRole('treeitem', { name: 'other' })).toHaveFocus();
  });

  it('recovers focus to the parent when a file disappears', async () => {
    const user = userEvent.setup();
    usePaginatedArtifactsResult.current.items = [makeItem()];
    const view = renderPage();
    await user.keyboard('j');
    usePaginatedArtifactsResult.current.items = [
      makeItem({ files: [{ path: 'out/report.md', size: 42 }] }),
    ];
    view.rerenderPage();
    expect(screen.getByRole('treeitem', { name: 'out' })).toHaveFocus();
    expect(screen.getByTestId('preview-pane')).toHaveTextContent(
      'out/report.md'
    );
  });

  it('ignores modified and composing navigation keys', async () => {
    const user = userEvent.setup();
    usePaginatedArtifactsResult.current.items = [makeItem()];
    renderPage();
    await user.keyboard(
      '{Control>}j{/Control}{Alt>}{ArrowDown}{/Alt}{Shift>}K{/Shift}'
    );
    fireEvent.keyDown(document.activeElement!, { key: 'j', isComposing: true });
    expect(screen.getByRole('treeitem', { name: 'report.md' })).toHaveFocus();
  });

  it('clamps navigation at the first and last visible row', async () => {
    const user = userEvent.setup();
    usePaginatedArtifactsResult.current.items = [makeItem()];
    renderPage();

    await user.keyboard('{Home}k{ArrowUp}');
    expect(screen.getByRole('treeitem', { name: 'reporter' })).toHaveFocus();
    await user.keyboard('{End}j{ArrowDown}');
    expect(screen.getByRole('treeitem', { name: 'plot.png' })).toHaveFocus();
  });

  it('navigates in rendered order and preserves collapsed folders', async () => {
    const user = userEvent.setup();
    usePaginatedArtifactsResult.current.items = [
      makeItem({
        files: [
          { path: 'out/a.txt', size: 1 },
          { path: 'logs/b.txt', size: 2 },
          { path: 'out/c.txt', size: 3 },
        ],
      }),
    ];
    renderPage();

    await user.keyboard('j');
    expect(screen.getByRole('treeitem', { name: 'c.txt' })).toHaveFocus();
    await user.keyboard('j');
    const logs = screen.getByRole('treeitem', { name: 'logs' });
    expect(logs).toHaveFocus();
    expect(logs).toHaveAttribute('aria-expanded', 'false');
    await user.keyboard('{Enter}j');
    expect(screen.getByRole('treeitem', { name: 'b.txt' })).toHaveFocus();
    await user.keyboard('{ArrowLeft}{ArrowLeft}k');
    expect(screen.getByRole('treeitem', { name: 'c.txt' })).toHaveFocus();
    expect(logs).toHaveAttribute('aria-expanded', 'false');
  });

  it('ignores navigation keys from the filter bar', () => {
    usePaginatedArtifactsResult.current.items = [makeItem()];
    renderPage();

    fireEvent.keyDown(screen.getByRole('button', { name: 'Quick' }), {
      key: 'ArrowDown',
    });

    expect(
      screen.getByText('preview of out/report.md in reporter/run-1')
    ).toBeInTheDocument();
  });

  it('shows the real artifact path in file tooltips', () => {
    usePaginatedArtifactsResult.current.items = [makeItem()];
    renderPage();

    expect(
      screen.getByRole('treeitem', { name: /report\.md/ })
    ).toHaveAttribute('title', 'out/report.md');
    expect(screen.getByRole('treeitem', { name: /plot\.png/ })).toHaveAttribute(
      'title',
      'out/plot.png'
    );
  });

  it('ignores navigation keys typed into filter inputs', async () => {
    const user = userEvent.setup();
    usePaginatedArtifactsResult.current.items = [makeItem()];
    renderPage();

    const input = screen.getByPlaceholderText('Filter by DAG name...');
    await user.type(input, 'j');
    fireEvent.keyDown(input, { key: 'j' });

    expect(
      screen.getByText('preview of out/report.md in reporter/run-1')
    ).toBeInTheDocument();
  });

  it('applies the default artifact view to the first request', async () => {
    sharedArtifactViewState.views.push(
      makeArtifactView({
        isDefault: true,
        dagName: 'nightly-etl',
        fileName: '*.csv',
      })
    );

    renderPage();

    await waitFor(() => {
      expect(lastQuery()['name']).toBe('nightly-etl');
      expect(lastQuery()['fileName']).toBe('*.csv');
    });
    expect(
      screen.getByRole('button', { name: 'Artifact view: Nightly reports' })
    ).toBeVisible();
  });

  it('uses the bookmarked artifact view from the URL', async () => {
    sharedArtifactViewState.views.push(
      makeArtifactView({
        id: 'url-view',
        name: 'Weekly plots',
        dagName: 'plotter',
        fileName: '*.png',
      })
    );

    renderPage(vi.fn(), {}, '/artifacts?view=url-view');

    await waitFor(() => {
      expect(lastQuery()['name']).toBe('plotter');
      expect(lastQuery()['fileName']).toBe('*.png');
    });
    expect(
      screen.getByRole('button', { name: 'Artifact view: Weekly plots' })
    ).toBeVisible();
  });

  it('gives explicit URL filters precedence over the requested view', async () => {
    sharedArtifactViewState.views.push(
      makeArtifactView({ id: 'view-a', name: 'View A', dagName: 'nightly-etl' })
    );

    renderPage(vi.fn(), {}, '/artifacts?view=view-a&name=adhoc');

    await waitFor(() => {
      expect(lastQuery()['name']).toBe('adhoc');
    });
    expect(
      screen.getByRole('button', { name: 'Artifact view: View A' })
    ).toBeVisible();
  });

  it('restores the unfiltered list for the All artifacts view', async () => {
    sharedArtifactViewState.views.push(
      makeArtifactView({ isDefault: true, dagName: 'nightly-etl' })
    );

    renderPage(vi.fn(), {}, '/artifacts?view=all');

    await waitFor(() => {
      expect(lastQuery()['name']).toBeUndefined();
      expect(lastQuery()['fromDate']).toBeUndefined();
    });
  });

  it('derives fresh dates for a preset view instead of reusing stored ones', async () => {
    sharedArtifactViewState.views.push(
      makeArtifactView({
        id: 'last-7',
        datePreset: RunDatePreset.last7days,
        // A stale range left over from when the view was saved.
        fromDate: '2020-01-01T00:00',
        toDate: '2020-01-08T00:00',
      })
    );

    renderPage(vi.fn(), {}, '/artifacts?view=last-7');

    await waitFor(() => {
      expect(lastQuery()['fromDate']).toBeTypeOf('number');
    });
    expect(lastQuery()['fromDate']).toBeGreaterThan(
      dayjs('2020-01-01T00:00').unix()
    );
  });

  it('saves the current filters as an artifact view and applies it', async () => {
    const user = userEvent.setup();
    createArtifactViewMock.mockResolvedValue(
      makeArtifactView({ id: 'saved-view', name: 'Nightly reports' })
    );

    renderPage();

    fireEvent.change(screen.getByPlaceholderText('Filter by DAG name...'), {
      target: { value: 'nightly-etl' },
    });
    fireEvent.keyDown(screen.getByPlaceholderText('Filter by DAG name...'), {
      key: 'Enter',
    });
    await waitFor(() => {
      expect(locationSearchParams().get('name')).toBe('nightly-etl');
    });

    await user.click(
      screen.getByRole('button', { name: 'Artifact view: Custom view' })
    );
    await user.click(
      screen.getByRole('menuitem', { name: 'Save current filters as view…' })
    );
    await user.type(
      screen.getByRole('textbox', { name: 'Name' }),
      'Nightly reports'
    );
    await user.click(screen.getByRole('button', { name: 'Save view' }));

    await waitFor(() => {
      expect(createArtifactViewMock).toHaveBeenCalledWith(
        expect.objectContaining({
          type: ViewSpecType.artifact,
          name: 'Nightly reports',
          dagName: 'nightly-etl',
          intervalDays: 1,
        })
      );
    });
    await waitFor(() => {
      expect(locationSearchParams().get('view')).toBe('saved-view');
    });
  });

  // A link shared with a teammate must resolve to the same list for them, so
  // filters the URL does not name fall back to defaults, not to whatever the
  // reader's own session happens to hold.
  it('ignores stored session filters for a URL that carries filters', async () => {
    readSearchStateMock.mockReturnValue({
      searchText: 'somethingelse',
      fileName: '**/*.csv',
      fromDate: undefined,
      toDate: undefined,
      dateRangeMode: 'preset',
      datePreset: RunDatePreset.all,
    });

    renderPage(
      vi.fn(),
      {},
      '/artifacts?name=reporter&dateMode=preset&preset=all'
    );

    await waitFor(() => {
      expect(lastQuery()['name']).toBe('reporter');
    });
    expect(lastQuery()['fileName']).toBeUndefined();
    expect(screen.getByPlaceholderText('Filter by file name...')).toHaveValue(
      ''
    );
  });

  it('restores stored session filters for a bare URL', async () => {
    readSearchStateMock.mockReturnValue({
      searchText: 'reporter',
      fileName: '**/*.csv',
      fromDate: undefined,
      toDate: undefined,
      dateRangeMode: 'preset',
      datePreset: RunDatePreset.all,
    });

    renderPage();

    await waitFor(() => {
      expect(lastQuery()['name']).toBe('reporter');
    });
    expect(lastQuery()['fileName']).toBe('**/*.csv');
  });

  // Changing the preset rewrites the URL, so it has to carry the filters that
  // were restored from the session or they would be dropped on the next pass.
  it('keeps restored filters when only the date preset changes', async () => {
    const user = userEvent.setup();
    readSearchStateMock.mockReturnValue({
      searchText: 'reporter',
      fileName: '**/*.csv',
      fromDate: undefined,
      toDate: undefined,
      dateRangeMode: 'preset',
      datePreset: RunDatePreset.all,
    });

    renderPage();
    await waitFor(() => {
      expect(lastQuery()['fileName']).toBe('**/*.csv');
    });

    await user.click(screen.getByRole('combobox', { name: 'Date preset' }));
    await user.click(screen.getByRole('option', { name: 'Last 7 days' }));

    await waitFor(() => {
      expect(locationSearchParams().get('preset')).toBe('last7days');
    });
    expect(lastQuery()['name']).toBe('reporter');
    expect(lastQuery()['fileName']).toBe('**/*.csv');
  });

  // A link shared with a teammate must resolve to the same list for them, so
  // filters the URL does not name fall back to defaults, not to whatever the
  // reader's own session happens to hold.
  it('ignores stored session filters for a URL that carries filters', async () => {
    readSearchStateMock.mockReturnValue({
      searchText: 'somethingelse',
      fileName: '**/*.csv',
      fromDate: undefined,
      toDate: undefined,
      dateRangeMode: 'preset',
      datePreset: RunDatePreset.all,
    });

    renderPage(
      vi.fn(),
      {},
      '/artifacts?name=reporter&dateMode=preset&preset=all'
    );

    await waitFor(() => {
      expect(lastQuery()['name']).toBe('reporter');
    });
    expect(lastQuery()['fileName']).toBeUndefined();
    expect(screen.getByPlaceholderText('Filter by file name...')).toHaveValue(
      ''
    );
  });

  it('restores stored session filters for a bare URL', async () => {
    readSearchStateMock.mockReturnValue({
      searchText: 'reporter',
      fileName: '**/*.csv',
      fromDate: undefined,
      toDate: undefined,
      dateRangeMode: 'preset',
      datePreset: RunDatePreset.all,
    });

    renderPage();

    await waitFor(() => {
      expect(lastQuery()['name']).toBe('reporter');
    });
    expect(lastQuery()['fileName']).toBe('**/*.csv');
  });

  // Changing the preset rewrites the URL, so it has to carry the filters that
  // were restored from the session or they would be dropped on the next pass.
  it('keeps restored filters when only the date preset changes', async () => {
    const user = userEvent.setup();
    readSearchStateMock.mockReturnValue({
      searchText: 'reporter',
      fileName: '**/*.csv',
      fromDate: undefined,
      toDate: undefined,
      dateRangeMode: 'preset',
      datePreset: RunDatePreset.all,
    });

    renderPage();
    await waitFor(() => {
      expect(lastQuery()['fileName']).toBe('**/*.csv');
    });

    await user.click(screen.getByRole('combobox', { name: 'Date preset' }));
    await user.click(screen.getByRole('option', { name: 'Last 7 days' }));

    await waitFor(() => {
      expect(locationSearchParams().get('preset')).toBe('last7days');
    });
    expect(lastQuery()['name']).toBe('reporter');
    expect(lastQuery()['fileName']).toBe('**/*.csv');
  });

  // An explicit custom mode owns its range: dropping both bounds means no
  // bounds, not the ones the selected view happens to carry.
  it('does not restore a view date range for an explicit empty custom range', async () => {
    sharedArtifactViewState.views.push(
      makeArtifactView({
        id: 'custom-view',
        name: 'Custom range',
        dagName: 'reporter',
        dateMode: RunDateMode.custom,
        fromDate: '2026-09-01T00:00',
        toDate: '2026-09-30T23:59',
      })
    );

    renderPage(
      vi.fn(),
      {},
      '/artifacts?view=custom-view&name=reporter&dateMode=custom'
    );

    await waitFor(() => {
      expect(lastQuery()['name']).toBe('reporter');
    });
    expect(lastQuery()['fromDate']).toBeUndefined();
    expect(lastQuery()['toDate']).toBeUndefined();
  });

  it('keeps an explicit custom range from the URL', async () => {
    sharedArtifactViewState.views.push(
      makeArtifactView({
        id: 'custom-view',
        name: 'Custom range',
        dateMode: RunDateMode.custom,
        fromDate: '2026-09-01T00:00',
        toDate: '2026-09-30T23:59',
      })
    );

    renderPage(
      vi.fn(),
      {},
      '/artifacts?view=custom-view&dateMode=custom&fromDate=2026-08-01T00:00'
    );

    await waitFor(() => {
      expect(lastQuery()['fromDate']).toBe(dayjs('2026-08-01T00:00').unix());
    });
    expect(lastQuery()['toDate']).toBeUndefined();
  });

  // A preset range means "relative to now", so a session left open across a
  // date boundary must not keep querying the range it computed back then.
  it('recomputes a preset range restored from session state', async () => {
    const stale = dayjs().subtract(3, 'day').startOf('day');
    readSearchStateMock.mockReturnValue({
      searchText: '',
      fileName: '',
      fromDate: stale.format('YYYY-MM-DDTHH:mm'),
      toDate: undefined,
      dateRangeMode: 'preset',
      datePreset: RunDatePreset.today,
    });

    renderPage();

    await waitFor(() => {
      expect(lastQuery()['fromDate']).toBe(dayjs().startOf('day').unix());
    });
  });

  it('does not mark a view edited when a date bound is empty', async () => {
    sharedArtifactViewState.views.push(
      makeArtifactView({
        id: 'custom-view',
        name: 'Custom range',
        dagName: 'reporter',
        dateMode: RunDateMode.custom,
        fromDate: '2026-09-01T00:00',
        // An empty rather than absent bound, as a hand-edited record can hold.
        toDate: '',
      })
    );

    renderPage(
      vi.fn(),
      {},
      '/artifacts?view=custom-view&name=reporter&dateMode=custom&fromDate=2026-09-01T00:00'
    );

    await waitFor(() => {
      expect(lastQuery()['name']).toBe('reporter');
    });
    expect(screen.queryByText('Edited')).toBeNull();
  });

  it('marks an artifact view as edited when its filters change', async () => {
    sharedArtifactViewState.views.push(
      makeArtifactView({ id: 'view-a', dagName: 'nightly-etl' })
    );

    renderPage(vi.fn(), {}, '/artifacts?view=view-a');
    await waitFor(() => {
      expect(lastQuery()['name']).toBe('nightly-etl');
    });

    fireEvent.change(screen.getByPlaceholderText('Filter by DAG name...'), {
      target: { value: 'other' },
    });
    fireEvent.keyDown(screen.getByPlaceholderText('Filter by DAG name...'), {
      key: 'Enter',
    });

    await waitFor(() => {
      expect(screen.getByText('Edited')).toBeVisible();
    });
  });
});
