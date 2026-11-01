// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { type RefObject, useLayoutEffect, useRef, useState } from 'react';

/**
 * Returns a ref for a scroll container and the tabIndex it should carry:
 * 0 while its content overflows, so keyboard users can scroll it, and
 * undefined otherwise, so it adds no Tab stop.
 */
export function useOverflowFocusable<T extends HTMLElement>(): [
  RefObject<T | null>,
  number | undefined,
] {
  const ref = useRef<T>(null);
  const [overflowing, setOverflowing] = useState(false);

  useLayoutEffect(() => {
    const element = ref.current;
    if (!element || typeof ResizeObserver === 'undefined') {
      return;
    }
    const update = () =>
      setOverflowing(
        element.scrollWidth > element.clientWidth + 1 ||
          element.scrollHeight > element.clientHeight + 1
      );
    update();
    const observer = new ResizeObserver(update);
    observer.observe(element);
    for (const child of Array.from(element.children)) {
      observer.observe(child);
    }
    return () => observer.disconnect();
  }, []);

  return [ref, overflowing ? 0 : undefined];
}
