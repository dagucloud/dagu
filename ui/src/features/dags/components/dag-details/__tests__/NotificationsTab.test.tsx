// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { AppBarContext } from '@/contexts/AppBarContext';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import { defaultWorkspaceSelection } from '@/lib/workspace';
import NotificationsTab from '../NotificationsTab';

const commandAccess = vi.hoisted(() => ({ canManage: true }));

vi.mock('@/contexts/AuthContext', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/contexts/AuthContext')>()),
  useCanManageCommandChannels: () => commandAccess.canManage,
  useCommandChannelsEnabled: () => true,
}));

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
  upgradeCommand: '',
  restartRequired: false,
  restartCommand: '',
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

// The DAG page scopes its requests to one node, which can differ from the
// node selected in the toolbar.
function renderTab(
  toolbarRemoteNode = 'local',
  onDirtyChange?: (dirty: boolean) => void
) {
  return render(
    <MemoryRouter>
      <ConfigContext.Provider value={config}>
        <AppBarContext.Provider
          value={
            {
              selectedRemoteNode: toolbarRemoteNode,
              workspaceSelection: defaultWorkspaceSelection(),
            } as never
          }
        >
          <RemoteNodeProvider remoteNode="local">
            <NotificationsTab
              fileName="example"
              onDirtyChange={onDirtyChange}
            />
          </RemoteNodeProvider>
        </AppBarContext.Provider>
      </ConfigContext.Provider>
    </MemoryRouter>
  );
}

