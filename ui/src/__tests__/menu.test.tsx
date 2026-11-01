// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from '@testing-library/react';
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

vi.mock('@/components/LicenseBadge', () => ({ LicenseBadge: () => null }));
vi.mock('@/components/WorkspaceDeprecationNotice', () => ({
  WorkspaceDeprecationNotice: () => null,
}));

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
  upgradeCommand: '',
  restartRequired: false,
  restartCommand: '',
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
  isOpen = true,
  hideHeader = false
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
              <MainListItems isOpen={isOpen} hideHeader={hideHeader} />
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
  // The mobile drawer has its own title and close button.
  it('leaves the title and collapse toggle to a container that has its own', () => {
    renderMenu('/cockpit', {}, {}, true, true);

    expect(
      screen.queryByRole('button', { name: 'Collapse sidebar' })
    ).not.toBeInTheDocument();
    expect(screen.getByRole('navigation', { name: 'Primary' })).toBeVisible();
  });

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
    expect(screen.getByRole('link', { name: 'Webhook' })).toHaveAttribute(
      'href',
      '/webhooks'
    );
    expect(screen.getByRole('link', { name: '配置与密钥' })).toHaveAttribute(
      'href',
      '/profiles'
    );
    expect(screen.getByRole('group', { name: '系统管理' })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: '主题' })).toHaveAttribute(
      'title',
      '跟随系统'
    );
  });

  it('keeps the compact language selector within its trigger', () => {
    renderMenu('/cockpit', {}, {}, false);

    const languageSelector = screen.getByRole('combobox', {
      name: 'Language',
    });
    expect(languageSelector).toHaveClass('h-7', 'w-7');
    expect(languageSelector.className).toContain('[&>svg:last-child]:hidden');
  });

  it('keeps theme and language as icon controls beside the user menu', () => {
    renderMenu('/cockpit');

    expect(screen.getByRole('combobox', { name: 'Language' })).toHaveClass(
      'h-7',
      'w-7'
    );
    expect(screen.getByRole('combobox', { name: 'Theme' })).toHaveAttribute(
      'title',
      'System'
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

  it('opens search from the sidebar with a keyboard shortcut hint', () => {
    renderMenu();

    expect(screen.getByRole('button', { name: 'Search...' })).toHaveAttribute(
      'aria-keyshortcuts',
      'Meta+K Control+K'
    );
  });

  // Every page is one click away: no accordion groups hide destinations.
  it('lists every page as a direct link in its section', () => {
    renderMenu();

    const expectLinks = (
      container: HTMLElement,
      links: [name: string, href: string][]
    ) => {
      for (const [name, href] of links) {
        expect(within(container).getByRole('link', { name })).toHaveAttribute(
          'href',
          href
        );
      }
    };

    expectLinks(document.body, [
      ['Overview', '/'],
      ['Workflows', '/dags'],
      ['Executions', '/dag-runs'],
      ['Queues', '/queues'],
      ['Artifacts', '/artifacts'],
      ['Wiki', '/wiki'],
    ]);
    expectLinks(screen.getByRole('group', { name: 'Monitor' }), [
      ['System Status', '/system-status'],
      ['Events', '/event-logs'],
      ['Audit Logs', '/audit-logs'],
      ['Notifications', '/notifications'],
      ['Incidents', '/incidents'],
      ['External Monitoring', '/license#monitoring'],
    ]);
    expectLinks(screen.getByRole('group', { name: 'Configure' }), [
      ['Profiles & Secrets', '/profiles'],
      ['Base Config', '/base-config'],
      ['Webhooks', '/webhooks'],
      ['Git Sync', '/git-sync'],
      ['API Reference', '/api-docs'],
    ]);
    expectLinks(screen.getByRole('group', { name: 'Administration' }), [
      ['Users', '/users'],
      ['API Keys', '/api-keys'],
      ['Remote Nodes', '/remote-nodes'],
      ['Terminal', '/terminal'],
    ]);
    expect(
      screen.queryByRole('button', { name: 'Workflows' })
    ).not.toBeInTheDocument();
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

    expect(screen.getByRole('link', { name: 'Git Sync' })).toBeVisible();
  });

  it('hides Git Sync from workspace-scoped users', () => {
    useCanAccessGitSyncMock.mockReturnValue(false);

    renderMenu('/git-sync');

    expect(
      screen.queryByRole('link', { name: 'Git Sync' })
    ).not.toBeInTheDocument();
  });

  it('omits the Administration section for non-admins', () => {
    useIsAdminMock.mockReturnValue(false);

    renderMenu();

    expect(
      screen.queryByRole('group', { name: 'Administration' })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('link', { name: 'Remote Nodes' })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('link', { name: 'External Monitoring' })
    ).not.toBeInTheDocument();
  });

  it('renders pinned views as standalone sidebar links', () => {
    useViewsMock.mockImplementation((type?: ViewSpecType) => ({
      views:
        type === undefined
          ? [{ id: 'v1', name: 'Prod board', pinned: true }]
          : [],
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

  describe('bookmark ordering', () => {
    const names = ['Board', 'Runs', 'Workflows view', 'Reports'];

    function bookmarkNames(): (string | null)[] {
      return screen
        .getAllByRole('link')
        .map((link) => link.textContent)
        .filter((name) => names.includes(name ?? ''));
    }

    beforeEach(() => {
      useViewsMock.mockImplementation((type?: ViewSpecType) => ({
        views: [
          {
            id: type ?? 'kanban',
            name: names[
              [
                undefined,
                ViewSpecType.run,
                ViewSpecType.workflow,
                ViewSpecType.artifact,
              ].indexOf(type)
            ],
            pinned: true,
            workspaceScope: ViewWorkspaceScope.all,
          },
        ],
      }));
    });

    it('reorders across view types by dragging and remembers the order', () => {
      renderMenu();
      const board = screen.getByRole('link', { name: 'Board' });
      const reports = screen.getByRole('link', { name: 'Reports' });
      const dataTransfer = {
        setData: vi.fn(),
        effectAllowed: '',
        dropEffect: '',
      };

      fireEvent.dragStart(reports, { dataTransfer });
      fireEvent.dragOver(board, { dataTransfer });
      fireEvent.drop(board, { dataTransfer });
      fireEvent.dragEnd(reports, { dataTransfer });

      expect(bookmarkNames()).toEqual([
        'Reports',
        'Board',
        'Runs',
        'Workflows view',
      ]);
      expect(screen.getByRole('link', { name: 'Overview' })).toHaveAttribute(
        'aria-current',
        'page'
      );
      cleanup();
      renderMenu();
      expect(bookmarkNames()).toEqual([
        'Reports',
        'Board',
        'Runs',
        'Workflows view',
      ]);
      fireEvent.click(screen.getByRole('link', { name: 'Reports' }));
      expect(screen.getByRole('link', { name: 'Reports' })).toHaveAttribute(
        'aria-current',
        'page'
      );
    });

    it('moves focused bookmarks with Alt and arrow keys, including when collapsed', () => {
      renderMenu('/cockpit', {}, {}, false);
      const board = screen.getByRole('link', { name: 'Board' });
      board.focus();

      fireEvent.keyDown(board, { key: 'ArrowUp', altKey: true });
      expect(bookmarkNames()).toEqual(names);
      fireEvent.keyDown(board, { key: 'ArrowDown' });
      expect(bookmarkNames()).toEqual(names);
      fireEvent.keyDown(board, { key: 'ArrowDown', altKey: true });
      expect(bookmarkNames()).toEqual([
        'Runs',
        'Board',
        'Workflows view',
        'Reports',
      ]);
      expect(board).toHaveFocus();
      fireEvent.keyDown(board, { key: 'ArrowUp', altKey: true });
      expect(bookmarkNames()).toEqual(names);
    });

    it('preserves hidden bookmarks and separate remote orders, appending new bookmarks', () => {
      localStorage.setItem(
        'user_preferences',
        JSON.stringify({
          pinnedViewOrder: {
            local: ['workflow', 'hidden-workspace', 'kanban', 'run'],
            remote: ['artifact', 'run', 'workflow', 'kanban'],
          },
        })
      );
      renderMenu();
      expect(bookmarkNames()).toEqual([
        'Workflows view',
        'Board',
        'Runs',
        'Reports',
      ]);
      fireEvent.keyDown(screen.getByRole('link', { name: 'Board' }), {
        key: 'ArrowUp',
        altKey: true,
      });
      cleanup();

      renderMenu('/cockpit', {}, { selectedRemoteNode: 'remote' });
      expect(bookmarkNames()).toEqual([
        'Reports',
        'Runs',
        'Workflows view',
        'Board',
      ]);
      cleanup();

      useViewsMock.mockImplementation((type?: ViewSpecType) => ({
        views:
          type === undefined
            ? [
                { id: 'kanban', name: 'Board', pinned: true },
                {
                  id: 'hidden-workspace',
                  name: 'Hidden workspace',
                  pinned: true,
                },
                { id: 'workflow', name: 'Workflows view', pinned: true },
              ]
            : [],
      }));
      renderMenu();
      expect(
        screen
          .getAllByRole('link')
          .slice(0, 3)
          .map((link) => link.textContent)
      ).toEqual(['Board', 'Hidden workspace', 'Workflows view']);
    });

    it('keeps the order when a drag is cancelled or comes from outside the bookmarks', () => {
      renderMenu();
      const board = screen.getByRole('link', { name: 'Board' });
      const reports = screen.getByRole('link', { name: 'Reports' });
      const dataTransfer = {
        setData: vi.fn(),
        effectAllowed: '',
        dropEffect: '',
      };

      fireEvent.drop(board, { dataTransfer });
      expect(bookmarkNames()).toEqual(names);
      fireEvent.dragStart(reports, { dataTransfer });
      fireEvent.dragOver(board, { dataTransfer });
      fireEvent.dragEnd(reports, { dataTransfer });
      expect(bookmarkNames()).toEqual(names);
      cleanup();
      renderMenu();
      expect(bookmarkNames()).toEqual(names);
    });
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

  it('renders starred run views for the current scope in the sidebar', () => {
    useViewsMock.mockImplementation((type?: ViewSpecType) => ({
      views:
        type === ViewSpecType.run
          ? [
              {
                id: 'run-1',
                name: 'Failed runs',
                pinned: true,
                workspace: '',
                workspaceScope: ViewWorkspaceScope.all,
              },
            ]
          : [],
    }));

    renderMenu('/dag-runs?view=run-1');

    const runViewLink = screen.getByRole('link', { name: 'Failed runs' });
    expect(runViewLink).toHaveAttribute('href', '/dag-runs?view=run-1');
    expect(runViewLink).toHaveAttribute('aria-current', 'page');
    expect(runViewLink.querySelector('svg')).toHaveClass('lucide-star');
    const executionsLink = screen.getByRole('link', { name: 'Executions' });
    expect(executionsLink).not.toHaveAttribute('aria-current');
  });

  it('renders starred artifact views for the current scope in the sidebar', () => {
    useViewsMock.mockImplementation((type?: ViewSpecType) => ({
      views:
        type === ViewSpecType.artifact
          ? [
              {
                id: 'artifact-1',
                name: 'Nightly reports',
                pinned: true,
                workspace: '',
                workspaceScope: ViewWorkspaceScope.all,
              },
            ]
          : [],
    }));

    renderMenu('/artifacts?view=artifact-1');

    const artifactViewLink = screen.getByRole('link', {
      name: 'Nightly reports',
    });
    expect(artifactViewLink).toHaveAttribute(
      'href',
      '/artifacts?view=artifact-1'
    );
    expect(artifactViewLink).toHaveAttribute('aria-current', 'page');
    expect(artifactViewLink.querySelector('svg')).toHaveClass('lucide-star');
    const artifactsLink = screen.getByRole('link', { name: 'Artifacts' });
    expect(artifactsLink).not.toHaveAttribute('aria-current');
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
    ['/dag-runs', 'Executions'],
    ['/dags/example', 'Workflows'],
    ['/queues', 'Queues'],
    ['/artifacts', 'Artifacts'],
    ['/system-status', 'System Status'],
    ['/event-logs', 'Events'],
    ['/notification-rules', 'Notifications'],
    ['/notification-channels', 'Notifications'],
    ['/incident-policies', 'Incidents'],
    ['/webhooks', 'Webhooks'],
    ['/users', 'Users'],
  ])('marks %s as the active entry', (path, label) => {
    renderMenu(path);

    expect(screen.getByRole('link', { name: label })).toHaveAttribute(
      'aria-current',
      'page'
    );
  });

  it('does not append Pro labels to unavailable sidebar features', () => {
    useHasFeatureMock.mockReturnValue(false);

    renderMenu();

    expect(screen.getByRole('link', { name: 'Audit Logs' })).toBeVisible();
    expect(
      screen.queryByRole('link', { name: 'Audit Logs (Pro)' })
    ).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Users' })).toBeVisible();
    expect(
      screen.queryByRole('link', { name: 'Users (Pro)' })
    ).not.toBeInTheDocument();
  });
});
