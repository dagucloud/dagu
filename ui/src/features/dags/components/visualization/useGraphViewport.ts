// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { flushSync } from 'react-dom';
import type { FlowchartType } from './Graph';
import {
  alignedScroll,
  anchorScrollDelta,
  autoGraphHeight,
  autoGraphMaxHeight,
  clampScale,
  fittedScale,
  GRAPH_MIN_SCALE,
  GRAPH_ZOOM_STEP,
  type GraphInitialFit,
  type GraphSize,
  type ScreenPoint,
} from './graphViewport';
import { useGraphGestures } from './useGraphGestures';

type Options = {
  /** Graph box whose height includes the viewport and any inset around it. */
  boxRef: React.RefObject<HTMLDivElement | null>;
  /** Scrollable element that contains the rendered SVG. */
  viewportRef: React.RefObject<HTMLDivElement | null>;
  layout: FlowchartType;
  /** Changes whenever the graph's shape changes; each change fits again. */
  structureKey: string;
  initialFit: GraphInitialFit;
  /** Whether the box height follows `autoHeight` rather than a fixed height. */
  sizeToContent: boolean;
};

/**
 * Zoom and pan state for a rendered graph. The graph is fitted to its
 * viewport when it first renders and whenever its structure changes;
 * status-only updates keep the user's zoom and scroll position.
 *
 * Each fit also computes `autoHeight`, a box height that shows the fitted
 * drawing without vertical scrolling, up to a share of the window height.
 */
export function useGraphViewport({
  boxRef,
  viewportRef,
  layout,
  structureKey,
  initialFit,
  sizeToContent,
}: Options) {
  const [scale, setScale] = React.useState(1);
  const [autoHeight, setAutoHeight] = React.useState<number>();
  const scaleRef = React.useRef(1);
  const contentSizeRef = React.useRef<GraphSize | null>(null);
  const fitPendingRef = React.useRef(true);
  const optionsRef = React.useRef({ layout, initialFit, sizeToContent });

  React.useEffect(() => {
    optionsRef.current = { layout, initialFit, sizeToContent };
  }, [layout, initialFit, sizeToContent]);

  React.useEffect(() => {
    fitPendingRef.current = true;
    // The previous drawing's size must not be fitted while the new one renders.
    contentSizeRef.current = null;
  }, [structureKey]);

  // Commits synchronously so the caller can read the new layout and scroll
  // before the browser paints the intermediate zoom.
  const commitScale = React.useCallback((next: number) => {
    scaleRef.current = next;
    flushSync(() => setScale(next));
  }, []);

  const fitTo = React.useCallback(
    (fit: GraphInitialFit, sizeBox: boolean) => {
      const box = boxRef.current;
      const viewport = viewportRef.current;
      const drawing = contentSizeRef.current;
      if (
        !box ||
        !viewport ||
        !drawing ||
        viewport.clientWidth === 0 ||
        viewport.clientHeight === 0
      ) {
        return false;
      }
      const width = viewport.clientWidth;
      // Box height outside the viewport, such as the toolbar inset on
      // narrow screens.
      const chrome = box.clientHeight - viewport.offsetHeight;
      const maxHeight = autoGraphMaxHeight(window.innerHeight);
      // A content-sized box can still grow, so fit against its largest size.
      const height =
        sizeBox && optionsRef.current.sizeToContent
          ? maxHeight - chrome
          : viewport.clientHeight;
      const next = fittedScale(drawing, { width, height }, fit);
      scaleRef.current = next;
      flushSync(() => {
        setScale(next);
        if (sizeBox) {
          setAutoHeight(
            autoGraphHeight(drawing, next, width, chrome, maxHeight)
          );
        }
      });
      const { left, top } = alignedScroll(optionsRef.current.layout, viewport);
      viewport.scrollLeft = left;
      viewport.scrollTop = top;
      return true;
    },
    [boxRef, viewportRef]
  );

  const fitIfPending = React.useCallback(() => {
    if (fitPendingRef.current && fitTo(optionsRef.current.initialFit, true)) {
      fitPendingRef.current = false;
    }
  }, [fitTo]);

  const handleContentSize = React.useCallback(
    (size: GraphSize | null) => {
      contentSizeRef.current = size;
      fitIfPending();
    },
    [fitIfPending]
  );

  // A graph mounted while hidden has no size yet; fit once it is laid out.
  React.useEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport || typeof ResizeObserver === 'undefined') {
      return;
    }
    let frame = 0;
    const observer = new ResizeObserver(() => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(fitIfPending);
    });
    observer.observe(viewport);
    return () => {
      observer.disconnect();
      cancelAnimationFrame(frame);
    };
  }, [fitIfPending, viewportRef]);

  /** Zooms while keeping the drawing under `anchor` (default: viewport centre) in place. */
  const zoomTo = React.useCallback(
    (target: number, anchor?: ScreenPoint) => {
      const previous = scaleRef.current;
      // After a full fit below the floor, zooming out stays put instead of
      // jumping back up to the floor.
      const next = clampScale(target, Math.min(GRAPH_MIN_SCALE, previous));
      if (next === previous) {
        return;
      }
      const viewport = viewportRef.current;
      const svg = viewport?.querySelector('svg');
      if (!viewport || !svg) {
        commitScale(next);
        return;
      }
      const bounds = viewport.getBoundingClientRect();
      const x = anchor?.x ?? bounds.left + viewport.clientWidth / 2;
      const y = anchor?.y ?? bounds.top + viewport.clientHeight / 2;
      const before = svg.getBoundingClientRect();
      const point = {
        x: (x - before.left) / previous,
        y: (y - before.top) / previous,
      };
      commitScale(next);
      const after = svg.getBoundingClientRect();
      viewport.scrollLeft += anchorScrollDelta(point.x, next, after.left, x);
      viewport.scrollTop += anchorScrollDelta(point.y, next, after.top, y);
    },
    [commitScale, viewportRef]
  );

  useGraphGestures({ viewportRef, scaleRef, zoomTo });

  return {
    scale,
    autoHeight,
    handleContentSize,
    zoomIn: () => zoomTo(scaleRef.current * GRAPH_ZOOM_STEP),
    zoomOut: () => zoomTo(scaleRef.current / GRAPH_ZOOM_STEP),
    resetZoom: () => zoomTo(1),
    fitToView: () => {
      fitTo('full', false);
    },
  };
}
