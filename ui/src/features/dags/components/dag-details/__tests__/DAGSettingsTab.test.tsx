// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { RuntimeProfileStatus } from '@/api/v1/schema';
import DAGSettingsTab from '../DAGSettingsTab';

const state = vi.hoisted(() => ({
  canManageProfiles: true,
  settings: { profile: '' } as { profile?: string },
}));

const putMock = vi.fn();
const deleteMock = vi.fn();
const mutateMock = vi.fn();

const profiles = {
  profiles: [
    { id: 'p-1', name: 'prod', status: RuntimeProfileStatus.active },
    { id: 'p-2', name: 'staging', status: RuntimeProfileStatus.active },
  ],
};

vi.mock('@/contexts/AuthContext', () => ({
  useCanManageProfiles: () => state.canManageProfiles,
  useIsAdmin: () => true,
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ PUT: putMock, DELETE: deleteMock }),
  useQuery: (path: string, init: unknown) => {
    if (init === null) return { data: undefined, isLoading: false };
    if (path === '/profiles') return { data: profiles, isLoading: false };
    return { data: state.settings, isLoading: false, mutate: mutateMock };
  },
}));

const params = {
  path: { fileName: 'example.yaml' },
  query: { remoteNode: 'local' },
};

beforeEach(() => {
  vi.clearAllMocks();
  state.canManageProfiles = true;
  state.settings = { profile: '' };
  putMock.mockResolvedValue({ data: {} });
  deleteMock.mockResolvedValue({ data: {} });
  mutateMock.mockResolvedValue(undefined);
});

// parity:test-settings-tab-untested
describe('DAGSettingsTab', () => {
  it('shows a read-only box to a user who cannot manage profiles', () => {
    state.canManageProfiles = false;
    state.settings = { profile: 'prod' };
    render(<DAGSettingsTab fileName="example.yaml" />);

    expect(screen.getByText('Default profile')).toBeVisible();
    expect(screen.getByText('prod')).toBeVisible();
    expect(screen.queryByRole('combobox')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Save' })).toBeNull();
  });

  it('saves a chosen profile with PUT', async () => {
    const user = userEvent.setup();
    render(<DAGSettingsTab fileName="example.yaml" />);

    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
    await user.click(screen.getByRole('combobox', { name: 'Default profile' }));
    await user.click(await screen.findByRole('option', { name: 'staging' }));
    await user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() =>
      expect(putMock).toHaveBeenCalledWith('/dags/{fileName}/settings', {
        params,
        body: { profile: 'staging' },
      })
    );
    expect(deleteMock).not.toHaveBeenCalled();
    expect(mutateMock).toHaveBeenCalled();
  });

  it('clears the profile with DELETE when No profile is saved', async () => {
    state.settings = { profile: 'prod' };
    const user = userEvent.setup();
    render(<DAGSettingsTab fileName="example.yaml" />);

    await user.click(screen.getByRole('combobox', { name: 'Default profile' }));
    await user.click(await screen.findByRole('option', { name: 'No profile' }));
    await user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() =>
      expect(deleteMock).toHaveBeenCalledWith('/dags/{fileName}/settings', {
        params,
      })
    );
    expect(putMock).not.toHaveBeenCalled();
  });

  // The Workflow panel keeps an unsaved choice in its leave guard.
  it('tells when the chosen profile differs from the saved one', async () => {
    const user = userEvent.setup();
    const onDirtyChange = vi.fn();
    render(
      <DAGSettingsTab fileName="example.yaml" onDirtyChange={onDirtyChange} />
    );
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);

    await user.click(screen.getByRole('combobox', { name: 'Default profile' }));
    await user.click(await screen.findByRole('option', { name: 'staging' }));
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);

    await user.click(screen.getByRole('button', { name: 'Reset' }));
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
  });
});
