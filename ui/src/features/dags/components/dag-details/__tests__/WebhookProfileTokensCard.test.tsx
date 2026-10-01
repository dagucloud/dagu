// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { components, WebhookAuthMode } from '../../../../../api/v1/schema';
import { useClient } from '../../../../../hooks/api';
import WebhookProfileTokensCard from '../WebhookProfileTokensCard';

vi.mock('../../../../../hooks/api', () => ({
  useClient: vi.fn(),
}));

type WebhookDetails = components['schemas']['WebhookDetails'];

const postMock = vi.fn();
const deleteMock = vi.fn();
const useClientMock = vi.mocked(useClient);

const webhook: WebhookDetails = {
  id: 'webhook-1',
  dagName: 'example',
  tokenPrefix: 'dagu_wh_',
  enabled: true,
  authMode: WebhookAuthMode.token_only,
  hmac: {
    enabled: false,
    secretConfigured: false,
  },
  profileSelection: {
    allowedProfiles: [],
  },
  profileTokens: [],
  createdAt: '2026-08-07T00:00:00Z',
  updatedAt: '2026-08-07T00:00:00Z',
};

const customerToken = {
  id: 'token-1',
  name: 'customer-a',
  tokenPrefix: 'dagu_wh_abcd',
  profile: 'customer-a-profile',
  createdAt: '2026-08-07T00:00:00Z',
};

function renderCard(
  overrides: Partial<WebhookDetails> = {},
  isAdmin = true
): { onWebhookChange: () => void; onTokenCreated: () => void } {
  const onWebhookChange = vi.fn();
  const onTokenCreated = vi.fn();
  render(
    <WebhookProfileTokensCard
      fileName="example"
      isAdmin={isAdmin}
      remoteNode="worker-a"
      webhook={{ ...webhook, ...overrides }}
      activeProfileNames={['customer-a-profile', 'customer-b-profile']}
      onWebhookChange={onWebhookChange}
      onTokenCreated={onTokenCreated}
    />
  );
  return { onWebhookChange, onTokenCreated };
}

describe('WebhookProfileTokensCard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useClientMock.mockReturnValue({
      POST: postMock,
      DELETE: deleteMock,
    } as never);
  });

  it('creates a token on the selected remote node and reveals it', async () => {
    const updatedWebhook = { ...webhook, profileTokens: [customerToken] };
    postMock.mockResolvedValue({
      data: { webhook: updatedWebhook, token: 'dagu_wh_secret' },
    });

    const user = userEvent.setup();
    const { onWebhookChange, onTokenCreated } = renderCard();

    await user.type(
      screen.getByRole('textbox', { name: 'Token name' }),
      ' customer-a '
    );
    await user.click(screen.getByRole('combobox', { name: 'Runtime profile' }));
    await user.click(
      await screen.findByRole('option', { name: 'customer-a-profile' })
    );
    await user.click(screen.getByRole('button', { name: 'Create token' }));

    await waitFor(() => expect(onTokenCreated).toHaveBeenCalled());
    expect(postMock).toHaveBeenCalledWith(
      '/dags/{fileName}/webhook/profile-tokens',
      {
        params: {
          path: { fileName: 'example' },
          query: { remoteNode: 'worker-a' },
        },
        body: { name: 'customer-a', profile: 'customer-a-profile' },
      }
    );
    expect(onWebhookChange).toHaveBeenCalledWith(updatedWebhook);
    expect(onTokenCreated).toHaveBeenCalledWith('dagu_wh_secret');
  });

  it('revokes a token after confirmation', async () => {
    deleteMock.mockResolvedValue({ data: webhook });

    const user = userEvent.setup();
    const { onWebhookChange } = renderCard({ profileTokens: [customerToken] });

    expect(screen.getByText('customer-a-profile')).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Revoke' }));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByRole('button', { name: 'Revoke' }));

    await waitFor(() => expect(onWebhookChange).toHaveBeenCalledWith(webhook));
    expect(deleteMock).toHaveBeenCalledWith(
      '/dags/{fileName}/webhook/profile-tokens/{tokenId}',
      {
        params: {
          path: { fileName: 'example', tokenId: 'token-1' },
          query: { remoteNode: 'worker-a' },
        },
      }
    );
  });

  it('lists tokens read-only for non-admins', () => {
    renderCard({ profileTokens: [customerToken] }, false);

    expect(screen.getByText('customer-a')).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Revoke' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Create token' })).toBeNull();
  });

  // Remote nodes running an older version omit profileTokens.
  it('renders an empty list when profileTokens is missing', () => {
    renderCard({ profileTokens: undefined });

    expect(screen.getByText('No profile tokens.')).toBeVisible();
  });

  it('hides token creation while authentication is HMAC only', () => {
    renderCard({ authMode: WebhookAuthMode.hmac_only });

    expect(
      screen.getByText(
        'Profile tokens are ignored while authentication is HMAC only.'
      )
    ).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Create token' })).toBeNull();
  });
});
