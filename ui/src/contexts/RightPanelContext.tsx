// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type ReactElement,
  type ReactNode,
} from 'react';

/** The panels on the right side of the page. One of them shows at a time. */
export type RightPanelId =
  | 'ndv'
  | 'add-step'
  | 'workflow'
  | 'launcher'
  | 'yaml'
  | 'run-def-inspector'
  | 'subdag-step';

export interface RightPanelHandle {
  isActive: boolean;
  claim(): void;
  release(): void;
}

interface Holder {
  id: RightPanelId;
  evict(): void;
}

interface RightPanelStore {
  subscribe(listener: () => void): () => void;
  getActive(): RightPanelId | null;
  claim(holder: Holder): void;
  release(id: RightPanelId): void;
  /** Clears the active panel only when `holder` is the current holder. */
  drop(holder: Holder): void;
  /** Closes the active panel, as if another panel took its place. */
  closeActive(): void;
}

function createRightPanelStore(): RightPanelStore {
  let holder: Holder | null = null;
  const listeners = new Set<() => void>();
  const notify = () => listeners.forEach((listener) => listener());
  return {
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    getActive: () => holder?.id ?? null,
    claim(next) {
      const previous = holder;
      if (previous && previous.id !== next.id) {
        previous.evict();
      }
      holder = next;
      if (previous?.id !== next.id) notify();
    },
    release(id) {
      if (holder?.id !== id) return;
      holder = null;
      notify();
    },
    drop(previous) {
      if (holder !== previous) return;
      holder = null;
      notify();
    },
    closeActive() {
      const previous = holder;
      if (!previous) return;
      holder = null;
      notify();
      previous.evict();
    },
  };
}

const RightPanelStoreContext = createContext<RightPanelStore | null>(null);

const noSubscribe = () => () => {};
const noActive = () => null;

/** Makes the right-side panels below it exclusive: a claim closes the others. */
export function RightPanelProvider({
  children,
}: {
  children: ReactNode;
}): ReactElement {
  const [store] = useState(createRightPanelStore);
  return (
    <RightPanelStoreContext.Provider value={store}>
      {children}
    </RightPanelStoreContext.Provider>
  );
}

/**
 * The right-side panel `id`. In a provider, claim() calls the onEvicted of
 * the current holder of another id synchronously, then makes `id` active;
 * release() clears it only while `id` is active. Without a provider, claim()
 * and release() only set a local isActive, and onEvicted is never called.
 */
export function useRightPanel(
  id: RightPanelId,
  onEvicted: () => void
): RightPanelHandle {
  const store = useContext(RightPanelStoreContext);
  const [localActive, setLocalActive] = useState(false);
  const active = useSyncExternalStore(
    store ? store.subscribe : noSubscribe,
    store ? store.getActive : noActive,
    store ? store.getActive : noActive
  );
  const onEvictedRef = useRef(onEvicted);
  useEffect(() => {
    onEvictedRef.current = onEvicted;
  }, [onEvicted]);

  // A holder that unmounts while active gives its place back.
  const claimedRef = useRef<Holder | null>(null);
  useEffect(() => {
    if (!store) return;
    return () => {
      if (claimedRef.current) store.drop(claimedRef.current);
    };
  }, [store]);

  const claim = useCallback(() => {
    if (store) {
      const holder = { id, evict: () => onEvictedRef.current() };
      claimedRef.current = holder;
      store.claim(holder);
    } else {
      setLocalActive(true);
    }
  }, [id, store]);

  const release = useCallback(() => {
    if (store) {
      store.release(id);
    } else {
      setLocalActive(false);
    }
  }, [id, store]);

  const isActive = store ? active === id : localActive;
  return useMemo(
    () => ({ isActive, claim, release }),
    [claim, isActive, release]
  );
}

/** The id of the right-side panel that shows, or null. Null without a provider. */
export function useActiveRightPanel(): RightPanelId | null {
  const store = useContext(RightPanelStoreContext);
  return useSyncExternalStore(
    store ? store.subscribe : noSubscribe,
    store ? store.getActive : noActive,
    store ? store.getActive : noActive
  );
}

/**
 * Gives a function that closes the right-side panel that shows, for a part of
 * the page that takes its room, such as the Activity panel. It does nothing
 * without a provider or when no panel shows.
 */
export function useCloseRightPanel(): () => void {
  const store = useContext(RightPanelStoreContext);
  return useCallback(() => store?.closeActive(), [store]);
}

/**
 * Claims `id` while `open` is true and releases it when it closes. A null id
 * claims nothing. `onEvicted` runs when another panel takes the place.
 */
export function useRightPanelClaim(
  id: RightPanelId | null | undefined,
  open: boolean,
  onEvicted: () => void
): void {
  // The fallback id is never claimed: `enabled` is false without an id.
  const { claim, release } = useRightPanel(id ?? 'ndv', onEvicted);
  const enabled = !!id && open;
  useEffect(() => {
    if (!enabled) return;
    claim();
    return release;
  }, [claim, enabled, release]);
}
