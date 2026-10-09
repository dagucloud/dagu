// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { FlowchartType } from './Graph';

export type GraphSize = { width: number; height: number };

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

export const GRAPH_MIN_SCALE = 0.1;
export const GRAPH_MAX_SCALE = 2;
/** Mermaid draws labels at 16px, so this keeps them at roughly 10px. */
export const GRAPH_READABLE_MIN_SCALE = 0.6;
export const GRAPH_ZOOM_STEP = 1.2;
export const GRAPH_CONTENT_PADDING_PX = 32;

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

/** Fitted scale that never enlarges the drawing. */
export function fittedScale(
  drawing: GraphSize,
  viewport: GraphSize,
  fit: GraphInitialFit
): number {
  const min = fit === 'readable' ? GRAPH_READABLE_MIN_SCALE : GRAPH_MIN_SCALE;
  return clampScale(fitScale(drawing, viewport), min, 1);
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
