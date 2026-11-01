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

/** Counts the hooks below that read each key, while they are mounted. */
function useMountedKey(mounted: Map<string, number>, key: Key) {
  const id = unstable_serialize(key);
  useEffect(() => {
    if (!id) return;
    mounted.set(id, (mounted.get(id) ?? 0) + 1);
    return () => {
      const left = (mounted.get(id) ?? 1) - 1;
      if (left > 0) mounted.set(id, left);
      else mounted.delete(id);
    };
  }, [mounted, id]);
}

function trackMounted(mounted: Map<string, number>): Middleware {
  return (useSWRNext) =>
    function useTrackedSWR(key, fetcher, config) {
      useMountedKey(mounted, key);
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

  const [mounted] = useState(() => new Map<string, number>());
  // Stable values: a new one would render every SWR hook below again.
  const swr = useMemo(() => {
    const track = trackMounted(mounted);
    return { shown: { use: [track] }, hidden: { use: [track, noPolling] } };
  }, [mounted]);

  // The queries mounted when the subtree hid are the ones that can have
  // missed a change. A query that mounts as it shows makes its own first
  // read, so it is not read again. The effects of the children run first:
  // on show, `mounted` already drops the queries that went away.
  const { mutate } = useSWRConfig();
  const missed = useRef<string[] | null>(null);
  useEffect(() => {
    if (hidden) {
      missed.current = [...mounted.keys()];
      return;
    }
    const ids = missed.current ?? [];
    missed.current = null;
    for (const id of ids) {
      if (mounted.has(id)) void mutate(id);
    }
  }, [hidden, mounted, mutate]);

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
