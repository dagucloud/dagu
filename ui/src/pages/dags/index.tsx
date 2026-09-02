// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import {
  components,
  PathsDagsGetParametersQueryOrder,
  PathsDagsGetParametersQuerySort,
  ViewSortField,
  ViewSortOrder,
  ViewSpecType,
} from '../../api/v1/schema';
import { Button } from '@/components/ui/button';
import { AppBarContext } from '../../contexts/AppBarContext';
import { useCanWriteForWorkspace } from '../../contexts/AuthContext';
import { useSearchState } from '../../contexts/SearchStateContext';
import { useUserPreferences } from '../../contexts/UserPreference';
import { DAGDetailsModal } from '../../features/dags/components/dag-details';
import { DAGErrors } from '../../features/dags/components/dag-editor';
import {
  DAGTable,
  type DAGDeleteResult,
} from '../../features/dags/components/dag-list';
import DAGListHeader from '../../features/dags/components/dag-list/DAGListHeader';
import type {
  WorkflowFilterSet,
  WorkflowFilterView,
} from '../../features/dags/components/dag-list/workflowViews';
import {
  workflowViewMatchesScope,
  workflowViewScopeForSelection,
} from '../../features/dags/components/dag-list/workflowViews';
import { useClient, useQuery } from '../../hooks/api';
import { useDAGsListSSE } from '../../hooks/useDAGsListSSE';
import {
  sseFallbackOptions,
  useSSECacheSync,
} from '../../hooks/useSSECacheSync';
import {
  withoutWorkspaceLabels,
  workspaceSelectionKey,
  workspaceSelectionQuery,
} from '../../lib/workspace';
import LoadingIndicator from '@/components/ui/loading-indicator';
import { useViews, type View, type ViewSpec } from '@/hooks/useViews';
import { I18nText } from '@/i18n/I18nText';

type DAGDefinitionsFilters = WorkflowFilterSet;

type AppliedDAGDefinitionsFilters = {
  scope: string;
  filters: DAGDefinitionsFilters;
};

type DAGsPageResponse = {
  dags: components['schemas']['DAGFile'][];
  errors: string[];
  pagination: components['schemas']['Pagination'];
};

const areLabelsEqual = (a: string[], b: string[]): boolean => {
  if (a.length !== b.length) return false;
  const sortedA = [...a].sort();
  const sortedB = [...b].sort();
  return sortedA.every((label, i) => label === sortedB[i]);
};

const areDAGDefinitionsFiltersEqual = (
  a: DAGDefinitionsFilters,
  b: DAGDefinitionsFilters
) =>
  a.searchText === b.searchText &&
  areLabelsEqual(a.searchLabels, b.searchLabels) &&
  a.activeOnly === b.activeOnly &&
  a.sortField === b.sortField &&
  a.sortOrder === b.sortOrder;

const ALL_WORKFLOWS_VIEW_PARAM = 'all';
const DELETE_BATCH_SIZE = 5;

function normalizeWorkflowSortField(value?: string | null): ViewSortField {
  return value === ViewSortField.nextRun
    ? ViewSortField.nextRun
    : ViewSortField.name;
}

function normalizeWorkflowSortOrder(value?: string | null): ViewSortOrder {
  return value === ViewSortOrder.desc ? ViewSortOrder.desc : ViewSortOrder.asc;
}

function workflowSortFieldQuery(
  value: ViewSortField
): PathsDagsGetParametersQuerySort {
  return value === ViewSortField.nextRun
    ? PathsDagsGetParametersQuerySort.nextRun
    : PathsDagsGetParametersQuerySort.name;
}

function workflowSortOrderQuery(
  value: ViewSortOrder
): PathsDagsGetParametersQueryOrder {
  return value === ViewSortOrder.desc
    ? PathsDagsGetParametersQueryOrder.desc
    : PathsDagsGetParametersQueryOrder.asc;
}

function workflowFilterViewFromView(view: View): WorkflowFilterView {
  return {
    id: view.id,
    name: view.name,
    pinned: view.pinned ?? false,
    filters: {
      searchText: view.dagName ?? '',
      searchLabels: view.labels ?? [],
      activeOnly: view.activeOnly ?? false,
      sortField: normalizeWorkflowSortField(view.sortField),
      sortOrder: normalizeWorkflowSortOrder(view.sortOrder),
    },
  };
}

const cloneFilters = (
  filters: DAGDefinitionsFilters
): DAGDefinitionsFilters => ({
  ...filters,
  searchLabels: [...filters.searchLabels],
});

