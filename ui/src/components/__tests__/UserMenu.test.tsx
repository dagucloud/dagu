// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { UserAuthProvider, UserRole } from '@/api/v1/schema';
import { UserMenu } from '../UserMenu';

const mocks = vi.hoisted(() => ({
  useAuth: vi.fn(),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useAuth: mocks.useAuth,
}));

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ authMode: 'builtin' }),
}));

function renderMenu(authProvider: UserAuthProvider) {
  mocks.useAuth.mockReturnValue({
    user: {
      id: 'user-id',
      username: 'test-user',
      role: UserRole.viewer,
      workspaceAccess: { all: true, grants: [] },
      authProvider,
      createdAt: '2026-07-20T00:00:00Z',
      updatedAt: '2026-07-20T00:00:00Z',
    },
    logout: vi.fn(),
    isAuthenticated: true,
  });

  render(
    <MemoryRouter>
      <UserMenu />
    </MemoryRouter>
  );
}

describe('UserMenu', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('does not offer password changes to OIDC users', async () => {
    const user = userEvent.setup();
    renderMenu(UserAuthProvider.oidc);

    await user.click(screen.getByRole('button', { name: 'test-user' }));

    expect(
      screen.queryByRole('menuitem', { name: 'Change Password' })
    ).not.toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Sign Out' })).toBeVisible();
  });

  it('offers password changes to local users', async () => {
    const user = userEvent.setup();
    renderMenu(UserAuthProvider.builtin);

    await user.click(screen.getByRole('button', { name: 'test-user' }));

    expect(
      screen.getByRole('menuitem', { name: 'Change Password' })
    ).toBeVisible();
  });
});
