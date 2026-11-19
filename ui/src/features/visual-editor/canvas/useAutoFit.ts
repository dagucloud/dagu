// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useRef, type RefObject } from 'react';

/** Delay after the last size change before the view fits again. */
export const AUTO_FIT_DELAY_MS = 150;

/**
 * Time after the element first has a size in which a fit jumps instead of
 * gliding: the parts of a page that opens (its triggers, its overlays)
 * arrive one after another, and the graph should not glide for each.
 */
export const AUTO_FIT_SETTLE_MS = 1000;

export interface AutoFit {
  /** Give to `onUserMove`: a pan or zoom by the user stops the fit. */
  onUserMove(): void;
  /**
   * A move that the host chose (for example a reveal). It stops the fit on a
   * resize, but not the fit after the graph changed.
   */
  onHostMove(): void;
  /**
   * Fits now, for a change of the graph (undo, redo, discard, a new step).
   * It does nothing after a pan or zoom by the user.
   */
  refit(): void;
  /** Fits again on each size change, for example after the fit button. */
  resume(): void;
  /** True after a pan or zoom by the user, until `resume`. */
  moved(): boolean;
  /**
   * Starts the first moments of the element again, for content that takes
   * its place as a new page would: the fits jump until they end.
   */
  settle(): void;
  /** True once the first moments of the element are over: the zoom is kept. */
  settled(): boolean;
}

/** The size of a laid out element, rounded; null before it has a layout. */
function measure(
  element: HTMLElement
): { width: number; height: number } | null {
  const width = element.clientWidth;
  const height = element.clientHeight;
  return width > 0 && height > 0 ? { width, height } : null;
}

/**
 * Fits the view again when the element of `ref` changes width, for example
 * when the inspector opens beside the canvas. A change of height alone does
 * not fit again: `onHeightChange` runs instead, so that the host can keep one
 * step in view without a new fit. A window with a width change in which the
 * element also got smaller (a panel opens) runs `onShrink` instead of the
 * fit, when the host gives it: the view then moves only as far as the graph
 * needs. After the first moments of the element, a width change in which it
 * grew runs `onGrow` in the same way, so that the zoom never changes. It
 * stops after the user pans or zooms, so that it never moves a view that the
 * user chose. `fit` learns whether the move may glide: not while the element
 * has just shown.
 */
export function useAutoFit(
  ref: RefObject<HTMLElement | null>,
  fit: (glide: boolean) => void,
  delayMs: number = AUTO_FIT_DELAY_MS,
  onHeightChange?: () => void,
  onShrink?: () => void,
  onGrow?: () => void
): AutoFit {
  const userMoved = useRef(false);
  const hostMoved = useRef(false);
  const fitRef = useRef(fit);
  fitRef.current = fit;
  const heightRef = useRef(onHeightChange);
  heightRef.current = onHeightChange;
  const shrinkRef = useRef(onShrink);
  shrinkRef.current = onShrink;
  const growRef = useRef(onGrow);
  growRef.current = onGrow;
  // When the element first had a size.
  const shownAt = useRef<number | null>(null);
  const settled = useCallback(() => {
    const since = shownAt.current;
    return since !== null && Date.now() - since >= AUTO_FIT_SETTLE_MS;
  }, []);
  const runFit = useCallback(() => fitRef.current(settled()), [settled]);

  useEffect(() => {
    const element = ref.current;
    if (!element || typeof ResizeObserver === 'undefined') {
      return;
    }
    let timer: ReturnType<typeof setTimeout> | undefined;
    // The size that the flow fitted at its first layout. A size that changes
    // before the first report, such as a panel restored from memory, fits
    // again too.
    let size = measure(element);
    if (size) shownAt.current ??= Date.now();
    let pendingFit = false;
    let pendingShrink = false;
    const observer = new ResizeObserver(([entry]) => {
      if (!entry) {
        return;
      }
      const width = Math.round(entry.contentRect.width);
      const height = Math.round(entry.contentRect.height);
      if (width > 0 && height > 0) shownAt.current ??= Date.now();
      // The first report is the initial size, which the flow already fits.
      // A change of height alone does not fit again: a dock that opens or
      // closes must not move the graph.
      const widthChanged = size !== null && size.width !== width;
      const heightChanged = size !== null && size.height !== height;
      const shrunk =
        size !== null && (width < size.width || height < size.height);
      size = { width, height };
      if (!widthChanged && !heightChanged) {
        return;
      }
      // A width change in the pending window makes the whole change a fit.
      pendingFit = pendingFit || widthChanged;
      pendingShrink = pendingShrink || shrunk;
      clearTimeout(timer);
      timer = setTimeout(() => {
        const refit = pendingFit;
        const shrink = pendingShrink;
        pendingFit = false;
        pendingShrink = false;
        if (userMoved.current || hostMoved.current) {
          return;
        }
        if (refit && shrink && shrinkRef.current) {
          shrinkRef.current();
        } else if (refit && growRef.current && settled()) {
          growRef.current();
        } else if (refit) {
          runFit();
        } else {
          heightRef.current?.();
        }
      }, delayMs);
    });
    observer.observe(element);
    return () => {
      observer.disconnect();
      clearTimeout(timer);
    };
  }, [ref, delayMs, runFit, settled]);

  const onUserMove = useCallback(() => {
    userMoved.current = true;
  }, []);
  const onHostMove = useCallback(() => {
    hostMoved.current = true;
  }, []);
  const resume = useCallback(() => {
    userMoved.current = false;
    hostMoved.current = false;
  }, []);
  const refit = useCallback(() => {
    if (!userMoved.current) {
      hostMoved.current = false;
      runFit();
    }
  }, [runFit]);

  const moved = useCallback(() => userMoved.current, []);
  const settle = useCallback(() => {
    shownAt.current = Date.now();
  }, []);

  return { onUserMove, onHostMove, resume, refit, moved, settle, settled };
}
