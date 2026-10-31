// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { createContext, useContext, useRef, type ReactNode } from 'react';
import { SWRConfig, type Middleware } from 'swr';
import { WorkspaceDataContext } from '../data/context';

const HiddenContext = createContext(false);

/**
 * Turns off the refresh interval of every SWR hook below. The middleware sees
 * the options of the hook merged, so no hook can keep its own interval.
 */
const noPolling: Middleware = (useSWRNext) => (key, fetcher, config) =>
  useSWRNext(key, fetcher, { ...config, refreshInterval: 0 });

// Stable values: a new one would render every SWR hook below again.
const HIDDEN_SWR = { use: [noPolling] };
const SHOWN_SWR = {};

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
 * and data. While hidden, the SWR hooks in it do not poll: the frozen props
 * can still say that a run that ended is active.
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
  return (
    <HiddenContext.Provider value={hidden}>
      <SWRConfig value={hidden ? HIDDEN_SWR : SHOWN_SWR}>
        <WorkspaceDataContext.Provider value={shown.current.data}>
          {shown.current.children}
        </WorkspaceDataContext.Provider>
      </SWRConfig>
    </HiddenContext.Provider>
  );
}
