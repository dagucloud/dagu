// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { RuntimeProfileEntryKind, RuntimeProfileStatus } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import ProfilesPage from '..';

const mutateMock = vi.fn();

vi.mock('@/contexts/AuthContext', () => ({
  useCanManageProfiles: () => true,
  useCanManageSecrets: () => true,
  useCanWriteForWorkspace: () => false,
  useIsAdmin: () => false,
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({
    DELETE: vi.fn(),
    PATCH: vi.fn(),
    POST: vi.fn(),
    PUT: vi.fn(),
  }),
  useQuery: vi.fn(),
}));

const useQueryMock = useQuery as unknown as {
  mockImplementation: (fn: (path: string) => unknown) => void;
};

function renderPage(initialEntry = '/profiles') {
  render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <AppBarContext.Provider
        value={
          {
            setTitle: vi.fn(),
            selectedRemoteNode: 'local',
          } as never
        }
      >
        <ProfilesPage />
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

describe('ProfilesPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useQueryMock.mockImplementation((path: string) => {
      if (path === '/profiles') {
        return {
          data: {
            profiles: [
              {
                id: 'prod-id',
                name: 'prod',
                status: RuntimeProfileStatus.active,
                protected: true,
                description: 'Production credentials',
                entries: [
                  {
                    key: 'API_TOKEN',
                    kind: RuntimeProfileEntryKind.secret,
                    createdAt: '2026-01-01T00:00:00Z',
                    updatedAt: '2026-01-01T00:00:00Z',
                  },
                ],
                createdAt: '2026-01-01T00:00:00Z',
                updatedAt: '2026-01-01T00:00:00Z',
              },
            ],
          },
          mutate: mutateMock,
          isLoading: false,
        };
      }

      return {
        data: undefined,
        mutate: vi.fn(),
        isLoading: false,
      };
    });
  });

  it('switches between separate profile and secret ref tabs', async () => {
    const user = userEvent.setup();
    renderPage();

    expect(
      screen.getByRole('heading', { name: 'Profiles & Secrets', level: 1 })
    ).toBeVisible();
    expect(screen.getByRole('tab', { name: 'Profiles' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(
      screen.getByRole('heading', { name: 'Profiles', level: 2 })
    ).toBeVisible();
    expect(
      screen.queryByRole('heading', { name: 'DAG Secret Refs', level: 2 })
    ).not.toBeInTheDocument();

    await user.click(screen.getByRole('tab', { name: 'DAG Secret Refs' }));

    expect(
      screen.getByRole('heading', { name: 'DAG Secret Refs', level: 2 })
    ).toBeVisible();
    expect(
      screen.getByRole('tab', { name: 'DAG Secret Refs' })
    ).toHaveAttribute('aria-selected', 'true');
    expect(
      screen.queryByRole('heading', { name: 'Profiles', level: 2 })
    ).not.toBeInTheDocument();
  });

  it('disables protected profile mutations for non-admin managers', async () => {
    const user = userEvent.setup();
    renderPage();

    expect(screen.getByText('prod')).toBeVisible();
    expect(screen.getByText('Protected')).toBeVisible();
    expect(screen.getByText('Admin')).toBeVisible();

    expect(screen.getByRole('button', { name: 'Variable' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Secret' })).toBeDisabled();
    expect(
      screen.getByRole('button', { name: 'Edit API_TOKEN' })
    ).toBeDisabled();
    expect(
      screen.getByRole('button', { name: 'Delete API_TOKEN' })
    ).toBeDisabled();
    expect(
      screen.getByRole('button', { name: 'Actions for prod' })
    ).toBeDisabled();

    await user.click(screen.getByRole('button', { name: 'Add Profile' }));

    expect(screen.getByRole('checkbox', { name: 'Protected' })).toBeDisabled();
  });
});
