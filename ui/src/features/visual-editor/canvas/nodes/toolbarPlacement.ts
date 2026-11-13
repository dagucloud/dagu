// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useLayoutEffect, useRef, useState } from 'react';
import { useViewportWhile } from '../engine/store';

/** The toolbar keeps its on-screen size between these zoom levels. */
export const TOOLBAR_ZOOM_MIN = 0.6;
export const TOOLBAR_ZOOM_MAX = 1.5;

const GAP = 4;
const MARGIN = 4;
// The toolbar keeps at least this many px over its node, so that it leaves the
// canvas with the node and does not stay at the edge when the node is gone.
const HOLD = 24;
// The size of the toolbar on screen before a measure (and in jsdom).
const FALLBACK_SIZE = { width: 112, height: 32 };

interface Box {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

export interface ToolbarPlacement {
  /** True when the toolbar sits under the node: no room above it. */
  below: boolean;
  /** Horizontal shift in screen px that keeps the toolbar inside the canvas. */
  shift: number;
}

/**
 * The scale that cancels the zoom of the canvas for the toolbar, so that it
 * keeps its on-screen size between 60 % and 150 %. Beyond, it follows the zoom
 * of the nearest limit.
 */
export function toolbarScale(zoom: number): number {
  const level = Math.min(TOOLBAR_ZOOM_MAX, Math.max(TOOLBAR_ZOOM_MIN, zoom));
  return 1 / level;
}

/** Which side of the node the toolbar lines up with. */
export type ToolbarAlign = 'right' | 'center';

/**
 * Where a toolbar of `size` (on screen) goes for a node: above it, right
 * aligned or centred, or under it when the canvas has no room above; shifted
 * to stay inside the canvas, as long as it keeps a part over its node.
 */
export function placeToolbar(
  node: Box,
  canvas: Box,
  size: { width: number; height: number },
  align: ToolbarAlign = 'right',
  obstacles: readonly Box[] = []
): ToolbarPlacement {
  const roomAbove = node.top - canvas.top - MARGIN - GAP;
  const roomBelow = canvas.bottom - node.bottom - MARGIN - GAP;
  let below =
    roomAbove < size.height &&
    (roomBelow >= size.height || roomBelow > roomAbove);
  // The right edge of the toolbar, before any shift.
  const edge =
    align === 'center'
      ? (node.left + node.right) / 2 + size.width / 2
      : node.right;
  let shift = 0;
  if (edge > canvas.right - MARGIN) {
    shift = canvas.right - MARGIN - edge;
  }
  const left = edge + shift - size.width;
  if (left < canvas.left + MARGIN) shift += canvas.left + MARGIN - left;
  // A node that leaves the canvas takes its toolbar with it.
  const held = Math.min(HOLD, size.width, node.right - node.left);
  shift = Math.min(
    node.right - held - (edge - size.width),
    Math.max(node.left + held - edge, shift)
  );
  shift = Math.round(shift);
  // The shift is already as far right as the canvas lets it go, so a toolbar
  // that covers an obstacle (the Model pickers, the labels of the connectors,
  // the end of a link) goes to the other side of the node, when that side has
  // room and is free. Else it goes right of the obstacle, as long as it keeps
  // a part over its node and stays inside the canvas.
  const hits = (under: boolean, at: number) => {
    const right = edge + at;
    const top = under ? node.bottom : node.top - size.height;
    const spot = {
      left: right - size.width,
      right,
      top,
      bottom: top + size.height,
    };
    return obstacles.filter(
      (o) =>
        spot.left < o.right &&
        spot.right > o.left &&
        spot.top < o.bottom &&
        spot.bottom > o.top
    );
  };
  const covers = (under: boolean) => hits(under, shift).length > 0;
  if (covers(below)) {
    const room = below ? roomAbove : roomBelow;
    if (room >= size.height && !covers(!below)) below = !below;
  }
  if (covers(below)) {
    let at = shift;
    for (let i = 0; i <= obstacles.length; i++) {
      const hit = hits(below, at)[0];
      if (!hit) break;
      at = hit.right + GAP - (edge - size.width);
    }
    at = Math.round(at);
    const left = edge + at - size.width;
    if (
      hits(below, at).length === 0 &&
      edge + at <= canvas.right - MARGIN &&
      left <= node.right - held
    ) {
      shift = at;
    }
  }
  return { below, shift };
}

/**
 * Follows the placement of the toolbar of a node: it is measured after each
 * pan and zoom, and when the pointer or the focus enters the node. A toolbar
 * that is not shown (`selected` false) does not follow the view: it is
 * invisible, and it reads the view when it appears. A pan then draws no node
 * and measures no layout, except for the nodes with a toolbar on screen.
 */
export function useToolbarPlacement(
  selected: boolean | undefined,
  align: ToolbarAlign = 'right'
) {
  const ref = useRef<HTMLDivElement>(null);
  const viewport = useViewportWhile(Boolean(selected));
  const [placement, setPlacement] = useState<ToolbarPlacement>({
    below: false,
    shift: 0,
  });

  const measure = useCallback(() => {
    const element = ref.current;
    const node = element?.closest<HTMLElement>('[data-flow-node]');
    const canvas = element?.closest<HTMLElement>('[data-flow-root]');
    if (!element || !node || !canvas) return;
    const canvasBox = canvas.getBoundingClientRect();
    if (canvasBox.width === 0 || canvasBox.height === 0) return;
    const box = element.getBoundingClientRect();
    const size = {
      width: box.width || FALLBACK_SIZE.width,
      height: box.height || FALLBACK_SIZE.height,
    };
    // The toolbar never covers the Model pickers of the other nodes, the
    // labels of the connectors, or a link that ends at the top or the bottom
    // of its node: a handle gives the link a box as long as the toolbar.
    const reach = size.height + GAP + MARGIN;
    const obstacles = [
      ...Array.from(
        canvas.querySelectorAll<HTMLElement>('[data-shape="model-card"]')
      )
        .filter((card) => !node.contains(card))
        .map((card) => card.getBoundingClientRect()),
      ...Array.from(
        canvas.querySelectorAll<HTMLElement>('[data-testid^="ve-canvas-slot-"]')
      ).map((label) => label.getBoundingClientRect()),
      ...Array.from(
        node.querySelectorAll<HTMLElement>(
          '[data-flow-handle][data-handle-position="top"], [data-flow-handle][data-handle-position="bottom"]'
        )
      ).map((handle): Box => {
        const { left, right, top, bottom } = handle.getBoundingClientRect();
        return handle.dataset.handlePosition === 'top'
          ? { left, right, top: top - reach, bottom }
          : { left, right, top, bottom: bottom + reach };
      }),
    ];
    // The toolbar hangs on the card, which is smaller than the node box when
    // the node has satellites around it.
    const next = placeToolbar(
      (element.parentElement ?? node).getBoundingClientRect(),
      canvasBox,
      size,
      align,
      obstacles
    );
    setPlacement((current) =>
      current.below === next.below && current.shift === next.shift
        ? current
        : next
    );
  }, [align]);

  useLayoutEffect(() => {
    if (selected) {
      measure();
    }
  }, [measure, viewport.x, viewport.y, viewport.zoom, selected]);

  useLayoutEffect(() => {
    const node = ref.current?.closest<HTMLElement>('[data-flow-node]');
    if (!node) return;
    node.addEventListener('pointerenter', measure);
    node.addEventListener('focusin', measure);
    return () => {
      node.removeEventListener('pointerenter', measure);
      node.removeEventListener('focusin', measure);
    };
  }, [measure]);

  return {
    ref,
    placement,
    scale: toolbarScale(viewport.zoom),
    // The shift is in screen px; the node layer scales it by the zoom.
    shiftX: viewport.zoom > 0 ? placement.shift / viewport.zoom : 0,
  };
}
