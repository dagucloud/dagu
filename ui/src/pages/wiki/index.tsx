// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronLeft } from 'lucide-react';
import React, {
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
} from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import {
  PathsWikiGetParametersQueryOrder,
  PathsWikiGetParametersQuerySort,
} from '@/api/v1/schema';
import SplitLayout from '@/components/SplitLayout';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useAuth, useCanWrite } from '@/contexts/AuthContext';
import {
  WikiPageTabProvider,
  useWikiPageTabContext,
} from '@/contexts/WikiPageTabContext';
import { UnsavedChangesProvider } from '@/contexts/UnsavedChangesContext';
import { useUserPreferences } from '@/contexts/UserPreference';
import { CockpitToolbar } from '@/features/cockpit/components/CockpitToolbar';
import { useCockpitState } from '@/features/cockpit/hooks/useCockpitState';
import { useClient, useQuery } from '@/hooks/api';
import { useIsMobile } from '@/hooks/useIsMobile';
import { useWikiTreeSSE } from '@/hooks/useWikiTreeSSE';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';
import {
  sanitizeWorkspaceName,
  sanitizeWorkspaceSelection,
  WorkspaceKind,
  workspaceTargetQueryForWorkspace,
  workspaceNameForSelection,
  workspaceSelectionKey,
  workspaceSelectionQuery,
  visibleWikiPagePathForWorkspace,
} from '@/lib/workspace';
import ConfirmModal from '@/components/ui/confirm-dialog';
import { CreateWikiPageModal } from './components/CreateWikiPageModal';
import WikiPageTabEditorPanel from './components/WikiPageTabEditorPanel';
import WikiTreeSidebar from './components/WikiTreeSidebar';
import { RenameWikiPageModal } from './components/RenameWikiPageModal';
import { WIKI_SSE_FALLBACK_INTERVAL_MS } from './lib/wiki-page-polling';
import { encodeWikiPagePathForURL } from './lib/wiki-page-path';
import { normalizeWikiPagePathFromURL } from './lib/wiki-page-url';
import type { WikiPageMutationTarget } from './lib/wiki-page-mutation';
import { useWikiPageMutations } from './hooks/useWikiPageMutations';
import type { ContextAction } from './components/WikiPageTreeNode';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { useI18n } from '@/i18n/I18nProvider';

function titleFromPath(wikiPagePath: string): string {
  const segments = wikiPagePath.split('/');
  return segments[segments.length - 1] || wikiPagePath;
}

function safeDecodeURIComponent(value: string): string | null {
  try {
    return decodeURIComponent(value);
  } catch {
    return null;
  }
}

function workspaceSearchForWikiPageTab(workspace?: string | null): string {
  const sanitized = sanitizeWorkspaceName(workspace ?? '');
  if (sanitized) {
    return `?workspace=${encodeURIComponent(sanitized)}`;
  }
  return '';
}

function normalizedWikiWorkspace(workspace?: string | null): string | null {
  return sanitizeWorkspaceName(workspace ?? '') || null;
}

