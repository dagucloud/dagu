// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Config } from '@/contexts/ConfigContext';
import App from '../App';

const { clientMock, clientGetMock } = vi.hoisted(() => {
  const clientGetMock = vi.fn();
  return {
    clientGetMock,
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
}));

vi.mock('../features/agent', () => ({
  AgentChatModal: () => null,
  AgentChatProvider: ({ children }: { children: React.ReactNode }) => (
    <>{children}</>
  ),
}));

vi.mock('../layouts/Layout', () => ({
  default: ({ children }: { children: React.ReactNode }) => (
    <main>{children}</main>
  ),
}));

vi.mock('../pages/agent', () => ({ default: () => <h1>Agent</h1> }));
vi.mock('../pages/agent-memory', () => ({
  default: () => <h1>Agent Memory</h1>,
}));
vi.mock('../pages/agent-settings', () => ({
  default: () => <h1>Agent Settings</h1>,
}));
vi.mock('../pages/agent-souls', () => ({
  default: () => <h1>Agent Souls</h1>,
}));
vi.mock('../pages/agent-souls/SoulEditorPage', () => ({
  default: () => <h1>Soul Editor</h1>,
}));
vi.mock('../pages/agent-tools', () => ({
  default: () => <h1>Agent Tools</h1>,
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
vi.mock('../pages/design', () => ({ default: () => <h1>Design</h1> }));
vi.mock('../pages/docs', () => ({ default: () => <h1>Docs</h1> }));
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
vi.mock('../pages/overview', () => ({ default: () => <h1>Overview</h1> }));
vi.mock('../pages/views', () => ({ default: () => <h1>View</h1> }));
vi.mock('../pages/profiles', () => ({ default: () => <h1>Profiles</h1> }));
vi.mock('../pages/queues', () => ({ default: () => <h1>Queues</h1> }));
vi.mock('../pages/queues/queue', () => ({
  default: () => <h1>Queue Details</h1>,
}));
vi.mock('../pages/search', () => ({ default: () => <h1>Search</h1> }));
vi.mock('../pages/secrets', () => ({ default: () => <h1>Secrets</h1> }));
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
    terminalEnabled: true,
    gitSyncEnabled: true,
    agentEnabled: false,
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
    },
    ...overrides,
  };
}

function renderAt(path: string, config = makeConfig()): void {
  window.history.pushState({}, '', path);
  render(<App config={config} />);
}

describe('App license routing', () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    clientGetMock.mockReset();
    clientGetMock.mockResolvedValue({ data: { workspaces: [] } });
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

  it.each([
    { path: '/notifications', heading: 'Notifications' },
    { path: '/notification-rules', heading: 'Notification Rules' },
    { path: '/notification-channels', heading: 'Notification Channels' },
  ])('allows $path in community mode', async ({ path, heading }) => {
    renderAt(path);

    expect(
      await screen.findByRole('heading', { name: heading })
    ).toBeVisible();
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
});
