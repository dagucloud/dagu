// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Insets, Rect } from './engine/geometry';

/** Part of the visible length, centred, in which a revealed card stays put. */
export const REVEAL_ZONE = 0.6;

/**
 * Pan on one axis, in px, that shows a card of `length` px at `start` in the
 * visible span `[from, to]`.
 * - The card does not touch the span, or it is longer than the span: its
 *   centre goes to the centre of the span.
 * - The card is in the span and its centre is in the central zone: no pan.
 * - Otherwise the pan is the least one that puts the centre in the zone and
 *   keeps the whole card in the span.
 */
function axisDelta(
  start: number,
  length: number,
  from: number,
  to: number
): number {
  const span = to - from;
  const middle = (from + to) / 2;
  const centre = start + length / 2;
  if (length >= span || start >= to || start + length <= from) {
    return middle - centre;
  }
  const half = length / 2;
  const reach = (span * REVEAL_ZONE) / 2;
  const low = Math.max(middle - reach, from + half);
  const high = Math.min(middle + reach, to - half);
  if (centre < low) return low - centre;
  if (centre > high) return high - centre;
  return 0;
}

/**
 * The pan that a reveal of a card needs, as the px to add to the view
 * translation. `nodeRect` and `viewRect` are in the same screen space.
 * `obstructions` are the px that a toolbar, a dock or a panel covers at each
 * side of `viewRect`: the visible area is what they leave free. The zoom
 * never changes. A card that sits in the central zone of the visible area
 * gives `{ dx: 0, dy: 0 }`.
 */
export function revealDelta(
  nodeRect: Rect,
  viewRect: Rect,
  obstructions: Insets
): { dx: number; dy: number } {
  const left = viewRect.x + obstructions.left;
  const top = viewRect.y + obstructions.top;
  // An obstruction larger than the canvas leaves a 1 px area.
  const right = Math.max(
    viewRect.x + viewRect.width - obstructions.right,
    left + 1
  );
  const bottom = Math.max(
    viewRect.y + viewRect.height - obstructions.bottom,
    top + 1
  );
  return {
    dx: axisDelta(nodeRect.x, nodeRect.width, left, right),
    dy: axisDelta(nodeRect.y, nodeRect.height, top, bottom),
  };
}

/** Gap in px that a graph wider than the area may keep at its start edge. */
const START_GAP = 24;

/**
 * Pan on one axis, in px, that keeps content of `length` px at `start` in the
 * span `[from, to]`: none when it is there, else the least pan.
 * Content longer than the span keeps its start side: it moves only to cover
 * the span, or to close a gap at its start of more than `START_GAP`.
 */
function keepAxisDelta(
  start: number,
  length: number,
  from: number,
  to: number
): number {
  // Content that fits may start from the span start to the last place where
  // it still ends in the span. Longer content may start from the place where
  // it ends at the span end to the span start plus the gap.
  const fits = length <= to - from;
  const first = fits ? from : to - length;
  const last = fits ? to - length : from + START_GAP;
  const target = Math.min(Math.max(start, first), last);
  return target - start;
}

/**
 * The pan that keeps a graph in view after the canvas shrank or a panel
 * covered a part of it, as the px to add to the view translation. `bounds`
 * and `viewRect` are in the same screen space; `obstructions` are the px that
 * a toolbar or a panel covers at each side of `viewRect`. The zoom never
 * changes. A graph that is already in the free area gives `{ dx: 0, dy: 0 }`.
 */
export function keepInViewDelta(
  bounds: Rect,
  viewRect: Rect,
  obstructions: Insets
): { dx: number; dy: number } {
  const left = viewRect.x + obstructions.left;
  const top = viewRect.y + obstructions.top;
  const right = Math.max(
    viewRect.x + viewRect.width - obstructions.right,
    left + 1
  );
  const bottom = Math.max(
    viewRect.y + viewRect.height - obstructions.bottom,
    top + 1
  );
  return {
    dx: keepAxisDelta(bounds.x, bounds.width, left, right),
    dy: keepAxisDelta(bounds.y, bounds.height, top, bottom),
  };
}
