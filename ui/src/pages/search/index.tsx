// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import SearchResult from '@/features/search/components/SearchResult';
import { useInfinite } from '@/hooks/api';
import { Search as SearchIcon } from 'lucide-react';
import React, { useEffect, useMemo, useRef } from 'react';
import { useLocation, useSearchParams } from 'react-router-dom';
import { AppBarContext } from '../../contexts/AppBarContext';
import { useSearchState } from '../../contexts/SearchStateContext';
import {
  workspaceSelectionKey,
  workspaceSelectionQuery,
} from '../../lib/workspace';
import Title from '@/components/ui/title';

type SearchFilters = {
  searchVal: string;
};

type SearchFeedPanelProps = {
  title: string;
  query: string;
  hasResults: boolean;
  isLoading: boolean;
  initialErrorMessage: string | null;
  loadMoreErrorMessage: string | null;
  emptyMessage: string;
  hasMore: boolean;
  isLoadingMore: boolean;
  onLoadMore: () => void;
  onRetryLoadMore: () => void;
  sentinelRef: React.RefObject<HTMLDivElement | null>;
  children: React.ReactNode;
};

type SearchFeedProps = {
  query: string;
  remoteNode: string;
  workspaceQuery: ReturnType<typeof workspaceSelectionQuery>;
};

function buildSearchParams(filters: SearchFilters): URLSearchParams {
  const params = new URLSearchParams();
  const query = filters.searchVal.trim();

  if (query) {
    params.set('q', query);
  }

  return params;
}

function getErrorStatus(error: unknown): number | undefined {
  const err = error as { status?: number; response?: { status?: number } };
  return err?.status ?? err?.response?.status;
}

function getErrorMessage(error: unknown, unavailableMessage?: string): string {
  if (getErrorStatus(error) === 403 && unavailableMessage) {
    return unavailableMessage;
  }

  return (
    (error as { message?: string })?.message || 'Search failed. Try again.'
  );
}

