import { render, screen } from '@testing-library/react';
import * as React from 'react';
import { beforeEach, describe, expect, it } from 'vitest';
import { LicenseBanner } from '@/components/LicenseBanner';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';

function makeConfig(overrides: Partial<Config> = {}): Config {
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
    authMode: 'none',
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
      plan: 'trial',
      expiry: '2026-03-01T00:00:00Z',
      features: ['audit'],
      gracePeriod: true,
      graceEndsAt: '2026-03-05T00:00:00Z',
      community: false,
      source: 'file',
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

describe('LicenseBanner', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('renders the server-provided grace end date', () => {
    render(
      <ConfigContext.Provider value={makeConfig()}>
        <LicenseBanner />
      </ConfigContext.Provider>
    );

    expect(
      screen.getByText(/Features will be disabled on 2026-03-05\./)
    ).toBeInTheDocument();
    expect(screen.queryByText(/2026-03-15/)).not.toBeInTheDocument();
  });
});
