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
import { Link, MemoryRouter } from 'react-router-dom';
import {
  LicenseMonitoringLevel,
  LicenseRemoteAccessLevel,
  LicenseStatusResponseConnectedVia,
} from '@/api/v1/schema';
import type { LicenseMonitoring } from '@/hooks/useLicenseMonitoring';
import type { LicenseRemoteAccess } from '@/hooks/useLicenseRemoteAccess';
import dayjs from '@/lib/dayjs';

vi.mock('@/hooks/api', async () => {
  const { default: useSWR } = await import('swr');
  const useClient = vi.fn();
  return {
    useClient,
    // As in swr-openapi, null params skip the request.
    useQuery: (path: string, params: unknown, options: object) =>
      useSWR(
        params === null ? null : [path, params],
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
    upgradeCommand: '',
    restartRequired: false,
    restartCommand: '',
    permissions: {
      writeDags: true,
      runDags: true,
    },
    license: {
      valid: true,
      plan: 'pro',
      // Far enough ahead that no expiry warning applies.
      expiry: '2099-04-30T00:00:00Z',
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
  {
    path = '/license',
    authMode = 'builtin',
    children,
  }: {
    path?: string;
    authMode?: Config['authMode'];
    children?: React.ReactNode;
  } = {}
) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
        <ConfigContext.Provider
          value={{ ...makeConfig(licenseOverrides), authMode }}
        >
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
              {children}
            </LicenseProvider>
          </AppBarContext.Provider>
        </ConfigContext.Provider>
      </SWRConfig>
    </MemoryRouter>
  );
}

const online: Partial<LicenseStatus> = {
  connectedVia: LicenseStatusResponseConnectedVia.console,
  serverId: 'srv-123',
  lastCheckIn: new Date().toISOString(),
};

// A server connected on the free plan, whose token claims no features.
const freeOnline: Partial<LicenseStatus> = {
  ...online,
  plan: 'free',
  features: [],
  workspace: 'acme',
};

const unchosen: LicenseMonitoring = {
  level: LicenseMonitoringLevel.off,
  configured: false,
  chosen: false,
  noticeDismissed: false,
  errorsOffered: false,
};

function overview() {
  return screen.getByRole('region', { name: 'Overview' });
}

// Reads the value of a row in the overview by its label.
function row(label: string) {
  const term = within(overview()).getByText(label, { selector: 'dt' });
  return term.nextElementSibling as HTMLElement;
}

async function openKeyForm(name = 'Use a different license key') {
  await userEvent.click(screen.getByRole('button', { name }));
  return screen.getByLabelText('License key');
}

function findMonitoringCard() {
  return screen.findByRole('region', { name: 'Monitoring' });
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

    expect(row('Status')).toHaveTextContent('License error');
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

    await user.type(await openKeyForm(), 'key');
    await user.click(screen.getByRole('button', { name: 'Activate' }));

    await waitFor(() => {
      expect(get).toHaveBeenCalledWith('/license/status', {
        params: { query: { remoteNode: 'local' } },
      });
      expect(row('Plan')).toHaveTextContent('Enterprise plan');
    });
    expect(row('Status')).toHaveTextContent('Active');
    expect(screen.getByRole('status')).toHaveTextContent('License activated.');
  });
  it('shows only entitled features as included', () => {
    renderPage({ features: ['audit'] });
    const audit = screen
      .getByRole('heading', { name: 'Audit logs' })
      .closest('li')!;
    const sso = screen
      .getByRole('heading', { name: 'Single sign-on' })
      .closest('li')!;
    expect(within(audit).getByText('Included')).toBeVisible();
    expect(
      within(audit).getByRole('link', { name: 'Open feature' })
    ).toHaveAttribute('href', '/audit-logs');
    expect(within(sso).getByText('Not included')).toBeVisible();
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
    await userEvent.type(await openKeyForm(), 'invalid');
    await userEvent.click(screen.getByRole('button', { name: 'Activate' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Invalid license key'
    );
    expect(row('Plan')).toHaveTextContent('Pro plan');
    expect(row('Status')).toHaveTextContent('Active');
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
    expect(screen.getAllByText('Not included')).toHaveLength(5);
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
    await openKeyForm();
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

    expect(row('Server')).toHaveTextContent('build-01');
    expect(row('Workspace')).toHaveTextContent('acme');
    expect(row('Connection')).toHaveTextContent('Connected via Dagu Console');
    expect(row('Server ID')).toHaveTextContent('srv-123');
    expect(row('License ID')).toHaveTextContent('lic-456');
    expect(
      screen.getByRole('link', { name: 'Manage in Dagu Console' })
    ).toHaveAttribute('href', 'https://console.dagu.sh/servers?server=srv-123');
  });

  it('renames this server in Dagu Console', async () => {
    const reported = 's-macbook-pro.taild5c75a.ts.net';
    const renamed = makeConfig({
      ...online,
      serverName: 'Build box',
      reportedName: reported,
    }).license;
    const put = vi.fn().mockResolvedValue({ data: renamed });
    useClientMock.mockReturnValue({
      POST: vi.fn(),
      PUT: put,
      GET: vi.fn().mockReturnValue(new Promise(() => {})),
    } as never);
    renderPage({ ...online, serverName: reported, reportedName: reported });

    expect(row('Server')).not.toHaveTextContent('Reports as');
    await userEvent.click(screen.getByRole('button', { name: 'Rename' }));
    const input = screen.getByLabelText('Server name');
    expect(input).toHaveValue('');
    expect(input).toHaveAttribute('placeholder', reported);
    await userEvent.type(input, '  Build box ');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(put).toHaveBeenCalledWith('/license/server-name', {
      params: { query: { remoteNode: 'local' } },
      body: { name: 'Build box' },
    });
    await waitFor(() => expect(row('Server')).toHaveTextContent('Build box'));
    expect(row('Server')).toHaveTextContent(`Reports as ${reported}`);
    expect(screen.queryByLabelText('Server name')).not.toBeInTheDocument();
  });

  it('starts a rename from the name a person gave the server', async () => {
    useClientMock.mockReturnValue({
      POST: vi.fn(),
      PUT: vi.fn(),
      GET: vi.fn().mockReturnValue(new Promise(() => {})),
    } as never);
    renderPage({ ...online, serverName: 'Build box', reportedName: 'ip-10' });

    await userEvent.click(screen.getByRole('button', { name: 'Rename' }));

    expect(screen.getByLabelText('Server name')).toHaveValue('Build box');
  });

  it('says why a rename failed and keeps the form open', async () => {
    useClientMock.mockReturnValue({
      POST: vi.fn(),
      PUT: vi.fn().mockResolvedValue({
        error: { message: 'Server names are at most 100 characters.' },
      }),
      GET: vi.fn().mockReturnValue(new Promise(() => {})),
    } as never);
    renderPage({ ...online, serverName: 'build-01', reportedName: 'build-01' });

    await userEvent.click(screen.getByRole('button', { name: 'Rename' }));
    await userEvent.type(screen.getByLabelText('Server name'), 'Build box');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Server names are at most 100 characters.'
    );
    expect(screen.getByLabelText('Server name')).toHaveValue('Build box');
  });

  it('offers no rename for a license that does not check in', () => {
    renderPage({ serverName: 'build-01' });

    expect(row('Server')).toHaveTextContent('build-01');
    expect(
      within(overview()).queryByRole('button', { name: 'Rename' })
    ).not.toBeInTheDocument();
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

  // A trial started in Dagu Console shows as soon as the person comes back,
  // to the tab or the window: the page checks in once on return, and not on
  // later returns.
  it('checks in on coming back from Dagu Console', async () => {
    const trial = makeConfig({ ...online, plan: 'trial' }).license;
    const post = vi.fn().mockResolvedValue({ data: trial });
    useClientMock.mockReturnValue({
      POST: post,
      GET: vi.fn().mockReturnValue(new Promise(() => {})),
    } as never);
    renderPage({
      ...freeOnline,
      consoleUrl: 'https://console.dagu.sh/servers',
    });

    document.dispatchEvent(new Event('visibilitychange'));
    expect(post).not.toHaveBeenCalled();

    await userEvent.click(
      screen.getByRole('link', { name: 'Start free trial' })
    );
    // Back from Console in another window.
    window.dispatchEvent(new Event('focus'));
    expect(post).toHaveBeenCalledTimes(1);
    document.dispatchEvent(new Event('visibilitychange'));

    expect(post).toHaveBeenCalledTimes(1);
    expect(post).toHaveBeenCalledWith('/license/refresh', {
      params: { query: { remoteNode: 'local' } },
    });
    expect(await screen.findByText('Trial plan')).toBeVisible();
  });

  it('says why a check-in failed', async () => {
    useClientMock.mockReturnValue({
      POST: vi.fn().mockResolvedValue({ error: { message: '' } }),
      GET: vi.fn().mockReturnValue(new Promise(() => {})),
    } as never);
    renderPage(online);

    await userEvent.click(screen.getByRole('button', { name: 'Check now' }));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Dagu Console could not be reached. Try again later.'
    );
  });

  it('says why a deactivation failed and keeps the license', async () => {
    useClientMock.mockReturnValue({
      POST: vi.fn().mockResolvedValue({ error: { message: 'disk is full' } }),
      GET: vi.fn().mockReturnValue(new Promise(() => {})),
    } as never);
    renderPage();

    await userEvent.click(
      screen.getByRole('button', { name: 'Deactivate License' })
    );
    await userEvent.click(screen.getByRole('button', { name: 'Deactivate' }));

    const zone = screen.getByRole('region', { name: 'Danger zone' });
    expect(await within(zone).findByRole('alert')).toHaveTextContent(
      'disk is full'
    );
    expect(row('Plan')).toHaveTextContent('Pro plan');
  });

  it('links role-based access to its setup guide without built-in auth', () => {
    renderPage({}, { authMode: 'basic' });

    const rbac = screen
      .getByRole('heading', { name: 'Role-based access' })
      .closest('li')!;
    expect(
      within(rbac).getByRole('link', { name: 'Setup guide' })
    ).toHaveAttribute(
      'href',
      'https://docs.dagu.sh/server-admin/authentication/builtin'
    );
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

    function offer() {
      return screen.getByRole('region', {
        name: 'Monitor this server for free',
      });
    }

    function connectButton() {
      return within(offer()).getByRole('button', { name: 'Connect for free' });
    }

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

      await userEvent.click(connectButton());

      expect(post).toHaveBeenCalledWith('/license/connect', {
        params: { query: { remoteNode: 'local' } },
        body: { report: 'errors' },
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
        await screen.findByText('Connected to Dagu Console.', undefined, {
          timeout: 5000,
        })
      ).toBeVisible();
      expect(row('Plan')).toHaveTextContent('Team plan');
      expect(row('Status')).toHaveTextContent('Active');
    });

    // After approval, the server checks in with Dagu Console and can report.
    // A level chosen when connecting is saved unless configuration sets one.
    function approveAndConnect(
      initial: LicenseMonitoring,
      connected: Partial<LicenseStatus> = { plan: 'team', ...online }
    ) {
      vi.spyOn(window, 'open').mockReturnValue(null);
      let approved = false;
      let monitoring = initial;
      const get = vi.fn((path: string) =>
        Promise.resolve({
          data:
            path === '/license/connect'
              ? approved
                ? { state: 'granted' }
                : pending
              : path === '/license/monitoring'
                ? monitoring
                : approved
                  ? makeConfig(connected).license
                  : community(),
        })
      );
      const post = vi.fn(
        (
          path: string,
          init?: { body?: { report?: LicenseMonitoringLevel } }
        ) => {
          if (path !== '/license/connect') {
            return Promise.resolve({
              data: { ...monitoring, noticeDismissed: true },
            });
          }
          const report = init?.body?.report;
          if (report && !monitoring.configured) {
            monitoring = { ...monitoring, level: report, chosen: true };
          }
          return Promise.resolve({ data: pending });
        }
      );
      const put = vi.fn(
        (_path: string, init: { body: { level: LicenseMonitoringLevel } }) =>
          Promise.resolve({
            data: { ...monitoring, level: init.body.level, chosen: true },
          })
      );
      useClientMock.mockReturnValue({
        POST: post,
        GET: get,
        PUT: put,
      } as never);
      renderPage(community());
      return {
        post,
        put,
        // The server's first report reaches Dagu Console.
        report: () => {
          monitoring = {
            ...monitoring,
            lastReportAt: new Date().toISOString(),
            lastReport: '{"protocol":2}',
          };
        },
        approve: async (button = connectButton) => {
          await userEvent.click(button());
          await screen.findByText('ABCD1234');
          approved = true;
        },
      };
    }

    it('connects a free server reporting at the level chosen', async () => {
      const { approve } = approveAndConnect(unchosen, freeOnline);
      await approve();

      expect(
        await screen.findByText('Connected to acme.', undefined, {
          timeout: 5000,
        })
      ).toBeVisible();
      expect(row('Plan')).toHaveTextContent('Free plan');
      expect(row('Status')).toHaveTextContent('Monitoring only');
      expect(
        await within(await findMonitoringCard()).findByText(
          'Health, run status, and error messages'
        )
      ).toBeVisible();
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    // The first report shows within seconds of connecting, not at the next
    // regular refresh half a minute later.
    it('shows the first report soon after connecting', async () => {
      const { approve, report } = approveAndConnect(unchosen);
      await approve();
      const card = await screen.findByRole(
        'region',
        { name: 'Monitoring' },
        { timeout: 5000 }
      );
      report();

      expect(
        await within(card).findByRole(
          'button',
          { name: 'View last report' },
          { timeout: 5000 }
        )
      ).toBeVisible();
    }, 15_000);

    // The trial starts in the same approval: the approval page offers it
    // first, as it does when another page asked for the trial.
    it('asks the approval page for the trial when chosen', async () => {
      const tab = { opener: {}, location: { href: '' }, close: vi.fn() };
      vi.spyOn(window, 'open').mockReturnValue(tab as unknown as Window);
      useClientMock.mockReturnValue({
        POST: vi.fn().mockResolvedValue({ data: pending }),
        GET: vi.fn().mockReturnValue(new Promise(() => {})),
      } as never);
      renderPage(community());

      await userEvent.click(
        within(offer()).getByRole('checkbox', {
          name: /Also try paid features free for 14 days/,
        })
      );
      // The card no longer promises that paid features stay off.
      expect(offer()).toHaveTextContent(
        'Free for one server, with 14 days of paid features. After the trial: reports every 5 minutes and keeps 7 days of run history.'
      );
      expect(offer()).not.toHaveTextContent('Paid features stay off.');
      await userEvent.click(
        within(offer()).getByRole('button', { name: 'Connect and start trial' })
      );

      const withTrial = pending.connectUrl + '&trial=1';
      expect(tab.location.href).toBe(withTrial);
      expect(
        await screen.findByRole('link', { name: 'Open Dagu Console' })
      ).toHaveAttribute('href', withTrial);
    });

    it('chooses the trial when the page is asked for it', () => {
      useClientMock.mockReturnValue({
        GET: vi.fn().mockReturnValue(new Promise(() => {})),
      } as never);
      renderPage(community(), { path: '/license?trial=1#monitoring' });

      expect(
        within(offer()).getByRole('checkbox', {
          name: /Also try paid features free for 14 days/,
        })
      ).toBeChecked();
      expect(
        within(offer()).getByRole('button', { name: 'Connect and start trial' })
      ).toBeVisible();
    });

    it('starts the same request from the free monitoring offer', async () => {
      vi.spyOn(window, 'open').mockReturnValue(null);
      const post = vi.fn().mockResolvedValue({ data: pending });
      useClientMock.mockReturnValue({
        POST: post,
        GET: vi.fn().mockReturnValue(new Promise(() => {})),
      } as never);
      renderPage(community());

      expect(offer()).toHaveTextContent(
        'Reporting starts as soon as the server connects.'
      );
      expect(offer()).toHaveTextContent(
        'Free for one server: reports every 5 minutes and keeps 7 days of run history. Paid features stay off.'
      );
      expect(offer()).toHaveTextContent(
        'Never sent: full logs, outputs, parameters, environment, step commands, DAG YAML, schedule expressions, and the values of workflow secrets.'
      );
      expect(
        within(offer()).getByRole('radio', {
          name: 'Health, run status, and error messages (recommended)',
        })
      ).toBeChecked();
      await userEvent.click(
        within(offer()).getByRole('button', { name: 'Connect for free' })
      );

      expect(post).toHaveBeenCalledWith('/license/connect', {
        params: { query: { remoteNode: 'local' } },
        body: { report: 'errors' },
      });
      expect(await screen.findByText('ABCD1234')).toBeVisible();
      expect(
        screen.queryByRole('region', { name: 'Monitor this server for free' })
      ).not.toBeInTheDocument();
    });

    it('reports the level chosen in the offer without asking again', async () => {
      const { post, approve } = approveAndConnect(unchosen);
      await userEvent.click(
        within(offer()).getByRole('radio', { name: 'Health only' })
      );
      await approve(() =>
        within(offer()).getByRole('button', { name: 'Connect for free' })
      );

      expect(post).toHaveBeenCalledWith('/license/connect', {
        params: { query: { remoteNode: 'local' } },
        body: { report: 'health' },
      });
      expect(
        await screen.findByText('Connected to Dagu Console.', undefined, {
          timeout: 5000,
        })
      ).toBeVisible();
      expect(
        await within(await findMonitoringCard()).findByText('Health only')
      ).toBeVisible();
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    it('keeps the level chosen before when the server connects again', async () => {
      const { post, approve } = approveAndConnect({
        ...unchosen,
        level: LicenseMonitoringLevel.runs,
        chosen: true,
      });
      // The level chosen before is selected once the server's choice loads.
      await waitFor(() =>
        expect(
          within(offer()).getByRole('radio', { name: 'Health and run status' })
        ).toBeChecked()
      );
      await approve(() =>
        within(offer()).getByRole('button', { name: 'Connect for free' })
      );

      expect(post).toHaveBeenCalledWith('/license/connect', {
        params: { query: { remoteNode: 'local' } },
        body: { report: 'runs' },
      });
    });

    it('promises failure emails only with run status', async () => {
      approveAndConnect(unchosen);
      const runsText =
        "Dagu Console shows this server's runs, failures, and trends, and emails you when a workflow fails or the server stops reporting.";
      expect(
        await within(offer()).findByText(runsText, { exact: false })
      ).toBeVisible();

      await userEvent.click(
        within(offer()).getByRole('radio', { name: 'Health only' })
      );

      expect(
        within(offer()).getByText(
          "Dagu Console shows this server's health and emails you when the server stops reporting.",
          { exact: false }
        )
      ).toBeVisible();
      expect(
        within(offer()).queryByText(runsText, { exact: false })
      ).not.toBeInTheDocument();
    });

    it('shows a level set in the configuration in the offer', async () => {
      const { post, approve } = approveAndConnect({
        ...unchosen,
        level: LicenseMonitoringLevel.health,
        configured: true,
      });

      expect(
        await within(offer()).findByText(
          'cloud.report in the server configuration sets this. Change it there and restart Dagu.'
        )
      ).toBeVisible();
      expect(within(offer()).getByText('Health only')).toBeVisible();
      expect(within(offer()).queryByRole('radio')).not.toBeInTheDocument();
      await approve(() =>
        within(offer()).getByRole('button', { name: 'Connect for free' })
      );

      expect(post).toHaveBeenCalledWith('/license/connect', {
        params: { query: { remoteNode: 'local' } },
      });
    });

    it('does not ask after connecting when the configuration sets reporting', async () => {
      const { approve } = approveAndConnect({
        ...unchosen,
        level: LicenseMonitoringLevel.health,
        configured: true,
      });
      await approve();

      expect(
        await screen.findByText('Connected to Dagu Console.', undefined, {
          timeout: 5000,
        })
      ).toBeVisible();
      expect(await screen.findByText('Set in configuration')).toBeVisible();
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    it('offers a link when the browser blocks the new tab', async () => {
      vi.spyOn(window, 'open').mockReturnValue(null);
      useClientMock.mockReturnValue({
        POST: vi.fn().mockResolvedValue({ data: pending }),
        GET: vi.fn().mockReturnValue(new Promise(() => {})),
      } as never);
      renderPage(community());

      await userEvent.click(connectButton());

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

      await userEvent.click(connectButton());

      expect(await screen.findByRole('alert')).toHaveTextContent(
        'this server already has an active license'
      );
      expect(tab.close).toHaveBeenCalled();
      expect(screen.getByRole('button', { name: 'Try again' })).toBeEnabled();
    });

    it('retries a failed request from its callout alone', async () => {
      vi.spyOn(window, 'open').mockReturnValue(null);
      const post = vi
        .fn()
        .mockResolvedValueOnce({ error: { message: 'Dagu Console is down' } })
        .mockResolvedValue({ data: pending });
      useClientMock.mockReturnValue({
        POST: post,
        GET: vi.fn().mockReturnValue(new Promise(() => {})),
      } as never);
      renderPage(community());

      await userEvent.click(connectButton());
      const callout = await screen.findByRole('alert');
      expect(callout).toHaveTextContent('Could not connect to Dagu Console');
      expect(
        screen.queryByRole('button', { name: 'Connect for free' })
      ).not.toBeInTheDocument();

      await userEvent.click(
        within(callout).getByRole('button', { name: 'Try again' })
      );
      const request = await screen.findByRole('region', {
        name: 'Waiting for approval',
      });
      expect(within(request).getByText('ABCD1234')).toBeVisible();
      expect(post).toHaveBeenCalledTimes(2);
      expect(
        screen.queryByRole('button', { name: 'Connect for free' })
      ).not.toBeInTheDocument();
    });

    it('shows the countdown and why a request expired', async () => {
      vi.spyOn(window, 'open').mockReturnValue(null);
      let expired = false;
      const get = vi.fn((path: string) =>
        Promise.resolve({
          data:
            path === '/license/connect'
              ? expired
                ? { state: 'expired', error: 'The request expired.' }
                : pending
              : community(),
        })
      );
      useClientMock.mockReturnValue({
        POST: vi.fn().mockResolvedValue({ data: pending }),
        GET: get,
      } as never);
      renderPage(community());
      await userEvent.click(connectButton());
      const request = await screen.findByRole('region', {
        name: 'Waiting for approval',
      });
      expect(within(request).getByText(/^Expires in \d+:\d{2}$/)).toBeVisible();

      expired = true;
      const callout = await screen.findByRole('alert', undefined, {
        timeout: 5000,
      });
      expect(callout).toHaveTextContent(
        'Could not connect to Dagu Console' + 'The request expired.'
      );
      expect(
        within(callout).getByRole('button', { name: 'Try again' })
      ).toBeEnabled();
    });

    it('waits for an activation before connecting', async () => {
      useClientMock.mockReturnValue({
        POST: vi.fn().mockReturnValue(new Promise(() => {})),
        GET: vi.fn().mockReturnValue(new Promise(() => {})),
      } as never);
      renderPage(community());

      await userEvent.type(await openKeyForm('Use a license key'), 'key');
      await userEvent.click(screen.getByRole('button', { name: 'Activate' }));

      expect(connectButton()).toBeDisabled();
      expect(
        screen.getByRole('button', { name: 'Activating...' })
      ).toBeDisabled();
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
      await userEvent.click(connectButton());
      await screen.findByText('ABCD1234');

      await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));

      expect(del).toHaveBeenCalledWith('/license/connect', {
        params: { query: { remoteNode: 'local' } },
      });
      expect(connectButton()).toBeVisible();
    });

    it('keeps waiting when the cancel does not reach the server', async () => {
      vi.spyOn(window, 'open').mockReturnValue(null);
      useClientMock.mockReturnValue({
        POST: vi.fn().mockResolvedValue({ data: pending }),
        GET: vi.fn().mockReturnValue(new Promise(() => {})),
        DELETE: vi.fn().mockRejectedValue(new TypeError('Failed to fetch')),
      } as never);
      renderPage(community());
      await userEvent.click(connectButton());
      await screen.findByText('ABCD1234');

      await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));

      expect(await screen.findByRole('alert')).toHaveTextContent(
        'Failed to fetch'
      );
      expect(screen.getByText('ABCD1234')).toBeVisible();
      expect(
        screen.queryByRole('button', { name: 'Connect for free' })
      ).not.toBeInTheDocument();
    });

    it('explains that an environment license blocks connecting', () => {
      renderPage({ ...community(), source: 'env', error: 'activation failed' });

      expect(
        screen.getByText(/license is set by DAGU_LICENSE or DAGU_LICENSE_KEY/)
      ).toBeVisible();
      expect(
        screen.queryByRole('button', { name: 'Connect for free' })
      ).not.toBeInTheDocument();
    });
  });

  describe('monitoring', () => {
    // Serves an online license and the given monitoring state, which PUT
    // changes the way the server does.
    function serveMonitoring(initial: LicenseMonitoring) {
      let monitoring = initial;
      const get = vi.fn((path: string) =>
        Promise.resolve({
          data:
            path === '/license/monitoring'
              ? monitoring
              : makeConfig(online).license,
        })
      );
      const put = vi.fn(
        (_path: string, init: { body: { level: LicenseMonitoringLevel } }) => {
          monitoring = { ...monitoring, level: init.body.level, chosen: true };
          return Promise.resolve({ data: monitoring });
        }
      );
      useClientMock.mockReturnValue({
        GET: get,
        POST: vi.fn(),
        PUT: put,
      } as never);
      renderPage(online);
      return { get, put };
    }

    it('shows what the server reports and the last report', async () => {
      serveMonitoring({
        level: LicenseMonitoringLevel.runs,
        configured: false,
        chosen: true,
        noticeDismissed: false,
        errorsOffered: false,
        lastReportAt: new Date().toISOString(),
        lastReport: '{"protocol":2,"heartbeat_secret":"[redacted]"}',
      });

      const card = await findMonitoringCard();
      expect(
        await within(card).findByText('Health and run status')
      ).toBeVisible();
      expect(within(card).getByText('Last report')).toBeVisible();
      expect(within(card).getByText(/ago/)).toBeVisible();
      expect(
        within(card).getByRole('button', { name: 'Change' })
      ).toBeVisible();
      expect(
        within(card).getByRole('button', { name: 'Turn off' })
      ).toBeVisible();

      await userEvent.click(
        within(card).getByRole('button', { name: 'View last report' })
      );
      const dialog = await screen.findByRole('dialog', { name: 'Last report' });
      expect(dialog).toHaveTextContent('"heartbeat_secret": "[redacted]"');
    });

    it('turns reporting on with the chosen level', async () => {
      const { put } = serveMonitoring(unchosen);
      const card = await findMonitoringCard();
      expect(await within(card).findByText('Off')).toBeVisible();
      expect(within(card).queryByText('Last report')).not.toBeInTheDocument();

      await userEvent.click(
        within(card).getByRole('button', { name: 'Turn on' })
      );
      const dialog = await screen.findByRole('dialog', {
        name: 'Choose what this server reports',
      });
      const health = within(dialog).getByRole('radio', { name: 'Health only' });
      const runs = within(dialog).getByRole('radio', {
        name: 'Health and run status',
      });
      const errors = within(dialog).getByRole('radio', {
        name: 'Health, run status, and error messages (recommended)',
      });
      expect(errors).toBeChecked();
      expect(errors).toHaveAccessibleDescription(
        "Sends everything Health and run status sends, plus why each failed run failed: the error messages of up to 5 failed steps and handlers, or the run's own, each up to 2 KB from its end. A step's error can end with the last lines of its stderr, with the values of workflow secrets masked."
      );
      expect(health).toHaveAccessibleDescription(
        "Sends the Dagu version, OS, architecture, process start time, service counts (schedulers holding the scheduler lock, and registered coordinators), the scheduler's last tick and whether it is paused, and how far remote access lets calls from Dagu Cloud go."
      );
      expect(runs).toHaveAccessibleDescription(
        "Sends the health fields, plus the status of each top-level DAG run that finishes, with its failed step names (up to 50) and how it was triggered, and the list of runs queued, running, or waiting. Each run is sent as its DAG name, run ID, attempt ID, status, trigger, and queued, started, and finished times. Also each scheduled DAG's next run, and whether its latest scheduled run started. No parameters, logs, or outputs."
      );
      expect(dialog).toHaveTextContent(
        'Never sent: full logs, outputs, parameters, environment, step commands, DAG YAML, schedule expressions, and the values of workflow secrets.'
      );
      expect(dialog).toHaveTextContent(
        'the protocol version, license ID, server ID, and heartbeat secret'
      );
      expect(dialog).toHaveTextContent(
        'Turning reporting off sends one last report that carries only what identifies and authenticates this server and the choice Off'
      );

      await userEvent.click(health);
      await userEvent.click(
        within(dialog).getByRole('button', { name: 'Turn on' })
      );

      expect(put).toHaveBeenCalledWith('/license/monitoring', {
        params: { query: { remoteNode: 'local' } },
        body: { level: 'health' },
      });
      expect(await within(card).findByText('Health only')).toBeVisible();
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    it('turns reporting off', async () => {
      const { put } = serveMonitoring({
        ...unchosen,
        level: LicenseMonitoringLevel.health,
        chosen: true,
      });
      const card = await findMonitoringCard();

      await userEvent.click(
        await within(card).findByRole('button', { name: 'Turn off' })
      );

      expect(put).toHaveBeenCalledWith('/license/monitoring', {
        params: { query: { remoteNode: 'local' } },
        body: { level: 'off' },
      });
      expect(
        await within(card).findByRole('button', { name: 'Turn on' })
      ).toBeVisible();
    });

    it('shows a level set in the configuration read-only', async () => {
      serveMonitoring({
        ...unchosen,
        level: LicenseMonitoringLevel.health,
        configured: true,
      });
      const card = await findMonitoringCard();

      expect(
        await within(card).findByText('Set in configuration')
      ).toBeVisible();
      expect(within(card).getByText('Health only')).toBeVisible();
      expect(
        within(card).getByText(
          'cloud.report in the server configuration sets this. Change it there and restart Dagu.'
        )
      ).toBeVisible();
      for (const name of ['Turn on', 'Change', 'Turn off']) {
        expect(
          within(card).queryByRole('button', { name })
        ).not.toBeInTheDocument();
      }
    });

    it('says why turning reporting off failed', async () => {
      serveMonitoring({
        ...unchosen,
        level: LicenseMonitoringLevel.health,
        chosen: true,
      }).put.mockResolvedValue({
        error: { message: 'config is read-only' },
      } as never);
      const card = await findMonitoringCard();

      await userEvent.click(
        await within(card).findByRole('button', { name: 'Turn off' })
      );

      expect(await within(card).findByRole('alert')).toHaveTextContent(
        'config is read-only'
      );
      expect(within(card).getByText('Health only')).toBeVisible();
    });

    it('offers a retry when monitoring cannot be read', async () => {
      let fail = true;
      const get = vi.fn((path: string) =>
        Promise.resolve(
          path === '/license/monitoring'
            ? fail
              ? { error: { message: 'unreachable' } }
              : { data: { ...unchosen, chosen: true } }
            : { data: makeConfig(online).license }
        )
      );
      useClientMock.mockReturnValue({ GET: get, POST: vi.fn() } as never);
      renderPage(online);
      const card = await findMonitoringCard();

      expect(
        await within(card).findByText('Monitoring status unavailable')
      ).toBeVisible();
      fail = false;
      await userEvent.click(
        within(card).getByRole('button', { name: 'Retry' })
      );

      expect(
        await within(card).findByRole('button', { name: 'Turn on' })
      ).toBeVisible();
    });

    it('says when the server stopped reporting', async () => {
      serveMonitoring({
        level: LicenseMonitoringLevel.runs,
        configured: false,
        chosen: true,
        noticeDismissed: false,
        errorsOffered: false,
        lastReportAt: new Date(Date.now() - 11 * 60_000).toISOString(),
      });
      const card = await findMonitoringCard();

      expect(
        await within(card).findByText(
          /^Not reporting since \d{4}-\d{2}-\d{2} \d{2}:\d{2}$/
        )
      ).toBeVisible();
    });

    it('shows when the last report arrived while reports arrive', async () => {
      serveMonitoring({
        level: LicenseMonitoringLevel.runs,
        configured: false,
        chosen: true,
        noticeDismissed: false,
        errorsOffered: false,
        lastReportAt: new Date(Date.now() - 9 * 60_000).toISOString(),
      });
      const card = await findMonitoringCard();

      expect(await within(card).findByText('9 minutes ago')).toBeVisible();
      expect(within(card).queryByText(/Not reporting/)).not.toBeInTheDocument();
    });

    it('says until when Dagu Console pauses storing finished runs', async () => {
      const until = new Date(Date.now() + 3 * 60 * 60_000);
      serveMonitoring({
        level: LicenseMonitoringLevel.runs,
        configured: false,
        chosen: true,
        noticeDismissed: false,
        errorsOffered: false,
        lastReportAt: new Date().toISOString(),
        runsPausedUntil: until.toISOString(),
      });
      const card = await findMonitoringCard();

      expect(
        await within(card).findByText(
          `Run history paused until ${dayjs(until).format('YYYY-MM-DD HH:mm')}`
        )
      ).toBeVisible();
    });

    it('does not mention a pause that has ended', async () => {
      serveMonitoring({
        level: LicenseMonitoringLevel.runs,
        configured: false,
        chosen: true,
        noticeDismissed: false,
        errorsOffered: false,
        lastReportAt: new Date().toISOString(),
        runsPausedUntil: new Date(Date.now() - 60_000).toISOString(),
      });
      const card = await findMonitoringCard();

      expect(
        await within(card).findByText('Health and run status')
      ).toBeVisible();
      expect(
        within(card).queryByText(/Run history paused/)
      ).not.toBeInTheDocument();
    });

    it('is not offered for a license that does not check in', () => {
      const get = vi.fn().mockReturnValue(new Promise(() => {}));
      useClientMock.mockReturnValue({ GET: get, POST: vi.fn() } as never);
      renderPage();

      expect(
        screen.queryByRole('heading', { name: 'Monitoring' })
      ).not.toBeInTheDocument();
      expect(get).not.toHaveBeenCalledWith(
        '/license/monitoring',
        expect.anything()
      );
    });
  });

  describe('remote access', () => {
    const remoteOff: LicenseRemoteAccess = {
      level: LicenseRemoteAccessLevel.off,
      levelConfigured: false,
      approvals: false,
      approvalsConfigured: false,
      connected: false,
    };

    // Serves an online license with audit logs, the given remote access,
    // which PUT changes the way the server does, and the given audit entries.
    function serveRemoteAccess(initial: LicenseRemoteAccess, entries = []) {
      let access = initial;
      const get = vi.fn((path: string) =>
        Promise.resolve({
          data:
            path === '/license/remote-access'
              ? access
              : path === '/license/monitoring'
                ? unchosen
                : path === '/audit'
                  ? { entries, total: entries.length }
                  : makeConfig(online).license,
        })
      );
      const put = vi.fn(
        (
          _path: string,
          init: {
            body: { level?: LicenseRemoteAccessLevel; approvals?: boolean };
          }
        ) => {
          access = { ...access, ...init.body };
          return Promise.resolve({ data: access });
        }
      );
      useClientMock.mockReturnValue({
        GET: get,
        POST: vi.fn(),
        PUT: put,
      } as never);
      renderPage(online);
      return { get, put };
    }

    function findRemoteAccessCard() {
      return screen.findByRole('region', { name: 'Remote access' });
    }

    it('turns remote access on with the chosen role', async () => {
      const { put } = serveRemoteAccess(remoteOff);
      const card = await findRemoteAccessCard();
      await within(card).findByText('Off');
      expect(within(card).queryByText('Connection')).not.toBeInTheDocument();

      await userEvent.click(
        within(card).getByRole('button', { name: 'Turn on' })
      );
      const dialog = await screen.findByRole('dialog', {
        name: 'Choose remote access',
      });
      expect(
        within(dialog).getByRole('radio', { name: 'Viewer' })
      ).toBeChecked();
      const operator = within(dialog).getByRole('radio', { name: 'Operator' });
      expect(operator).toHaveAccessibleDescription(
        'Also start, enqueue, retry, and stop runs of existing DAGs, with any parameters those DAGs take.'
      );
      await userEvent.click(operator);
      await userEvent.click(
        within(dialog).getByRole('button', { name: 'Turn on' })
      );

      expect(put).toHaveBeenCalledWith('/license/remote-access', {
        params: { query: { remoteNode: 'local' } },
        body: { level: 'operator' },
      });
      expect(await within(card).findByText('Operator')).toBeVisible();
      expect(within(card).getByText('Connecting…')).toBeVisible();
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    it('warns that developer runs any command on the machine', async () => {
      serveRemoteAccess(remoteOff);
      const card = await findRemoteAccessCard();
      await userEvent.click(
        await within(card).findByRole('button', { name: 'Turn on' })
      );
      const dialog = await screen.findByRole('dialog', {
        name: 'Choose remote access',
      });
      expect(within(dialog).queryByRole('alert')).not.toBeInTheDocument();
      await userEvent.click(
        within(dialog).getByRole('radio', { name: 'Developer' })
      );
      expect(within(dialog).getByRole('alert')).toHaveTextContent(
        'run any command on this machine, as the user Dagu runs as'
      );
    });

    it('lets approvals be turned on and shows the connection', async () => {
      const { put } = serveRemoteAccess({
        ...remoteOff,
        level: LicenseRemoteAccessLevel.operator,
        connected: true,
      });
      const card = await findRemoteAccessCard();
      expect(await within(card).findByText('Connected')).toBeVisible();

      const approvals = within(card).getByRole('switch', { name: 'Approvals' });
      expect(approvals).not.toBeChecked();
      await userEvent.click(approvals);
      expect(put).toHaveBeenCalledWith('/license/remote-access', {
        params: { query: { remoteNode: 'local' } },
        body: { approvals: true },
      });
      await waitFor(() => expect(approvals).toBeChecked());
    });

    it('says why Dagu Cloud refused the connection', async () => {
      serveRemoteAccess({
        ...remoteOff,
        level: LicenseRemoteAccessLevel.viewer,
        error: 'Access from Dagu Cloud needs a plan or a trial.',
      });
      const card = await findRemoteAccessCard();
      expect(
        await within(card).findByText(
          'Access from Dagu Cloud needs a plan or a trial.'
        )
      ).toBeVisible();
    });

    it('lists the calls agents made lately', async () => {
      serveRemoteAccess(
        { ...remoteOff, level: LicenseRemoteAccessLevel.operator },
        [
          {
            id: '2',
            timestamp: new Date().toISOString(),
            category: 'mcp',
            action: 'mcp.tool_call.denied',
            mcpTool: 'dagu_change',
            username: 'ana@example.com',
            details: '{"mcp_action":"upsert_dag","resource_id":"etl"}',
          },
          {
            id: '1',
            timestamp: new Date().toISOString(),
            category: 'mcp',
            action: 'mcp.tool_call.started',
            mcpTool: 'dagu_execute',
            username: 'ana@example.com',
          },
        ] as never
      );
      const card = await findRemoteAccessCard();
      const activity = await within(card).findByText(
        'dagu_change upsert_dag etl'
      );
      expect(activity.closest('li')).toHaveTextContent('Denied');
      expect(within(card).getAllByRole('listitem')).toHaveLength(1);
    });

    it('leaves configured settings read-only', async () => {
      serveRemoteAccess({
        ...remoteOff,
        level: LicenseRemoteAccessLevel.viewer,
        levelConfigured: true,
        approvalsConfigured: true,
        connected: true,
      });
      const card = await findRemoteAccessCard();
      await within(card).findByText('Viewer');
      expect(within(card).queryByRole('button', { name: 'Change' })).toBeNull();
      expect(within(card).queryByRole('switch')).toBeNull();
      expect(card).toHaveTextContent(
        'cloud.remote_access and cloud.remote_approvals in the server configuration set these.'
      );
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
    // The first status read answers at once; the one after activating
    // waits. Other reads never answer.
    const get = vi.fn((path: string) => {
      if (path !== '/license/status') return new Promise(() => {});
      return statusReads() === 1
        ? Promise.resolve({ data: community })
        : pending;
    });
    const statusReads = () =>
      get.mock.calls.filter(([path]) => path === '/license/status').length;
    useClientMock.mockReturnValue({
      POST: vi.fn().mockResolvedValue({
        data: { plan: 'team', features: status.features },
      }),
      GET: get,
    } as never);
    renderPage(community);
    await waitFor(() => expect(statusReads()).toBe(1));
    await userEvent.type(await openKeyForm('Use a license key'), 'key');
    await userEvent.click(screen.getByRole('button', { name: 'Activate' }));
    await waitFor(() => expect(statusReads()).toBe(2));
    expect(screen.getByText('Community', { exact: true })).toBeVisible();
    await act(async () => {
      resolve({ data: status });
      await pending;
    });
    await waitFor(() =>
      expect(row('Status')).toHaveTextContent('Needs attention')
    );
    expect(row('Plan')).toHaveTextContent('Team plan');
  });

  describe('by state', () => {
    const day = 86_400_000;
    // Ends an hour inside the given number of days, so the count of days
    // left does not depend on when the test runs.
    const inDays = (days: number) =>
      new Date(Date.now() + days * day - 3_600_000).toISOString();

    function callouts() {
      return screen
        .queryAllByRole('heading', { level: 5 })
        .map((title) => title.closest<HTMLElement>('[role]')!);
    }

    it('offers a community server one way to connect, with the trial', () => {
      renderPage({
        valid: false,
        community: true,
        plan: '',
        features: [],
        expiry: '',
        source: '',
      });

      expect(screen.getAllByRole('button', { name: /^Connect / })).toHaveLength(
        1
      );
      expect(
        screen.getByRole('checkbox', {
          name: /Also try paid features free for 14 days/,
        })
      ).not.toBeChecked();
      expect(
        screen.queryByRole('link', { name: 'Start free trial' })
      ).not.toBeInTheDocument();
      expect(
        screen.queryByRole('link', { name: 'Manage in Dagu Console' })
      ).not.toBeInTheDocument();
      expect(row('Status')).toHaveTextContent('Community');
      expect(
        within(row('Status')).getByRole('link', { name: 'View plans' })
      ).toHaveAttribute('href', expect.stringContaining('dagu.sh/pricing'));
      expect(row('Connection')).toHaveTextContent('Not connected');
      expect(callouts()).toHaveLength(0);
      expect(
        screen.queryByRole('region', { name: 'Danger zone' })
      ).not.toBeInTheDocument();
    });

    it('links a licensed server to Dagu Console and nothing else', () => {
      renderPage({
        ...online,
        consoleUrl: 'https://console.dagu.sh/servers?server=srv-123',
      });

      expect(
        screen.getByRole('link', { name: 'Manage in Dagu Console' })
      ).toHaveAttribute(
        'href',
        'https://console.dagu.sh/servers?server=srv-123'
      );
      expect(
        screen.queryByRole('button', { name: 'Connect for free' })
      ).not.toBeInTheDocument();
      expect(callouts()).toHaveLength(0);
      expect(row('Status')).toHaveTextContent('Active');
    });

    it('treats a free license as monitoring only', () => {
      renderPage({
        ...freeOnline,
        expiry: inDays(2),
        consoleUrl: 'https://console.dagu.sh/servers?server=srv-123',
      });

      expect(
        screen.getByRole('link', { name: 'Manage in Dagu Console' })
      ).toHaveAttribute(
        'href',
        'https://console.dagu.sh/servers?server=srv-123'
      );
      expect(
        screen.getByRole('link', { name: 'Start free trial' })
      ).toHaveAttribute(
        'href',
        'https://console.dagu.sh/servers?server=srv-123'
      );
      expect(
        screen.queryByRole('button', { name: 'Connect for free' })
      ).not.toBeInTheDocument();
      expect(
        screen.queryByRole('region', { name: 'Monitor this server for free' })
      ).not.toBeInTheDocument();
      expect(row('Plan')).toHaveTextContent('Free plan');
      expect(row('Status')).toHaveTextContent('Monitoring only');
      expect(
        within(overview()).queryByText('Expires', { selector: 'dt' })
      ).not.toBeInTheDocument();
      expect(callouts()).toHaveLength(0);
      expect(screen.getAllByText('Included in Team and Pro')).toHaveLength(5);
      expect(screen.queryByText('Included')).not.toBeInTheDocument();
    });

    it('calls out no expiry of a free license', () => {
      renderPage({
        ...freeOnline,
        valid: false,
        gracePeriod: true,
        graceEndsAt: '2099-05-14T00:00:00Z',
      });

      expect(callouts()).toHaveLength(0);
      expect(row('Status')).toHaveTextContent('Monitoring only');
    });

    it('says that disconnecting a free server stops monitoring', async () => {
      renderPage(freeOnline);

      await userEvent.click(screen.getByRole('button', { name: 'Disconnect' }));

      const dialog = await screen.findByRole('dialog');
      expect(dialog).toHaveTextContent(
        'Dagu Console stops monitoring this server, and its free slot becomes available to another server.'
      );
      expect(dialog).not.toHaveTextContent('Paid features');
    });

    it('stays quiet more than 30 days before a paid license expires', () => {
      renderPage({ ...online, expiry: inDays(31) });

      expect(callouts()).toHaveLength(0);
      expect(row('Status')).toHaveTextContent('Active');
    });

    it.each([
      [
        'a trial within three days',
        { plan: 'trial', expiry: inDays(2) },
        'status',
        'License expires in 2 days',
        'Licensed features stop working when it expires.',
        'View plans',
      ],
      [
        'a paid license within 30 days',
        { expiry: inDays(30) },
        'status',
        'License expires in 30 days',
        'Licensed features stop working when it expires.',
        'Renew license',
      ],
      [
        'the grace period',
        {
          valid: false,
          gracePeriod: true,
          graceEndsAt: '2099-05-14T00:00:00Z',
        },
        'status',
        'License expired',
        'Licensed features keep working until 2099-05-14.',
        'Renew license',
      ],
      [
        'an expired license',
        { valid: false },
        'alert',
        'License expired',
        'Licensed features are unavailable.',
        'Renew license',
      ],
      [
        'a warning from Dagu Console',
        { warningCode: 'MACHINE_LIMIT_EXCEEDED' },
        'status',
        'License needs attention',
        'This license is active on more machines than allowed. Deactivate extra machines in Dagu Console.',
        'Manage in Dagu Console',
      ],
      [
        'a license error',
        { error: 'License token verification failed.' },
        'alert',
        'License error',
        'License token verification failed.',
        'Manage license',
      ],
    ])(
      'calls out %s with one action',
      (_case, license, role, title, detail, action) => {
        renderPage({ ...online, ...license });

        const shown = callouts();
        expect(shown).toHaveLength(1);
        const callout = shown[0]!;
        expect(callout).toHaveAttribute('role', role);
        expect(callout).toHaveTextContent(title + detail + action);
        const links = within(callout).getAllByRole('link');
        expect(links).toHaveLength(1);
        expect(links[0]).toHaveTextContent(action);
        // Only the header's link to Dagu Console may repeat the action.
        expect(screen.getAllByRole('link', { name: action })).toHaveLength(
          action === 'Manage in Dagu Console' ? 2 : 1
        );
      }
    );

    it('states each fact once', () => {
      renderPage({
        ...online,
        plan: 'team',
        serverName: 'build-01',
        workspace: 'acme',
        licenseId: 'lic-456',
      });

      for (const fact of [
        'Team plan',
        'Active',
        '2099-04-30',
        'acme',
        'build-01',
        'Connected via Dagu Console',
        'srv-123',
        'lic-456',
      ]) {
        expect(screen.getAllByText(fact)).toHaveLength(1);
      }
    });

    it('offers a retry when the status cannot be read', async () => {
      const get = vi
        .fn()
        .mockResolvedValueOnce({ error: { message: 'unreachable' } })
        .mockResolvedValue({ data: makeConfig().license });
      useClientMock.mockReturnValue({ GET: get, POST: vi.fn() } as never);
      renderPage();

      const callout = await screen.findByRole('status');
      expect(callout).toHaveTextContent('License status unavailable');
      expect(
        screen.queryByRole('region', { name: 'Overview' })
      ).not.toBeInTheDocument();
      await userEvent.click(
        within(callout).getByRole('button', { name: 'Retry' })
      );

      await waitFor(() => expect(row('Status')).toHaveTextContent('Active'));
      expect(get).toHaveBeenCalledTimes(2);
    });

    it('keeps disconnecting apart in the danger zone', () => {
      renderPage(online);

      const zone = screen.getByRole('region', { name: 'Danger zone' });
      expect(zone).toHaveTextContent('Disconnect this server');
      expect(
        within(zone).getByRole('button', { name: 'Disconnect' })
      ).toBeEnabled();
    });
  });

  describe('license key', () => {
    it('stays collapsed until asked for', async () => {
      renderPage();
      expect(screen.queryByLabelText('License key')).not.toBeInTheDocument();
      const open = screen.getByRole('button', {
        name: 'Use a different license key',
      });
      expect(open).toHaveAttribute('aria-expanded', 'false');

      await userEvent.click(open);
      expect(screen.getByLabelText('License key')).toHaveFocus();
      expect(screen.getByRole('button', { name: 'Activate' })).toBeDisabled();

      await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
      expect(screen.queryByLabelText('License key')).not.toBeInTheDocument();
    });

    it('offers a key to a community server', async () => {
      renderPage({ valid: false, community: true, plan: '', features: [] });

      expect(await openKeyForm('Use a license key')).toBeVisible();
    });

    it('opens again from a repeated link to #activate', async () => {
      renderPage(
        { valid: false, community: true, plan: '', features: [] },
        {
          path: '/license#activate',
          children: <Link to="/license#activate">Activate a key</Link>,
        }
      );
      await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
      expect(screen.queryByLabelText('License key')).not.toBeInTheDocument();

      await userEvent.click(
        screen.getByRole('link', { name: 'Activate a key' })
      );

      expect(screen.getByLabelText('License key')).toHaveFocus();
    });

    it('opens from a link to #activate', () => {
      renderPage(
        { valid: false, community: true, plan: '', features: [] },
        { path: '/license#activate' }
      );

      expect(screen.getByLabelText('License key')).toHaveFocus();
    });
  });

  // The sidebar and the monitoring offers link to #monitoring.
  it('scrolls to free monitoring from a link to #monitoring', () => {
    const scroll = vi.spyOn(Element.prototype, 'scrollIntoView');

    renderPage(
      { valid: false, community: true, plan: '', features: [] },
      { path: '/license#monitoring' }
    );

    expect(scroll.mock.contexts).toContain(
      screen.getByRole('region', { name: 'Monitor this server for free' })
    );
    scroll.mockRestore();
  });
});
