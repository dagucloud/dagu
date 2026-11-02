// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronDown, ChevronUp, Maximize2, X } from 'lucide-react';
import React from 'react';
import { createPortal } from 'react-dom';
import {
  BrowserRouter,
  Navigate,
  Route,
  Routes,
  useLocation,
} from 'react-router-dom';
import { SWRConfig, mutate as globalMutate } from 'swr';

import { LicenseFeaturePrompt } from '@/components/LicenseFeaturePrompt';
import { hasActiveLicense, type LicensedFeature } from '@/lib/license';

import { ProtectedRoute } from './components/ProtectedRoute';
import { QueryFeedback } from './components/QueryFeedback';
import { ErrorModalProvider } from '@/components/ui/error-modal';
import { ToastProvider } from '@/components/ui/simple-toast';
import { WorkspaceLoading } from '@/features/workflow-workspace/WorkspaceLoading';
import RunProgressStack from '@/features/dags/components/dag-execution/RunProgressStack';
import LegacyRunRedirect from './pages/dag-runs/LegacyRunRedirect';
import { AppBarContext } from './contexts/AppBarContext';
import { AuthProvider, useCanAccessGitSync } from './contexts/AuthContext';
import { Config, ConfigContext } from './contexts/ConfigContext';
import { useHasFeature, useLicense } from './hooks/useLicense';
import { SchemaProvider } from './contexts/SchemaContext';
import { SearchStateProvider } from './contexts/SearchStateContext';
import {
  UserPreferencesProvider,
  useResolvedTheme,
} from './contexts/UserPreference';
import Layout from './layouts/Layout';
import fetchJson from './lib/fetchJson';
import { fetchWithTimeout, shouldRetryQueryError } from './lib/requestTimeout';
import { useClient } from './hooks/api';
import { LicenseProvider } from './components/LicenseProvider';
import { addAuthSessionListener, getAuthToken } from './lib/authSession';
import {
  getStoredWorkspaceSelection,
  persistWorkspaceSelection,
  sanitizeWorkspaceName,
  sanitizeWorkspaceSelection,
  WorkspaceKind,
  workspaceNameForSelection,
  type WorkspaceSelection,
} from './lib/workspace';
import { UserRole } from './api/v1/schema';
import LoginPage from './pages/login';
import SetupPage from './pages/setup';
import LoadingIndicator from '@/components/ui/loading-indicator';
import { I18nProvider, useI18n } from '@/i18n/I18nProvider';
import { IconButton } from '@/components/ui/icon-button';
import { LayoutChromeContext } from '@/contexts/LayoutChromeContext';
import {
  OverlayResizeHandle,
  useOverlayWidth,
} from '@/features/peek/OverlayResizeHandle';
import { useOpenPage } from '@/features/dag-runs/lib/useOpenPage';
import { overlayStepOfKey } from '@/features/peek/overlayKeys';
import {
  useWorkflowOverlay,
  WorkflowOverlayProvider,
} from '@/features/peek/WorkflowOverlay';
import { escFree } from '@/features/workflow-workspace/canvas/escFree';
import { translateStatic } from '@/i18n/staticMessages';
import { I18nText } from '@/i18n/I18nText';

