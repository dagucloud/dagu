// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { NodeStatus } from '@/api/v1/schema';
import {
  statusTone,
  toneClasses,
  type StatusTone,
  type ToneClasses,
} from '@/lib/statusPalette';

type CanvasPart = keyof Pick<ToneClasses, 'ring' | 'stroke'>;

// These tones keep their theme utilities: --success, --destructive and
// --warning hold the same colours as their --status-* tokens in both themes.
// Literal strings, so that Tailwind finds them.
const THEME_UTILITIES: Partial<Record<StatusTone, Record<CanvasPart, string>>> =
  {
    success: {
      ring: 'ring-success',
      stroke: 'stroke-success',
    },
    error: {
      ring: 'ring-destructive',
      stroke: 'stroke-destructive',
    },
    warning: {
      ring: 'ring-warning',
      stroke: 'stroke-warning',
    },
  };

function canvasClass(status: NodeStatus, part: CanvasPart): string {
  const tone = statusTone(status);
  return THEME_UTILITIES[tone]?.[part] ?? toneClasses(tone)[part];
}

/**
 * Border classes for the card of a node with this run status: the colour of
 * the status on the line of the card itself, in both themes, with the width
 * of the plain border. The line and the card contour are one.
 */
export function statusBorderClass(status: NodeStatus): string {
  const border = canvasClass(status, 'ring').replace(/^ring-/, 'border-');
  return `${border} dark:${border}`;
}

/** Stroke classes for the outline of a node that is not a card. */
export function statusStrokeClass(status: NodeStatus): string {
  return canvasClass(status, 'stroke');
}

/**
 * The run status that tints an edge: the one of its source step, except for
 * an edge into a target that was skipped or did not start, which stays
 * neutral. Undefined without a run.
 */
export function edgeTintStatus(
  source: NodeStatus | undefined,
  target: NodeStatus | undefined
): NodeStatus | undefined {
  if (source === undefined) return undefined;
  if (target === NodeStatus.Skipped || target === NodeStatus.NotStarted) {
    return NodeStatus.Skipped;
  }
  return source;
}
