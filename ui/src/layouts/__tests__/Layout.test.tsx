// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
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

vi.mock('@/features/agent', () => ({
  useAgentChatContext: () => ({ toggleChat: vi.fn() }),
  AgentChatPanel: ({ onClose }: { onClose?: () => void }) => (
    <div data-testid="agent-sidebar">
      <button type="button" onClick={onClose}>
        Close Agent
      </button>
    </div>
  ),
}));

vi.mock('../../menu', () => ({
  mainListItems: ({
    onAgentModeToggle,
  }: {
    onAgentModeToggle?: () => void;
  }) => (
    <div data-testid="sidebar-menu">
      <button type="button" onClick={onAgentModeToggle}>
        Open Agent
      </button>
    </div>
  ),
}));

const config = {
  title: 'Dagu',
  navbarColor: '',
  agentEnabled: true,
} as Config;

function renderLayout(path: string): void {
  render(
    <MemoryRouter initialEntries={[path]}>
      <ConfigContext.Provider value={config}>
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

  it('keeps the app sidebar visible on the agent home page', () => {
    renderLayout('/agent');

    expect(screen.getByTestId('sidebar-menu')).toBeVisible();
    expect(screen.getByText('Page Content')).toBeVisible();
  });

  it('renders content home navigation and breadcrumbs for detail pages', () => {
    renderLayout(
      '/dag-runs/briefing_gmail_fetch_test/019df6cf-0127-7340-bd96-d51bc1453045'
    );

    expect(
      screen.getByRole('link', { name: 'Content home' })
    ).toHaveAttribute('href', '/home');
    expect(screen.getByRole('link', { name: 'DAG Runs' })).toHaveAttribute(
      'href',
      '/dag-runs'
    );
    expect(screen.getByText('briefing_gmail_fetch_test')).toBeVisible();
    expect(
      screen.getByText('019df6cf-0127-7340-bd96-d51bc1453045')
    ).toBeVisible();
  });

  it('preserves nested agent page labels in breadcrumbs', () => {
    renderLayout('/agent-souls/new');

    expect(screen.getByRole('link', { name: 'Agent' })).toHaveAttribute(
      'href',
      '/agent'
    );
    expect(screen.getByRole('link', { name: 'Souls' })).toHaveAttribute(
      'href',
      '/agent-souls'
    );
    expect(screen.getByText('New Soul')).toBeVisible();
  });

  it('keeps workflow design fullscreen without the app sidebar', () => {
    renderLayout('/design');

    expect(screen.queryByTestId('sidebar-menu')).toBeNull();
    expect(
      screen.queryByRole('link', { name: 'Content home' })
    ).not.toBeInTheDocument();
    expect(screen.getByText('Page Content')).toBeVisible();
  });

  it('switches the desktop sidebar into the agent panel without covering content', () => {
    renderLayout('/cockpit');

    fireEvent.click(screen.getByRole('button', { name: 'Open Agent' }));

    expect(screen.queryByTestId('sidebar-menu')).toBeNull();
    expect(screen.getByTestId('agent-sidebar')).toBeVisible();
    expect(
      screen.getByRole('separator', { name: 'Resize agent panel' })
    ).toBeVisible();
    expect(screen.getByText('Page Content')).toBeVisible();
  });

  it('resizes the agent sidebar from the divider', () => {
    renderLayout('/cockpit');

    fireEvent.click(screen.getByRole('button', { name: 'Open Agent' }));
    const sidebar = screen.getByTestId('app-sidebar');
    const divider = screen.getByRole('separator', {
      name: 'Resize agent panel',
    });

    expect(sidebar).toHaveStyle({ width: '420px' });

    fireEvent.pointerDown(divider, { clientX: 420 });
    fireEvent.pointerMove(document, { clientX: 520 });
    fireEvent.pointerUp(document);

    expect(sidebar).toHaveStyle({ width: '520px' });
  });

  it('reclamps the saved agent sidebar width when the viewport narrows', () => {
    Object.defineProperty(window, 'innerWidth', {
      configurable: true,
      value: 1200,
      writable: true,
    });
    localStorage.setItem('agentSidebarWidth', '720');
    renderLayout('/cockpit');

    fireEvent.click(screen.getByRole('button', { name: 'Open Agent' }));
    const sidebar = screen.getByTestId('app-sidebar');

    expect(sidebar).toHaveStyle({ width: '720px' });

    Object.defineProperty(window, 'innerWidth', {
      configurable: true,
      value: 700,
      writable: true,
    });
    fireEvent.resize(window);

    expect(sidebar).toHaveStyle({ width: '340px' });
    expect(localStorage.getItem('agentSidebarWidth')).toBe('340');
  });
});
