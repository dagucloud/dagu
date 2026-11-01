// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useLayoutEffect, useState, type RefObject } from 'react';

/**
 * The width of the element, current while it resizes. null before the first
 * measure and where the element has no layout (jsdom).
 */
export function useElementWidth(
  ref: RefObject<HTMLElement | null>
): number | null {
  const [width, setWidth] = useState<number | null>(null);
  useLayoutEffect(() => {
    const element = ref.current;
    if (!element) {
      setWidth(null);
      return;
    }
    // The border box, on the first measure and on each resize alike.
    const read = () => {
      const next = element.getBoundingClientRect().width;
      setWidth(next > 0 ? next : null);
    };
    read();
    if (typeof ResizeObserver !== 'function') return;
    const observer = new ResizeObserver(read);
    observer.observe(element);
    return () => observer.disconnect();
  }, [ref]);
  return width;
}
