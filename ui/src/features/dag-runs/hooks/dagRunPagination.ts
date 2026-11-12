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
import {
  useDAGRunsListSSE,
  type DAGRunsListSSEResponse,
} from '@/hooks/useDAGRunsListSSE';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';
import { isAbortLikeError } from '@/lib/requestTimeout';
import { getQueryErrorMessage } from '@/lib/queryErrors';

export type DAGRunSummary = components['schemas']['DAGRunSummary'];
export type DAGRunsPageResponse = components['schemas']['DAGRunsPageResponse'];
export type DAGRunListQuery = paths['/dag-runs']['get']['parameters']['query'];

const EXACT_DAG_RUN_PAGE_LIMIT = 100;

function normalizeDAGRunListQueryValue(value: unknown): unknown {
  if (Array.isArray(value)) {
    return [...value]
      .map((item) => normalizeDAGRunListQueryValue(item))
      .sort((left, right) => String(left).localeCompare(String(right)));
  }
  return value;
}

function normalizeDAGRunListQuery(
  query: DAGRunListQuery | undefined
): Record<string, unknown> {
  const normalizedEntries = Object.entries(query ?? {})
    .filter(([, value]) => value !== undefined)
    .map(([key, value]) => [key, normalizeDAGRunListQueryValue(value)] as const)
    .sort(([left], [right]) => left.localeCompare(right));
  return Object.fromEntries(normalizedEntries);
}

function getDAGRunListQueryKey(query: DAGRunListQuery | undefined): string {
  return JSON.stringify(normalizeDAGRunListQuery(query));
}

export function mergeUniqueDAGRuns(
  head: DAGRunSummary[],
  older: DAGRunSummary[]
): DAGRunSummary[] {
  const merged: DAGRunSummary[] = [];
  const seen = new Set<string>();

  for (const dagRun of [...head, ...older]) {
    const key = `${dagRun.name}\u0000${dagRun.dagRunId}`;
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    merged.push(dagRun);
  }

  return merged;
}

async function fetchDAGRunsPage(
  client: ReturnType<typeof useClient>,
  query: DAGRunListQuery,
  signal: AbortSignal
): Promise<DAGRunsPageResponse> {
  const response = await client.GET('/dag-runs', {
    params: { query },
    signal,
  });

  if (response.error) {
    const message =
      response.error &&
      typeof response.error === 'object' &&
      'message' in response.error
        ? String(response.error.message)
        : 'Failed to load DAG runs';
    throw new Error(message);
  }

  return (response.data ?? { dagRuns: [] }) as DAGRunsPageResponse;
}

type FetchAllDAGRunsOptions = {
  remoteNode: string;
  signal: AbortSignal;
  onPage?: (dagRuns: DAGRunSummary[], page: DAGRunsPageResponse) => void;
};

export async function fetchAllDAGRuns(
  client: ReturnType<typeof useClient>,
  query: DAGRunListQuery,
  { remoteNode, signal, onPage }: FetchAllDAGRunsOptions
): Promise<DAGRunSummary[]> {
  let allRuns: DAGRunSummary[] = [];
  let cursor: string | undefined;

  for (;;) {
    if (signal.aborted) {
      throw new DOMException('Aborted', 'AbortError');
    }

    const page = await fetchDAGRunsPage(
      client,
      {
        ...query,
        remoteNode,
        limit: EXACT_DAG_RUN_PAGE_LIMIT,
        cursor,
      },
      signal
    );

    allRuns = mergeUniqueDAGRuns(allRuns, page.dagRuns ?? []);
    onPage?.(allRuns, page);
    if (!page.nextCursor) {
      return allRuns;
    }
    cursor = page.nextCursor;
  }
}

type UsePaginatedDAGRunsOptions = {
  query: DAGRunListQuery;
  enabled?: boolean;
  liveEnabled?: boolean;
  fallbackIntervalMs?: number;
  resetOnSSEInvalidate?: boolean;
};

type UsePaginatedDAGRunsResult = {
  dagRuns: DAGRunSummary[];
  headPage: DAGRunsPageResponse | undefined;
  error: Error | null;
  isInitialLoading: boolean;
  isLoadingMore: boolean;
  loadMoreError: string | null;
  hasMore: boolean;
  refresh: () => Promise<void>;
  loadMore: () => Promise<void>;
};

