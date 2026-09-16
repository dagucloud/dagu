// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import dayjs from 'dayjs';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import type { ArtifactListItem } from '@/features/artifacts/hooks/artifactListPagination';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { WorkspaceKind } from '@/lib/workspace';
import Artifacts from '..';

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

vi.mock('@/features/dag-runs/components/dag-run-details', () => ({
  DAGRunDetailsModal: ({
    name,
    dagRunId,
    initialTab,
    onClose,
  }: {
    name: string;
    dagRunId: string;
    initialTab: string;
    onClose: () => void;
  }) => (
    <div role="dialog">
      Run modal for {name}/{dagRunId} on {initialTab}
      <button type="button" onClick={onClose}>
        Close run
      </button>
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
    files: [{ path: 'out/report.md', size: 42 }],
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
});

afterEach(() => {
  vi.restoreAllMocks();
});

function LocationProbe(): React.JSX.Element {
  const location = useLocation();
  return <output data-testid="location-search">{location.search}</output>;
}

function renderPage(
  setTitle = vi.fn(),
  initialEntry = '/artifacts',
  configOverrides: Partial<Config> = {}
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
              workspaceSelection: { kind: WorkspaceKind.all },
            } as never
          }
        >
          <Artifacts />
        </AppBarContext.Provider>
      </ConfigContext.Provider>
    </MemoryRouter>
  );
}

function lastQuery(): Record<string, unknown> {
  const calls = usePaginatedArtifactsMock.mock.calls;
  return calls[calls.length - 1]?.[0]?.query ?? {};
}

function expectRunModal(name: string, dagRunId: string): void {
  expect(
    screen.getByText(`Run modal for ${name}/${dagRunId} on artifacts`)
  ).toBeInTheDocument();
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

  it('bounds the initial query to the default Today preset', () => {
    renderPage();

    expect(lastQuery().fromDate).toBe(
      dayjs(`${dayjs().format('YYYY-MM-DD')}T00:00:00`).unix()
    );
  });

  it('interprets custom dates in the configured timezone', async () => {
    const user = userEvent.setup();
    renderPage(vi.fn(), '/artifacts', { tzOffsetInSec: -5 * 60 * 60 });

    fireEvent.click(screen.getByRole('button', { name: 'Custom' }));
    const inputs = await screen.findAllByPlaceholderText(
      'YYYY-MM-DD HH:mm:ss'
    );
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

  it('shows an API error instead of an empty state', () => {
    usePaginatedArtifactsResult.current.error = new Error(
      'Invalid file name pattern'
    );
    renderPage();

    expect(screen.getByText('Invalid file name pattern')).toBeInTheDocument();
    expect(screen.queryByText('No artifacts found')).not.toBeInTheDocument();
  });

  it('opens the run in a new tab under the base path', () => {
    const openMock = vi.spyOn(window, 'open').mockImplementation(() => null);
    usePaginatedArtifactsResult.current.items = [makeItem()];
    renderPage(vi.fn(), '/artifacts', { basePath: '/dagu' });

    const nameLink = screen.getByRole('link', { name: 'reporter' });
    fireEvent.click(nameLink.closest('tr') ?? nameLink, { metaKey: true });

    expect(openMock).toHaveBeenCalledWith(
      '/dagu/dag-runs/reporter/run-1',
      '_blank'
    );
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

  it('applies the custom date range when Enter is pressed', async () => {
    const user = userEvent.setup();
    renderPage();

    fireEvent.click(screen.getByRole('button', { name: 'Custom' }));
    const inputs = await screen.findAllByPlaceholderText(
      'YYYY-MM-DD HH:mm:ss'
    );
    const fromInput = inputs[0]!;
    const toInput = inputs[1]!;
    await user.clear(fromInput);
    await user.type(fromInput, '2026-09-01 00:00:00');
    await user.clear(toInput);
    await user.type(toInput, '2026-09-15 00:00:00');
    fireEvent.keyDown(fromInput, { key: 'Enter' });

    const query = lastQuery();
    expect(query.fromDate).toBe(dayjs('2026-09-01T00:00:00').unix());
    expect(query.toDate).toBe(dayjs('2026-09-15T00:00:00').unix());
  });

  it('renders a run row with its artifact files', () => {
    usePaginatedArtifactsResult.current.items = [
      makeItem({
        files: [
          { path: 'out/report.md', size: 42 },
          { path: 'out/plot.png', size: 1024 },
        ],
      }),
      makeItem({ name: 'ingest', dagRunId: 'run-2', files: [] }),
    ];
    renderPage();

    expect(screen.getByRole('link', { name: 'reporter' })).toHaveAttribute(
      'href',
      '/dag-runs/reporter/run-1'
    );
    expect(screen.getByText('out/report.md')).toBeInTheDocument();
    expect(screen.getAllByText('No files').length).toBe(1);
  });

  it('marks runs whose file lists were truncated', () => {
    usePaginatedArtifactsResult.current.items = [
      makeItem({ filesTruncated: true }),
    ];
    renderPage();

    expect(screen.getByText('truncated')).toBeInTheDocument();
  });

  it('shows an empty state when no runs produced artifacts', () => {
    renderPage();

    expect(screen.getByText('No artifacts found')).toBeInTheDocument();
  });

  it('opens the run details modal on the artifacts tab', () => {
    usePaginatedArtifactsResult.current.items = [makeItem()];
    renderPage();

    const nameLink = screen.getByRole('link', { name: 'reporter' });
    fireEvent.click(nameLink.closest('tr') ?? nameLink);

    expectRunModal('reporter', 'run-1');
  });

  it('restores the selected run from the URL', () => {
    renderPage(
      vi.fn(),
      '/artifacts?selectedRunName=reporter&selectedRunId=run-1&selectedRunTab=artifacts'
    );

    expectRunModal('reporter', 'run-1');
  });

  it('shows a load more button when a next cursor exists', () => {
    usePaginatedArtifactsResult.current.items = [makeItem()];
    usePaginatedArtifactsResult.current.hasMore = true;
    renderPage();

    expect(
      screen.getByRole('button', { name: 'Load more' })
    ).toBeInTheDocument();
  });
});