const AdministrationPage = React.lazy(() => import('./pages/administration'));
const APIKeysPage = React.lazy(() => import('./pages/api-keys'));
const APIDocsPage = React.lazy(() => import('./pages/api-docs'));
const ArtifactsPage = React.lazy(() => import('./pages/artifacts'));
const AuditLogsPage = React.lazy(() => import('./pages/audit-logs'));
const BaseConfigPage = React.lazy(() => import('./pages/base-config'));
const CalendarsPage = React.lazy(() => import('./pages/calendars'));
const CalendarPage = React.lazy(() => import('./pages/calendars/calendar'));
const DAGRuns = React.lazy(() => import('./pages/dag-runs'));
const DAGs = React.lazy(() => import('./pages/dags'));
// The workflow page opens from most pages: its code preloads while the
// browser is idle (see preloadWhenIdle).
const loadWorkflowPage = () => import('./pages/dags/dag');
const DAGDetails = React.lazy(loadWorkflowPage);
const WikiPage = React.lazy(() => import('./pages/wiki'));
const EventLogsPage = React.lazy(() => import('./pages/event-logs'));
const GitSyncPage = React.lazy(() => import('./pages/git-sync'));
const HomePage = React.lazy(() => import('./pages/home'));
const IncidentPoliciesPage = React.lazy(
  () => import('./pages/incident-policies')
);
const IncidentProvidersPage = React.lazy(
  () => import('./pages/incident-providers')
);
const IncidentsPage = React.lazy(() => import('./pages/incidents'));
const IntegrationsPage = React.lazy(() => import('./pages/integrations'));
const LicensePage = React.lazy(() => import('./pages/license'));
const NotFoundPage = React.lazy(() => import('./pages/not-found'));
const NotificationChannelsPage = React.lazy(
  () => import('./pages/notification-channels')
);
const NotificationRulesPage = React.lazy(
  () => import('./pages/notification-rules')
);
const NotificationsPage = React.lazy(() => import('./pages/notifications'));
const OverviewPage = React.lazy(() => import('./pages/overview'));
const ViewPage = React.lazy(() => import('./pages/views'));
const ProfilesPage = React.lazy(() => import('./pages/profiles'));
const Queues = React.lazy(() => import('./pages/queues'));
const QueueDetailsPage = React.lazy(() => import('./pages/queues/queue'));
const Search = React.lazy(() => import('./pages/search'));
const SystemStatus = React.lazy(() => import('./pages/system-status'));
const TerminalPage = React.lazy(() => import('./pages/terminal'));
const RemoteNodesPage = React.lazy(() => import('./pages/remote-nodes'));
const UsersPage = React.lazy(() => import('./pages/users'));
const WebhooksPage = React.lazy(() => import('./pages/webhooks'));

type Props = {
  config: Config;
};

const REMOTE_NODE_STORAGE_KEY = 'dagu-selected-remote-node';
const STATIC_PAGE_TITLES = new Set([
  'API Docs',
  'API Keys',
  'Artifacts',
  'Audit Logs',
  'Base Config',
  'Calendars',
  'Cockpit',
  'Events',
  'Executions',
  'Git Sync',
  'Incident Connections',
  'Incident Routing',
  'Incidents',
  'License',
  'Notification Channels',
  'Notification Rules',
  'Notifications',
  'Profiles & Secrets',
  'Queue',
  'Queues',
  'Remote Nodes',
  'Search',
  'System Status',
  'Terminal',
  'Timeline',
  'User Management',
  'Webhooks',
  'Wiki',
  'Workers',
  'Workflows',
]);
const WORKSPACE_SENSITIVE_TARGET_PATH_PREFIXES = [
  '/dags/{fileName}',
  '/dag-runs/{name}/{dagRunId}',
] as const;

function LegacyWikiRouteRedirect() {
  const location = useLocation();
  const suffix = location.pathname.replace(/^\/docs/, '');
  return (
    <Navigate to={`/wiki${suffix}${location.search}${location.hash}`} replace />
  );
}

function isWorkspaceSensitiveTargetPath(path: unknown): boolean {
  return (
    typeof path === 'string' &&
    WORKSPACE_SENSITIVE_TARGET_PATH_PREFIXES.some((prefix) =>
      path.startsWith(prefix)
    )
  );
}

function isWorkspaceScopedSWRKey(key: unknown): boolean {
  if (!Array.isArray(key) || key.length < 3) {
    return false;
  }

  if (isWorkspaceSensitiveTargetPath(key[1])) {
    return true;
  }

  const init = key[2];
  if (!init || typeof init !== 'object') {
    return false;
  }

  const query = (init as { params?: { query?: Record<string, unknown> } })
    .params?.query;
  return !!query && Object.prototype.hasOwnProperty.call(query, 'workspace');
}

function parseRemoteNodes(remoteNodesConfig: string): string[] {
  const nodes = remoteNodesConfig
    .split(',')
    .filter(Boolean)
    .map((node) => node.trim());
  if (!nodes.includes('local')) {
    nodes.unshift('local');
  }
  return nodes;
}

function getStoredRemoteNode(validNodes: string[]): string {
  const storedNode = localStorage.getItem(REMOTE_NODE_STORAGE_KEY);
  if (storedNode && validNodes.includes(storedNode)) {
    return storedNode;
  }
  return 'local';
}

// Helper to wrap admin-only elements
function AdminElement({
  children,
}: {
  children: React.ReactElement;
}): React.ReactElement {
  return (
    <ProtectedRoute requiredRole={UserRole.admin}>{children}</ProtectedRoute>
  );
}

