// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';

function findScrollContainer(element: HTMLElement): HTMLElement | null {
  for (let node = element.parentElement; node; node = node.parentElement) {
    const { overflowY } = window.getComputedStyle(node);
    if (overflowY === 'auto' || overflowY === 'scroll') {
      return node;
    }
  }
  return null;
}

/**
 * Keeps the anchor element at the same place on screen while the user works
 * in it and the content element above it changes size.
 *
 * Attach `anchorRef` to the element that should stay put and `contentRef` to
 * the resizing content. The anchor is held only while focus is inside it and
 * its top edge is in the upper half of the scroll viewport. While mounted, the
 * hook replaces browser scroll anchoring for both elements.
 */
export function useEditorScrollAnchor() {
  const [anchor, anchorRef] = React.useState<HTMLElement | null>(null);
  const [content, contentRef] = React.useState<HTMLElement | null>(null);

  React.useEffect(() => {
    if (!anchor || !content) {
      return;
    }
    const container = findScrollContainer(anchor);
    const scroller = container ?? document.scrollingElement;
    if (!scroller) {
      return;
    }
    const scrollEvents: HTMLElement | Window = container ?? window;
    const viewportMiddle = () => {
      if (!container) {
        return window.innerHeight / 2;
      }
      const rect = container.getBoundingClientRect();
      return rect.top + rect.height / 2;
    };

    // Browser scroll anchoring moves the scroll position during layout, which
    // is indistinguishable from a user scroll here. This hook takes its place.
    const restoreOverflowAnchor = [anchor, content].map((element) => {
      const previous = element.style.overflowAnchor;
      element.style.overflowAnchor = 'none';
      return () => {
        element.style.overflowAnchor = previous;
      };
    });

    // Only a content resize changes the distance from the content's top to
    // the anchor's top; scrolling and layout above the content move both.
    const gap = () =>
      anchor.getBoundingClientRect().top - content.getBoundingClientRect().top;
    let lastGap = gap();

    // The scroll position the user chose. Content that shrinks near the
    // bottom clamps the position to the new maximum before the observer runs;
    // that clamp is part of the content change, not a choice by the user.
    let restingScrollTop = scroller.scrollTop;
    const onScroll = () => {
      const scrollTop = scroller.scrollTop;
      const maxScrollTop = scroller.scrollHeight - scroller.clientHeight;
      if (scrollTop < restingScrollTop && scrollTop >= maxScrollTop - 1) {
        return;
      }
      restingScrollTop = scrollTop;
    };

    const observer = new ResizeObserver(() => {
      const nextGap = gap();
      const drift = nextGap - lastGap;
      lastGap = nextGap;
      const previousTop =
        anchor.getBoundingClientRect().top -
        drift +
        scroller.scrollTop -
        restingScrollTop;
      if (
        drift !== 0 &&
        anchor.contains(document.activeElement) &&
        previousTop < viewportMiddle()
      ) {
        scroller.scrollTop = restingScrollTop + drift;
      }
      restingScrollTop = scroller.scrollTop;
    });
    observer.observe(content);
    scrollEvents.addEventListener('scroll', onScroll, { passive: true });

    return () => {
      observer.disconnect();
      scrollEvents.removeEventListener('scroll', onScroll);
      restoreOverflowAnchor.forEach((restore) => restore());
    };
  }, [anchor, content]);

  return { anchorRef, contentRef };
}