function useAutoLoadMore(
  sentinelRef: React.RefObject<HTMLDivElement | null>,
  enabled: boolean,
  onLoadMore: () => void
) {
  useEffect(() => {
    const el = sentinelRef.current;
    if (!el || !enabled) {
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

function SearchFeedPanel({
  title,
  query,
  hasResults,
  isLoading,
  initialErrorMessage,
  loadMoreErrorMessage,
  emptyMessage,
  hasMore,
  isLoadingMore,
  onLoadMore,
  onRetryLoadMore,
  sentinelRef,
  children,
}: SearchFeedPanelProps) {
  if (!query) {
    return (
      <div className="text-sm text-muted-foreground italic">
        Enter a search term and press Enter or click Search
      </div>
    );
  }

  if (isLoading && !hasResults && !initialErrorMessage) {
    return (
      <div className="text-sm text-muted-foreground italic">
        Searching {title.toLowerCase()}...
      </div>
    );
  }

  if (initialErrorMessage && !hasResults) {
    return (
      <div className="text-sm text-destructive">{initialErrorMessage}</div>
    );
  }

  if (!isLoading && !hasResults && !initialErrorMessage) {
    return (
      <div className="text-sm text-muted-foreground italic">{emptyMessage}</div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-lg font-semibold">{title}</h2>
        <span className="text-xs text-muted-foreground">Infinite results</span>
      </div>

      {children}

      {loadMoreErrorMessage && (
        <div className="flex flex-col items-center gap-3">
          <div className="text-sm text-destructive">{loadMoreErrorMessage}</div>
          <Button
            variant="outline"
            onClick={() => {
              onRetryLoadMore();
            }}
            disabled={isLoadingMore}
          >
            {isLoadingMore ? 'Retrying...' : 'Retry load more'}
          </Button>
        </div>
      )}

      {hasMore && !loadMoreErrorMessage && (
        <div className="flex flex-col items-center gap-3">
          <Button
            variant="outline"
            onClick={() => {
              onLoadMore();
            }}
            disabled={isLoadingMore}
          >
            {isLoadingMore ? 'Loading...' : 'Load more'}
          </Button>
          <div ref={sentinelRef} className="h-4 w-full" />
        </div>
      )}

      {!hasMore && (
        <div className="mb-6 text-center text-xs text-muted-foreground">
          End of results
        </div>
      )}
    </div>
  );
}

function DAGSearchFeed({ query, remoteNode, workspaceQuery }: SearchFeedProps) {
  const sentinelRef = useRef<HTMLDivElement>(null);
  const { data, error, isLoading, isValidating, setSize, mutate } = useInfinite(
    '/search/dags',
    (pageIndex, previousPage) => {
      if (!query) {
        return null;
      }
      if (previousPage && !previousPage.hasMore) {
        return null;
      }

      return {
        params: {
          query: {
            remoteNode,
            q: query,
            cursor: pageIndex === 0 ? undefined : previousPage?.nextCursor,
            ...workspaceQuery,
          },
        },
      };
    },
    {
      revalidateIfStale: false,
      revalidateOnFocus: false,
      revalidateOnReconnect: false,
      revalidateFirstPage: false,
    }
  );

  const pages = data ?? [];
  const results = pages.flatMap((page) => page.results ?? []);
  const hasResults = results.length > 0;
  const lastPage = pages[pages.length - 1];
  const hasMore = lastPage?.hasMore ?? false;
  const isLoadingMore = isValidating && pages.length > 0;
  const initialErrorMessage =
    pages.length === 0 && error ? getErrorMessage(error) : null;
  const loadMoreErrorMessage =
    pages.length > 0 && error ? getErrorMessage(error) : null;

  const loadMoreResults = React.useCallback(() => {
    if (!query || !hasMore || isLoadingMore || loadMoreErrorMessage) {
      return;
    }
    void setSize((current) => current + 1);
  }, [hasMore, isLoadingMore, loadMoreErrorMessage, query, setSize]);

  const retryLoadMore = React.useCallback(() => {
    void mutate();
  }, [mutate]);

  useAutoLoadMore(
    sentinelRef,
    !!query && hasMore && !loadMoreErrorMessage,
    loadMoreResults
  );

  return (
    <SearchFeedPanel
      title="DAGs"
      query={query}
      hasResults={hasResults}
      isLoading={isLoading}
      initialErrorMessage={initialErrorMessage}
      loadMoreErrorMessage={loadMoreErrorMessage}
      emptyMessage="No dags found"
      hasMore={hasMore}
      isLoadingMore={isLoadingMore}
      onLoadMore={loadMoreResults}
      onRetryLoadMore={retryLoadMore}
      sentinelRef={sentinelRef}
    >
      <SearchResult
        query={query}
        results={results}
        workspaceQuery={workspaceQuery}
      />
    </SearchFeedPanel>
  );
}

function Search() {
  const [, setSearchParams] = useSearchParams();
  const location = useLocation();
  const appBarContext = React.useContext(AppBarContext);
  const searchState = useSearchState();
  const remoteKey = appBarContext.selectedRemoteNode || 'local';
  const workspaceSelection = appBarContext.workspaceSelection;
  const workspaceQuery = useMemo(
    () => workspaceSelectionQuery(workspaceSelection),
    [workspaceSelection]
  );
  const workspaceKey = workspaceSelectionKey(workspaceSelection);
  const searchStateScope = JSON.stringify({
    remoteNode: remoteKey,
    workspace: workspaceKey,
  });
  const inputRef = useRef<HTMLInputElement>(null);
  const hydratedScopeRef = useRef<string | null>(null);

  const queryParams = useMemo(
    () => new URLSearchParams(location.search),
    [location.search]
  );

  const currentFilters = useMemo<SearchFilters>(
    () => ({
      searchVal: queryParams.get('q') || '',
    }),
    [queryParams]
  );

  const [searchVal, setSearchVal] = React.useState(currentFilters.searchVal);

  useEffect(() => {
    setSearchVal(currentFilters.searchVal);
  }, [currentFilters.searchVal]);

  useEffect(() => {
    const hasUrlState = queryParams.has('q');
    if (hydratedScopeRef.current !== searchStateScope) {
      hydratedScopeRef.current = searchStateScope;
      const stored = searchState.readState<SearchFilters>(
        'searchPage',
        searchStateScope
      );

      if (!hasUrlState && stored) {
        setSearchParams(buildSearchParams(stored), { replace: true });
        return;
      }
    }

    searchState.writeState('searchPage', searchStateScope, currentFilters);
  }, [
    currentFilters,
    queryParams,
    searchState,
    searchStateScope,
    setSearchParams,
  ]);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const syncFilters = React.useCallback(
    (next: SearchFilters, replace = false) => {
      setSearchParams(buildSearchParams(next), { replace });
    },
    [setSearchParams]
  );

  const onSubmit = React.useCallback(
    (value: string) => {
      syncFilters(
        {
          searchVal: value.trim(),
        },
        false
      );
    },
    [syncFilters]
  );

  const submittedQuery = currentFilters.searchVal.trim();
  const remoteNode = appBarContext.selectedRemoteNode || 'local';

  return (
    <div className="max-w-5xl">
      <div className="w-full">
        <Title>Search</Title>

        <div className="flex flex-col gap-3 pt-2">
          <div className="flex flex-wrap items-center gap-2">
            <Input
              placeholder="Search text..."
              className="max-w-md"
              ref={inputRef}
              value={searchVal}
              onChange={(e) => {
                setSearchVal(e.target.value);
              }}
              type="search"
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  onSubmit(searchVal);
                }
              }}
            />
            <Button
              disabled={!searchVal.trim() && !submittedQuery}
              onClick={() => {
                onSubmit(searchVal);
              }}
            >
              <SearchIcon className="h-4 w-4" />
              Search
            </Button>
          </div>

        </div>

        <div className="mt-4 space-y-4">
          <DAGSearchFeed
            query={submittedQuery}
            remoteNode={remoteNode}
            workspaceQuery={workspaceQuery}
          />
        </div>
      </div>
    </div>
  );
}

export default Search;
