// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { PageContextProvider } from '@/contexts/PageContext';
import { useBoundedDAGRunDetails } from '@/features/dag-runs/hooks/useBoundedDAGRunDetails';
import DAGRunDetailsPage from '..';

vi.mock('@/features/dag-runs/hooks/useBoundedDAGRunDetails', () => ({
  useBoundedDAGRunDetails: vi.fn(),
}));

vi.mock('@/features/dag-runs/components/dag-run-details', () => ({
  DAGRunDetailsContent: vi.fn(({ fillHeight }) => (
    <div data-fill-height={String(fillHeight)} data-testid="dag-run-content" />
  )),
}));

const useBoundedDAGRunDetailsMock = vi.mocked(useBoundedDAGRunDetails);

const dagRunDetails = {
  name: 'release-notes',
  dagRunId: 'run-1',
  artifactsAvailable: true,
} as never;

function renderPage() {
  render(
    <MemoryRouter initialEntries={['/dag-runs/release-notes/run-1']}>
      <AppBarContext.Provider
        value={
          {
            selectedRemoteNode: 'local',
            setContext: vi.fn(),
          } as never
        }
      >
        <PageContextProvider>
          <Routes>
            <Route
              path="/dag-runs/:name/:dagRunId"
              element={<DAGRunDetailsPage />}
            />
          </Routes>
        </PageContextProvider>
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

describe('DAGRunDetailsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useBoundedDAGRunDetailsMock.mockReturnValue({
      data: dagRunDetails,
      error: null,
      refresh: vi.fn(),
    } as never);
  });

  it('uses full-height layout for DAG run details content', () => {
    renderPage();

    const content = screen.getByTestId('dag-run-content');
    expect(content).toHaveAttribute('data-fill-height', 'true');
    expect(content.parentElement).toHaveClass('h-full');
    expect(content.parentElement).toHaveClass('min-h-0');
  });
});
