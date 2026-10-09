import {
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import * as React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import LicensePage from '@/pages/license';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  ConfigContext,
  type Config,
  type LicenseStatus,
} from '@/contexts/ConfigContext';
import { useClient } from '@/hooks/api';
import { LicenseProvider } from '@/components/LicenseProvider';
import { SWRConfig } from 'swr';
import { MemoryRouter } from 'react-router-dom';
import { LicenseStatusResponseConnectedVia } from '@/api/v1/schema';

vi.mock('@/hooks/api', async () => {
  const { default: useSWR } = await import('swr');
  const useClient = vi.fn();
  return {
    useClient,
    useQuery: (path: string, params: unknown, options: object) =>
      useSWR(
        [path, params],
        async () => {
          const result = await useClient().GET(path, params);
          if (result.error) throw result.error;
          return result.data;
        },
        options
      ),
  };
});
vi.mock('@/contexts/AuthContext', () => ({ useIsAdmin: () => true }));

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

function renderPage(licenseOverrides: Partial<LicenseStatus> = {}) {
  return render(
    <MemoryRouter>
      <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
        <ConfigContext.Provider value={makeConfig(licenseOverrides)}>
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
            <LicenseProvider
              enabled
              remoteNode="local"
              initialLicense={makeConfig(licenseOverrides).license}
            >
              <LicensePage />
            </LicenseProvider>
          </AppBarContext.Provider>
        </ConfigContext.Provider>
      </SWRConfig>
    </MemoryRouter>
  );
}

