// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext, useMemo } from 'react';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useConfig } from '@/contexts/ConfigContext';
import dayjs from '@/lib/dayjs';
import { components, Status } from '@/api/v1/schema';
import { usePaginatedDAGRuns } from '@/features/dag-runs/hooks/dagRunPagination';
import { workspaceSelectionQuery } from '@/lib/workspace';

type DAGRunSummary = components['schemas']['DAGRunSummary'];

const QUEUED_PAGE_LIMIT = 10;
const RUNNING_PAGE_LIMIT = 20;
const WAITING_PAGE_LIMIT = 20;
const DONE_PAGE_LIMIT = 10;
const FAILED_PAGE_LIMIT = 10;

/**
 * Explicit filters for a saved view. When provided, they override the global
 * AppBar workspace selection and add label/name filters. An empty (or absent)
 * workspace means all workspaces.
 */
export type KanbanFilters = {
  workspace?: string;
  labels?: string[];
  name?: string;
};

export interface KanbanColumnData {
  runs: DAGRunSummary[];
  hasMore: boolean;
  isInitialLoading: boolean;
  isLoadingMore: boolean;
  error: Error | null;
  loadMoreError: string | null;
  loadMore: () => Promise<void>;
  retry: () => Promise<void>;
}

export interface KanbanColumns {
  queued: KanbanColumnData;
  running: KanbanColumnData;
  review: KanbanColumnData;
  done: KanbanColumnData;
  failed: KanbanColumnData;
}

function dayBounds(
  dateStr: string,
  tzOffsetInSec: number | undefined
): { fromDate: number; toDate: number } {
  const d =
    tzOffsetInSec !== undefined
      ? dayjs(dateStr).utcOffset(tzOffsetInSec / 60, true)
      : dayjs(dateStr);
  return {
    fromDate: d.startOf('day').unix(),
    toDate: d.add(1, 'day').startOf('day').unix(),
  };
}

function useKanbanBucket(
  query: {
    remoteNode: string;
    labels?: string;
    name?: string;
    workspace?: string;
    fromDate: number;
    toDate: number;
    status: Status[];
    limit: number;
  },
  liveEnabled: boolean,
  fallbackIntervalMs: number
): KanbanColumnData {
  const {
    dagRuns,
    error,
    isInitialLoading,
    isLoadingMore,
    loadMoreError,
    hasMore,
    refresh,
    loadMore,
  } = usePaginatedDAGRuns({
    query,
    liveEnabled,
    fallbackIntervalMs,
    resetOnSSEInvalidate: liveEnabled,
  });

  return {
    runs: dagRuns,
    hasMore,
    isInitialLoading,
    isLoadingMore,
    error,
    loadMoreError,
    loadMore,
    retry: refresh,
  };
}

/**
 * Builds the five Kanban status columns for a single day.
 *
 * When `filters` is provided the query is driven by those explicit filters
 * (used by saved views). When omitted, the query falls back to the global
 * AppBar workspace selection (used by the Cockpit tab).
 */
export function useDateKanbanData(
  date: string,
  isToday: boolean,
  isLive: boolean,
  filters?: KanbanFilters
) {
  const appBarContext = useContext(AppBarContext);
  const { tzOffsetInSec } = useConfig();
  const remoteNode = appBarContext.selectedRemoteNode || 'local';

  // Derive primitive filter values so the memoized query keeps a stable
  // identity across renders even when the caller passes a fresh filters object.
  const hasFilters = filters != null;
  const filterWorkspace = filters?.workspace ?? '';
  const labelsParam =
    filters?.labels && filters.labels.length > 0
      ? filters.labels.join(',')
      : '';
  const nameParam = filters?.name ?? '';

  const workspaceQuery = useMemo(() => {
    if (hasFilters) {
      return filterWorkspace ? { workspace: filterWorkspace } : { workspace: 'all' };
    }
    return workspaceSelectionQuery(appBarContext.workspaceSelection);
  }, [hasFilters, filterWorkspace, appBarContext.workspaceSelection]);

  const { fromDate, toDate } = useMemo(
    () => dayBounds(date, tzOffsetInSec),
    [date, tzOffsetInSec]
  );
  const baseQuery = useMemo(
    () => ({
      remoteNode,
      fromDate,
      toDate,
      ...workspaceQuery,
      ...(labelsParam ? { labels: labelsParam } : {}),
      ...(nameParam ? { name: nameParam } : {}),
    }),
    [fromDate, remoteNode, toDate, workspaceQuery, labelsParam, nameParam]
  );
  const fallbackIntervalMs = isToday ? 2000 : 0;

  const queued = useKanbanBucket(
    {
      ...baseQuery,
      status: [Status.Queued, Status.NotStarted],
      limit: QUEUED_PAGE_LIMIT,
    },
    isLive,
    fallbackIntervalMs
  );
  const running = useKanbanBucket(
    {
      ...baseQuery,
      status: [Status.Running],
      limit: RUNNING_PAGE_LIMIT,
    },
    isLive,
    fallbackIntervalMs
  );
  const review = useKanbanBucket(
    {
      ...baseQuery,
      status: [Status.Waiting],
      limit: WAITING_PAGE_LIMIT,
    },
    isLive,
    fallbackIntervalMs
  );
  const done = useKanbanBucket(
    {
      ...baseQuery,
      status: [Status.Success, Status.PartialSuccess],
      limit: DONE_PAGE_LIMIT,
    },
    isLive,
    fallbackIntervalMs
  );
  const failed = useKanbanBucket(
    {
      ...baseQuery,
      status: [Status.Failed, Status.Aborted, Status.Rejected],
      limit: FAILED_PAGE_LIMIT,
    },
    isLive,
    fallbackIntervalMs
  );

  const columns = useMemo(
    () => ({
      queued,
      running,
      review,
      done,
      failed,
    }),
    [done, failed, queued, review, running]
  );

  const allColumns = [queued, running, review, done, failed];
  const hasAnyRuns = allColumns.some((column) => column.runs.length > 0);
  const firstError =
    allColumns.find((column) => column.error != null)?.error ?? null;
  const isLoading =
    !hasAnyRuns && allColumns.some((column) => column.isInitialLoading);
  const isEmpty = !hasAnyRuns && !isLoading && firstError == null;

  return {
    columns,
    error: !hasAnyRuns ? firstError : null,
    isLoading,
    isEmpty,
    retry: async () => {
      await Promise.all(allColumns.map((column) => column.retry()));
    },
  };
}
