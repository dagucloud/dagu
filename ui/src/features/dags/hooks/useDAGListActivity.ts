// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext, useEffect, useMemo, useRef, useState } from 'react';
import type { components } from '@/api/v1/schema';
import { ACTIVITY_WINDOW_SECONDS } from '@/features/dags/components/dag-list/activityBarsModel';
import { useClient, useQuery } from '@/hooks/api';
import { useDAGRunChangesSSE } from '@/hooks/useDAGRunChangesSSE';
import { usePageVisible } from '@/hooks/usePageVisible';
import { SSEPausedContext } from '@/hooks/useSSE';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';
import { getQueryErrorStatus, isNotFoundQueryError } from '@/lib/queryErrors';

type Activity = components['schemas']['DAGListActivity'];
type ActivityPage = components['schemas']['DAGListActivityPage'];
type RunChanges = components['schemas']['DAGRunChanges'];
type Client = ReturnType<typeof useClient>;

/** The most DAGs that one activity request names, as the server allows. */
const NAMES_PER_READ = 100;
/**
 * The most bytes of names in one activity request, so that long names keep
 * the request line under the 8 KiB that common proxies accept.
 */
const NAME_BYTES_PER_READ = 6_000;
/**
 * The most activity requests in flight, so that a long list leaves the other
 * requests of the page a connection.
 */
const READS_AT_ONCE = 4;
/** The polling interval of the change feed while the stream is unavailable. */
const FALLBACK_INTERVAL_MS = 60_000;
/** The wait after a first failed read; it doubles up to RETRY_MAX_MS. */
const RETRY_MIN_MS = 5_000;
const RETRY_MAX_MS = 60_000;

/** The change feed, with its times in ms. */
interface Feed {
  since: number;
  readAt: number;
  changedAt: ReadonlyMap<string, number>;
}

/** What the list knows of the activity of the DAGs of one node. */
interface NodeActivity {
  node: string;
  /** The activity of each DAG read, null when the server left it out. */
  activity: ReadonlyMap<string, Activity | null>;
  /**
   * The server time, in ms, up to which the activity of each DAG read is
   * known current. It moves with each feed, apart from the activity, so that
   * a feed redraws no strip.
   */
  currentAt: ReadonlyMap<string, number>;
  /** DAGs read that are to be read again. */
  stale: ReadonlySet<string>;
  /** DAGs whose read is in flight. */
  reading: ReadonlySet<string>;
  /** The last change feed applied. */
  feed: Feed | null;
  /** Failed reads in a row. */
  failures: number;
  /** The client time before which no read starts, after a failure. */
  retryAt: number | null;
  /** The node gives no activity, as an older remote node does. */
  missing: boolean;
}

type ReadResult =
  | { page: ActivityPage }
  | { missing: true }
  | { error: unknown };

/**
 * The activity of the DAGs that a list shows, by file name, over the window of
 * its strips. The list reads the activity of each DAG once, then again only
 * when the change feed names the DAG, or when the feed cannot tell what
 * changed since the read. Nothing is read while `enabled` is false or the tab
 * is hidden. Undefined for a node that gives no activity, such as an older
 * remote node.
 */
