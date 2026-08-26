// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { AppBarContext } from '@/contexts/AppBarContext';
import { defaultWorkspaceSelection } from '@/lib/workspace';
import NotificationsTab from '../NotificationsTab';

const fetchMock = vi.fn();
let dagSettingsResponse: () => Promise<Response>;

const config: Config = {
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
  oidcEnabled: false,
  oidcButtonLabel: '',
  proxyEnabled: false,
  proxyButtonLabel: '',
  terminalEnabled: true,
  gitSyncEnabled: true,
  updateAvailable: false,
  latestVersion: '',
  permissions: {
    writeDags: true,
    runDags: true,
  },
  license: {
    valid: true,
    plan: 'pro',
    expiry: '',
    features: [],
    gracePeriod: false,
    community: false,
    source: 'test',
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

function jsonResponse(body: unknown, status = 200): Promise<Response> {
  return Promise.resolve(
    new Response(JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })
  );
}

function emptyResponse(status: number): Promise<Response> {
  return Promise.resolve(new Response(null, { status }));
}

function renderTab() {
  return render(
    <MemoryRouter>
      <ConfigContext.Provider value={config}>
        <AppBarContext.Provider
          value={
            {
              selectedRemoteNode: 'local',
              workspaceSelection: defaultWorkspaceSelection(),
            } as never
          }
        >
          <NotificationsTab fileName="example" />
        </AppBarContext.Provider>
      </ConfigContext.Provider>
    </MemoryRouter>
  );
}

beforeEach(() => {
  fetchMock.mockReset();
  dagSettingsResponse = () => emptyResponse(404);
  fetchMock.mockImplementation((input: RequestInfo | URL) => {
    const url = String(input);
    if (url === '/api/v1/dags/example/notifications?remoteNode=local') {
      return dagSettingsResponse();
    }
    if (url === '/api/v1/notification-channels?remoteNode=local') {
      return jsonResponse({
        channels: [
          {
            id: 'slack-1',
            name: 'Slack Ops',
            type: 'slack',
            enabled: true,
            createdAt: '2026-05-17T00:00:00Z',
            updatedAt: '2026-05-17T00:00:00Z',
          },
        ],
      });
    }
    if (url === '/api/v1/notification-routes/global?remoteNode=local') {
      return jsonResponse({
        scope: 'global',
        enabled: true,
        inheritGlobal: true,
        routes: [
          {
            id: 'route-1',
            channelId: 'slack-1',
            enabled: true,
            events: ['dag_run_failed'],
          },
        ],
      });
    }
    if (url === '/api/v1/dags/example/notifications/test?remoteNode=local') {
      return jsonResponse({
        results: [
          {
            targetId: 'route-1',
            targetName: 'Slack Ops',
            provider: 'slack',
            delivered: true,
          },
        ],
      });
    }
    return jsonResponse({ message: `unexpected ${url}` }, 500);
  });
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('NotificationsTab', () => {
  it('blocks DAG controls when delivery is unavailable', async () => {
    dagSettingsResponse = () =>
      jsonResponse({ message: 'Notification delivery is unavailable' }, 503);

    renderTab();

    expect(
      await screen.findByText('Notification delivery is unavailable')
    ).toBeVisible();
    expect(
      screen.queryByRole('button', { name: /configure dag override/i })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: /send test/i })
    ).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /refresh/i })).toBeVisible();
  });

  it('ignores a stale load failure after refresh succeeds', async () => {
    const user = userEvent.setup();
    const defaultFetch = fetchMock.getMockImplementation();
    let resolveFirst: (response: Response) => void = () => {};
    const firstRequest = new Promise<Response>((resolve) => {
      resolveFirst = resolve;
    });
    let settingsRequests = 0;
    fetchMock.mockImplementation((input: RequestInfo | URL) => {
      if (
        String(input) === '/api/v1/dags/example/notifications?remoteNode=local'
      ) {
        settingsRequests++;
        if (settingsRequests === 1) {
          return firstRequest;
        }
      }
      return defaultFetch?.(input);
    });

    renderTab();
    await user.click(screen.getByRole('button', { name: /refresh/i }));
    expect(await screen.findByText('Inherited')).toBeVisible();

    await act(async () => {
      resolveFirst(
        new Response(
          JSON.stringify({ message: 'Notification delivery is unavailable' }),
          {
            status: 503,
            headers: { 'Content-Type': 'application/json' },
          }
        )
      );
      await firstRequest;
    });

    expect(
      screen.queryByText('Notification delivery is unavailable')
    ).not.toBeInTheDocument();
    expect(screen.getByText('Inherited')).toBeVisible();
  });

  it('shows inherited rules instead of silently creating a DAG override', async () => {
    renderTab();

    expect(await screen.findByText('Inherited')).toBeVisible();
    expect(screen.getByText('This DAG inherits Global rules.')).toBeVisible();
    expect(screen.getByText('Effective Inherited Routes')).toBeVisible();
    expect(await screen.findByText('Slack Ops')).toBeVisible();
    expect(
      screen.getByRole('button', { name: /configure dag override/i })
    ).toBeVisible();
    expect(screen.queryByText('Send to')).not.toBeInTheDocument();
  });

  it('tests the effective inherited route from the DAG page', async () => {
    const user = userEvent.setup();
    renderTab();

    await screen.findByText('Effective Inherited Routes');
    await user.click(screen.getByRole('button', { name: /send test/i }));

    await waitFor(() =>
      expect(screen.getByText('Test delivered')).toBeVisible()
    );
    const testCall = fetchMock.mock.calls.find(
      ([input]) =>
        String(input) ===
        '/api/v1/dags/example/notifications/test?remoteNode=local'
    );
    expect(testCall).toBeTruthy();
    expect(JSON.parse(testCall?.[1]?.body as string)).toEqual({
      eventType: 'dag_run_failed',
    });
  });

  it('makes empty DAG overrides explicit and keeps one save action', async () => {
    dagSettingsResponse = () =>
      jsonResponse({
        id: 'settings-1',
        dagName: 'example',
        enabled: true,
        events: ['dag_run_failed'],
        targets: [],
        subscriptions: [],
        createdAt: '2026-05-17T00:00:00Z',
        updatedAt: '2026-05-17T00:00:00Z',
      });
    renderTab();

    expect((await screen.findAllByText('DAG override')).length).toBeGreaterThan(
      0
    );
    expect(
      screen.getByText(
        'This DAG override has no destinations. Inherited rules are not used while the override exists.'
      )
    ).toBeVisible();
    expect(
      screen.getByText(
        'Inherited rules are not used while this DAG override exists. Add a channel or reset to inherit.'
      )
    ).toBeVisible();
    expect(
      screen.getByRole('button', { name: /reset to inherit/i })
    ).toBeVisible();
    expect(
      screen.getAllByRole('button', { name: /save changes/i })
    ).toHaveLength(1);
  });

  it('explains Teams setup and custom webhook body templates', async () => {
    dagSettingsResponse = () =>
      jsonResponse({
        id: 'settings-1',
        dagName: 'example',
        enabled: true,
        events: ['dag_run_failed'],
        targets: [
          {
            id: 'webhook-1',
            name: 'Custom webhook',
            type: 'webhook',
            enabled: true,
            events: [],
            webhook: {
              urlConfigured: true,
              urlPreview: 'http...hook',
              messageTemplate: 'DAG {{dag.name}} {{run.status}}',
            },
          },
          {
            id: 'teams-1',
            name: 'Teams alerts',
            type: 'teams',
            enabled: true,
            events: [],
            teams: {
              webhookUrlConfigured: true,
              webhookUrlPreview: 'http...teams',
              messageTemplate: 'DAG {{dag.name}} {{run.status}}',
            },
          },
        ],
        subscriptions: [],
        createdAt: '2026-05-17T00:00:00Z',
        updatedAt: '2026-05-17T00:00:00Z',
      });
    renderTab();

    expect(await screen.findByLabelText('Webhook endpoint URL')).toBeVisible();
    expect(screen.getByLabelText('Webhook message template')).toBeVisible();
    expect(screen.getByLabelText('Webhook JSON body template')).toBeVisible();
    expect(
      screen.getByText(/leave blank to keep the default Dagu payload/i)
    ).toBeVisible();
    expect(screen.getByText('{"text": "{{message}}"}')).toBeVisible();

    expect(screen.getByLabelText('Teams webhook URL')).toBeVisible();
    expect(screen.getByLabelText('Teams message template')).toBeVisible();
    expect(
      screen.getByText(/sends the compatible MessageCard format automatically/i)
    ).toBeVisible();
    expect(
      screen.getByRole('link', { name: /open microsoft setup guide/i })
    ).toHaveAttribute(
      'href',
      'https://support.microsoft.com/en-US/Workflows/send-messages-in-teams-using-incoming-webhooks'
    );
  });
});
