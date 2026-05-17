// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { UserRole } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { mainListItems as MainListItems } from '../menu';
import { defaultWorkspaceSelection } from '@/lib/workspace';

const useAuthMock = vi.fn();
const useIsAdminMock = vi.fn();
const useCanAccessSystemStatusMock = vi.fn();
const useCanViewEventLogsMock = vi.fn();
const useCanManageWebhooksMock = vi.fn();
const useCanManageSecretsMock = vi.fn();
const useCanViewAuditLogsMock = vi.fn();
const useHasFeatureMock = vi.fn();
const updatePreferenceMock = vi.fn();
const toggleChatMock = vi.fn();

vi.mock('@/contexts/AuthContext', () => ({
  useAuth: () => useAuthMock(),
  useIsAdmin: () => useIsAdminMock(),
  useCanAccessSystemStatus: () => useCanAccessSystemStatusMock(),
  useCanViewEventLogs: () => useCanViewEventLogsMock(),
  useCanManageWebhooks: () => useCanManageWebhooksMock(),
  useCanManageSecrets: () => useCanManageSecretsMock(),
  useCanViewAuditLogs: () => useCanViewAuditLogsMock(),
}));

vi.mock('@/hooks/useLicense', () => ({
  useHasFeature: (feature: string) => useHasFeatureMock(feature),
  useLicense: () => ({
    valid: true,
    plan: 'pro',
    expiry: '',
    features: [],
    gracePeriod: false,
    community: false,
    source: 'test',
    warningCode: '',
  }),
}));

vi.mock('../contexts/UserPreference', () => ({
  useUserPreferences: () => ({
    preferences: { theme: 'dark' },
    updatePreference: updatePreferenceMock,
  }),
}));

vi.mock('../features/agent', () => ({
  useAgentChatContext: () => ({ toggleChat: toggleChatMock }),
}));

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
    plan: 'community',
    expiry: '',
    features: ['audit', 'rbac'],
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
};

function renderMenu(
  initialEntry = '/cockpit',
  configOverride: Partial<Config> = {},
  appBarOverride: Partial<React.ContextType<typeof AppBarContext>> = {}
): void {
  render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <ConfigContext.Provider value={{ ...config, ...configOverride }}>
        <AppBarContext.Provider
          value={{
            title: '',
            setTitle: vi.fn(),
            remoteNodes: ['local'],
            setRemoteNodes: vi.fn(),
            selectedRemoteNode: 'local',
            selectRemoteNode: vi.fn(),
            workspaces: [],
            workspaceError: null,
            workspaceSelection: defaultWorkspaceSelection(),
            selectWorkspace: vi.fn(),
            createWorkspace: vi.fn(),
            deleteWorkspace: vi.fn(),
            ...appBarOverride,
          }}
        >
          <MainListItems isOpen />
        </AppBarContext.Provider>
      </ConfigContext.Provider>
    </MemoryRouter>
  );
}

beforeEach(() => {
  localStorage.clear();
  useAuthMock.mockReturnValue({
    user: { id: '1', username: 'admin', role: UserRole.admin },
  });
  useIsAdminMock.mockReturnValue(true);
  useCanAccessSystemStatusMock.mockReturnValue(true);
  useCanViewEventLogsMock.mockReturnValue(true);
  useCanManageWebhooksMock.mockReturnValue(true);
  useCanManageSecretsMock.mockReturnValue(true);
  useCanViewAuditLogsMock.mockReturnValue(true);
  useHasFeatureMock.mockReturnValue(true);
});

