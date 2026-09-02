// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import LoginPage from '../login';

vi.mock('@/contexts/AuthContext', () => ({
  useAuth: () => ({
    login: vi.fn(),
    isAuthenticated: false,
    isLoading: false,
    setupRequired: false,
  }),
}));

// Radix Select requires pointer-capture APIs missing from jsdom.
Object.defineProperty(HTMLElement.prototype, 'hasPointerCapture', {
  configurable: true,
  value: () => false,
});

function makeConfig(overrides: Partial<Config> = {}): Config {
  return {
    apiURL: '/dagu/api/v1',
    basePath: '/dagu',
    title: 'Dagu',
    navbarColor: '',
    tz: 'UTC',
    tzOffsetInSec: 0,
    version: 'test',
    maxDashboardPageLimit: 100,
    remoteNodes: '',
    initialWorkspaces: [],
    authMode: 'builtin',
    setupRequired: false,
    oidcEnabled: false,
    oidcButtonLabel: '',
    proxyEnabled: false,
    proxyButtonLabel: '',
    terminalEnabled: false,
    gitSyncEnabled: false,
    updateAvailable: false,
    latestVersion: '',
    permissions: { writeDags: false, runDags: false },
    license: {
      valid: true,
      plan: 'enterprise',
      expiry: '',
      features: ['sso'],
      gracePeriod: false,
      community: false,
      source: 'test',
      warningCode: '',
    },
    paths: {
      dagsDir: '',
      logDir: '',
      suspendFlagsDir: '',
      adminLogsDir: '',
      baseConfig: '',
      dagRunsDir: '',
      queueDir: '',
      procDir: '',
      serviceRegistryDir: '',
      configFileUsed: '',
      gitSyncDir: '',
      auditLogsDir: '',
    },
    ...overrides,
  };
}

function renderLogin(config: Config) {
  render(
    <UserPreferencesProvider>
      <I18nProvider>
        <MemoryRouter initialEntries={['/login']}>
          <ConfigContext.Provider value={config}>
            <LoginPage />
          </ConfigContext.Provider>
        </MemoryRouter>
      </I18nProvider>
    </UserPreferencesProvider>
  );
}

describe('LoginPage', () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.lang = 'en';
  });

  it('renders the configured explicit login link', () => {
    renderLogin(
      makeConfig({
        proxyEnabled: true,
        proxyButtonLabel: 'Continue with Corporate SSO',
      })
    );

    expect(
      screen.getByRole('link', { name: 'Continue with Corporate SSO' })
    ).toHaveAttribute('href', '/dagu/proxy-login');
    expect(screen.queryByText('Login with SSO')).not.toBeInTheDocument();
  });

  it('does not render an external-login separator when integrations are disabled', () => {
    renderLogin(makeConfig());

    expect(screen.queryByText('or')).not.toBeInTheDocument();
  });

  it('allows language selection before authentication', async () => {
    const user = userEvent.setup();
    renderLogin(makeConfig());

    const languageSelector = screen.getByRole('combobox', {
      name: 'Language',
    });
    expect(languageSelector).toHaveClass('h-7');
    await user.click(languageSelector);
    await user.click(
      await screen.findByRole('option', { name: 'Chinese (Simplified)' })
    );

    expect(screen.getByRole('button', { name: '登录' })).toBeInTheDocument();
    expect(
      JSON.parse(localStorage.getItem('user_preferences') ?? '{}')
    ).toEqual(expect.objectContaining({ locale: 'zh-CN' }));
    await waitFor(() => expect(document.documentElement.lang).toBe('zh-CN'));
  });

  it('switches the login page to Japanese', async () => {
    const user = userEvent.setup();
    renderLogin(makeConfig());

    await user.click(screen.getByRole('combobox', { name: 'Language' }));
    await user.click(await screen.findByRole('option', { name: 'Japanese' }));

    expect(
      screen.getByRole('button', { name: 'ログイン' })
    ).toBeInTheDocument();
    expect(
      JSON.parse(localStorage.getItem('user_preferences') ?? '{}')
    ).toEqual(expect.objectContaining({ locale: 'ja' }));
    await waitFor(() => expect(document.documentElement.lang).toBe('ja'));
  });
});
