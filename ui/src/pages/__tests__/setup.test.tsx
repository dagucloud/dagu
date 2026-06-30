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
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Config } from '@/contexts/ConfigContext';
import { ConfigContext } from '@/contexts/ConfigContext';
import { useAuth } from '@/contexts/AuthContext';
import SetupPage from '../setup';

const navigateMock = vi.fn();
const setupMock = vi.fn();
const completeSetupMock = vi.fn();

vi.mock('react-router-dom', () => ({
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
  terminalEnabled: false,
  gitSyncEnabled: false,
  updateAvailable: false,
  latestVersion: '',
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

function renderPage() {
  return render(
    <ConfigContext.Provider value={config}>
      <SetupPage />
    </ConfigContext.Provider>
  );
}

beforeEach(() => {
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

    await waitFor(() => {
      expect(setupMock).toHaveBeenCalledWith('admin-user', 'password123');
    });

    expect(completeSetupMock).toHaveBeenCalledWith({
      token: 'token-1',
      user: { id: '1', username: 'admin-user', role: 'admin' },
    });
    expect(navigateMock).toHaveBeenCalledWith('/', { replace: true });
  });
});