describe('LicensePage', () => {
  beforeEach(() => {
    useClientMock.mockReturnValue({
      POST: vi.fn(),
      GET: vi.fn().mockReturnValue(new Promise(() => {})),
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
    const get = vi
      .fn()
      .mockResolvedValueOnce({ data: makeConfig().license })
      .mockResolvedValue({ data: status });
    useClientMock.mockReturnValue({ POST: post, GET: get } as never);
    renderPage();

    await user.type(screen.getByLabelText('License key'), 'key');
    await user.click(screen.getByRole('button', { name: 'Activate' }));

    await waitFor(() => {
      expect(get).toHaveBeenCalledWith('/license/status', {
        params: { query: { remoteNode: 'local' } },
      });
      expect(screen.getByText('Enterprise · Active')).toBeVisible();
    });
  });
  it('shows only entitled features as included', () => {
    renderPage({ features: ['audit'] });
    const audit = screen
      .getByRole('heading', { name: 'Audit logs' })
      .closest('article')!;
    const sso = screen
      .getByRole('heading', { name: 'Single sign-on' })
      .closest('article')!;
    expect(within(audit).getByText('Included')).toBeVisible();
    expect(
      within(sso).getByText('Requires a license with this feature')
    ).toBeVisible();
    expect(
      within(sso).queryByRole('link', { name: 'Setup guide' })
    ).not.toBeInTheDocument();
    expect(screen.getAllByText('Included')).toHaveLength(3);
  });

  it('preserves the current plan when activation fails', async () => {
    const post = vi
      .fn()
      .mockResolvedValue({ error: { message: 'Invalid license key' } });
    useClientMock.mockReturnValue({
      POST: post,
      GET: vi.fn().mockResolvedValue({ data: makeConfig().license }),
    } as never);
    renderPage();
    await userEvent.type(screen.getByLabelText('License key'), 'invalid');
    await userEvent.click(screen.getByRole('button', { name: 'Activate' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Invalid license key'
    );
    expect(screen.getByText('Pro · Active')).toBeVisible();
  });

  it('updates status and benefits after deactivation', async () => {
    const community = makeConfig({
      valid: false,
      community: true,
      plan: '',
      features: [],
      expiry: '',
    }).license;
    const post = vi.fn().mockResolvedValue({});
    const get = vi
      .fn()
      .mockResolvedValueOnce({ data: makeConfig().license })
      .mockResolvedValue({ data: community });
    useClientMock.mockReturnValue({ POST: post, GET: get } as never);
    renderPage();
    await waitFor(() => expect(get).toHaveBeenCalled());
    await userEvent.click(
      screen.getByRole('button', { name: 'Deactivate License' })
    );
    await userEvent.click(screen.getByRole('button', { name: 'Deactivate' }));
    expect(
      await screen.findByText('License deactivated. Running in community mode.')
    ).toBeVisible();
    expect(screen.getByText('Community', { exact: true })).toBeVisible();
    expect(
      screen.getAllByText('Requires a license with this feature')
    ).toHaveLength(5);
  });
  it('labels a pending deactivation without claiming activation', async () => {
    let resolve!: (value: object) => void;
    const pending = new Promise<object>((done) => {
      resolve = done;
    });
    useClientMock.mockReturnValue({
      POST: vi.fn(() => pending),
      GET: vi.fn().mockResolvedValue({ data: makeConfig().license }),
    } as never);
    renderPage();
    await userEvent.click(
      screen.getByRole('button', { name: 'Deactivate License' })
    );
    await userEvent.click(screen.getByRole('button', { name: 'Deactivate' }));
    expect(screen.getByRole('button', { name: 'Activate' })).toBeDisabled();
    expect(
      screen.getByRole('button', { name: 'Deactivating...' })
    ).toBeDisabled();
    resolve({});
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Deactivate License' })
      ).toBeEnabled()
    );
  });
  it('identifies this server and links to it in Dagu Console', () => {
    renderPage({
      source: 'file',
      serverName: 'build-01',
      workspace: 'acme',
      connectedVia: LicenseStatusResponseConnectedVia.console,
      serverId: 'srv-123',
      licenseId: 'lic-456',
      lastCheckIn: new Date().toISOString(),
      consoleUrl: 'https://console.dagu.sh/servers?server=srv-123',
    });

    const panel = screen
      .getByRole('heading', { name: 'This server' })
      .closest('section')!;
    expect(within(panel).getByText('build-01')).toBeVisible();
    expect(within(panel).getByText('acme')).toBeVisible();
    expect(within(panel).getByText('Connected via Dagu Console')).toBeVisible();
    expect(within(panel).getByText('srv-123')).toBeVisible();
    expect(within(panel).getByText('lic-456')).toBeVisible();
    expect(
      within(panel).getByRole('link', { name: 'Manage in Dagu Console' })
    ).toHaveAttribute('href', 'https://console.dagu.sh/servers?server=srv-123');
    expect(screen.getByText('Server: build-01')).toBeVisible();
  });

  it('checks in with Dagu Console on demand', async () => {
    const checkedIn = makeConfig({
      connectedVia: LicenseStatusResponseConnectedVia.key,
      serverId: 'srv-123',
      lastCheckIn: new Date().toISOString(),
    }).license;
    const post = vi.fn().mockResolvedValue({ data: checkedIn });
    useClientMock.mockReturnValue({
      POST: post,
      GET: vi.fn().mockReturnValue(new Promise(() => {})),
    } as never);
    renderPage({
      connectedVia: LicenseStatusResponseConnectedVia.key,
      serverId: 'srv-123',
      lastCheckIn: '2026-01-01T00:00:00Z',
    });

    await userEvent.click(screen.getByRole('button', { name: 'Check now' }));

    expect(post).toHaveBeenCalledWith('/license/refresh', {
      params: { query: { remoteNode: 'local' } },
    });
  });

  it('warns when disconnecting could not free the console slot', async () => {
    const post = vi.fn().mockResolvedValue({
      data: { message: 'License deactivated', releaseFailed: true },
    });
    useClientMock.mockReturnValue({
      POST: post,
      GET: vi.fn().mockReturnValue(new Promise(() => {})),
    } as never);
    renderPage({
      connectedVia: LicenseStatusResponseConnectedVia.console,
      serverId: 'srv-123',
    });

    await userEvent.click(screen.getByRole('button', { name: 'Disconnect' }));
    const dialog = await screen.findByRole('dialog');
    expect(
      within(dialog).getByText('Disconnect this server')
    ).toBeInTheDocument();
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Disconnect' })
    );

    expect(
      await screen.findByText(
        'Disconnected here, but Dagu Console could not be reached. Disconnect this server in Dagu Console to free its slot.'
      )
    ).toBeVisible();
  });

  it('explains that a config key returns after restart', () => {
    renderPage({ connectedVia: LicenseStatusResponseConnectedVia.config });

    expect(
      screen.getByText(
        /license.key in the config file activates this license again/
      )
    ).toBeVisible();
    expect(
      screen.getByRole('button', { name: 'Deactivate License' })
    ).toBeInTheDocument();
  });

  describe('connecting to Dagu Console', () => {
    const community = () =>
      makeConfig({
        valid: false,
        community: true,
        plan: '',
        features: [],
        expiry: '',
        source: '',
      }).license;
    const pending = {
      state: 'pending',
      connectUrl: 'https://console.dagu.sh/servers/connect?code=abc',
      code: 'ABCD1234',
      expiresAt: new Date(Date.now() + 15 * 60_000).toISOString(),
    };

    afterEach(() => {
      vi.restoreAllMocks();
    });

    it('opens the approval page and installs the license once approved', async () => {
      const tab = { opener: {}, location: { href: '' }, close: vi.fn() };
      vi.spyOn(window, 'open').mockReturnValue(tab as unknown as Window);
      let approved = false;
      const get = vi.fn((path: string) =>
        Promise.resolve({
          data:
            path === '/license/connect'
              ? approved
                ? { state: 'granted' }
                : pending
              : approved
                ? makeConfig({ plan: 'team' }).license
                : community(),
        })
      );
      const post = vi.fn().mockResolvedValue({ data: pending });
      useClientMock.mockReturnValue({ POST: post, GET: get } as never);
      renderPage(community());

      await userEvent.click(
        screen.getByRole('button', { name: 'Connect to Dagu Console' })
      );

      expect(post).toHaveBeenCalledWith('/license/connect', {
        params: { query: { remoteNode: 'local' } },
      });
      expect(window.open).toHaveBeenCalledWith('about:blank', '_blank');
      expect(tab.opener).toBeNull();
      expect(tab.location.href).toBe(pending.connectUrl);
      expect(await screen.findByText('ABCD1234')).toBeVisible();
      expect(
        screen.getByRole('link', { name: 'Open Dagu Console' })
      ).toHaveAttribute('href', pending.connectUrl);

      approved = true;
      expect(
        await screen.findByText(
          'Team connected. Explore your included features below.',
          undefined,
          { timeout: 5000 }
        )
      ).toBeVisible();
      expect(screen.getByText('Team · Active')).toBeVisible();
    });

    it('offers a link when the browser blocks the new tab', async () => {
      vi.spyOn(window, 'open').mockReturnValue(null);
      useClientMock.mockReturnValue({
        POST: vi.fn().mockResolvedValue({ data: pending }),
        GET: vi.fn().mockReturnValue(new Promise(() => {})),
      } as never);
      renderPage(community());

      await userEvent.click(
        screen.getByRole('button', { name: 'Connect to Dagu Console' })
      );

      expect(
        await screen.findByText(
          'Your browser blocked the new tab. Open Dagu Console to continue.'
        )
      ).toBeVisible();
      expect(
        screen.getByRole('link', { name: 'Open Dagu Console' })
      ).toHaveAttribute('href', pending.connectUrl);
    });

    it('shows why a request could not start and closes the blank tab', async () => {
      const tab = { opener: {}, location: { href: '' }, close: vi.fn() };
      vi.spyOn(window, 'open').mockReturnValue(tab as unknown as Window);
      useClientMock.mockReturnValue({
        POST: vi.fn().mockResolvedValue({
          error: { message: 'this server already has an active license' },
        }),
        GET: vi.fn().mockReturnValue(new Promise(() => {})),
      } as never);
      renderPage(community());

      await userEvent.click(
        screen.getByRole('button', { name: 'Connect to Dagu Console' })
      );

      expect(await screen.findByRole('alert')).toHaveTextContent(
        'this server already has an active license'
      );
      expect(tab.close).toHaveBeenCalled();
      expect(screen.getByRole('button', { name: 'Try again' })).toBeEnabled();
    });

    it('cancels a pending request', async () => {
      vi.spyOn(window, 'open').mockReturnValue(null);
      const del = vi.fn().mockResolvedValue({ data: { state: 'idle' } });
      useClientMock.mockReturnValue({
        POST: vi.fn().mockResolvedValue({ data: pending }),
        GET: vi.fn().mockReturnValue(new Promise(() => {})),
        DELETE: del,
      } as never);
      renderPage(community());
      await userEvent.click(
        screen.getByRole('button', { name: 'Connect to Dagu Console' })
      );
      await screen.findByText('ABCD1234');

      await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));

      expect(del).toHaveBeenCalledWith('/license/connect', {
        params: { query: { remoteNode: 'local' } },
      });
      expect(
        screen.getByRole('button', { name: 'Connect to Dagu Console' })
      ).toBeVisible();
    });

    it('keeps waiting when the cancel does not reach the server', async () => {
      vi.spyOn(window, 'open').mockReturnValue(null);
      useClientMock.mockReturnValue({
        POST: vi.fn().mockResolvedValue({ data: pending }),
        GET: vi.fn().mockReturnValue(new Promise(() => {})),
        DELETE: vi.fn().mockRejectedValue(new TypeError('Failed to fetch')),
      } as never);
      renderPage(community());
      await userEvent.click(
        screen.getByRole('button', { name: 'Connect to Dagu Console' })
      );
      await screen.findByText('ABCD1234');

      await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));

      expect(await screen.findByRole('alert')).toHaveTextContent(
        'Failed to fetch'
      );
      expect(screen.getByText('ABCD1234')).toBeVisible();
      expect(
        screen.queryByRole('button', { name: 'Connect to Dagu Console' })
      ).not.toBeInTheDocument();
    });

    it('explains that an environment license blocks connecting', () => {
      renderPage({ ...community(), source: 'env', error: 'activation failed' });

      expect(
        screen.getByText(/license is set by DAGU_LICENSE or DAGU_LICENSE_KEY/)
      ).toBeVisible();
      expect(
        screen.queryByRole('button', { name: 'Connect to Dagu Console' })
      ).not.toBeInTheDocument();
    });
  });

  it('waits for authoritative activation status and preserves warnings', async () => {
    const community = makeConfig({
      community: true,
      valid: false,
      plan: '',
      features: [],
    }).license;
    const status = makeConfig({
      plan: 'team',
      warningCode: 'MACHINE_LIMIT_EXCEEDED',
    }).license;
    let resolve!: (value: { data: LicenseStatus }) => void;
    const pending = new Promise<{ data: LicenseStatus }>((done) => {
      resolve = done;
    });
    const get = vi
      .fn()
      .mockResolvedValueOnce({ data: community })
      .mockReturnValue(pending);
    useClientMock.mockReturnValue({
      POST: vi.fn().mockResolvedValue({
        data: { plan: 'team', features: status.features },
      }),
      GET: get,
    } as never);
    renderPage(community);
    await waitFor(() => expect(get).toHaveBeenCalledTimes(1));
    await userEvent.type(screen.getByLabelText('License key'), 'key');
    await userEvent.click(screen.getByRole('button', { name: 'Activate' }));
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
    expect(screen.getByText('Community', { exact: true })).toBeVisible();
    await act(async () => {
      resolve({ data: status });
      await pending;
    });
    expect(
      await screen.findByText('Team · License needs attention')
    ).toBeVisible();
  });
});
