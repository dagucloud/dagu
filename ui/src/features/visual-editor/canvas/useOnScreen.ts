// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useRef, useState } from 'react';

/**
 * True once the element has been on screen. The DAGs card asks the server for
 * a DAG only then, so that a canvas with many steps loads only what it
 * shows. The value never goes back to false: the miniature stays when the
 * user pans away. Without `IntersectionObserver` (a test) the element counts
 * as on screen.
 */
export function useOnScreen<T extends Element>(): [
  (element: T | null) => void,
  boolean,
] {
  const [seen, setSeen] = useState(
    () => typeof IntersectionObserver === 'undefined'
  );
  const observer = useRef<IntersectionObserver | null>(null);
  // React calls the ref with null when the element goes away: that ends the watch.
  const ref = useCallback((element: T | null) => {
    observer.current?.disconnect();
    observer.current = null;
    if (!element || typeof IntersectionObserver === 'undefined') return;
    const watcher = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) {
        setSeen(true);
        watcher.disconnect();
      }
    });
    watcher.observe(element);
    observer.current = watcher;
  }, []);
  return [ref, seen];
}
