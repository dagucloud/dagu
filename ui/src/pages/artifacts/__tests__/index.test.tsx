// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
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
});

afterEach(() => {
  vi.restoreAllMocks();
});

function renderPage(
  setTitle = vi.fn(),
  configOverrides: Partial<Config> = {}
): void {
  render(
    <MemoryRouter>
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

  it('lists runs with their artifact files and previews the first file', () => {
    usePaginatedArtifactsResult.current.items = [
      makeItem(),
      makeItem({ name: 'ingest', dagRunId: 'run-2', files: [] }),
    ];
    renderPage();

    expect(screen.getByRole('button', { name: /reporter/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /ingest/ })).toBeInTheDocument();
    // The newest run with files opens automatically; its first file is
    // selected so the preview pane shows something immediately.
    expect(screen.getByRole('button', { name: /report\.md/ })).toBeInTheDocument();
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

    fireEvent.click(screen.getByRole('button', { name: /plot\.png/ }));

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
      screen.queryByRole('button', { name: /raw\.json/ })
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /oldest/ }));

    expect(
      screen.getByRole('button', { name: /raw\.json/ })
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
});