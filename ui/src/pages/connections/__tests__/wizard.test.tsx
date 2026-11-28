// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  ConnectionAccess,
  ConnectionGrant,
  ConnectionProviderName,
  ConnectionRedirectMode,
  ConnectionStatus,
  OAuthAppKind,
  OAuthAppStatus,
  OAuthClientType,
  UserRole,
  type components,
} from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import ConnectionsPage from '..';
import ConnectionPage from '../ConnectionPage';

type OAuthApp = components['schemas']['OAuthApp'];
type Handler = (init: { params?: unknown; body?: unknown }) => unknown;

const mocks = vi.hoisted(() => ({
  routes: {} as Record<string, Handler>,
  bodies: {} as Record<string, unknown>,
  role: 'manager',
}));

function call(method: string) {
  return async (path: string, init: { params?: unknown; body?: unknown }) => {
    const key = `${method} ${path}`;
    mocks.bodies[key] = init?.body;
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
  useIsAdmin: () => mocks.role === UserRole.admin,
}));
vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ authMode: 'builtin' }),
}));

const CALLBACK_URI = 'https://dagu.example.com/oauth/callback';

function app(overrides: Partial<OAuthApp>): OAuthApp {
  return {
    id: 'agent-web',
    name: 'Google agents',
    provider: ConnectionProviderName.google,
    kind: OAuthAppKind.byo,
    clientId: 'agent.apps.googleusercontent.com',
    clientType: OAuthClientType.web,
    agent: true,
    status: OAuthAppStatus.ok,
    clientSecretConfigured: true,
    createdAt: '2026-11-01T00:00:00Z',
    updatedAt: '2026-11-01T00:00:00Z',
    ...overrides,
  };
}

function preset(name: string, apis: string[], writeScopes: string[] = []) {
  return {
    name,
    readScopes: [`https://www.googleapis.com/auth/${name}.readonly`],
    writeScopes,
    apis,
    enableApisUrl: `https://console.cloud.google.com/apis/enableflow?apiid=${apis.join(',')}`,
    preview: false,
  };
}

const CALLBACK = {
  mode: ConnectionRedirectMode.server_callback,
  clientType: OAuthClientType.web,
  redirectUri: CALLBACK_URI,
};
// The redirects of a server without a public URL.
const DESKTOP_ONLY = [
  {
    mode: ConnectionRedirectMode.loopback,
    clientType: OAuthClientType.desktop,
  },
  {
    mode: ConnectionRedirectMode.paste,
    clientType: OAuthClientType.desktop,
    redirectUri: 'http://127.0.0.1:1/oauth/callback',
  },
];

function routes(apps: OAuthApp[], redirects = [CALLBACK, ...DESKTOP_ONLY]) {
  mocks.routes['GET /connections'] = () => ({ connections: [] });
  mocks.routes['GET /connection-providers'] = () => ({
    redirects,
    providers: [
      {
        name: ConnectionProviderName.google,
        userScopes: ['openid', 'email'],
        setupLinks: {
          clients: 'https://console.cloud.google.com/auth/clients',
          consentScreen: 'https://console.cloud.google.com/auth/audience',
        },
        services: [
          preset(
            'gmail',
            ['gmail.googleapis.com', 'gmailmcp.googleapis.com'],
            ['https://www.googleapis.com/auth/gmail.modify']
          ),
          preset('drive', ['drive.googleapis.com', 'drivemcp.googleapis.com']),
        ],
      },
    ],
  });
  mocks.routes['GET /oauth-apps'] = () => ({ apps });
}

function renderPage() {
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
        <MemoryRouter initialEntries={['/connections?workspace=ops']}>
          <Routes>
            <Route path="/connections" element={<ConnectionsPage />} />
            <Route path="/connections/:name" element={<ConnectionPage />} />
          </Routes>
        </MemoryRouter>
      </AppBarContext.Provider>
    </SWRConfig>
  );
}

