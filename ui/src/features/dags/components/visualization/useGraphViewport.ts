// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { flushSync } from 'react-dom';
import type { FlowchartType } from './Graph';
import {
  alignedScroll,
  anchorScrollDelta,
  clampScale,
  fittedScale,
  GRAPH_ZOOM_STEP,
  type GraphInitialFit,
  type GraphSize,
  wheelZoomFactor,
} from './graphViewport';

type Options = {
  /** Scrollable element that contains the rendered SVG. */
  viewportRef: React.RefObject<HTMLDivElement | null>;
  layout: FlowchartType;
  /** Changes whenever the graph's shape changes; each change fits again. */
  structureKey: string;
  initialFit: GraphInitialFit;
};

type ScreenPoint = { x: number; y: number };

/** Safari's non-standard trackpad pinch event. */
type GestureEvent = UIEvent & {
  scale: number;
  clientX: number;
  clientY: number;
};

type Drag = {
  pointerId: number;
  start: ScreenPoint;
  scrollLeft: number;
  scrollTop: number;
  moved: boolean;
};

/** Mouse travel before a press becomes a pan instead of a click. */
const DRAG_THRESHOLD_PX = 4;
const PANNING_ATTRIBUTE = 'data-graph-panning';

/**
 * Zoom and pan state for a rendered graph. The graph is fitted to its
 * viewport when it first renders and whenever its structure changes;
 * status-only updates keep the user's zoom and scroll position.
 */
export function useGraphViewport({
  viewportRef,
  layout,
  structureKey,
  initialFit,
}: Options) {
  const [scale, setScale] = React.useState(1);
  const scaleRef = React.useRef(1);
  const contentSizeRef = React.useRef<GraphSize | null>(null);
  const fitPendingRef = React.useRef(true);
  const optionsRef = React.useRef({ layout, initialFit });

  React.useEffect(() => {
    optionsRef.current = { layout, initialFit };
  }, [layout, initialFit]);

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
    (fit: GraphInitialFit) => {
      const viewport = viewportRef.current;
      const drawing = contentSizeRef.current;
      if (
        !viewport ||
        !drawing ||
        viewport.clientWidth === 0 ||
        viewport.clientHeight === 0
      ) {
        return false;
      }
      commitScale(
        fittedScale(
          drawing,
          { width: viewport.clientWidth, height: viewport.clientHeight },
          fit
        )
      );
      const { left, top } = alignedScroll(optionsRef.current.layout, viewport);
      viewport.scrollLeft = left;
      viewport.scrollTop = top;
      return true;
    },
    [commitScale, viewportRef]
  );

  const fitIfPending = React.useCallback(() => {
    if (fitPendingRef.current && fitTo(optionsRef.current.initialFit)) {
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
      const next = clampScale(target);
      const previous = scaleRef.current;
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

  // Ctrl/Cmd+wheel and pinch zoom at the cursor; a mouse drag pans. Plain
  // wheel and touch keep native scrolling so the page never gets trapped.
  React.useEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport) {
      return;
    }
    let gestureStartScale: number | null = null;
    let drag: Drag | null = null;
    let suppressClick = false;

    const onWheel = (event: WheelEvent) => {
      if (!event.ctrlKey && !event.metaKey) {
        return;
      }
      event.preventDefault();
      if (gestureStartScale !== null) {
        return;
      }
      zoomTo(
        scaleRef.current * wheelZoomFactor(event.deltaY, event.deltaMode),
        {
          x: event.clientX,
          y: event.clientY,
        }
      );
    };

    // Safari reports trackpad pinch as gesture events instead of Ctrl+wheel.
    const onGestureStart = (event: Event) => {
      event.preventDefault();
      gestureStartScale = scaleRef.current;
    };
    const onGestureChange = (event: Event) => {
      event.preventDefault();
      if (gestureStartScale === null) {
        return;
      }
      const gesture = event as GestureEvent;
      zoomTo(gestureStartScale * gesture.scale, {
        x: gesture.clientX,
        y: gesture.clientY,
      });
    };
    const onGestureEnd = () => {
      gestureStartScale = null;
    };

    const onPointerDown = (event: PointerEvent) => {
      suppressClick = false;
      if (event.pointerType !== 'mouse' || event.button !== 0) {
        return;
      }
      // Presses on the scrollbars keep their native behaviour.
      const bounds = viewport.getBoundingClientRect();
      if (
        event.clientX - bounds.left >= viewport.clientWidth ||
        event.clientY - bounds.top >= viewport.clientHeight
      ) {
        return;
      }
      drag = {
        pointerId: event.pointerId,
        start: { x: event.clientX, y: event.clientY },
        scrollLeft: viewport.scrollLeft,
        scrollTop: viewport.scrollTop,
        moved: false,
      };
    };
    const onPointerMove = (event: PointerEvent) => {
      if (!drag || event.pointerId !== drag.pointerId) {
        return;
      }
      const dx = event.clientX - drag.start.x;
      const dy = event.clientY - drag.start.y;
      if (!drag.moved) {
        if (Math.hypot(dx, dy) < DRAG_THRESHOLD_PX) {
          return;
        }
        drag.moved = true;
        viewport.setPointerCapture(event.pointerId);
        viewport.setAttribute(PANNING_ATTRIBUTE, '');
      }
      viewport.scrollLeft = drag.scrollLeft - dx;
      viewport.scrollTop = drag.scrollTop - dy;
    };
    const onPointerEnd = (event: PointerEvent) => {
      if (!drag || event.pointerId !== drag.pointerId) {
        return;
      }
      suppressClick = drag.moved;
      viewport.removeAttribute(PANNING_ATTRIBUTE);
      drag = null;
    };
    // Capture phase runs before React's root listener, so the click that
    // ends a pan never selects a node.
    const onClickCapture = (event: MouseEvent) => {
      if (suppressClick) {
        suppressClick = false;
        event.preventDefault();
        event.stopPropagation();
      }
    };

    viewport.addEventListener('wheel', onWheel, { passive: false });
    viewport.addEventListener('gesturestart', onGestureStart);
    viewport.addEventListener('gesturechange', onGestureChange);
    viewport.addEventListener('gestureend', onGestureEnd);
    viewport.addEventListener('pointerdown', onPointerDown);
    viewport.addEventListener('pointermove', onPointerMove);
    viewport.addEventListener('pointerup', onPointerEnd);
    viewport.addEventListener('pointercancel', onPointerEnd);
    viewport.addEventListener('click', onClickCapture, true);
    return () => {
      viewport.removeEventListener('wheel', onWheel);
      viewport.removeEventListener('gesturestart', onGestureStart);
      viewport.removeEventListener('gesturechange', onGestureChange);
      viewport.removeEventListener('gestureend', onGestureEnd);
      viewport.removeEventListener('pointerdown', onPointerDown);
      viewport.removeEventListener('pointermove', onPointerMove);
      viewport.removeEventListener('pointerup', onPointerEnd);
      viewport.removeEventListener('pointercancel', onPointerEnd);
      viewport.removeEventListener('click', onClickCapture, true);
      viewport.removeAttribute(PANNING_ATTRIBUTE);
    };
  }, [viewportRef, zoomTo]);

  return {
    scale,
    handleContentSize,
    zoomIn: () => zoomTo(scaleRef.current * GRAPH_ZOOM_STEP),
    zoomOut: () => zoomTo(scaleRef.current / GRAPH_ZOOM_STEP),
    resetZoom: () => zoomTo(1),
    fitToView: () => {
      fitTo('full');
    },
  };
}
