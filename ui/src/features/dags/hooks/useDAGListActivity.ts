// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useMemo, useState } from 'react';
import {
  PathsDagListActivityGetParametersQueryOrder as ActivityOrder,
  PathsDagListActivityGetParametersQuerySort as ActivitySort,
  PathsDagsGetParametersQueryOrder as ListOrder,
  PathsDagsGetParametersQuerySort as ListSort,
  type components,
  type operations,
} from '@/api/v1/schema';
import { ACTIVITY_WINDOW_SECONDS } from '@/features/dags/components/dag-list/activityBarsModel';
import { useClient, useQuery } from '@/hooks/api';
import { useDAGListActivitySSE } from '@/hooks/useDAGListActivitySSE';
import { usePageVisible } from '@/hooks/usePageVisible';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';
import { getQueryErrorStatus, isNotFoundQueryError } from '@/lib/queryErrors';
import { isAbortLikeError, shouldRetryQueryError } from '@/lib/requestTimeout';

type Activity = components['schemas']['DAGListActivity'];
type ActivityEntry = components['schemas']['DAGListActivityEntry'];
/** The query of one page of `GET /dags`. */
type ListQuery = NonNullable<operations['listDAGs']['parameters']['query']>;
type ActivityQuery = operations['getDAGListActivity']['parameters']['query'];

/** The pages after the first that a list holds. */
export interface LaterListPages {
  /** The last page that the list holds, 1 while it holds only the first. */
  through: number;
  /** The order of the first page that those pages were read with. */
  readWith: string;
}

type LaterPageActivity = { readWith: string; dags: ActivityEntry[] };

/**
 * The polling interval while the stream is unavailable. The live topic also
 * reads the activity at most once in 10 seconds.
 */
const FALLBACK_INTERVAL_MS = 10_000;

const NO_PAGES: ReadonlyMap<number, LaterPageActivity> = new Map();

// The two endpoints declare the same sort values, each in an enum of its own.
const ACTIVITY_SORT: Record<ListSort, ActivitySort> = {
  [ListSort.name]: ActivitySort.name,
  [ListSort.nextRun]: ActivitySort.nextRun,
  [ListSort.status]: ActivitySort.status,
  [ListSort.lastRun]: ActivitySort.lastRun,
};
const ACTIVITY_ORDER: Record<ListOrder, ActivityOrder> = {
  [ListOrder.asc]: ActivityOrder.asc,
  [ListOrder.desc]: ActivityOrder.desc,
};

/** The activity request of `page` of the list that `list` reads. */
function activityQuery(list: ListQuery, page: number): ActivityQuery {
  const { sort, order, ...filters } = list;
  return {
    ...filters,
    page,
    sort: sort === undefined ? undefined : ACTIVITY_SORT[sort],
    order: order === undefined ? undefined : ACTIVITY_ORDER[order],
    window: ACTIVITY_WINDOW_SECONDS,
  };
}

/** The numbers of the pages after the first, up to `through`. */
function laterPageNumbers(through: number): number[] {
  return Array.from({ length: Math.max(0, through - 1) }, (_, i) => i + 2);
}

/** A node without the endpoint, such as an older remote node. */
function isMissingEndpointError(error: unknown): boolean {
  return isNotFoundQueryError(error) || getQueryErrorStatus(error) === 501;
}

/**
 * SWR does not poll a key that holds an error, so a failed read is retried,
 * with a growing delay, unless the node lacks the endpoint or the session
 * expired.
 */
function shouldRetryRead(error: unknown): boolean {
  return (
    !isMissingEndpointError(error) &&
    (isAbortLikeError(error) || shouldRetryQueryError(error))
  );
}

/**
 * The activity of the DAGs that a DAG list holds, by file name, over the
 * window of its strips. `firstPage` is the query of the first page of the
 * list. That page is live: its `daglistactivity` topic sends its activity,
 * which is polled only while the stream is unavailable. Each page after it
 * is read once for each order of the first page that the list read it with.
 * Nothing is read while `enabled` is false or the tab is hidden. Undefined
 * for a node that gives no activity, such as an older remote node.
 */
export function useDAGListActivity(
  firstPage: ListQuery,
  laterPages: LaterListPages,
  enabled: boolean
): ReadonlyMap<string, Activity> | undefined {
  const visible = usePageVisible();
  const remoteNode = firstPage.remoteNode ?? 'local';
  const [failedNode, setFailedNode] = useState<string | null>(null);
  const unavailable = failedNode === remoteNode;
  const live = enabled && visible && !unavailable;

  const firstQuery = useMemo(() => activityQuery(firstPage, 1), [firstPage]);
  const sse = useDAGListActivitySSE(firstQuery, live, remoteNode);
  const { data, error, mutate } = useQuery(
    '/dag-list-activity',
    live ? { params: { query: firstQuery } } : null,
    {
      ...sseFallbackOptions(sse, FALLBACK_INTERVAL_MS),
      keepPreviousData: true,
      shouldRetryOnError: shouldRetryRead,
    }
  );
  useSSECacheSync(sse, mutate);

  useEffect(() => {
    if (isMissingEndpointError(error)) {
      setFailedNode(remoteNode);
    }
  }, [error, remoteNode]);

  const client = useClient();
  const { through, readWith } = laterPages;
  const listKey = JSON.stringify(firstQuery);
  const [later, setLater] = useState<{
    listKey: string;
    pages: ReadonlyMap<number, LaterPageActivity>;
  }>({ listKey: '', pages: NO_PAGES });
  const held = later.listKey === listKey ? later.pages : NO_PAGES;
  const unread = live
    ? laterPageNumbers(through)
        .filter((page) => held.get(page)?.readWith !== readWith)
        .join(',')
    : '';

  useEffect(() => {
    if (!unread) return;
    const controller = new AbortController();
    void Promise.all(
      unread
        .split(',')
        .map(Number)
        .map(async (page) => {
          try {
            const response = await client.GET('/dag-list-activity', {
              params: { query: { ...firstQuery, page } },
              signal: controller.signal,
            });
            return { page, dags: response.data?.dags };
          } catch {
            return { page, dags: undefined };
          }
        })
    ).then((results) => {
      if (controller.signal.aborted) return;
      setLater((current) => {
        const pages = new Map(
          current.listKey === listKey ? current.pages : NO_PAGES
        );
        // A failed read keeps what the page showed until the list reads the
        // page again.
        for (const { page, dags } of results) {
          pages.set(page, {
            readWith,
            dags: dags ?? pages.get(page)?.dags ?? [],
          });
        }
        return { listKey, pages };
      });
    });
    return () => controller.abort();
  }, [client, firstQuery, listKey, readWith, unread]);

  return useMemo(() => {
    if (unavailable) return undefined;
    const byFile = new Map<string, Activity>();
    for (const page of laterPageNumbers(through)) {
      for (const entry of held.get(page)?.dags ?? []) {
        byFile.set(entry.fileName, entry.activity);
      }
    }
    // The first page is live: its activity wins over a later page read before.
    for (const entry of data?.dags ?? []) {
      byFile.set(entry.fileName, entry.activity);
    }
    return byFile;
  }, [data, held, through, unavailable]);
}
