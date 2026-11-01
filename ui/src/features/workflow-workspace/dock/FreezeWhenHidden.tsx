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
 * again, every query in it is read once, since a run can have ended and
 * written its last log while nothing read it.
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

  const { mutate } = useSWRConfig();
  const wasHidden = useRef(hidden);
  useEffect(() => {
    if (wasHidden.current && !hidden) {
      for (const id of mounted.keys()) void mutate(id);
    }
    wasHidden.current = hidden;
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