describe('sidebar menu', () => {
  it('hides the remote node selector when local is the only option', () => {
    renderMenu('/cockpit', {}, { remoteNodes: ['local'] });

    expect(
      screen.queryByRole('combobox', { name: /remote node/i })
    ).not.toBeInTheDocument();
  });

  it('shows the remote node selector when another node is available', () => {
    renderMenu(
      '/cockpit',
      {},
      {
        remoteNodes: ['local', 'worker-a'],
        selectedRemoteNode: 'worker-a',
      }
    );

    expect(
      screen.getByRole('combobox', { name: /remote node/i })
    ).toHaveTextContent('worker-a');
  });

  it('renders top-level operational sections as collapsed accordions', () => {
    renderMenu();

    expect(screen.getByRole('link', { name: 'Overview' })).toHaveAttribute(
      'href',
      '/'
    );
    expect(screen.getByRole('link', { name: 'Overview' })).toHaveAttribute(
      'aria-current',
      'page'
    );
    expect(
      screen.queryByRole('button', { name: 'Toggle Overview section' })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('link', { name: 'Timeline' })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('link', { name: 'Cockpit' })
    ).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Workflows' })).toHaveAttribute(
      'href',
      '/dags'
    );
    expect(
      screen.getByRole('button', { name: 'Toggle Workflows section' })
    ).toHaveAttribute('aria-expanded', 'false');
    expect(
      screen.queryByRole('link', { name: 'Definitions' })
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole('link', { name: 'Workflows' }).querySelector('svg')
    ).not.toBeNull();
    expect(screen.getByRole('link', { name: 'Executions' })).toHaveAttribute(
      'href',
      '/dag-runs'
    );
    expect(
      screen.getByRole('button', { name: 'Toggle Executions section' })
    ).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByRole('link', { name: 'Monitor' })).toHaveAttribute(
      'href',
      '/system-status'
    );
    expect(
      screen.getByRole('button', { name: 'Toggle Monitor section' })
    ).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByRole('link', { name: 'Notifications' })).toHaveAttribute(
      'href',
      '/notifications'
    );
    expect(
      screen.getByRole('button', { name: 'Toggle Notifications section' })
    ).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByRole('link', { name: 'Rules' })).not.toBeVisible();
    expect(screen.getByRole('link', { name: 'Channels' })).not.toBeVisible();
    expect(screen.getByRole('link', { name: 'Integrations' })).toHaveAttribute(
      'href',
      '/integrations'
    );
    expect(
      screen.getByRole('button', { name: 'Toggle Integrations section' })
    ).toHaveAttribute('aria-expanded', 'false');
    expect(
      screen.getByRole('link', { name: 'Administration' })
    ).toHaveAttribute('href', '/administration');
    expect(
      screen.getByRole('button', { name: 'Toggle Administration section' })
    ).toHaveAttribute('aria-expanded', 'false');

    expect(
      screen.queryByRole('link', { name: 'Docs' })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('link', { name: 'API Docs' })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('link', { name: 'Dashboard' })
    ).not.toBeInTheDocument();
  });

  it('expands workflow, execution, monitor, and notifications sections', () => {
    renderMenu();

    fireEvent.click(screen.getByRole('link', { name: 'Workflows' }));
    expect(
      screen.getByRole('button', { name: 'Toggle Workflows section' })
    ).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(
      screen.getByRole('button', { name: 'Toggle Workflows section' })
    );
    expect(
      screen.getByRole('button', { name: 'Toggle Workflows section' })
    ).toHaveAttribute('aria-expanded', 'true');
    expect(screen.queryByRole('link', { name: 'Definitions' })).toBeNull();
    expect(screen.getByRole('link', { name: 'Git Sync' })).toBeVisible();

    fireEvent.click(
      screen.getByRole('button', { name: 'Toggle Executions section' })
    );
    expect(screen.queryByRole('link', { name: 'Runs' })).toBeNull();
    const queueLink = screen.getByRole('link', { name: 'Queues' });
    expect(queueLink).toBeVisible();
    expect(queueLink.querySelector('svg')).toBeNull();

    fireEvent.click(
      screen.getByRole('button', { name: 'Toggle Monitor section' })
    );
    expect(
      screen.queryByRole('link', { name: 'System Status' })
    ).not.toBeInTheDocument();
    const monitorSubmenuItems = [
      screen.getByRole('link', { name: 'Events' }),
      screen.getByRole('link', { name: 'Audit Logs' }),
    ];
    for (const item of monitorSubmenuItems) {
      expect(item).toBeVisible();
      expect(item.querySelector('svg')).toBeNull();
    }

    const workflowSubmenuItems = [
      screen.getByRole('link', { name: 'Search' }),
      screen.getByRole('link', { name: 'Base Config' }),
      screen.getByRole('link', { name: 'Runbooks' }),
      screen.getByRole('link', { name: 'Git Sync' }),
    ];
    for (const item of workflowSubmenuItems) {
      expect(item).toBeVisible();
      expect(item.querySelector('svg')).toBeNull();
    }

    fireEvent.click(
      screen.getByRole('button', { name: 'Toggle Notifications section' })
    );
    const notificationSubmenuItems = [
      screen.getByRole('link', { name: 'Rules' }),
      screen.getByRole('link', { name: 'Channels' }),
    ];
    for (const item of notificationSubmenuItems) {
      expect(item).toBeVisible();
      expect(item.querySelector('svg')).toBeNull();
    }
    const [rulesLink, channelsLink] = notificationSubmenuItems;
    expect(rulesLink).toBeDefined();
    expect(channelsLink).toBeDefined();
    expect(
      rulesLink!.compareDocumentPosition(channelsLink!) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
  });

  it('expands integration and administration nested sections', () => {
    renderMenu();

    fireEvent.click(
      screen.getByRole('button', { name: 'Toggle Integrations section' })
    );
    const integrationSubmenuItems = [
      screen.getByRole('link', { name: 'Webhooks' }),
      screen.getByRole('link', { name: 'API Reference' }),
    ];
    for (const item of integrationSubmenuItems) {
      expect(item).toBeVisible();
      expect(item.querySelector('svg')).toBeNull();
    }
    fireEvent.click(
      screen.getByRole('button', { name: 'Toggle Administration section' })
    );
    const accessSection = screen.getByRole('button', {
      name: 'Access section',
    });
    const infrastructureSection = screen.getByRole('button', {
      name: 'Infrastructure section',
    });
    expect(accessSection).toBeVisible();
    expect(
      accessSection.querySelector('svg:not(.lucide-chevron-down)')
    ).toBeNull();
    expect(infrastructureSection).toBeVisible();
    expect(
      infrastructureSection.querySelector('svg:not(.lucide-chevron-down)')
    ).toBeNull();

    expect(screen.getByRole('link', { name: 'Agent' })).toHaveAttribute(
      'href',
      '/agent'
    );
    expect(
      screen.getByRole('button', { name: 'Toggle Agent section' })
    ).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(
      screen.getByRole('button', { name: 'Toggle Agent section' })
    );
    const agentSubmenuItems = [
      screen.getByRole('link', { name: 'Models' }),
      screen.getByRole('link', { name: 'Tools' }),
      screen.getByRole('link', { name: 'Memory' }),
      screen.getByRole('link', { name: 'Souls' }),
    ];
    for (const item of agentSubmenuItems) {
      expect(item).toBeVisible();
      expect(item.querySelector('svg')).toBeNull();
    }
    expect(screen.getByRole('link', { name: 'Models' })).toHaveAttribute(
      'href',
      '/agent-settings'
    );
    expect(screen.getByRole('link', { name: 'Tools' })).toHaveAttribute(
      'href',
      '/agent-tools'
    );
  });

  it('uses Workflows as the selectable Definitions entry', () => {
    renderMenu('/dags');

    expect(screen.getByRole('link', { name: /workflows/i })).toHaveAttribute(
      'href',
      '/dags'
    );
    expect(screen.getByRole('link', { name: /workflows/i })).toHaveAttribute(
      'aria-current',
      'page'
    );
    expect(
      screen.queryByRole('link', { name: /definitions/i })
    ).not.toBeInTheDocument();
  });

  it.each([
    ['/dag-runs', 'executions'],
    ['/system-status', 'monitor'],
    ['/notifications', 'notifications'],
    ['/integrations', 'integrations'],
    ['/administration', 'administration'],
  ])('marks %s as the active section entry', (path, label) => {
    renderMenu(path);

    expect(
      screen.getByRole('link', { name: new RegExp(label, 'i') })
    ).toHaveAttribute('aria-current', 'page');
  });

  it.each([
    ['/notification-rules', 'Rules'],
    ['/notification-channels', 'Channels'],
  ])('marks %s as the active notification item', (path, label) => {
    renderMenu(path);

    expect(screen.getByRole('link', { name: label })).toHaveAttribute(
      'aria-current',
      'page'
    );
  });

  it.each([
    ['/git-sync', 'workflows'],
    ['/queues', 'executions'],
    ['/event-logs', 'monitor'],
    ['/notification-channels', 'notifications'],
    ['/webhooks', 'integrations'],
    ['/users', 'administration'],
  ])('does not auto-expand %s inside %s', (path, label) => {
    renderMenu(path);

    expect(
      screen.getByRole('button', {
        name: new RegExp(`toggle ${label} section`, 'i'),
      })
    ).toHaveAttribute('aria-expanded', 'false');
  });

  it('does not auto-expand Administration nested groups when opened', () => {
    localStorage.setItem('navgroup_expanded_administration-access', 'true');
    localStorage.setItem(
      'navgroup_expanded_administration-infrastructure',
      'true'
    );

    renderMenu('/administration');

    fireEvent.click(
      screen.getByRole('button', { name: /toggle administration section/i })
    );

    expect(
      screen.getByRole('button', { name: /access section/i })
    ).toHaveAttribute('aria-expanded', 'false');
    expect(
      screen.getByRole('button', { name: /infrastructure section/i })
    ).toHaveAttribute('aria-expanded', 'false');
    expect(
      screen.getByRole('button', { name: /toggle agent section/i })
    ).toHaveAttribute('aria-expanded', 'false');
  });

  it('keeps agent settings reachable when agent is disabled', () => {
    renderMenu('/administration', { agentEnabled: false });

    fireEvent.click(
      screen.getByRole('button', { name: /toggle administration section/i })
    );
    expect(screen.getByRole('link', { name: /agent/i })).toHaveAttribute(
      'href',
      '/agent'
    );
    expect(
      screen.getByRole('button', { name: /toggle agent section/i })
    ).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(
      screen.getByRole('button', { name: /toggle agent section/i })
    );
    expect(screen.getByRole('link', { name: /models/i })).toHaveAttribute(
      'href',
      '/agent-settings'
    );
    expect(screen.getByRole('link', { name: /tools/i })).toHaveAttribute(
      'href',
      '/agent-tools'
    );
    expect(screen.getByRole('link', { name: /memory/i })).toBeVisible();
    expect(screen.getByRole('link', { name: /souls/i })).toBeVisible();
  });
});