/** Walks the gallery and the Services and Agents steps. */
async function throughAgents(user: ReturnType<typeof userEvent.setup>) {
  renderPage();
  await user.click(
    await screen.findByRole('button', { name: 'New connection' })
  );
  const dialog = await screen.findByRole('dialog');
  expect(
    within(dialog).getByRole('button', { name: /Microsoft 365/ })
  ).toBeDisabled();
  await user.click(
    within(dialog).getByRole('button', { name: /Google Workspace/ })
  );
  await user.type(await within(dialog).findByLabelText('Name'), 'work');
  await user.click(within(dialog).getByLabelText('Gmail'));
  await user.click(within(dialog).getByLabelText('Drive'));
  await user.click(
    within(dialog).getByRole('combobox', { name: 'Access for Gmail' })
  );
  await user.click(
    await screen.findByRole('option', { name: 'Read and write' })
  );
  expect(
    within(dialog).getByText('https://www.googleapis.com/auth/gmail.modify')
  ).toBeVisible();
  await user.click(within(dialog).getByRole('button', { name: 'Next' }));
  await user.click(within(dialog).getByRole('radio', { name: /AI agents/ }));
  await user.click(within(dialog).getByRole('button', { name: 'Next' }));
  return dialog;
}

beforeEach(() => {
  mocks.routes = {};
  mocks.bodies = {};
  mocks.role = UserRole.manager;
});

