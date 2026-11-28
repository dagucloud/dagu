// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  ConnectionAccess,
  ConnectionAuthorizationPhase,
  ConnectionGrant,
  ConnectionProviderName,
  ConnectionRedirectMode,
  ConnectionStatus,
  OAuthClientType,
  UserRole,
  type components,
} from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import ConnectionsPage from '..';
import ConnectionPage from '../ConnectionPage';

type Connection = components['schemas']['Connection'];
type Handler = (init: { params?: unknown; body?: unknown }) => unknown;

// The requests that DELETE .../authorization received.
let deletes: { params?: unknown }[] = [];

const mocks = vi.hoisted(() => ({
  routes: {} as Record<string, Handler>,
  events: [] as string[],
  role: 'manager',
}));

function call(method: string) {
  return async (path: string, init: { params?: unknown; body?: unknown }) => {
    const key = `${method} ${path}`;
    mocks.events.push(key);
    const handler = mocks.routes[key];
    if (!handler) {
      return {
        error: { message: `no route ${key}` },
        response: { status: 404 },
      };
    }
    return { data: await handler(init ?? {}), response: { status: 200 } };
  };
}

vi.mock('@/hooks/api', async () => {
  const { default: useSWR } = await import('swr');
  const client = {
    GET: call('GET'),
    POST: call('POST'),
    DELETE: call('DELETE'),
  };
  return {
    useClient: () => client,
    // As in swr-openapi, null params skip the request.
    useQuery: (path: string, params: unknown, options: object) =>
      useSWR(
        params === null ? null : [path, params],
        async () => {
          const result = await client.GET(path, params as never);
          if (result.error) throw result.error;
          return result.data;
        },
        options
      ),
  };
});
vi.mock('@/contexts/AuthContext', () => ({
  useAuth: () => ({ user: { role: mocks.role } }),
  useIsAdmin: () => mocks.role === 'admin',
}));
vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ authMode: 'builtin' }),
}));

function connection(overrides: Partial<Connection> = {}): Connection {
  return {
    name: 'work',
    workspace: 'ops',
    provider: ConnectionProviderName.google,
    app: 'app-1',
    grant: ConnectionGrant.authorization_code,
    services: [
      { name: 'gmail', access: ConnectionAccess.read, missingScopes: [] },
    ],
    scopes: [],
    boundHosts: [],
    allowTokenExport: false,
    status: ConnectionStatus.connected,
    account: 'ops@example.com',
    grantedScopes: ['openid', 'email'],
    refreshTokenConfigured: true,
    createdAt: '2026-11-01T00:00:00Z',
    updatedAt: '2026-11-01T00:00:00Z',
    ...overrides,
  };
}

function renderAt(path: string) {
  return render(
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
      <AppBarContext.Provider
        value={
          {
            setTitle: vi.fn(),
            selectedRemoteNode: 'local',
            workspaces: [{ id: 'w1', name: 'ops' }],
          } as never
        }
      >
        <MemoryRouter initialEntries={[path]}>
          <Routes>
            <Route path="/connections" element={<ConnectionsPage />} />
            <Route path="/connections/:name" element={<ConnectionPage />} />
          </Routes>
        </MemoryRouter>
      </AppBarContext.Provider>
    </SWRConfig>
  );
}

