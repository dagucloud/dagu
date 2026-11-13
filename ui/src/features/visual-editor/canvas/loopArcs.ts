// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { repeatSummary } from '@/components/ui/repeat-summary';
import { formatCompactCount } from '@/lib/formatCompactCount';
import type { CanvasNode } from '../contracts';
import { retryTitle, type RetryFacts, type Translate } from './agentCard';
import type { CanvasNodeWithRoutes } from './types';

/**
 * A loop that the card of a step draws as a small return arc where the step
 * starts: on its upper edge in a vertical graph, on its left side in a
 * horizontal one. It is the retry of a failed step (`retry_policy`), or the
 * repeat of a step that ran (`repeat_policy`, while or until).
 */
export interface LoopArc {
  kind: 'retry' | 'repeat';
  /** The side of the card that the arc sits on: the retry on the right, the repeat on the left. */
  side: 'right' | 'left';
  /** The number of tries, as `×12`. `∞` for a repeat with no limit. Empty when the limit is a reference. */
  label: string;
  /** The whole policy, as a tooltip and as the name of the arc. */
  title: string;
}

/** The number of tries as text: `×12`. Nothing for a limit that a reference gives. */
function triesLabel(limit: RetryFacts['limit']): string {
  return typeof limit === 'number' ? `×${formatCompactCount(limit)}` : '';
}

/**
 * The loops of a step, in the order that the card draws them: the retry (right
 * side), then the repeat (left side). Empty for a step with neither.
 */
export function loopArcs(
  ts: Translate,
  node: Pick<CanvasNode, 'repeat'> & Pick<CanvasNodeWithRoutes, 'retry'>
): LoopArc[] {
  const arcs: LoopArc[] = [];
  if (node.retry) {
    arcs.push({
      kind: 'retry',
      side: 'right',
      label: triesLabel(node.retry.limit),
      title: retryTitle(ts, node.retry),
    });
  }
  if (node.repeat) {
    arcs.push({
      kind: 'repeat',
      side: 'left',
      label:
        node.repeat.limit !== undefined ? triesLabel(node.repeat.limit) : '∞',
      title: repeatSummary(node.repeat, ts),
    });
  }
  return arcs;
}

/**
 * Where the arcs of a card sit: where the step starts. In a vertical graph
 * (`top`) they sit on the upper edge, a little in from the corners: the retry
 * at the right one, the repeat at the left one. In a horizontal graph (`side`)
 * they sit on the left side: the retry low, the repeat high.
 */
export type LoopPlacement = 'side' | 'top';

/**
 * The geometry of an arc in px, from the point where it leaves the card. The
 * paths are drawn in a box of 16 x 14 and shown `LOOP_SCALE` times larger. On
 * a side the loop bulges out by about 15 px, is `LOOP_HEIGHT` tall, and its
 * foot is `LOOP_FOOT` above the bottom of the card: clear of the handle in the
 * middle of a side and of the caption of a compact card. The label sits at the
 * apex, so that the arc and its label take at most 42 px beside the card: less
 * than the space between two ranks (96 px).
 */
export const LOOP_SCALE = 1.4;
export const LOOP_WIDTH = 16 * LOOP_SCALE;
export const LOOP_HEIGHT = 14 * LOOP_SCALE;
export const LOOP_FOOT = 8;
/**
 * On the lower or upper edge: the distance from the corner to the nearer foot
 * of the arc. The upper edge of a compact card is narrow and has the link of
 * the step at its middle: its arcs sit at the corners.
 */
export const LOOP_BOTTOM_INSET = 22;
export const LOOP_TOP_INSET = 2;

/**
 * The path of the arc of a side, in a box of 16 x 14 units whose left
 * edge is on the side of the card. It leaves the card at the top and comes back
 * at the bottom, where the arrowhead is. The arc of the other side is its
 * mirror.
 */
export const LOOP_PATH = 'M0.75 0.75 C13 -1.5 13 14.5 1.5 13.25';
/** The arrowhead at the end of the path, pointing into the card. */
export const LOOP_HEAD = 'M0.5 13.25 L5.5 10.75 L5.5 15.75 Z';

/**
 * The path of an arc of the lower edge, in a box of 16 x 14 units whose
 * top edge is on the edge of the card. It leaves the card at the left and
 * comes back at the right, where the arrowhead is. The arc of the upper edge is
 * its mirror, up.
 */
export const LOOP_BOTTOM_PATH = 'M0.75 0.75 C-1.5 13 14.5 13 13.25 1.5';
/** The arrowhead at the end of the path of the lower edge, pointing into the card. */
export const LOOP_BOTTOM_HEAD = 'M13.25 0.5 L10.75 5.5 L15.75 5.5 Z';
