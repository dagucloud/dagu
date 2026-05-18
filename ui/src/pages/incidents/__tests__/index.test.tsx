// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';

import { AppBarContext } from '@/contexts/AppBarContext';

vi.hoisted(() => {
  vi.stubGlobal('getConfig', () => ({
    apiURL: '/api/v1',
    authMode: 'builtin',
  }));
});

import IncidentsPage from '..';

function renderPage() {
  const setTitle = vi.fn();

  render(
    <MemoryRouter>
      <AppBarContext.Provider value={{ setTitle } as never}>
        <IncidentsPage />
      </AppBarContext.Provider>
    </MemoryRouter>
  );

  return { setTitle };
}

describe('IncidentsPage', () => {
  it('renders provider and policy setup links in order', () => {
    const { setTitle } = renderPage();

    expect(screen.getByRole('heading', { name: /^incidents$/i })).toBeVisible();
    const providersLink = screen.getByRole('link', { name: /^providers/i });
    const policiesLink = screen.getByRole('link', { name: /^policies/i });
    expect(providersLink).toHaveAttribute('href', '/incident-providers');
    expect(policiesLink).toHaveAttribute('href', '/incident-policies');
    expect(
      providersLink.compareDocumentPosition(policiesLink) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
    expect(
      screen.getByText(
        'Connect PagerDuty or SolarWinds Incident Response credentials.'
      )
    ).toBeVisible();
    expect(
      screen.getByText('Set Global defaults and workspace incident overrides.')
    ).toBeVisible();
    expect(setTitle).toHaveBeenCalledWith('Incidents');
  });
});
