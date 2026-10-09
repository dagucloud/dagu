// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { FlowchartType } from './Graph';

export type GraphSize = { width: number; height: number };
export type ScreenPoint = { x: number; y: number };

/**
 * How far a newly shown graph may shrink to fit: `readable` keeps labels
 * legible, `full` shows the whole drawing.
 */
export type GraphInitialFit = 'readable' | 'full';

type ScrollMetrics = {
  scrollWidth: number;
  scrollHeight: number;
  clientWidth: number;
  clientHeight: number;
};

/** Smallest zoom reached by zooming out; a full fit may go lower. */
export const GRAPH_MIN_SCALE = 0.1;
export const GRAPH_MAX_SCALE = 2;
/** Mermaid draws labels at 16px, so this keeps them at roughly 10px. */
export const GRAPH_READABLE_MIN_SCALE = 0.6;
export const GRAPH_ZOOM_STEP = 1.2;
export const GRAPH_CONTENT_PADDING_PX = 32;
export const GRAPH_DEFAULT_HEIGHT = 380;
export const GRAPH_MIN_HEIGHT = 200;

/** Content-sized graphs grow to this share of the window height. */
const AUTO_MAX_HEIGHT_RATIO = 0.6;
/** Room for a horizontal scrollbar under a drawing wider than its viewport. */
const SCROLLBAR_ALLOWANCE_PX = 16;

/** One mouse-wheel notch zooms about 10%; trackpad pinches send smaller deltas. */
const WHEEL_MAX_DELTA_PX = 10;
const WHEEL_ZOOM_DIVISOR = 100;

export function clampScale(
  scale: number,
  min = GRAPH_MIN_SCALE,
  max = GRAPH_MAX_SCALE
): number {
  return Math.min(max, Math.max(min, scale));
}

/**
 * Largest scale at which the drawing fits inside the viewport padding, or 1
 * when either size is empty.
 */
export function fitScale(drawing: GraphSize, viewport: GraphSize): number {
  const width = viewport.width - 2 * GRAPH_CONTENT_PADDING_PX;
  const height = viewport.height - 2 * GRAPH_CONTENT_PADDING_PX;
  if (drawing.width <= 0 || drawing.height <= 0 || width <= 0 || height <= 0) {
    return 1;
  }
  return Math.min(width / drawing.width, height / drawing.height);
}

/**
 * Fitted scale that never enlarges the drawing. A readable fit stops at
 * GRAPH_READABLE_MIN_SCALE; a full fit goes as low as the drawing needs.
 */
export function fittedScale(
  drawing: GraphSize,
  viewport: GraphSize,
  fit: GraphInitialFit
): number {
  const scale = Math.min(1, fitScale(drawing, viewport));
  return fit === 'readable' ? Math.max(GRAPH_READABLE_MIN_SCALE, scale) : scale;
}

/** Tallest box a content-sized graph may take in a window of this height. */
export function autoGraphMaxHeight(windowHeight: number): number {
  return Math.max(
    GRAPH_DEFAULT_HEIGHT,
    Math.round(windowHeight * AUTO_MAX_HEIGHT_RATIO)
  );
}

/**
 * Box height that shows the drawing at `scale` without vertical scrolling,
 * limited to [GRAPH_MIN_HEIGHT, maxHeight]. `chrome` is the part of the box
 * outside the scrolling viewport.
 */
export function autoGraphHeight(
  drawing: GraphSize,
  scale: number,
  viewportWidth: number,
  chrome: number,
  maxHeight: number
): number {
  const padding = 2 * GRAPH_CONTENT_PADDING_PX;
  const scrollbar =
    drawing.width * scale + padding > viewportWidth
      ? SCROLLBAR_ALLOWANCE_PX
      : 0;
  const height = drawing.height * scale + padding + chrome + scrollbar;
  return Math.round(Math.min(maxHeight, Math.max(GRAPH_MIN_HEIGHT, height)));
}

/**
 * Zoom factor for one Ctrl/Cmd+wheel event. Line and page deltas count as a
 * full wheel notch.
 */
export function wheelZoomFactor(deltaY: number, deltaMode: number): number {
  const delta =
    deltaMode === WheelEvent.DOM_DELTA_PIXEL
      ? Math.min(WHEEL_MAX_DELTA_PX, Math.max(-WHEEL_MAX_DELTA_PX, deltaY))
      : Math.sign(deltaY) * WHEEL_MAX_DELTA_PX;
  return Math.exp(-delta / WHEEL_ZOOM_DIVISOR);
}

/**
 * Scroll offset change that puts the drawing point `point` (unscaled) back
 * under the screen coordinate `anchor` after the drawing starts at `start`
 * on screen at `scale`.
 */
export function anchorScrollDelta(
  point: number,
  scale: number,
  start: number,
  anchor: number
): number {
  return start + point * scale - anchor;
}

/**
 * Scroll position that shows where a graph begins: the top centre for
 * top-down graphs and the left middle for left-to-right graphs.
 */
export function alignedScroll(
  layout: FlowchartType,
  metrics: ScrollMetrics
): { left: number; top: number } {
  const centreLeft = Math.max(
    0,
    (metrics.scrollWidth - metrics.clientWidth) / 2
  );
  const centreTop = Math.max(
    0,
    (metrics.scrollHeight - metrics.clientHeight) / 2
  );
  return layout === 'LR'
    ? { left: 0, top: centreTop }
    : { left: centreLeft, top: 0 };
}
