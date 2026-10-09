// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { type ScreenPoint, wheelZoomFactor } from './graphViewport';

type Options = {
  /** Scrollable element that contains the rendered SVG. */
  viewportRef: React.RefObject<HTMLDivElement | null>;
  /** Current zoom, read when a gesture starts. */
  scaleRef: React.RefObject<number>;
  /** Zooms to `scale`, keeping the drawing under `anchor` in place. */
  zoomTo: (scale: number, anchor: ScreenPoint) => void;
};

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
/** `PointerEvent.buttons` bit for the primary (left) button. */
const PRIMARY_BUTTON = 1;

/**
 * Gesture input for a graph viewport: Ctrl/Cmd+wheel and pinch zoom at the
 * cursor, and a mouse drag pans. Plain wheel and touch keep native scrolling
 * so the page never gets trapped.
 */
export function useGraphGestures({
  viewportRef,
  scaleRef,
  zoomTo,
}: Options): void {
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
      // A release outside the graph never reaches it before the pointer is
      // captured, so a move without the primary button ends the press.
      if ((event.buttons & PRIMARY_BUTTON) === 0) {
        drag = null;
        viewport.removeAttribute(PANNING_ATTRIBUTE);
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
  }, [scaleRef, viewportRef, zoomTo]);
}