beforeEach(() => {
  commandAccess.canManage = true;
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

// parity:test-notifications-tab parity:api-notifications-incidents-wiki
describe('NotificationsTab', () => {
  // parity:notif-header-actions parity:notif-configure-override
  // parity:notif-save parity:notif-reset-override
  it('reports unsaved changes after configure, a save, an edit and a reset', async () => {
    const user = userEvent.setup();
    const onDirtyChange = vi.fn();
    const settingsURL = '/api/v1/dags/example/notifications?remoteNode=local';
    const saved = {
      id: 'settings-1',
      dagName: 'example',
      enabled: true,
      events: ['dag_run_failed'],
      targets: [],
      subscriptions: [],
      createdAt: '2026-05-17T00:00:00Z',
      updatedAt: '2026-05-17T00:00:00Z',
    };
    const defaultFetch = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(
      (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input) === settingsURL && init?.method === 'PUT') {
          return jsonResponse(saved);
        }
        if (String(input) === settingsURL && init?.method === 'DELETE') {
          return emptyResponse(204);
        }
        return defaultFetch(input, init);
      }
    );
    renderTab('local', onDirtyChange);
    const methodCalls = (method: string) =>
      fetchMock.mock.calls.filter(
        ([input, init]) =>
          String(input) === settingsURL && init?.method === method
      );

    await screen.findByText('Effective Inherited Routes');
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
    // parity:notif-subnav
    expect(screen.getByRole('link', { name: /^rules$/i })).toHaveAttribute(
      'href',
      '/notification-rules'
    );
    expect(screen.getByRole('link', { name: /^channels$/i })).toHaveAttribute(
      'href',
      '/notification-channels'
    );

    await user.click(
      screen.getByRole('button', { name: /configure workflow override/i })
    );
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);

    await user.click(screen.getByRole('button', { name: /save changes/i }));
    await waitFor(() => expect(onDirtyChange).toHaveBeenLastCalledWith(false));
    expect(methodCalls('PUT')).toHaveLength(1);

    await user.click(
      screen.getByRole('switch', { name: 'Toggle notifications' })
    );
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);

    await user.click(screen.getByRole('button', { name: /reset to inherit/i }));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByRole('button', { name: 'Reset' }));
    await waitFor(() => expect(onDirtyChange).toHaveBeenLastCalledWith(false));
    expect(methodCalls('DELETE')).toHaveLength(1);
  });

  // parity:notif-provider-email
  it('adds an email destination to a new override', async () => {
    const user = userEvent.setup();
    renderTab();

    await user.click(
      await screen.findByRole('button', {
        name: /configure workflow override/i,
      })
    );
    await user.click(
      screen.getAllByRole('button', { name: /add custom/i })[0]!
    );

    expect(
      await screen.findByLabelText('Email subject template')
    ).toBeVisible();
    expect(screen.getByLabelText('Email body template')).toBeVisible();
  });

  // parity:notif-load
  it('blocks DAG controls when delivery is unavailable', async () => {
    dagSettingsResponse = () =>
      jsonResponse({ message: 'Notification delivery is unavailable' }, 503);

    renderTab();

    expect(
      await screen.findByText('Notification delivery is unavailable')
    ).toBeVisible();
    expect(
      screen.queryByRole('button', { name: /configure workflow override/i })
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

  // parity:notif-source-card parity:notif-inherited-routes
  // The tab sits in the Workflow panel (360 to 560 px) in a window of any
  // width, so the header row follows the width of its own box.
  it('stacks the header by container width, not by window width', async () => {
    renderTab();

    const title = await screen.findByText('Workflow notifications');
    const row = title.closest('h2')!.parentElement!.parentElement!;
    expect(row).toHaveClass('flex-col', '@3xl:flex-row');
    expect(row.className).not.toMatch(/(^|\s)(sm|md|lg|xl):/);
    expect(row.parentElement!.parentElement).toHaveClass('@container');
    expect(row.children[1]).toHaveClass('flex-wrap', 'min-w-0');
  });

  it('shows inherited rules instead of silently creating a workflow override', async () => {
    renderTab();

    expect(await screen.findByText('Inherited')).toBeVisible();
    expect(
      screen.getByText('This workflow inherits Global rules.')
    ).toBeVisible();
    expect(screen.getByText('Effective Inherited Routes')).toBeVisible();
    expect(await screen.findByText('Slack Ops')).toBeVisible();
    expect(
      screen.getByRole('button', { name: /configure workflow override/i })
    ).toBeVisible();
    expect(screen.queryByText('Send to')).not.toBeInTheDocument();
  });

  // parity:notif-test-results
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

  // parity:notif-command-lock parity:notif-subscriptions
  it.each([
    { name: 'the toolbar node is local', toolbarRemoteNode: 'local' },
    { name: 'the toolbar shows another node', toolbarRemoteNode: 'edge' },
  ])(
    'locks test sends to a local command channel when $name',
    async ({ toolbarRemoteNode }) => {
      commandAccess.canManage = false;
      const defaultFetch = fetchMock.getMockImplementation()!;
      fetchMock.mockImplementation(
        (input: RequestInfo | URL, init?: RequestInit) =>
          String(input) === '/api/v1/notification-channels?remoteNode=local'
            ? jsonResponse({
                channels: [
                  {
                    id: 'command-1',
                    name: 'Desktop',
                    type: 'command',
                    enabled: true,
                    command: { command: 'exit 0' },
                    createdAt: '2026-05-17T00:00:00Z',
                    updatedAt: '2026-05-17T00:00:00Z',
                  },
                ],
              })
            : defaultFetch(input, init)
      );
      dagSettingsResponse = () =>
        jsonResponse({
          id: 'settings-1',
          dagName: 'example',
          enabled: true,
          events: ['dag_run_failed'],
          targets: [],
          subscriptions: [
            { id: 'subscription-1', channelId: 'command-1', enabled: true },
          ],
          createdAt: '2026-05-17T00:00:00Z',
          updatedAt: '2026-05-17T00:00:00Z',
        });
      renderTab(toolbarRemoteNode);

      const reason = await screen.findByText(
        'Only admins can change or test local command channels.'
      );
      const card = reason.closest<HTMLElement>('[data-slot="card"]');
      expect(card).not.toBeNull();
      expect(
        within(card!).getByRole('button', { name: /send test/i })
      ).toBeDisabled();
    }
  );

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

    expect(
      (await screen.findAllByText('Workflow override')).length
    ).toBeGreaterThan(0);
    expect(
      screen.getByText(
        'This workflow override has no destinations. Inherited rules are not used while the override exists.'
      )
    ).toBeVisible();
    expect(
      screen.getByText(
        'Inherited rules are not used while this workflow override exists. Add a channel or reset to inherit.'
      )
    ).toBeVisible();
    expect(
      screen.getByRole('button', { name: /reset to inherit/i })
    ).toBeVisible();
    expect(
      screen.getAllByRole('button', { name: /save changes/i })
    ).toHaveLength(1);
  });

  // parity:notif-provider-slack-teams-telegram
  it('shows the Slack and Telegram destination fields', async () => {
    dagSettingsResponse = () =>
      jsonResponse({
        id: 'settings-1',
        dagName: 'example',
        enabled: true,
        events: ['dag_run_failed'],
        targets: [
          {
            id: 'slack-target',
            name: 'Slack alerts',
            type: 'slack',
            enabled: true,
            events: [],
            slack: { webhookUrlConfigured: true, webhookUrlPreview: 'h...k' },
          },
          {
            id: 'telegram-target',
            name: 'Telegram alerts',
            type: 'telegram',
            enabled: true,
            events: [],
            telegram: {
              botTokenConfigured: true,
              botTokenPreview: '12...ab',
              chatId: '-100',
              topicId: '7',
            },
          },
        ],
        subscriptions: [],
        createdAt: '2026-05-17T00:00:00Z',
        updatedAt: '2026-05-17T00:00:00Z',
      });
    renderTab();

    expect(await screen.findByText('Slack webhook URL')).toBeVisible();
    expect(
      screen.getByPlaceholderText('Webhook URL configured (h...k)')
    ).toBeVisible();
    expect(screen.getByLabelText('Telegram topic ID')).toHaveValue('7');
    expect(screen.getByText('Chat ID')).toBeVisible();
  });

  // parity:notif-custom-destinations parity:notif-provider-webhook
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
