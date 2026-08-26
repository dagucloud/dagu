// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { AppBarContext } from '@/contexts/AppBarContext';

vi.hoisted(() => {
  vi.stubGlobal('getConfig', () => ({
    apiURL: '/api/v1',
    authMode: 'builtin',
  }));
});

const mocks = vi.hoisted(() => ({
  useQuery: vi.fn(),
  client: {
    PUT: vi.fn(),
    POST: vi.fn(),
    DELETE: vi.fn(),
  },
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => mocks.client,
  useQuery: mocks.useQuery,
}));

import NotificationsPage, { NotificationChannelsPage } from '..';

Object.defineProperty(HTMLElement.prototype, 'hasPointerCapture', {
  configurable: true,
  value: () => false,
});

function renderPage() {
  const setTitle = vi.fn();

  render(
    <MemoryRouter>
      <AppBarContext.Provider value={{ setTitle } as never}>
        <NotificationsPage />
      </AppBarContext.Provider>
    </MemoryRouter>
  );

  return { setTitle };
}

function renderChannelsPage(settings: object) {
  const settingsQuery = {
    data: settings,
    error: undefined,
    isLoading: false,
    mutate: vi.fn(),
  };
  const channelsQuery = {
    data: { channels: [] },
    error: undefined,
    isLoading: false,
    mutate: vi.fn(),
  };
  mocks.useQuery.mockImplementation((path: string) =>
    path === '/notification-settings' ? settingsQuery : channelsQuery
  );

  render(
    <MemoryRouter>
      <AppBarContext.Provider
        value={
          {
            setTitle: vi.fn(),
            selectedRemoteNode: 'local',
          } as never
        }
      >
        <NotificationChannelsPage />
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('NotificationsPage', () => {
  it('renders notification links by section', () => {
    const { setTitle } = renderPage();

    expect(
      screen.getByRole('heading', { name: /^notifications$/i })
    ).toBeVisible();
    const rulesLink = screen.getByRole('link', { name: /^rules/i });
    const channelsLink = screen.getByRole('link', { name: /^channels/i });
    expect(rulesLink).toHaveAttribute('href', '/notification-rules');
    expect(channelsLink).toHaveAttribute('href', '/notification-channels');
    expect(
      rulesLink.compareDocumentPosition(channelsLink) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
    expect(
      screen.getByText('Set Global defaults and workspace overrides.')
    ).toBeVisible();
    expect(
      screen.getByText(
        'Manage Slack, email, webhook, and Telegram destinations.'
      )
    ).toBeVisible();
    expect(setTitle).toHaveBeenCalledWith('Notifications');
  });
});

describe('NotificationChannelsPage', () => {
  it('blocks channel controls when delivery is unavailable', () => {
    mocks.useQuery.mockReturnValue({
      data: undefined,
      error: new Error('Notification delivery is unavailable'),
      isLoading: false,
      mutate: vi.fn(),
    });

    render(
      <MemoryRouter>
        <AppBarContext.Provider value={{ setTitle: vi.fn() } as never}>
          <NotificationChannelsPage />
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    expect(
      screen.getByText('Notification delivery is unavailable')
    ).toBeVisible();
    expect(
      screen.queryByRole('button', { name: /^save$/i })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: /add channel/i })
    ).not.toBeInTheDocument();
  });

  it('preserves the configured password indicator when toggling authentication modes', async () => {
    const user = userEvent.setup();
    renderChannelsPage({
      smtp: {
        host: 'smtp.example.com',
        port: '587',
        username: 'sender@example.com',
        passwordConfigured: true,
      },
    });

    expect(screen.getByPlaceholderText('Password configured')).toBeVisible();

    await user.click(screen.getByLabelText('SMTP authentication'));
    await user.click(screen.getByRole('option', { name: 'OAuth 2.0' }));
    await user.click(screen.getByLabelText('SMTP authentication'));
    await user.click(screen.getByRole('option', { name: 'Password' }));

    expect(screen.getByPlaceholderText('Password configured')).toBeVisible();
  });

  it('preserves configured OAuth indicators when toggling authentication modes', async () => {
    const user = userEvent.setup();
    renderChannelsPage({
      smtp: {
        host: 'smtp.office365.com',
        port: '587',
        username: 'sender@example.com',
        oauth: {
          provider: 'microsoft',
          tenantId: 'tenant',
          clientId: 'client',
          clientSecretConfigured: true,
          refreshTokenConfigured: false,
          serviceAccountJsonConfigured: false,
        },
      },
    });

    expect(
      screen.getByPlaceholderText('Client secret configured')
    ).toBeVisible();

    await user.click(screen.getByLabelText('SMTP authentication'));
    await user.click(screen.getByRole('option', { name: 'Password' }));
    await user.click(screen.getByLabelText('SMTP authentication'));
    await user.click(screen.getByRole('option', { name: 'OAuth 2.0' }));

    expect(
      screen.getByPlaceholderText('Client secret configured')
    ).toBeVisible();
  });

  it('keeps typed OAuth secrets when identity fields change', async () => {
    const user = userEvent.setup();
    renderChannelsPage({});

    await user.click(screen.getByLabelText('SMTP authentication'));
    await user.click(screen.getByRole('option', { name: 'OAuth 2.0' }));

    const clientSecret = screen.getByPlaceholderText('Client secret');
    await user.type(clientSecret, 'typed-secret');
    await user.type(
      screen.getByPlaceholderText('Microsoft tenant ID'),
      'tenant'
    );
    await user.type(screen.getByPlaceholderText('Client ID'), 'client');
    await user.type(
      screen.getByPlaceholderText('Sender mailbox'),
      'sender@example.com'
    );

    expect(clientSecret).toHaveValue('typed-secret');
  });
});
