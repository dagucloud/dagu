// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { UserAuthProvider, UserRole, type components } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import UsersPage from '..';
import { UserFormModal } from '../UserFormModal';

vi.mock('@/contexts/AuthContext', () => ({
  TOKEN_KEY: 'daguToken',
  useAuth: () => ({ user: { id: 'admin' } }),
  useIsAdmin: () => true,
}));

type User = components['schemas']['User'];
type UsersListResponse = components['schemas']['UsersListResponse'];

function makeConfig(): Config {
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
    oidcEnabled: true,
    oidcButtonLabel: 'Login with SSO',
    proxyEnabled: true,
    proxyButtonLabel: 'Continue with SSO',
    terminalEnabled: false,
    gitSyncEnabled: false,
    updateAvailable: false,
    latestVersion: '',
    permissions: { writeDags: true, runDags: true },
    license: {
      valid: true,
      plan: 'pro',
      expiry: '2027-01-01T00:00:00Z',
      features: ['rbac'],
      gracePeriod: false,
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
  };
}

function makeUser(overrides: Partial<User> = {}): User {
  return {
    id: 'oidc-user',
    username: 'oidc-user@example.com',
    role: UserRole.viewer,
    workspaceAccess: {
      all: false,
      grants: [{ workspace: 'payments', role: UserRole.operator }],
    },
    authProvider: UserAuthProvider.oidc,
    isDisabled: false,
    createdAt: '2026-07-20T00:00:00Z',
    updatedAt: '2026-07-20T00:00:00Z',
    ...overrides,
  };
}

const appBarValue = {
  title: '',
  setTitle: () => undefined,
  remoteNodes: ['local'],
  setRemoteNodes: () => undefined,
  selectedRemoteNode: 'local',
  selectRemoteNode: () => undefined,
  workspaces: [{ id: 'payments', name: 'payments' }],
};

function renderPage(response: components['schemas']['UsersListResponse']) {
  const fetchMock = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => response,
  });
  vi.stubGlobal('fetch', fetchMock);

  render(
    <MemoryRouter>
      <ConfigContext.Provider value={makeConfig()}>
        <AppBarContext.Provider value={appBarValue}>
          <UsersPage />
        </AppBarContext.Provider>
      </ConfigContext.Provider>
    </MemoryRouter>
  );

  return fetchMock;
}

