// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useCallback,
  useState,
  type KeyboardEvent,
  type PointerEvent,
  type ReactElement,
} from 'react';
import { writeLocalStorage } from '@/lib/local-storage-migration';
import { useI18n } from '@/i18n/I18nProvider';

/** Storage key of the width of the workflow overlay, in pixels. */
export const OVERLAY_WIDTH_KEY = 'dagu.workflowOverlay.width';
const MIN_WIDTH = 640;
/** The overlay leaves at least this much of the page below in sight. */
const MIN_ROOM_BELOW = 96;
/** The width that an arrow key adds or takes. */
const KEY_STEP = 32;

function clampWidth(width: number): number {
  const max = Math.max(MIN_WIDTH, window.innerWidth - MIN_ROOM_BELOW);
  return Math.round(Math.min(max, Math.max(MIN_WIDTH, width)));
}

function storedWidth(): number | null {
  try {
    const value = Number(window.localStorage.getItem(OVERLAY_WIDTH_KEY));
    return Number.isFinite(value) && value > 0 ? clampWidth(value) : null;
  } catch {
    // Storage can be unavailable in privacy-restricted browser contexts.
    return null;
  }
}

/**
 * The width of the workflow overlay that the user set, kept across visits;
 * null for the default width. `set` keeps it, `reset` drops it.
 */
export function useOverlayWidth(): {
  width: number | null;
  set(width: number, keep: boolean): void;
  reset(): void;
} {
  const [width, setWidth] = useState(storedWidth);
  const set = useCallback((next: number, keep: boolean) => {
    const value = clampWidth(next);
    setWidth(value);
    if (keep) writeLocalStorage(OVERLAY_WIDTH_KEY, String(value));
  }, []);
  const reset = useCallback(() => {
    setWidth(null);
    try {
      window.localStorage.removeItem(OVERLAY_WIDTH_KEY);
    } catch {
      // The default applies for this visit only.
    }
  }, []);
  return { width, set, reset };
}

/**
 * The left edge of the overlay: a drag or the arrow keys change the width
 * of the overlay, a double click gives the default back. `width` is the
 * width that shows; the edge reads it at the start of a change.
 */
export function OverlayResizeHandle({
  width,
  onResize,
  onReset,
}: {
  width: () => number;
  onResize(width: number, keep: boolean): void;
  onReset(): void;
}): ReactElement {
  const { ts } = useI18n();
  const [drag, setDrag] = useState<{
    startX: number;
    startWidth: number;
  } | null>(null);

  const onPointerDown = (event: PointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    event.preventDefault();
    event.currentTarget.setPointerCapture(event.pointerId);
    setDrag({ startX: event.clientX, startWidth: width() });
  };
  // The overlay stands at the right: a move to the left widens it.
  const widthAt = (clientX: number) =>
    drag ? drag.startWidth + drag.startX - clientX : width();
  const onPointerMove = (event: PointerEvent<HTMLDivElement>) => {
    if (drag) onResize(widthAt(event.clientX), false);
  };
  const onPointerUp = (event: PointerEvent<HTMLDivElement>) => {
    if (!drag) return;
    onResize(widthAt(event.clientX), true);
    setDrag(null);
  };
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
    event.preventDefault();
    const step = event.key === 'ArrowLeft' ? KEY_STEP : -KEY_STEP;
    onResize(width() + step, true);
  };

  return (
    <div
      role="separator"
      aria-label={ts('Resize workflow view')}
      aria-orientation="vertical"
      aria-valuenow={Math.round(width())}
      tabIndex={0}
      title={ts('Resize workflow view')}
      className="group absolute inset-y-0 left-0 z-20 hidden w-3 -translate-x-1.5 cursor-col-resize touch-none outline-none md:block"
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={() => setDrag(null)}
      onKeyDown={onKeyDown}
      onDoubleClick={onReset}
    >
      <div className="mx-auto h-full w-px bg-transparent transition-colors group-hover:bg-primary/60 group-focus-visible:bg-primary" />
    </div>
  );
}
