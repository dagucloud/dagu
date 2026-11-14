// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import NotFoundPage from '../not-found';

describe('NotFoundPage', () => {
  it('names the missing path and links back to the workflows', () => {
    const setTitle = vi.fn();

    render(
      <MemoryRouter initialEntries={['/no/such/page']}>
        <AppBarContext.Provider value={{ setTitle } as never}>
          <NotFoundPage />
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    expect(
      screen.getByRole('heading', { name: 'Page not found' })
    ).toBeVisible();
    expect(screen.getByText('/no/such/page')).toBeVisible();
    expect(
      screen.getByRole('link', { name: 'Go to Workflows' })
    ).toHaveAttribute('href', '/dags');
    expect(setTitle).toHaveBeenCalledWith('Page not found');
  });
});
