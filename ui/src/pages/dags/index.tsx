// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { v4 as uuidv4 } from 'uuid';
import {
  components,
  PathsDagsGetParametersQueryOrder,
  PathsDagsGetParametersQuerySort,
} from '../../api/v1/schema';
import { Button } from '@/components/ui/button';
import { AppBarContext } from '../../contexts/AppBarContext';
import { useSearchState } from '../../contexts/SearchStateContext';
import {
  useUserPreferences,
  type WorkflowFilterSet,
  type WorkflowFilterView,
  type WorkflowFilterViewPreferences,
  type WorkflowFilterViewScope,
} from '../../contexts/UserPreference';
import { DAGDetailsModal } from '../../features/dags/components/dag-details';
import { DAGErrors } from '../../features/dags/components/dag-editor';
import { DAGTable } from '../../features/dags/components/dag-list';
import DAGListHeader from '../../features/dags/components/dag-list/DAGListHeader';
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
import { useDebouncedValue } from '@/hooks/useDebouncedValue';

type DAGDefinitionsFilters = WorkflowFilterSet;

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
  a.sortField === b.sortField &&
  a.sortOrder === b.sortOrder;

const ALL_WORKFLOWS_VIEW_PARAM = 'all';
const EMPTY_WORKFLOW_VIEW_SCOPE: WorkflowFilterViewScope = { views: [] };
const EMPTY_WORKFLOW_VIEW_PREFERENCES: WorkflowFilterViewPreferences = {};

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
  params.delete('sort');
  params.delete('order');

  if (viewId === ALL_WORKFLOWS_VIEW_PARAM) {
    params.set('view', ALL_WORKFLOWS_VIEW_PARAM);
  } else {
    if (viewId) {
      params.set('view', viewId);
    }
    params.set('search', filters.searchText);
    params.set('labels', filters.searchLabels.join(','));
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
  const { preferences, updatePreference } = useUserPreferences();
  const previousWorkspaceKeyRef = React.useRef(workspaceKey);
  const [selectedDAG, setSelectedDAG] = React.useState<string | null>(null);
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
  const loadMoreSentinelRef = React.useRef<HTMLDivElement>(null);
  const autoLoadPendingRef = React.useRef(false);
  const loadMoreControllerRef = React.useRef<AbortController | null>(null);
  const paginationGenerationRef = React.useRef(0);

  const defaultFilters = React.useMemo<DAGDefinitionsFilters>(
    () => ({
      searchText: '',
      searchLabels: [],
      sortField: 'name',
      sortOrder: 'asc',
    }),
    []
  );
  const workflowViewScopes =
    preferences.workflowFilterViews ?? EMPTY_WORKFLOW_VIEW_PREFERENCES;
  const workflowViewScope =
    workflowViewScopes[searchStateScope] ?? EMPTY_WORKFLOW_VIEW_SCOPE;
  const workflowViewScopeRef = React.useRef(workflowViewScope);
  React.useEffect(() => {
    workflowViewScopeRef.current = workflowViewScope;
  }, [workflowViewScope]);

  const [searchText, setSearchText] = React.useState(defaultFilters.searchText);
  const [searchLabels, setSearchLabels] = React.useState<string[]>(
    defaultFilters.searchLabels
  );
  const [sortField, setSortField] = React.useState(defaultFilters.sortField);
  const [sortOrder, setSortOrder] = React.useState(defaultFilters.sortOrder);
  const debouncedSearchText = useDebouncedValue(searchText, 500);
  const debouncedSearchLabels = useDebouncedValue(searchLabels, 500);

  React.useEffect(() => {
    if (previousWorkspaceKeyRef.current === workspaceKey) {
      return;
    }
    previousWorkspaceKeyRef.current = workspaceKey;
    setSelectedDAG(null);
  }, [workspaceKey]);

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
      sortField,
      sortOrder,
    }),
    [searchText, searchLabels, sortField, sortOrder]
  );

  const currentFiltersRef = React.useRef(currentFilters);
  React.useEffect(() => {
    currentFiltersRef.current = currentFilters;
  }, [currentFilters]);

  const lastPersistedFiltersRef = React.useRef<DAGDefinitionsFilters | null>(
    null
  );
  const previousFilterScopeRef = React.useRef(searchStateScope);

  React.useEffect(() => {
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

    if (params.has('sort')) {
      urlFilters.sortField = params.get('sort') || defaultFilters.sortField;
      hasUrlFilters = true;
    }

    if (params.has('order')) {
      urlFilters.sortOrder = params.get('order') || defaultFilters.sortOrder;
      hasUrlFilters = true;
    }

    const scopeChanged = previousFilterScopeRef.current !== searchStateScope;
    previousFilterScopeRef.current = searchStateScope;
    const viewScope = workflowViewScopeRef.current;
    const requestedViewId = scopeChanged ? null : params.get('view');
    const requestedView = viewScope.views.find(
      (view) => view.id === requestedViewId
    );
    const defaultView = viewScope.views.find(
      (view) => view.id === viewScope.defaultViewId
    );

    let base = defaultFilters;
    let nextActiveViewId: string | null = null;

    if (scopeChanged) {
      hasUrlFilters = false;
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

    const next = hasUrlFilters
      ? { ...cloneFilters(base), ...urlFilters }
      : cloneFilters(base);

    if (scopeChanged) {
      const nextSearch = buildWorkflowFilterSearch(
        location.search,
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

    if (current && areDAGDefinitionsFiltersEqual(current, next)) {
      lastPersistedFiltersRef.current = next;
      searchState.writeState('dagDefinitions', searchStateScope, next);
      return;
    }

    setSearchText(next.searchText);
    setSearchLabels(next.searchLabels);
    setSortField(next.sortField);
    setSortOrder(next.sortOrder);

    lastPersistedFiltersRef.current = next;
    searchState.writeState('dagDefinitions', searchStateScope, next);
  }, [
    defaultFilters,
    location.pathname,
    location.search,
    navigate,
    searchState,
    searchStateScope,
  ]);

  React.useEffect(() => {
    const persisted = lastPersistedFiltersRef.current;
    if (persisted && areDAGDefinitionsFiltersEqual(persisted, currentFilters)) {
      return;
    }

    lastPersistedFiltersRef.current = currentFilters;
    searchState.writeState('dagDefinitions', searchStateScope, currentFilters);
  }, [currentFilters, searchState, searchStateScope]);

  const queryParams = React.useMemo(
    () => ({
      remoteNode,
      page: 1,
      perPage: preferences.pageLimit || 200,
      name: debouncedSearchText || undefined,
      labels:
        debouncedSearchLabels.length > 0
          ? debouncedSearchLabels.join(',')
          : undefined,
      sort: sortField,
      order: sortOrder,
      ...workspaceQuery,
    }),
    [
      remoteNode,
      preferences.pageLimit,
      debouncedSearchText,
      debouncedSearchLabels,
      sortField,
      sortOrder,
      workspaceQuery,
    ]
  );
  const queryKey = React.useMemo(
    () => getDAGListQueryKey(queryParams),
    [queryParams]
  );

  const dagsListSSE = useDAGsListSSE(queryParams);
  const { data, mutate, isLoading } = useQuery(
    '/dags',
    {
      params: {
        query: {
          ...queryParams,
          sort: sortField as PathsDagsGetParametersQuerySort,
          order: sortOrder as PathsDagsGetParametersQueryOrder,
        },
      },
    },
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
      setSearchText(next.searchText);
      setSearchLabels(next.searchLabels);
      setSortField(next.sortField);
      setSortOrder(next.sortOrder);
      setActiveWorkflowViewId(
        viewId === ALL_WORKFLOWS_VIEW_PARAM ? null : viewId
      );
      updateFilterLocation(next, viewId, replace);
    },
    [updateFilterLocation]
  );

  const persistWorkflowViewScope = React.useCallback(
    (nextScope: WorkflowFilterViewScope) => {
      updatePreference('workflowFilterViews', {
        ...workflowViewScopes,
        [searchStateScope]: nextScope,
      });
    },
    [searchStateScope, updatePreference, workflowViewScopes]
  );

  const refreshFn = React.useCallback(() => {
    resetLoadedPages();
    setTimeout(() => mutate(), 500);
  }, [mutate, resetLoadedPages]);

  const handleSelectDAG = React.useCallback((fileName: string) => {
    setSelectedDAG(fileName);
  }, []);

  React.useEffect(() => {
    appBarContext.setTitle('Workflows');
  }, [appBarContext]);

  const searchTextChange = (nextSearchText: string) => {
    const next = {
      ...currentFiltersRef.current,
      searchText: nextSearchText,
    };
    currentFiltersRef.current = next;
    setSearchText(nextSearchText);
    updateFilterLocation(next, activeWorkflowViewId);
  };

  const searchLabelsChange = (labels: string[]) => {
    const next = {
      ...currentFiltersRef.current,
      searchLabels: [...labels],
    };
    currentFiltersRef.current = next;
    setSearchLabels(labels);
    updateFilterLocation(next, activeWorkflowViewId);
  };

  const handleSortChange = (field: string, order: string) => {
    const next = {
      ...currentFiltersRef.current,
      sortField: field,
      sortOrder: order,
    };
    currentFiltersRef.current = next;
    setSortField(field);
    setSortOrder(order);
    updateFilterLocation(next, activeWorkflowViewId);
  };

  const handleSelectWorkflowView = (viewId: string) => {
    const view = workflowViewScope.views.find((item) => item.id === viewId);
    if (view) {
      applyFilters(view.filters, view.id);
    }
  };

  const handleShowAllWorkflows = () => {
    applyFilters(defaultFilters, ALL_WORKFLOWS_VIEW_PARAM);
  };

  const handleResetWorkflowView = () => {
    const view = workflowViewScope.views.find(
      (item) => item.id === activeWorkflowViewId
    );
    if (view) {
      applyFilters(view.filters, view.id, true);
    }
  };

  const handleSaveWorkflowView = (name: string, makeDefault: boolean) => {
    const view: WorkflowFilterView = {
      id: uuidv4(),
      name,
      filters: cloneFilters(currentFiltersRef.current),
    };
    persistWorkflowViewScope({
      views: [...workflowViewScope.views, view],
      defaultViewId: makeDefault ? view.id : workflowViewScope.defaultViewId,
    });
    applyFilters(view.filters, view.id, true);
  };

  const handleUpdateWorkflowView = () => {
    const view = workflowViewScope.views.find(
      (item) => item.id === activeWorkflowViewId
    );
    if (!view) {
      return;
    }
    const filters = cloneFilters(currentFiltersRef.current);
    persistWorkflowViewScope({
      ...workflowViewScope,
      views: workflowViewScope.views.map((item) =>
        item.id === view.id ? { ...item, filters } : item
      ),
    });
    applyFilters(filters, view.id, true);
  };

  const handleSetDefaultWorkflowView = (viewId: string | undefined) => {
    persistWorkflowViewScope({
      ...workflowViewScope,
      defaultViewId: viewId,
    });
  };

  const handleDeleteWorkflowView = (viewId: string) => {
    const deletingActiveView = viewId === activeWorkflowViewId;
    persistWorkflowViewScope({
      views: workflowViewScope.views.filter((view) => view.id !== viewId),
      defaultViewId:
        workflowViewScope.defaultViewId === viewId
          ? undefined
          : workflowViewScope.defaultViewId,
    });
    if (deletingActiveView) {
      applyFilters(defaultFilters, ALL_WORKFLOWS_VIEW_PARAM, true);
    }
  };

  const activeWorkflowView = workflowViewScope.views.find(
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
            sort: sortField as PathsDagsGetParametersQuerySort,
            order: sortOrder as PathsDagsGetParametersQueryOrder,
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
  }, [client, isLoadingMore, nextPage, queryParams, sortField, sortOrder]);

  React.useEffect(() => {
    if (!isLoadingMore) {
      autoLoadPendingRef.current = false;
    }
  }, [isLoadingMore]);

  const canAutoLoadMore = supportsIntersectionObserver();
  useAutoLoadMore(
    loadMoreSentinelRef,
    canAutoLoadMore && hasMore && !isLoadingMore && !loadMoreError,
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
      {data ? (
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
            isLoading={isLoading}
            sortField={sortField}
            sortOrder={sortOrder}
            onSortChange={handleSortChange}
            workflowViews={workflowViewScope.views}
            activeWorkflowViewId={activeWorkflowViewId}
            defaultWorkflowViewId={workflowViewScope.defaultViewId}
            isAllWorkflowsView={isAllWorkflowsView}
            isWorkflowViewEdited={isWorkflowViewEdited}
            onSelectWorkflowView={handleSelectWorkflowView}
            onShowAllWorkflows={handleShowAllWorkflows}
            onResetWorkflowView={handleResetWorkflowView}
            onSaveWorkflowView={handleSaveWorkflowView}
            onUpdateWorkflowView={handleUpdateWorkflowView}
            onSetDefaultWorkflowView={handleSetDefaultWorkflowView}
            onDeleteWorkflowView={handleDeleteWorkflowView}
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
                    Loading more workflows...
                  </div>
                ) : (
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => void handleLoadMore()}
                  >
                    {loadMoreError
                      ? 'Retry loading more'
                      : 'Load more workflows'}
                  </Button>
                )}
              </>
            ) : dagFiles.length > 0 ? (
              <div className="text-sm text-muted-foreground">
                All workflows are displayed.
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
          onClose={() => setSelectedDAG(null)}
        />
      )}
    </div>
  );
}

function DAGs() {
  return <DAGsContent />;
}

export default DAGs;
