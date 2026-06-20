// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ViewPage from '..';
import { useViews } from '@/hooks/useViews';

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

function renderAt(viewId: string) {
  render(
    <MemoryRouter initialEntries={[`/views/${viewId}`]}>
      <Routes>
        <Route path="/views/:viewId" element={<ViewPage />} />
        <Route path="/" element={<div>home</div>} />
      </Routes>
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

  it('redirects home when the view is missing', async () => {
    mockViews([]);
    renderAt('ghost');

    await waitFor(() => expect(screen.getByText('home')).toBeInTheDocument());
  });
});
