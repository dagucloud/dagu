// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  APIKeyAllowedSurfaces,
  APIKeyAttributionClass,
  UserRole,
  type components,
} from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import APIKeysPage from '..';

vi.mock('@/contexts/AuthContext', () => ({
  TOKEN_KEY: 'daguToken',
  useIsAdmin: () => true,
}));

type APIKey = components['schemas']['APIKey'];

function makeConfig(licenseOverrides: Partial<Config['license']> = {}): Config {
  return {
    apiURL: '/api/v1',
    basePath: '/',
    title: 'Dagu',
    navbarColor: '',
    tz: 'UTC',
    tzOffsetInSec: 0,
    version: 'test',
    maxDashboardPageLimit: 100,
    remoteNodes: 'local',
    initialWorkspaces: [],
    authMode: 'builtin',
    setupRequired: false,
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
      valid: false,
      plan: '',
      expiry: '',
      features: [],
      gracePeriod: false,
      graceEndsAt: '',
      community: true,
      source: '',
      warningCode: '',
      ...licenseOverrides,
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
}

function makeAPIKey(id: string): APIKey {
  return {
    id,
    name: `api-key-${id}`,
    role: UserRole.viewer,
    workspaceAccess: { all: true, grants: [] },
    allowedSurfaces: [
      APIKeyAllowedSurfaces.rest_api,
      APIKeyAllowedSurfaces.mcp,
    ],
    attributionClass: APIKeyAttributionClass.service_account,
    serviceAccountId: `api_key:${id}`,
    serviceAccountName: `api-key-${id}`,
    keyPrefix: `dagu_${id}`,
    createdAt: '2026-05-22T00:00:00Z',
    updatedAt: '2026-05-22T00:00:00Z',
    createdBy: 'admin',
    lastUsedAt: null,
  };
}

function renderPage({
  license,
  apiKeys,
}: {
  license?: Partial<Config['license']>;
  apiKeys: APIKey[];
}) {
  const fetchMock = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({ apiKeys }),
  });
  vi.stubGlobal('fetch', fetchMock);

  render(
    <ConfigContext.Provider value={makeConfig(license)}>
      <AppBarContext.Provider
        value={{
          title: '',
          setTitle: () => undefined,
          remoteNodes: ['local'],
          setRemoteNodes: () => undefined,
          selectedRemoteNode: 'local',
          selectRemoteNode: () => undefined,
          workspaces: [],
        }}
      >
        <APIKeysPage />
      </AppBarContext.Provider>
    </ConfigContext.Provider>
  );

  return { fetchMock };
}

describe('APIKeysPage', () => {
  beforeEach(() => {
    localStorage.setItem('daguToken', 'test-token');
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  it('warns and disables creation when a community install reaches 2 API keys', async () => {
    renderPage({
      apiKeys: [makeAPIKey('1'), makeAPIKey('2')],
    });

    expect(
      await screen.findByText(/Community installs can manage up to 2 API keys/i)
    ).toBeVisible();
    expect(
      screen.getByRole('button', { name: /create api key/i })
    ).toBeDisabled();
  });

  it('does not warn licensed installs with more than 2 API keys', async () => {
    renderPage({
      license: {
        valid: true,
        plan: 'pro',
        expiry: '2026-05-22T00:00:00Z',
        features: ['audit', 'rbac'],
        community: false,
        source: 'file',
      },
      apiKeys: [makeAPIKey('1'), makeAPIKey('2'), makeAPIKey('3')],
    });

    await waitFor(() => {
      expect(screen.getAllByText('api-key-3')[0]).toBeVisible();
    });

    expect(
      screen.queryByText(/Community installs can manage up to 2 API keys/i)
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: /create api key/i })
    ).toBeEnabled();
  });

  it('warns when an inactive loaded license reaches 2 API keys', async () => {
    renderPage({
      license: {
        valid: false,
        gracePeriod: false,
        community: false,
        plan: 'pro',
        source: 'file',
      },
      apiKeys: [makeAPIKey('1'), makeAPIKey('2')],
    });

    expect(
      await screen.findByText(/Community installs can manage up to 2 API keys/i)
    ).toBeVisible();
  });
});
