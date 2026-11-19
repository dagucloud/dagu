// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useRef, useState } from 'react';
import { useClient } from '@/hooks/api';
import { HISTORY_LIMIT } from '../dock/runTimelineModel';
import { useWorkspaceData } from './context';
import type { ApiDAGRunDetails } from './types';

/** The runs older than the latest page of the history, read page by page. */
export interface OlderRuns {
  /** The runs read so far, newest first. */
  runs: ApiDAGRunDetails[];
  /** True while older runs remain on the server. */
  hasMore: boolean;
  loading: boolean;
  /** True when the last read failed. */
  failed: boolean;
  /** Reads the next page. It does nothing while a read runs or when none remains. */
  loadMore(): void;
}

interface Pages {
  runs: ApiDAGRunDetails[];
  /** The cursor of the next page; null when no run remains. */
  cursor: string | null;
}

const NO_RUNS: ApiDAGRunDetails[] = [];

/**
 * The runs of the latest page `before` that left it through its oldest end
 * in `after`: newer runs pushed them out.
 */
function pushedOut(
  before: readonly ApiDAGRunDetails[],
  after: readonly ApiDAGRunDetails[]
): ApiDAGRunDetails[] {
  const oldest = after[after.length - 1];
  if (!oldest) return [];
  const kept = new Set(after.map((r) => r.dagRunId));
  const at = before.findIndex((r) => r.dagRunId === oldest.dagRunId);
  return (at < 0 ? before : before.slice(at + 1)).filter(
    (r) => !kept.has(r.dagRunId)
  );
}

/**
 * Reads the runs that come after `head`, the latest page of the history
 * (newest first), from its cursor on. Nothing is read before the first
 * loadMore. Once pages are read, a run that a newer one pushes out of the
 * head joins them, so that no run falls between the head and the pages.
 */
export function useOlderRuns(
  head: ApiDAGRunDetails[],
  headCursor: string | null,
  includeBackfill = false
): OlderRuns {
  const { fileName, remoteNode } = useWorkspaceData('useOlderRuns');
  const client = useClient();
  const [pages, setPages] = useState<Pages | null>(null);
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);

  // Another workflow, or a history with or without the backfill runs, starts
  // with no page read: a cursor belongs to the list it came from.
  const scope = `${remoteNode}\n${fileName}\n${includeBackfill}`;
  const [seenScope, setSeenScope] = useState(scope);
  const [seenHead, setSeenHead] = useState(head);
  if (seenScope !== scope) {
    setSeenScope(scope);
    setSeenHead(head);
    setPages(null);
    setLoading(false);
    setFailed(false);
  } else if (seenHead !== head) {
    setSeenHead(head);
    if (pages) {
      const out = pushedOut(seenHead, head);
      if (out.length > 0) setPages({ ...pages, runs: [...out, ...pages.runs] });
    }
  }

  const pending = useRef<AbortController | null>(null);
  useEffect(
    () => () => {
      pending.current?.abort();
      pending.current = null;
    },
    [scope]
  );

  const cursor = pages ? pages.cursor : headCursor;
  const cursorRef = useRef(cursor);
  cursorRef.current = cursor;

  const loadMore = useCallback(() => {
    const next = cursorRef.current;
    if (pending.current || next === null) return;
    const controller = new AbortController();
    pending.current = controller;
    // The pages start now: a run pushed out of the head while the first one
    // loads joins them.
    setPages((current) => current ?? { runs: [], cursor: next });
    setLoading(true);
    setFailed(false);
    const settle = (page: Pages | null) => {
      if (controller.signal.aborted) return;
      pending.current = null;
      setLoading(false);
      if (!page) {
        setFailed(true);
        return;
      }
      setPages((current) => ({
        runs: [...(current?.runs ?? []), ...page.runs],
        cursor: page.cursor,
      }));
    };
    client
      .GET('/dags/{fileName}/dag-runs', {
        params: {
          path: { fileName },
          query: {
            remoteNode,
            cursor: next,
            limit: HISTORY_LIMIT,
            ...(includeBackfill ? { includeBackfill: true } : {}),
          },
        },
        signal: controller.signal,
      })
      .then(({ data }) =>
        settle(
          data ? { runs: data.dagRuns, cursor: data.nextCursor ?? null } : null
        )
      )
      .catch(() => settle(null));
  }, [client, fileName, remoteNode, includeBackfill]);

  return {
    runs: pages?.runs ?? NO_RUNS,
    hasMore: cursor !== null,
    loading,
    failed,
    loadMore,
  };
}
