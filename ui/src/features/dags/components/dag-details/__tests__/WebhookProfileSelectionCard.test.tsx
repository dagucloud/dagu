// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
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
      .mockResolvedValueOnce({
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
      });

    const user = userEvent.setup();
    render(
      <WebhookProfileSelectionCard
        fileName="example"
        isAdmin
        remoteNode="local"
        webhook={webhook}
        onWebhookChange={vi.fn()}
      />
    );

    expect(
      await screen.findByText('Profile service unavailable')
    ).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Retry' }));

    expect(await screen.findByText('prod')).toBeVisible();
    expect(screen.queryByText('Profile service unavailable')).toBeNull();
    expect(getMock).toHaveBeenCalledTimes(2);
  });
});
