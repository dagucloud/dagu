// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigate,
  useNavigationType,
} from 'react-router-dom';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import { defaultWorkspaceSelection } from '@/lib/workspace';
import type { ApiDAGDetails } from '../../contracts';
import { SettingsLeaveGuardProvider } from '../../settings/useSettingsLeaveGuard';
import { WebhookLauncherPanel } from '@/features/visual-editor/launchers/LauncherPanel';
import { WorkflowTabsPanel } from '../WorkflowTabsPanel';

const state = vi.hoisted(() => ({
  canManageProfiles: true,
  canWriteWiki: true,
  profile: '',
  queries: [] as string[],
}));

const client = vi.hoisted(() => ({
  GET: vi.fn(),
  POST: vi.fn(),
  PUT: vi.fn(),
  DELETE: vi.fn(),
}));

vi.mock('@/contexts/AuthContext', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/contexts/AuthContext')>()),
  useIsAdmin: () => true,
  useCanManageProfiles: () => state.canManageProfiles,
  useCanWriteForWorkspace: () => state.canWriteWiki,
  useCanManageCommandChannels: () => true,
  useCommandChannelsEnabled: () => true,
}));

// The control of the default profile is tested with its own file: here a
// button stands for an unsaved choice.
vi.mock('@/features/dags/components/dag-details/DAGSettingsTab', () => ({
  default: ({ onDirtyChange }: { onDirtyChange?: (d: boolean) => void }) => (
    <button type="button" onClick={() => onDirtyChange?.(true)}>
      choose another profile
    </button>
  ),
}));

vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: vi.fn() }),
}));

vi.mock('@/components/LicenseFeaturePrompt', () => ({
  LicenseFeaturePrompt: ({ feature }: { feature: string }) => (
    <div data-testid="license-prompt">{feature}</div>
  ),
}));

vi.mock('@/pages/wiki/components/CreateWikiPageModal', () => ({
  CreateWikiPageModal: ({ isOpen }: { isOpen: boolean }) =>
    isOpen ? <div data-testid="create-wiki-page" /> : null,
}));

// The real SWR runs the queries, so refreshInterval polls as in the app.
vi.mock('@/hooks/api', async () => {
  const { default: useSWR } = await import('swr');
  const data = (path: string): unknown => {
    switch (path) {
      case '/dags/{fileName}/settings':
        return { profile: state.profile };
      case '/profiles':
        return {
          profiles: [
            {
              id: 'p-1',
              name: 'prod',
              status: 'active',
              protected: false,
              entries: [],
            },
            {
              id: 'p-2',
              name: 'staging',
              status: 'active',
              protected: false,
              entries: [],
            },
          ],
        };
      case '/profiles/_global':
        return { name: '_global', status: 'active', entries: [] };
      case '/profiles/_workspaces/{workspaceName}':
        return { name: '_workspaces/ops', status: 'active', entries: [] };
      case '/dags/{fileName}/webhook/public':
        return {
          state: 'active',
          url: 'https://hooks.example/x',
          deliveries: [],
        };
      case '/incident-providers':
        return { providers: [] };
      case '/dags/{fileName}/incidents':
        return {};
      case '/wiki':
        return {
          items: [
            { id: 'nightly/runbook', title: 'Nightly runbook', tags: [] },
          ],
        };
      default:
        return { items: [] };
    }
  };
  return {
    useClient: () => client,
    useQuery: (path: string, init: unknown, config?: object) =>
      useSWR(
        init === null ? null : [path, JSON.stringify(init)],
        async () => {
          state.queries.push(path);
          return data(path);
        },
        config
      ),
  };
});

const config = (licensed: boolean): Config => ({
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
  permissions: { writeDags: true, runDags: true },
  license: {
    valid: licensed,
    plan: licensed ? 'pro' : '',
    expiry: '',
    features: [],
    gracePeriod: false,
    community: !licensed,
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
});

const webhook = {
  id: 'webhook-1',
  dagName: 'nightly',
  tokenPrefix: 'dagu_wh_',
  enabled: true,
  authMode: 'token_only',
  hmac: { enabled: false, secretConfigured: false },
  profileSelection: { allowedProfiles: [] },
  createdAt: '2026-10-01T00:00:00Z',
  updatedAt: '2026-10-01T00:00:00Z',
  public: {
    secretConfigured: false,
    events: [],
    createdAt: '2026-10-01T00:00:00Z',
  },
};

function json(body: unknown, status = 200): Promise<Response> {
  return Promise.resolve(
    new Response(JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })
  );
}

