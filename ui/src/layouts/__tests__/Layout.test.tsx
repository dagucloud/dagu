// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
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
    <UserPreferencesProvider>
      <I18nProvider>
        <MemoryRouter initialEntries={[path]}>
          <ConfigContext.Provider value={{ ...config, ...configOverride }}>
            <Layout>
              <div>Page Content</div>
            </Layout>
          </ConfigContext.Provider>
        </MemoryRouter>
      </I18nProvider>
    </UserPreferencesProvider>
  );
}

describe('Layout', () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.lang = 'en';
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
    const breadcrumbs = screen.getByRole('navigation', { name: 'breadcrumb' });
    expect(
      within(breadcrumbs).getByRole('link', { name: 'Home' })
    ).toHaveAttribute('href', '/home');
    expect(
      within(breadcrumbs).getByRole('link', { name: 'Executions' })
    ).toHaveAttribute('href', '/dag-runs');
    expect(
      within(breadcrumbs).getByRole('link', { name: 'DAG Runs' })
    ).toHaveAttribute('href', '/dag-runs');
    expect(
      within(breadcrumbs).getByRole('link', {
        name: 'briefing_gmail_fetch_test',
      })
    ).toHaveAttribute('href', '/dag-runs?name=briefing_gmail_fetch_test');
    expect(
      within(breadcrumbs).getByRole('link', {
        name: '019df6cf-0127-7340-bd96-d51bc1453045',
      })
    ).toHaveAttribute(
      'href',
      '/dag-runs/briefing_gmail_fetch_test/019df6cf-0127-7340-bd96-d51bc1453045'
    );
    expect(
      within(breadcrumbs).getByRole('link', {
        name: '019df6cf-0127-7340-bd96-d51bc1453045',
      })
    ).toHaveAttribute('aria-current', 'page');
    expect(within(breadcrumbs).getAllByRole('link')).toHaveLength(5);
  });

  it('opens the mobile navigation as a keyboard-contained dialog', async () => {
    renderLayout('/home');

    const openButton = screen.getByRole('button', { name: 'Open menu' });
    fireEvent.click(openButton);

    const dialog = screen.getByRole('dialog', { name: 'Dagu' });
    const closeButton = within(dialog).getByRole('button', {
      name: 'Close menu',
    });
    expect(closeButton).toHaveFocus();
    expect(
      screen.getByText('Page Content').closest('[aria-hidden="true"]')
    ).toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'Tab' });
    expect(closeButton).toHaveFocus();

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await waitFor(() => expect(openButton).toHaveFocus());
  });

  it('localizes mobile navigation controls', () => {
    localStorage.setItem(
      'user_preferences',
      JSON.stringify({ locale: 'zh-CN' })
    );
    renderLayout('/home');

    fireEvent.click(screen.getByRole('button', { name: '打开菜单' }));

    expect(
      within(screen.getByRole('dialog')).getByRole('button', {
        name: '关闭菜单',
      })
    ).toBeInTheDocument();
  });
});