export function useDAGListActivity(
  fileNames: readonly string[],
  remoteNode: string,
  enabled: boolean
): ReadonlyMap<string, Activity> | undefined {
  const client = useClient();
  const visible = usePageVisible();
  const paused = useContext(SSEPausedContext);
  const [state, setState] = useState(() => initialState(remoteNode));
  // One file name can name another workflow on another node.
  if (state.node !== remoteNode) {
    setState(initialState(remoteNode));
  }
  const live = enabled && visible && !paused && !state.missing;

  const sse = useDAGRunChangesSSE(live, remoteNode);
  const {
    data: feedData,
    error: feedError,
    mutate,
  } = useQuery(
    '/dag-run-changes',
    live ? { params: { query: { remoteNode } } } : null,
    {
      ...sseFallbackOptions(sse, FALLBACK_INTERVAL_MS),
      shouldRetryOnError: shouldRetryFeed,
    }
  );
  useSSECacheSync(sse, mutate);
  useEffect(() => {
    if (feedData) {
      setState(onNode(remoteNode, (s) => applyFeed(s, parseFeed(feedData))));
    }
  }, [feedData, remoteNode]);
  // A node without the feed, as one without the event store, would have the
  // list read every row each minute: it gives no activity instead.
  useEffect(() => {
    if (isMissingEndpoint(feedError)) {
      setState(
        onNode(remoteNode, (s) => (s.missing ? s : { ...s, missing: true }))
      );
    }
  }, [feedError, remoteNode]);

  // The list keeps the activity of the DAGs it shows only.
  useEffect(() => {
    setState(onNode(remoteNode, (s) => keepListed(s, fileNames)));
  }, [fileNames, remoteNode, state.activity]);

  // Declared before the reads, so that the reads of a node find its signal.
  const abortRef = useRef<AbortController | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    abortRef.current = controller;
    return () => controller.abort();
  }, [remoteNode]);

  // One batch of reads at a time. The reads are not aborted when the effect
  // runs again: it runs on each change of the state, and the reads in flight
  // are part of that state.
  useEffect(() => {
    const signal = abortRef.current?.signal;
    if (!live || !signal || state.reading.size > 0 || state.retryAt !== null) {
      return;
    }
    const batches = readBatches(namesToRead(state, fileNames)).slice(
      0,
      READS_AT_ONCE
    );
    if (batches.length === 0) {
      return;
    }
    const feedAtStart = state.feed;
    setState(onNode(remoteNode, (s) => startReading(s, batches.flat())));
    for (const names of batches) {
      void readActivity(client, remoteNode, names, signal).then((result) => {
        setState(
          onNode(remoteNode, (s) => {
            if (signal.aborted) {
              return releaseReading(s, names);
            }
            if ('page' in result) {
              return finishReading(s, names, result.page, feedAtStart);
            }
            return failReading(s, names, result);
          })
        );
      });
    }
  }, [client, fileNames, live, remoteNode, state]);

  useEffect(() => {
    if (state.retryAt === null) {
      return;
    }
    const timer = setTimeout(
      () => setState(onNode(remoteNode, (s) => ({ ...s, retryAt: null }))),
      Math.max(0, state.retryAt - Date.now())
    );
    return () => clearTimeout(timer);
  }, [remoteNode, state.retryAt]);

  return useMemo(
    () => (state.missing ? undefined : shownActivity(state.activity)),
    [state.activity, state.missing]
  );
}

function initialState(node: string): NodeActivity {
  return {
    node,
    activity: new Map(),
    currentAt: new Map(),
    stale: new Set(),
    reading: new Set(),
    feed: null,
    failures: 0,
    retryAt: null,
    missing: false,
  };
}

/** A state update that leaves the state of another node as it is. */
function onNode(node: string, change: (s: NodeActivity) => NodeActivity) {
  return (s: NodeActivity) => (s.node === node ? change(s) : s);
}

/**
 * Whether the activity of a DAG may miss a change: the feed may miss changes
 * before its `since`, and it names the DAG with a later change. A change at
 * the very time of the read reads the DAG again, which costs one read at most.
 */
function isStale(currentAt: number, feed: Feed, name: string): boolean {
  return (
    feed.since >= currentAt ||
    (feed.changedAt.get(name) ?? -Infinity) >= currentAt
  );
}

/**
 * Marks the DAGs read that the feed tells may have changed, and moves the
 * others to the time of the feed.
 */
function applyFeed(s: NodeActivity, feed: Feed): NodeActivity {
  const currentAt = new Map(s.currentAt);
  const stale = new Set(s.stale);
  for (const [name, at] of s.currentAt) {
    if (s.reading.has(name) || stale.has(name)) {
      continue;
    }
    if (isStale(at, feed, name)) {
      stale.add(name);
    } else {
      currentAt.set(name, Math.max(at, feed.readAt));
    }
  }
  return { ...s, currentAt, stale, feed };
}

function keepListed(
  s: NodeActivity,
  fileNames: readonly string[]
): NodeActivity {
  const listed = new Set(fileNames);
  if ([...s.activity.keys()].every((name) => listed.has(name))) {
    return s;
  }
  return {
    ...s,
    activity: new Map([...s.activity].filter(([name]) => listed.has(name))),
    currentAt: new Map([...s.currentAt].filter(([name]) => listed.has(name))),
    stale: new Set([...s.stale].filter((name) => listed.has(name))),
  };
}

/** The DAGs to read, in list order: those never read, then the stale ones. */
function namesToRead(s: NodeActivity, fileNames: readonly string[]): string[] {
  const listed = [...new Set(fileNames)].filter((name) => !s.reading.has(name));
  return [
    ...listed.filter((name) => !s.activity.has(name)),
    ...listed.filter((name) => s.stale.has(name)),
  ];
}

function startReading(s: NodeActivity, names: string[]): NodeActivity {
  const reading = new Set(s.reading);
  const stale = new Set(s.stale);
  for (const name of names) {
    reading.add(name);
    stale.delete(name);
  }
  return { ...s, reading, stale };
}

