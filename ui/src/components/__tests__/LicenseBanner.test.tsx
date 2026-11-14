import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import * as React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { LicenseBanner } from '@/components/LicenseBanner';
import {
  ConfigContext,
  type Config,
  type LicenseStatus,
} from '@/contexts/ConfigContext';

const { isAdmin } = vi.hoisted(() => ({ isAdmin: vi.fn(() => true) }));
vi.mock('@/contexts/AuthContext', () => ({ useIsAdmin: isAdmin }));

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
      valid: false,
      plan: 'trial',
      expiry: '2026-03-01T00:00:00Z',
      features: ['audit'],
      gracePeriod: true,
      graceEndsAt: '2026-03-05T00:00:00Z',
      community: false,
      source: 'file',
      warningCode: '',
      error: '',
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

function renderBanner(license: Partial<LicenseStatus>) {
  const config = makeConfig();
  config.license = { ...config.license, ...license };
  return render(
    <ConfigContext.Provider value={config}>
      <LicenseBanner />
    </ConfigContext.Provider>
  );
}

describe('LicenseBanner', () => {
  beforeEach(() => {
    localStorage.clear();
    isAdmin.mockReturnValue(true);
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

  it('shows configured license failures in community mode', () => {
    const config = makeConfig();
    config.license = {
      ...config.license,
      valid: false,
      community: true,
      gracePeriod: false,
      error:
        'License token verification failed. Check the configured token and server logs.',
    };

    render(
      <ConfigContext.Provider value={config}>
        <LicenseBanner />
      </ConfigContext.Provider>
    );

    expect(screen.getByRole('alert')).toHaveTextContent(
      'License token verification failed'
    );
  });

  describe('before a paid license expires', () => {
    const paid = {
      valid: true,
      plan: 'team',
      gracePeriod: false,
      graceEndsAt: '',
    };

    beforeEach(() => {
      vi.useFakeTimers({ toFake: ['Date'] });
      vi.setSystemTime(new Date('2026-10-01T00:00:00Z'));
    });
    afterEach(() => {
      vi.useRealTimers();
    });

    it('stays quiet more than 30 days ahead', () => {
      renderBanner({ ...paid, expiry: '2026-11-01T00:00:00Z' });

      expect(screen.queryByText(/expires in/)).not.toBeInTheDocument();
    });

    it('notes the expiry 30 days ahead and turns urgent at seven', () => {
      const view = renderBanner({ ...paid, expiry: '2026-10-31T00:00:00Z' });
      expect(screen.getByRole('status')).toHaveTextContent(
        'Your Dagu license expires in 30 days. Please renew.'
      );
      view.unmount();

      renderBanner({ ...paid, expiry: '2026-10-08T00:00:00Z' });
      expect(screen.getByRole('alert')).toHaveTextContent(
        'Your Dagu license expires in 7 days. Please renew.'
      );
    });

    it('remembers a dismissal for this server, plan, expiry, and urgency', async () => {
      const expiry = '2026-10-31T00:00:00Z';
      const view = renderBanner({ ...paid, expiry });
      await userEvent.click(
        screen.getByRole('button', {
          name: 'Dismiss license expiry notification',
        })
      );

      expect(screen.queryByRole('status')).not.toBeInTheDocument();
      expect(
        localStorage.getItem(`license-banner-local-team-${expiry}-notice`)
      ).toBe('true');
      view.unmount();
      renderBanner({ ...paid, expiry });
      expect(screen.queryByRole('status')).not.toBeInTheDocument();
    });

    it('stays quiet for a free license', () => {
      renderBanner({ ...paid, plan: 'free', expiry: '2026-10-02T00:00:00Z' });
      expect(screen.queryByRole('status')).not.toBeInTheDocument();
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    });

    it('asks non-admins to contact their administrator', () => {
      isAdmin.mockReturnValue(false);
      renderBanner({ ...paid, expiry: '2026-10-31T00:00:00Z' });

      expect(screen.getByRole('status')).toHaveTextContent(
        'Please contact your administrator.'
      );
      expect(screen.queryByRole('link')).not.toBeInTheDocument();
    });
  });
});
