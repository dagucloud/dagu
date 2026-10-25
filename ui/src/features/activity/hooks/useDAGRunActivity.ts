// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { components } from '@/api/v1/schema';
import { useClient, useQuery } from '@/hooks/api';
import { useDAGRunActivitySSE } from '@/hooks/useDAGRunActivitySSE';
import { usePageVisible } from '@/hooks/usePageVisible';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';
import {
  getQueryErrorMessage,
  getQueryErrorStatus,
  isNotFoundQueryError,
} from '@/lib/queryErrors';
import { isAbortLikeError } from '@/lib/requestTimeout';
import type { ActivityRun, DAGRunActivity } from '../lib/activityModel';

/** Hours covered by the live window. */
export const LIVE_WINDOW_HOURS = 24;
/** REST polling interval while the live stream is unavailable. */
const FALLBACK_INTERVAL_MS = 5000;

/** A window that ends now and follows new runs, or a fixed past range. */
export type ActivityWindow =
  | { kind: 'live' }
  | { kind: 'fixed'; from: number; to: number };

/**
 * An always-on DAG restarts without end, so the summary leaves its runs out. The
 * always-on list shows it instead.
 */
const ALWAYS_ON_RUNS = { excludeAlwaysOnRuns: true } as const;

type WorkspaceQuery = {
  workspace?: components['parameters']['Workspace'];
};

export type DAGRunActivityState = {
  /** The first page, which a live window keeps current. */
  activity?: DAGRunActivity;
  /** Runs to draw: the first page, or a snapshot of it and older pages. */
  runs: ActivityRun[];
  isLoading: boolean;
  /** Set when the node cannot serve activity, such as an older remote node. */
  unavailable: boolean;
  refresh: () => Promise<unknown>;
  hasMore: boolean;
  loadMore: () => Promise<void>;
  isLoadingMore: boolean;
  loadMoreError: string | null;
  /** Set while a live window shows older pages instead of following runs. */
  paused: boolean;
  /** Drops older pages so a live window follows new runs again. */
  resume: () => void;
};

/**
 * Older runs loaded on demand, with the first page as it was when they were
 * first requested. Older pages are not updated, so the first page is frozen
 * with them; merging a moving first page would leave gaps between pages.
 */
type LoadedPages = {
  /** Identity of the window and filters the pages belong to. */
  key: string;
  head: ActivityRun[];
  older: ActivityRun[];
  /** The first page's window, which the cursor is bound to. */
  from: number;
  to: number;
  cursor?: string;
};

function runKey(run: ActivityRun): string {
  return `${run.name}/${run.dagRunId}`;
}

function appendUnique(runs: ActivityRun[], more: ActivityRun[]): ActivityRun[] {
  const seen = new Set(runs.map(runKey));
  return [...runs, ...more.filter((run) => !seen.has(runKey(run)))];
}

function isMissingEndpointError(error: unknown): boolean {
  return isNotFoundQueryError(error) || getQueryErrorStatus(error) === 501;
}

/**
 * Reads DAG-run activity for a window. A live window streams its first page
 * while the tab is visible; a fixed window is read once. Older runs load on
 * request, and while they are shown a live window's runs stop moving until
 * resume is called. A node without the endpoint, such as an older remote
 * node, is not read again until refresh is called, so it is not polled
 * forever.
 */
