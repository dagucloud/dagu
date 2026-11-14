// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useLayoutEffect, useState, type RefObject } from 'react';

/**
 * The width of the element, kept current while it resizes. Before a
 * measurement (and where the element has no layout, as in jsdom) it is the
 * width of the window.
 */
export function useZoneWidth(ref: RefObject<HTMLElement | null>): number {
  const [width, setWidth] = useState<number | null>(null);
  const [viewport, setViewport] = useState(() => window.innerWidth);
  useLayoutEffect(() => {
    const element = ref.current;
    const read = () => {
      setViewport(window.innerWidth);
      const next = element?.getBoundingClientRect().width ?? 0;
      setWidth(next > 0 ? Math.round(next) : null);
    };
    read();
    window.addEventListener('resize', read);
    if (!element || typeof ResizeObserver !== 'function') {
      return () => window.removeEventListener('resize', read);
    }
    const observer = new ResizeObserver(read);
    observer.observe(element);
    return () => {
      window.removeEventListener('resize', read);
      observer.disconnect();
    };
  }, [ref]);
  return width ?? viewport;
}
