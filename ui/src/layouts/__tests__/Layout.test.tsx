// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
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
import {
  useFullBleed,
  useHideBreadcrumb,
} from '@/contexts/LayoutChromeContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import { dispatchHandledEscape } from '@/test/keyboard';
import Layout from '../Layout';

vi.mock('@/components/LicenseBadge', () => ({ LicenseBadge: () => null }));

vi.mock('@/components/LicenseBanner', () => ({
  LicenseBanner: () => <div data-testid="license-banner" />,
}));

vi.mock('@/components/MonitoringNotice', () => ({
  MonitoringNotice: () => <div data-testid="monitoring-notice" />,
}));

vi.mock('@/components/UpdateBanner', () => ({
  UpdateBanner: () => <div data-testid="update-banner" />,
}));

vi.mock('@/components/SchedulerPauseBanner', () => ({
  SchedulerPauseBanner: () => <div data-testid="scheduler-pause-banner" />,
}));

vi.mock('../../menu', () => ({
  mainListItems: () => <div data-testid="sidebar-menu" />,
}));

const config = {
  title: 'Dagu',
  navbarColor: '',
} as Config;

function FullBleedPage({ on }: { on: boolean }) {
  useFullBleed(on);
  return <div>Page Content</div>;
}

function HideBreadcrumbPage({ on }: { on: boolean }) {
  useHideBreadcrumb(on);
  return <div>Page Content</div>;
}

