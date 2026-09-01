// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { UserRole, ViewSpecType, ViewWorkspaceScope } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import { mainListItems as MainListItems } from '../menu';
import { defaultWorkspaceSelection } from '@/lib/workspace';

const useAuthMock = vi.fn();
const useIsAdminMock = vi.fn();
const useCanAccessSystemStatusMock = vi.fn();
const useCanAccessGitSyncMock = vi.fn();
const useCanViewEventLogsMock = vi.fn();
const useCanManageWebhooksMock = vi.fn();
const useCanManageProfilesMock = vi.fn();
const useCanViewAuditLogsMock = vi.fn();
const useHasFeatureMock = vi.fn();
const useViewsMock = vi.fn();

vi.mock('@/contexts/AuthContext', () => ({
  useAuth: () => useAuthMock(),
  useIsAdmin: () => useIsAdminMock(),
  useCanAccessSystemStatus: () => useCanAccessSystemStatusMock(),
  useCanAccessGitSync: () => useCanAccessGitSyncMock(),
  useCanViewEventLogs: () => useCanViewEventLogsMock(),
  useCanManageWebhooks: () => useCanManageWebhooksMock(),
  useCanManageProfiles: () => useCanManageProfilesMock(),
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

vi.mock('@/hooks/useViews', () => ({
  useViews: (type?: ViewSpecType) => useViewsMock(type),
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
  appBarOverride: Partial<React.ContextType<typeof AppBarContext>> = {},
  isOpen = true
): void {
  render(
    <UserPreferencesProvider>
      <I18nProvider>
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
              <MainListItems isOpen={isOpen} />
            </AppBarContext.Provider>
          </ConfigContext.Provider>
        </MemoryRouter>
      </I18nProvider>
    </UserPreferencesProvider>
  );
}

beforeEach(() => {
  localStorage.clear();
  document.documentElement.lang = 'en';
  useViewsMock.mockReset();
  useViewsMock.mockReturnValue({ views: [] });
  useAuthMock.mockReturnValue({
    user: { id: '1', username: 'admin', role: UserRole.admin },
  });
  useIsAdminMock.mockReturnValue(true);
  useCanAccessSystemStatusMock.mockReturnValue(true);
  useCanAccessGitSyncMock.mockReturnValue(true);
  useCanViewEventLogsMock.mockReturnValue(true);
  useCanManageWebhooksMock.mockReturnValue(true);
  useCanManageProfilesMock.mockReturnValue(true);
  useCanViewAuditLogsMock.mockReturnValue(true);
  useHasFeatureMock.mockReturnValue(true);
});

describe('sidebar menu', () => {
  it('localizes shell controls without English wrappers', () => {
    localStorage.setItem(
      'user_preferences',
      JSON.stringify({ locale: 'zh-CN' })
    );
    renderMenu(
      '/cockpit',
      {},
      { remoteNodes: ['local', 'worker-a'], selectedRemoteNode: 'worker-a' }
    );

    expect(
      screen.getByRole('combobox', { name: '远程节点' })
    ).toHaveTextContent('worker-a');
    expect(
      screen.getByRole('button', { name: '收起侧边栏' })
    ).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '集成' })).toHaveAttribute(
      'href',
      '/integrations'
    );
    expect(screen.getByRole('button', { name: '集成' })).toHaveAttribute(
      'aria-expanded',
      'false'
    );
    expect(
      screen.queryByRole('button', { name: /Toggle .* section/ })
    ).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: '配置与密钥' })).toHaveAttribute(
      'href',
      '/profiles'
    );
    expect(screen.getByRole('link', { name: '系统管理' })).toHaveAttribute(
      'href',
      '/administration'
    );
    expect(
      screen.getByRole('button', { name: '深色模式' })
    ).toBeInTheDocument();
  });

  it('keeps the compact language selector within its trigger', () => {
    renderMenu('/cockpit', {}, {}, false);

    const languageSelector = screen.getByRole('combobox', {
      name: 'Language',
    });
    expect(languageSelector).toHaveClass('h-7', 'w-7');
    expect(languageSelector.className).toContain('[&>svg:last-child]:hidden');
  });

  it('styles the sidebar language selector', () => {
    renderMenu('/cockpit');

    const languageSelector = screen.getByRole('combobox', {
      name: 'Language',
    });
    expect(languageSelector).toHaveClass('justify-start');
    expect(languageSelector.className).toContain('[&>svg:last-child]:ml-auto');
    expect(languageSelector.querySelector('svg')).toHaveClass(
      'text-sidebar-foreground'
    );
  });

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
      screen.queryByRole('button', { name: 'Overview' })
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
    expect(screen.getByRole('button', { name: 'Workflows' })).toHaveAttribute(
      'aria-expanded',
      'false'
    );
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
    expect(screen.getByRole('button', { name: 'Executions' })).toHaveAttribute(
      'aria-expanded',
      'false'
    );
    expect(screen.getByRole('link', { name: 'Monitor' })).toHaveAttribute(
      'href',
      '/system-status'
    );
    expect(screen.getByRole('button', { name: 'Monitor' })).toHaveAttribute(
      'aria-expanded',
      'false'
    );
    expect(screen.getByRole('link', { name: 'Notifications' })).toHaveAttribute(
      'href',
      '/notifications'
    );
    expect(
      screen.getByRole('button', { name: 'Notifications' })
    ).toHaveAttribute('aria-expanded', 'false');
    expect(
      screen.queryByRole('link', { name: 'Rules' })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('link', { name: 'Channels' })
    ).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Integrations' })).toHaveAttribute(
      'href',
      '/integrations'
    );
    expect(
      screen.getByRole('button', { name: 'Integrations' })
    ).toHaveAttribute('aria-expanded', 'false');
    expect(
      screen.getByRole('link', { name: 'Profiles & Secrets' })
    ).toHaveAttribute('href', '/profiles');
    expect(
      screen.getByRole('link', { name: 'Administration' })
    ).toHaveAttribute('href', '/administration');
    expect(
      screen.getByRole('button', { name: 'Administration' })
    ).toHaveAttribute('aria-expanded', 'false');

    expect(
      screen.queryByRole('link', { name: 'API Reference' })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('link', { name: 'Dashboard' })
    ).not.toBeInTheDocument();
  });

  it('expands the workflows section', () => {
    renderMenu();

    fireEvent.click(screen.getByRole('link', { name: 'Workflows' }));
    expect(screen.getByRole('button', { name: 'Workflows' })).toHaveAttribute(
      'aria-expanded',
      'false'
    );
    fireEvent.click(screen.getByRole('button', { name: 'Workflows' }));
    expect(screen.getByRole('button', { name: 'Workflows' })).toHaveAttribute(
      'aria-expanded',
      'true'
    );
    expect(screen.queryByRole('link', { name: 'Definitions' })).toBeNull();
    const submenuItems = [
      screen.getByRole('link', { name: 'Search' }),
      screen.getByRole('link', { name: 'Base Config' }),
      screen.getByRole('link', { name: 'Git Sync' }),
    ];
    for (const item of submenuItems) {
      expect(item).toBeVisible();
      expect(item.querySelector('svg')).toBeNull();
    }
  });

  it('shows Git Sync to all-workspace viewers', () => {
    useAuthMock.mockReturnValue({
      user: {
        id: 'viewer-1',
        username: 'viewer',
        role: UserRole.viewer,
        workspaceAccess: { all: true, grants: [] },
      },
    });

    renderMenu('/git-sync');
    fireEvent.click(screen.getByRole('button', { name: 'Workflows' }));

    expect(screen.getByRole('link', { name: 'Git Sync' })).toBeVisible();
  });

  it('hides Git Sync from workspace-scoped users', () => {
    useCanAccessGitSyncMock.mockReturnValue(false);

    renderMenu('/git-sync');
    fireEvent.click(screen.getByRole('button', { name: 'Workflows' }));

    expect(
      screen.queryByRole('link', { name: 'Git Sync' })
    ).not.toBeInTheDocument();
  });

  it('expands the executions section', () => {
    renderMenu();

    fireEvent.click(screen.getByRole('button', { name: 'Executions' }));
    expect(screen.queryByRole('link', { name: 'Runs' })).toBeNull();
    const queueLink = screen.getByRole('link', { name: 'Queues' });
    expect(queueLink).toBeVisible();
    expect(queueLink.querySelector('svg')).toBeNull();
  });

  it('expands the monitor section', () => {
    renderMenu();

    fireEvent.click(screen.getByRole('button', { name: 'Monitor' }));
    expect(
      screen.queryByRole('link', { name: 'System Status' })
    ).not.toBeInTheDocument();
    const submenuItems = [
      screen.getByRole('link', { name: 'Events' }),
      screen.getByRole('link', { name: 'Audit Logs' }),
    ];
    for (const item of submenuItems) {
      expect(item).toBeVisible();
      expect(item.querySelector('svg')).toBeNull();
    }
  });

  it('expands the notifications section', () => {
    renderMenu();

    fireEvent.click(screen.getByRole('button', { name: 'Notifications' }));
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

    fireEvent.click(screen.getByRole('button', { name: 'Integrations' }));
    const integrationSubmenuItems = [
      screen.getByRole('link', { name: 'Webhooks' }),
      screen.getByRole('link', { name: 'API Reference' }),
    ];
    for (const item of integrationSubmenuItems) {
      expect(item).toBeVisible();
      expect(item.querySelector('svg')).toBeNull();
    }
    fireEvent.click(screen.getByRole('button', { name: 'Administration' }));
    const accessSection = screen.getByRole('button', {
      name: 'Access',
    });
    const infrastructureSection = screen.getByRole('button', {
      name: 'Infrastructure',
    });
    expect(accessSection).toBeVisible();
    expect(
      accessSection.querySelector('svg:not(.lucide-chevron-down)')
    ).toBeNull();
    expect(infrastructureSection).toBeVisible();
    expect(
      infrastructureSection.querySelector('svg:not(.lucide-chevron-down)')
    ).toBeNull();
  });

  it('renders pinned views as standalone sidebar links', () => {
    useViewsMock.mockImplementation((type?: ViewSpecType) => ({
      views:
        type === ViewSpecType.workflow
          ? []
          : [{ id: 'v1', name: 'Prod board', pinned: true }],
    }));

    renderMenu('/');

    // Overview stays a flat link, not an accordion.
    expect(
      screen.queryByRole('button', { name: 'Overview' })
    ).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Overview' })).toHaveAttribute(
      'href',
      '/'
    );
    // The pinned view is its own top-level link.
    expect(screen.getByRole('link', { name: 'Prod board' })).toHaveAttribute(
      'href',
      '/views/v1'
    );
  });

  it('renders starred workflow views for the current scope in the sidebar', () => {
    useViewsMock.mockImplementation((type?: ViewSpecType) => ({
      views:
        type === ViewSpecType.workflow
          ? [
              {
                id: 'workflow-1',
                name: 'Production workflows',
                pinned: true,
                workspace: '',
              },
              {
                id: 'other-scope',
                name: 'Default workspace workflows',
                pinned: true,
                workspace: '',
                workspaceScope: ViewWorkspaceScope.default,
              },
            ]
          : [],
    }));

    renderMenu('/dags?view=workflow-1');

    expect(
      screen.getByRole('link', { name: 'Production workflows' })
    ).toHaveAttribute('href', '/dags?view=workflow-1');
    const starredViewLink = screen.getByRole('link', {
      name: 'Production workflows',
    });
    expect(starredViewLink).toHaveAttribute('aria-current', 'page');
    expect(starredViewLink.querySelector('svg')).toHaveClass('lucide-star');
    const workflowsLink = screen.getByRole('link', { name: 'Workflows' });
    expect(workflowsLink).not.toHaveAttribute('aria-current');
    expect(workflowsLink.querySelector('svg')).toHaveClass('lucide-network');
    expect(
      screen.queryByRole('link', { name: 'Default workspace workflows' })
    ).not.toBeInTheDocument();
  });

  it('keeps Workflows selected when the active view is not starred', () => {
    useViewsMock.mockImplementation((type?: ViewSpecType) => ({
      views:
        type === ViewSpecType.workflow
          ? [
              {
                id: 'workflow-1',
                name: 'Production workflows',
                pinned: false,
                workspace: '',
                workspaceScope: ViewWorkspaceScope.all,
              },
            ]
          : [],
    }));

    renderMenu('/dags?view=workflow-1');

    expect(
      screen.queryByRole('link', { name: 'Production workflows' })
    ).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Workflows' })).toHaveAttribute(
      'aria-current',
      'page'
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

  it('hides notifications for non-builtin auth without write permission', () => {
    renderMenu('/cockpit', {
      authMode: 'basic',
      permissions: { ...config.permissions, writeDags: false },
    });

    expect(
      screen.queryByRole('link', { name: 'Notifications' })
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

    expect(screen.queryByRole('link', { name: label })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Notifications' })).toHaveAttribute(
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
        name: new RegExp(`^${label}$`, 'i'),
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

    fireEvent.click(screen.getByRole('button', { name: /^administration$/i }));

    expect(screen.getByRole('button', { name: /^access$/i })).toHaveAttribute(
      'aria-expanded',
      'false'
    );
    expect(
      screen.getByRole('button', { name: /^infrastructure$/i })
    ).toHaveAttribute('aria-expanded', 'false');
  });

  it('does not append Pro labels to unavailable sidebar features', () => {
    useHasFeatureMock.mockReturnValue(false);

    renderMenu();

    fireEvent.click(screen.getByRole('button', { name: 'Monitor' }));
    expect(screen.getByRole('link', { name: 'Audit Logs' })).toBeVisible();
    expect(
      screen.queryByRole('link', { name: 'Audit Logs (Pro)' })
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Administration' }));
    fireEvent.click(screen.getByRole('button', { name: 'Access' }));
    expect(screen.getByRole('link', { name: 'Users' })).toBeVisible();
    expect(
      screen.queryByRole('link', { name: 'Users (Pro)' })
    ).not.toBeInTheDocument();
  });
});
