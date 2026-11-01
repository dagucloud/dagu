// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ViewPage from '..';
import { useViews } from '@/hooks/useViews';
import { AppBarContext } from '@/contexts/AppBarContext';

vi.mock('@/hooks/useViews', () => ({ useViews: vi.fn() }));

vi.mock('@/contexts/AuthContext', () => ({
  useCanWrite: () => true,
  useCanWriteForWorkspace: () => true,
}));

vi.mock('@/features/views/ViewEditorDialog', () => ({
  ViewEditorDialog: () => null,
}));

vi.mock('@/features/views/ViewPanel', () => ({
  ViewPanel: ({ view }: { view: { name: string } }) => (
    <div>View panel: {view.name}</div>
  ),
}));

vi.mock('@/features/peek/WorkflowPeek', async () => {
  const { DialogTitle } = await import('@/components/ui/dialog');
  return {
    WorkflowPeek: ({ target }: { target: { dag: string } }) => (
      <DialogTitle>{target.dag}</DialogTitle>
    ),
  };
});

const useViewsMock = vi.mocked(useViews);

function mockViews(views: Array<{ id: string; name: string }>) {
  useViewsMock.mockReturnValue({
    views,
    isLoading: false,
    error: null,
    createView: vi.fn(),
    updateView: vi.fn(),
    deleteView: vi.fn(),
    refresh: vi.fn(),
  } as unknown as ReturnType<typeof useViews>);
}

function renderAt(viewId: string, setTitle = vi.fn()) {
  render(
    <MemoryRouter initialEntries={[`/views/${viewId}`]}>
      <AppBarContext.Provider value={{ setTitle } as never}>
        <Routes>
          <Route path="/views/:viewId" element={<ViewPage />} />
          <Route path="/" element={<div>home</div>} />
        </Routes>
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

afterEach(() => {
  vi.clearAllMocks();
});

beforeEach(() => {
  mockViews([{ id: 'v1', name: 'My View' }]);
});

describe('ViewPage', () => {
  it('renders the view board with no Overview tabs', () => {
    renderAt('v1');

    expect(screen.getByRole('heading', { name: 'My View' })).toBeInTheDocument();
    expect(screen.getByText('View panel: My View')).toBeInTheDocument();
    expect(screen.queryByRole('tab')).not.toBeInTheDocument();
  });

  it('shows the peek of a ?peek link over the board', () => {
    renderAt('v1?peek=etl&peekRun=run-1');

    expect(screen.getByRole('dialog', { name: 'etl' })).toBeInTheDocument();
  });

  it('explains when the view is missing', () => {
    mockViews([]);
    renderAt('ghost');

    expect(
      screen.getByRole('heading', { name: 'View not found' })
    ).toBeInTheDocument();
    expect(
      screen.getByRole('link', { name: 'Go to Overview' })
    ).toHaveAttribute('href', '/');
  });

  it('names the browser tab after the view', () => {
    const setTitle = vi.fn();
    renderAt('v1', setTitle);

    expect(setTitle).toHaveBeenCalledWith('My View');
  });

  it('keeps a neutral tab title while views load', () => {
    useViewsMock.mockReturnValue({
      ...useViewsMock(),
      views: [],
      isLoading: true,
    });
    const setTitle = vi.fn();
    renderAt('v1', setTitle);

    expect(setTitle).toHaveBeenCalledWith('');
    expect(setTitle).not.toHaveBeenCalledWith('View not found');
  });
});