function renderLayout(
  path: string,
  configOverride?: Partial<Config>,
  page: React.ReactElement = <div>Page Content</div>
) {
  return render(
    <UserPreferencesProvider>
      <I18nProvider>
        <MemoryRouter initialEntries={[path]}>
          <ConfigContext.Provider value={{ ...config, ...configOverride }}>
            <Layout>{page}</Layout>
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
    renderLayout('/queues/briefing_gmail_fetch_test');

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
      within(breadcrumbs).getByRole('link', {
        name: 'briefing_gmail_fetch_test',
      })
    ).toHaveAttribute('href', '/queues/briefing_gmail_fetch_test');
    expect(
      within(breadcrumbs).getByRole('link', {
        name: 'briefing_gmail_fetch_test',
      })
    ).toHaveAttribute('aria-current', 'page');
    expect(within(breadcrumbs).getAllByRole('link')).toHaveLength(4);
  });

  it('labels unknown routes as not found instead of echoing the URL', () => {
    renderLayout('/this-does-not-exist');

    const breadcrumbs = screen.getByRole('navigation', { name: 'breadcrumb' });
    expect(
      within(breadcrumbs).getByRole('link', { name: 'Page not found' })
    ).toHaveAttribute('aria-current', 'page');
    expect(
      within(breadcrumbs).queryByText('This Does Not Exist')
    ).not.toBeInTheDocument();
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

  it('keeps the mobile navigation open when a menu in it handles Escape', () => {
    renderLayout('/home');

    fireEvent.click(screen.getByRole('button', { name: 'Open menu' }));
    dispatchHandledEscape();

    expect(screen.getByRole('dialog', { name: 'Dagu' })).toBeInTheDocument();
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

  it('localizes Chinese breadcrumbs', () => {
    localStorage.setItem(
      'user_preferences',
      JSON.stringify({ locale: 'zh-CN' })
    );
    renderLayout('/queues/example');

    const breadcrumbs = screen.getByRole('navigation', { name: '面包屑' });
    expect(
      within(breadcrumbs).getByRole('link', { name: '首页' })
    ).toBeVisible();
    expect(
      within(breadcrumbs).getByRole('link', { name: '执行记录' })
    ).toBeVisible();
  });

  // Below 900px the icon rail leaves the content its room; the saved choice
  // (expanded) is kept for wider windows.
  it('starts the sidebar as its icon rail on a window under 900px', () => {
    window.innerWidth = 800;
    const { unmount } = renderLayout('/dags/example');
    expect(screen.getByTestId('app-sidebar').style.width).toBe('56px');
    expect(localStorage.getItem('sidebarExpanded')).toBe('true');
    unmount();

    window.innerWidth = 900;
    renderLayout('/dags/example');
    expect(screen.getByTestId('app-sidebar').style.width).toBe('240px');
  });

  it('follows a resize across the 900px width', () => {
    renderLayout('/dags/example');
    expect(screen.getByTestId('app-sidebar').style.width).toBe('240px');
    act(() => {
      window.innerWidth = 700;
      window.dispatchEvent(new Event('resize'));
    });
    expect(screen.getByTestId('app-sidebar').style.width).toBe('56px');
    act(() => {
      window.innerWidth = 1200;
      window.dispatchEvent(new Event('resize'));
    });
    expect(screen.getByTestId('app-sidebar').style.width).toBe('240px');
  });

  // Custom navbar colors drive every sidebar text token, so labels stay
  // readable on any background brightness.
  it('derives sidebar text colors from a custom navbar color', () => {
    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <MemoryRouter initialEntries={['/dags']}>
            <ConfigContext.Provider value={config}>
              <Layout navbarColor="#1e3a8a">
                <div>Page Content</div>
              </Layout>
            </ConfigContext.Provider>
          </MemoryRouter>
        </I18nProvider>
      </UserPreferencesProvider>
    );

    const sidebar = screen.getByTestId('app-sidebar');
    expect(sidebar.style.getPropertyValue('--sidebar-foreground')).toBe('#fff');
    expect(sidebar.style.getPropertyValue('--sidebar-accent-foreground')).toBe(
      '#fff'
    );
  });

  // parity:shell-layout-breadcrumb
  // A full-bleed page (the workflow workspace) owns its scroll and padding;
  // the breadcrumb and the banners stay above it.
  it('removes content padding and scroll only for a full-bleed page', () => {
    const contentArea = () => screen.getByText('Page Content').parentElement;
    const scrolling = ['overflow-auto', 'p-4', 'md:p-6'];

    const { unmount } = renderLayout(
      '/queues/example',
      undefined,
      <FullBleedPage on={false} />
    );
    expect(contentArea()).toHaveClass(...scrolling, 'min-h-0', 'flex-1');
    expect(contentArea()).not.toHaveClass('overflow-hidden');
    unmount();

    renderLayout('/queues/example', undefined, <FullBleedPage on />);

    expect(contentArea()).toHaveClass('overflow-hidden', 'min-h-0', 'flex-1');
    for (const name of scrolling) {
      expect(contentArea()).not.toHaveClass(name);
    }
    expect(screen.getByRole('link', { name: 'Content home' })).toBeVisible();
    expect(
      screen.getByRole('navigation', { name: 'breadcrumb' })
    ).toBeInTheDocument();
    expect(screen.getByTestId('update-banner')).toBeInTheDocument();
    expect(screen.getByTestId('license-banner')).toBeInTheDocument();
    expect(screen.getByTestId('scheduler-pause-banner')).toBeInTheDocument();
  });

  // The workflow workspace draws its own first row: the page asks the layout
  // to drop the strip. The other pages keep it, and so do the banners.
  it('hides the breadcrumb strip only while a page asks for it', () => {
    const { unmount } = renderLayout(
      '/queues/example',
      undefined,
      <HideBreadcrumbPage on={false} />
    );
    expect(
      screen.getByRole('navigation', { name: 'breadcrumb' })
    ).toBeInTheDocument();
    unmount();

    renderLayout('/queues/example', undefined, <HideBreadcrumbPage on />);

    expect(
      screen.queryByRole('navigation', { name: 'breadcrumb' })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('link', { name: 'Content home' })
    ).not.toBeInTheDocument();
    expect(screen.getByTestId('update-banner')).toBeInTheDocument();
    expect(screen.getByTestId('license-banner')).toBeInTheDocument();
    expect(screen.getByTestId('scheduler-pause-banner')).toBeInTheDocument();
  });

  // The workflow page owns its first row and its area: the frame takes that
  // shape from the address, with no claim, so that a reload shows no strip and
  // no padding that go away once the page has loaded.
  it('gives a workflow page its frame from the first render', () => {
    renderLayout('/dags/example', undefined, <FullBleedPage on={false} />);
    const contentArea = screen.getByText('Page Content').parentElement;
    expect(contentArea).toHaveClass('overflow-hidden', 'min-h-0', 'flex-1');
    expect(contentArea).not.toHaveClass('p-4');
    expect(
      screen.queryByRole('navigation', { name: 'breadcrumb' })
    ).not.toBeInTheDocument();
    expect(screen.getByTestId('update-banner')).toBeInTheDocument();
  });

  it('keeps the strip on the other pages and on the workflow list', () => {
    const { unmount } = renderLayout('/dags');
    expect(
      screen.getByRole('navigation', { name: 'breadcrumb' })
    ).toBeInTheDocument();
    unmount();
    renderLayout('/queues/example');
    expect(
      screen.getByRole('navigation', { name: 'breadcrumb' })
    ).toBeInTheDocument();
  });

  it('leaves license and monitoring notices to the license page', () => {
    const { unmount } = renderLayout('/home');
    expect(screen.getByTestId('license-banner')).toBeInTheDocument();
    expect(screen.getByTestId('monitoring-notice')).toBeInTheDocument();
    unmount();

    renderLayout('/license');
    expect(screen.queryByTestId('license-banner')).not.toBeInTheDocument();
    expect(screen.queryByTestId('monitoring-notice')).not.toBeInTheDocument();
  });
});