function buildWorkflowFilterSearch(
  currentSearch: string,
  filters: DAGDefinitionsFilters,
  viewId: string | null
): string {
  const params = new URLSearchParams(currentSearch);
  params.delete('view');
  params.delete('search');
  params.delete('labels');
  params.delete('tags');
  params.delete('active');
  params.delete('sort');
  params.delete('order');

  if (viewId === ALL_WORKFLOWS_VIEW_PARAM) {
    params.set('view', ALL_WORKFLOWS_VIEW_PARAM);
  } else {
    if (viewId) {
      params.set('view', viewId);
    }
    if (filters.searchText) {
      params.set('search', filters.searchText);
    }
    if (filters.searchLabels.length > 0) {
      params.set('labels', filters.searchLabels.join(','));
    }
    if (filters.activeOnly) {
      params.set('active', 'true');
    }
    params.set('sort', filters.sortField);
    params.set('order', filters.sortOrder);
  }

  const search = params.toString();
  return search ? `?${search}` : '';
}

function mergeUniqueDAGFiles(
  head: components['schemas']['DAGFile'][],
  older: components['schemas']['DAGFile'][]
): components['schemas']['DAGFile'][] {
  const merged: components['schemas']['DAGFile'][] = [];
  const seen = new Set<string>();

  for (const dag of [...head, ...older]) {
    if (seen.has(dag.fileName)) {
      continue;
    }
    seen.add(dag.fileName);
    merged.push(dag);
  }

  return merged;
}

function getNextPage(
  pagination: components['schemas']['Pagination'] | undefined
): number | null {
  if (!pagination) {
    return null;
  }

  if (
    pagination.nextPage > pagination.currentPage &&
    pagination.nextPage <= pagination.totalPages
  ) {
    return pagination.nextPage;
  }

  if (pagination.currentPage < pagination.totalPages) {
    return pagination.currentPage + 1;
  }

  return null;
}

function getDAGListQueryKey(query: Record<string, unknown>): string {
  return JSON.stringify(
    Object.entries(query)
      .filter(([, value]) => value !== undefined)
      .sort(([left], [right]) => left.localeCompare(right))
  );
}

function useAutoLoadMore(
  sentinelRef: React.RefObject<HTMLDivElement | null>,
  enabled: boolean,
  onLoadMore: () => void
) {
  React.useEffect(() => {
    const el = sentinelRef.current;
    if (!el || !enabled || typeof IntersectionObserver === 'undefined') {
      return;
    }

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry?.isIntersecting) {
          onLoadMore();
        }
      },
      { threshold: 0.1 }
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [enabled, onLoadMore, sentinelRef]);
}

function supportsIntersectionObserver(): boolean {
  return typeof IntersectionObserver !== 'undefined';
}