function GitSyncElement({
  children,
}: {
  children: React.ReactElement;
}): React.ReactElement {
  const canAccess = useCanAccessGitSync();
  if (!canAccess) return <Navigate to="/" replace />;
  return children;
}

function ManagerElement({
  children,
}: {
  children: React.ReactElement;
}): React.ReactElement {
  return (
    <ProtectedRoute requiredRole={UserRole.manager}>{children}</ProtectedRoute>
  );
}

function DeveloperElement({
  children,
}: {
  children: React.ReactElement;
}): React.ReactElement {
  return (
    <ProtectedRoute requiredRole={UserRole.developer}>
      {children}
    </ProtectedRoute>
  );
}

function LicensedRoute({
  feature,
  children,
}: {
  feature: LicensedFeature;
  children: React.ReactElement;
}): React.ReactElement {
  const hasFeature = useHasFeature(feature);
  if (hasFeature) return children;
  return <LicenseFeaturePrompt feature={feature} headingLevel={1} />;
}

function ActiveLicenseDeveloperElement({
  children,
}: {
  children: React.ReactElement;
}): React.ReactElement {
  const license = useLicense();
  const licensed = hasActiveLicense(license);
  return (
    <DeveloperElement>
      {licensed ? (
        children
      ) : (
        <LicenseFeaturePrompt feature="incidents" headingLevel={1} />
      )}
    </DeveloperElement>
  );
}

type LazyRouteErrorBoundaryProps = {
  children: React.ReactNode;
};

type LazyRouteErrorBoundaryState = {
  hasError: boolean;
};

class LazyRouteErrorBoundary extends React.Component<
  LazyRouteErrorBoundaryProps,
  LazyRouteErrorBoundaryState
> {
  state: LazyRouteErrorBoundaryState = { hasError: false };

  static getDerivedStateFromError(): LazyRouteErrorBoundaryState {
    return { hasError: true };
  }

  render(): React.ReactNode {
    if (this.state.hasError) {
      return (
        <div
          role="alert"
          className="flex h-full flex-col items-center justify-center gap-4 p-8 text-center"
        >
          <h2 className="text-xl font-semibold">
            <I18nText text={'Unable to load this page'} />
          </h2>
          <p className="max-w-md text-sm text-muted-foreground">
            <I18nText
              text={
                'The page may have changed since this tab was opened. Reload to use the latest version.'
              }
            />
          </p>
          <button
            type="button"
            className="rounded-md bg-primary-fill px-4 py-2 text-sm font-medium text-primary-foreground"
            onClick={() => window.location.reload()}
          >
            <I18nText text={'Reload'} />
          </button>
        </div>
      );
    }

    return this.props.children;
  }
}

/**
 * The fallback while a page chunk loads. A workflow page gets its own loading
 * frame, so that the bar is in place from the first paint.
 */
function RouteFallback(): React.ReactElement {
  const { pathname } = useLocation();
  const workflow = /^\/dags\/([^/]+)/.exec(pathname);
  if (workflow) {
    let name = workflow[1]!;
    try {
      name = decodeURIComponent(name);
    } catch {
      // A name that is not valid text stays as it is.
    }
    return <WorkspaceLoading fileName={name} />;
  }
  return <LoadingIndicator />;
}

// How long the preload waits for an idle moment before it runs anyway.
const IDLE_PRELOAD_TIMEOUT_MS = 5000;

/**
 * Loads the code of the workflow page and of its canvas once the browser is
 * idle, so that opening a workflow does not wait for it. Returns a cancel
 * function.
 */
function preloadWhenIdle(): () => void {
  const preload = () => {
    void loadWorkflowPage()
      .then((page) => page.preloadWorkflowCanvas())
      .catch(() => {
        // A failed preload is retried by the page when it opens.
      });
  };
  if (typeof window.requestIdleCallback === 'function') {
    const id = window.requestIdleCallback(preload, {
      timeout: IDLE_PRELOAD_TIMEOUT_MS,
    });
    return () => window.cancelIdleCallback(id);
  }
  const id = window.setTimeout(preload, IDLE_PRELOAD_TIMEOUT_MS);
  return () => window.clearTimeout(id);
}

