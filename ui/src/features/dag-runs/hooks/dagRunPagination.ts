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

type UseExactDAGRunsOptions = {
  query: DAGRunListQuery;
  enabled?: boolean;
  liveEnabled?: boolean;
  fallbackIntervalMs?: number;
};

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

type UseExactDAGRunsResult = {
  data: DAGRunSummary[];
  error: Error | null;
  isLoading: boolean;
  isValidating: boolean;
  refresh: () => Promise<void>;
};

export function useExactDAGRuns({
  query,
  enabled = true,
  liveEnabled = true,
  fallbackIntervalMs = 5000,
}: UseExactDAGRunsOptions): UseExactDAGRunsResult {
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
  const [data, setData] = useState<DAGRunSummary[]>([]);
  const [error, setError] = useState<Error | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [isValidating, setIsValidating] = useState(false);

  const dataRef = useRef<DAGRunSummary[]>([]);
  const controllerRef = useRef<AbortController | null>(null);
  const requestIDRef = useRef(0);
  const queryRef = useRef(resolvedQuery);
  const lastSSEPayloadRef = useRef<DAGRunsListSSEResponse | null>(null);
  const skipNextSSERefreshRef = useRef(true);
  const queryKey = useMemo(
    () => getDAGRunListQueryKey(resolvedQuery),
    [resolvedQuery]
  );

  dataRef.current = data;
  queryRef.current = resolvedQuery;

  const refresh = useCallback(async (): Promise<void> => {
    if (!enabled) {
      return;
    }

    const requestID = requestIDRef.current + 1;
    requestIDRef.current = requestID;

    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    setError(null);

    const hadExistingData = dataRef.current.length > 0;

    if (!hadExistingData) {
      setIsLoading(true);
    } else {
      setIsValidating(true);
    }

    try {
      const next = await fetchAllDAGRuns(client, queryRef.current, {
        remoteNode,
        signal: controller.signal,
        onPage: (pageRuns, page) => {
          if (controller.signal.aborted || requestID !== requestIDRef.current) {
            return;
          }

          dataRef.current = pageRuns;
          setData(pageRuns);
          setError(null);

          if (!hadExistingData) {
            setIsLoading(false);
            setIsValidating(Boolean(page.nextCursor));
          }
        },
      });
      if (controller.signal.aborted || requestID !== requestIDRef.current) {
        return;
      }
      dataRef.current = next;
      setData(next);
      setError(null);
    } catch (fetchError) {
      if (
        (controller.signal.aborted && isAbortLikeError(fetchError)) ||
        requestID !== requestIDRef.current
      ) {
        return;
      }
      setError(
        fetchError instanceof Error
          ? fetchError
          : new Error('Failed to load DAG runs')
      );
    } finally {
      if (requestID === requestIDRef.current) {
        if (controllerRef.current === controller) {
          controllerRef.current = null;
        }
        setIsLoading(false);
        setIsValidating(false);
      }
    }
  }, [client, enabled, remoteNode]);

  useEffect(() => {
    if (!enabled) {
      controllerRef.current?.abort();
      controllerRef.current = null;
      setData([]);
      setError(null);
      setIsLoading(false);
      setIsValidating(false);
      return;
    }

    // Trigger reloads only when the semantic query changes, not when callers
    // allocate a fresh but equivalent query object during rerenders.
    void refresh();

    return () => {
      controllerRef.current?.abort();
      controllerRef.current = null;
    };
  }, [enabled, queryKey, refresh]);

  useEffect(() => {
    lastSSEPayloadRef.current = null;
    skipNextSSERefreshRef.current = true;
  }, [enabled, liveEnabled, queryKey]);

  useEffect(() => {
    if (
      !enabled ||
      !liveEnabled ||
      !sseState.isConnected ||
      sseState.data == null ||
      sseState.data === lastSSEPayloadRef.current
    ) {
      return;
    }

    lastSSEPayloadRef.current = sseState.data;
    if (skipNextSSERefreshRef.current) {
      skipNextSSERefreshRef.current = false;
      return;
    }

    void refresh();
  }, [enabled, liveEnabled, refresh, sseState.data, sseState.isConnected]);

  useEffect(() => {
    if (
      !enabled ||
      fallbackIntervalMs <= 0 ||
      (liveEnabled && sseState.isConnected && !sseState.shouldUseFallback)
    ) {
      return;
    }

    const timer = window.setInterval(() => {
      void refresh();
    }, fallbackIntervalMs);

    return () => {
      window.clearInterval(timer);
    };
  }, [
    enabled,
    fallbackIntervalMs,
    liveEnabled,
    sseState.isConnected,
    sseState.isConnecting,
    sseState.shouldUseFallback,
    refresh,
  ]);

  return {
    data,
    error,
    isLoading,
    isValidating,
    refresh,
  };
}

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
          ? new Error('Failed to load DAG runs')
          : null,
    isInitialLoading: isLoading,
    isLoadingMore,
    loadMoreError,
    hasMore: nextCursor !== null,
    refresh,
    loadMore,
  };
}
