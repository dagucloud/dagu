// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { type CSSProperties, useLayoutEffect, useState } from 'react';

/** Width of the fade at an edge that has more content beyond it. */
const FADE_PX = 24;

type Edges = { start: boolean; end: boolean };

function fadeMask(axis: 'x' | 'y', { start, end }: Edges): string | undefined {
  if (!start && !end) return undefined;
  const direction = axis === 'x' ? 'to right' : 'to bottom';
  const head = start ? `transparent, #000 ${FADE_PX}px` : '#000';
  const tail = end ? `#000 calc(100% - ${FADE_PX}px), transparent` : '#000';
  return `linear-gradient(${direction}, ${head}, ${tail})`;
}

/**
 * Fades the edges of a scroll container that hide more content, so a row
 * of tabs or a list shows it can scroll even where scrollbars are hidden.
 * Returns the ref to attach, which also works on an element that mounts
 * later, and the style that draws the fades.
 */
export function useScrollFade<T extends HTMLElement>(
  axis: 'x' | 'y'
): [(element: T | null) => void, CSSProperties] {
  const [element, setElement] = useState<T | null>(null);
  const [edges, setEdges] = useState<Edges>({ start: false, end: false });

  useLayoutEffect(() => {
    if (!element) return;
    const update = () => {
      const position = axis === 'x' ? element.scrollLeft : element.scrollTop;
      const hidden =
        axis === 'x'
          ? element.scrollWidth - element.clientWidth
          : element.scrollHeight - element.clientHeight;
      const next = { start: position > 1, end: hidden - position > 1 };
      setEdges((current) =>
        current.start === next.start && current.end === next.end
          ? current
          : next
      );
    };
    update();
    element.addEventListener('scroll', update, { passive: true });
    let observer: ResizeObserver | undefined;
    if (typeof ResizeObserver !== 'undefined') {
      observer = new ResizeObserver(update);
      observer.observe(element);
      for (const child of Array.from(element.children)) {
        observer.observe(child);
      }
    }
    // A child added later, such as a tab that needs a loaded user, can make
    // the row overflow without resizing the row itself.
    let additions: MutationObserver | undefined;
    if (typeof MutationObserver !== 'undefined') {
      additions = new MutationObserver((records) => {
        for (const record of records) {
          for (const node of Array.from(record.addedNodes)) {
            if (node instanceof Element) observer?.observe(node);
          }
        }
        update();
      });
      additions.observe(element, { childList: true });
    }
    return () => {
      element.removeEventListener('scroll', update);
      observer?.disconnect();
      additions?.disconnect();
    };
  }, [axis, element]);

  const mask = fadeMask(axis, edges);
  return [setElement, mask ? { maskImage: mask, WebkitMaskImage: mask } : {}];
}
