// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  components,
  WebhookAuthMode,
  WebhookPublicDeliveryOutcome,
  WebhookPublicSender,
  WebhookPublicState,
} from '../../../../../api/v1/schema';
import { useClient, useQuery } from '@/hooks/api';
import WebhookPublicURLCard from '../WebhookPublicURLCard';

vi.mock('@/hooks/api', () => ({
  useClient: vi.fn(),
  useQuery: vi.fn(),
}));

type WebhookDetails = components['schemas']['WebhookDetails'];
type WebhookPublicDetails = components['schemas']['WebhookPublicDetails'];

const useQueryMock = useQuery as unknown as {
  mockReturnValue: (value: unknown) => void;
};

// returnsDetails makes the card's status query answer with details, or fail
// with error.
function returnsDetails(details?: WebhookPublicDetails, error?: unknown) {
  useQueryMock.mockReturnValue({ data: details, error, mutate: mutateMock });
}

const postMock = vi.fn();
const putMock = vi.fn();
const deleteMock = vi.fn();
const mutateMock = vi.fn();

const webhook: WebhookDetails = {
  id: 'webhook-1',
  dagName: 'deploy.yaml',
  tokenPrefix: 'dagu_wh_',
  enabled: true,
  authMode: WebhookAuthMode.token_only,
  hmac: { enabled: false, secretConfigured: false },
  profileSelection: { allowedProfiles: [] },
  createdAt: '2026-10-01T00:00:00Z',
  updatedAt: '2026-10-01T00:00:00Z',
};

const publicWebhook: WebhookDetails = {
  ...webhook,
  public: {
    secretConfigured: false,
    events: [],
    createdAt: '2026-10-01T00:00:00Z',
  },
};

const activeDetails: WebhookPublicDetails = {
  url: 'https://hooks.dagu.sh/server/token',
  state: WebhookPublicState.active,
  maxHooks: 1000,
  deliveries: [
    {
      id: 'delivery-1',
      receivedAt: '2026-10-14T01:02:03Z',
      size: 2048,
      event: 'push',
      outcome: WebhookPublicDeliveryOutcome.started,
      dagRunId: '3f0c2d4e-run',
    },
    {
      id: 'delivery-2',
      receivedAt: '2026-10-14T01:03:00Z',
      size: 10,
      outcome: WebhookPublicDeliveryOutcome.rejected,
      error: 'the signature does not match the signing secret',
    },
  ],
  missed: { expired: 2, refused: 1 },
};

function renderCard(
  current: WebhookDetails,
  { isAdmin = true }: { isAdmin?: boolean } = {}
) {
  const onWebhookChange = vi.fn();
  const onSecretCreated = vi.fn();
  render(
    <MemoryRouter>
      <WebhookPublicURLCard
        fileName="deploy.yaml"
        dagName="deploy"
        isAdmin={isAdmin}
        remoteNode="local"
        webhook={current}
        onWebhookChange={onWebhookChange}
        onSecretCreated={onSecretCreated}
      />
    </MemoryRouter>
  );
  return { onWebhookChange, onSecretCreated };
}

const params = {
  path: { fileName: 'deploy.yaml' },
  query: { remoteNode: 'local' },
};

