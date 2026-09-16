// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { components, paths } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useClient, useQuery } from '@/hooks/api';
import { isAbortLikeError } from '@/lib/requestTimeout';

export type ArtifactListItem = components['schemas']['ArtifactListItem'];
export type ArtifactListResponse = components['schemas']['ArtifactListResponse'];
export type ArtifactListQuery =
  paths['/artifacts']['get']['parameters']['query'];

function normalizeArtifactListQuery(
  query: ArtifactListQuery | undefined
): Record<string, unknown> {
  const normalizedEntries = Object.entries(query ?? {})
    .filter(([, value]) => value !== undefined)
    .map(([key, value]) => [key, value] as const)
    .sort(([left], [right]) => left.localeCompare(right));
  return Object.fromEntries(normalizedEntries);
}

function getArtifactListQueryKey(query: ArtifactListQuery | undefined): string {
  return JSON.stringify(normalizeArtifactListQuery(query));
}

export function mergeUniqueArtifacts(
  head: ArtifactListItem[],
  older: ArtifactListItem[]
): ArtifactListItem[] {
  const merged: ArtifactListItem[] = [];
  const seen = new Set<string>();

  for (const item of [...head, ...older]) {
    const key = `${item.name}\u0000${item.dagRunId}`;
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    merged.push(item);
  }

  return merged;
}

type UsePaginatedArtifactsOptions = {
  query: ArtifactListQuery;
  enabled?: boolean;
};

type UsePaginatedArtifactsResult = {
  items: ArtifactListItem[];
  error: Error | null;
  isInitialLoading: boolean;
  isLoadingMore: boolean;
  loadMoreError: string | null;
  hasMore: boolean;
  refresh: () => Promise<void>;
  loadMore: () => Promise<void>;
};

export function usePaginatedArtifacts({
  query,
  enabled = true,
}: UsePaginatedArtifactsOptions): UsePaginatedArtifactsResult {
  const appBarContext = useContext(AppBarContext);
  const client = useClient();
  const remoteNode =
    query?.remoteNode || appBarContext.selectedRemoteNode || 'local';
  const resolvedQuery = useMemo(
    () => ({
      ...query,
      remoteNode,
    }),
    [query, remoteNode]
  );
  const stableQueryKey = useMemo(
    () => getArtifactListQueryKey(resolvedQuery),
    [resolvedQuery]
  );
  const [olderItems, setOlderItems] = useState<ArtifactListItem[]>([]);
  const [continuationCursorOverride, setContinuationCursorOverride] = useState<
    string | null | undefined
  >(undefined);
  const [isLoadingMore, setIsLoadingMore] = useState(false);
  const [loadMoreError, setLoadMoreError] = useState<string | null>(null);
  const loadMoreControllerRef = useRef<AbortController | null>(null);
  const paginationGenerationRef = useRef(0);
  const previousHeadCursorRef = useRef<string | null | undefined>(undefined);

  const {
    data: headPage,
    mutate,
    isLoading,
    error,
  } = useQuery(
    '/artifacts',
    enabled
      ? {
          params: {
            query: resolvedQuery,
          },
        }
      : null
  );

  const resetOlderPages = useCallback(() => {
    paginationGenerationRef.current += 1;
    loadMoreControllerRef.current?.abort();
    loadMoreControllerRef.current = null;
    setOlderItems([]);
    setContinuationCursorOverride(undefined);
    setLoadMoreError(null);
    setIsLoadingMore(false);
  }, []);

  useEffect(() => {
    resetOlderPages();
  }, [enabled, resetOlderPages, stableQueryKey]);

  // A head page that moved (new top row, so its cursor differs) invalidates
  // every previously loaded continuation page; the window shifted. Drop them
  // and re-anchor at the new head, so a stale page can never mix into the
  // merged list or leave an exhausted cursor behind.
  useEffect(() => {
    const cursor = headPage?.nextCursor;
    if (cursor === previousHeadCursorRef.current) {
      return;
    }
    previousHeadCursorRef.current = cursor;
    resetOlderPages();
  }, [headPage?.nextCursor, resetOlderPages]);

  const items = useMemo(
    () => mergeUniqueArtifacts(headPage?.items ?? [], olderItems),
    [headPage?.items, olderItems]
  );
  const nextCursor =
    continuationCursorOverride === undefined
      ? (headPage?.nextCursor ?? null)
      : continuationCursorOverride;

  const refresh = useCallback(async (): Promise<void> => {
    resetOlderPages();
    await mutate();
  }, [mutate, resetOlderPages]);

  const loadMore = useCallback(async (): Promise<void> => {
    if (isLoadingMore || !nextCursor) {
      return;
    }

    const generation = paginationGenerationRef.current;
    loadMoreControllerRef.current?.abort();
    const controller = new AbortController();
    loadMoreControllerRef.current = controller;
    setIsLoadingMore(true);
    setLoadMoreError(null);

    try {
      const response = await client.GET('/artifacts', {
        params: {
          query: {
            ...query,
            remoteNode,
            cursor: nextCursor,
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
            : 'Failed to load more artifacts';
        setLoadMoreError(message);
        return;
      }

      const pageData = (response.data ?? { items: [] }) as ArtifactListResponse;
      setOlderItems((previous) =>
        mergeUniqueArtifacts(previous, pageData.items ?? [])
      );
      setContinuationCursorOverride(pageData.nextCursor ?? null);
    } catch (caughtError) {
      if (controller.signal.aborted && isAbortLikeError(caughtError)) {
        return;
      }
      setLoadMoreError(
        caughtError instanceof Error
          ? caughtError.message
          : 'Failed to load more artifacts'
      );
    } finally {
      if (loadMoreControllerRef.current === controller) {
        loadMoreControllerRef.current = null;
      }
      if (generation === paginationGenerationRef.current) {
        setIsLoadingMore(false);
      }
    }
  }, [client, isLoadingMore, nextCursor, query, remoteNode]);

  return {
    items,
    error:
      error instanceof Error
        ? error
        : error
          ? new Error('Failed to load artifacts')
          : null,
    isInitialLoading: isLoading,
    isLoadingMore,
    loadMoreError,
    hasMore: nextCursor !== null,
    refresh,
    loadMore,
  };
}