function WikiContent() {
  const { ts } = useI18n();
  const appBarContext = useContext(AppBarContext);
  const { setTitle } = appBarContext;
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const navigate = useNavigate();
  const location = useLocation();
  const client = useClient();
  const { showToast } = useSimpleToast();
  const isMobile = useIsMobile();

  const { selectedTemplate, selectTemplate } = useCockpitState();
  const workspaceSelection = appBarContext.workspaceSelection;
  const normalizedWorkspaceSelection =
    sanitizeWorkspaceSelection(workspaceSelection);
  const selectedWorkspace = workspaceNameForSelection(workspaceSelection);
  const canCreateAtRoot =
    normalizedWorkspaceSelection.kind !== WorkspaceKind.all;
  const rootCreateWorkspace =
    normalizedWorkspaceSelection.kind === WorkspaceKind.workspace
      ? (normalizedWorkspaceSelection.workspace ?? null)
      : null;
  const workspaceQuery = React.useMemo(
    () => workspaceSelectionQuery(workspaceSelection),
    [workspaceSelection]
  );
  const canWrite = useCanWrite();

  const { tabs, activeTabId, openWikiPage } = useWikiPageTabContext();

  // Mobile view state
  const [mobileView, setMobileView] = useState<'tree' | 'editor'>('tree');

  // Active page content for outline panel
  const [activeWikiPageContent, setActiveWikiPageContent] = useState<
    string | null
  >(null);

  // Clear stale content when switching tabs so the outline panel doesn't show old headings
  useEffect(() => {
    setActiveWikiPageContent(null);
  }, [activeTabId]);

  // Modal state
  const [createModalOpen, setCreateModalOpen] = useState(false);
  const [createParentDir, setCreateParentDir] = useState('');
  const [createWorkspace, setCreateWorkspace] = useState<string | null>(null);
  const [createLoading, setCreateLoading] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);

  const [renameModalOpen, setRenameModalOpen] = useState(false);
  const [renameWikiPagePath, setRenameWikiPagePath] = useState('');
  const [renameWorkspace, setRenameWorkspace] = useState<string | null>(null);
  const [renameLoading, setRenameLoading] = useState(false);
  const [renameError, setRenameError] = useState<string | null>(null);

  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
  const [deleteWikiPagePath, setDeleteWikiPagePath] = useState('');
  const [deleteWikiPageTitle, setDeleteWikiPageTitle] = useState('');
  const [deleteWorkspace, setDeleteWorkspace] = useState<string | null>(null);

  // Batch delete state
  const [batchDeleteTargets, setBatchDeleteTargets] = useState<
    WikiPageMutationTarget[]
  >([]);
  const [batchDeleteConfirmOpen, setBatchDeleteConfirmOpen] = useState(false);

  // Sort preferences
  const { preferences, updatePreference } = useUserPreferences();
  const { wikiSortField, wikiSortOrder } = preferences;
  const sort = wikiSortField as PathsWikiGetParametersQuerySort;
  const order = wikiSortOrder as PathsWikiGetParametersQueryOrder;

  const wikiTreeSSE = useWikiTreeSSE({
    sort,
    order,
    remoteNode,
    ...workspaceQuery,
  });

  const {
    data: treeData,
    mutate,
    error: treeError,
    isLoading: treeIsLoading,
  } = useQuery(
    '/wiki',
    {
      params: {
        query: {
          remoteNode,
          perPage: 200,
          sort,
          order,
          ...workspaceQuery,
        },
      },
    },
    {
      ...sseFallbackOptions(wikiTreeSSE, WIKI_SSE_FALLBACK_INTERVAL_MS),
      revalidateIfStale: false,
      revalidateOnFocus: false,
      revalidateOnReconnect: false,
      keepPreviousData: true,
    }
  );
  useSSECacheSync(wikiTreeSSE, mutate);
  const revalidateTree = useCallback(() => {
    void mutate();
  }, [mutate]);
  const { changePath, deleteBatch, deletePath, hasUnsavedTabs } =
    useWikiPageMutations({ remoteNode, revalidateTree });

  // Set page title
  useEffect(() => {
    setTitle('Wiki');
  }, [setTitle]);

  // URL ↔ Tab sync with loop prevention
  const isNavigatingRef = useRef(false);
  const isInitialMountRef = useRef(true);

  // URL → Tab (source of truth on mount)
  useEffect(() => {
    if (isNavigatingRef.current) return;
    const wikiPagePath = location.pathname.replace(/^\/wiki\/?/, '');
    if (wikiPagePath) {
      const searchParams = new URLSearchParams(location.search);
      const queryWorkspace = sanitizeWorkspaceName(
        searchParams.get('workspace') ?? ''
      );
      const wikiWorkspace = queryWorkspace || null;
      const decodedWikiPagePath = safeDecodeURIComponent(wikiPagePath);
      if (decodedWikiPagePath === null) {
        navigate('/wiki', { replace: true });
        return;
      }
      const decodedPath = normalizeWikiPagePathFromURL(decodedWikiPagePath);
      openWikiPage(decodedPath, titleFromPath(decodedPath), wikiWorkspace);
    }
  }, [
    location.pathname,
    location.search,
    navigate,
    openWikiPage,
    selectedWorkspace,
  ]);

  // Tab → URL (skip on initial mount — URL takes precedence).
  // Deliberately triggered by tab changes only: reacting to location changes
  // here races the URL → Tab effect above (an external navigation lands
  // before the tab state catches up) and the two effects then navigate
  // against each other. The latest location is read through a ref instead.
  const locationRef = useRef(location);
  locationRef.current = location;
  useEffect(() => {
    if (isInitialMountRef.current) {
      isInitialMountRef.current = false;
      return;
    }
    if (isNavigatingRef.current) return;
    const activeTab = activeTabId
      ? tabs.find((t) => t.id === activeTabId)
      : null;
    const wikiPagePath = activeTab?.wikiPagePath;
    const currentLocation = locationRef.current;
    const currentPath = currentLocation.pathname.replace(/^\/wiki\/?/, '');
    const targetSearch = activeTab
      ? workspaceSearchForWikiPageTab(activeTab.workspace)
      : '';
    const encodedWikiPagePath = wikiPagePath
      ? encodeWikiPagePathForURL(wikiPagePath)
      : '';
    if (
      wikiPagePath &&
      (encodedWikiPagePath !== currentPath ||
        currentLocation.search !== targetSearch)
    ) {
      isNavigatingRef.current = true;
      navigate(`/wiki/${encodedWikiPagePath}${targetSearch}`, {
        replace: true,
      });
      requestAnimationFrame(() => {
        isNavigatingRef.current = false;
      });
    } else if (!wikiPagePath && currentLocation.pathname !== '/wiki') {
      isNavigatingRef.current = true;
      navigate('/wiki', { replace: true });
      requestAnimationFrame(() => {
        isNavigatingRef.current = false;
      });
    }
  }, [activeTabId, tabs, navigate]);

  // File selection handler
  const handleSelectFile = useCallback(
    (wikiPagePath: string, title: string, workspace?: string | null) => {
      const visiblePath = visibleWikiPagePathForWorkspace(
        wikiPagePath,
        workspace
      );
      openWikiPage(visiblePath, title, workspace ?? null);
      if (isMobile) setMobileView('editor');
    },
    [openWikiPage, isMobile]
  );

  // Context menu actions
  const handleContextAction = useCallback((action: ContextAction) => {
    switch (action.type) {
      case 'create':
        setCreateParentDir(action.parentDir);
        setCreateWorkspace(normalizedWikiWorkspace(action.workspace));
        setCreateError(null);
        setCreateModalOpen(true);
        break;
      case 'rename':
        setRenameWikiPagePath(action.wikiPagePath);
        setRenameWorkspace(normalizedWikiWorkspace(action.workspace));
        setRenameError(null);
        setRenameModalOpen(true);
        break;
      case 'delete':
        setDeleteWikiPagePath(action.wikiPagePath);
        setDeleteWikiPageTitle(action.title);
        setDeleteWorkspace(normalizedWikiWorkspace(action.workspace));
        setDeleteConfirmOpen(true);
        break;
      case 'deleteBatch':
        setBatchDeleteTargets([...action.targets]);
        setBatchDeleteConfirmOpen(true);
        break;
    }
  }, []);

  // Create handler
  const handleCreate = useCallback(
    async (path: string, content: string) => {
      if (!canWrite) {
        setCreateError('You do not have permission to create Wiki pages');
        return;
      }
      setCreateLoading(true);
      setCreateError(null);
      try {
        const mutationQuery = workspaceTargetQueryForWorkspace(createWorkspace);
        const { error } = await client.POST('/wiki', {
          params: { query: { remoteNode, ...mutationQuery } },
          body: { id: path, content },
        });
        if (error) {
          setCreateError(error?.message || 'Failed to create Wiki page');
          return;
        }
        mutate();
        openWikiPage(path, titleFromPath(path), createWorkspace);
        showToast('Wiki page created');
        setCreateModalOpen(false);
      } catch {
        setCreateError('Failed to create Wiki page');
      } finally {
        setCreateLoading(false);
      }
    },
    [
      canWrite,
      client,
      createWorkspace,
      remoteNode,
      mutate,
      openWikiPage,
      showToast,
    ]
  );

  // Rename handler (from modal)
  const handleRenameModal = useCallback(
    async (newPath: string) => {
      if (!canWrite) {
        setRenameError('You do not have permission to rename Wiki pages');
        return;
      }
      if (hasUnsavedTabs(renameWikiPagePath, renameWorkspace)) {
        setRenameError('Save open changes before renaming this path');
        return;
      }
      setRenameLoading(true);
      setRenameError(null);
      try {
        const error = await changePath({
          oldPath: renameWikiPagePath,
          newPath,
          workspace: renameWorkspace,
          failureMessage: 'Failed to rename Wiki page',
        });
        if (error) {
          setRenameError(error);
          return;
        }
        showToast('Wiki page renamed');
        setRenameModalOpen(false);
      } finally {
        setRenameLoading(false);
      }
    },
    [
      canWrite,
      renameWikiPagePath,
      renameWorkspace,
      changePath,
      hasUnsavedTabs,
      showToast,
    ]
  );

  // Shared path-change handler for rename and move
  const handlePathChange = useCallback(
    async (
      oldPath: string,
      newPath: string,
      action: 'renamed' | 'moved',
      workspace?: string | null
    ) => {
      if (!canWrite) {
        showToast('You do not have permission to edit Wiki pages');
        return;
      }
      const mutationWorkspace = normalizedWikiWorkspace(workspace);
      if (hasUnsavedTabs(oldPath, mutationWorkspace)) {
        showToast('Save open changes before renaming or moving this path');
        return;
      }
      const failureMessage = `Failed to ${
        action === 'renamed' ? 'rename' : 'move'
      } Wiki page`;
      const error = await changePath({
        oldPath,
        newPath,
        workspace: mutationWorkspace,
        failureMessage,
        revalidateOnFailure: true,
      });
      if (error) {
        showToast(error);
        return;
      }
      showToast(`Wiki page ${action}`);
    },
    [canWrite, changePath, hasUnsavedTabs, showToast]
  );

  const handleInlineRename = useCallback(
    (oldPath: string, newPath: string, workspace?: string | null) =>
      handlePathChange(oldPath, newPath, 'renamed', workspace),
    [handlePathChange]
  );

  const handleMove = useCallback(
    (oldPath: string, newPath: string, workspace?: string | null) =>
      handlePathChange(oldPath, newPath, 'moved', workspace),
    [handlePathChange]
  );

  // Heading click for outline panel
  const handleHeadingClick = useCallback((anchor: string) => {
    // Find the heading in the preview panel and scroll to it
    const el = document.getElementById(anchor);
    if (el) {
      el.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }
  }, []);

  // Delete handler (supports both files and directories)
  const handleDelete = useCallback(async () => {
    if (!canWrite) {
      showToast('You do not have permission to delete Wiki pages');
      setDeleteConfirmOpen(false);
      return;
    }
    try {
      const error = await deletePath(deleteWikiPagePath, deleteWorkspace);
      if (error) {
        showToast(error);
        return;
      }
      showToast('Wiki page deleted');
    } finally {
      setDeleteConfirmOpen(false);
    }
  }, [canWrite, deleteWikiPagePath, deleteWorkspace, deletePath, showToast]);

  // Batch delete handler
  const handleBatchDelete = useCallback(async () => {
    if (!canWrite) {
      showToast('You do not have permission to delete Wiki pages');
      setBatchDeleteConfirmOpen(false);
      setBatchDeleteTargets([]);
      return;
    }
    try {
      const { deletedCount, failedCount } =
        await deleteBatch(batchDeleteTargets);
      if (failedCount > 0) {
        showToast(`Deleted ${deletedCount}, ${failedCount} failed`);
      } else {
        showToast(`Deleted ${deletedCount} items`);
      }
    } catch {
      showToast('Failed to delete Wiki pages');
    } finally {
      setBatchDeleteConfirmOpen(false);
      setBatchDeleteTargets([]);
    }
  }, [batchDeleteTargets, canWrite, deleteBatch, showToast]);

  // Batch delete from selection bar
  const handleBatchDeleteFromBar = useCallback(
    (targets: WikiPageMutationTarget[]) => {
      setBatchDeleteTargets(targets);
      setBatchDeleteConfirmOpen(true);
    },
    []
  );

  // Delete triggered from tab menu or editor header
  const handleDeleteFromTab = useCallback(
    (wikiPagePath: string, title: string, workspace?: string | null) => {
      setDeleteWikiPagePath(wikiPagePath);
      setDeleteWikiPageTitle(title);
      setDeleteWorkspace(normalizedWikiWorkspace(workspace));
      setDeleteConfirmOpen(true);
    },
    []
  );

  const leftPanel = (
    <WikiTreeSidebar
      tree={treeData?.tree}
      isLoading={treeIsLoading}
      error={treeError}
      onRetry={() => mutate()}
      onContextAction={handleContextAction}
      canCreateNew={canCreateAtRoot}
      onCreateNew={() => {
        if (!canCreateAtRoot) {
          showToast('Select a workspace before creating a Wiki page');
          return;
        }
        setCreateParentDir('');
        setCreateWorkspace(rootCreateWorkspace);
        setCreateError(null);
        setCreateModalOpen(true);
      }}
      onSelectFile={handleSelectFile}
      onRename={handleInlineRename}
      onMove={handleMove}
      onBatchDelete={handleBatchDeleteFromBar}
      activeWikiPageContent={activeWikiPageContent}
      onHeadingClick={handleHeadingClick}
      sortField={wikiSortField}
      sortOrder={wikiSortOrder}
      onSortChange={(field, order) => {
        updatePreference('wikiSortField', field);
        updatePreference('wikiSortOrder', order);
      }}
    />
  );

  const cockpitToolbar = (
    <div className="[&>div]:mb-0">
      <CockpitToolbar
        selectedWorkspace={selectedWorkspace}
        selectedTemplate={selectedTemplate}
        onSelectTemplate={selectTemplate}
      />
    </div>
  );

  const rightPanel =
    tabs.length > 0 ? (
      <WikiPageTabEditorPanel
        onDeleteWikiPage={handleDeleteFromTab}
        toolbar={cockpitToolbar}
        onContentChange={setActiveWikiPageContent}
      />
    ) : null;

  const modals = (
    <>
      <CreateWikiPageModal
        isOpen={createModalOpen}
        onClose={() => setCreateModalOpen(false)}
        onSubmit={handleCreate}
        parentDir={createParentDir}
        workspace={createWorkspace}
        isLoading={createLoading}
        externalError={createError}
      />
      <RenameWikiPageModal
        isOpen={renameModalOpen}
        onClose={() => setRenameModalOpen(false)}
        onSubmit={handleRenameModal}
        currentPath={renameWikiPagePath}
        isLoading={renameLoading}
        externalError={renameError}
      />
      <I18nProps>
        <ConfirmModal
          title="Delete Wiki page"
          buttonText="Delete"
          visible={deleteConfirmOpen}
          dismissModal={() => setDeleteConfirmOpen(false)}
          onSubmit={handleDelete}
        >
          <p className="text-sm text-muted-foreground">
            <I18nTemplate
              text="Are you sure you want to delete {name}? This action cannot be undone."
              values={{ name: <strong>{deleteWikiPageTitle}</strong> }}
            />
          </p>
        </ConfirmModal>
      </I18nProps>
      <I18nProps>
        <ConfirmModal
          title="Delete Wiki"
          buttonText={ts('Delete {count} items', {
            count: batchDeleteTargets.length,
          })}
          visible={batchDeleteConfirmOpen}
          dismissModal={() => setBatchDeleteConfirmOpen(false)}
          onSubmit={handleBatchDelete}
        >
          <p className="text-sm text-muted-foreground">
            {ts(
              'Are you sure you want to delete {count} items? This cannot be undone.',
              {
                count: batchDeleteTargets.length,
              }
            )}
          </p>
        </ConfirmModal>
      </I18nProps>
    </>
  );

  // Mobile layout
  if (isMobile) {
    return (
      <div className="-m-4 w-[calc(100%+2rem)] h-[calc(100%+2rem)]">
        {mobileView === 'tree' ? (
          <div className="h-full">{leftPanel}</div>
        ) : (
          <div className="flex flex-col h-full">
            <button
              type="button"
              className="flex items-center gap-1 px-3 py-2 text-sm text-muted-foreground hover:text-foreground border-b border-border"
              onClick={() => setMobileView('tree')}
            >
              <ChevronLeft className="h-4 w-4" />
              <I18nText text={'Wiki'} />
            </button>
            <div className="flex-1 overflow-hidden min-h-0">
              {rightPanel || (
                <div className="flex items-center justify-center h-full">
                  <p className="text-sm text-muted-foreground">
                    <I18nText text={'Select a Wiki page to start editing.'} />
                  </p>
                </div>
              )}
            </div>
          </div>
        )}

        {modals}
      </div>
    );
  }

  // Desktop layout
  return (
    <div className="-m-4 md:-m-6 w-[calc(100%+2rem)] md:w-[calc(100%+3rem)] h-[calc(100%+2rem)] md:h-[calc(100%+3rem)]">
      <SplitLayout
        leftPanel={leftPanel}
        rightPanel={rightPanel}
        defaultLeftWidth={25}
        minLeftWidth={15}
        maxLeftWidth={40}
        storageKey="wikiTreeWidth"
        legacyStorageKey="docTreeWidth"
        emptyRightMessage="Select a Wiki page to start editing"
      />

      {modals}
    </div>
  );
}

function WikiPage() {
  const appBarContext = useContext(AppBarContext);
  const { user } = useAuth();
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const wikiTabStorageKey = `dagu_wiki_tabs:${JSON.stringify({
    userId: user?.id ?? 'anonymous',
    remoteNode,
    workspace: workspaceSelectionKey(appBarContext.workspaceSelection),
  })}`;

  return (
    <UnsavedChangesProvider>
      <WikiPageTabProvider
        key={wikiTabStorageKey}
        storageKey={wikiTabStorageKey}
      >
        <WikiContent />
      </WikiPageTabProvider>
    </UnsavedChangesProvider>
  );
}

export default WikiPage;
