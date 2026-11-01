// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { createContext, useContext, useMemo } from 'react';
import { useElementWidth } from '@/hooks/useElementWidth';

/**
 * The row at the top of a graph zone that holds the toolbars of the canvas.
 * `undefined`: no host, the toolbars float over the canvas. `null`: the host
 * row is not mounted yet. An element: the toolbars render in it.
 */
export const CanvasToolbarHostContext = createContext<
  HTMLElement | null | undefined
>(undefined);

/** The row of the toolbars that the zone gives, if any. */
export function useCanvasToolbarHost(): HTMLElement | null | undefined {
  return useContext(CanvasToolbarHostContext);
}

/**
 * Classes of a graph zone with a row for the toolbars: it sets the height of
 * the row in --ww-toolbar-row, where the right panels read the top that they
 * start at. The row floats over the canvas, which fills the whole zone; the
 * panels start under it. Below md the row is hidden and the variable is 0.
 */
export const TOOLBAR_ZONE_CLASS =
  '[--ww-toolbar-row:2.5rem] max-md:[--ww-toolbar-row:0px]';

/**
 * Classes of the row of the toolbars: one line of 40px over the top of the
 * zone, never wrapped, with no background. The row lets a click through to
 * the canvas; each group takes its own (see TOOLBAR_GROUP_CLASS).
 */
export const TOOLBAR_ROW_CLASS =
  'pointer-events-none absolute inset-x-0 top-0 z-10 flex h-10 min-w-0 items-center justify-between gap-2 px-2 max-md:hidden';

/**
 * With a right panel docked in the zone, the row goes above the panel (z 60),
 * so that its controls sit over the top rows of the one box that the panel is.
 */
export const TOOLBAR_ROW_OVER_PANEL_CLASS = 'z-[65]';

/**
 * Classes of a group of toolbar controls in the row: it takes the pointer, has
 * no background, border or shadow of its own, and each of its buttons floats
 * with its own border and a translucent card background. There is no blur:
 * the canvas moves under the row, and a blur would repaint at each frame. A
 * button with a fill of its own (Save, a pressed toggle) keeps it. Literal
 * strings, so that Tailwind finds them.
 */
export const TOOLBAR_GROUP_CLASS = [
  'pointer-events-auto',
  '[&_button:not([class*=primary-fill]):not([class*=bg-destructive]):not([aria-pressed=true])]:border',
  '[&_button:not([class*=primary-fill]):not([class*=bg-destructive]):not([aria-pressed=true])]:border-border',
  '[&_button:not([class*=primary-fill]):not([class*=bg-destructive]):not([aria-pressed=true])]:bg-card/90',
  '[&_button:not([class*=primary-fill]):not([class*=bg-destructive]):not([aria-pressed=true]):hover]:bg-muted',
  '[&_button[aria-pressed=true]]:border',
  '[&_button[aria-pressed=true]]:border-border',
  '[&_[aria-hidden=true].w-px]:hidden',
].join(' ');

/** The width of the host row, kept current; null while unknown. */
export function useToolbarHostWidth(
  host: HTMLElement | null | undefined
): number | null {
  const ref = useMemo(() => ({ current: host ?? null }), [host]);
  return useElementWidth(ref);
}