// parity:test-webhook-public-url
describe('WebhookPublicURLCard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(useClient).mockReturnValue({
      POST: postMock,
      PUT: putMock,
      DELETE: deleteMock,
    } as never);
    returnsDetails(undefined);
  });

  // parity:webhook-public-toggle
  it('turns on a public URL', async () => {
    postMock.mockResolvedValue({ data: publicWebhook });
    const user = userEvent.setup();
    const { onWebhookChange } = renderCard(webhook);

    await user.click(screen.getByRole('switch', { name: 'Public URL' }));

    await waitFor(() =>
      expect(onWebhookChange).toHaveBeenCalledWith(publicWebhook)
    );
    expect(postMock).toHaveBeenCalledWith('/dags/{fileName}/webhook/public', {
      params,
    });
  });

  // parity:webhook-public-url-copy parity:webhook-public-deliveries
  // parity:webhook-public-missed
  it('shows the URL, its deliveries, and missed deliveries', async () => {
    returnsDetails(activeDetails);
    deleteMock.mockResolvedValue({});
    const user = userEvent.setup();
    renderCard(publicWebhook);

    expect(
      screen.getByText('https://hooks.dagu.sh/server/token')
    ).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Copy' }));
    await waitFor(async () =>
      expect(await navigator.clipboard.readText()).toBe(
        'https://hooks.dagu.sh/server/token'
      )
    );
    expect(screen.getAllByText('push')).not.toHaveLength(0);
    expect(screen.getByRole('link', { name: '3f0c2d4e' })).toHaveAttribute(
      'href',
      '/dags/deploy.yaml?remoteNode=local&run=3f0c2d4e-run'
    );
    expect(
      screen.getByText('the signature does not match the signing secret')
    ).toBeVisible();
    expect(
      screen.getByText(/2 waited more than 7 days, and 1 arrived/)
    ).toBeVisible();

    await user.click(screen.getByRole('button', { name: 'Dismiss' }));
    await waitFor(() => expect(mutateMock).toHaveBeenCalled());
    expect(deleteMock).toHaveBeenCalledWith(
      '/dags/{fileName}/webhook/public/missed',
      { params }
    );
  });

  // parity:webhook-public-sender
  it('reveals the GitHub secret Dagu makes', async () => {
    const saved = {
      ...publicWebhook,
      public: {
        ...publicWebhook.public!,
        sender: WebhookPublicSender.github,
        secretConfigured: true,
        events: ['push'],
      },
    };
    putMock.mockResolvedValue({
      data: { webhook: saved, secret: 'a'.repeat(64) },
    });
    const user = userEvent.setup();
    const { onWebhookChange, onSecretCreated } = renderCard(publicWebhook);

    await user.click(screen.getByRole('combobox', { name: 'Sender' }));
    await user.click(await screen.findByRole('option', { name: 'GitHub' }));
    await user.type(
      screen.getByRole('textbox', { name: 'Events' }),
      ' push , '
    );
    expect(
      screen.getByText(/set Content type to/, { exact: false })
    ).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Save sender' }));

    await waitFor(() =>
      expect(onSecretCreated).toHaveBeenCalledWith('a'.repeat(64))
    );
    expect(putMock).toHaveBeenCalledWith(
      '/dags/{fileName}/webhook/public/sender',
      {
        params,
        body: {
          sender: WebhookPublicSender.github,
          secret: '',
          generateSecret: undefined,
          events: ['push'],
        },
      }
    );
    expect(onWebhookChange).toHaveBeenCalledWith(saved);
  });

  // parity:webhook-public-new-url
  it('confirms before replacing the URL', async () => {
    postMock.mockResolvedValue({ data: publicWebhook });
    const user = userEvent.setup();
    renderCard(publicWebhook);

    await user.click(screen.getByRole('button', { name: 'New URL' }));
    const dialog = await screen.findByRole('dialog');
    expect(postMock).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole('button', { name: 'Replace' }));

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        '/dags/{fileName}/webhook/public/regenerate',
        { params }
      )
    );
  });

  it('says when the URL cannot be loaded, and loads it again', async () => {
    returnsDetails(undefined, { message: 'boom' });
    const user = userEvent.setup();
    renderCard(publicWebhook);

    expect(
      screen.getByText('The public URL could not be loaded.')
    ).toBeVisible();
    expect(screen.queryByText('Connecting to Dagu Cloud…')).toBeNull();
    expect(screen.queryByText('No deliveries yet.')).toBeNull();

    await user.click(screen.getByRole('button', { name: 'Retry' }));
    expect(mutateMock).toHaveBeenCalled();
  });

  it('says when the plan leaves the URL out', () => {
    returnsDetails({
      ...activeDetails,
      state: WebhookPublicState.overLimit,
      maxHooks: 1,
    });
    renderCard(publicWebhook);

    expect(
      screen.getByText(/Your plan allows 1 public URLs/, { exact: false })
    ).toBeVisible();
  });

  it('lets only admins change a public URL', () => {
    returnsDetails(activeDetails);
    renderCard(publicWebhook, { isAdmin: false });

    expect(screen.getByRole('switch', { name: 'Public URL' })).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'New URL' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Dismiss' })).toBeNull();
    expect(screen.getByText(/Anyone with the URL · All events/)).toBeVisible();
  });
});
