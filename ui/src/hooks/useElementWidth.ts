// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useLayoutEffect, useState, type RefObject } from 'react';

/** The width of the content box: the same value as a ResizeObserver reports. */
function contentWidth(element: HTMLElement): number {
  const style = getComputedStyle(element);
  return (
    element.clientWidth -
    (parseFloat(style.paddingLeft) || 0) -
    (parseFloat(style.paddingRight) || 0)
  );
}

/**
 * The width of the content box of the element, current while it resizes.
 * null before the first measure and where the element has no layout (jsdom).
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
    const read = (next: number) => setWidth(next > 0 ? next : null);
    read(contentWidth(element));
    if (typeof ResizeObserver !== 'function') return;
    const observer = new ResizeObserver((entries) => {
      const entry = entries[entries.length - 1];
      read(entry ? entry.contentRect.width : contentWidth(element));
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, [ref]);
  return width;
}
