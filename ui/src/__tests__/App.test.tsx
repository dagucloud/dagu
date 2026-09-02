// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Config } from '@/contexts/ConfigContext';
import { AppBarContext } from '@/contexts/AppBarContext';
import App from '../App';

const { clientMock, clientGetMock, overviewImportError, useQueryMock } =
  vi.hoisted(() => {
    const clientGetMock = vi.fn();
    return {
      clientGetMock,
      overviewImportError: { current: false },
      useQueryMock: vi.fn(),
      clientMock: {
        GET: clientGetMock,
        POST: vi.fn(),
        DELETE: vi.fn(),
      },
    };
  });

vi.hoisted(() => {
  vi.stubGlobal('getConfig', () => ({
    apiURL: '/api/v1',
    basePath: '/',
    version: 'test',
  }));
});

vi.mock('@/hooks/api', () => ({
  useClient: () => clientMock,
  useQuery: useQueryMock,
}));

vi.mock('../layouts/Layout', () => ({
  default: ({ children }: { children: React.ReactNode }) => (
    <main>{children}</main>
  ),
}));

vi.mock('../pages/administration', () => ({
  default: () => <h1>Administration</h1>,
}));
vi.mock('../pages/api-keys', () => ({ default: () => <h1>API Keys</h1> }));
vi.mock('../pages/api-docs', () => ({ default: () => <h1>API Docs</h1> }));
vi.mock('../pages/audit-logs', () => ({
  default: () => <h1>Audit Logs</h1>,
}));
vi.mock('../pages/base-config', () => ({
  default: () => <h1>Base Config</h1>,
}));
vi.mock('../pages/dag-runs', () => ({ default: () => <h1>DAG Runs</h1> }));
vi.mock('../pages/dag-runs/dag-run', () => ({
  default: () => <h1>DAG Run Details</h1>,
}));
vi.mock('../pages/dags', () => ({ default: () => <h1>DAGs</h1> }));
vi.mock('../pages/dags/dag', () => ({
  default: () => <h1>DAG Details</h1>,
}));
vi.mock('../pages/wiki', () => ({ default: () => <h1>Wiki</h1> }));
vi.mock('../pages/event-logs', () => ({
  default: () => <h1>Event Logs</h1>,
}));
vi.mock('../pages/git-sync', () => ({ default: () => <h1>Git Sync</h1> }));
vi.mock('../pages/home', () => ({ default: () => <h1>Home</h1> }));
vi.mock('../pages/incident-policies', () => ({
  default: () => <h1>Incident Routing</h1>,
}));
vi.mock('../pages/incident-providers', () => ({
  default: () => <h1>Incident Connections</h1>,
}));
vi.mock('../pages/incidents', () => ({
  default: () => <h1>Incidents</h1>,
}));
vi.mock('../pages/integrations', () => ({
  default: () => <h1>Integrations</h1>,
}));
vi.mock('../pages/license', () => ({ default: () => <h1>License</h1> }));
vi.mock('../pages/login', () => ({ default: () => <h1>Login</h1> }));
vi.mock('../pages/notification-channels', () => ({
  default: () => <h1>Notification Channels</h1>,
}));
vi.mock('../pages/notification-rules', () => ({
  default: () => <h1>Notification Rules</h1>,
}));
vi.mock('../pages/notifications', () => ({
  default: () => <h1>Notifications</h1>,
}));
vi.mock('../pages/overview', () => {
  if (overviewImportError.current) {
    throw new Error('Loading chunk failed');
  }
  return { default: () => <h1>Overview</h1> };
});
vi.mock('../pages/views', () => ({ default: () => <h1>View</h1> }));
vi.mock('../pages/profiles', () => ({
  default: () => <h1>Profiles &amp; Secrets</h1>,
}));
vi.mock('../pages/queues', () => ({ default: () => <h1>Queues</h1> }));
vi.mock('../pages/queues/queue', () => ({
  default: () => {
    const { setTitle } = React.useContext(AppBarContext);
    React.useEffect(() => {
      setTitle('Queue name is missing.');
    }, [setTitle]);
    return <h1>Queue Details</h1>;
  },
}));
vi.mock('../pages/search', () => ({
  default: () => {
    const { setTitle } = React.useContext(AppBarContext);
    React.useEffect(() => {
      setTitle('Search');
    }, [setTitle]);
    return <h1>Search</h1>;
  },
}));
vi.mock('../pages/setup', () => ({ default: () => <h1>Setup</h1> }));
vi.mock('../pages/system-status', () => ({
  default: () => <h1>System Status</h1>,
}));
vi.mock('../pages/terminal', () => ({ default: () => <h1>Terminal</h1> }));
vi.mock('../pages/remote-nodes', () => ({
  default: () => <h1>Remote Nodes</h1>,
}));
vi.mock('../pages/users', () => ({ default: () => <h1>Users</h1> }));
vi.mock('../pages/webhooks', () => ({ default: () => <h1>Webhooks</h1> }));

