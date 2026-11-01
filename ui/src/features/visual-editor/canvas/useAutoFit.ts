// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useRef, type RefObject } from 'react';

/** Delay after the last size change before the view fits again. */
export const AUTO_FIT_DELAY_MS = 150;

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

/**
 * Fits the view again when the element of `ref` changes width, for example
 * when the inspector opens beside the canvas. A change of height alone does
 * not fit again: `onHeightChange` runs instead, so that the host can keep one
 * step in view without a new fit. It stops after the user pans or zooms, so that it never
 * moves a view that the user chose.
 */
export function useAutoFit(
  ref: RefObject<HTMLElement | null>,
  fit: () => void,
  delayMs: number = AUTO_FIT_DELAY_MS,
  onHeightChange?: () => void
): AutoFit {
  const userMoved = useRef(false);
  const hostMoved = useRef(false);
  const fitRef = useRef(fit);
  fitRef.current = fit;
  const heightRef = useRef(onHeightChange);
  heightRef.current = onHeightChange;

  useEffect(() => {
    const element = ref.current;
    if (!element || typeof ResizeObserver === 'undefined') {
      return;
    }
    let timer: ReturnType<typeof setTimeout> | undefined;
    let size: { width: number; height: number } | null = null;
    let pendingFit = false;
    const observer = new ResizeObserver(([entry]) => {
      if (!entry) {
        return;
      }
      const { width, height } = entry.contentRect;
      // The first report is the initial size, which the flow already fits.
      // A change of height alone does not fit again: a dock that opens or
      // closes must not move the graph.
      const widthChanged = size !== null && size.width !== width;
      const heightChanged = size !== null && size.height !== height;
      size = { width, height };
      if (!widthChanged && !heightChanged) {
        return;
      }
      // A width change in the pending window makes the whole change a fit.
      pendingFit = pendingFit || widthChanged;
      clearTimeout(timer);
      timer = setTimeout(() => {
        const refit = pendingFit;
        pendingFit = false;
        if (userMoved.current || hostMoved.current) {
          return;
        }
        if (refit) {
          fitRef.current();
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
  }, [ref, delayMs]);

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
      fitRef.current();
    }
  }, []);

  const moved = useCallback(() => userMoved.current, []);

  return { onUserMove, onHostMove, resume, refit, moved };
}
