// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import AdministrationPage from '..';

const config = {
  authMode: 'builtin',
  terminalEnabled: true,
} as Config;

function renderPage(configOverride: Partial<Config> = {}) {
  const setTitle = vi.fn();

  render(
    <MemoryRouter>
      <ConfigContext.Provider value={{ ...config, ...configOverride }}>
        <AppBarContext.Provider value={{ setTitle } as never}>
          <AdministrationPage />
        </AppBarContext.Provider>
      </ConfigContext.Provider>
    </MemoryRouter>
  );

  return { setTitle };
}

describe('AdministrationPage', () => {
  it('renders administration links by section', () => {
    const { setTitle } = renderPage();

    expect(
      screen.getByRole('heading', { name: /administration/i })
    ).toBeVisible();
    expect(screen.getByRole('link', { name: /users/i })).toHaveAttribute(
      'href',
      '/users'
    );
    expect(screen.getByRole('link', { name: /api keys/i })).toHaveAttribute(
      'href',
      '/api-keys'
    );
    expect(screen.getByRole('link', { name: /remote nodes/i })).toHaveAttribute(
      'href',
      '/remote-nodes'
    );
    expect(screen.getByText('Manage accounts and roles.')).toBeVisible();
    expect(
      screen.getByText('Issue access tokens for automation.')
    ).toBeVisible();
    expect(
      screen.getByText('Configure distributed execution targets.')
    ).toBeVisible();
    expect(screen.getByText('Open a server-side shell.')).toBeVisible();
    expect(
      screen.getByText('Review plan and entitlement status.')
    ).toBeVisible();
    expect(setTitle).toHaveBeenCalledWith('Administration');
  });
});
