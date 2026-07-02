// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import IntegrationsPage from '..';

describe('IntegrationsPage', () => {
  it('renders integration links', () => {
    const setTitle = vi.fn();

    render(
      <MemoryRouter>
        <AppBarContext.Provider value={{ setTitle } as never}>
          <IntegrationsPage />
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    expect(screen.getByRole('heading', { name: /integrations/i })).toBeVisible();
    expect(screen.getByRole('link', { name: /webhooks/i })).toHaveAttribute(
      'href',
      '/webhooks'
    );
    expect(screen.getByRole('link', { name: /api docs/i })).toHaveAttribute(
      'href',
      '/api-docs'
    );
    expect(
      screen.getByText('Trigger workflows from external systems.')
    ).toBeVisible();
    expect(
      screen.getByText('Explore authenticated REST API endpoints.')
    ).toBeVisible();
    expect(setTitle).toHaveBeenCalledWith('Integrations');
  });
});
