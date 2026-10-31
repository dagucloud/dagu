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
