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
      POST: vi
        .fn()
        .mockResolvedValue({
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
