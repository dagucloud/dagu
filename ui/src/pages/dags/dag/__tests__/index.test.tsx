// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { PageContextProvider } from '@/contexts/PageContext';
import { useQuery } from '@/hooks/api';
import { WorkspaceKind } from '@/lib/workspace';
import DAGDetails from '..';

vi.mock('@/features/dags/components/dag-details', () => ({
  DAGHeader: () => null,
  DAGDetailsContent: vi.fn(({ fillHeight }) => (
    <div data-fill-height={String(fillHeight)} data-testid="dag-details-content" />
  )),
}));

vi.mock('@/hooks/api', () => ({
  useQuery: vi.fn(),
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
  workspaceSelection: { kind: WorkspaceKind.all },
} as never;

const dagData = {
  dag: {
    name: 'release-notes',
    artifacts: { enabled: true },
  },
  filePath: '/tmp/release-notes.yaml',
  latestDAGRun: {
    name: 'release-notes',
    dagRunId: 'run-1',
    artifactsAvailable: true,
  },
  localDags: [],
};

function renderPage() {
  render(
    <MemoryRouter initialEntries={['/dags/release-notes']}>
      <AppBarContext.Provider value={appBarValue}>
        <PageContextProvider>
          <Routes>
            <Route path="/dags/:fileName" element={<DAGDetails />} />
          </Routes>
        </PageContextProvider>
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
});
