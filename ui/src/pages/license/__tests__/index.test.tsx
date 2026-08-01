import { cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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
    proxyEnabled: false,
    proxyButtonLabel: '',
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
      error: '',
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

function renderPage(
  licenseOverrides: Partial<LicenseStatus> = {},
  updateConfig: (patch: Partial<Config>) => void = () => undefined
) {
  return render(
    <ConfigContext.Provider value={makeConfig(licenseOverrides)}>
      <ConfigUpdateContext.Provider value={updateConfig}>
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

  it('shows a configured license failure instead of community status', () => {
    renderPage({
      valid: false,
      community: true,
      error:
        'License token verification failed. Check the configured token and server logs.',
    });

    expect(screen.getByText('License Error')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent(
      'License token verification failed'
    );
  });

  it('refreshes the authoritative status after activation', async () => {
    const user = userEvent.setup();
    const updateConfig = vi.fn();
    const status: LicenseStatus = {
      valid: true,
      plan: 'enterprise',
      expiry: '2027-01-01T00:00:00Z',
      features: ['audit', 'rbac'],
      gracePeriod: false,
      graceEndsAt: '',
      community: false,
      source: 'file',
      warningCode: '',
      error: '',
    };
    const post = vi.fn().mockResolvedValue({
      data: {
        plan: 'enterprise',
        expiry: status.expiry,
        features: status.features,
      },
    });
    const get = vi.fn().mockResolvedValue({ data: status });
    useClientMock.mockReturnValue({ POST: post, GET: get } as never);
    renderPage({}, updateConfig);

    await user.type(
      screen.getByRole('textbox', { name: 'License key' }),
      'key'
    );
    await user.click(screen.getByRole('button', { name: 'Activate' }));

    await waitFor(() => {
      expect(get).toHaveBeenCalledWith('/license/status', {
        params: { query: { remoteNode: 'local' } },
      });
      expect(updateConfig).toHaveBeenLastCalledWith({ license: status });
    });
  });
});
