// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { WorkspaceKind } from '@/lib/workspace';
import DAGRuns from '..';

const { readSearchStateMock, searchStateMock, writeSearchStateMock } =
  vi.hoisted(() => {
    const readState = vi.fn(() => null);
    const writeState = vi.fn();
    return {
      readSearchStateMock: readState,
      searchStateMock: { readState, writeState },
      writeSearchStateMock: writeState,
    };
  });

vi.mock('@/contexts/SearchStateContext', () => ({
  useSearchState: () => searchStateMock,
}));

vi.mock('@/contexts/UserPreference', () => ({
  useUserPreferences: () => ({
    preferences: {
      dagRunsViewMode: 'list',
    },
    updatePreference: vi.fn(),
  }),
}));

vi.mock('@/hooks/api', () => ({
  useQuery: () => ({
    data: { labels: [] },
  }),
}));

vi.mock('@/features/dag-runs/hooks/dagRunPagination', () => ({
  usePaginatedDAGRuns: () => ({
    dagRuns: [],
    isLoadingMore: false,
    loadMoreError: null,
    hasMore: false,
    refresh: vi.fn(),
    loadMore: vi.fn(),
  }),
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

vi.mock(
  '@/features/dag-runs/components/dag-run-list/DAGRunGroupedView',
  () => ({
    default: () => <div>Grouped Runs</div>,
  })
);

vi.mock('@/features/dag-runs/components/dag-run-list/DAGRunTable', () => ({
  default: ({
    onSelectDAGRun,
    onViewArtifacts,
  }: {
    onSelectDAGRun: (run: { name: string; dagRunId: string }) => void;
    onViewArtifacts: (run: { name: string; dagRunId: string }) => void;
  }) => (
    <div>
      <div>Run Table</div>
      <button
        type="button"
        onClick={() => onSelectDAGRun({ name: 'demo', dagRunId: 'run-1' })}
      >
        Open run
      </button>
      <button
        type="button"
        onClick={() => onViewArtifacts({ name: 'demo', dagRunId: 'run-1' })}
      >
        Open artifacts
      </button>
    </div>
  ),
}));

const config = {
  tzOffsetInSec: undefined,
} as Config;

beforeEach(() => {
  readSearchStateMock.mockReset();
  readSearchStateMock.mockReturnValue(null);
  writeSearchStateMock.mockReset();
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

function LocationProbe(): React.JSX.Element {
  const location = useLocation();
  return <output data-testid="location-search">{location.search}</output>;
}

function renderPage(setTitle = vi.fn(), initialEntry = '/dag-runs'): void {
  render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <LocationProbe />
      <ConfigContext.Provider value={config}>
        <AppBarContext.Provider
          value={
            {
              setTitle,
              selectedRemoteNode: 'local',
              workspaceSelection: { kind: WorkspaceKind.all },
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
  it('uses the Executions page title', () => {
    const setTitle = vi.fn();

    renderPage(setTitle);

    expect(
      screen.getByRole('heading', { name: /^executions$/i })
    ).toBeVisible();
    expect(screen.queryByRole('heading', { name: /dag runs/i })).toBeNull();
    expect(setTitle).toHaveBeenCalledWith('Executions');
  });

  it('uses consistent filter control sizing', () => {
    renderPage();

    expect(
      screen.getByPlaceholderText('Filter by DAG name...').className
    ).toContain('h-9');
    expect(
      screen.getByPlaceholderText('Filter by Run ID...').className
    ).toContain('h-9');
    expect(
      screen.getByRole('combobox', { name: 'Status' }).className
    ).toContain('h-9');
    expect(
      screen.getByRole('combobox', { name: 'Date preset' }).className
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

  it('stores the selected run in the URL and clears it on close', () => {
    renderPage();

    fireEvent.click(screen.getByRole('button', { name: 'Open run' }));
    expect(screen.getByRole('dialog')).toHaveTextContent(
      'Run modal for demo/run-1 on status'
    );
    expect(screen.getByTestId('location-search')).toHaveTextContent(
      '?selectedRunName=demo&selectedRunId=run-1'
    );

    fireEvent.click(screen.getByRole('button', { name: 'Close run' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByTestId('location-search')).toHaveTextContent('');
  });

  it('opens a run on the artifacts tab and stores that selection in the URL', () => {
    renderPage();

    fireEvent.click(screen.getByRole('button', { name: 'Open artifacts' }));

    expect(screen.getByRole('dialog')).toHaveTextContent(
      'Run modal for demo/run-1 on artifacts'
    );
    expect(screen.getByTestId('location-search')).toHaveTextContent(
      '?selectedRunName=demo&selectedRunId=run-1&selectedRunTab=artifacts'
    );
  });

  it('preserves execution filters when opening a run', async () => {
    renderPage();

    fireEvent.change(screen.getByPlaceholderText('Filter by DAG name...'), {
      target: { value: 'deploy' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));

    await waitFor(() => {
      const search = screen.getByTestId('location-search').textContent ?? '';
      expect(new URLSearchParams(search).get('name')).toBe('deploy');
    });

    fireEvent.click(screen.getByRole('button', { name: 'Open run' }));

    const search = screen.getByTestId('location-search').textContent ?? '';
    expect(new URLSearchParams(search).get('name')).toBe('deploy');
    expect(new URLSearchParams(search).get('selectedRunName')).toBe('demo');
    expect(new URLSearchParams(search).get('selectedRunId')).toBe('run-1');
  });

  it('restores the run and artifact tab from the URL', () => {
    renderPage(
      vi.fn(),
      '/dag-runs?selectedRunName=demo&selectedRunId=run-1&selectedRunTab=artifacts'
    );

    expect(screen.getByRole('dialog')).toHaveTextContent(
      'Run modal for demo/run-1 on artifacts'
    );
  });
});
