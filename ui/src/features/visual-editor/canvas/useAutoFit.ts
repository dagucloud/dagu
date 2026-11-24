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
 * Fits the view again when the element of `ref` changes size: an inspector
 * that opens beside the canvas, a dock that opens under it, a window that
 * resizes. After the first moments of the element, the host can keep the zoom
 * instead: a change that left the element smaller on one axis runs `onShrink`,
 * any other change runs `onGrow`, and the fit runs only for the one that the
 * host does not give. It stops after the user pans or zooms, so that it never
 * moves a view that the user chose. `fit` learns whether the move may glide:
 * not while the element has just shown.
 */
export function useAutoFit(
  ref: RefObject<HTMLElement | null>,
  fit: (glide: boolean) => void,
  delayMs: number = AUTO_FIT_DELAY_MS,
  onShrink?: () => void,
  onGrow?: () => void
): AutoFit {
  const userMoved = useRef(false);
  const hostMoved = useRef(false);
  const fitRef = useRef(fit);
  fitRef.current = fit;
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
    // The size before the changes that wait for the delay: a panel that
    // gives its place to another one counts as one change.
    let before: typeof size = null;
    const observer = new ResizeObserver(([entry]) => {
      if (!entry) {
        return;
      }
      const width = Math.round(entry.contentRect.width);
      const height = Math.round(entry.contentRect.height);
      if (width > 0 && height > 0) shownAt.current ??= Date.now();
      const last = size;
      size = { width, height };
      // The first report is the initial size, which the flow already fits.
      if (!last || (last.width === width && last.height === height)) {
        return;
      }
      before ??= last;
      clearTimeout(timer);
      timer = setTimeout(() => {
        const from = before;
        before = null;
        if (!from || userMoved.current || hostMoved.current) {
          return;
        }
        if (from.width === width && from.height === height) {
          return;
        }
        const shrunk = width < from.width || height < from.height;
        // While the element has just shown, its room is not final: the view
        // fits each change.
        const keep = settled()
          ? shrunk
            ? shrinkRef.current
            : growRef.current
          : undefined;
        if (keep) {
          keep();
        } else {
          runFit();
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
