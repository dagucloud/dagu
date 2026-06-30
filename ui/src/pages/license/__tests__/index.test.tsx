import { cleanup, render, screen } from '@testing-library/react';
import * as React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import LicensePage from '@/pages/license';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  ConfigContext,
  ConfigUpdateContext,
  type Config,
  type LicenseStatus,
} from '@/contexts/ConfigContext';
import { useClient } from '@/hooks/api';

vi.mock('@/hooks/api', () => ({
  useClient: vi.fn(),
}));

const useClientMock = vi.mocked(useClient);

function makeConfig(licenseOverrides: Partial<LicenseStatus> = {}): Config {
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
      valid: true,
      plan: 'pro',
      expiry: '2026-04-30T00:00:00Z',
      features: ['audit', 'rbac'],
      gracePeriod: false,
      graceEndsAt: '',
      community: false,
      source: 'file',
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

function renderPage(licenseOverrides: Partial<LicenseStatus> = {}) {
  return render(
    <ConfigContext.Provider value={makeConfig(licenseOverrides)}>
      <ConfigUpdateContext.Provider value={() => undefined}>
        <AppBarContext.Provider
          value={{
            title: '',
            setTitle: () => undefined,
            remoteNodes: ['local'],
            setRemoteNodes: () => undefined,
            selectedRemoteNode: 'local',
            selectRemoteNode: () => undefined,
          }}
        >
          <LicensePage />
        </AppBarContext.Provider>
      </ConfigUpdateContext.Provider>
    </ConfigContext.Provider>
  );
}

describe('LicensePage', () => {
  beforeEach(() => {
    useClientMock.mockReturnValue({
      POST: vi.fn(),
    } as never);
  });

  afterEach(() => {
    cleanup();
  });

  it('shows the deactivate button during grace period for file-backed licenses', () => {
    renderPage({
      valid: false,
      gracePeriod: true,
      graceEndsAt: '2026-05-10T00:00:00Z',
      community: false,
      source: 'file',
    });

    expect(
      screen.getByRole('button', { name: 'Deactivate License' })
    ).toBeInTheDocument();
  });

  it('shows environment variable guidance during grace period for env-backed licenses', () => {
    renderPage({
      valid: false,
      gracePeriod: true,
      graceEndsAt: '2026-05-10T00:00:00Z',
      community: false,
      source: 'env',
    });

    expect(
      screen.getByText(
        /This license is configured via an environment variable/i
      )
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Deactivate License' })
    ).not.toBeInTheDocument();
  });

  it('keeps expired non-community licenses deactivatable after grace ends', () => {
    renderPage({
      valid: false,
      gracePeriod: false,
      community: false,
      source: 'file',
      expiry: '2026-04-01T00:00:00Z',
    });

    expect(
      screen.getByRole('button', { name: 'Deactivate License' })
    ).toBeInTheDocument();
  });
});
