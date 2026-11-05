// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useSyncExternalStore } from 'react';
import { matchesQuery } from '@/lib/breakpoints';

/**
 * Whether the media query matches, and a render when that changes.
 * `fallback` is the answer where the browser has no matchMedia, as in tests.
 */
export function useMediaQuery(query: string, fallback = false): boolean {
  const subscribe = useCallback(
    (onChange: () => void) => {
      if (typeof window.matchMedia !== 'function') return () => {};
      const list = window.matchMedia(query);
      list.addEventListener?.('change', onChange);
      return () => list.removeEventListener?.('change', onChange);
    },
    [query]
  );
  return useSyncExternalStore(
    subscribe,
    () => matchesQuery(query, fallback),
    () => fallback
  );
}