describe('UsersPage', () => {
  beforeEach(() => {
    localStorage.setItem('daguToken', 'test-token');
    localStorage.setItem('dagu_auth_token', 'test-token');
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  it('marks OIDC users as managed when the selected node enables access sync', async () => {
    renderPage({
      users: [
        makeUser(),
        makeUser({
          id: 'local-user',
          username: 'local-user',
          authProvider: UserAuthProvider.builtin,
        }),
      ],
      oidcWorkspaceAccessSyncEnabled: true,
      managedAuthorizationProviders: [UserAuthProvider.oidc],
    });

    expect(await screen.findByText('Managed by SSO')).toBeVisible();
    expect(screen.getByText('Local')).toBeVisible();
  });

  it('treats an empty managed-provider list as unmanaged', async () => {
    renderPage({ users: [makeUser()], managedAuthorizationProviders: [] });

    expect(await screen.findByText('SSO')).toBeVisible();
    expect(screen.queryByText('Managed by SSO')).not.toBeInTheDocument();
  });

  it('supports older nodes that only report the OIDC sync flag', async () => {
    renderPage({
      users: [makeUser()],
      oidcWorkspaceAccessSyncEnabled: true,
    } as UsersListResponse);

    expect(await screen.findByText('Managed by SSO')).toBeVisible();
  });

  it('does not offer local password reset for OIDC users', async () => {
    const user = userEvent.setup();
    renderPage({
      users: [makeUser()],
      oidcWorkspaceAccessSyncEnabled: true,
      managedAuthorizationProviders: [UserAuthProvider.oidc],
    });

    await user.click(
      await screen.findByRole('button', {
        name: 'Actions for oidc-user@example.com',
      })
    );

    expect(
      screen.queryByRole('menuitem', { name: 'Reset Password' })
    ).not.toBeInTheDocument();
  });

  it('marks proxy authorization as managed and hides password reset', async () => {
    const user = userEvent.setup();
    renderPage({
      users: [
        makeUser({
          id: 'proxy-user',
          username: 'proxy-user',
          authProvider: UserAuthProvider.proxy,
        }),
      ],
      managedAuthorizationProviders: [UserAuthProvider.proxy],
    });

    expect(await screen.findByText('Managed by Proxy')).toBeVisible();
    await user.click(
      screen.getByRole('button', { name: 'Actions for proxy-user' })
    );
    expect(
      screen.queryByRole('menuitem', { name: 'Reset Password' })
    ).not.toBeInTheDocument();
  });

  it('offers password reset for local users', async () => {
    const user = userEvent.setup();
    renderPage({
      users: [
        makeUser({
          id: 'local-user',
          username: 'local-user',
          authProvider: UserAuthProvider.builtin,
        }),
      ],
      managedAuthorizationProviders: [],
    });

    await user.click(
      await screen.findByRole('button', { name: 'Actions for local-user' })
    );

    expect(
      screen.getByRole('menuitem', { name: 'Reset Password' })
    ).toBeVisible();
  });

  it('clears managed user state when a node refresh fails', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({
          users: [makeUser()],
          oidcWorkspaceAccessSyncEnabled: true,
          managedAuthorizationProviders: [UserAuthProvider.oidc],
        }),
      })
      .mockResolvedValueOnce({ ok: false });
    vi.stubGlobal('fetch', fetchMock);

    const view = render(
      <MemoryRouter>
        <ConfigContext.Provider value={makeConfig()}>
          <AppBarContext.Provider value={appBarValue}>
            <UsersPage />
          </AppBarContext.Provider>
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    expect(await screen.findByText('Managed by SSO')).toBeVisible();

    view.rerender(
      <MemoryRouter>
        <ConfigContext.Provider value={makeConfig()}>
          <AppBarContext.Provider
            value={{ ...appBarValue, selectedRemoteNode: 'remote-a' }}
          >
            <UsersPage />
          </AppBarContext.Provider>
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    expect(await screen.findByText('Failed to fetch users')).toBeVisible();
    expect(screen.queryByText('oidc-user@example.com')).not.toBeInTheDocument();
    expect(screen.queryByText('Managed by SSO')).not.toBeInTheDocument();
  });

  it('keeps identity fields editable while omitting managed access from PATCH', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true });
    vi.stubGlobal('fetch', fetchMock);
    const onSuccess = vi.fn();
    const user = userEvent.setup();

    render(
      <ConfigContext.Provider value={makeConfig()}>
        <AppBarContext.Provider value={appBarValue}>
          <UserFormModal
            open
            user={makeUser({
              workspaceAccess: {
                all: false,
                grants: [
                  { workspace: 'future-workspace', role: UserRole.developer },
                ],
              },
            })}
            managedAuthorizationProviders={[UserAuthProvider.oidc]}
            onClose={() => undefined}
            onSuccess={onSuccess}
          />
        </AppBarContext.Provider>
      </ConfigContext.Provider>
    );

    expect(screen.getByLabelText('Role managed by SSO')).toHaveTextContent(
      'viewer'
    );
    expect(screen.getByText('future-workspace')).toBeVisible();
    expect(screen.getByText('Workspace not currently available')).toBeVisible();

    const username = screen.getByLabelText('Username');
    await user.clear(username);
    await user.type(username, 'renamed@example.com');
    await user.click(screen.getByRole('button', { name: 'Update' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledOnce());
    const request = fetchMock.mock.calls[0]?.[1] as RequestInit;
    expect(request.method).toBe('PATCH');
    expect(JSON.parse(request.body as string)).toEqual({
      username: 'renamed@example.com',
    });
    expect(onSuccess).toHaveBeenCalledOnce();
  });

  it('renders proxy authorization as read-only when managed', () => {
    render(
      <ConfigContext.Provider value={makeConfig()}>
        <AppBarContext.Provider value={appBarValue}>
          <UserFormModal
            open
            user={makeUser({
              authProvider: UserAuthProvider.proxy,
              username: 'proxy-user',
            })}
            managedAuthorizationProviders={[UserAuthProvider.proxy]}
            onClose={() => undefined}
            onSuccess={() => undefined}
          />
        </AppBarContext.Provider>
      </ConfigContext.Provider>
    );

    expect(
      screen.getByLabelText('Role managed by Proxy')
    ).toBeVisible();
    expect(screen.getByText('Managed by Proxy')).toBeVisible();
  });
});
