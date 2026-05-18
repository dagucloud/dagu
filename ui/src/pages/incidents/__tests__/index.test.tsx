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
  it('renders connection and routing setup links in order', () => {
    const { setTitle } = renderPage();

    expect(screen.getByRole('heading', { name: /^incidents$/i })).toBeVisible();
    const connectionsLink = screen.getByRole('link', {
      name: /^connections/i,
    });
    const routingLink = screen.getByRole('link', { name: /^routing/i });
    expect(connectionsLink).toHaveAttribute('href', '/incident-providers');
    expect(routingLink).toHaveAttribute('href', '/incident-policies');
    expect(
      connectionsLink.compareDocumentPosition(routingLink) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
    expect(
      screen.getByText('Connect PagerDuty or SolarWinds Incident Response.')
    ).toBeVisible();
    expect(
      screen.getByText('Choose where Global and workspace incidents go.')
    ).toBeVisible();
    expect(setTitle).toHaveBeenCalledWith('Incidents');
  });
});
