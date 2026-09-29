// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { components } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useClient } from '@/hooks/api';

const defaultQueuePageSize = 20;

type QueueDAGRun = components['schemas']['DAGRunSummary'];

interface UseQueuedItemsFeedProps {
  enabled: boolean;
  queueName: string;
  refreshToken: string;
}

const getQueueFeedKey = (dagRun: QueueDAGRun): string =>
  `${dagRun.name}\u0000${dagRun.dagRunId}`;

const mergeQueuedItems = (
  previous: QueueDAGRun[],
  next: QueueDAGRun[]
): QueueDAGRun[] => {
  if (previous.length === 0) {
    return next;
  }

  const seen = new Set(previous.map(getQueueFeedKey));
  const merged = [...previous];
  for (const item of next) {
    const key = getQueueFeedKey(item);
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    merged.push(item);
  }
  return merged;
};

export function useQueuedItemsFeed({
  enabled,
  queueName,
  refreshToken,
}: UseQueuedItemsFeedProps) {
  const client = useClient();
  const appBarContext = React.useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const [items, setItems] = React.useState<QueueDAGRun[]>([]);
  const [nextCursor, setNextCursor] = React.useState<string | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [isLoading, setIsLoading] = React.useState(false);
  const [isLoadingMore, setIsLoadingMore] = React.useState(false);
  const requestIDRef = React.useRef(0);
  // The refs mirror state synchronously so loadMore guards see a value as
  // soon as it is set, before any re-render or effect.
  const nextCursorRef = React.useRef<string | null>(null);
  const loadingMoreRef = React.useRef(false);

  const updateNextCursor = React.useCallback((cursor: string | null) => {
    nextCursorRef.current = cursor;
    setNextCursor(cursor);
  }, []);

  const updateLoadingMore = React.useCallback((loadingMore: boolean) => {
    loadingMoreRef.current = loadingMore;
    setIsLoadingMore(loadingMore);
  }, []);

  const loadPage = React.useCallback(
    async (reset: boolean) => {
      if (!enabled) {
        setItems([]);
        updateNextCursor(null);
        setError(null);
        setIsLoading(false);
        updateLoadingMore(false);
        return;
      }

      if (!reset) {
        if (loadingMoreRef.current || !nextCursorRef.current) {
          return;
        }
        updateLoadingMore(true);
      } else {
        setIsLoading(true);
        updateLoadingMore(false);
        setError(null);
      }

      const requestID = requestIDRef.current + 1;
      requestIDRef.current = requestID;

      try {
        const { data, error } = await client.GET('/queues/{name}/items', {
          params: {
            path: { name: queueName },
            query: {
              cursor: reset ? undefined : (nextCursorRef.current ?? undefined),
              limit: defaultQueuePageSize,
              remoteNode,
            },
          },
        });

        if (requestIDRef.current !== requestID) {
          return;
        }

        if (error) {
          setError(error.message || 'Failed to load queued items.');
          if (reset) {
            setIsLoading(false);
          } else {
            updateLoadingMore(false);
          }
          return;
        }

        const pageItems = data?.items ?? [];
        setItems((previous) =>
          reset ? pageItems : mergeQueuedItems(previous, pageItems)
        );
        updateNextCursor(data?.nextCursor ?? null);
        setError(null);
        setIsLoading(false);
        updateLoadingMore(false);
      } catch (error) {
        if (requestIDRef.current !== requestID) {
          return;
        }
        const message =
          error instanceof Error && error.message
            ? error.message
            : error &&
                typeof error === 'object' &&
                'message' in error &&
                typeof error.message === 'string' &&
                error.message.length > 0
              ? error.message
              : 'Failed to load queued items.';
        setError(message);
        setIsLoading(false);
        updateLoadingMore(false);
      }
    },
    [
      client,
      enabled,
      queueName,
      remoteNode,
      updateLoadingMore,
      updateNextCursor,
    ]
  );

  React.useEffect(() => {
    if (!enabled) {
      requestIDRef.current += 1;
      setItems([]);
      updateNextCursor(null);
      setError(null);
      setIsLoading(false);
      updateLoadingMore(false);
      return;
    }

    void loadPage(true);
  }, [enabled, loadPage, refreshToken, updateLoadingMore, updateNextCursor]);

  const loadMore = React.useCallback(() => loadPage(false), [loadPage]);

  const reload = React.useCallback(() => loadPage(true), [loadPage]);

  return {
    error,
    hasMore: nextCursor !== null,
    isLoading,
    isLoadingMore,
    items,
    loadMore,
    reload,
  };
}