function routeFetch(input: RequestInfo | URL, init?: RequestInit) {
  const url = String(input);
  const method = init?.method ?? 'GET';
  const q = '?remoteNode=local';
  if (url === `/api/v1/dags/nightly.yaml/webhook${q}`) {
    return method === 'POST'
      ? json({ webhook, token: 'one-time-token' })
      : json(webhook);
  }
  if (url === `/api/v1/dags/nightly.yaml/notifications${q}`) {
    return json({ message: 'not found' }, 404);
  }
  if (url === `/api/v1/notification-channels${q}`) {
    return json({ channels: [] });
  }
  if (url === `/api/v1/notification-routes/global${q}`) {
    return json({
      scope: 'global',
      enabled: true,
      inheritGlobal: true,
      routes: [],
    });
  }
  if (url === `/api/v1/notification-routes/workspaces/ops${q}`) {
    return json({
      scope: 'workspace',
      workspace: 'ops',
      enabled: true,
      inheritGlobal: false,
      routes: [],
    });
  }
  return json({ message: `unexpected ${url}` }, 500);
}

const fetchMock = vi.fn(routeFetch);

const dag = { name: 'nightly' } as ApiDAGDetails;

function Page() {
  const location = useLocation();
  const navigationType = useNavigationType();
  const navigate = useNavigate();
  return (
    <SettingsLeaveGuardProvider>
      <output data-testid="location">
        {location.pathname + location.search}
      </output>
      <output data-testid="navigation">{navigationType}</output>
      <button type="button" onClick={() => navigate(-1)}>
        Back
      </button>
      <div className="flex h-96 flex-col">
        <WorkflowTabsPanel
          overview={<p>overview body</p>}
          dag={dag}
          fileName="nightly.yaml"
          workspaceName="ops"
        />
      </div>
    </SettingsLeaveGuardProvider>
  );
}

function renderAt(
  entries: string[],
  { licensed = true }: { licensed?: boolean } = {}
) {
  return render(
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
      <ConfigContext.Provider value={config(licensed)}>
        <AppBarContext.Provider
          value={
            {
              selectedRemoteNode: 'local',
              workspaceSelection: defaultWorkspaceSelection(),
            } as never
          }
        >
          <RemoteNodeProvider remoteNode="local">
            <MemoryRouter
              initialEntries={entries}
              initialIndex={entries.length - 1}
            >
              <Routes>
                <Route path="/dags/:fileName" element={<Page />} />
                <Route path="/dags/:fileName/:tab" element={<Page />} />
              </Routes>
            </MemoryRouter>
          </RemoteNodeProvider>
        </AppBarContext.Provider>
      </ConfigContext.Provider>
    </SWRConfig>
  );
}

const tablist = () =>
  screen.getByRole('tablist', { name: 'Workflow sections' });
const tab = (name: string) => within(tablist()).getByRole('tab', { name });
const location = () => screen.getByTestId('location').textContent;
const navigation = () => screen.getByTestId('navigation').textContent;

async function makeNotificationsDirty(
  user: ReturnType<typeof userEvent.setup>
) {
  await user.click(
    await screen.findByRole('button', { name: /configure workflow override/i })
  );
}

