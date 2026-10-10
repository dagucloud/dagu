// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import * as React from 'react';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  LicenseMonitoringLevel,
  LicenseStatusResponseConnectedVia,
} from '@/api/v1/schema';
import { MonitoringNotice } from '@/components/MonitoringNotice';
import { useIsAdmin } from '@/contexts/AuthContext';
import type { LicenseStatus } from '@/contexts/ConfigContext';
import { LicenseContext } from '@/contexts/LicenseContext';
import { useClient } from '@/hooks/api';
import type { LicenseMonitoring } from '@/hooks/useLicenseMonitoring';

vi.mock('@/hooks/api', async () => {
  const { default: useSWR } = await import('swr');
  const useClient = vi.fn();
  return {
    useClient,
    // As in swr-openapi, null params skip the request.
    useQuery: (path: string, params: unknown, options: object) =>
      useSWR(
        params === null ? null : [path, params],
        async () => {
          const result = await useClient().GET(path, params);
          if (result.error) throw result.error;
          return result.data;
        },
        options
      ),
  };
});
vi.mock('@/contexts/AuthContext', () => ({ useIsAdmin: vi.fn() }));
vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: vi.fn() }),
}));

const useClientMock = vi.mocked(useClient);
const useIsAdminMock = vi.mocked(useIsAdmin);

const online: LicenseStatus = {
  valid: true,
  plan: 'team',
  expiry: '',
  features: [],
  gracePeriod: false,
  graceEndsAt: '',
  community: false,
  source: 'file',
  warningCode: '',
  error: '',
  connectedVia: LicenseStatusResponseConnectedVia.console,
  serverId: 'srv-123',
};

const unchosen: LicenseMonitoring = {
  level: LicenseMonitoringLevel.off,
  configured: false,
  chosen: false,
  noticeDismissed: false,
};

const notice = 'Get an email when this server or its workflows fail.';

// Serves monitoring the way the server does: PUT chooses a level and POST
// dismisses the notice.
function serve(initial: LicenseMonitoring) {
  let monitoring = initial;
  const client = {
    GET: vi.fn(() => Promise.resolve({ data: monitoring })),
    PUT: vi.fn(
      (_path: string, init: { body: { level: LicenseMonitoringLevel } }) => {
        monitoring = { ...monitoring, level: init.body.level, chosen: true };
        return Promise.resolve({ data: monitoring });
      }
    ),
    POST: vi.fn(() => {
      monitoring = { ...monitoring, noticeDismissed: true };
      return Promise.resolve({ data: monitoring });
    }),
  };
  useClientMock.mockReturnValue(client as never);
  return client;
}

function renderNotice(license: LicenseStatus = online) {
  return render(
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
      <LicenseContext.Provider value={{ license, mutate: vi.fn() }}>
        <MonitoringNotice />
      </LicenseContext.Provider>
    </SWRConfig>
  );
}

describe('MonitoringNotice', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useIsAdminMock.mockReturnValue(true);
  });

  it('stays dismissed once an admin dismisses it', async () => {
    const client = serve(unchosen);
    renderNotice();

    await userEvent.click(
      await screen.findByRole('button', { name: 'Dismiss monitoring notice' })
    );

    expect(client.POST).toHaveBeenCalledWith(
      '/license/monitoring/dismiss-notice',
      { params: { query: { remoteNode: 'local' } } }
    );
    await waitFor(() =>
      expect(screen.queryByText(notice)).not.toBeInTheDocument()
    );
  });

  it('leads to the choice of what to report', async () => {
    const client = serve(unchosen);
    renderNotice();

    await userEvent.click(
      await screen.findByRole('button', { name: 'Choose what to report' })
    );
    const dialog = await screen.findByRole('dialog', {
      name: 'Get an email when this server or its workflows fail',
    });
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Turn on' })
    );

    expect(client.PUT).toHaveBeenCalledWith('/license/monitoring', {
      params: { query: { remoteNode: 'local' } },
      body: { level: 'runs' },
    });
    await waitFor(() =>
      expect(screen.queryByText(notice)).not.toBeInTheDocument()
    );
    expect(client.POST).not.toHaveBeenCalled();
  });

  it('stops asking when the offer is declined', async () => {
    const client = serve(unchosen);
    renderNotice();

    await userEvent.click(
      await screen.findByRole('button', { name: 'Choose what to report' })
    );
    const dialog = await screen.findByRole('dialog');
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Not now' })
    );

    expect(client.POST).toHaveBeenCalledWith(
      '/license/monitoring/dismiss-notice',
      { params: { query: { remoteNode: 'local' } } }
    );
    expect(client.PUT).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(screen.queryByText(notice)).not.toBeInTheDocument()
    );
  });

  it.each([
    ['the level is chosen', { ...unchosen, chosen: true }],
    ['the configuration sets the level', { ...unchosen, configured: true }],
    ['it was dismissed', { ...unchosen, noticeDismissed: true }],
  ])('is not shown when %s', async (_case, monitoring) => {
    const client = serve(monitoring);
    renderNotice();

    await waitFor(() => expect(client.GET).toHaveBeenCalled());
    // Let the response render before checking that nothing is shown.
    await act(async () => {
      await client.GET.mock.results[0]?.value;
    });
    expect(screen.queryByText(notice)).not.toBeInTheDocument();
  });

  it('asks only admins of a server that checks in with Dagu Console', () => {
    const client = serve(unchosen);
    useIsAdminMock.mockReturnValue(false);
    const { unmount } = renderNotice();
    unmount();
    useIsAdminMock.mockReturnValue(true);
    renderNotice({ ...online, serverId: undefined });

    expect(client.GET).not.toHaveBeenCalled();
    expect(screen.queryByText(notice)).not.toBeInTheDocument();
  });
});