export function useDAGRunActivity(
  window: ActivityWindow,
  workspaceQuery: WorkspaceQuery,
  remoteNode: string
): DAGRunActivityState {
  const visible = usePageVisible();
  const live = window.kind === 'live';
  const [failedNode, setFailedNode] = useState<string | null>(null);
  const unavailable = failedNode === remoteNode;

  const sse = useDAGRunActivitySSE(
    { hours: LIVE_WINDOW_HOURS, ...ALWAYS_ON_RUNS, ...workspaceQuery },
    live && visible && !unavailable,
    remoteNode
  );

  const query =
    window.kind === 'live'
      ? { hours: LIVE_WINDOW_HOURS }
      : { fromDate: window.from, toDate: window.to };
  const { data, error, isLoading, mutate } = useQuery(
    '/dag-run-activity',
    {
      params: {
        query: { ...query, ...ALWAYS_ON_RUNS, ...workspaceQuery, remoteNode },
      },
    },
    {
      ...(live && !unavailable
        ? sseFallbackOptions(sse, FALLBACK_INTERVAL_MS)
        : { refreshInterval: 0, revalidateOnFocus: false }),
      keepPreviousData: true,
      shouldRetryOnError: false,
    }
  );
  useSSECacheSync(sse, mutate);

  useEffect(() => {
    if (!data && isMissingEndpointError(error)) {
      setFailedNode(remoteNode);
    }
  }, [data, error, remoteNode]);

  const refresh = useCallback(() => {
    setFailedNode(null);
    return mutate();
  }, [mutate]);

  const client = useClient();
  const pagesKey = JSON.stringify([window, workspaceQuery, remoteNode]);
  const [loadedPages, setLoadedPages] = useState<LoadedPages | null>(null);
  const [isLoadingMore, setIsLoadingMore] = useState(false);
  const [loadMoreError, setLoadMoreError] = useState<string | null>(null);
  const loadMoreController = useRef<AbortController | null>(null);
  const pages = loadedPages?.key === pagesKey ? loadedPages : null;

  const resume = useCallback(() => {
    loadMoreController.current?.abort();
    loadMoreController.current = null;
    setLoadedPages(null);
    setIsLoadingMore(false);
    setLoadMoreError(null);
  }, []);

  // A new window, workspace, or node starts again from its first page.
  useEffect(() => resume, [pagesKey, resume]);

  const loadMore = useCallback(async (): Promise<void> => {
    const base: LoadedPages | null =
      pages ??
      (data
        ? {
            key: pagesKey,
            head: data.runs,
            older: [],
            from: data.from,
            to: data.to,
            cursor: data.nextCursor,
          }
        : null);
    if (!base?.cursor || isLoadingMore) return;

    loadMoreController.current?.abort();
    const controller = new AbortController();
    loadMoreController.current = controller;
    setIsLoadingMore(true);
    setLoadMoreError(null);
    try {
      const response = await client.GET('/dag-run-activity', {
        params: {
          query: {
            fromDate: base.from,
            toDate: base.to,
            cursor: base.cursor,
            ...ALWAYS_ON_RUNS,
            ...workspaceQuery,
            remoteNode,
          },
        },
        signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      if (response.error || !response.data) {
        setLoadMoreError(
          getQueryErrorMessage(response.error, 'Failed to load more runs')
        );
        return;
      }
      const page = response.data;
      setLoadedPages({
        ...base,
        older: appendUnique(base.older, page.runs),
        cursor: page.nextCursor,
      });
    } catch (caught) {
      if (controller.signal.aborted && isAbortLikeError(caught)) return;
      setLoadMoreError(
        caught instanceof Error ? caught.message : 'Failed to load more runs'
      );
    } finally {
      if (loadMoreController.current === controller) {
        loadMoreController.current = null;
        setIsLoadingMore(false);
      }
    }
  }, [
    client,
    data,
    isLoadingMore,
    pages,
    pagesKey,
    remoteNode,
    workspaceQuery,
  ]);

  const runs = useMemo(
    () => (pages ? appendUnique(pages.head, pages.older) : (data?.runs ?? [])),
    [data?.runs, pages]
  );

  return {
    activity: data,
    runs,
    isLoading: isLoading && !data,
    unavailable,
    refresh,
    hasMore: pages ? !!pages.cursor : !!data?.nextCursor,
    loadMore,
    isLoadingMore,
    loadMoreError,
    paused: live && !!pages,
    resume,
  };
}