beforeEach(() => {
  state.canManageProfiles = true;
  state.canWriteWiki = true;
  state.profile = '';
  state.queries = [];
  client.GET.mockReset().mockResolvedValue({ data: { profiles: [] } });
  client.POST.mockReset().mockResolvedValue({ data: {} });
  client.PUT.mockReset().mockResolvedValue({ data: {} });
  client.DELETE.mockReset().mockResolvedValue({ data: {} });
  fetchMock.mockReset().mockImplementation(routeFetch);
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('WorkflowTabsPanel', () => {
  it('shows the overview by default, with the four tabs', () => {
    renderAt(['/dags/nightly.yaml']);

    expect(
      within(tablist())
        .getAllByRole('tab')
        .map((t) => t.textContent)
    ).toEqual(['Overview', 'Wiki', 'Incidents', 'Notifications']);
    expect(tab('Overview')).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByText('overview body')).toBeVisible();
    expect(screen.getByRole('tabpanel', { name: 'Overview' })).toBeVisible();
  });

  it('opens the tab that ?wtab names', async () => {
    renderAt(['/dags/nightly.yaml?wtab=wiki']);

    expect(tab('Wiki')).toHaveAttribute('aria-selected', 'true');
    expect(
      await screen.findByRole('link', { name: /Nightly runbook/ })
    ).toBeVisible();
    expect(screen.queryByText('overview body')).toBeNull();
  });

  // parity:test-wiki-tab parity:wiki-list parity:wiki-new-runbook
  // parity:shell-wiki-tab-hidden-on-page
  it('mounts the wiki, notifications and incidents tabs', async () => {
    const user = userEvent.setup();
    renderAt(['/dags/nightly.yaml']);

    await user.click(tab('Notifications'));
    expect(await screen.findByText('Workflow notifications')).toBeVisible();
    expect(
      (await screen.findAllByText(/ops workspace rules/)).length
    ).toBeGreaterThan(0);

    await user.click(tab('Incidents'));
    expect(await screen.findByText('Workflow incidents')).toBeVisible();

    await user.click(tab('Wiki'));
    await user.click(
      await screen.findByRole('button', { name: 'New runbook' })
    );
    expect(screen.getByTestId('create-wiki-page')).toBeInTheDocument();
  });

  it('gates the incidents tab behind the license', async () => {
    renderAt(['/dags/nightly.yaml?wtab=incidents'], { licensed: false });

    expect(await screen.findByTestId('license-prompt')).toHaveTextContent(
      'incidents'
    );
  });

  // The runtime profile is a section of the Overview tab. The old Profile tab
  // is gone: its link opens the Overview.
  it('shows the runtime profile in the overview, and has no Profile tab', async () => {
    renderAt(['/dags/nightly.yaml']);

    expect(
      await screen.findByRole('region', { name: 'Runtime profile' })
    ).toBeVisible();
    expect(
      within(screen.getByRole('tabpanel', { name: 'Overview' })).getByText(
        'overview body'
      )
    ).toBeVisible();
    expect(
      within(tablist()).queryByRole('tab', { name: 'Profile' })
    ).toBeNull();
  });

  it('opens the overview for the old ?wtab=profile link', async () => {
    renderAt(['/dags/nightly.yaml?wtab=profile']);

    expect(tab('Overview')).toHaveAttribute('aria-selected', 'true');
    expect(
      await screen.findByRole('region', { name: 'Runtime profile' })
    ).toBeVisible();
  });

  it('shows the runtime profile on no other tab', async () => {
    const user = userEvent.setup();
    renderAt(['/dags/nightly.yaml']);
    await screen.findByRole('region', { name: 'Runtime profile' });

    await user.click(tab('Wiki'));

    expect(
      screen.queryByRole('region', { name: 'Runtime profile' })
    ).toBeNull();
  });

  it('puts the tab in the URL with replace and keeps the query', async () => {
    const user = userEvent.setup();
    renderAt(['/dags/nightly.yaml?remoteNode=local&foo=bar']);

    await user.click(tab('Wiki'));
    expect(location()).toBe(
      '/dags/nightly.yaml?wtab=wiki&remoteNode=local&foo=bar'
    );
    expect(navigation()).toBe('REPLACE');

    await user.click(tab('Overview'));
    expect(location()).toBe('/dags/nightly.yaml?remoteNode=local&foo=bar');
  });

  // The panel is 360 to 560 px wide in a window of any width: its tabs must
  // follow the panel width, so the body is a query container.
  it('makes the tab body a query container', () => {
    renderAt(['/dags/nightly.yaml?wtab=wiki']);

    expect(screen.getByRole('tabpanel', { name: 'Wiki' })).toHaveClass(
      '@container'
    );
  });

  it('is a tablist with a roving tabindex and a labelled panel', () => {
    renderAt(['/dags/nightly.yaml?wtab=wiki']);

    const tabs = within(tablist()).getAllByRole('tab');
    expect(tabs.map((t) => t.getAttribute('aria-selected'))).toEqual([
      'false',
      'true',
      'false',
      'false',
    ]);
    expect(tabs.map((t) => t.tabIndex)).toEqual([-1, 0, -1, -1]);
    const panel = screen.getByRole('tabpanel', { name: 'Wiki' });
    expect(tabs[1]).toHaveAttribute('aria-controls', panel.id);
  });

  it('is a 32 px row of tabs', () => {
    renderAt(['/dags/nightly.yaml']);

    expect(tablist()).toHaveClass('h-8');
  });

  it('opens a tab with the arrows, Home and End and keeps one tab stop', async () => {
    const user = userEvent.setup();
    renderAt(['/dags/nightly.yaml']);

    tab('Overview').focus();
    await user.keyboard('{ArrowRight}');
    expect(tab('Wiki')).toHaveFocus();
    expect(tab('Wiki')).toHaveAttribute('aria-selected', 'true');
    expect(location()).toBe('/dags/nightly.yaml?wtab=wiki');
    await user.keyboard('{End}');
    expect(tab('Notifications')).toHaveFocus();
    await user.keyboard('{ArrowRight}');
    expect(tab('Overview')).toHaveFocus();
    expect(location()).toBe('/dags/nightly.yaml');
    await user.keyboard('{ArrowLeft}');
    expect(tab('Notifications')).toHaveFocus();
    await user.keyboard('{Home}');
    expect(tab('Overview')).toHaveFocus();
    expect(
      within(tablist())
        .getAllByRole('tab')
        .filter((t) => t.tabIndex === 0)
    ).toHaveLength(1);
  });

  // The runtime profile choice is a draft too: leaving the Overview asks.
  it('asks before a tab change leaves an unsaved profile choice', async () => {
    const user = userEvent.setup();
    renderAt(['/dags/nightly.yaml']);
    await user.click(
      await screen.findByRole('button', { name: 'choose another profile' })
    );

    await user.click(tab('Wiki'));

    const confirm = await screen.findByRole('dialog', {
      name: 'Leave without saving?',
    });
    expect(location()).toBe('/dags/nightly.yaml');
    await user.click(within(confirm).getByRole('button', { name: 'Leave' }));
    await waitFor(() =>
      expect(location()).toBe('/dags/nightly.yaml?wtab=wiki')
    );

    // The draft left with its section: no question on the way back.
    await user.click(tab('Overview'));
    expect(location()).toBe('/dags/nightly.yaml');
  });

  it('asks before a tab change leaves a dirty draft', async () => {
    const user = userEvent.setup();
    renderAt(['/dags/nightly.yaml?wtab=notifications']);
    await makeNotificationsDirty(user);

    await user.click(tab('Wiki'));

    const confirm = await screen.findByRole('dialog', {
      name: 'Leave without saving?',
    });
    expect(location()).toBe('/dags/nightly.yaml?wtab=notifications');
    await user.click(within(confirm).getByRole('button', { name: 'Cancel' }));
    expect(tab('Notifications')).toHaveAttribute('aria-selected', 'true');

    await user.click(tab('Wiki'));
    await user.click(
      within(
        await screen.findByRole('dialog', { name: 'Leave without saving?' })
      ).getByRole('button', { name: 'Leave' })
    );
    await waitFor(() =>
      expect(location()).toBe('/dags/nightly.yaml?wtab=wiki')
    );
  });
});

// parity:chg-settings-back - Browser Back and Forward are not guarded (no
// useBlocker under BrowserRouter): the draft is lost, as on today's tabs.
describe('WorkflowTabsPanel browser Back', () => {
  it('does not guard browser Back', async () => {
    const user = userEvent.setup();
    renderAt(['/dags/nightly.yaml', '/dags/nightly.yaml?wtab=notifications']);
    await makeNotificationsDirty(user);
    expect(screen.getByText('Unsaved changes')).toBeVisible();

    await user.click(screen.getByRole('button', { name: 'Back' }));

    expect(location()).toBe('/dags/nightly.yaml');
    expect(
      screen.queryByRole('dialog', { name: 'Leave without saving?' })
    ).toBeNull();
  });
});

// parity:webhook-public-state
describe('Webhook launcher panel public URL polling', () => {
  it('stops polling the public URL when the panel closes', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const publicReads = () =>
      state.queries.filter((p) => p === '/dags/{fileName}/webhook/public')
        .length;
    const view = render(
      <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
        <ConfigContext.Provider value={config(true)}>
          <AppBarContext.Provider
            value={
              {
                selectedRemoteNode: 'local',
                workspaceSelection: defaultWorkspaceSelection(),
              } as never
            }
          >
            <RemoteNodeProvider remoteNode="local">
              <MemoryRouter>
                <WebhookLauncherPanel
                  fileName="nightly.yaml"
                  dagName="nightly"
                  onClose={() => undefined}
                />
              </MemoryRouter>
            </RemoteNodeProvider>
          </AppBarContext.Provider>
        </ConfigContext.Provider>
      </SWRConfig>
    );

    expect(await screen.findByText('Public URL')).toBeVisible();
    await waitFor(() => expect(publicReads()).toBe(1));
    await act(() => vi.advanceTimersByTimeAsync(10_000));
    expect(publicReads()).toBe(2);

    view.unmount();
    const reads = publicReads();
    await act(() => vi.advanceTimersByTimeAsync(60_000));
    expect(publicReads()).toBe(reads);
  });
});