function LazyRoutes({
  children,
}: {
  children: React.ReactNode;
}): React.ReactElement {
  // While a workflow page shows over the page, the page keeps its location
  // and takes no input. The routes always get a location: with one, they
  // render under a provider of their own, and a switch between none and one
  // would mount the page again and lose its state.
  const overlay = useWorkflowOverlay();
  const location = useLocation();
  return (
    <LazyRouteErrorBoundary>
      <React.Suspense fallback={<RouteFallback />}>
        <div className="contents" inert={overlay?.isOpen || undefined}>
          <Routes location={overlay?.background ?? location}>{children}</Routes>
        </div>
      </React.Suspense>
    </LazyRouteErrorBoundary>
  );
}

/**
 * The workflow page over the page that opened it, as a sheet from the
 * right. The backdrop, the close button and an Esc that the workflow page
 * leaves go back to the page below; the expand button keeps the workflow
 * page as the page.
 */
function WorkflowOverlayHost(): React.ReactElement | null {
  const overlay = useWorkflowOverlay();
  const { ts } = useI18n();
  const location = useLocation();
  const openPage = useOpenPage();
  const panelRef = React.useRef<HTMLElement>(null);
  const overlayWidth = useOverlayWidth();
  const isOpen = overlay?.isOpen ?? false;
  const close = overlay?.close;
  const step = overlay?.step;
  React.useEffect(() => {
    if (!isOpen || !close || !step) return;
    // The window hears a key after the handlers of the workflow page. Up and
    // Down step through the rows of the page below, as the list does.
    const onKeyDown = (event: KeyboardEvent) => {
      if (escFree(event)) {
        close();
        return;
      }
      const delta = overlayStepOfKey(event);
      if (delta === null) return;
      event.preventDefault();
      step(delta);
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [isOpen, close, step]);
  if (!overlay?.isOpen) return null;
  return createPortal(
    <div className="fixed inset-0 z-40">
      <div
        aria-hidden="true"
        className="absolute inset-0 bg-black/60"
        onClick={overlay.close}
      />
      {/* A panel inset from the window, so that the dimmed page below shows
          around it: a layer over the page, not a new page. */}
      <section
        role="dialog"
        aria-modal="true"
        aria-label={ts('Workflow')}
        ref={panelRef}
        // The width that the user set applies from md: a narrow window shows
        // the panel at full width.
        style={
          overlayWidth.width === null
            ? undefined
            : ({
                '--overlay-width': `${overlayWidth.width}px`,
              } as React.CSSProperties)
        }
        className="absolute inset-0 flex min-w-0 flex-col bg-background shadow-2xl md:inset-y-3 md:left-auto md:right-3 md:w-[var(--overlay-width,min(76rem,calc(100vw-18rem)))] md:max-w-[calc(100vw-6rem)] md:rounded-xl md:border md:border-border"
      >
        <OverlayResizeHandle
          width={() => panelRef.current?.getBoundingClientRect().width ?? 0}
          onResize={overlayWidth.set}
          onReset={overlayWidth.reset}
        />
        <div className="flex h-8 shrink-0 items-center justify-end gap-1 border-b border-border px-2">
          <IconButton
            icon={ChevronUp}
            size="sm"
            label={ts('Previous')}
            shortcut="↑"
            disabled={!overlay.canStep(-1)}
            onClick={() => overlay.step(-1)}
          />
          <IconButton
            icon={ChevronDown}
            size="sm"
            label={ts('Next')}
            shortcut="↓"
            disabled={!overlay.canStep(1)}
            onClick={() => overlay.step(1)}
          />
          <span className="mx-0.5 h-4 w-px bg-border" aria-hidden="true" />
          <IconButton
            icon={Maximize2}
            size="sm"
            label={ts('Open workflow')}
            description={ts('Cmd/Ctrl+Click for new tab')}
            onClick={(event) => {
              // The URL is the workflow page: a new tab opens it alone.
              if (event.metaKey || event.ctrlKey) {
                openPage(`${location.pathname}${location.search}`, event);
                return;
              }
              overlay.expand();
            }}
          />
          <IconButton
            icon={X}
            size="sm"
            label={ts('Close')}
            onClick={overlay.close}
          />
        </div>
        <div className="relative flex min-h-0 flex-1 flex-col overflow-hidden md:rounded-b-xl">
          {/* The frame of the page below stays as it is. */}
          <LayoutChromeContext.Provider value={null}>
            <LazyRouteErrorBoundary>
              <React.Suspense fallback={<RouteFallback />}>
                <Routes>
                  <Route path="/dags/:fileName/:tab" element={<DAGDetails />} />
                  <Route path="/dags/:fileName/" element={<DAGDetails />} />
                  {/* From the click until the URL names the workflow. */}
                  <Route
                    path="*"
                    element={
                      <WorkspaceLoading fileName={overlay.target?.dag ?? ''} />
                    }
                  />
                </Routes>
              </React.Suspense>
            </LazyRouteErrorBoundary>
          </LayoutChromeContext.Provider>
        </div>
      </section>
    </div>,
    document.body
  );
}

function AppInner({ config: initialConfig }: Props): React.ReactElement {
  const client = useClient();
  const { locale } = useI18n();
  const [config, setConfig] = React.useState(initialConfig);
  const initialWorkspacesRef = React.useRef(initialConfig.initialWorkspaces);
  const updateConfig = React.useCallback((patch: Partial<Config>) => {
    setConfig((prev) => ({ ...prev, ...patch }));
  }, []);

  const [title, setTitle] = React.useState<string>('');
  const theme = useResolvedTheme();

  const [remoteNodes, setRemoteNodes] = React.useState<string[]>(() =>
    parseRemoteNodes(config.remoteNodes)
  );

  const [selectedRemoteNode, setSelectedRemoteNode] = React.useState<string>(
    () => getStoredRemoteNode(remoteNodes)
  );
  const [workspaces, setWorkspaces] = React.useState(
    () => config.initialWorkspaces ?? []
  );
  const [workspacesLoaded, setWorkspacesLoaded] = React.useState(false);
  const [workspaceError, setWorkspaceError] = React.useState<Error | null>(
    null
  );
  const [workspaceSelection, setWorkspaceSelection] =
    React.useState<WorkspaceSelection>(() => getStoredWorkspaceSelection());
  const [authToken, setAuthToken] = React.useState<string | null>(() =>
    getAuthToken()
  );
  const selectedWorkspaceName = workspaceNameForSelection(workspaceSelection);
  const canFetchAuthenticatedResources =
    config.authMode !== 'builtin' || authToken !== null;
  const handleSelectWorkspace = React.useCallback(
    (selection: WorkspaceSelection) => {
      const sanitized = sanitizeWorkspaceSelection(selection);
      setWorkspaceSelection(sanitized);
      persistWorkspaceSelection(sanitized);

      // Revalidate active workspace-scoped queries without blanking unrelated
      // cache entries, such as system status or worker lists.
      void globalMutate(isWorkspaceScopedSWRKey);
    },
    []
  );
  const workspaceFetchSeqRef = React.useRef(0);

  const applyWorkspaces = React.useCallback(
    (next: Config['initialWorkspaces']) => {
      const sorted = [...next].sort((a, b) => a.name.localeCompare(b.name));
      setWorkspaces(sorted);
      updateConfig({ initialWorkspaces: sorted });
    },
    [updateConfig]
  );

  const handleSelectRemoteNode = React.useCallback(
    (node: string) => {
      const validNode = remoteNodes.includes(node) ? node : 'local';
      setSelectedRemoteNode(validNode);
      localStorage.setItem(REMOTE_NODE_STORAGE_KEY, validNode);

      // Retain node-specific license status while other resource queries refresh.
      globalMutate(
        (key) => !Array.isArray(key) || key[1] !== '/license/status',
        undefined,
        { revalidate: false }
      );
      setWorkspacesLoaded(false);
    },
    [remoteNodes]
  );

  const fetchWorkspaces = React.useCallback(async () => {
    if (!canFetchAuthenticatedResources) {
      setWorkspaceError(null);
      applyWorkspaces(initialWorkspacesRef.current ?? []);
      setWorkspacesLoaded(true);
      return;
    }

    const requestSeq = workspaceFetchSeqRef.current + 1;
    workspaceFetchSeqRef.current = requestSeq;
    setWorkspaceError(null);
    try {
      const response = await client.GET('/workspaces', {
        params: { query: { remoteNode: selectedRemoteNode } },
      });
      if (workspaceFetchSeqRef.current !== requestSeq) {
        return;
      }
      if (response.error) {
        throw new Error(response.error.message || 'Failed to load workspaces');
      }
      applyWorkspaces(response.data?.workspaces || []);
    } catch (error) {
      if (workspaceFetchSeqRef.current !== requestSeq) {
        return;
      }
      const nextError =
        error instanceof Error ? error : new Error('Failed to load workspaces');
      setWorkspaceError(nextError);
      if (selectedRemoteNode === 'local') {
        applyWorkspaces(initialWorkspacesRef.current ?? []);
      }
    } finally {
      if (workspaceFetchSeqRef.current === requestSeq) {
        setWorkspacesLoaded(true);
      }
    }
  }, [
    applyWorkspaces,
    canFetchAuthenticatedResources,
    client,
    selectedRemoteNode,
  ]);

  const handleCreateWorkspace = React.useCallback(
    async (name: string) => {
      const sanitized = sanitizeWorkspaceName(name);
      if (!sanitized) return;
      setWorkspaceError(null);
      const response = await client.POST('/workspaces', {
        params: { query: { remoteNode: selectedRemoteNode } },
        body: { name: sanitized },
      });
      if (response.error || !response.data) {
        const nextError = new Error(
          response.error?.message || 'Failed to create workspace'
        );
        setWorkspaceError(nextError);
        throw nextError;
      }
      applyWorkspaces([
        ...workspaces.filter((workspace) => workspace.id !== response.data.id),
        response.data,
      ]);
      handleSelectWorkspace({
        kind: WorkspaceKind.workspace,
        workspace: response.data.name,
      });
    },
    [
      applyWorkspaces,
      client,
      handleSelectWorkspace,
      selectedRemoteNode,
      workspaces,
    ]
  );

  const handleDeleteWorkspace = React.useCallback(
    async (id: string) => {
      setWorkspaceError(null);
      const response = await client.DELETE('/workspaces/{workspaceId}', {
        params: {
          path: { workspaceId: id },
          query: { remoteNode: selectedRemoteNode },
        },
      });
      if (response.error) {
        const nextError = new Error(
          response.error.message || 'Failed to delete workspace'
        );
        setWorkspaceError(nextError);
        throw nextError;
      }
      applyWorkspaces(workspaces.filter((workspace) => workspace.id !== id));
      const deletedSelected = workspaces.some(
        (workspace) =>
          workspace.id === id && workspace.name === selectedWorkspaceName
      );
      if (deletedSelected) {
        handleSelectWorkspace({ kind: WorkspaceKind.all });
      }
    },
    [
      applyWorkspaces,
      client,
      handleSelectWorkspace,
      selectedRemoteNode,
      selectedWorkspaceName,
      workspaces,
    ]
  );

  // Fetch remote node names from the API on mount so the dropdown
  // includes store-sourced nodes (not just config-sourced ones from the template).
  React.useEffect(() => {
    if (!canFetchAuthenticatedResources) {
      return;
    }

    const fetchRemoteNodeNames = async () => {
      try {
        const token = getAuthToken();
        const headers: Record<string, string> = { Accept: 'application/json' };
        if (token) {
          headers['Authorization'] = `Bearer ${token}`;
        }
        const response = await fetchWithTimeout(
          `${config.apiURL}/remote-nodes?remoteNode=local`,
          { headers }
        );
        if (!response.ok) return;
        const data = await response.json();
        const nodes: { name: string }[] = data.remoteNodes || [];
        if (nodes.length > 0) {
          const names = [
            'local',
            ...nodes.map((n: { name: string }) => n.name),
          ];
          setRemoteNodes([...new Set(names)]);
        }
      } catch {
        // Silently fall back to template-provided nodes
      }
    };
    fetchRemoteNodeNames();
  }, [canFetchAuthenticatedResources, config.apiURL]);

  React.useEffect(() => {
    return addAuthSessionListener((change) => {
      setAuthToken(change.token);
    });
  }, []);

  React.useEffect(() => {
    if (!remoteNodes.includes(selectedRemoteNode)) {
      handleSelectRemoteNode('local');
    }
  }, [remoteNodes, selectedRemoteNode, handleSelectRemoteNode]);

  React.useEffect(() => {
    void fetchWorkspaces();
  }, [fetchWorkspaces]);

  React.useEffect(() => {
    if (
      workspacesLoaded &&
      workspaceSelection.kind === WorkspaceKind.workspace &&
      !workspaces.some((workspace) => workspace.name === selectedWorkspaceName)
    ) {
      handleSelectWorkspace({ kind: WorkspaceKind.all });
    }
  }, [
    handleSelectWorkspace,
    selectedWorkspaceName,
    workspaceSelection.kind,
    workspaces,
    workspacesLoaded,
  ]);

  React.useEffect(() => {
    if (!canFetchAuthenticatedResources) return;
    return preloadWhenIdle();
  }, [canFetchAuthenticatedResources]);

  React.useEffect(() => {
    document.documentElement.classList.toggle('dark', theme === 'dark');
    document.documentElement.style.colorScheme = theme;
    document.documentElement.style.backgroundColor = 'var(--background)';
  }, [theme]);

  React.useEffect(() => {
    const base = config.title || 'Dagu';
    const localizedTitle = STATIC_PAGE_TITLES.has(title)
      ? translateStatic(locale, title)
      : title;
    document.title = localizedTitle ? `${localizedTitle} - ${base}` : base;
  }, [title, config.title, locale]);

  return (
    <SWRConfig
      value={{
        fetcher: fetchJson,
        onError: console.error,
        shouldRetryOnError: shouldRetryQueryError,
        revalidateOnFocus: false,
        revalidateOnReconnect: false,
      }}
    >
      <AppBarContext.Provider
        value={{
          title,
          setTitle,
          remoteNodes,
          setRemoteNodes,
          selectedRemoteNode,
          selectRemoteNode: handleSelectRemoteNode,
          workspaces,
          workspaceError,
          workspaceSelection,
          selectWorkspace: handleSelectWorkspace,
          createWorkspace: handleCreateWorkspace,
          deleteWorkspace: handleDeleteWorkspace,
        }}
      >
        <ConfigContext.Provider value={config}>
          <LicenseProvider
            enabled={canFetchAuthenticatedResources}
            remoteNode={selectedRemoteNode}
            initialLicense={initialConfig.license}
          >
            <AuthProvider>
              <SearchStateProvider>
                <SchemaProvider>
                  <ErrorModalProvider>
                    <ToastProvider>
                      <QueryFeedback>
                        <BrowserRouter basename={config.basePath}>
                          <Routes>
                            <Route path="/login" element={<LoginPage />} />
                            <Route path="/setup" element={<SetupPage />} />
                            <Route
                              path="/*"
                              element={
                                <ProtectedRoute>
                                  <WorkflowOverlayProvider>
                                    <Layout navbarColor={config.navbarColor}>
                                      <LazyRoutes>
                                        <Route
                                          path="/"
                                          element={<OverviewPage />}
                                        />
                                        <Route
                                          path="/dashboard"
                                          element={
                                            <OverviewPage initialTab="timeline" />
                                          }
                                        />
                                        <Route
                                          path="/cockpit"
                                          element={
                                            <OverviewPage initialTab="cockpit" />
                                          }
                                        />
                                        <Route
                                          path="/views/:viewId"
                                          element={<ViewPage />}
                                        />
                                        <Route
                                          path="/home"
                                          element={<HomePage />}
                                        />
                                        <Route
                                          path="/api-docs"
                                          element={<APIDocsPage />}
                                        />
                                        <Route
                                          path="/integrations"
                                          element={<IntegrationsPage />}
                                        />
                                        <Route
                                          path="/notifications"
                                          element={
                                            <DeveloperElement>
                                              <NotificationsPage />
                                            </DeveloperElement>
                                          }
                                        />
                                        <Route
                                          path="/notification-rules"
                                          element={
                                            <DeveloperElement>
                                              <NotificationRulesPage />
                                            </DeveloperElement>
                                          }
                                        />
                                        <Route
                                          path="/notification-channels"
                                          element={
                                            <DeveloperElement>
                                              <NotificationChannelsPage />
                                            </DeveloperElement>
                                          }
                                        />
                                        <Route
                                          path="/incidents"
                                          element={
                                            <ActiveLicenseDeveloperElement>
                                              <IncidentsPage />
                                            </ActiveLicenseDeveloperElement>
                                          }
                                        />
                                        <Route
                                          path="/incident-providers"
                                          element={
                                            <ActiveLicenseDeveloperElement>
                                              <IncidentProvidersPage />
                                            </ActiveLicenseDeveloperElement>
                                          }
                                        />
                                        <Route
                                          path="/incident-policies"
                                          element={
                                            <ActiveLicenseDeveloperElement>
                                              <IncidentPoliciesPage />
                                            </ActiveLicenseDeveloperElement>
                                          }
                                        />
                                        <Route
                                          path="/dags/"
                                          element={<DAGs />}
                                        />
                                        <Route
                                          path="/dags/:fileName/:tab"
                                          element={<DAGDetails />}
                                        />
                                        <Route
                                          path="/dags/:fileName/"
                                          element={<DAGDetails />}
                                        />
                                        <Route
                                          path="/search/"
                                          element={<Search />}
                                        />
                                        <Route
                                          path="/wiki/*"
                                          element={<WikiPage />}
                                        />
                                        <Route
                                          path="/docs/*"
                                          element={<LegacyWikiRouteRedirect />}
                                        />
                                        <Route
                                          path="/queues"
                                          element={<Queues />}
                                        />
                                        <Route
                                          path="/queues/:name"
                                          element={<QueueDetailsPage />}
                                        />
                                        <Route
                                          path="/dag-runs"
                                          element={<DAGRuns />}
                                        />
                                        <Route
                                          path="/artifacts"
                                          element={<ArtifactsPage />}
                                        />
                                        <Route
                                          path="/dag-runs/:name/:dagRunId"
                                          element={<LegacyRunRedirect />}
                                        />
                                        <Route
                                          path="/system-status"
                                          element={
                                            <DeveloperElement>
                                              <SystemStatus />
                                            </DeveloperElement>
                                          }
                                        />
                                        <Route
                                          path="/base-config"
                                          element={
                                            <DeveloperElement>
                                              <BaseConfigPage />
                                            </DeveloperElement>
                                          }
                                        />
                                        <Route
                                          path="/calendars"
                                          element={<CalendarsPage />}
                                        />
                                        <Route
                                          path="/calendars/:name"
                                          element={<CalendarPage />}
                                        />
                                        <Route
                                          path="/users"
                                          element={
                                            <AdminElement>
                                              <UsersPage />
                                            </AdminElement>
                                          }
                                        />
                                        <Route
                                          path="/administration"
                                          element={
                                            <AdminElement>
                                              <AdministrationPage />
                                            </AdminElement>
                                          }
                                        />
                                        <Route
                                          path="/remote-nodes"
                                          element={
                                            <AdminElement>
                                              <RemoteNodesPage />
                                            </AdminElement>
                                          }
                                        />
                                        <Route
                                          path="/api-keys"
                                          element={
                                            <AdminElement>
                                              <APIKeysPage />
                                            </AdminElement>
                                          }
                                        />
                                        <Route
                                          path="/webhooks"
                                          element={
                                            <DeveloperElement>
                                              <WebhooksPage />
                                            </DeveloperElement>
                                          }
                                        />
                                        <Route
                                          path="/secrets"
                                          element={
                                            <Navigate
                                              to="/profiles#secret-refs"
                                              replace
                                            />
                                          }
                                        />
                                        <Route
                                          path="/profiles"
                                          element={
                                            <ManagerElement>
                                              <ProfilesPage />
                                            </ManagerElement>
                                          }
                                        />
                                        <Route
                                          path="/terminal"
                                          element={
                                            <AdminElement>
                                              <TerminalPage />
                                            </AdminElement>
                                          }
                                        />
                                        <Route
                                          path="/event-logs"
                                          element={
                                            <ManagerElement>
                                              <EventLogsPage />
                                            </ManagerElement>
                                          }
                                        />
                                        <Route
                                          path="/audit-logs"
                                          element={
                                            <ManagerElement>
                                              <LicensedRoute feature="audit">
                                                <AuditLogsPage />
                                              </LicensedRoute>
                                            </ManagerElement>
                                          }
                                        />
                                        <Route
                                          path="/license"
                                          element={
                                            <AdminElement>
                                              <LicensePage
                                                key={selectedRemoteNode}
                                              />
                                            </AdminElement>
                                          }
                                        />
                                        <Route
                                          path="/git-sync"
                                          element={
                                            <GitSyncElement>
                                              <GitSyncPage />
                                            </GitSyncElement>
                                          }
                                        />
                                        <Route
                                          path="*"
                                          element={<NotFoundPage />}
                                        />
                                      </LazyRoutes>
                                      <WorkflowOverlayHost />
                                    </Layout>
                                  </WorkflowOverlayProvider>
                                </ProtectedRoute>
                              }
                            />
                          </Routes>
                          <RunProgressStack />
                        </BrowserRouter>
                      </QueryFeedback>
                    </ToastProvider>
                  </ErrorModalProvider>
                </SchemaProvider>
              </SearchStateProvider>
            </AuthProvider>
          </LicenseProvider>
        </ConfigContext.Provider>
      </AppBarContext.Provider>
    </SWRConfig>
  );
}

function App({ config }: Props): React.ReactElement {
  return (
    <UserPreferencesProvider>
      <I18nProvider>
        <AppInner config={config} />
      </I18nProvider>
    </UserPreferencesProvider>
  );
}

export default App;
