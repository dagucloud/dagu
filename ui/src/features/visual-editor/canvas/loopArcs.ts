// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { repeatSummary, type Translate } from '@/components/ui/repeat-summary';
import { formatCompactCount } from '@/lib/formatCompactCount';
import type { CanvasNode } from '../contracts';
import { retryTitle, type RetryFacts } from './retryPolicy';
import type { CanvasNodeWithRoutes } from './types';

/**
 * A loop that the card of a step draws as a small return arc on its upper
 * edge. It is the retry of a failed step (`retry_policy`), or the repeat of a
 * step that ran (`repeat_policy`, while or until).
 */
export interface LoopArc {
  kind: 'retry' | 'repeat';
  /** The most tries, as `max 12`. `∞` for a repeat with no limit. Empty when the limit is a reference. */
  label: string;
  /** The whole policy, as a tooltip and as the name of the arc. */
  title: string;
}

/** The most tries as text: `max 12`. Nothing for a limit that a reference gives. */
function triesLabel(ts: Translate, limit: RetryFacts['limit']): string {
  return typeof limit === 'number'
    ? ts('max {limit}', { limit: formatCompactCount(limit) })
    : '';
}

/**
 * The loops of a step, in the order that the card draws them: the retry, then
 * the repeat. Empty for a step with neither, and a retry with a limit of 0
 * never retries, so it has no arc.
 */
export function loopArcs(
  ts: Translate,
  node: Pick<CanvasNode, 'repeat'> & Pick<CanvasNodeWithRoutes, 'retry'>
): LoopArc[] {
  const arcs: LoopArc[] = [];
  const retries = node.retry?.limit;
  if (node.retry && !(typeof retries === 'number' && retries <= 0)) {
    arcs.push({
      kind: 'retry',
      label: triesLabel(ts, node.retry.limit),
      title: retryTitle(ts, node.retry),
    });
  }
  if (node.repeat) {
    arcs.push({
      kind: 'repeat',
      label:
        node.repeat.limit !== undefined
          ? triesLabel(ts, node.repeat.limit)
          : node.repeat.limitRef !== undefined
            ? ''
            : '∞',
      title: repeatSummary(node.repeat, ts),
    });
  }
  return arcs;
}

/**
 * The geometry of an arc in px. The arcs sit side by side at the left of the
 * upper edge of the card: clear of the handles in either direction of the
 * graph, of the toolbar that shows over the right of the edge, and of the
 * issue badges at the right corner. The path is drawn in a box of 16 x 14 and
 * shown `LOOP_SCALE` times larger, so an arc rises about 20 px above the
 * card, and its label sits over it. A compact card has no room: it draws none.
 */
export const LOOP_SCALE = 1.4;
export const LOOP_WIDTH = 16 * LOOP_SCALE;
export const LOOP_HEIGHT = 14 * LOOP_SCALE;
/** The distance from the left corner to the first arc, and between two arcs. */
export const LOOP_INSET = 22;
export const LOOP_GAP = 2;

/** The left of the arc in a slot (0 or 1), from the left side of the card. */
export function loopArcLeft(slot: number): number {
  return LOOP_INSET + slot * (LOOP_WIDTH + LOOP_GAP);
}

/**
 * The path of an arc, in a box of 16 x 14 units whose top edge is on the
 * edge of the card, drawn mirrored upward. It leaves the card at the left and
 * comes back at the right, where the arrowhead is.
 */
export const LOOP_ARC_PATH = 'M0.75 0.75 C-1.5 13 14.5 13 13.25 1.5';
/** The arrowhead at the end of the path, pointing into the card. */
export const LOOP_ARC_HEAD = 'M13.25 0.5 L10.75 5.5 L15.75 5.5 Z';