describe('NewConnectionDialog', () => {
  // A manager picks a fitting app. Creating the connection opens its page
  // with the connect dialog, which runs Consent and Done.
  it('creates a connection and opens sign-in', async () => {
    const user = userEvent.setup();
    routes([
      app({}),
      app({ id: 'std', name: 'Google standard', agent: false }),
    ]);
    mocks.routes['POST /connections'] = () => ({});
    mocks.routes['GET /connections/{name}'] = () => ({
      name: 'work',
      workspace: 'ops',
      provider: ConnectionProviderName.google,
      app: 'agent-web',
      grant: ConnectionGrant.authorization_code,
      services: [],
      scopes: [],
      boundHosts: [],
      allowTokenExport: false,
      status: ConnectionStatus.pending,
      grantedScopes: [],
      refreshTokenConfigured: false,
      createdAt: '2026-11-01T00:00:00Z',
      updatedAt: '2026-11-01T00:00:00Z',
    });
    mocks.routes['GET /oauth-apps/{appId}'] = () => app({});

    const dialog = await throughAgents(user);
    expect(within(dialog).queryByText('Google standard')).toBeNull();
    expect(
      within(dialog).getByText(
        /their authors can read this connection’s tokens/
      )
    ).toBeVisible();
    await user.click(
      within(dialog).getByRole('radio', { name: /Google agents/ })
    );
    expect(
      within(dialog).getAllByText('agent.apps.googleusercontent.com').length
    ).toBeGreaterThan(0);
    await user.click(
      within(dialog).getByRole('button', { name: 'Create and sign in' })
    );

    expect(
      await screen.findByRole('dialog', { name: 'Connect work' })
    ).toBeVisible();
    expect(mocks.bodies['POST /connections']).toEqual({
      name: 'work',
      app: 'agent-web',
      services: [
        { name: 'gmail', access: ConnectionAccess.write },
        { name: 'drive', access: ConnectionAccess.read },
      ],
    });
  });

  it('tells a manager what to ask an admin for', async () => {
    const user = userEvent.setup();
    routes([app({ id: 'std', agent: false })]);

    const dialog = await throughAgents(user);

    expect(
      within(dialog).getByText(
        'Ask an admin to register a Google OAuth app with:'
      )
    ).toBeVisible();
    expect(
      within(dialog).getByText('Client type: Web application')
    ).toBeVisible();
    expect(within(dialog).getByText(CALLBACK_URI)).toBeVisible();
    expect(
      within(dialog).queryByRole('button', { name: 'Register app' })
    ).toBeNull();
  });

  // An admin with no fitting app follows the checklist and uploads the
  // client JSON, which registers an app for agents and selects it.
  it('registers an app from the admin checklist', async () => {
    const user = userEvent.setup();
    mocks.role = UserRole.admin;
    routes([]);
    mocks.routes['POST /oauth-apps'] = () => {
      const registered = app({ id: 'new-app', name: 'Google (AI agents)' });
      mocks.routes['GET /oauth-apps'] = () => ({ apps: [registered] });
      return registered;
    };

    const dialog = await throughAgents(user);

    expect(
      within(dialog).getByRole('link', { name: 'Enable APIs in Google Cloud' })
    ).toHaveAttribute(
      'href',
      'https://console.cloud.google.com/apis/enableflow?apiid=gmail.googleapis.com,gmailmcp.googleapis.com,drive.googleapis.com,drivemcp.googleapis.com'
    );
    expect(within(dialog).getByText(CALLBACK_URI)).toBeVisible();
    expect(
      within(dialog).getByText(
        'Tick “Use this client for an AI-powered agent”.'
      )
    ).toBeVisible();

    const json = '{"web":{"client_id":"agent.apps.googleusercontent.com"}}';
    await user.upload(
      within(dialog).getByLabelText('Client JSON file'),
      new File([json], 'client_secret.json', { type: 'application/json' })
    );
    await user.click(
      within(dialog).getByRole('button', { name: 'Register app' })
    );

    expect(
      await within(dialog).findByRole('radio', { name: /Google \(AI agents\)/ })
    ).toBeChecked();
    expect(
      within(dialog).getByRole('button', { name: 'Create and sign in' })
    ).toBeEnabled();
    expect(mocks.bodies['POST /oauth-apps']).toEqual({
      name: 'Google (AI agents)',
      provider: 'google',
      clientFile: json,
      agent: true,
    });
  });

  // A server without a public URL cannot sign in through a web client, so
  // the browser refuses its JSON instead of registering an unusable app.
  it('refuses a web client the server cannot use', async () => {
    const user = userEvent.setup();
    mocks.role = UserRole.admin;
    routes([], DESKTOP_ONLY);

    const dialog = await throughAgents(user);
    expect(
      within(dialog).getByText(/Choose the application type Desktop app/)
    ).toBeVisible();
    await user.upload(
      within(dialog).getByLabelText('Client JSON file'),
      new File(['{"web":{"client_id":"x"}}'], 'client.json', {
        type: 'application/json',
      })
    );
    await user.click(
      within(dialog).getByRole('button', { name: 'Register app' })
    );

    expect(
      await within(dialog).findByText(/it cannot sign in through a web client/)
    ).toBeVisible();
    expect(mocks.bodies).not.toHaveProperty(['POST /oauth-apps']);
    expect(
      within(dialog).getByRole('button', { name: 'Create and sign in' })
    ).toBeDisabled();
  });

  it('flags a name the workspace already has', async () => {
    const user = userEvent.setup();
    routes([app({})]);
    mocks.routes['GET /connections'] = () => ({
      connections: [{ name: 'work', workspace: 'ops' }],
    });

    renderPage();
    await user.click(
      await screen.findByRole('button', { name: 'New connection' })
    );
    const dialog = await screen.findByRole('dialog');
    await user.click(
      within(dialog).getByRole('button', { name: /Google Workspace/ })
    );
    await user.type(await within(dialog).findByLabelText('Name'), 'work');
    await user.click(within(dialog).getByLabelText('Gmail'));

    expect(
      within(dialog).getByText(/already has a connection named work/)
    ).toBeVisible();
    expect(
      within(dialog).getByRole('link', { name: 'Open it' })
    ).toHaveAttribute('href', '/connections/work?workspace=ops');
    expect(within(dialog).getByRole('button', { name: 'Next' })).toBeDisabled();
  });

  // A failed app list is not an empty one: the step offers a retry, not
  // the request to an admin.
  it('retries an app list that failed to load', async () => {
    const user = userEvent.setup();
    routes([app({})]);
    const listed = mocks.routes['GET /oauth-apps']!;
    delete mocks.routes['GET /oauth-apps'];

    const dialog = await throughAgents(user);
    expect(
      await within(dialog).findByText('no route GET /oauth-apps')
    ).toBeVisible();
    expect(within(dialog).queryByText(/Ask an admin to register/)).toBeNull();

    mocks.routes['GET /oauth-apps'] = listed;
    await user.click(within(dialog).getByRole('button', { name: 'Try again' }));
    expect(
      await within(dialog).findByRole('radio', { name: /Google agents/ })
    ).toBeVisible();
  });

  // The browser reads a hand-edited file as the server's decoder does: a
  // null "web" is absent, and keys match regardless of case.
  it('accepts a desktop client file with a null web key', async () => {
    const user = userEvent.setup();
    mocks.role = UserRole.admin;
    routes([], DESKTOP_ONLY);
    mocks.routes['POST /oauth-apps'] = () =>
      app({ id: 'desk', clientType: OAuthClientType.desktop });

    const dialog = await throughAgents(user);
    const json = '{"web":null,"Installed":{"client_id":"x"}}';
    await user.upload(
      within(dialog).getByLabelText('Client JSON file'),
      new File([json], 'client.json', { type: 'application/json' })
    );
    await user.click(
      within(dialog).getByRole('button', { name: 'Register app' })
    );

    await waitFor(() =>
      expect(mocks.bodies['POST /oauth-apps']).toMatchObject({
        clientFile: json,
      })
    );
    expect(
      within(dialog).queryByText(/cannot sign in through a web client/)
    ).toBeNull();
  });
});
