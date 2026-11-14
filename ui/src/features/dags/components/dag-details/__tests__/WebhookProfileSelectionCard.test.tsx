// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { createQueryHook } from 'swr-openapi';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  components,
  RuntimeProfileStatus,
  WebhookAuthMode,
} from '../../../../../api/v1/schema';
import { useClient } from '../../../../../hooks/api';
import WebhookProfileSelectionCard from '../WebhookProfileSelectionCard';

const api = vi.hoisted(() => ({
  getMock: vi.fn(),
  putMock: vi.fn(),
  useQuery: null as unknown as (...args: unknown[]) => unknown,
}));

// The query hook is the real swr-openapi hook over the mocked GET. A new key
// prefix in each test gives each test an empty SWR cache.
vi.mock('../../../../../hooks/api', () => ({
  useClient: vi.fn(),
  useQuery: (...args: unknown[]) => api.useQuery(...args),
}));

type WebhookDetails = components['schemas']['WebhookDetails'];

const getMock = api.getMock;
const putMock = api.putMock;
const useClientMock = vi.mocked(useClient);
let testIndex = 0;

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

// parity:webhook-profile-selection parity:test-webhook-profiles parity:api-settings
// The card reads /profiles through useProfiles, the key shared with the Start
// dialog and Settings > Runtime profile.
describe('WebhookProfileSelectionCard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    testIndex += 1;
    api.useQuery = createQueryHook(
      { GET: getMock } as never,
      `test-${testIndex}`
    ) as (...args: unknown[]) => unknown;
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

  it('shows the allowed profiles as badges to a non-admin without a request', () => {
    const onActiveProfileNamesChange = vi.fn();
    render(
      <WebhookProfileSelectionCard
        fileName="example"
        isAdmin={false}
        remoteNode="worker-a"
        webhook={{
          ...webhook,
          profileSelection: { allowedProfiles: ['prod', 'staging'] },
        }}
        onActiveProfileNamesChange={onActiveProfileNamesChange}
        onWebhookChange={vi.fn()}
      />
    );

    expect(screen.getByText('prod')).toBeVisible();
    expect(screen.getByText('staging')).toBeVisible();
    expect(screen.queryByRole('checkbox')).toBeNull();
    expect(
      screen.queryByRole('button', { name: 'Save profile selection' })
    ).toBeNull();
    expect(getMock).not.toHaveBeenCalled();
    expect(onActiveProfileNamesChange).toHaveBeenLastCalledWith([]);
  });

  it('reports no active profile while the load fails', async () => {
    getMock.mockResolvedValue({ error: { message: 'Down' } });
    const onActiveProfileNamesChange = vi.fn();
    render(
      <WebhookProfileSelectionCard
        fileName="example"
        isAdmin
        remoteNode="worker-a"
        webhook={{
          ...webhook,
          profileSelection: { allowedProfiles: ['prod'] },
        }}
        onActiveProfileNamesChange={onActiveProfileNamesChange}
        onWebhookChange={vi.fn()}
      />
    );

    expect(await screen.findByText('Down')).toBeVisible();
    expect(
      screen.getByText(
        'Runtime profiles could not be loaded. Editing is disabled.'
      )
    ).toBeVisible();
    expect(screen.getByText('Status unknown')).toBeVisible();
    expect(
      screen.getByRole('button', { name: 'Save profile selection' })
    ).toBeDisabled();
    expect(onActiveProfileNamesChange).toHaveBeenLastCalledWith([]);
  });
});
