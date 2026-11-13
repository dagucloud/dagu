// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { createContext, useContext } from 'react';
import type { CanvasDirection, CanvasGraph } from '../contracts';
import { chatPlan } from './layout';
import type { SubBox, SubRowPlan, SubSide } from './subRow';
import type { CanvasNodeWithRoutes, NodePositions } from './types';

/**
 * The links of the sub-nodes that the user moved away from their place in
 * the box of their step. A sub-node in place has no entry: its link is the
 * one of the plan. The connector of a slot on the step never moves: only the
 * entry of the moved sub-node turns to the side that faces the step.
 */
export interface SubLinks {
  /** The side of the step where a moved sub-node now sits, by sub-node key. */
  sides: Record<string, SubSide>;
}

export const NO_SUB_LINKS: SubLinks = { sides: {} };

/** The links of the moved sub-nodes, for the nodes that draw them. */
export const SubLinksContext = createContext<SubLinks>(NO_SUB_LINKS);

export function useSubLinks(): SubLinks {
  return useContext(SubLinksContext);
}

/**
 * The side of the step where a moved sub-node sits. A sub-node keeps the
 * side of its slot while it is wholly on that side of the step. Otherwise
 * the link leaves by the side that faces the sub-node: on a diagonal, the
 * side with the wider gap. A sub-node over the step keeps the side of its
 * slot.
 */
export function facingSide(
  plan: Pick<SubRowPlan, 'base'>,
  box: SubBox,
  fallback: SubSide
): SubSide {
  const b = plan.base;
  const h =
    box.x + box.width <= b.x ? 'left' : box.x >= b.x + b.width ? 'right' : null;
  const v =
    box.y + box.height <= b.y
      ? 'top'
      : box.y >= b.y + b.height
        ? 'bottom'
        : null;
  if (h === fallback || v === fallback) return fallback;
  if (h && !v) return h;
  if (v && !h) return v;
  if (!h || !v) return fallback;
  const gapH =
    h === 'left' ? b.x - (box.x + box.width) : box.x - (b.x + b.width);
  const gapV =
    v === 'top' ? b.y - (box.y + box.height) : box.y - (b.y + b.height);
  return gapH >= gapV ? h : v;
}

/**
 * The entries of the sub-nodes that sit away from their place in the box of
 * their step, from the places of the canvas. The card of the step and the
 * connectors of its slots stay where they are. Only the entry of a moved
 * sub-node changes sides, to face the step.
 */
export function movedSubLinks(
  graph: CanvasGraph,
  positions: NodePositions,
  compact: boolean,
  direction: CanvasDirection
): SubLinks {
  const sides: SubLinks['sides'] = {};
  for (const node of graph.nodes as CanvasNodeWithRoutes[]) {
    const at = positions[node.key];
    const plan = node.subRow ? chatPlan(node, compact, direction) : null;
    if (!node.subRow || !plan || !at) continue;
    for (const item of node.subRow.items) {
      const planned = plan.items[item.key];
      const now = positions[item.key];
      if (!planned || !now) continue;
      // The layout rounds the places of the sub-nodes: so does this offset.
      const dx = now.x - (at.x + Math.round(planned.x));
      const dy = now.y - (at.y + Math.round(planned.y));
      if (dx === 0 && dy === 0) continue;
      const box: SubBox = { ...planned, x: planned.x + dx, y: planned.y + dy };
      sides[item.key] = facingSide(plan, box, plan.slots[item.slot].side);
    }
  }
  // No moved sub-node: the same value, so that no sub-node draws again.
  return Object.keys(sides).length === 0 ? NO_SUB_LINKS : { sides };
}
