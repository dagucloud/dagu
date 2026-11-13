// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// The sub-nodes of a chat step can reach out of the box that the layout ranks,
// along the flow, next to the steps before and after it. This module finds
// what they touch, and tells the layout how to clear it.

export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

/**
 * A thing that a sub-node must not touch: a card, a sub-node, or the box that
 * an edge sweeps. `top` and `bottom` are the first and the last rank that
 * move it: a card has one rank, an edge has the ranks of its two ends.
 * `before` and `after` are set on a sub-node that reaches out of the ranked
 * box of its owner on that side. `owner` is null for an edge.
 */
export interface Block {
  owner: string | null;
  top: number;
  bottom: number;
  rect: Rect;
  before?: boolean;
  after?: boolean;
}

export interface Flip {
  owner: string;
  side: 'before' | 'after';
}

export interface Clearance {
  /** Extra room in px to add after each rank, by index of the rank. */
  gaps: Map<number, number>;
  /** Sides where a sub-node cannot be cleared: the box must hold it. */
  flips: Flip[];
}

/** Clear space in px that a sub-node keeps from what it could touch. */
export const CLEARANCE = 16;

/**
 * What each sub-node that reaches out touches. A block in a rank before or
 * after the owner moves away when the gap between the ranks grows: the
 * result gives the growth. A block in the rank of the owner, or in a gap next
 * to it, does not: the result names the side of the owner to flip.
 */
export function clearance(
  horizontal: boolean,
  blocks: readonly Block[]
): Clearance {
  const from = (r: Rect) => (horizontal ? r.x : r.y);
  const to = (r: Rect) => from(r) + (horizontal ? r.width : r.height);
  const crossFrom = (r: Rect) => (horizontal ? r.y : r.x);
  const crossTo = (r: Rect) => crossFrom(r) + (horizontal ? r.height : r.width);
  const reaches = (b: Block) => b.before === true || b.after === true;
  const gaps = new Map<number, number>();
  const flips: Flip[] = [];
  for (const s of blocks) {
    if (!reaches(s) || s.owner === null) {
      continue;
    }
    for (const o of blocks) {
      if (
        o.owner === s.owner ||
        crossTo(s.rect) + CLEARANCE <= crossFrom(o.rect) ||
        crossTo(o.rect) + CLEARANCE <= crossFrom(s.rect)
      ) {
        continue;
      }
      const grow = (rank: number, need: number) =>
        gaps.set(rank, Math.max(gaps.get(rank) ?? 0, Math.ceil(need)));
      // The block of a later rank must come after the sub-node, and the
      // block of an earlier rank before it, whether they overlap now or not:
      // a gap that grows moves the whole column past what it passed.
      if (o.top > s.top) {
        grow(s.top, to(s.rect) + CLEARANCE - from(o.rect));
      } else if (o.bottom < s.top) {
        // Two sub-nodes that reach out: the one in the first rank decides.
        if (!reaches(o)) {
          grow(s.top - 1, to(o.rect) + CLEARANCE - from(s.rect));
        }
      } else if (
        to(s.rect) + CLEARANCE > from(o.rect) &&
        to(o.rect) + CLEARANCE > from(s.rect)
      ) {
        const nearer: Flip['side'] =
          from(o.rect) + to(o.rect) < from(s.rect) + to(s.rect)
            ? 'before'
            : 'after';
        const sides: Flip['side'][] = s[nearer]
          ? [nearer]
          : ['before', 'after'];
        for (const side of sides) {
          if (s[side]) {
            flips.push({ owner: s.owner, side });
          }
        }
      }
    }
  }
  return { gaps, flips };
}
