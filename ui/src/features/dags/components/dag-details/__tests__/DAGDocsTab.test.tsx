// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';

import { AppBarContext } from '@/contexts/AppBarContext';
import DAGDocsTab from '../DAGDocsTab';

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ permissions: { writeDags: false } }),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteForWorkspace: () => false,
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ POST: vi.fn() }),
  useQuery: () => ({ data: { items: [] }, mutate: vi.fn() }),
}));

vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: vi.fn() }),
}));

vi.mock('@/pages/docs/components/CreateDocModal', () => ({
  CreateDocModal: () => null,
}));

describe('DAGDocsTab', () => {
  it('shows the current DAG wikilink in the empty state', () => {
    render(
      <MemoryRouter>
        <AppBarContext.Provider value={{ selectedRemoteNode: 'local' } as never}>
          <DAGDocsTab dagName="nightly-etl" />
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    expect(screen.getByText('[[dag:nightly-etl]]')).toBeInTheDocument();
  });
});
