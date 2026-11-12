// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
import React from 'react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ProtectedRoute } from '@/components/ProtectedRoute';
import type { Config } from '@/contexts/ConfigContext';
import { ConfigContext } from '@/contexts/ConfigContext';
import { useAuth } from '@/contexts/AuthContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import SetupPage from '../setup';

const navigateMock = vi.fn();
const setupMock = vi.fn();
const completeSetupMock = vi.fn();

vi.mock('react-router-dom', async (importOriginal) => ({
  ...(await importOriginal<typeof import('react-router-dom')>()),
  useNavigate: () => navigateMock,
}));

vi.mock('@/contexts/AuthContext', () => ({
  useAuth: vi.fn(),
}));

const useAuthMock = vi.mocked(useAuth);

const config: Config = {
  apiURL: '/api/v1',
  basePath: '/',
  title: 'Dagu',
  navbarColor: '',
  tz: 'UTC',
  tzOffsetInSec: 0,
  version: 'test',
  maxDashboardPageLimit: 100,
  remoteNodes: '',
  initialWorkspaces: [],
  authMode: 'builtin',
  setupRequired: true,
  oidcEnabled: false,
  oidcButtonLabel: '',
  proxyEnabled: false,
  proxyButtonLabel: '',
  terminalEnabled: false,
  gitSyncEnabled: false,
  updateAvailable: false,
  latestVersion: '',
  upgradeCommand: '',
  restartRequired: false,
  restartCommand: '',
  permissions: {
    writeDags: true,
    runDags: true,
  },
  license: {
    valid: true,
    plan: 'community',
    expiry: '',
    features: [],
    gracePeriod: false,
    community: true,
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
};

// Renders the setup page, reached from entry. Protected pages send visitors
// there until an admin account exists.
function renderPage(entry = '/setup') {
  return render(
    <UserPreferencesProvider>
      <I18nProvider>
        <MemoryRouter initialEntries={[entry]}>
          <ConfigContext.Provider value={config}>
            <Routes>
              <Route path="/setup" element={<SetupPage />} />
              <Route
                path="*"
                element={
                  <ProtectedRoute>
                    <p>Requested page</p>
                  </ProtectedRoute>
                }
              />
            </Routes>
          </ConfigContext.Provider>
        </MemoryRouter>
      </I18nProvider>
    </UserPreferencesProvider>
  );
}

function createAdmin() {
  fireEvent.change(screen.getByLabelText('Username'), {
    target: { value: 'admin-user' },
  });
  fireEvent.change(screen.getByLabelText('Password'), {
    target: { value: 'password123' },
  });
  fireEvent.change(screen.getByLabelText('Confirm Password'), {
    target: { value: 'password123' },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Create account' }));
}

beforeEach(() => {
  localStorage.clear();
  navigateMock.mockReset();
  setupMock.mockReset();
  completeSetupMock.mockReset();

  setupMock.mockResolvedValue({
    token: 'token-1',
    user: { id: '1', username: 'admin-user', role: 'admin' },
  });

  useAuthMock.mockReturnValue({
    user: null,
    token: null,
    isAuthenticated: false,
    isLoading: false,
    setupRequired: true,
    login: vi.fn(),
    setup: setupMock,
    logout: vi.fn(),
    refreshUser: vi.fn(),
    completeSetup: completeSetupMock,
  });
});

afterEach(() => {
  cleanup();
});

describe('SetupPage', () => {
  it('completes onboarding after creating the admin account', async () => {
    renderPage();

    createAdmin();

    await waitFor(() => {
      expect(setupMock).toHaveBeenCalledWith('admin-user', 'password123');
    });

    expect(completeSetupMock).toHaveBeenCalledWith({
      token: 'token-1',
      user: { id: '1', username: 'admin-user', role: 'admin' },
    });
    expect(navigateMock).toHaveBeenCalledWith('/', { replace: true });
  });

  // Links such as the startup banner's point at a section of a page.
  it('returns to the requested page after creating the admin account', async () => {
    renderPage('/license?remoteNode=local#monitoring');

    createAdmin();

    await waitFor(() =>
      expect(navigateMock).toHaveBeenCalledWith(
        '/license?remoteNode=local#monitoring',
        { replace: true }
      )
    );
  });

  it('uses the saved Japanese locale', () => {
    localStorage.setItem('user_preferences', JSON.stringify({ locale: 'ja' }));

    renderPage();

    expect(
      screen.getByText('管理者アカウントを作成してください')
    ).toBeVisible();
    expect(
      screen.getByRole('button', { name: 'アカウントを作成' })
    ).toBeVisible();
  });
});
