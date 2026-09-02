// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, WikiPageTreeNodeResponseType } from '@/api/v1/schema';
import { useCanWrite } from '@/contexts/AuthContext';
import { useWikiPageTabContext } from '@/contexts/WikiPageTabContext';
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
import type { WikiSortField, WikiSortOrder } from '@/contexts/UserPreference';
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
import WikiPageTreeNode, { type ContextAction } from './WikiPageTreeNode';
import WikiPageBacklinksPanel from './WikiPageBacklinksPanel';
import WikiPageOutlinePanel from './WikiPageOutlinePanel';
import {
  workspaceNameForSelection,
  workspaceSelectionQuery,
} from '@/lib/workspace';
import {
  wikiPageMutationTargetForTreeNode,
  isWorkspaceRootTreeNode,
  resolveWikiTreeMove,
  type WikiPageMutationTarget,
} from '../lib/wiki-page-mutation';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

type WikiPageTreeNodeResponse =
  components['schemas']['WikiPageTreeNodeResponse'];

type Props = {
  tree: WikiPageTreeNodeResponse[] | undefined;
  isLoading?: boolean;
  error?: unknown;
  onRetry?: () => void;
  onContextAction: (action: ContextAction) => void;
  canCreateNew?: boolean;
  onCreateNew: () => void;
  onSelectFile: (
    wikiPagePath: string,
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
  onBatchDelete: (targets: WikiPageMutationTarget[]) => void;
  onSelectionChange?: (ids: string[]) => void;
  activeWikiPageContent?: string | null;
  onHeadingClick?: (anchor: string) => void;
  sortField: WikiSortField;
  sortOrder: WikiSortOrder;
  onSortChange: (field: WikiSortField, order: WikiSortOrder) => void;
};

function buildWorkspaceById(
  nodes: WikiPageTreeNodeResponse[] | undefined
): Map<string, string | null> {
  const byId = new Map<string, string | null>();
  const walk = (node: WikiPageTreeNodeResponse) => {
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
function collectAllIds(nodes: WikiPageTreeNodeResponse[]): string[] {
  const ids: string[] = [];
  function walk(node: WikiPageTreeNodeResponse) {
    if (node.type === WikiPageTreeNodeResponseType.directory) {
      ids.push(node.id);
    }
    node.children?.forEach(walk);
  }
  nodes.forEach(walk);
  return ids;
}

function collectNodeIds(
  nodes: WikiPageTreeNodeResponse[] | undefined
): Set<string> {
  const ids = new Set<string>();
  const walk = (node: WikiPageTreeNodeResponse) => {
    ids.add(node.id);
    node.children?.forEach(walk);
  };
  nodes?.forEach(walk);
  return ids;
}

function resolveTreeNodeId(
  wikiPagePath: string | null,
  workspace: string | null | undefined,
  nodeIds: Set<string>
): string | null {
  if (!wikiPagePath) return null;
  const candidates = workspace
    ? [`${workspace}/${wikiPagePath}`, wikiPagePath]
    : [wikiPagePath];
  return candidates.find((id) => nodeIds.has(id)) ?? wikiPagePath;
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
  nodes: WikiPageTreeNodeResponse[],
  matchIds: Set<string>,
  ancestorIds: Set<string>
): WikiPageTreeNodeResponse[] {
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
        node.type !== WikiPageTreeNodeResponseType.directory ||
        matchIds.has(node.id) ||
        !!(node.children && node.children.length > 0)
    );
}

// Collect the distinct tags present on file nodes, first casing wins.
function collectTagVocabulary(
  nodes: WikiPageTreeNodeResponse[] | undefined
): string[] {
  const byKey = new Map<string, string>();
  const walk = (node: WikiPageTreeNodeResponse) => {
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
  nodes: WikiPageTreeNodeResponse[],
  selectedTags: string[]
): Set<string> {
  const ids = new Set<string>();
  const walk = (node: WikiPageTreeNodeResponse) => {
    if (node.type === WikiPageTreeNodeResponseType.file) {
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

function WikiTreeSidebar({
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
  activeWikiPageContent,
  onHeadingClick,
  sortField,
  sortOrder,
  onSortChange,
}: Props) {
  const { ts } = useI18n();
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
  const { activeTabId, tabs } = useWikiPageTabContext();
  const activeTab = activeTabId
    ? tabs.find((t) => t.id === activeTabId) || null
    : null;
  const activeWikiPagePath = activeTab?.wikiPagePath || null;
  const activeWikiPageWorkspace = activeTab?.workspace ?? null;
  const treeNodeIds = useMemo(() => collectNodeIds(tree), [tree]);
  const activeTreeNodeId = useMemo(
    () =>
      resolveTreeNodeId(
        activeWikiPagePath,
        activeWikiPageWorkspace,
        treeNodeIds
      ),
    [activeWikiPagePath, activeWikiPageWorkspace, treeNodeIds]
  );

  const treeRef = useRef<TreeApi<WikiPageTreeNodeResponse>>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const [containerHeight, setContainerHeight] = useState(400);

  // Selection state for multi-select
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const workspaceById = useMemo(() => buildWorkspaceById(tree), [tree]);
  const selectedTargets = useMemo(
    () =>
      selectedIds
        .filter((id) => !isWorkspaceRootTreeNode(id, workspaceById.get(id)))
        .map((id) =>
          wikiPageMutationTargetForTreeNode(id, workspaceById.get(id))
        ),
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

  // Auto-reveal active page node (once per activeWikiPagePath change)
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
        const { data, error } = await client.GET('/wiki/search', {
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

  // Compute initial open state: expand ancestors of active page
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
    (nodes: NodeApi<WikiPageTreeNodeResponse>[]) => {
      const ids = nodes.map((n) => n.id);
      setSelectedIds(ids);
      onSelectionChange?.(ids);
    },
    [onSelectionChange]
  );

  // Handle node activation (file click)
  const handleActivate = useCallback(
    (node: NodeApi<WikiPageTreeNodeResponse>) => {
      if (node.data.type !== WikiPageTreeNodeResponseType.directory) {
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
      node: NodeApi<WikiPageTreeNodeResponse>;
    }) => {
      const parts = id.split('/');
      parts[parts.length - 1] = name;
      const newTreePath = parts.join('/');
      if (newTreePath !== id) {
        const workspace = node.data.workspace ?? null;
        const oldTarget = wikiPageMutationTargetForTreeNode(id, workspace);
        const newTarget = wikiPageMutationTargetForTreeNode(
          newTreePath,
          workspace
        );
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
      dragNodes: NodeApi<WikiPageTreeNodeResponse>[];
      parentId: string | null;
      parentNode: NodeApi<WikiPageTreeNodeResponse> | null;
      index: number;
    }) => {
      for (const [idx, dragId] of dragIds.entries()) {
        const dragNode = dragNodes[idx];
        const resolved = resolveWikiTreeMove({
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
      parentNode: NodeApi<WikiPageTreeNodeResponse>;
      dragNodes: NodeApi<WikiPageTreeNodeResponse>[];
      index: number;
    }) => {
      // Cannot drop on a file node
      if (parentNode?.isLeaf) return true;
      // Cannot drop into own subtree
      for (const dn of dragNodes) {
        if (parentNode && dn.isAncestorOf(parentNode)) return true;
        if (parentNode && dn.id === parentNode.id) return true;
        const resolved = resolveWikiTreeMove({
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
            const isDir =
              node.data.type === WikiPageTreeNodeResponseType.directory;
            const hasChildren = !!(
              node.data.children && node.data.children.length > 0
            );
            const target = wikiPageMutationTargetForTreeNode(
              node.id,
              node.data.workspace ?? null
            );
            onContextAction({
              type: 'delete',
              wikiPagePath: target.path,
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

  const hasWikiPages = treeData && treeData.length > 0;
  const selectionOverlayActive = selectedIds.length > 1 && canEdit;
  const filtersActive =
    (searchResults !== null && searchQuery.length >= 2) ||
    selectedTags.length > 0;

  // Custom node renderer that passes through extra props
  const renderNode = useCallback(
    (
      props: import('react-arborist').NodeRendererProps<WikiPageTreeNodeResponse>
    ) => (
      <WikiPageTreeNode
        {...props}
        onContextAction={onContextAction}
        canWrite={canEdit}
        activeWikiPagePath={activeWikiPagePath}
        activeTreeNodeId={activeTreeNodeId}
        selectedIds={selectedIds}
        selectedTargets={selectedTargets}
      />
    ),
    [
      onContextAction,
      canEdit,
      activeWikiPagePath,
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
          <I18nText text={'Wiki'} />
        </span>
        <div className="flex items-center gap-0.5">
          <I18nProps>
            <button
              type="button"
              onClick={handleExpandAll}
              className="p-1 rounded-sm hover:bg-accent text-muted-foreground hover:text-foreground"
              title="Expand All"
            >
              <ChevronsUpDown className="h-3.5 w-3.5" />
            </button>
          </I18nProps>
          <I18nProps>
            <button
              type="button"
              onClick={handleCollapseAll}
              className="p-1 rounded-sm hover:bg-accent text-muted-foreground hover:text-foreground"
              title="Collapse All"
            >
              <ChevronsDownUp className="h-3.5 w-3.5" />
            </button>
          </I18nProps>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                className="p-1 rounded-sm hover:bg-accent text-muted-foreground hover:text-foreground"
                title={ts('Sort')}
              >
                <ArrowUpDown className="h-3.5 w-3.5" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-44">
              <DropdownMenuLabel className="text-xs py-1">
                <I18nText text={'Sort by'} />
              </DropdownMenuLabel>
              <DropdownMenuSeparator />
              <DropdownMenuRadioGroup
                value={`${sortField}:${sortOrder}`}
                onValueChange={(v) => {
                  const [f, o] = v.split(':') as [WikiSortField, WikiSortOrder];
                  onSortChange(f, o);
                }}
              >
                <DropdownMenuRadioItem value="name:asc" className="text-xs">
                  <I18nText text={'Name A–Z'} />
                </DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="name:desc" className="text-xs">
                  <I18nText text={'Name Z–A'} />
                </DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="type:asc" className="text-xs">
                  <I18nText text={'Folders first'} />
                </DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="type:desc" className="text-xs">
                  <I18nText text={'Files first'} />
                </DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="mtime:desc" className="text-xs">
                  <I18nText text={'Newest first'} />
                </DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="mtime:asc" className="text-xs">
                  <I18nText text={'Oldest first'} />
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
                  title={ts('Filter by tags')}
                >
                  <Tag className="h-3.5 w-3.5" />
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-44">
                <DropdownMenuLabel className="text-xs py-1">
                  <I18nText text={'Filter by tags'} />
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
                      <I18nText text={'Clear filter'} />
                    </DropdownMenuItem>
                  </>
                )}
              </DropdownMenuContent>
            </DropdownMenu>
          )}
          {canEdit && canCreateNew && (
            <I18nProps>
              <button
                type="button"
                onClick={onCreateNew}
                className="p-1 rounded-sm hover:bg-accent text-muted-foreground hover:text-foreground"
                title="New Wiki page"
              >
                <FilePlus className="h-4 w-4" />
              </button>
            </I18nProps>
          )}
        </div>
      </div>

      {/* Search / Selection bar (selection replaces search when active) */}
      <div className="px-2 py-1.5 border-b border-border relative">
        {/* Search input — always rendered to define the container height */}
        <div
          className={selectionOverlayActive ? 'invisible' : undefined}
          aria-hidden={selectionOverlayActive || undefined}
        >
          <div className="relative">
            <Search className="absolute left-2 top-1/2 -translate-y-1/2 h-3 w-3 text-muted-foreground" />
            <I18nProps>
              <input
                type="text"
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                placeholder="Search Wiki pages..."
                className="w-full text-xs bg-muted/50 border border-border rounded px-2 py-1 pl-6 pr-6 outline-none focus:ring-1 focus:ring-primary placeholder:text-muted-foreground/60"
                tabIndex={selectionOverlayActive ? -1 : undefined}
                disabled={selectionOverlayActive}
              />
            </I18nProps>
            {searchQuery && (
              <button
                type="button"
                onClick={() => setSearchQuery('')}
                className="absolute right-1.5 top-1/2 -translate-y-1/2 p-0.5 rounded-sm hover:bg-accent text-muted-foreground"
                disabled={selectionOverlayActive}
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
                <I18nText
                  text={
                    searchResults.length === 1
                      ? '{count} result'
                      : '{count} results'
                  }
                  values={{ count: searchResults.length }}
                />
              </div>
            )}
        </div>
        {/* Selection bar — overlaid on top when multi-select is active */}
        {selectionOverlayActive && (
          <div className="absolute inset-0 flex items-center justify-between px-3">
            <span className="text-xs text-muted-foreground">
              {selectedIds.length} <I18nText text={'selected'} />
            </span>
            <div className="flex items-center gap-1">
              <button
                type="button"
                onClick={() => onBatchDelete(selectedTargets)}
                disabled={selectedTargets.length === 0}
                className="flex items-center gap-0.5 text-xs text-destructive hover:text-destructive/80 px-1 py-0.5 rounded-sm hover:bg-destructive/10"
              >
                <Trash2 className="h-3 w-3" /> <I18nText text={'Delete'} />{' '}
                {selectedTargets.length}
              </button>
              <I18nProps>
                <button
                  type="button"
                  onClick={() => treeRef.current?.deselectAll()}
                  className="p-0.5 rounded-sm hover:bg-accent text-muted-foreground"
                  title="Clear selection"
                >
                  <X className="h-3 w-3" />
                </button>
              </I18nProps>
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
              <I18nText text={'Failed to load Wiki pages'} />
            </p>
            {onRetry && (
              <button
                type="button"
                onClick={onRetry}
                className="flex items-center gap-1 text-xs text-primary hover:underline"
              >
                <RefreshCw className="h-3 w-3" />
                <I18nText text={'Retry'} />
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
        ) : hasWikiPages ? (
          <>
            {error && onRetry && (
              <div className="flex items-center justify-between px-3 py-1 bg-destructive/10 border-b border-border">
                <span className="text-xs text-destructive">
                  <I18nText text={'Refresh failed'} />
                </span>
                <button
                  type="button"
                  onClick={onRetry}
                  className="flex items-center gap-1 text-xs text-primary hover:underline"
                >
                  <RefreshCw className="h-3 w-3" />
                  <I18nText text={'Retry'} />
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
                    <I18nText text={'No matching Wiki pages'} />
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
              <Tree<WikiPageTreeNodeResponse>
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
            {filtersActive ? (
              <>
                <Search className="h-8 w-8 text-muted-foreground/50" />
                <p className="text-sm text-muted-foreground">
                  <I18nText text={'No matching Wiki pages'} />
                </p>
              </>
            ) : (
              <>
                <FileText className="h-8 w-8 text-muted-foreground/50" />
                <p className="text-sm text-muted-foreground">
                  <I18nText text={'No Wiki pages yet.'} />
                </p>
                {canEdit && canCreateNew && (
                  <button
                    type="button"
                    onClick={onCreateNew}
                    className="text-sm text-primary hover:underline"
                  >
                    <I18nText text={'Create your first Wiki page'} />
                  </button>
                )}
              </>
            )}
          </div>
        )}
      </div>

      {/* Outline panel */}
      {onHeadingClick && (
        <WikiPageOutlinePanel
          markdown={activeWikiPageContent}
          onHeadingClick={onHeadingClick}
        />
      )}

      {/* Backlinks panel */}
      <WikiPageBacklinksPanel
        wikiPagePath={activeWikiPagePath}
        workspace={activeWikiPageWorkspace}
        onSelectWikiPage={onSelectFile}
      />
    </div>
  );
}

export default WikiTreeSidebar;
