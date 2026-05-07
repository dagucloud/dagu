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
import { useCanWrite } from '@/contexts/AuthContext';
import { DocTabProvider, useDocTabContext } from '@/contexts/DocTabContext';
import { usePageContext } from '@/contexts/PageContext';
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
import { normalizeDocPathFromURL } from './lib/doc-url';
import type { DocMutationTarget } from './lib/doc-mutation';
import type { ContextAction } from './components/DocArboristNode';

function titleFromPath(docPath: string): string {
  const segments = docPath.split('/');
  return segments[segments.length - 1] || docPath;
}

function encodeDocPathForURL(docPath: string): string {
  return docPath.split('/').map(encodeURIComponent).join('/');
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

function docWorkspaceMatches(
  left?: string | null,
  right?: string | null
): boolean {
  return normalizedDocWorkspace(left) === normalizedDocWorkspace(right);
}

function docPathMatches(docPath: string, targetPath: string): boolean {
  return docPath === targetPath || docPath.startsWith(targetPath + '/');
}

function DocsContent() {
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const navigate = useNavigate();
  const location = useLocation();
  const client = useClient();
  const { showToast } = useSimpleToast();
  const isMobile = useIsMobile();

  const { selectedTemplate, selectTemplate } = useCockpitState();
  const workspaceSelection = appBarContext.workspaceSelection;
  const selectedWorkspace = workspaceNameForSelection(workspaceSelection);
  const workspaceQuery = React.useMemo(
    () => workspaceSelectionQuery(workspaceSelection),
    [workspaceSelection]
  );
  const canWrite = useCanWrite();

  const { setContext } = usePageContext();
  const {
    tabs,
    activeTabId,
    openDoc,
    closeTab,
    updateTab,
    clearDraft,
    markTabSaved,
  } = useDocTabContext();

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
      ...sseFallbackOptions(docTreeSSE),
      revalidateIfStale: false,
      revalidateOnFocus: false,
      revalidateOnReconnect: false,
      keepPreviousData: true,
    }
  );
  useSSECacheSync(docTreeSSE, mutate);

  // Set page title
  useEffect(() => {
    appBarContext.setTitle('Documents');
  }, [appBarContext]);

  // Set page context for agent chat (mirrors DAG detail page pattern)
  useEffect(() => {
    const activeTab = tabs.find((t) => t.id === activeTabId);
    if (activeTab) {
      setContext({
        docPath: activeTab.docPath,
        docTitle: activeTab.title,
        source: 'docs-page',
      });
    } else {
      setContext(null);
    }
    return () => {
      setContext(null);
    };
  }, [activeTabId, tabs, setContext]);

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
      const decodedPath = normalizeDocPathFromURL(decodeURIComponent(docPath));
      openDoc(decodedPath, titleFromPath(decodedPath), docWorkspace);
    }
  }, [location.pathname, location.search, openDoc, selectedWorkspace]);

  // Tab → URL (skip on initial mount — URL takes precedence)
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
    const currentPath = location.pathname.replace(/^\/docs\/?/, '');
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
    } else if (docPath && location.search !== targetSearch) {
      isNavigatingRef.current = true;
      navigate(`/docs/${encodedDocPath}${targetSearch}`, { replace: true });
      requestAnimationFrame(() => {
        isNavigatingRef.current = false;
      });
    } else if (!docPath && location.pathname !== '/docs') {
      isNavigatingRef.current = true;
      navigate('/docs', { replace: true });
      requestAnimationFrame(() => {
        isNavigatingRef.current = false;
      });
    }
  }, [activeTabId, tabs, navigate, location.pathname, location.search]);

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
  const handleContextAction = useCallback(
    (action: ContextAction) => {
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
    },
    []
  );

  // Create handler
  const handleCreate = useCallback(
    async (path: string) => {
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
          body: { id: path, content: '' },
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
    [
      canWrite,
      client,
      createWorkspace,
      remoteNode,
      mutate,
      openDoc,
      showToast,
    ]
  );

  // Rename handler (from modal)
  const handleRenameModal = useCallback(
    async (newPath: string) => {
      if (!canWrite) {
        setRenameError('You do not have permission to rename documents');
        return;
      }
      setRenameLoading(true);
      setRenameError(null);
      try {
        const mutationQuery = workspaceTargetQueryForWorkspace(renameWorkspace);
        const { error } = await client.POST('/docs/doc/rename', {
          params: {
            query: {
              remoteNode,
              path: renameDocPath,
              ...mutationQuery,
            },
          },
          body: { newPath },
        });
        if (error) {
          setRenameError(error?.message || 'Failed to rename document');
          return;
        }
        mutate();
        // Update tabs under the renamed path in the mutated workspace only.
        for (const tab of tabs) {
          if (
            docWorkspaceMatches(tab.workspace, renameWorkspace) &&
            docPathMatches(tab.docPath, renameDocPath)
          ) {
            const updatedPath =
              newPath + tab.docPath.slice(renameDocPath.length);
            updateTab(tab.id, {
              docPath: updatedPath,
              title: titleFromPath(updatedPath),
            });
          }
        }
        showToast('Document renamed');
        setRenameModalOpen(false);
      } catch {
        setRenameError('Failed to rename document');
      } finally {
        setRenameLoading(false);
      }
    },
    [
      client,
      canWrite,
      remoteNode,
      renameDocPath,
      renameWorkspace,
      mutate,
      tabs,
      updateTab,
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
      try {
        const mutationQuery =
          workspaceTargetQueryForWorkspace(mutationWorkspace);
        const { error } = await client.POST('/docs/doc/rename', {
          params: {
            query: { remoteNode, path: oldPath, ...mutationQuery },
          },
          body: { newPath },
        });
        if (error) {
          showToast(
            error?.message ||
              `Failed to ${action === 'renamed' ? 'rename' : 'move'} document`
          );
          mutate();
          return;
        }
        mutate();
        // Update tabs under the moved path in the mutated workspace only.
        for (const tab of tabs) {
          if (
            docWorkspaceMatches(tab.workspace, mutationWorkspace) &&
            docPathMatches(tab.docPath, oldPath)
          ) {
            const updatedPath = newPath + tab.docPath.slice(oldPath.length);
            updateTab(tab.id, {
              docPath: updatedPath,
              title: titleFromPath(updatedPath),
            });
          }
        }
        showToast(`Document ${action}`);
      } catch {
        showToast(
          `Failed to ${action === 'renamed' ? 'rename' : 'move'} document`
        );
        mutate();
      }
    },
    [
      canWrite,
      client,
      remoteNode,
      mutate,
      tabs,
      updateTab,
      showToast,
    ]
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
      const mutationQuery = workspaceTargetQueryForWorkspace(deleteWorkspace);
      const { error } = await client.DELETE('/docs/doc', {
        params: {
          query: { remoteNode, path: deleteDocPath, ...mutationQuery },
        },
      });
      if (error) {
        showToast('Failed to delete document');
        return;
      }
      mutate();
      // Close tabs for deleted path (exact match + prefix for directories)
      for (const tab of tabs) {
        if (
          docWorkspaceMatches(tab.workspace, deleteWorkspace) &&
          docPathMatches(tab.docPath, deleteDocPath)
        ) {
          clearDraft(tab.id);
          markTabSaved(tab.id);
          closeTab(tab.id);
        }
      }
      showToast('Document deleted');
    } catch {
      showToast('Failed to delete document');
    } finally {
      setDeleteConfirmOpen(false);
    }
  }, [
    client,
    canWrite,
    remoteNode,
    deleteDocPath,
    deleteWorkspace,
    mutate,
    tabs,
    closeTab,
    clearDraft,
    markTabSaved,
    showToast,
  ]);

  // Batch delete handler
  const handleBatchDelete = useCallback(async () => {
    if (!canWrite) {
      showToast('You do not have permission to delete documents');
      setBatchDeleteConfirmOpen(false);
      setBatchDeleteTargets([]);
      return;
    }
    try {
      const grouped = new Map<string, DocMutationTarget[]>();
      for (const target of batchDeleteTargets) {
        const workspace = normalizedDocWorkspace(target.workspace);
        const key = workspace ?? '';
        grouped.set(key, [...(grouped.get(key) ?? []), target]);
      }

      let deletedCount = 0;
      let failedCount = 0;
      const deletedByWorkspace = new Map<string, Set<string>>();

      for (const [workspaceKey, targets] of grouped.entries()) {
        const workspace = workspaceKey || null;
        const mutationQuery = workspaceTargetQueryForWorkspace(workspace);
        const { data, error } = await client.POST('/docs/delete-batch', {
          params: { query: { remoteNode, ...mutationQuery } },
          body: { paths: targets.map((target) => target.path) },
        });
        if (error) {
          failedCount += targets.length;
          continue;
        }
        deletedCount += data.deleted.length;
        failedCount += data.failed?.length || 0;
        const previousDeleted = Array.from(
          deletedByWorkspace.get(workspaceKey) ?? []
        );
        deletedByWorkspace.set(
          workspaceKey,
          new Set([...previousDeleted, ...data.deleted])
        );
      }
      mutate();

      for (const tab of tabs) {
        const workspaceKey = normalizedDocWorkspace(tab.workspace) ?? '';
        const deletedSet = deletedByWorkspace.get(workspaceKey);
        if (!deletedSet) continue;
        const shouldClose =
          deletedSet.has(tab.docPath) ||
          [...deletedSet].some((dp) => tab.docPath.startsWith(dp + '/'));
        if (shouldClose) {
          clearDraft(tab.id);
          markTabSaved(tab.id);
          closeTab(tab.id);
        }
      }
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
  }, [
    batchDeleteTargets,
    canWrite,
    client,
    remoteNode,
    mutate,
    tabs,
    closeTab,
    clearDraft,
    markTabSaved,
    showToast,
  ]);

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
      onCreateNew={() => {
        setCreateParentDir('');
        setCreateWorkspace(null);
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
              Documents
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

        {/* Modals */}
        <CreateDocModal
          isOpen={createModalOpen}
          onClose={() => setCreateModalOpen(false)}
          onSubmit={handleCreate}
          parentDir={createParentDir}
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
          title="Delete Documents"
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

      {/* Modals */}
      <CreateDocModal
        isOpen={createModalOpen}
        onClose={() => setCreateModalOpen(false)}
        onSubmit={handleCreate}
        parentDir={createParentDir}
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
        title="Delete Documents"
        buttonText={`Delete ${batchDeleteTargets.length} items`}
        visible={batchDeleteConfirmOpen}
        dismissModal={() => setBatchDeleteConfirmOpen(false)}
        onSubmit={handleBatchDelete}
      >
        <p className="text-sm text-muted-foreground">
          Are you sure you want to delete {batchDeleteTargets.length} items? This
          cannot be undone.
        </p>
      </ConfirmModal>
    </div>
  );
}

function DocsPage() {
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const docTabStorageKey = `dagu_doc_tabs:${JSON.stringify({
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
