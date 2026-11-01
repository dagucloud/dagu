// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import {
  SWRConfig,
  unstable_serialize,
  useSWRConfig,
  type Key,
  type Middleware,
} from 'swr';
import { SSEPausedContext } from '@/hooks/useSSE';
import { WorkspaceDataContext } from '../data/context';

const HiddenContext = createContext(false);

/**
 * Turns off the refresh interval of every SWR hook below. The middleware sees
 * the options of the hook merged, so no hook can keep its own interval.
 */
const noPolling: Middleware = (useSWRNext) => (key, fetcher, config) =>
  useSWRNext(key, fetcher, { ...config, refreshInterval: 0 });

/** The read of one key by one mounted SWR hook. */
interface TrackedQuery {
  readonly key: string;
}

/** The queries of a subtree, and the ones it holds or mounts while hidden. */
interface QueryTracker {
  /** The reads of the mounted hooks. */
  mounted: Set<TrackedQuery>;
  /**
   * The mounted reads that can have missed a change while the subtree was
   * hidden. A read leaves when its hook unmounts or changes its key.
   */
  missed: Set<TrackedQuery>;
  /** Whether the subtree renders hidden: set during its render. */
  hidden: boolean;
}

function useTrackedKey(tracker: QueryTracker, key: Key) {
  const id = unstable_serialize(key);
  useEffect(() => {
    if (!id) return;
    const query: TrackedQuery = { key: id };
    tracker.mounted.add(query);
    // A query that mounts while hidden, as one that a URL change swaps in,
    // reads once and then misses the rest.
    if (tracker.hidden) tracker.missed.add(query);
    return () => {
      tracker.mounted.delete(query);
      tracker.missed.delete(query);
    };
  }, [tracker, id]);
}

function trackQueries(tracker: QueryTracker): Middleware {
  return (useSWRNext) =>
    function useTrackedSWR(key, fetcher, config) {
      useTrackedKey(tracker, key);
      return useSWRNext(key, fetcher, config);
    };
}

/**
 * True inside a hidden dock body (see FreezeWhenHidden): nobody sees it, so
 * a clock in it can stop.
 */
export function useDockHidden(): boolean {
  return useContext(HiddenContext);
}

/**
 * Keeps a mounted but hidden subtree as it was when it was last shown. While
 * `hidden`, a render of the parent gives it the same elements and the data
 * of the workspace does not reach it, so it renders no more for the events of
 * a run. It renders once when `hidden` turns false, with the live elements
 * and data. While hidden, the SWR hooks in it do not poll and its SSE hooks
 * hold no subscription: the frozen props can still say that a run that ended
 * is active, and the server reads a log for each subscription. Once shown
 * again, every query that was in it while hidden is read once, since a run
 * can have ended and written its last log while nothing read it.
 */
export function FreezeWhenHidden({
  hidden,
  children,
}: {
  hidden: boolean;
  children: ReactNode;
}) {
  const data = useContext(WorkspaceDataContext);
  const shown = useRef({ data, children });
  if (!hidden) shown.current = { data, children };

  const [tracker] = useState<QueryTracker>(() => ({
    mounted: new Set(),
    missed: new Set(),
    hidden,
  }));
  // Set before the children render: the effects of a query that mounts as
  // the subtree shows run before this component's, and see it shown.
  tracker.hidden = hidden;
  // Stable values: a new one would render every SWR hook below again.
  const swr = useMemo(() => {
    const track = trackQueries(tracker);
    return { shown: { use: [track] }, hidden: { use: [track, noPolling] } };
  }, [tracker]);

  // The queries held when the subtree hid, and those mounted while it was
  // hidden, can have missed a change: the key of each still mounted is read
  // once when it shows. A query that mounts as it shows makes its own first
  // read, also when it replaces a held one with the same key.
  const { mutate } = useSWRConfig();
  useEffect(() => {
    if (hidden) {
      for (const query of tracker.mounted) tracker.missed.add(query);
      return;
    }
    const keys = new Set(Array.from(tracker.missed, (query) => query.key));
    tracker.missed.clear();
    for (const key of keys) void mutate(key);
  }, [hidden, tracker, mutate]);

  return (
    <HiddenContext.Provider value={hidden}>
      <SSEPausedContext.Provider value={hidden}>
        <SWRConfig value={hidden ? swr.hidden : swr.shown}>
          <WorkspaceDataContext.Provider value={shown.current.data}>
            {shown.current.children}
          </WorkspaceDataContext.Provider>
        </SWRConfig>
      </SSEPausedContext.Provider>
    </HiddenContext.Provider>
  );
}
