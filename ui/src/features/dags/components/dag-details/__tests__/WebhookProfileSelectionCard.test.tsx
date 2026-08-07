// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  components,
  RuntimeProfileStatus,
  WebhookAuthMode,
} from '../../../../../api/v1/schema';
import { useClient } from '../../../../../hooks/api';
import WebhookProfileSelectionCard from '../WebhookProfileSelectionCard';

vi.mock('../../../../../hooks/api', () => ({
  useClient: vi.fn(),
}));

type WebhookDetails = components['schemas']['WebhookDetails'];

const getMock = vi.fn();
const putMock = vi.fn();
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
  createdAt: '2026-08-07T00:00:00Z',
  updatedAt: '2026-08-07T00:00:00Z',
};

const activeProfilesResponse = {
  data: {
    profiles: [
      {
        id: 'profile-1',
        name: 'prod',
        status: RuntimeProfileStatus.active,
        protected: false,
        entries: [],
        createdAt: '2026-08-07T00:00:00Z',
        updatedAt: '2026-08-07T00:00:00Z',
      },
    ],
  },
};

describe('WebhookProfileSelectionCard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useClientMock.mockReturnValue({
      GET: getMock,
      PUT: putMock,
    } as never);
  });

  it('retries loading runtime profiles without a page reload', async () => {
    getMock
      .mockResolvedValueOnce({
        error: { message: 'Profile service unavailable' },
      })
      .mockResolvedValueOnce(activeProfilesResponse);

    const user = userEvent.setup();
    render(
      <WebhookProfileSelectionCard
        fileName="example"
        isAdmin
        remoteNode="worker-a"
        webhook={webhook}
        onActiveProfileNamesChange={vi.fn()}
        onWebhookChange={vi.fn()}
      />
    );

    expect(
      await screen.findByText('Profile service unavailable')
    ).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Retry' }));

    expect(await screen.findByText('prod')).toBeVisible();
    expect(screen.queryByText('Profile service unavailable')).toBeNull();
    expect(getMock.mock.calls).toEqual([
      ['/profiles', { params: { query: { remoteNode: 'worker-a' } } }],
      ['/profiles', { params: { query: { remoteNode: 'worker-a' } } }],
    ]);
  });

  it('preserves unsaved edits when unchanged configuration is refreshed', async () => {
    getMock.mockResolvedValue(activeProfilesResponse);

    const user = userEvent.setup();
    const onActiveProfileNamesChange = vi.fn();
    const onWebhookChange = vi.fn();
    const { rerender } = render(
      <WebhookProfileSelectionCard
        fileName="example"
        isAdmin
        remoteNode="worker-a"
        webhook={webhook}
        onActiveProfileNamesChange={onActiveProfileNamesChange}
        onWebhookChange={onWebhookChange}
      />
    );
    const checkbox = await screen.findByRole('checkbox', { name: 'prod' });
    await user.click(checkbox);
    expect(checkbox).toBeChecked();

    rerender(
      <WebhookProfileSelectionCard
        fileName="example"
        isAdmin
        remoteNode="worker-a"
        webhook={{
          ...webhook,
          updatedAt: '2026-08-07T01:00:00Z',
          profileSelection: { allowedProfiles: [] },
        }}
        onActiveProfileNamesChange={onActiveProfileNamesChange}
        onWebhookChange={onWebhookChange}
      />
    );

    expect(screen.getByRole('checkbox', { name: 'prod' })).toBeChecked();
  });

  it('routes profile selection updates to the selected remote node', async () => {
    const updatedWebhook = {
      ...webhook,
      profileSelection: { allowedProfiles: ['prod'] },
    };
    getMock.mockResolvedValue(activeProfilesResponse);
    putMock.mockResolvedValue({ data: updatedWebhook });
    const onActiveProfileNamesChange = vi.fn();
    const onWebhookChange = vi.fn();

    const user = userEvent.setup();
    render(
      <WebhookProfileSelectionCard
        fileName="example"
        isAdmin
        remoteNode="worker-a"
        webhook={webhook}
        onActiveProfileNamesChange={onActiveProfileNamesChange}
        onWebhookChange={onWebhookChange}
      />
    );

    await user.click(await screen.findByRole('checkbox', { name: 'prod' }));
    await user.click(
      screen.getByRole('button', { name: 'Save profile selection' })
    );

    await waitFor(() => {
      expect(putMock).toHaveBeenCalledWith(
        '/dags/{fileName}/webhook/profile-selection',
        {
          params: {
            path: { fileName: 'example' },
            query: { remoteNode: 'worker-a' },
          },
          body: { allowedProfiles: ['prod'] },
        }
      );
    });
    expect(onActiveProfileNamesChange).toHaveBeenCalledWith(['prod']);
    expect(onWebhookChange).toHaveBeenCalledWith(updatedWebhook);
  });
});
