// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { NodeStatus } from '@/api/v1/schema';
import { statusTone, type StatusTone } from '@/lib/statusPalette';

interface CanvasColor {
  border: string;
  ring: string;
  stroke: string;
  tint?: string;
}

// The colours of the graph, from the --graph-* tokens. Each token has the
// value of the --status-* token of its theme, as in the export. The border
// repeats under dark: so that it wins over the dark outline of the card. A
// step that did not run has no tint. Literal strings, so that Tailwind finds
// them.
const CANVAS_COLOR: Record<StatusTone, CanvasColor> = {
  success: {
    border: 'border-[var(--graph-success)] dark:border-[var(--graph-success)]',
    ring: 'ring-[var(--graph-success)]',
    stroke: 'stroke-[var(--graph-success)]',
    tint: 'bg-[color-mix(in_srgb,var(--graph-success)_10%,var(--card))]',
  },
  error: {
    border: 'border-[var(--graph-error)] dark:border-[var(--graph-error)]',
    ring: 'ring-[var(--graph-error)]',
    stroke: 'stroke-[var(--graph-error)]',
    tint: 'bg-[color-mix(in_srgb,var(--graph-error)_10%,var(--card))]',
  },
  running: {
    border: 'border-[var(--graph-running)] dark:border-[var(--graph-running)]',
    ring: 'ring-[var(--graph-running)]',
    stroke: 'stroke-[var(--graph-running)]',
    tint: 'bg-[color-mix(in_srgb,var(--graph-running)_10%,var(--card))]',
  },
  warning: {
    border: 'border-[var(--graph-warning)] dark:border-[var(--graph-warning)]',
    ring: 'ring-[var(--graph-warning)]',
    stroke: 'stroke-[var(--graph-warning)]',
    tint: 'bg-[color-mix(in_srgb,var(--graph-warning)_10%,var(--card))]',
  },
  aborted: {
    border: 'border-[var(--graph-aborted)] dark:border-[var(--graph-aborted)]',
    ring: 'ring-[var(--graph-aborted)]',
    stroke: 'stroke-[var(--graph-aborted)]',
    tint: 'bg-[color-mix(in_srgb,var(--graph-aborted)_10%,var(--card))]',
  },
  neutral: {
    border: 'border-[var(--graph-neutral)] dark:border-[var(--graph-neutral)]',
    ring: 'ring-[var(--graph-neutral)]',
    stroke: 'stroke-[var(--graph-neutral)]',
  },
};

function canvasColor(status: NodeStatus): CanvasColor {
  return CANVAS_COLOR[statusTone(status)];
}

/**
 * Border classes for the card of a node with this run status: the colour of
 * the status on the line of the card itself, in both themes, with the width
 * of the plain border. The line and the card contour are one.
 */
export function statusBorderClass(status: NodeStatus): string {
  return canvasColor(status).border;
}

/**
 * Classes for the card of a step of the run, so that its status reads at
 * any zoom: the border in the colour of the status, a ring of the same
 * colour that doubles the line without moving the box, and a light tint of
 * the colour over the card. A step that did not run keeps a neutral border
 * and the plain card.
 */
export function statusCardClass(status: NodeStatus): string {
  const { border, ring, tint } = canvasColor(status);
  if (!tint) return border;
  return `${border} ring-1 ${ring} ${tint}`;
}

/**
 * The dash of an edge that leaves a step of this status: a failed step dashes
 * its edges, so that the path of a failure reads apart from a success.
 */
export function statusDashArray(status: NodeStatus): string | undefined {
  return statusTone(status) === 'error' ? '5 4' : undefined;
}

/** Stroke classes for the outline of a node that is not a card. */
export function statusStrokeClass(status: NodeStatus): string {
  return canvasColor(status).stroke;
}

/**
 * The run status that tints an edge: the one of its source step, except for
 * an edge into a target that was skipped or did not start, which stays
 * neutral. Undefined without a run, and into a step that is not in the run,
 * such as one added since.
 */
export function edgeTintStatus(
  source: NodeStatus | undefined,
  target: NodeStatus | undefined
): NodeStatus | undefined {
  if (source === undefined || target === undefined) return undefined;
  if (target === NodeStatus.Skipped || target === NodeStatus.NotStarted) {
    return NodeStatus.Skipped;
  }
  return source;
}
