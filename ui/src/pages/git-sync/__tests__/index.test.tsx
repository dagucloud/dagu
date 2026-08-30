// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { SyncItemKind, SyncStatus, SyncSummary } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  mutateStatus: vi.fn(),
  post: vi.fn(),
  showError: vi.fn(),
  showToast: vi.fn(),
  useQuery: vi.fn(),
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ GET: vi.fn(), POST: mocks.post }),
  useQuery: mocks.useQuery,
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanWrite: () => true,
}));

vi.mock('@/contexts/UserPreference', () => ({
  useUserPreferences: () => ({ preferences: { theme: 'light' } }),
}));

vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: mocks.showError }),
}));

vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: mocks.showToast }),
}));

import GitSyncPage from '..';

const itemID = 'scripts/a,b.sh';

function renderPage() {
  render(
    <MemoryRouter initialEntries={['/?type=file']}>
      <AppBarContext.Provider
        value={{ selectedRemoteNode: 'local', setTitle: vi.fn() } as never}
      >
        <GitSyncPage />
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.post.mockResolvedValue({ data: { synced: [itemID] } });
  const statusQuery = {
    data: {
      enabled: true,
      repository: 'repo',
      branch: 'main',
      summary: SyncSummary.pending,
      items: [
        {
          itemId: itemID,
          filePath: itemID,
          displayName: itemID,
          status: SyncStatus.modified,
          kind: SyncItemKind.file,
        },
      ],
      counts: {
        synced: 0,
        modified: 1,
        untracked: 0,
        conflict: 0,
        missing: 0,
      },
    },
    mutate: mocks.mutateStatus,
  };
  const configQuery = {
    data: {
      enabled: true,
      pushEnabled: true,
    },
  };
  mocks.useQuery.mockImplementation((path: string) =>
    path === '/sync/status' ? statusQuery : configQuery
  );
});

describe('GitSyncPage', () => {
  it('batch publishes comma-containing item IDs unchanged', async () => {
    const user = userEvent.setup();
    renderPage();

    const checkbox = screen.getByRole('checkbox', {
      name: `Select ${itemID}`,
    });
    await waitFor(() => expect(checkbox).toBeChecked());

    await user.click(screen.getByTitle('Publish 1 selected'));
    await user.click(screen.getByRole('button', { name: 'Publish' }));

    await waitFor(() =>
      expect(mocks.post).toHaveBeenCalledWith('/sync/publish-all', {
        params: { query: { remoteNode: 'local' } },
        body: { message: 'Batch update', itemIds: [itemID] },
      })
    );
  });
});
