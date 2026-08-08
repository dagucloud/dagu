// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, DocTreeNodeResponseType } from '@/api/v1/schema';
import { useCanWrite } from '@/contexts/AuthContext';
import { useDocTabContext } from '@/contexts/DocTabContext';
import { useClient } from '@/hooks/api';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import type { DocSortField, DocSortOrder } from '@/contexts/UserPreference';
import {
  AlertCircle,
  ArrowUpDown,
  ChevronsDownUp,
  ChevronsUpDown,
  FileText,
  FilePlus,
  Loader2,
  RefreshCw,
  Search,
  Tag,
  Trash2,
  X,
} from 'lucide-react';
import React, {
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { Tree, TreeApi, NodeApi } from 'react-arborist';
import DocArboristNode, { type ContextAction } from './DocArboristNode';
import DocBacklinksPanel from './DocBacklinksPanel';
import DocOutlinePanel from './DocOutlinePanel';
import {
  workspaceNameForSelection,
  workspaceSelectionQuery,
} from '@/lib/workspace';
import {
  docMutationTargetForTreeNode,
  isWorkspaceRootTreeNode,
  resolveDocTreeMove,
  type DocMutationTarget,
} from '../lib/doc-mutation';

type DocTreeNodeResponse = components['schemas']['DocTreeNodeResponse'];

type Props = {
  tree: DocTreeNodeResponse[] | undefined;
  isLoading?: boolean;
  error?: unknown;
  onRetry?: () => void;
  onContextAction: (action: ContextAction) => void;
  canCreateNew?: boolean;
  onCreateNew: () => void;
  onSelectFile: (
    docPath: string,
    title: string,
    workspace?: string | null
  ) => void;
  onRename: (
    oldPath: string,
    newPath: string,
    workspace?: string | null
  ) => Promise<void>;
  onMove: (
    oldPath: string,
    newPath: string,
    workspace?: string | null
  ) => Promise<void>;
  onBatchDelete: (targets: DocMutationTarget[]) => void;
  onSelectionChange?: (ids: string[]) => void;
  activeDocContent?: string | null;
  onHeadingClick?: (anchor: string) => void;
  sortField: DocSortField;
  sortOrder: DocSortOrder;
  onSortChange: (field: DocSortField, order: DocSortOrder) => void;
};

function buildWorkspaceById(
  nodes: DocTreeNodeResponse[] | undefined
): Map<string, string | null> {
  const byId = new Map<string, string | null>();
  const walk = (node: DocTreeNodeResponse) => {
    byId.set(node.id, node.workspace ?? null);
    node.children?.forEach(walk);
  };
  nodes?.forEach(walk);
  return byId;
}

function collectAncestors(path: string): string[] {
  const parts = path.split('/');
  const ancestors: string[] = [];
  for (let i = 1; i < parts.length; i++) {
    ancestors.push(parts.slice(0, i).join('/'));
  }
  return ancestors;
}

// Collect all node IDs in the tree (for expand all)
function collectAllIds(nodes: DocTreeNodeResponse[]): string[] {
  const ids: string[] = [];
  function walk(node: DocTreeNodeResponse) {
    if (node.type === DocTreeNodeResponseType.directory) {
      ids.push(node.id);
    }
    node.children?.forEach(walk);
  }
  nodes.forEach(walk);
  return ids;
}

function collectNodeIds(nodes: DocTreeNodeResponse[] | undefined): Set<string> {
  const ids = new Set<string>();
  const walk = (node: DocTreeNodeResponse) => {
    ids.add(node.id);
    node.children?.forEach(walk);
  };
  nodes?.forEach(walk);
  return ids;
}

function resolveTreeNodeId(
  docPath: string | null,
  workspace: string | null | undefined,
  nodeIds: Set<string>
): string | null {
  if (!docPath) return null;
  const candidates = workspace
    ? [`${workspace}/${docPath}`, docPath]
    : [docPath];
  return candidates.find((id) => nodeIds.has(id)) ?? docPath;
}

// Collect all ancestor paths of matching IDs (for search filtering)
function collectAncestorPaths(matchIds: Set<string>): Set<string> {
  const ancestors = new Set<string>();
  for (const id of matchIds) {
    const parts = id.split('/');
    for (let i = 1; i < parts.length; i++) {
      ancestors.add(parts.slice(0, i).join('/'));
    }
  }
  return ancestors;
}

// Filter tree to only include matching nodes and their ancestors
function filterTree(
  nodes: DocTreeNodeResponse[],
  matchIds: Set<string>,
  ancestorIds: Set<string>
): DocTreeNodeResponse[] {
  return nodes
    .filter((node) => matchIds.has(node.id) || ancestorIds.has(node.id))
    .map((node) => {
      if (!node.children) return node;
      const filteredChildren = filterTree(node.children, matchIds, ancestorIds);
      return {
        ...node,
        children: filteredChildren.length > 0 ? filteredChildren : undefined,
      };
    })
    .filter(
      (node) =>
        node.type !== DocTreeNodeResponseType.directory ||
        matchIds.has(node.id) ||
        !!(node.children && node.children.length > 0)
    );
}

// Collect the distinct tags present on file nodes, first casing wins.
function collectTagVocabulary(
  nodes: DocTreeNodeResponse[] | undefined
): string[] {
  const byKey = new Map<string, string>();
  const walk = (node: DocTreeNodeResponse) => {
    node.tags?.forEach((tag) => {
      const key = tag.toLowerCase();
      if (!byKey.has(key)) byKey.set(key, tag);
    });
    node.children?.forEach(walk);
  };
  nodes?.forEach(walk);
  return [...byKey.values()].sort((a, b) =>
    a.toLowerCase().localeCompare(b.toLowerCase())
  );
}

// Collect IDs of file nodes carrying every selected tag (case-insensitive).
function collectTagMatchIds(
  nodes: DocTreeNodeResponse[],
  selectedTags: string[]
): Set<string> {
  const ids = new Set<string>();
  const walk = (node: DocTreeNodeResponse) => {
    if (node.type === DocTreeNodeResponseType.file) {
      const nodeTags = (node.tags ?? []).map((t) => t.toLowerCase());
      if (selectedTags.every((t) => nodeTags.includes(t))) {
        ids.add(node.id);
      }
    }
    node.children?.forEach(walk);
  };
  nodes.forEach(walk);
  return ids;
}

const SKELETON_WIDTHS = [75, 60, 85, 65, 90, 70];

function DocTreeSidebar({
  tree,
  isLoading,
  error,
  onRetry,
  onContextAction,
  canCreateNew = true,
  onCreateNew,
  onSelectFile,
  onRename,
  onMove,
  onBatchDelete,
  onSelectionChange,
  activeDocContent,
  onHeadingClick,
  sortField,
  sortOrder,
  onSortChange,
}: Props) {
  const canWrite = useCanWrite();
  const canEdit = canWrite;
  const client = useClient();
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const workspaceQuery = useMemo(
    () => workspaceSelectionQuery(appBarContext.workspaceSelection),
    [appBarContext.workspaceSelection]
  );
  const selectedWorkspace = workspaceNameForSelection(
    appBarContext.workspaceSelection
  );
  const { activeTabId, tabs } = useDocTabContext();
  const activeTab = activeTabId
    ? tabs.find((t) => t.id === activeTabId) || null
    : null;
  const activeDocPath = activeTab?.docPath || null;
  const activeDocWorkspace = activeTab?.workspace ?? null;
  const treeNodeIds = useMemo(() => collectNodeIds(tree), [tree]);
  const activeTreeNodeId = useMemo(
    () => resolveTreeNodeId(activeDocPath, activeDocWorkspace, treeNodeIds),
    [activeDocPath, activeDocWorkspace, treeNodeIds]
  );

  const treeRef = useRef<TreeApi<DocTreeNodeResponse>>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const [containerHeight, setContainerHeight] = useState(400);

  // Selection state for multi-select
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const workspaceById = useMemo(() => buildWorkspaceById(tree), [tree]);
  const selectedTargets = useMemo(
    () =>
      selectedIds
        .filter((id) => !isWorkspaceRootTreeNode(id, workspaceById.get(id)))
        .map((id) => docMutationTargetForTreeNode(id, workspaceById.get(id))),
    [selectedIds, workspaceById]
  );

  // Search state; results arrive relevance-ranked from the server.
  type RankedSearchItem = {
    id: string;
    title: string;
    workspace: string | null;
    matchCount: number;
  };
  const [searchQuery, setSearchQuery] = useState('');
  const [searchResults, setSearchResults] = useState<RankedSearchItem[] | null>(
    null
  );
  const [isSearching, setIsSearching] = useState(false);
  const [searchError, setSearchError] = useState<string | null>(null);
  const searchTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Tag filter state (lowercased tag keys)
  const [selectedTags, setSelectedTags] = useState<string[]>([]);
  const tagVocabulary = useMemo(() => collectTagVocabulary(tree), [tree]);
  // Drop selections for tags that no longer exist in the tree.
  useEffect(() => {
    if (selectedTags.length === 0) return;
    const known = new Set(tagVocabulary.map((t) => t.toLowerCase()));
    if (selectedTags.some((t) => !known.has(t))) {
      setSelectedTags((prev) => prev.filter((t) => known.has(t)));
    }
  }, [tagVocabulary, selectedTags]);

  // Measure container height for react-arborist
  useEffect(() => {
    if (!containerRef.current) return;
    const observer = new ResizeObserver((entries) => {
      for (const entry of entries) {
        setContainerHeight(entry.contentRect.height);
      }
    });
    observer.observe(containerRef.current);
    return () => observer.disconnect();
  }, []);

  // Auto-reveal active doc node (once per activeDocPath change)
  const hasRevealedRef = useRef<string | null>(null);
  useEffect(() => {
    if (!activeTreeNodeId || !treeRef.current || !tree) return;
    if (hasRevealedRef.current === activeTreeNodeId) return;
    // Small delay to ensure tree has rendered
    const timer = setTimeout(() => {
      const api = treeRef.current;
      if (!api) return;
      // Open ancestors
      const ancestors = collectAncestors(activeTreeNodeId);
      for (const a of ancestors) {
        const n = api.get(a);
        if (n && !n.isOpen) n.open();
      }
      // Scroll to active node (do NOT call select() — it accumulates multi-selections)
      api.scrollTo(activeTreeNodeId);
      hasRevealedRef.current = activeTreeNodeId;
    }, 50);
    return () => clearTimeout(timer);
  }, [activeTreeNodeId, tree]);

  // Debounced search
  useEffect(() => {
    let cancelled = false;

    if (searchTimerRef.current) {
      clearTimeout(searchTimerRef.current);
    }

    if (searchQuery.length < 2) {
      setSearchResults(null);
      setSearchError(null);
      setIsSearching(false);
      return;
    }

    setIsSearching(true);
    searchTimerRef.current = setTimeout(async () => {
      try {
        setSearchError(null);
        const { data, error } = await client.GET('/docs/search', {
          params: { query: { remoteNode, q: searchQuery, ...workspaceQuery } },
        });
        if (cancelled) return;
        if (error) {
          setSearchResults(null);
          setSearchError(error.message || 'Search failed');
          return;
        }
        setSearchResults(
          data?.results?.map((r) => ({
            id: r.id,
            title: r.title,
            workspace: r.workspace ?? null,
            matchCount: r.matchCount ?? 0,
          })) ?? []
        );
      } catch {
        if (!cancelled) {
          setSearchResults(null);
          setSearchError('Search failed');
        }
      } finally {
        if (!cancelled) setIsSearching(false);
      }
    }, 300);

    return () => {
      cancelled = true;
      if (searchTimerRef.current) clearTimeout(searchTimerRef.current);
    };
  }, [searchQuery, client, remoteNode, workspaceQuery]);

  // Compute filtered tree data (text search and tag filter intersect)
  const filterMatchIds = useMemo(() => {
    if (!tree) return null;
    if (!searchResults && selectedTags.length === 0) return null;

    let matchIds: Set<string> | null = searchResults
      ? new Set(searchResults.map((r) => r.id))
      : null;
    if (selectedTags.length > 0) {
      const tagIds = collectTagMatchIds(tree, selectedTags);
      matchIds = matchIds
        ? new Set([...matchIds].filter((id) => tagIds.has(id)))
        : tagIds;
    }
    return matchIds;
  }, [tree, searchResults, selectedTags]);

  const treeData = useMemo(() => {
    if (!tree) return [];
    if (!filterMatchIds) return tree;

    const ancestorIds = collectAncestorPaths(filterMatchIds);
    return filterTree(tree, filterMatchIds, ancestorIds);
  }, [tree, filterMatchIds]);

  // While a text query is active, show the server-ranked flat result list
  // instead of the filtered tree. An active tag filter narrows the list.
  const rankedResults = useMemo(() => {
    if (!searchResults || searchQuery.length < 2) return null;
    if (selectedTags.length === 0 || !tree) return searchResults;
    const tagIds = collectTagMatchIds(tree, selectedTags);
    return searchResults.filter((r) => tagIds.has(r.id));
  }, [searchResults, searchQuery, selectedTags, tree]);

  useEffect(() => {
    if (!filterMatchIds || !treeRef.current) return;
    const api = treeRef.current;
    const ancestorIds = collectAncestorPaths(filterMatchIds);
    for (const id of ancestorIds) {
      const node = api.get(id);
      if (node && !node.isOpen) node.open();
    }
  }, [filterMatchIds, treeData]);

  // Compute initial open state: expand ancestors of active doc
  const initialOpenState = useMemo(() => {
    const state: Record<string, boolean> = {};
    if (activeTreeNodeId) {
      const ancestors = collectAncestors(activeTreeNodeId);
      for (const a of ancestors) {
        state[a] = true;
      }
    }
    // When searching, expand everything to show results
    if (searchResults && tree) {
      const allIds = collectAllIds(tree);
      for (const id of allIds) {
        state[id] = true;
      }
    }
    return state;
  }, [activeTreeNodeId, searchResults, tree]);

  // Expand all
  const handleExpandAll = useCallback(() => {
    treeRef.current?.openAll();
  }, []);

  // Collapse all
  const handleCollapseAll = useCallback(() => {
    treeRef.current?.closeAll();
  }, []);

  // Track selection changes
  const handleSelect = useCallback(
    (nodes: NodeApi<DocTreeNodeResponse>[]) => {
      const ids = nodes.map((n) => n.id);
      setSelectedIds(ids);
      onSelectionChange?.(ids);
    },
    [onSelectionChange]
  );

  // Handle node activation (file click)
  const handleActivate = useCallback(
    (node: NodeApi<DocTreeNodeResponse>) => {
      if (node.data.type !== DocTreeNodeResponseType.directory) {
        const displayTitle = node.data.title || node.data.name;
        onSelectFile(node.id, displayTitle, node.data.workspace ?? null);
      }
    },
    [onSelectFile]
  );

  // Handle inline rename
  const handleRename = useCallback(
    async ({
      id,
      name,
      node,
    }: {
      id: string;
      name: string;
      node: NodeApi<DocTreeNodeResponse>;
    }) => {
      const parts = id.split('/');
      parts[parts.length - 1] = name;
      const newTreePath = parts.join('/');
      if (newTreePath !== id) {
        const workspace = node.data.workspace ?? null;
        const oldTarget = docMutationTargetForTreeNode(id, workspace);
        const newTarget = docMutationTargetForTreeNode(newTreePath, workspace);
        if (oldTarget.path && newTarget.path) {
          await onRename(oldTarget.path, newTarget.path, oldTarget.workspace);
        }
      }
    },
    [onRename]
  );

  // Handle drag-and-drop move
  const handleMove = useCallback(
    async ({
      dragIds,
      parentId,
      dragNodes,
      parentNode,
    }: {
      dragIds: string[];
      dragNodes: NodeApi<DocTreeNodeResponse>[];
      parentId: string | null;
      parentNode: NodeApi<DocTreeNodeResponse> | null;
      index: number;
    }) => {
      for (const [idx, dragId] of dragIds.entries()) {
        const dragNode = dragNodes[idx];
        const resolved = resolveDocTreeMove({
          dragId,
          dragWorkspace: dragNode?.data.workspace ?? null,
          parentId,
          parentWorkspace: parentNode?.data.workspace ?? null,
          rootWorkspace: selectedWorkspace,
        });
        if (resolved) {
          await onMove(resolved.oldPath, resolved.newPath, resolved.workspace);
        }
      }
    },
    [onMove, selectedWorkspace]
  );

  // Disable drop on files and prevent dropping into own subtree
  const disableDrop = useCallback(
    ({
      parentNode,
      dragNodes,
    }: {
      parentNode: NodeApi<DocTreeNodeResponse>;
      dragNodes: NodeApi<DocTreeNodeResponse>[];
      index: number;
    }) => {
      // Cannot drop on a file node
      if (parentNode?.isLeaf) return true;
      // Cannot drop into own subtree
      for (const dn of dragNodes) {
        if (parentNode && dn.isAncestorOf(parentNode)) return true;
        if (parentNode && dn.id === parentNode.id) return true;
        const resolved = resolveDocTreeMove({
          dragId: dn.id,
          dragWorkspace: dn.data.workspace ?? null,
          parentId: parentNode?.id ?? null,
          parentWorkspace: parentNode?.data.workspace ?? null,
          rootWorkspace: selectedWorkspace,
        });
        if (!resolved) return true;
      }
      return false;
    },
    [selectedWorkspace]
  );

  // Disable drag when user has no write permission
  const disableDrag = useCallback(() => {
    return !canEdit;
  }, [canEdit]);

  // Keyboard shortcuts: Delete and F2
  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (!canEdit) return;
      const api = treeRef.current;
      if (!api) return;

      if (e.key === 'Delete' || e.key === 'Backspace') {
        if (selectedIds.length > 1) {
          if (selectedTargets.length === 0) return;
          e.preventDefault();
          onBatchDelete(selectedTargets);
        } else if (selectedIds.length === 1) {
          const node = api.get(selectedIds[0] ?? null);
          if (node && !node.isEditing) {
            if (isWorkspaceRootTreeNode(node.id, node.data.workspace ?? null)) {
              return;
            }
            e.preventDefault();
            const isDir = node.data.type === DocTreeNodeResponseType.directory;
            const hasChildren = !!(
              node.data.children && node.data.children.length > 0
            );
            const target = docMutationTargetForTreeNode(
              node.id,
              node.data.workspace ?? null
            );
            onContextAction({
              type: 'delete',
              docPath: target.path,
              title: node.data.title || node.data.name,
              isDir,
              hasChildren,
              workspace: target.workspace,
            });
          }
        }
      } else if (e.key === 'F2') {
        if (selectedIds.length === 1) {
          const node = api.get(selectedIds[0] ?? null);
          if (
            node &&
            !node.isEditing &&
            !isWorkspaceRootTreeNode(node.id, node.data.workspace ?? null)
          ) {
            e.preventDefault();
            node.edit();
          }
        }
      }
    },
    [canEdit, selectedIds, selectedTargets, onBatchDelete, onContextAction]
  );

  const hasDocuments = treeData && treeData.length > 0;

  // Custom node renderer that passes through extra props
  const renderNode = useCallback(
    (
      props: import('react-arborist').NodeRendererProps<DocTreeNodeResponse>
    ) => (
      <DocArboristNode
        {...props}
        onContextAction={onContextAction}
        canWrite={canEdit}
        activeDocPath={activeDocPath}
        activeTreeNodeId={activeTreeNodeId}
        selectedIds={selectedIds}
        selectedTargets={selectedTargets}
      />
    ),
    [
      onContextAction,
      canEdit,
      activeDocPath,
      activeTreeNodeId,
      selectedIds,
      selectedTargets,
    ]
  );

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div className="flex items-center justify-between px-3 py-2 border-b border-border">
        <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
          Docs
        </span>
        <div className="flex items-center gap-0.5">
          <button
            type="button"
            onClick={handleExpandAll}
            className="p-1 rounded-sm hover:bg-accent text-muted-foreground hover:text-foreground"
            title="Expand All"
          >
            <ChevronsUpDown className="h-3.5 w-3.5" />
          </button>
          <button
            type="button"
            onClick={handleCollapseAll}
            className="p-1 rounded-sm hover:bg-accent text-muted-foreground hover:text-foreground"
            title="Collapse All"
          >
            <ChevronsDownUp className="h-3.5 w-3.5" />
          </button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                className="p-1 rounded-sm hover:bg-accent text-muted-foreground hover:text-foreground"
                title="Sort"
              >
                <ArrowUpDown className="h-3.5 w-3.5" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-44">
              <DropdownMenuLabel className="text-xs py-1">
                Sort by
              </DropdownMenuLabel>
              <DropdownMenuSeparator />
              <DropdownMenuRadioGroup
                value={`${sortField}:${sortOrder}`}
                onValueChange={(v) => {
                  const [f, o] = v.split(':') as [DocSortField, DocSortOrder];
                  onSortChange(f, o);
                }}
              >
                <DropdownMenuRadioItem value="name:asc" className="text-xs">
                  Name A–Z
                </DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="name:desc" className="text-xs">
                  Name Z–A
                </DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="type:asc" className="text-xs">
                  Folders first
                </DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="type:desc" className="text-xs">
                  Files first
                </DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="mtime:desc" className="text-xs">
                  Newest first
                </DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="mtime:asc" className="text-xs">
                  Oldest first
                </DropdownMenuRadioItem>
              </DropdownMenuRadioGroup>
            </DropdownMenuContent>
          </DropdownMenu>
          {tagVocabulary.length > 0 && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button
                  type="button"
                  className={`p-1 rounded-sm hover:bg-accent hover:text-foreground ${
                    selectedTags.length > 0
                      ? 'text-primary'
                      : 'text-muted-foreground'
                  }`}
                  title="Filter by tags"
                >
                  <Tag className="h-3.5 w-3.5" />
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-44">
                <DropdownMenuLabel className="text-xs py-1">
                  Filter by tags
                </DropdownMenuLabel>
                <DropdownMenuSeparator />
                {tagVocabulary.map((tag) => {
                  const key = tag.toLowerCase();
                  return (
                    <DropdownMenuCheckboxItem
                      key={key}
                      className="text-xs"
                      checked={selectedTags.includes(key)}
                      onCheckedChange={(checked) => {
                        setSelectedTags((prev) =>
                          checked
                            ? [...prev, key]
                            : prev.filter((t) => t !== key)
                        );
                      }}
                    >
                      {tag}
                    </DropdownMenuCheckboxItem>
                  );
                })}
                {selectedTags.length > 0 && (
                  <>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem
                      className="text-xs text-muted-foreground"
                      onSelect={() => setSelectedTags([])}
                    >
                      Clear filter
                    </DropdownMenuItem>
                  </>
                )}
              </DropdownMenuContent>
            </DropdownMenu>
          )}
          {canEdit && canCreateNew && (
            <button
              type="button"
              onClick={onCreateNew}
              className="p-1 rounded-sm hover:bg-accent text-muted-foreground hover:text-foreground"
              title="New Document"
            >
              <FilePlus className="h-4 w-4" />
            </button>
          )}
        </div>
      </div>

      {/* Search / Selection bar (selection replaces search when active) */}
      <div className="px-2 py-1.5 border-b border-border relative">
        {/* Search input — always rendered to define the container height */}
        <div
          className={
            selectedIds.length > 1 && canEdit ? 'invisible' : undefined
          }
        >
          <div className="relative">
            <Search className="absolute left-2 top-1/2 -translate-y-1/2 h-3 w-3 text-muted-foreground" />
            <input
              type="text"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              placeholder="Search docs..."
              className="w-full text-xs bg-muted/50 border border-border rounded px-2 py-1 pl-6 pr-6 outline-none focus:ring-1 focus:ring-primary placeholder:text-muted-foreground/60"
              tabIndex={selectedIds.length > 1 && canEdit ? -1 : undefined}
            />
            {searchQuery && (
              <button
                type="button"
                onClick={() => setSearchQuery('')}
                className="absolute right-1.5 top-1/2 -translate-y-1/2 p-0.5 rounded-sm hover:bg-accent text-muted-foreground"
              >
                <X className="h-3 w-3" />
              </button>
            )}
            {isSearching && (
              <Loader2 className="absolute right-1.5 top-1/2 -translate-y-1/2 h-3 w-3 text-muted-foreground animate-spin" />
            )}
          </div>
          {searchError && searchQuery.length >= 2 && (
            <div className="text-[10px] text-destructive mt-0.5 px-1">
              {searchError}
            </div>
          )}
          {searchResults !== null &&
            searchQuery.length >= 2 &&
            !searchError && (
              <div className="text-[10px] text-muted-foreground mt-0.5 px-1">
                {searchResults.length} result
                {searchResults.length !== 1 ? 's' : ''}
              </div>
            )}
        </div>
        {/* Selection bar — overlaid on top when multi-select is active */}
        {selectedIds.length > 1 && canEdit && (
          <div className="absolute inset-0 flex items-center justify-between px-3">
            <span className="text-xs text-muted-foreground">
              {selectedIds.length} selected
            </span>
            <div className="flex items-center gap-1">
              <button
                type="button"
                onClick={() => onBatchDelete(selectedTargets)}
                disabled={selectedTargets.length === 0}
                className="flex items-center gap-0.5 text-xs text-destructive hover:text-destructive/80 px-1 py-0.5 rounded-sm hover:bg-destructive/10"
              >
                <Trash2 className="h-3 w-3" /> Delete {selectedTargets.length}
              </button>
              <button
                type="button"
                onClick={() => treeRef.current?.deselectAll()}
                className="p-0.5 rounded-sm hover:bg-accent text-muted-foreground"
                title="Clear selection"
              >
                <X className="h-3 w-3" />
              </button>
            </div>
          </div>
        )}
      </div>

      {/* Tree */}
      <div
        ref={containerRef}
        className="flex-1 overflow-hidden min-h-0 outline-none"
        onKeyDown={handleKeyDown}
        tabIndex={-1}
      >
        {error && !tree ? (
          <div className="flex flex-col items-center justify-center h-full gap-2 p-4 text-center">
            <AlertCircle className="h-6 w-6 text-destructive/60" />
            <p className="text-xs text-muted-foreground">
              Failed to load documents
            </p>
            {onRetry && (
              <button
                type="button"
                onClick={onRetry}
                className="flex items-center gap-1 text-xs text-primary hover:underline"
              >
                <RefreshCw className="h-3 w-3" />
                Retry
              </button>
            )}
          </div>
        ) : isLoading && !tree ? (
          <div className="p-3 space-y-2">
            {Array.from({ length: 6 }).map((_, i) => (
              <div
                key={i}
                className="h-5 rounded bg-muted/60 animate-pulse"
                style={{
                  width: `${SKELETON_WIDTHS[i]}%`,
                  marginLeft: `${(i % 3) * 12}px`,
                }}
              />
            ))}
          </div>
        ) : hasDocuments ? (
          <>
            {error && onRetry && (
              <div className="flex items-center justify-between px-3 py-1 bg-destructive/10 border-b border-border">
                <span className="text-xs text-destructive">Refresh failed</span>
                <button
                  type="button"
                  onClick={onRetry}
                  className="flex items-center gap-1 text-xs text-primary hover:underline"
                >
                  <RefreshCw className="h-3 w-3" />
                  Retry
                </button>
              </div>
            )}
            {rankedResults ? (
              <div
                className="overflow-y-auto"
                style={{ height: containerHeight }}
              >
                {rankedResults.length === 0 && (
                  <div className="px-3 py-2 text-xs text-muted-foreground">
                    No matching documents
                  </div>
                )}
                {rankedResults.map((item) => (
                  <button
                    key={`${item.workspace ?? ''}/${item.id}`}
                    type="button"
                    onClick={() =>
                      onSelectFile(item.id, item.title, item.workspace)
                    }
                    className="w-full text-left px-3 py-1 hover:bg-accent"
                    title={item.id}
                  >
                    <div className="flex items-center gap-1.5">
                      <span className="text-xs truncate">{item.title}</span>
                      {item.matchCount > 0 && (
                        <span className="ml-auto shrink-0 px-1 text-[10px] leading-4 rounded bg-muted text-muted-foreground">
                          {item.matchCount}
                        </span>
                      )}
                    </div>
                    <div className="text-[10px] text-muted-foreground truncate">
                      {item.id}
                    </div>
                  </button>
                ))}
              </div>
            ) : (
              <Tree<DocTreeNodeResponse>
                ref={treeRef}
                data={treeData}
                width="100%"
                height={
                  error && onRetry ? containerHeight - 28 : containerHeight
                }
                indent={16}
                rowHeight={28}
                openByDefault={false}
                initialOpenState={initialOpenState}
                disableEdit={!canEdit}
                disableDrag={disableDrag}
                disableDrop={disableDrop}
                onActivate={handleActivate}
                onSelect={handleSelect}
                onRename={handleRename}
                onMove={handleMove}
                idAccessor="id"
                childrenAccessor={(d) => d.children ?? null}
              >
                {renderNode}
              </Tree>
            )}
          </>
        ) : (
          <div className="flex flex-col items-center justify-center h-full gap-3 p-4 text-center">
            {searchResults !== null && searchQuery.length >= 2 ? (
              <>
                <Search className="h-8 w-8 text-muted-foreground/50" />
                <p className="text-sm text-muted-foreground">
                  No matching documents
                </p>
              </>
            ) : (
              <>
                <FileText className="h-8 w-8 text-muted-foreground/50" />
                <p className="text-sm text-muted-foreground">
                  No documents yet.
                </p>
                {canEdit && canCreateNew && (
                  <button
                    type="button"
                    onClick={onCreateNew}
                    className="text-sm text-primary hover:underline"
                  >
                    Create your first document
                  </button>
                )}
              </>
            )}
          </div>
        )}
      </div>

      {/* Outline panel */}
      {onHeadingClick && (
        <DocOutlinePanel
          markdown={activeDocContent}
          onHeadingClick={onHeadingClick}
        />
      )}

      {/* Backlinks panel */}
      <DocBacklinksPanel
        docPath={activeDocPath}
        workspace={activeDocWorkspace}
        onSelectDoc={onSelectFile}
      />
    </div>
  );
}

export default DocTreeSidebar;
