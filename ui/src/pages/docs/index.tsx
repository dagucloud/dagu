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
  PathsDocsGetParametersQueryOrder,
  PathsDocsGetParametersQuerySort,
} from '@/api/v1/schema';
import SplitLayout from '@/components/SplitLayout';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useAuth, useCanWrite } from '@/contexts/AuthContext';
import { DocTabProvider, useDocTabContext } from '@/contexts/DocTabContext';
import { UnsavedChangesProvider } from '@/contexts/UnsavedChangesContext';
import { useUserPreferences } from '@/contexts/UserPreference';
import { CockpitToolbar } from '@/features/cockpit/components/CockpitToolbar';
import { useCockpitState } from '@/features/cockpit/hooks/useCockpitState';
import { useClient, useQuery } from '@/hooks/api';
import { useIsMobile } from '@/hooks/useIsMobile';
import { useDocTreeSSE } from '@/hooks/useDocTreeSSE';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';
import {
  sanitizeWorkspaceName,
  sanitizeWorkspaceSelection,
  WorkspaceKind,
  workspaceTargetQueryForWorkspace,
  workspaceNameForSelection,
  workspaceSelectionKey,
  workspaceSelectionQuery,
  visibleDocumentPathForWorkspace,
} from '@/lib/workspace';
import ConfirmModal from '@/components/ui/confirm-dialog';
import { CreateDocModal } from './components/CreateDocModal';
import DocTabEditorPanel from './components/DocTabEditorPanel';
import DocTreeSidebar from './components/DocTreeSidebar';
import { RenameDocModal } from './components/RenameDocModal';
import { DOC_SSE_FALLBACK_INTERVAL_MS } from './lib/doc-polling';
import { normalizeDocPathFromURL } from './lib/doc-url';
import type { DocMutationTarget } from './lib/doc-mutation';
import { useDocMutations } from './hooks/useDocMutations';
import type { ContextAction } from './components/DocArboristNode';

function titleFromPath(docPath: string): string {
  const segments = docPath.split('/');
  return segments[segments.length - 1] || docPath;
}

function encodeDocPathForURL(docPath: string): string {
  return docPath.split('/').map(encodeURIComponent).join('/');
}

function safeDecodeURIComponent(value: string): string | null {
  try {
    return decodeURIComponent(value);
  } catch {
    return null;
  }
}

function workspaceSearchForDocTab(workspace?: string | null): string {
  const sanitized = sanitizeWorkspaceName(workspace ?? '');
  if (sanitized) {
    return `?workspace=${encodeURIComponent(sanitized)}`;
  }
  return '';
}

function normalizedDocWorkspace(workspace?: string | null): string | null {
  return sanitizeWorkspaceName(workspace ?? '') || null;
}