export function usePaginatedDAGRuns({
  query,
  enabled = true,
  liveEnabled = true,
  fallbackIntervalMs = 2000,
  resetOnSSEInvalidate = false,
}: UsePaginatedDAGRunsOptions): UsePaginatedDAGRunsResult {
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
  const sseState = useDAGRunsListSSE(query, enabled && liveEnabled, remoteNode);
  const [olderRuns, setOlderRuns] = useState<DAGRunSummary[]>([]);
  const [continuationCursorOverride, setContinuationCursorOverride] = useState<
    string | null | undefined
  >(undefined);
  const [isLoadingMore, setIsLoadingMore] = useState(false);
  const [loadMoreError, setLoadMoreError] = useState<string | null>(null);
  const loadMoreControllerRef = useRef<AbortController | null>(null);
  const paginationGenerationRef = useRef(0);
  const previousHeadCursorRef = useRef<string | null | undefined>(undefined);
  const lastSSEPayloadRef = useRef<DAGRunsListSSEResponse | null>(null);
  const skipNextSSEResetRef = useRef(true);

  const stableQueryKey = useMemo(
    () => getDAGRunListQueryKey(resolvedQuery),
    [resolvedQuery]
  );
  const {
    data: headPage,
    mutate,
    isLoading,
    error,
  } = useQuery(
    '/dag-runs',
    enabled
      ? {
          params: {
            query: resolvedQuery,
          },
        }
      : null,
    sseFallbackOptions(sseState, fallbackIntervalMs)
  );
  useSSECacheSync(sseState, mutate);

  const resetOlderPages = useCallback(() => {
    paginationGenerationRef.current += 1;
    loadMoreControllerRef.current?.abort();
    loadMoreControllerRef.current = null;
    setOlderRuns([]);
    setContinuationCursorOverride(undefined);
    setLoadMoreError(null);
    setIsLoadingMore(false);
  }, []);

  useEffect(() => {
    resetOlderPages();
  }, [resetOlderPages, stableQueryKey]);

  // A head page that moved (new top row, so its cursor differs) invalidates
  // every previously loaded continuation page; the window shifted. Drop them
  // and re-anchor at the new head, so a stale page can never mix into the
  // merged list or leave an exhausted cursor behind. This covers silent
  // head updates from SSE syncing and the SSE-fallback polling interval.
  useEffect(() => {
    const cursor = headPage?.nextCursor;
    if (cursor === previousHeadCursorRef.current) {
      return;
    }
    previousHeadCursorRef.current = cursor;
    resetOlderPages();
  }, [headPage?.nextCursor, resetOlderPages]);

  useEffect(() => {
    lastSSEPayloadRef.current = null;
    skipNextSSEResetRef.current = true;
  }, [enabled, liveEnabled, resetOnSSEInvalidate, stableQueryKey]);

  useEffect(() => {
    if (
      !enabled ||
      !liveEnabled ||
      !resetOnSSEInvalidate ||
      !sseState.isConnected ||
      sseState.data == null ||
      sseState.data === lastSSEPayloadRef.current
    ) {
      return;
    }

    lastSSEPayloadRef.current = sseState.data;
    if (skipNextSSEResetRef.current) {
      skipNextSSEResetRef.current = false;
      return;
    }

    resetOlderPages();
  }, [
    enabled,
    liveEnabled,
    resetOnSSEInvalidate,
    resetOlderPages,
    sseState.data,
    sseState.isConnected,
  ]);

  const dagRuns = useMemo(
    () => mergeUniqueDAGRuns(headPage?.dagRuns ?? [], olderRuns),
    [headPage?.dagRuns, olderRuns]
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
      const response = await client.GET('/dag-runs', {
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
            : 'Failed to load more DAG runs';
        setLoadMoreError(message);
        return;
      }

      const pageData = (response.data ?? {
        dagRuns: [],
      }) as DAGRunsPageResponse;
      setOlderRuns((previous) =>
        mergeUniqueDAGRuns(previous, pageData.dagRuns ?? [])
      );
      setContinuationCursorOverride(pageData.nextCursor ?? null);
    } catch (caughtError) {
      if (controller.signal.aborted && isAbortLikeError(caughtError)) {
        return;
      }
      setLoadMoreError(
        caughtError instanceof Error
          ? caughtError.message
          : 'Failed to load more DAG runs'
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
    dagRuns,
    headPage,
    error:
      error instanceof Error
        ? error
        : error
          ? new Error(getQueryErrorMessage(error, 'Failed to load DAG runs'))
          : null,
    isInitialLoading: isLoading,
    isLoadingMore,
    loadMoreError,
    hasMore: nextCursor !== null,
    refresh,
    loadMore,
  };
}
