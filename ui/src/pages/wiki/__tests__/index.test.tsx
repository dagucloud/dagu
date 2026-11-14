// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import type { WorkspaceSelection } from '@/lib/workspace';
import WikiPage from '..';

const testState = vi.hoisted(() => ({
  post: vi.fn(),
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ POST: testState.post }),
  useQuery: () => ({
    data: { tree: [] },
    error: undefined,
    isLoading: false,
    mutate: vi.fn(),
  }),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useAuth: () => ({ user: null }),
  useCanWrite: () => true,
  useCanWriteForWorkspace: () => true,
}));

vi.mock('@/contexts/UserPreference', () => ({
  useUserPreferences: () => ({
    preferences: { wikiSortField: 'name', wikiSortOrder: 'asc' },
    updatePreference: vi.fn(),
  }),
}));

vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: vi.fn() }),
}));

vi.mock('@/hooks/useWikiTreeSSE', () => ({
  useWikiTreeSSE: () => ({}),
}));

vi.mock('@/hooks/useSSECacheSync', () => ({
  sseFallbackOptions: () => ({}),
  useSSECacheSync: () => undefined,
}));

vi.mock('@/features/cockpit/hooks/useCockpitState', () => ({
  useCockpitState: () => ({ selectedTemplate: null, selectTemplate: vi.fn() }),
}));

vi.mock('@/features/cockpit/components/CockpitToolbar', () => ({
  CockpitToolbar: () => null,
}));

vi.mock('../components/WikiPageTabEditorPanel', () => ({
  default: () => null,
}));

vi.mock('../hooks/useWikiPageTemplates', () => ({
  useWikiPageTemplates: () => [],
  useResolveTemplateContent: () => async () => '',
}));

function renderWiki(workspaceSelection: WorkspaceSelection) {
  render(
    <MemoryRouter initialEntries={['/wiki']}>
      <AppBarContext.Provider
        value={
          {
            setTitle: vi.fn(),
            selectedRemoteNode: 'local',
            workspaceSelection,
          } as never
        }
      >
        <WikiPage />
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

describe('WikiPage', () => {
  beforeEach(() => {
    testState.post.mockReset();
    testState.post.mockResolvedValue({ error: undefined });
  });

  // Most installations never create a workspace, so the aggregate view is
  // where they land and must still allow creating pages.
  it('creates root pages in the default Wiki from the all view', async () => {
    const user = userEvent.setup();
    renderWiki({ kind: 'all' });

    await user.click(
      screen.getByRole('button', { name: 'Create your first Wiki page' })
    );
    await user.type(screen.getByLabelText('Path'), 'runbook');
    await user.click(screen.getByRole('button', { name: /create/i }));

    expect(testState.post).toHaveBeenCalledWith('/wiki', {
      params: { query: { remoteNode: 'local', workspace: 'default' } },
      body: { id: 'runbook', content: '' },
    });
  });

  it('creates root pages in the selected workspace', async () => {
    const user = userEvent.setup();
    renderWiki({ kind: 'workspace', workspace: 'ops' });

    await user.click(
      screen.getByRole('button', { name: 'Create your first Wiki page' })
    );
    await user.type(screen.getByLabelText('Path'), 'runbook');
    await user.click(screen.getByRole('button', { name: /create/i }));

    expect(testState.post).toHaveBeenCalledWith('/wiki', {
      params: { query: { remoteNode: 'local', workspace: 'ops' } },
      body: { id: 'runbook', content: '' },
    });
  });
});