function DocsContent() {
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

  const { tabs, activeTabId, openDoc } = useDocTabContext();

  // Mobile view state
  const [mobileView, setMobileView] = useState<'tree' | 'editor'>('tree');

  // Active doc content for outline panel
  const [activeDocContent, setActiveDocContent] = useState<string | null>(null);

  // Clear stale content when switching tabs so the outline panel doesn't show old headings
  useEffect(() => {
    setActiveDocContent(null);
  }, [activeTabId]);

  // Modal state
  const [createModalOpen, setCreateModalOpen] = useState(false);
  const [createParentDir, setCreateParentDir] = useState('');
  const [createWorkspace, setCreateWorkspace] = useState<string | null>(null);
  const [createLoading, setCreateLoading] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);

  const [renameModalOpen, setRenameModalOpen] = useState(false);
  const [renameDocPath, setRenameDocPath] = useState('');
  const [renameWorkspace, setRenameWorkspace] = useState<string | null>(null);
  const [renameLoading, setRenameLoading] = useState(false);
  const [renameError, setRenameError] = useState<string | null>(null);

  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
  const [deleteDocPath, setDeleteDocPath] = useState('');
  const [deleteDocTitle, setDeleteDocTitle] = useState('');
  const [deleteWorkspace, setDeleteWorkspace] = useState<string | null>(null);

  // Batch delete state
  const [batchDeleteTargets, setBatchDeleteTargets] = useState<
    DocMutationTarget[]
  >([]);
  const [batchDeleteConfirmOpen, setBatchDeleteConfirmOpen] = useState(false);

  // Sort preferences
  const { preferences, updatePreference } = useUserPreferences();
  const { docSortField, docSortOrder } = preferences;
  const sort = docSortField as PathsDocsGetParametersQuerySort;
  const order = docSortOrder as PathsDocsGetParametersQueryOrder;

  const docTreeSSE = useDocTreeSSE({
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
    '/docs',
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
      ...sseFallbackOptions(docTreeSSE, DOC_SSE_FALLBACK_INTERVAL_MS),
      revalidateIfStale: false,
      revalidateOnFocus: false,
      revalidateOnReconnect: false,
      keepPreviousData: true,
    }
  );
  useSSECacheSync(docTreeSSE, mutate);
  const revalidateTree = useCallback(() => {
    void mutate();
  }, [mutate]);
  const { changePath, deleteBatch, deletePath, hasUnsavedTabs } =
    useDocMutations({ remoteNode, revalidateTree });

  // Set page title
  useEffect(() => {
    setTitle('Docs');
  }, [setTitle]);

  // URL ↔ Tab sync with loop prevention
  const isNavigatingRef = useRef(false);
  const isInitialMountRef = useRef(true);

  // URL → Tab (source of truth on mount)
  useEffect(() => {
    if (isNavigatingRef.current) return;
    const docPath = location.pathname.replace(/^\/docs\/?/, '');
    if (docPath) {
      const searchParams = new URLSearchParams(location.search);
      const queryWorkspace = sanitizeWorkspaceName(
        searchParams.get('workspace') ?? ''
      );
      const docWorkspace = queryWorkspace || null;
      const decodedDocPath = safeDecodeURIComponent(docPath);
      if (decodedDocPath === null) {
        navigate('/docs', { replace: true });
        return;
      }
      const decodedPath = normalizeDocPathFromURL(decodedDocPath);
      openDoc(decodedPath, titleFromPath(decodedPath), docWorkspace);
    }
  }, [
    location.pathname,
    location.search,
    navigate,
    openDoc,
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
    const docPath = activeTab?.docPath;
    const currentLocation = locationRef.current;
    const currentPath = currentLocation.pathname.replace(/^\/docs\/?/, '');
    const targetSearch = activeTab
      ? workspaceSearchForDocTab(activeTab.workspace)
      : '';
    const encodedDocPath = docPath ? encodeDocPathForURL(docPath) : '';
    if (docPath && encodedDocPath !== currentPath) {
      isNavigatingRef.current = true;
      navigate(`/docs/${encodedDocPath}${targetSearch}`, { replace: true });
      requestAnimationFrame(() => {
        isNavigatingRef.current = false;
      });
    } else if (docPath && currentLocation.search !== targetSearch) {
      isNavigatingRef.current = true;
      navigate(`/docs/${encodedDocPath}${targetSearch}`, { replace: true });
      requestAnimationFrame(() => {
        isNavigatingRef.current = false;
      });
    } else if (!docPath && currentLocation.pathname !== '/docs') {
      isNavigatingRef.current = true;
      navigate('/docs', { replace: true });
      requestAnimationFrame(() => {
        isNavigatingRef.current = false;
      });
    }
  }, [activeTabId, tabs, navigate]);

  // File selection handler
  const handleSelectFile = useCallback(
    (docPath: string, title: string, workspace?: string | null) => {
      const visiblePath = visibleDocumentPathForWorkspace(docPath, workspace);
      openDoc(visiblePath, title, workspace ?? null);
      if (isMobile) setMobileView('editor');
    },
    [openDoc, isMobile]
  );

  // Context menu actions
  const handleContextAction = useCallback((action: ContextAction) => {
    switch (action.type) {
      case 'create':
        setCreateParentDir(action.parentDir);
        setCreateWorkspace(normalizedDocWorkspace(action.workspace));
        setCreateError(null);
        setCreateModalOpen(true);
        break;
      case 'rename':
        setRenameDocPath(action.docPath);
        setRenameWorkspace(normalizedDocWorkspace(action.workspace));
        setRenameError(null);
        setRenameModalOpen(true);
        break;
      case 'delete':
        setDeleteDocPath(action.docPath);
        setDeleteDocTitle(action.title);
        setDeleteWorkspace(normalizedDocWorkspace(action.workspace));
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
        setCreateError('You do not have permission to create documents');
        return;
      }
      setCreateLoading(true);
      setCreateError(null);
      try {
        const mutationQuery = workspaceTargetQueryForWorkspace(createWorkspace);
        const { error } = await client.POST('/docs', {
          params: { query: { remoteNode, ...mutationQuery } },
          body: { id: path, content },
        });
        if (error) {
          setCreateError(error?.message || 'Failed to create document');
          return;
        }
        mutate();
        openDoc(path, titleFromPath(path), createWorkspace);
        showToast('Document created');
        setCreateModalOpen(false);
      } catch {
        setCreateError('Failed to create document');
      } finally {
        setCreateLoading(false);
      }
    },
    [canWrite, client, createWorkspace, remoteNode, mutate, openDoc, showToast]
  );

  // Rename handler (from modal)
  const handleRenameModal = useCallback(
    async (newPath: string) => {
      if (!canWrite) {
        setRenameError('You do not have permission to rename documents');
        return;
      }
      if (hasUnsavedTabs(renameDocPath, renameWorkspace)) {
        setRenameError('Save open changes before renaming this path');
        return;
      }
      setRenameLoading(true);
      setRenameError(null);
      try {
        const error = await changePath({
          oldPath: renameDocPath,
          newPath,
          workspace: renameWorkspace,
          failureMessage: 'Failed to rename document',
        });
        if (error) {
          setRenameError(error);
          return;
        }
        showToast('Document renamed');
        setRenameModalOpen(false);
      } finally {
        setRenameLoading(false);
      }
    },
    [
      canWrite,
      renameDocPath,
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
        showToast('You do not have permission to edit documents');
        return;
      }
      const mutationWorkspace = normalizedDocWorkspace(workspace);
      if (hasUnsavedTabs(oldPath, mutationWorkspace)) {
        showToast('Save open changes before renaming or moving this path');
        return;
      }
      const failureMessage = `Failed to ${
        action === 'renamed' ? 'rename' : 'move'
      } document`;
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
      showToast(`Document ${action}`);
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
      showToast('You do not have permission to delete documents');
      setDeleteConfirmOpen(false);
      return;
    }
    try {
      const error = await deletePath(deleteDocPath, deleteWorkspace);
      if (error) {
        showToast(error);
        return;
      }
      showToast('Document deleted');
    } finally {
      setDeleteConfirmOpen(false);
    }
  }, [canWrite, deleteDocPath, deleteWorkspace, deletePath, showToast]);

  // Batch delete handler
  const handleBatchDelete = useCallback(async () => {
    if (!canWrite) {
      showToast('You do not have permission to delete documents');
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
      showToast('Failed to delete documents');
    } finally {
      setBatchDeleteConfirmOpen(false);
      setBatchDeleteTargets([]);
    }
  }, [batchDeleteTargets, canWrite, deleteBatch, showToast]);

  // Batch delete from selection bar
  const handleBatchDeleteFromBar = useCallback(
    (targets: DocMutationTarget[]) => {
      setBatchDeleteTargets(targets);
      setBatchDeleteConfirmOpen(true);
    },
    []
  );

  // Delete triggered from tab menu or editor header
  const handleDeleteFromTab = useCallback(
    (docPath: string, title: string, workspace?: string | null) => {
      setDeleteDocPath(docPath);
      setDeleteDocTitle(title);
      setDeleteWorkspace(normalizedDocWorkspace(workspace));
      setDeleteConfirmOpen(true);
    },
    []
  );

  const leftPanel = (
    <DocTreeSidebar
      tree={treeData?.tree}
      isLoading={treeIsLoading}
      error={treeError}
      onRetry={() => mutate()}
      onContextAction={handleContextAction}
      canCreateNew={canCreateAtRoot}
      onCreateNew={() => {
        if (!canCreateAtRoot) {
          showToast('Select a workspace before creating a document');
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
      activeDocContent={activeDocContent}
      onHeadingClick={handleHeadingClick}
      sortField={docSortField}
      sortOrder={docSortOrder}
      onSortChange={(field, order) => {
        updatePreference('docSortField', field);
        updatePreference('docSortOrder', order);
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
      <DocTabEditorPanel
        onDeleteDoc={handleDeleteFromTab}
        toolbar={cockpitToolbar}
        onContentChange={setActiveDocContent}
      />
    ) : null;

  const modals = (
    <>
      <CreateDocModal
        isOpen={createModalOpen}
        onClose={() => setCreateModalOpen(false)}
        onSubmit={handleCreate}
        parentDir={createParentDir}
        workspace={createWorkspace}
        isLoading={createLoading}
        externalError={createError}
      />
      <RenameDocModal
        isOpen={renameModalOpen}
        onClose={() => setRenameModalOpen(false)}
        onSubmit={handleRenameModal}
        currentPath={renameDocPath}
        isLoading={renameLoading}
        externalError={renameError}
      />
      <ConfirmModal
        title="Delete Document"
        buttonText="Delete"
        visible={deleteConfirmOpen}
        dismissModal={() => setDeleteConfirmOpen(false)}
        onSubmit={handleDelete}
      >
        <p className="text-sm text-muted-foreground">
          Are you sure you want to delete <strong>{deleteDocTitle}</strong>?
          This action cannot be undone.
        </p>
      </ConfirmModal>
      <ConfirmModal
        title="Delete Docs"
        buttonText={`Delete ${batchDeleteTargets.length} items`}
        visible={batchDeleteConfirmOpen}
        dismissModal={() => setBatchDeleteConfirmOpen(false)}
        onSubmit={handleBatchDelete}
      >
        <p className="text-sm text-muted-foreground">
          Are you sure you want to delete {batchDeleteTargets.length} items?
          This cannot be undone.
        </p>
      </ConfirmModal>
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
              Docs
            </button>
            <div className="flex-1 overflow-hidden min-h-0">
              {rightPanel || (
                <div className="flex items-center justify-center h-full">
                  <p className="text-sm text-muted-foreground">
                    Select a document to start editing.
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
        storageKey="docTreeWidth"
        emptyRightMessage="Select a document to start editing"
      />

      {modals}
    </div>
  );
}

function DocsPage() {
  const appBarContext = useContext(AppBarContext);
  const { user } = useAuth();
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const docTabStorageKey = `dagu_doc_tabs:${JSON.stringify({
    userId: user?.id ?? 'anonymous',
    remoteNode,
    workspace: workspaceSelectionKey(appBarContext.workspaceSelection),
  })}`;

  return (
    <UnsavedChangesProvider>
      <DocTabProvider key={docTabStorageKey} storageKey={docTabStorageKey}>
        <DocsContent />
      </DocTabProvider>
    </UnsavedChangesProvider>
  );
}

export default DocsPage;