beforeEach(() => {
  deletes = [];
  mocks.routes = {};
  mocks.events = [];
  mocks.role = UserRole.manager;
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

const AUTHORIZATION_URL = 'https://accounts.google.com/o/oauth2/v2/auth?x=1';
const CALLBACK_URI = 'https://dagu.example.com/oauth/callback';

/** Routes for a reconnect of a web client in server callback mode. */
function serverCallbackRoutes(start: Handler = () => serverCallbackStart()) {
  mocks.routes['GET /connections/{name}'] = () => connection();
  mocks.routes['GET /connections/{name}/tools'] = () => ({ servers: [] });
  mocks.routes['GET /connection-providers'] = () => ({
    redirects: [
      {
        mode: ConnectionRedirectMode.server_callback,
        clientType: OAuthClientType.web,
        redirectUri: CALLBACK_URI,
      },
    ],
    providers: [],
  });
  mocks.routes['GET /oauth-apps/{appId}'] = () => ({
    id: 'app-1',
    clientId: 'web.apps.googleusercontent.com',
    clientType: OAuthClientType.web,
  });
  mocks.routes['POST /connections/{name}/authorization'] = start;
  mocks.routes['DELETE /connections/{name}/authorization'] = (init) => {
    deletes.push(init);
    return undefined;
  };
}

function serverCallbackStart() {
  return {
    session: 's1',
    authorizationUrl: AUTHORIZATION_URL,
    mode: ConnectionRedirectMode.server_callback,
    redirectUri: CALLBACK_URI,
    clientId: 'web.apps.googleusercontent.com',
    expiresAt: new Date(Date.now() + 10 * 60_000).toISOString(),
  };
}

function stubPopup() {
  const popup = { opener: {}, location: { href: '' }, close: vi.fn() };
  vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window);
  return popup;
}

/** How many times the dialog read the session's state. */
function reads(): number {
  return mocks.events.filter(
    (event) => event === 'GET /connections/{name}/authorization'
  ).length;
}

/** The session handles that DELETE .../authorization cancelled. */
function cancelled(): unknown[] {
  return deletes.map(
    (init) => (init.params as { query: { session: string } }).query.session
  );
}

async function openDialog(user: ReturnType<typeof userEvent.setup>) {
  renderAt('/connections/work?workspace=ops');
  await user.click(await screen.findByRole('button', { name: 'Reconnect' }));
  const dialog = await screen.findByRole('dialog');
  await within(dialog).findByText(CALLBACK_URI);
  return dialog;
}

describe('ConnectionsPage', () => {
  it('lists the workspace connections', async () => {
    mocks.routes['GET /connections'] = () => ({
      connections: [
        connection(),
        connection({
          name: 'mail',
          account: undefined,
          status: ConnectionStatus.needs_reconnect,
        }),
      ],
    });

    renderAt('/connections?workspace=ops');

    const link = await screen.findByRole('link', { name: 'work' });
    expect(link).toHaveAttribute('href', '/connections/work?workspace=ops');
    const row = link.closest('tr')!;
    expect(within(row).getByText('Google Workspace')).toBeVisible();
    expect(within(row).getByText('ops@example.com')).toBeVisible();
    expect(within(row).getByText('Connected')).toBeVisible();
    const other = screen.getByRole('link', { name: 'mail' }).closest('tr')!;
    expect(within(other).getByText('Needs reconnect')).toBeVisible();
  });
});

describe('ConnectionPage', () => {
  it('shows status, reason, and missing scopes to a viewer', async () => {
    mocks.role = UserRole.viewer;
    mocks.routes['GET /connections/{name}'] = () =>
      connection({
        status: ConnectionStatus.needs_reconnect,
        statusReason: 'invalid_grant: Token has been expired or revoked.',
        services: [
          {
            name: 'gmail',
            access: ConnectionAccess.write,
            missingScopes: ['https://www.googleapis.com/auth/gmail.modify'],
          },
        ],
      });

    renderAt('/connections/work?workspace=ops');

    expect(
      await screen.findByText(
        'invalid_grant: Token has been expired or revoked.'
      )
    ).toBeVisible();
    expect(screen.getByText('Needs reconnect')).toBeVisible();
    expect(
      screen.getByText('https://www.googleapis.com/auth/gmail.modify')
    ).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Reconnect' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Remove' })).toBeNull();
  });

  // A paste session on a desktop client: the popup opens before the start
  // request, the redirect URI and client ID show before consent, and the
  // pasted address leads to the account to confirm.
  it('reconnects through a pasted redirect', async () => {
    const user = userEvent.setup();
    const popup = { opener: {}, location: { href: '' }, close: vi.fn() };
    vi.spyOn(window, 'open').mockImplementation(() => {
      mocks.events.push('open');
      return popup as unknown as Window;
    });
    const redirectUri = 'http://127.0.0.1:1/oauth/callback';
    const clientId = '123-abc.apps.googleusercontent.com';
    let completed: unknown;
    let confirmed: unknown;
    mocks.routes['GET /connections/{name}'] = () => connection();
    mocks.routes['GET /connections/{name}/tools'] = () => ({ servers: [] });
    mocks.routes['GET /connection-providers'] = () => ({
      redirects: [
        { mode: ConnectionRedirectMode.loopback, clientType: 'desktop' },
        {
          mode: ConnectionRedirectMode.paste,
          clientType: OAuthClientType.desktop,
          redirectUri,
        },
      ],
      providers: [],
    });
    mocks.routes['GET /oauth-apps/{appId}'] = () => ({
      id: 'app-1',
      clientId,
      clientType: OAuthClientType.desktop,
    });
    mocks.routes['POST /connections/{name}/authorization'] = () => ({
      session: 's1',
      authorizationUrl: 'https://accounts.google.com/o/oauth2/v2/auth?x=1',
      mode: ConnectionRedirectMode.paste,
      redirectUri,
      clientId,
      expiresAt: '2026-11-28T10:10:00Z',
    });
    mocks.routes['POST /connections/{name}/authorization/complete'] = (
      init
    ) => {
      completed = init.body;
      return undefined;
    };
    mocks.routes['GET /connections/{name}/authorization'] = () => ({
      phase: ConnectionAuthorizationPhase.confirm,
      account: 'other@example.com',
      probes: [{ service: 'gmail', ok: true }],
    });
    mocks.routes['POST /connections/{name}/authorization/confirm'] = (init) => {
      confirmed = init.body;
      return connection();
    };

    renderAt('/connections/work?workspace=ops');
    await user.click(await screen.findByRole('button', { name: 'Reconnect' }));
    const dialog = await screen.findByRole('dialog');
    expect(await within(dialog).findByText(redirectUri)).toBeVisible();
    expect(within(dialog).getByText(clientId)).toBeVisible();
    expect(
      within(dialog).getByText(/the popup ends on a page that fails to load/)
    ).toBeVisible();

    mocks.events = [];
    await user.click(
      within(dialog).getByRole('button', { name: 'Open sign-in' })
    );
    await waitFor(() =>
      expect(popup.location.href).toBe(
        'https://accounts.google.com/o/oauth2/v2/auth?x=1'
      )
    );
    expect(mocks.events.slice(0, 2)).toEqual([
      'open',
      'POST /connections/{name}/authorization',
    ]);

    const pasted = `${redirectUri}?code=c&state=s`;
    await user.type(
      within(dialog).getByLabelText('Address the popup ended on'),
      pasted
    );
    await user.click(within(dialog).getByRole('button', { name: 'Continue' }));
    expect(await within(dialog).findByText('other@example.com')).toBeVisible();
    expect(completed).toEqual({ redirectUrl: pasted });
    expect(
      within(dialog).getByText(/This is not ops@example.com/)
    ).toBeVisible();

    await user.click(
      within(dialog).getByRole('button', { name: 'Confirm account' })
    );
    expect(await within(dialog).findByText('work is connected.')).toBeVisible();
    expect(confirmed).toEqual({ session: 's1' });
  });

  // The poll runs only while a server callback session waits, and ends
  // once the session reaches confirm.
  it('polls a server callback session until confirm', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    stubPopup();
    serverCallbackRoutes();
    const phases = [
      { phase: ConnectionAuthorizationPhase.waiting },
      {
        phase: ConnectionAuthorizationPhase.confirm,
        account: 'ops@example.com',
      },
    ];
    let reads = 0;
    mocks.routes['GET /connections/{name}/authorization'] = () =>
      phases[Math.min(reads++, phases.length - 1)];

    const dialog = await openDialog(user);
    await user.click(
      within(dialog).getByRole('button', { name: 'Open sign-in' })
    );
    expect(
      await within(dialog).findByRole('button', { name: 'Copy sign-in link' })
    ).toBeVisible();

    await act(() => vi.advanceTimersByTimeAsync(2000));
    await act(() => vi.advanceTimersByTimeAsync(2000));
    expect(
      await within(dialog).findByRole('button', { name: 'Confirm account' })
    ).toBeVisible();
    expect(reads).toBe(2);
    await act(() => vi.advanceTimersByTimeAsync(10_000));
    expect(reads).toBe(2);

    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(cancelled()).toEqual(['s1']));
  });

  it('cancels a waiting session on close', async () => {
    const user = userEvent.setup();
    stubPopup();
    serverCallbackRoutes();
    mocks.routes['GET /connections/{name}/authorization'] = () => ({
      phase: ConnectionAuthorizationPhase.waiting,
    });

    const dialog = await openDialog(user);
    await user.click(
      within(dialog).getByRole('button', { name: 'Open sign-in' })
    );
    await within(dialog).findByRole('button', { name: 'Copy sign-in link' });
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));

    await waitFor(() => expect(cancelled()).toEqual(['s1']));
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  // The start request returns after the dialog closed: its session is
  // cancelled, and the popup never reaches the consent page.
  it('cancels a session that starts after close', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    const popup = stubPopup();
    let respond: (value: unknown) => void = () => {};
    serverCallbackRoutes(
      () =>
        new Promise((resolve) => {
          respond = resolve;
        })
    );

    const dialog = await openDialog(user);
    await user.click(
      within(dialog).getByRole('button', { name: 'Open sign-in' })
    );
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await act(async () => respond(serverCallbackStart()));

    await waitFor(() => expect(cancelled()).toEqual(['s1']));
    expect(popup.close).toHaveBeenCalled();
    expect(popup.location.href).toBe('');
    await act(() => vi.advanceTimersByTimeAsync(4000));
    expect(reads()).toBe(0);
  });

  // The cutoff runs on the browser's clock: a server whose expiresAt reads
  // as past here still gets polled, until 10 minutes after the start.
  it('ends a waiting session at its local deadline', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    stubPopup();
    serverCallbackRoutes(() => ({
      ...serverCallbackStart(),
      expiresAt: new Date(Date.now() - 60 * 60_000).toISOString(),
    }));
    mocks.routes['GET /connections/{name}/authorization'] = () => ({
      phase: ConnectionAuthorizationPhase.waiting,
    });

    const dialog = await openDialog(user);
    await user.click(
      within(dialog).getByRole('button', { name: 'Open sign-in' })
    );
    await act(() => vi.advanceTimersByTimeAsync(4000));
    expect(reads()).toBe(2);
    expect(within(dialog).queryByText(/Signing in failed/)).toBeNull();

    await act(() => vi.advanceTimersByTimeAsync(10 * 60_000));
    expect(
      within(dialog).getByText('Signing in failed: expired')
    ).toBeVisible();
    expect(cancelled()).toEqual(['s1']);
    const total = reads();
    await act(() => vi.advanceTimersByTimeAsync(4000));
    expect(reads()).toBe(total);
  });

  it('ends the session on a refused read', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    stubPopup();
    serverCallbackRoutes();

    const dialog = await openDialog(user);
    await user.click(
      within(dialog).getByRole('button', { name: 'Open sign-in' })
    );
    await act(() => vi.advanceTimersByTimeAsync(2000));
    expect(
      within(dialog).getByText(
        'Signing in failed: no route GET /connections/{name}/authorization'
      )
    ).toBeVisible();
    await act(() => vi.advanceTimersByTimeAsync(4000));
    expect(reads()).toBe(1);
  });

  // A confirm refused because the session ended reads the session again,
  // which offers another start.
  it('reads the session after a failed confirm', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    stubPopup();
    serverCallbackRoutes();
    const phases = [
      {
        phase: ConnectionAuthorizationPhase.confirm,
        account: 'ops@example.com',
      },
      { phase: ConnectionAuthorizationPhase.failed, reason: 'expired' },
    ];
    let count = 0;
    mocks.routes['GET /connections/{name}/authorization'] = () =>
      phases[Math.min(count++, phases.length - 1)];

    const dialog = await openDialog(user);
    await user.click(
      within(dialog).getByRole('button', { name: 'Open sign-in' })
    );
    await act(() => vi.advanceTimersByTimeAsync(2000));
    await user.click(
      within(dialog).getByRole('button', { name: 'Confirm account' })
    );

    expect(
      await within(dialog).findByRole('button', { name: 'Try again' })
    ).toBeVisible();
    expect(
      within(dialog).getByText('Signing in failed: expired')
    ).toBeVisible();
  });

  // A confirm in flight may already have made the new tokens live, so the
  // dialog cannot be cancelled until it answers.
  it('keeps the dialog open while a confirm runs', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    stubPopup();
    serverCallbackRoutes();
    mocks.routes['GET /connections/{name}/authorization'] = () => ({
      phase: ConnectionAuthorizationPhase.confirm,
      account: 'ops@example.com',
    });
    let respond: (value: unknown) => void = () => {};
    mocks.routes['POST /connections/{name}/authorization/confirm'] = () =>
      new Promise((resolve) => {
        respond = resolve;
      });

    const dialog = await openDialog(user);
    await user.click(
      within(dialog).getByRole('button', { name: 'Open sign-in' })
    );
    await act(() => vi.advanceTimersByTimeAsync(2000));
    await user.click(
      within(dialog).getByRole('button', { name: 'Confirm account' })
    );
    expect(
      within(dialog).getByRole('button', { name: 'Cancel' })
    ).toBeDisabled();

    await act(async () => respond(connection()));
    expect(await within(dialog).findByText('work is connected.')).toBeVisible();
    expect(cancelled()).toEqual([]);
  });

  it('links to sign-in when the popup is blocked', async () => {
    const user = userEvent.setup();
    vi.spyOn(window, 'open').mockReturnValue(null);
    serverCallbackRoutes();
    mocks.routes['GET /connections/{name}/authorization'] = () => ({
      phase: ConnectionAuthorizationPhase.waiting,
    });

    const dialog = await openDialog(user);
    await user.click(
      within(dialog).getByRole('button', { name: 'Open sign-in' })
    );

    expect(
      await within(dialog).findByText(/The browser blocked the popup/)
    ).toBeVisible();
    expect(
      within(dialog).getByRole('link', { name: 'Open sign-in page' })
    ).toHaveAttribute('href', AUTHORIZATION_URL);
  });
});
