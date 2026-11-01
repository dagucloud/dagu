// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { Client } from 'openapi-fetch';
import { SWRConfig } from 'swr';
import { createQueryHook } from 'swr-openapi';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { paths } from '../../../../../api/v1/schema';
import WebhookTab from '../WebhookTab';

const clientPost = vi.hoisted(() => vi.fn());

vi.mock('../../../../../contexts/ConfigContext', () => ({
  useConfig: () => ({ apiURL: '/api/v1' }),
}));

vi.mock('../../../../../contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

vi.mock('../../../../../contexts/AuthContext', () => ({
  TOKEN_KEY: 'dagu_auth_token',
  useIsAdmin: () => true,
}));

vi.mock('../../../../../hooks/api', () => ({
  useClient: () => ({ GET: vi.fn(), POST: clientPost }),
}));

// The cards hand their one-time secrets to the tab, which shows them.
vi.mock('../WebhookProfileSelectionCard', () => ({ default: () => null }));
vi.mock('../WebhookProfileTokensCard', () => ({
  default: ({ onTokenCreated }: { onTokenCreated: (t: string) => void }) => (
    <button type="button" onClick={() => onTokenCreated('profile-token')}>
      create profile token
    </button>
  ),
}));
vi.mock('../WebhookPublicURLCard', () => ({
  default: ({ onSecretCreated }: { onSecretCreated: (s: string) => void }) => (
    <button type="button" onClick={() => onSecretCreated('sender-secret')}>
      make sender secret
    </button>
  ),
}));

const webhook = {
  id: '00000000-0000-0000-0000-000000000001',
  dagName: 'deploy',
  tokenPrefix: 'dagu_wh_',
  enabled: true,
  authMode: 'token_only',
  hmac: { enabled: false, secretConfigured: false },
  profileSelection: { allowedProfiles: [] },
  createdAt: '2026-10-01T00:00:00Z',
  updatedAt: '2026-10-01T00:00:00Z',
  createdBy: 'alice',
};

const hmacWebhook = {
  ...webhook,
  authMode: 'token_and_hmac',
  hmac: {
    enabled: true,
    secretConfigured: true,
    enforcementMode: 'strict',
    algorithm: 'HMAC-SHA256',
  },
};

const BASE = '/api/v1/dags/deploy/webhook';
const ENDPOINT = `${window.location.origin}/api/v1/webhooks/deploy`;

function json(body: unknown, status = 200): Promise<Response> {
  return Promise.resolve(new Response(JSON.stringify(body), { status }));
}

let current: typeof webhook | null;

// Reads the webhook through SWR, as the chip of the workflow bar does.
const useWebhookQuery = createQueryHook(
  {
    GET: async () =>
      current ? { data: current } : { error: { code: 'not_found' } },
  } as unknown as Client<paths>,
  '/'
);

function SharedWebhook() {
  const { data, error } = useWebhookQuery('/dags/{fileName}/webhook', {
    params: { path: { fileName: 'deploy' }, query: { remoteNode: 'local' } },
  });
  const state = error
    ? 'none'
    : data
      ? data.enabled
        ? 'enabled'
        : 'disabled'
      : 'loading';
  return <output data-testid="shared-webhook">{state}</output>;
}

function calls(method: string, url: string) {
  return fetchMock.mock.calls.filter(
    ([input, init]) => input === url && (init?.method ?? 'GET') === method
  );
}

const fetchMock = vi.fn();

function renderTab() {
  const onSecretVisibleChange = vi.fn();
  render(
    <WebhookTab
      fileName="deploy"
      onSecretVisibleChange={onSecretVisibleChange}
    />
  );
  return onSecretVisibleChange;
}

// parity:test-webhook-tab parity:api-webhook
describe('WebhookTab', () => {
  beforeEach(() => {
    current = webhook;
    fetchMock.mockImplementation((url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      if (url === `${BASE}?remoteNode=local`) {
        if (method === 'GET') {
          return current ? json(current) : json({}, 404);
        }
        if (method === 'POST') {
          current = webhook;
          return json({ webhook, token: 'created-token' });
        }
        if (method === 'DELETE') {
          current = null;
          return json({});
        }
      }
      if (url === `${BASE}/regenerate?remoteNode=local`) {
        return json({ webhook, token: 'new-token' });
      }
      if (url === `${BASE}/toggle?remoteNode=local`) {
        current = { ...webhook, enabled: false };
        return json(current);
      }
      return json({}, 404);
    });
    vi.stubGlobal('fetch', fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    fetchMock.mockReset();
    clientPost.mockReset();
  });

  // parity:webhook-regenerate-token parity:webhook-secret-reveal
  it('confirms before regenerating the webhook token', async () => {
    const onSecretVisibleChange = renderTab();

    fireEvent.click(
      await screen.findByRole('button', { name: 'Regenerate token' })
    );

    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveTextContent(
      'Callers using the current token lose access'
    );
    expect(
      fetchMock.mock.calls.some(([, init]) => init?.method === 'POST')
    ).toBe(false);

    fireEvent.click(within(dialog).getByRole('button', { name: 'Regenerate' }));

    expect(
      await screen.findByText('new-token', undefined, { timeout: 2000 })
    ).toBeVisible();
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/dags/deploy/webhook/regenerate?remoteNode=local',
      expect.objectContaining({ method: 'POST' })
    );
    await waitFor(() =>
      expect(onSecretVisibleChange).toHaveBeenLastCalledWith(true)
    );

    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    expect(onSecretVisibleChange).toHaveBeenLastCalledWith(false);
    expect(await screen.findByText('Endpoint')).toBeVisible();
  });

  // parity:webhook-loading-error
  it('shows the load error and loads again on Retry', async () => {
    let failing = true;
    const defaultFetch = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation((url: string, init?: RequestInit) =>
      failing
        ? json({ message: 'store is down' }, 500)
        : defaultFetch(url, init)
    );
    renderTab();

    expect(screen.getByText('Loading webhook...')).toBeVisible();
    expect(await screen.findByText('store is down')).toBeVisible();

    failing = false;
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));

    expect(await screen.findByText('Endpoint')).toBeVisible();
  });

  // parity:webhook-create
  it('creates a webhook from the empty state and shows its token once', async () => {
    current = null;
    const onSecretVisibleChange = renderTab();

    expect(await screen.findByText('No webhook yet')).toBeVisible();
    await waitFor(() =>
      expect(onSecretVisibleChange).toHaveBeenLastCalledWith(false)
    );
    fireEvent.click(screen.getByRole('button', { name: 'Create webhook' }));

    expect(await screen.findByText('created-token')).toBeVisible();
    expect(screen.getByText('Webhook Token')).toBeVisible();
    expect(calls('POST', `${BASE}?remoteNode=local`)).toHaveLength(1);
    await waitFor(() =>
      expect(onSecretVisibleChange).toHaveBeenLastCalledWith(true)
    );

    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    expect(onSecretVisibleChange).toHaveBeenLastCalledWith(false);
  });

  // parity:webhook-endpoint-copy parity:webhook-token-meta
  // parity:webhook-curl-example
  it('shows the endpoint, the token metadata and an example request', async () => {
    const user = userEvent.setup();
    renderTab();

    const label = await screen.findByText('Endpoint');
    expect(screen.getAllByText(ENDPOINT)[0]).toBeVisible();
    expect(screen.getByText('dagu_wh_')).toBeVisible();
    expect(screen.getByText(/Token only/)).toBeVisible();
    expect(screen.getByText(/alice/)).toBeVisible();
    expect(screen.getByText('Example Request')).toBeVisible();
    expect(
      screen.getByText(/Authorization: Bearer <YOUR_TOKEN>/)
    ).toBeVisible();

    await user.click(within(label.parentElement!).getByRole('button'));
    await waitFor(async () =>
      expect(await navigator.clipboard.readText()).toBe(ENDPOINT)
    );
  });

  // parity:webhook-enable-toggle
  it('confirms before it disables the webhook', async () => {
    renderTab();

    fireEvent.click(await screen.findByRole('switch'));
    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveTextContent(
      'Are you sure you want to disable this webhook?'
    );
    fireEvent.click(within(dialog).getByRole('button', { name: 'Disable' }));

    await waitFor(() =>
      expect(calls('POST', `${BASE}/toggle?remoteNode=local`)).toHaveLength(1)
    );
    expect(
      JSON.parse(
        calls('POST', `${BASE}/toggle?remoteNode=local`)[0]![1]!.body as string
      )
    ).toEqual({ enabled: false });
    expect(await screen.findByText('Disabled')).toBeVisible();
  });

  // Other views read the webhook through SWR; a change here reaches them.
  it('refreshes the shared webhook query after a toggle and a delete', async () => {
    render(
      <SWRConfig value={{ provider: () => new Map() }}>
        <SharedWebhook />
        <WebhookTab fileName="deploy" />
      </SWRConfig>
    );
    const shared = screen.getByTestId('shared-webhook');
    await waitFor(() => expect(shared).toHaveTextContent('enabled'));

    fireEvent.click(await screen.findByRole('switch'));
    const toggle = await screen.findByRole('dialog');
    fireEvent.click(within(toggle).getByRole('button', { name: 'Disable' }));
    await waitFor(() => expect(shared).toHaveTextContent('disabled'));

    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    const remove = await screen.findByRole('dialog');
    fireEvent.click(within(remove).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(shared).toHaveTextContent('none'));
  });

  // parity:webhook-delete
  it('deletes the webhook after confirmation', async () => {
    renderTab();

    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));

    expect(await screen.findByText('No webhook yet')).toBeVisible();
    expect(calls('DELETE', `${BASE}?remoteNode=local`)).toHaveLength(1);
  });

  // parity:webhook-hmac-enable parity:webhook-hmac-examples
  it('adds HMAC, shows its secret once, then the signing examples', async () => {
    clientPost.mockResolvedValue({
      data: { webhook: hmacWebhook, hmacSecret: 'hmac-secret' },
    });
    const onSecretVisibleChange = renderTab();

    fireEvent.click(
      await screen.findByRole('button', { name: 'Keep Token and Add HMAC' })
    );

    expect(await screen.findByText('hmac-secret')).toBeVisible();
    expect(screen.getByText('Webhook HMAC Secret')).toBeVisible();
    expect(clientPost).toHaveBeenCalledWith(
      '/dags/{fileName}/webhook/hmac/enable',
      {
        params: {
          path: { fileName: 'deploy' },
          query: { remoteNode: 'local' },
        },
        body: { authMode: 'token_and_hmac', enforcementMode: 'strict' },
      }
    );
    await waitFor(() =>
      expect(onSecretVisibleChange).toHaveBeenLastCalledWith(true)
    );

    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    expect(onSecretVisibleChange).toHaveBeenLastCalledWith(false);
    expect(await screen.findByText('Generate HMAC')).toBeVisible();
    expect(screen.getByText('Shell (OpenSSL)')).toBeVisible();
    expect(screen.getByText('Node.js')).toBeVisible();
  });

  // parity:webhook-hmac-settings parity:webhook-hmac-rotate-disable
  it('saves, rotates and disables HMAC', async () => {
    current = hmacWebhook;
    clientPost.mockImplementation((path: string) =>
      Promise.resolve(
        path.endsWith('/regenerate')
          ? { data: { webhook: hmacWebhook, hmacSecret: 'rotated-secret' } }
          : path.endsWith('/disable')
            ? { data: webhook }
            : { data: hmacWebhook }
      )
    );
    const onSecretVisibleChange = renderTab();
    const params = {
      path: { fileName: 'deploy' },
      query: { remoteNode: 'local' },
    };

    fireEvent.click(
      await screen.findByRole('button', { name: 'Save HMAC Settings' })
    );
    await waitFor(() =>
      expect(clientPost).toHaveBeenCalledWith(
        '/dags/{fileName}/webhook/hmac/configure',
        {
          params,
          body: { authMode: 'token_and_hmac', enforcementMode: 'strict' },
        }
      )
    );

    fireEvent.click(
      await screen.findByRole('button', { name: 'Regenerate HMAC Secret' })
    );
    let dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Regenerate' }));
    expect(await screen.findByText('rotated-secret')).toBeVisible();
    await waitFor(() =>
      expect(onSecretVisibleChange).toHaveBeenLastCalledWith(true)
    );
    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    expect(onSecretVisibleChange).toHaveBeenLastCalledWith(false);

    fireEvent.click(
      await screen.findByRole('button', { name: 'Disable HMAC' })
    );
    dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Disable' }));
    await waitFor(() =>
      expect(clientPost).toHaveBeenCalledWith(
        '/dags/{fileName}/webhook/hmac/disable',
        { params }
      )
    );
    expect(
      await screen.findByRole('button', { name: 'Use HMAC Only' })
    ).toBeVisible();
  });

  it('reports the sender and profile-token reveals of its cards', async () => {
    const onSecretVisibleChange = renderTab();

    fireEvent.click(
      await screen.findByRole('button', { name: 'make sender secret' })
    );
    expect(await screen.findByText('sender-secret')).toBeVisible();
    expect(screen.getByText('Signing Secret')).toBeVisible();
    await waitFor(() =>
      expect(onSecretVisibleChange).toHaveBeenLastCalledWith(true)
    );
    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    expect(onSecretVisibleChange).toHaveBeenLastCalledWith(false);

    fireEvent.click(
      await screen.findByRole('button', { name: 'create profile token' })
    );
    expect(await screen.findByText('profile-token')).toBeVisible();
    await waitFor(() =>
      expect(onSecretVisibleChange).toHaveBeenLastCalledWith(true)
    );
    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    expect(onSecretVisibleChange).toHaveBeenLastCalledWith(false);
  });

  // The host guards a shown secret: an unmount must not leave it guarded.
  it('reports the secret gone when the tab unmounts while it shows', async () => {
    const onSecretVisibleChange = vi.fn();
    const { unmount } = render(
      <WebhookTab
        fileName="deploy"
        onSecretVisibleChange={onSecretVisibleChange}
      />
    );
    fireEvent.click(
      await screen.findByRole('button', { name: 'make sender secret' })
    );
    await waitFor(() =>
      expect(onSecretVisibleChange).toHaveBeenLastCalledWith(true)
    );
    unmount();
    expect(onSecretVisibleChange).toHaveBeenLastCalledWith(false);
  });
});
