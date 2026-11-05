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
 * resizes. It stops after the user pans or zooms, so that it never moves a
 * view that the user chose. `fit` learns whether the move may glide: not
 * while the element has just shown.
 */
export function useAutoFit(
  ref: RefObject<HTMLElement | null>,
  fit: (glide: boolean) => void,
  delayMs: number = AUTO_FIT_DELAY_MS
): AutoFit {
  const userMoved = useRef(false);
  const hostMoved = useRef(false);
  const fitRef = useRef(fit);
  fitRef.current = fit;
  // When the element first had a size.
  const shownAt = useRef<number | null>(null);
  const runFit = useCallback(() => {
    const since = shownAt.current;
    fitRef.current(since !== null && Date.now() - since >= AUTO_FIT_SETTLE_MS);
  }, []);

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
    const observer = new ResizeObserver(([entry]) => {
      if (!entry) {
        return;
      }
      const width = Math.round(entry.contentRect.width);
      const height = Math.round(entry.contentRect.height);
      if (width > 0 && height > 0) shownAt.current ??= Date.now();
      const changed =
        size !== null && (size.width !== width || size.height !== height);
      size = { width, height };
      if (!changed) {
        return;
      }
      clearTimeout(timer);
      timer = setTimeout(() => {
        if (!userMoved.current && !hostMoved.current) {
          runFit();
        }
      }, delayMs);
    });
    observer.observe(element);
    return () => {
      observer.disconnect();
      clearTimeout(timer);
    };
  }, [ref, delayMs, runFit]);

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

  return { onUserMove, onHostMove, resume, refit, moved };
}