function makeConfig(overrides: Partial<Config> = {}): Config {
  return {
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
    authMode: 'none',
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
      valid: false,
      plan: 'community',
      expiry: '',
      features: [],
      gracePeriod: false,
      community: true,
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
      wikiDir: '',
      docsDir: '',
    },
    ...overrides,
  };
}

function renderAt(path: string, config = makeConfig()): void {
  window.history.pushState({}, '', path);
  render(<App config={config} />);
}

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  clientGetMock.mockReset();
  clientGetMock.mockResolvedValue({ data: { workspaces: [] } });
  useQueryMock.mockReset();
  useQueryMock.mockReturnValue({ data: undefined });
  overviewImportError.current = false;
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      Response.json({
        remoteNodes: [],
        type: 'object',
        properties: {},
      })
    )
  );
});

describe('App document title', () => {
  it('reflects the page title in the browser tab', async () => {
    renderAt('/search');

    await waitFor(() => {
      expect(document.title).toBe('Search - Dagu');
    });
  });

  it('localizes the page title', async () => {
    localStorage.setItem('user_preferences', JSON.stringify({ locale: 'ja' }));
    renderAt('/search');

    await waitFor(() => {
      expect(document.title).toBe('検索 - Dagu');
    });
  });

  it('preserves user-defined page titles', async () => {
    localStorage.setItem('user_preferences', JSON.stringify({ locale: 'ja' }));
    renderAt('/queues/name%20is%20missing.');

    await waitFor(() => {
      expect(document.title).toBe('Queue name is missing. - Dagu');
    });
  });

  it('falls back to the configured title when a page sets none', async () => {
    renderAt('/queues', makeConfig({ title: 'Operations' }));

    expect(
      await screen.findByRole('heading', { name: 'Queues' })
    ).toBeVisible();
    expect(document.title).toBe('Operations');
  });
});

describe('legacy Wiki routing', () => {
  it('preserves the path, query, and hash when redirecting from docs', async () => {
    renderAt('/docs/runbooks/deploy?workspace=ops#rollback');

    expect(await screen.findByRole('heading', { name: 'Wiki' })).toBeVisible();
    await waitFor(() => {
      expect(window.location.pathname).toBe('/wiki/runbooks/deploy');
      expect(window.location.search).toBe('?workspace=ops');
      expect(window.location.hash).toBe('#rollback');
    });
  });
});

describe('App license routing', () => {
  it.each([
    { path: '/notifications', heading: 'Notifications' },
    { path: '/notification-rules', heading: 'Notification Rules' },
    { path: '/notification-channels', heading: 'Notification Channels' },
  ])('allows $path in community mode', async ({ path, heading }) => {
    renderAt(path);

    expect(await screen.findByRole('heading', { name: heading })).toBeVisible();
    expect(
      screen.queryByRole('heading', { name: 'License Required' })
    ).not.toBeInTheDocument();
  });

  it('keeps incident management routes behind an active license', async () => {
    renderAt('/incidents');

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'License Required' })
      ).toBeVisible();
    });
    expect(
      screen.queryByRole('heading', { name: 'Incidents' })
    ).not.toBeInTheDocument();
  });

  it('updates licensed routes from the live license status', async () => {
    useQueryMock.mockReturnValue({
      data: {
        valid: true,
        plan: 'pro',
        expiry: '2027-01-01T00:00:00Z',
        features: ['audit'],
        gracePeriod: false,
        graceEndsAt: '',
        community: false,
        source: 'file',
        warningCode: '',
        error: '',
      },
    });

    renderAt('/incidents');

    expect(
      await screen.findByRole('heading', { name: 'Incidents' })
    ).toBeVisible();
    expect(useQueryMock).toHaveBeenCalledWith(
      '/license/status',
      { params: { query: { remoteNode: 'local' } } },
      expect.objectContaining({ refreshInterval: 60_000 })
    );
  });

  it('redirects the legacy secrets route to the secret refs section', async () => {
    renderAt('/secrets');

    expect(
      await screen.findByRole('heading', { name: 'Profiles & Secrets' })
    ).toBeVisible();
    expect(window.location.pathname).toBe('/profiles');
    expect(window.location.hash).toBe('#secret-refs');
  });

  it('offers a reload when a lazy route chunk fails to load', async () => {
    overviewImportError.current = true;
    const consoleError = vi
      .spyOn(console, 'error')
      .mockImplementation(() => undefined);

    renderAt('/');

    expect(
      await screen.findByRole('heading', { name: 'Unable to load this page' })
    ).toBeVisible();
    expect(screen.getByRole('button', { name: 'Reload' })).toBeVisible();

    consoleError.mockRestore();
  });
});