function DAGsContent() {
  const location = useLocation();
  const navigate = useNavigate();
  const query = React.useMemo(
    () => new URLSearchParams(location.search),
    [location.search]
  );
  const group = query.get('group') || '';
  const appBarContext = React.useContext(AppBarContext);
  const searchState = useSearchState();
  const client = useClient();
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const workspaceSelection = appBarContext.workspaceSelection;
  const workspaceQuery = React.useMemo(
    () => workspaceSelectionQuery(workspaceSelection),
    [workspaceSelection]
  );
  const workspaceKey = workspaceSelectionKey(workspaceSelection);
  const searchStateScope = JSON.stringify({
    remoteNode,
    workspace: workspaceKey,
  });
  const { preferences } = useUserPreferences();
  const workflowViewScope = React.useMemo(
    () => workflowViewScopeForSelection(workspaceSelection),
    [workspaceSelection]
  );
  const canManageWorkflowViews = useCanWriteForWorkspace(
    workflowViewScope.workspace
  );
  const {
    views: sharedWorkflowViews,
    isLoading: workflowViewsLoading,
    createView,
    updateView,
    deleteView,
  } = useViews(ViewSpecType.workflow);
  const scopedWorkflowViews = React.useMemo(
    () =>
      sharedWorkflowViews.filter((view) =>
        workflowViewMatchesScope(view, workflowViewScope)
      ),
    [sharedWorkflowViews, workflowViewScope]
  );
  const workflowViews = React.useMemo(
    () => scopedWorkflowViews.map(workflowFilterViewFromView),
    [scopedWorkflowViews]
  );
  const defaultWorkflowViewId = scopedWorkflowViews.find(
    (view) => view.isDefault
  )?.id;
  const previousWorkspaceKeyRef = React.useRef(workspaceKey);
  const [selectedDAG, setSelectedDAG] = React.useState<string | null>(() =>
    query.get('selectedDAG')
  );
  const updateSelectedDAG = React.useCallback(
    (fileName: string | null, replace = false) => {
      setSelectedDAG(fileName);
      const params = new URLSearchParams(location.search);
      if (fileName) {
        params.set('selectedDAG', fileName);
      } else {
        params.delete('selectedDAG');
      }
      const search = params.toString();
      navigate(
        {
          pathname: location.pathname,
          search: search ? `?${search}` : '',
        },
        { replace }
      );
    },
    [location.pathname, location.search, navigate]
  );
  const [olderDAGFiles, setOlderDAGFiles] = React.useState<
    components['schemas']['DAGFile'][]
  >([]);
  const [continuationPageOverride, setContinuationPageOverride] =
    React.useState<number | null | undefined>(undefined);
  const [isLoadingMore, setIsLoadingMore] = React.useState(false);
  const [loadMoreError, setLoadMoreError] = React.useState<string | null>(null);
  const [activeWorkflowViewId, setActiveWorkflowViewId] = React.useState<
    string | null
  >(null);
  const [workflowViewError, setWorkflowViewError] = React.useState<
    string | null
  >(null);
  const loadMoreSentinelRef = React.useRef<HTMLDivElement>(null);
  const autoLoadPendingRef = React.useRef(false);
  const loadMoreControllerRef = React.useRef<AbortController | null>(null);
  const paginationGenerationRef = React.useRef(0);

  const defaultFilters = React.useMemo<DAGDefinitionsFilters>(
    () => ({
      searchText: '',
      searchLabels: [],
      activeOnly: false,
      sortField: ViewSortField.name,
      sortOrder: ViewSortOrder.asc,
    }),
    []
  );
  const [searchText, setSearchText] = React.useState(defaultFilters.searchText);
  const [searchLabels, setSearchLabels] = React.useState<string[]>(
    defaultFilters.searchLabels
  );
  const [activeOnly, setActiveOnly] = React.useState(defaultFilters.activeOnly);
  const [sortField, setSortField] = React.useState(defaultFilters.sortField);
  const [sortOrder, setSortOrder] = React.useState(defaultFilters.sortOrder);
  const [appliedFilters, setAppliedFilters] =
    React.useState<AppliedDAGDefinitionsFilters | null>(null);
  const appliedFiltersRef = React.useRef<AppliedDAGDefinitionsFilters | null>(
    null
  );

  React.useEffect(() => {
    setSelectedDAG(query.get('selectedDAG'));
  }, [query]);

  React.useEffect(() => {
    if (previousWorkspaceKeyRef.current === workspaceKey) {
      return;
    }
    previousWorkspaceKeyRef.current = workspaceKey;
    updateSelectedDAG(null, true);
  }, [updateSelectedDAG, workspaceKey]);

  const resetLoadedPages = React.useCallback(() => {
    paginationGenerationRef.current += 1;
    loadMoreControllerRef.current?.abort();
    loadMoreControllerRef.current = null;
    setOlderDAGFiles([]);
    setContinuationPageOverride(undefined);
    setLoadMoreError(null);
    setIsLoadingMore(false);
  }, []);

  const currentFilters = React.useMemo<DAGDefinitionsFilters>(
    () => ({
      searchText,
      searchLabels,
      activeOnly,
      sortField,
      sortOrder,
    }),
    [searchText, searchLabels, activeOnly, sortField, sortOrder]
  );

  const currentFiltersRef = React.useRef(currentFilters);
  React.useEffect(() => {
    currentFiltersRef.current = currentFilters;
  }, [currentFilters]);

  const applyQueryFilters = React.useCallback(
    (filters: DAGDefinitionsFilters) => {
      const next = {
        scope: searchStateScope,
        filters: cloneFilters(filters),
      };
      appliedFiltersRef.current = next;
      setAppliedFilters(next);
    },
    [searchStateScope]
  );

  const lastPersistedFiltersRef = React.useRef<DAGDefinitionsFilters | null>(
    null
  );
  const previousFilterScopeRef = React.useRef(searchStateScope);

  React.useEffect(() => {
    if (workflowViewsLoading) {
      return;
    }
    const params = new URLSearchParams(location.search);
    const stored = searchState.readState<DAGDefinitionsFilters>(
      'dagDefinitions',
      searchStateScope
    );
    const urlFilters: Partial<DAGDefinitionsFilters> = {};
    let hasUrlFilters = false;

    if (params.has('search')) {
      urlFilters.searchText = params.get('search') ?? '';
      hasUrlFilters = true;
    }

    if (params.has('labels') || params.has('tags')) {
      const labelsParam = params.get('labels') ?? params.get('tags') ?? '';
      urlFilters.searchLabels = labelsParam
        ? labelsParam
            .split(',')
            .map((t) => t.trim().toLowerCase())
            .filter((t) => t !== '')
            .filter((t) => withoutWorkspaceLabels([t]).length > 0)
        : [];
      hasUrlFilters = true;
    }

    if (params.has('active')) {
      urlFilters.activeOnly = params.get('active') === 'true';
      hasUrlFilters = true;
    }

    if (params.has('sort')) {
      urlFilters.sortField = normalizeWorkflowSortField(params.get('sort'));
      hasUrlFilters = true;
    }

    if (params.has('order')) {
      urlFilters.sortOrder = normalizeWorkflowSortOrder(params.get('order'));
      hasUrlFilters = true;
    }

    const scopeChanged = previousFilterScopeRef.current !== searchStateScope;
    previousFilterScopeRef.current = searchStateScope;
    const requestedViewId = scopeChanged ? null : params.get('view');
    const requestedView = workflowViews.find(
      (view) => view.id === requestedViewId
    );
    const defaultView = workflowViews.find(
      (view) => view.id === defaultWorkflowViewId
    );

    let base = defaultFilters;
    let nextActiveViewId: string | null = null;

    if (scopeChanged) {
      hasUrlFilters = false;
    }

    if (requestedView && hasUrlFilters) {
      if (!params.has('search')) {
        urlFilters.searchText = '';
      }
      if (!params.has('labels') && !params.has('tags')) {
        urlFilters.searchLabels = [];
      }
      if (!params.has('active')) {
        urlFilters.activeOnly = false;
      }
    }

    if (requestedViewId === ALL_WORKFLOWS_VIEW_PARAM) {
      hasUrlFilters = false;
    } else if (requestedView) {
      base = requestedView.filters;
      nextActiveViewId = requestedView.id;
    } else if (!hasUrlFilters && defaultView) {
      base = defaultView.filters;
      nextActiveViewId = defaultView.id;
    } else if (!hasUrlFilters && stored) {
      base = { ...defaultFilters, ...stored };
    }

    const nextFilters = hasUrlFilters
      ? { ...cloneFilters(base), ...urlFilters }
      : cloneFilters(base);
    const next = {
      ...nextFilters,
      sortField: normalizeWorkflowSortField(nextFilters.sortField),
      sortOrder: normalizeWorkflowSortOrder(nextFilters.sortOrder),
    };

    if (scopeChanged) {
      params.delete('selectedDAG');
      const nextSearch = buildWorkflowFilterSearch(
        params.toString(),
        next,
        nextActiveViewId ?? ALL_WORKFLOWS_VIEW_PARAM
      );
      if (nextSearch !== location.search) {
        navigate(
          { pathname: location.pathname, search: nextSearch },
          { replace: true }
        );
      }
    }

    const current = currentFiltersRef.current;

    setActiveWorkflowViewId(nextActiveViewId);

    if (
      appliedFiltersRef.current?.scope !== searchStateScope ||
      !areDAGDefinitionsFiltersEqual(current, next)
    ) {
      applyQueryFilters(next);
    }

    if (current && areDAGDefinitionsFiltersEqual(current, next)) {
      lastPersistedFiltersRef.current = next;
      searchState.writeState('dagDefinitions', searchStateScope, next);
      return;
    }

    currentFiltersRef.current = next;
    setSearchText(next.searchText);
    setSearchLabels(next.searchLabels);
    setActiveOnly(next.activeOnly);
    setSortField(next.sortField);
    setSortOrder(next.sortOrder);

    lastPersistedFiltersRef.current = next;
    searchState.writeState('dagDefinitions', searchStateScope, next);
  }, [
    defaultFilters,
    applyQueryFilters,
    location.pathname,
    location.search,
    navigate,
    searchState,
    searchStateScope,
    workflowViews,
    workflowViewsLoading,
    defaultWorkflowViewId,
  ]);

  React.useEffect(() => {
    const applied = appliedFiltersRef.current;
    if (
      workflowViewsLoading ||
      applied?.scope !== searchStateScope ||
      areDAGDefinitionsFiltersEqual(applied.filters, currentFilters)
    ) {
      return;
    }

    const timer = window.setTimeout(() => {
      applyQueryFilters(currentFilters);
    }, 500);

    return () => window.clearTimeout(timer);
  }, [
    applyQueryFilters,
    currentFilters,
    searchStateScope,
    workflowViewsLoading,
  ]);

  React.useEffect(() => {
    const persisted = lastPersistedFiltersRef.current;
    if (persisted && areDAGDefinitionsFiltersEqual(persisted, currentFilters)) {
      return;
    }

    lastPersistedFiltersRef.current = currentFilters;
    searchState.writeState('dagDefinitions', searchStateScope, currentFilters);
  }, [currentFilters, searchState, searchStateScope]);

  const requestFilters = appliedFilters?.filters ?? defaultFilters;
  const filtersReady =
    !workflowViewsLoading && appliedFilters?.scope === searchStateScope;

  const queryParams = React.useMemo(
    () => ({
      remoteNode,
      page: 1,
      perPage: preferences.pageLimit || 200,
      name: requestFilters.searchText || undefined,
      labels:
        requestFilters.searchLabels.length > 0
          ? requestFilters.searchLabels.join(',')
          : undefined,
      active: requestFilters.activeOnly || undefined,
      sort: workflowSortFieldQuery(requestFilters.sortField),
      order: workflowSortOrderQuery(requestFilters.sortOrder),
      ...workspaceQuery,
    }),
    [remoteNode, preferences.pageLimit, requestFilters, workspaceQuery]
  );
  const queryKey = React.useMemo(
    () => getDAGListQueryKey(queryParams),
    [queryParams]
  );

  const dagsListSSE = useDAGsListSSE(queryParams, filtersReady);
  const { data, mutate, isLoading } = useQuery(
    '/dags',
    filtersReady
      ? {
          params: {
            query: queryParams,
          },
        }
      : null,
    {
      ...sseFallbackOptions(dagsListSSE),
      keepPreviousData: true,
      revalidateIfStale: false,
      revalidateOnFocus: false,
      revalidateOnReconnect: false,
    }
  );
  useSSECacheSync(dagsListSSE, mutate);

  React.useEffect(() => {
    resetLoadedPages();
  }, [queryKey, resetLoadedPages]);

  const updateFilterLocation = React.useCallback(
    (filters: DAGDefinitionsFilters, viewId: string | null, replace = true) => {
      const search = buildWorkflowFilterSearch(
        location.search,
        filters,
        viewId
      );
      navigate({ pathname: location.pathname, search }, { replace });
    },
    [location.pathname, location.search, navigate]
  );

  const applyFilters = React.useCallback(
    (
      filters: DAGDefinitionsFilters,
      viewId: string | null,
      replace = false
    ) => {
      const next = cloneFilters(filters);
      currentFiltersRef.current = next;
      applyQueryFilters(next);
      updateFilterLocation(next, viewId, replace);
      setSearchText(next.searchText);
      setSearchLabels(next.searchLabels);
      setActiveOnly(next.activeOnly);
      setSortField(next.sortField);
      setSortOrder(next.sortOrder);
      setActiveWorkflowViewId(
        viewId === ALL_WORKFLOWS_VIEW_PARAM ? null : viewId
      );
    },
    [applyQueryFilters, updateFilterLocation]
  );

  const buildWorkflowViewSpec = React.useCallback(
    (
      name: string,
      filters: DAGDefinitionsFilters,
      isDefault: boolean,
      pinned: boolean
    ): ViewSpec => ({
      name,
      type: ViewSpecType.workflow,
      workspace: workflowViewScope.workspace,
      workspaceScope: workflowViewScope.workspaceScope,
      labels: [...filters.searchLabels],
      dagName: filters.searchText,
      activeOnly: filters.activeOnly,
      intervalDays: 1,
      pinned,
      sortField: normalizeWorkflowSortField(filters.sortField),
      sortOrder: normalizeWorkflowSortOrder(filters.sortOrder),
      isDefault,
    }),
    [workflowViewScope]
  );

  const refreshFn = React.useCallback(() => {
    resetLoadedPages();
    setTimeout(() => mutate(), 500);
  }, [mutate, resetLoadedPages]);

  const handleDeleteDAGs = React.useCallback(
    async (fileNames: string[]): Promise<DAGDeleteResult[]> => {
      const deleteOne = async (fileName: string): Promise<DAGDeleteResult> => {
        try {
          const { error } = await client.DELETE('/dags/{fileName}', {
            params: {
              path: { fileName },
              query: { remoteNode },
            },
          });
          return {
            fileName,
            error: error
              ? error.message || 'The delete request failed'
              : undefined,
          };
        } catch (error) {
          return {
            fileName,
            error:
              error instanceof Error
                ? error.message
                : 'Unexpected server error',
          };
        }
      };

      const results: DAGDeleteResult[] = [];
      for (
        let index = 0;
        index < fileNames.length;
        index += DELETE_BATCH_SIZE
      ) {
        const batch = fileNames.slice(index, index + DELETE_BATCH_SIZE);
        results.push(...(await Promise.all(batch.map(deleteOne))));
      }
      const deletedFileNames = results
        .filter((result) => !result.error)
        .map((result) => result.fileName);

      if (deletedFileNames.length > 0) {
        if (selectedDAG && deletedFileNames.includes(selectedDAG)) {
          updateSelectedDAG(null, true);
        }
        resetLoadedPages();
        await mutate();
      }

      return results;
    },
    [
      client,
      mutate,
      remoteNode,
      resetLoadedPages,
      selectedDAG,
      updateSelectedDAG,
    ]
  );

  const handleRenameDAG = React.useCallback(
    async (fileName: string, newFileName: string): Promise<void> => {
      const { error } = await client.POST('/dags/{fileName}/rename', {
        params: {
          path: { fileName },
          query: { remoteNode },
        },
        body: { newFileName },
      });
      if (error) {
        throw new Error(error.message || 'Failed to rename workflow');
      }

      if (selectedDAG === fileName) {
        updateSelectedDAG(newFileName, true);
      }
      resetLoadedPages();
      await mutate();
    },
    [
      client,
      mutate,
      remoteNode,
      resetLoadedPages,
      selectedDAG,
      updateSelectedDAG,
    ]
  );

  const handleSelectDAG = React.useCallback(
    (fileName: string) => updateSelectedDAG(fileName),
    [updateSelectedDAG]
  );

  React.useEffect(() => {
    appBarContext.setTitle('Workflows');
  }, [appBarContext]);

  const patchFilters = React.useCallback(
    (patch: Partial<DAGDefinitionsFilters>, applyImmediately = false) => {
      const next = cloneFilters({ ...currentFiltersRef.current, ...patch });
      currentFiltersRef.current = next;
      if (applyImmediately) {
        applyQueryFilters(next);
      }
      setSearchText(next.searchText);
      setSearchLabels(next.searchLabels);
      setActiveOnly(next.activeOnly);
      setSortField(next.sortField);
      setSortOrder(next.sortOrder);
      updateFilterLocation(next, activeWorkflowViewId);
    },
    [activeWorkflowViewId, applyQueryFilters, updateFilterLocation]
  );

  const searchTextChange = (nextSearchText: string) => {
    patchFilters({ searchText: nextSearchText });
  };

  const searchLabelsChange = (labels: string[]) => {
    patchFilters({ searchLabels: labels });
  };

  const handleActiveOnlyChange = (checked: boolean) => {
    patchFilters({ activeOnly: checked }, true);
  };

  const handleSortChange = (field: string, order: string) => {
    patchFilters(
      {
        sortField: normalizeWorkflowSortField(field),
        sortOrder: normalizeWorkflowSortOrder(order),
      },
      true
    );
  };

  const handleSelectWorkflowView = (viewId: string) => {
    const view = workflowViews.find((item) => item.id === viewId);
    if (view) {
      setWorkflowViewError(null);
      applyFilters(view.filters, view.id);
    }
  };

  const handleShowAllWorkflows = () => {
    setWorkflowViewError(null);
    applyFilters(defaultFilters, ALL_WORKFLOWS_VIEW_PARAM);
  };

  const handleResetWorkflowView = () => {
    const view = workflowViews.find((item) => item.id === activeWorkflowViewId);
    if (view) {
      setWorkflowViewError(null);
      applyFilters(view.filters, view.id, true);
    }
  };

  const handleSaveWorkflowView = async (
    name: string,
    makeDefault: boolean,
    pinned: boolean
  ): Promise<void> => {
    const filters = cloneFilters(currentFiltersRef.current);
    setWorkflowViewError(null);
    try {
      const view = await createView(
        buildWorkflowViewSpec(name, filters, makeDefault, pinned)
      );
      applyFilters(workflowFilterViewFromView(view).filters, view.id, true);
    } catch (error) {
      setWorkflowViewError(
        error instanceof Error ? error.message : 'Failed to save workflow view'
      );
      throw error;
    }
  };

  const handleUpdateWorkflowView = async (): Promise<void> => {
    const view = scopedWorkflowViews.find(
      (item) => item.id === activeWorkflowViewId
    );
    if (!view) {
      return;
    }
    const filters = cloneFilters(currentFiltersRef.current);
    setWorkflowViewError(null);
    try {
      const updated = await updateView(
        view.id,
        buildWorkflowViewSpec(
          view.name,
          filters,
          view.isDefault ?? false,
          view.pinned ?? false
        )
      );
      applyFilters(
        workflowFilterViewFromView(updated).filters,
        updated.id,
        true
      );
    } catch (error) {
      setWorkflowViewError(
        error instanceof Error
          ? error.message
          : 'Failed to update workflow view'
      );
      throw error;
    }
  };

  const handleSetDefaultWorkflowView = async (
    viewId: string | undefined
  ): Promise<void> => {
    const target = scopedWorkflowViews.find(
      (view) => view.id === (viewId ?? defaultWorkflowViewId)
    );
    if (!target) {
      return;
    }
    setWorkflowViewError(null);
    try {
      await updateView(
        target.id,
        buildWorkflowViewSpec(
          target.name,
          workflowFilterViewFromView(target).filters,
          viewId !== undefined,
          target.pinned ?? false
        )
      );
    } catch (error) {
      setWorkflowViewError(
        error instanceof Error
          ? error.message
          : 'Failed to update the default workflow view'
      );
      throw error;
    }
  };

  const handleSetPinnedWorkflowView = async (
    viewId: string,
    pinned: boolean
  ): Promise<void> => {
    const target = scopedWorkflowViews.find((view) => view.id === viewId);
    if (!target) {
      return;
    }
    setWorkflowViewError(null);
    try {
      await updateView(
        target.id,
        buildWorkflowViewSpec(
          target.name,
          workflowFilterViewFromView(target).filters,
          target.isDefault ?? false,
          pinned
        )
      );
    } catch (error) {
      setWorkflowViewError(
        error instanceof Error
          ? error.message
          : 'Failed to update the starred workflow view'
      );
      throw error;
    }
  };

  const handleDeleteWorkflowView = async (viewId: string): Promise<void> => {
    const deletingActiveView = viewId === activeWorkflowViewId;
    setWorkflowViewError(null);
    try {
      await deleteView(viewId);
      if (deletingActiveView) {
        applyFilters(defaultFilters, ALL_WORKFLOWS_VIEW_PARAM, true);
      }
    } catch (error) {
      setWorkflowViewError(
        error instanceof Error
          ? error.message
          : 'Failed to delete workflow view'
      );
      throw error;
    }
  };

  const activeWorkflowView = workflowViews.find(
    (view) => view.id === activeWorkflowViewId
  );
  const isWorkflowViewEdited = activeWorkflowView
    ? !areDAGDefinitionsFiltersEqual(activeWorkflowView.filters, currentFilters)
    : false;
  const isAllWorkflowsView =
    activeWorkflowViewId === null &&
    areDAGDefinitionsFiltersEqual(currentFilters, defaultFilters);

  const nextPage =
    continuationPageOverride === undefined
      ? getNextPage(data?.pagination)
      : continuationPageOverride;
  const hasMore = nextPage !== null;
  const { dagFiles, errorCount } = React.useMemo(() => {
    const dags = data?.dags ?? [];
    const mergedDags = mergeUniqueDAGFiles(dags, olderDAGFiles);
    return {
      dagFiles: mergedDags,
      errorCount: mergedDags.filter((dag) => dag.errors?.length).length,
    };
  }, [data?.dags, olderDAGFiles]);

  const handleLoadMore = React.useCallback(async (): Promise<void> => {
    if (isLoadingMore || !nextPage) {
      return;
    }

    const generation = paginationGenerationRef.current;
    loadMoreControllerRef.current?.abort();
    const controller = new AbortController();
    loadMoreControllerRef.current = controller;
    setIsLoadingMore(true);
    setLoadMoreError(null);

    try {
      const response = await client.GET('/dags', {
        params: {
          query: {
            ...queryParams,
            page: nextPage,
          },
        },
        signal: controller.signal,
      });

      if (
        controller.signal.aborted ||
        generation !== paginationGenerationRef.current
      ) {
        return;
      }

      if (response.error) {
        const message =
          response.error &&
          typeof response.error === 'object' &&
          'message' in response.error
            ? String(response.error.message)
            : 'Failed to load more workflows';
        setLoadMoreError(message);
        return;
      }

      const pageData = (response.data ?? {
        dags: [],
        errors: [],
        pagination: {
          totalRecords: 0,
          currentPage: nextPage,
          totalPages: nextPage,
          nextPage: 0,
          prevPage: nextPage - 1,
        },
      }) as DAGsPageResponse;
      setOlderDAGFiles((previous) =>
        mergeUniqueDAGFiles(previous, pageData.dags ?? [])
      );
      setContinuationPageOverride(getNextPage(pageData.pagination));
    } catch (caughtError) {
      if (controller.signal.aborted) {
        return;
      }
      setLoadMoreError(
        caughtError instanceof Error
          ? caughtError.message
          : 'Failed to load more workflows'
      );
    } finally {
      if (loadMoreControllerRef.current === controller) {
        loadMoreControllerRef.current = null;
      }
      if (generation === paginationGenerationRef.current) {
        setIsLoadingMore(false);
      }
    }
  }, [client, isLoadingMore, nextPage, queryParams]);

  React.useEffect(() => {
    if (!isLoadingMore) {
      autoLoadPendingRef.current = false;
    }
  }, [isLoadingMore]);

  const canAutoLoadMore = supportsIntersectionObserver();
  useAutoLoadMore(
    loadMoreSentinelRef,
    filtersReady &&
      canAutoLoadMore &&
      hasMore &&
      !isLoadingMore &&
      !loadMoreError,
    () => {
      if (autoLoadPendingRef.current) {
        return;
      }
      autoLoadPendingRef.current = true;
      void handleLoadMore();
    }
  );

  return (
    <div className="max-w-7xl">
      <DAGListHeader onRefresh={refreshFn} />
      {filtersReady && data ? (
        <>
          <DAGErrors
            dags={dagFiles}
            errors={data.errors || []}
            hasError={(errorCount > 0 || data.errors?.length > 0) && !isLoading}
          />
          <DAGTable
            dags={dagFiles}
            group={group}
            refreshFn={refreshFn}
            searchText={searchText}
            handleSearchTextChange={searchTextChange}
            searchLabels={searchLabels}
            handleSearchLabelsChange={searchLabelsChange}
            activeOnly={activeOnly}
            handleActiveOnlyChange={handleActiveOnlyChange}
            isLoading={isLoading}
            sortField={sortField}
            sortOrder={sortOrder}
            onSortChange={handleSortChange}
            workflowViews={workflowViews}
            activeWorkflowViewId={activeWorkflowViewId}
            defaultWorkflowViewId={defaultWorkflowViewId}
            isAllWorkflowsView={isAllWorkflowsView}
            isWorkflowViewEdited={isWorkflowViewEdited}
            canManageWorkflowViews={canManageWorkflowViews}
            canDeleteDAGs={canManageWorkflowViews}
            canRenameDAGs={canManageWorkflowViews}
            workflowViewError={workflowViewError}
            onSelectWorkflowView={handleSelectWorkflowView}
            onShowAllWorkflows={handleShowAllWorkflows}
            onResetWorkflowView={handleResetWorkflowView}
            onSaveWorkflowView={handleSaveWorkflowView}
            onUpdateWorkflowView={handleUpdateWorkflowView}
            onSetDefaultWorkflowView={handleSetDefaultWorkflowView}
            onSetPinnedWorkflowView={handleSetPinnedWorkflowView}
            onDeleteWorkflowView={handleDeleteWorkflowView}
            onDeleteDAGs={handleDeleteDAGs}
            onRenameDAG={handleRenameDAG}
            resultCount={data.pagination.totalRecords}
            selectedDAG={selectedDAG}
            onSelectDAG={handleSelectDAG}
          />
          <div className="mt-3 flex flex-col items-center gap-2">
            {loadMoreError && (
              <div className="text-sm text-error">{loadMoreError}</div>
            )}
            {hasMore ? (
              <>
                <div ref={loadMoreSentinelRef} className="h-4 w-full" />
                {isLoadingMore ? (
                  <div className="text-sm text-muted-foreground">
                    <I18nText text={"Loading more workflows..."} />
                  </div>
                ) : (
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => void handleLoadMore()}
                  >
                    {loadMoreError
                      ? <I18nText text={"Retry loading more"} />
                      : <I18nText text={"Load more workflows"} />}
                  </Button>
                )}
              </>
            ) : dagFiles.length > 0 ? (
              <div className="text-sm text-muted-foreground">
                <I18nText text={"All workflows are displayed."} />
              </div>
            ) : null}
          </div>
        </>
      ) : (
        <LoadingIndicator />
      )}

      {selectedDAG && (
        <DAGDetailsModal
          fileName={selectedDAG}
          isOpen={!!selectedDAG}
          onClose={() => updateSelectedDAG(null, true)}
        />
      )}
    </div>
  );
}

function DAGs() {
  return <DAGsContent />;
}

export default DAGs;
