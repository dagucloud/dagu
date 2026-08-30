// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import {
  BrowserRouter,
  Link,
  Navigate,
  Route,
  Routes,
  useLocation,
} from 'react-router-dom';
import { SWRConfig, mutate as globalMutate } from 'swr';

import { Shield } from 'lucide-react';

import { ProtectedRoute } from './components/ProtectedRoute';
import { QueryFeedback } from './components/QueryFeedback';
import { ErrorModalProvider } from '@/components/ui/error-modal';
import { ToastProvider } from '@/components/ui/simple-toast';
import { AppBarContext } from './contexts/AppBarContext';
import { AuthProvider, useCanAccessGitSync } from './contexts/AuthContext';
import {
  Config,
  ConfigContext,
  ConfigUpdateContext,
  useUpdateConfig,
} from './contexts/ConfigContext';
import { useHasFeature, useLicense } from './hooks/useLicense';
import { SchemaProvider } from './contexts/SchemaContext';
import { SearchStateProvider } from './contexts/SearchStateContext';
import {
  UserPreferencesProvider,
  useUserPreferences,
} from './contexts/UserPreference';
import Layout from './layouts/Layout';
import fetchJson from './lib/fetchJson';
import { fetchWithTimeout, shouldRetryQueryError } from './lib/requestTimeout';
import { useClient, useQuery } from './hooks/api';
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

const AdministrationPage = React.lazy(() => import('./pages/administration'));
const APIKeysPage = React.lazy(() => import('./pages/api-keys'));
const APIDocsPage = React.lazy(() => import('./pages/api-docs'));
const AuditLogsPage = React.lazy(() => import('./pages/audit-logs'));
const BaseConfigPage = React.lazy(() => import('./pages/base-config'));
const DAGRuns = React.lazy(() => import('./pages/dag-runs'));
const DAGRunDetails = React.lazy(() => import('./pages/dag-runs/dag-run'));
const DAGs = React.lazy(() => import('./pages/dags'));
const DAGDetails = React.lazy(() => import('./pages/dags/dag'));
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
  feature: string;
  children: React.ReactElement;
}): React.ReactElement {
  const hasFeature = useHasFeature(feature);
  if (hasFeature) return children;
  return <LicenseRequiredMessage />;
}

function ActiveLicenseDeveloperElement({
  children,
}: {
  children: React.ReactElement;
}): React.ReactElement {
  const license = useLicense();
  const licensed = !license.community && (license.valid || license.gracePeriod);
  return (
    <DeveloperElement>
      {licensed ? children : <LicenseRequiredMessage />}
    </DeveloperElement>
  );
}

function LicenseRequiredMessage(): React.ReactElement {
  return (
    <div className="flex flex-col items-center justify-center h-full gap-4 text-center p-8">
      <Shield size={48} className="text-muted-foreground" />
      <h2 className="text-xl font-semibold">License Required</h2>
      <p className="text-sm text-muted-foreground max-w-md">
        This feature requires an active Dagu license or trial. Visit the{' '}
        <Link
          to="/license"
          className="text-primary underline underline-offset-2"
        >
          License
        </Link>{' '}
        page to activate your license.
      </p>
    </div>
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
          <h2 className="text-xl font-semibold">Unable to load this page</h2>
          <p className="max-w-md text-sm text-muted-foreground">
            The page may have changed since this tab was opened. Reload to use
            the latest version.
          </p>
          <button
            type="button"
            className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground"
            onClick={() => window.location.reload()}
          >
            Reload
          </button>
        </div>
      );
    }

    return this.props.children;
  }
}

function LazyRoutes({
  children,
}: {
  children: React.ReactNode;
}): React.ReactElement {
  return (
    <LazyRouteErrorBoundary>
      <React.Suspense fallback={<LoadingIndicator />}>
        <Routes>{children}</Routes>
      </React.Suspense>
    </LazyRouteErrorBoundary>
  );
}

function LicenseStatusSync({
  enabled,
  remoteNode,
}: {
  enabled: boolean;
  remoteNode: string;
}): null {
  const updateConfig = useUpdateConfig();
  const { data } = useQuery(
    '/license/status',
    enabled ? { params: { query: { remoteNode } } } : null,
    {
      keepPreviousData: true,
      refreshInterval: 60_000,
      revalidateOnFocus: true,
      revalidateOnReconnect: true,
      shouldRetryOnError: false,
    }
  );

  React.useEffect(() => {
    if (data) {
      updateConfig({ license: data });
    }
  }, [data, updateConfig]);

  return null;
}

function AppInner({ config: initialConfig }: Props): React.ReactElement {
  const client = useClient();
  const [config, setConfig] = React.useState(initialConfig);
  const initialWorkspacesRef = React.useRef(initialConfig.initialWorkspaces);
  const updateConfig = React.useCallback((patch: Partial<Config>) => {
    setConfig((prev) => ({ ...prev, ...patch }));
  }, []);

  const [title, setTitle] = React.useState<string>('');
  const { preferences } = useUserPreferences();
  const theme = preferences.theme || 'dark';

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

      // Clear SWR cache on node switch. Active hooks refetch automatically
      // since their keys include remoteNode.
      globalMutate(() => true, undefined, { revalidate: false });
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
    document.documentElement.classList.toggle('dark', theme === 'dark');
    document.documentElement.style.backgroundColor = 'var(--background)';
  }, [theme]);

  React.useEffect(() => {
    const base = config.title || 'Dagu';
    document.title = title ? `${title} - ${base}` : base;
  }, [title, config.title]);

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
          <ConfigUpdateContext.Provider value={updateConfig}>
            <LicenseStatusSync
              enabled={canFetchAuthenticatedResources}
              remoteNode={selectedRemoteNode}
            />
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
                                      <Route path="/dags/" element={<DAGs />} />
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
                                        path="/dag-runs/:name/:dagRunId"
                                        element={<DAGRunDetails />}
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
                                            <LicensePage />
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
                                    </LazyRoutes>
                                  </Layout>
                                </ProtectedRoute>
                              }
                            />
                          </Routes>
                        </BrowserRouter>
                      </QueryFeedback>
                    </ToastProvider>
                  </ErrorModalProvider>
                </SchemaProvider>
              </SearchStateProvider>
            </AuthProvider>
          </ConfigUpdateContext.Provider>
        </ConfigContext.Provider>
      </AppBarContext.Provider>
    </SWRConfig>
  );
}

function App({ config }: Props): React.ReactElement {
  return (
    <UserPreferencesProvider>
      <AppInner config={config} />
    </UserPreferencesProvider>
  );
}

export default App;
