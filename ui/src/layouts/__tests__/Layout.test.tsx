// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import Layout from '../Layout';

vi.mock('@/components/LicenseBanner', () => ({
  LicenseBanner: () => null,
}));

vi.mock('@/components/UpdateBanner', () => ({
  UpdateBanner: () => null,
}));

vi.mock('../../menu', () => ({
  mainListItems: () => <div data-testid="sidebar-menu" />,
}));

const config = {
  title: 'Dagu',
  navbarColor: '',
} as Config;

function renderLayout(path: string, configOverride?: Partial<Config>) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <ConfigContext.Provider value={{ ...config, ...configOverride }}>
        <Layout>
          <div>Page Content</div>
        </Layout>
      </ConfigContext.Provider>
    </MemoryRouter>
  );
}

describe('Layout', () => {
  beforeEach(() => {
    localStorage.clear();
    Object.defineProperty(window, 'innerWidth', {
      configurable: true,
      value: 1024,
      writable: true,
    });
  });

  it('renders content home navigation and breadcrumbs for detail pages', () => {
    renderLayout(
      '/dag-runs/briefing_gmail_fetch_test/019df6cf-0127-7340-bd96-d51bc1453045'
    );

    expect(screen.getByRole('link', { name: 'Content home' })).toHaveAttribute(
      'href',
      '/home'
    );
    expect(screen.getByRole('link', { name: 'DAG Runs' })).toHaveAttribute(
      'href',
      '/dag-runs'
    );
    expect(screen.getByText('briefing_gmail_fetch_test')).toBeVisible();
    expect(
      screen.getByText('019df6cf-0127-7340-bd96-d51bc1453045')
    ).toBeVisible();
  });
});