function finishReading(
  s: NodeActivity,
  names: string[],
  page: ActivityPage,
  feedAtStart: Feed | null
): NodeActivity {
  const readAt = Date.parse(page.readAt);
  const found = new Map(page.dags.map((entry) => [entry.fileName, entry]));
  const activity = new Map(s.activity);
  const currentAt = new Map(s.currentAt);
  const stale = new Set(s.stale);
  const reading = new Set(s.reading);
  for (const name of names) {
    reading.delete(name);
    activity.set(name, found.get(name)?.activity ?? null);
    currentAt.set(name, readAt);
    // A feed that came during the read can name a change that it missed.
    if (s.feed && s.feed !== feedAtStart && isStale(readAt, s.feed, name)) {
      stale.add(name);
    }
  }
  return { ...s, activity, currentAt, stale, reading, failures: 0 };
}

/** Ends the reads of names with no result: those read before are read again. */
function releaseReading(s: NodeActivity, names: string[]): NodeActivity {
  const reading = new Set(s.reading);
  const stale = new Set(s.stale);
  for (const name of names) {
    reading.delete(name);
    if (s.activity.has(name)) {
      stale.add(name);
    }
  }
  return { ...s, reading, stale };
}

/**
 * Keeps the activity that the DAGs had. A node without the endpoint gives no
 * activity; after any other failure, the reads wait longer each time.
 */
function failReading(
  s: NodeActivity,
  names: string[],
  result: Exclude<ReadResult, { page: ActivityPage }>
): NodeActivity {
  const released = releaseReading(s, names);
  const error = 'error' in result ? result.error : undefined;
  if ('missing' in result || isMissingEndpoint(error)) {
    return { ...released, missing: true };
  }
  const failures = s.failures + 1;
  return {
    ...released,
    failures,
    retryAt: Date.now() + retryDelay(failures, error),
  };
}

function retryDelay(failures: number, error: unknown): number {
  // An expired session does not mend soon.
  if (getQueryErrorStatus(error) === 401) {
    return RETRY_MAX_MS;
  }
  return Math.min(RETRY_MIN_MS * 2 ** (failures - 1), RETRY_MAX_MS);
}

/** A node without the endpoint, such as an older remote node. */
function isMissingEndpoint(error: unknown): boolean {
  return isNotFoundQueryError(error) || getQueryErrorStatus(error) === 501;
}

function shouldRetryFeed(error: unknown): boolean {
  return !isMissingEndpoint(error) && getQueryErrorStatus(error) !== 401;
}

function parseFeed(changes: RunChanges): Feed {
  return {
    since: Date.parse(changes.since),
    readAt: Date.parse(changes.readAt),
    changedAt: new Map(
      changes.changes.map((change) => [
        change.fileName,
        Date.parse(change.changedAt),
      ])
    ),
  };
}

async function readActivity(
  client: Client,
  remoteNode: string,
  names: string[],
  signal: AbortSignal
): Promise<ReadResult> {
  try {
    const { data, error, response } = await client.GET('/dag-list-activity', {
      params: {
        query: { fileName: names, window: ACTIVITY_WINDOW_SECONDS, remoteNode },
      },
      signal,
    });
    if (!data) {
      return { error: { ...error, response } };
    }
    // A node with the earlier form of the endpoint gives no readAt.
    if (!data.readAt) {
      return { missing: true };
    }
    return { page: data };
  } catch (error) {
    return { error };
  }
}

function shownActivity(
  read: ReadonlyMap<string, Activity | null>
): ReadonlyMap<string, Activity> {
  const shown = new Map<string, Activity>();
  for (const [name, activity] of read) {
    if (activity) {
      shown.set(name, activity);
    }
  }
  return shown;
}

/**
 * The names of each activity request: at most NAMES_PER_READ names and
 * NAME_BYTES_PER_READ bytes of them. A longer name goes alone.
 */
function readBatches(names: readonly string[]): string[][] {
  const batches: string[][] = [];
  let batch: string[] = [];
  let bytes = 0;
  for (const name of names) {
    const size = '&fileName='.length + encodeURIComponent(name).length;
    if (
      batch.length > 0 &&
      (batch.length === NAMES_PER_READ || bytes + size > NAME_BYTES_PER_READ)
    ) {
      batches.push(batch);
      batch = [];
      bytes = 0;
    }
    batch.push(name);
    bytes += size;
  }
  if (batch.length > 0) {
    batches.push(batch);
  }
  return batches;
}
