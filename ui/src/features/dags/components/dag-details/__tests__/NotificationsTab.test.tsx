import { render, screen, waitFor } from '@testing-library/react';
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
  terminalEnabled: true,
  gitSyncEnabled: true,
  agentEnabled: true,
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
  it('shows inherited rules instead of silently creating a DAG override', async () => {
    renderTab();

    expect(await screen.findByText('Inherited')).toBeVisible();
    expect(screen.getByText('This DAG inherits Global rules.')).toBeVisible();
    expect(screen.getByText('Effective Inherited Routes')).toBeVisible();
    expect(screen.getByText('Slack Ops')).toBeVisible();
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

  it('makes empty DAG overrides explicit and puts save next to destination edits', async () => {
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

    expect(await screen.findByText('DAG override')).toBeVisible();
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
      screen.getAllByRole('button', { name: /save changes/i }).length
    ).toBeGreaterThan(0);
  });
});
