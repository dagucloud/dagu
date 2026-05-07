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
import { ErrorModalProvider } from '@/components/ui/error-modal';
import { ToastProvider } from '@/components/ui/simple-toast';
import { AppBarContext } from './contexts/AppBarContext';
import { AuthProvider, hasRole, useAuth } from './contexts/AuthContext';
import {
  Config,
  ConfigContext,
  ConfigUpdateContext,
} from './contexts/ConfigContext';
import { useHasFeature } from './hooks/useLicense';
import { PageContextProvider } from './contexts/PageContext';
import { SchemaProvider } from './contexts/SchemaContext';
import { SearchStateProvider } from './contexts/SearchStateContext';
import {
  UserPreferencesProvider,
  useUserPreferences,
} from './contexts/UserPreference';
import { AgentChatModal, AgentChatProvider } from './features/agent';
import Layout from './layouts/Layout';
import fetchJson from './lib/fetchJson';
import { fetchWithTimeout, shouldRetryQueryError } from './lib/requestTimeout';
import { useClient } from './hooks/api';
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
import AgentMemoryPage from './pages/agent-memory';
import AgentPage from './pages/agent';
import AgentSettingsPage from './pages/agent-settings';
import AgentSoulsPage from './pages/agent-souls';
import SoulEditorPage from './pages/agent-souls/SoulEditorPage';
import AgentToolsPage from './pages/agent-tools';
import AdministrationPage from './pages/administration';
import APIKeysPage from './pages/api-keys';
import APIDocsPage from './pages/api-docs';
import AuditLogsPage from './pages/audit-logs';
import BaseConfigPage from './pages/base-config';
import DAGRuns from './pages/dag-runs';
import DAGRunDetails from './pages/dag-runs/dag-run';
import DAGs from './pages/dags';
import DAGDetails from './pages/dags/dag';
import WorkflowDesignPage from './pages/design';
import DocsPage from './pages/docs';
import EventLogsPage from './pages/event-logs';
import GitSyncPage from './pages/git-sync';
import HomePage from './pages/home';
import IntegrationsPage from './pages/integrations';
import LicensePage from './pages/license';
import LoginPage from './pages/login';
import OverviewPage from './pages/overview';
import Queues from './pages/queues';
import QueueDetailsPage from './pages/queues/queue';
import Search from './pages/search';
import SetupPage from './pages/setup';
import SystemStatus from './pages/system-status';
import TerminalPage from './pages/terminal';
import RemoteNodesPage from './pages/remote-nodes';
import UsersPage from './pages/users';
import WebhooksPage from './pages/webhooks';

type Props = {
  config: Config;
};

const REMOTE_NODE_STORAGE_KEY = 'dagu-selected-remote-node';
const WORKSPACE_SENSITIVE_TARGET_PATH_PREFIXES = [
  '/dags/{fileName}',
  '/dag-runs/{name}/{dagRunId}',
] as const;

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

function WriteElement({
  children,
}: {
  children: React.ReactElement;
}): React.ReactElement {
  const { user } = useAuth();
  const config = React.useContext(ConfigContext);
  const canWrite =
    config.authMode !== 'builtin'
      ? config.permissions.writeDags
      : hasRole(user?.role ?? UserRole.viewer, UserRole.developer);
  if (!canWrite || !config.agentEnabled) {
    return <Navigate to="/" replace />;
  }
  return children;
}

function AgentChatModalHost({
  enabled,
}: {
  enabled: boolean;
}): React.ReactElement | null {
  const location = useLocation();
  const isDesignWorkspace =
    location.pathname === '/design' || location.pathname.startsWith('/design/');
  if (!enabled || isDesignWorkspace) {
    return null;
  }
  return <AgentChatModal />;
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
  const selectedWorkspaceName = workspaceNameForSelection(workspaceSelection);
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
  }, [applyWorkspaces, client, selectedRemoteNode]);

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
    const fetchRemoteNodeNames = async () => {
      try {
        const token = localStorage.getItem('dagu_auth_token');
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
  }, [config.apiURL]);

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
            <AuthProvider>
              <SearchStateProvider>
                <SchemaProvider>
                  <ErrorModalProvider>
                    <ToastProvider>
                      <BrowserRouter basename={config.basePath}>
                        <Routes>
                          <Route path="/login" element={<LoginPage />} />
                          <Route path="/setup" element={<SetupPage />} />
                          <Route
                            path="/*"
                            element={
                              <ProtectedRoute>
                                <AgentChatProvider>
                                  <PageContextProvider>
                                    <Layout navbarColor={config.navbarColor}>
                                      <Routes>
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
                                          path="/design"
                                          element={
                                            <WriteElement>
                                              <WorkflowDesignPage />
                                            </WriteElement>
                                          }
                                        />
                                        <Route
                                          path="/search/"
                                          element={<Search />}
                                        />
                                        <Route
                                          path="/docs/*"
                                          element={<DocsPage />}
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
                                            <AdminElement>
                                              <GitSyncPage />
                                            </AdminElement>
                                          }
                                        />
                                        <Route
                                          path="/agent"
                                          element={
                                            <AdminElement>
                                              <AgentPage />
                                            </AdminElement>
                                          }
                                        />
                                        <Route
                                          path="/agent-settings"
                                          element={
                                            <AdminElement>
                                              <AgentSettingsPage />
                                            </AdminElement>
                                          }
                                        />
                                        <Route
                                          path="/agent-tools"
                                          element={
                                            <AdminElement>
                                              <AgentToolsPage />
                                            </AdminElement>
                                          }
                                        />
                                        <Route
                                          path="/agent-memory"
                                          element={
                                            <AdminElement>
                                              <AgentMemoryPage />
                                            </AdminElement>
                                          }
                                        />
                                        <Route
                                          path="/agent-souls"
                                          element={
                                            <AdminElement>
                                              <AgentSoulsPage />
                                            </AdminElement>
                                          }
                                        />
                                        <Route
                                          path="/agent-souls/new"
                                          element={
                                            <AdminElement>
                                              <SoulEditorPage />
                                            </AdminElement>
                                          }
                                        />
                                        <Route
                                          path="/agent-souls/:soulId"
                                          element={
                                            <AdminElement>
                                              <SoulEditorPage />
                                            </AdminElement>
                                          }
                                        />
                                      </Routes>
                                    </Layout>
                                    <AgentChatModalHost
                                      enabled={config.agentEnabled}
                                    />
                                  </PageContextProvider>
                                </AgentChatProvider>
                              </ProtectedRoute>
                            }
                          />
                        </Routes>
                      </BrowserRouter>
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
