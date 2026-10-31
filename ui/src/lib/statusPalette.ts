// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { NodeStatus, Status } from '@/api/v1/schema';

/** The colour group of a run or step status. */
export type StatusTone =
  | 'success'
  | 'error'
  | 'running'
  | 'warning'
  | 'aborted'
  | 'neutral';

/** Tailwind classes of one tone, each on its `--status-*` token. */
export interface ToneClasses {
  /** Solid fill of a status dot. */
  dot: string;
  text: string;
  ring: string;
  stroke: string;
  /** Tinted surface of a chip or badge. */
  bg: string;
}

/**
 * Gives the tone of a status, with the grouping of getStatusClass in
 * status-utils.ts. An unknown value is neutral.
 */
export function statusTone(
  status: Status | NodeStatus | undefined
): StatusTone {
  switch (status) {
    case Status.Success:
    case NodeStatus.Success:
      return 'success';
    case Status.Failed:
    case Status.Rejected:
    case NodeStatus.Failed:
    case NodeStatus.Rejected:
      return 'error';
    case Status.Running:
    case NodeStatus.Running:
      return 'running';
    case NodeStatus.Retrying:
    case Status.PartialSuccess:
    case Status.Waiting:
    case NodeStatus.PartialSuccess:
    case NodeStatus.Waiting:
      return 'warning';
    case Status.Aborted:
    case NodeStatus.Aborted:
      return 'aborted';
    default:
      return 'neutral';
  }
}

// Literal strings, so that Tailwind finds every class in this file.
const TONE_CLASSES: Record<StatusTone, ToneClasses> = {
  success: {
    dot: 'bg-[var(--status-success)]',
    text: 'text-[var(--status-success)]',
    ring: 'ring-[var(--status-success)]',
    stroke: 'stroke-[var(--status-success)]',
    bg: 'bg-[var(--status-success)]/10',
  },
  error: {
    dot: 'bg-[var(--status-error)]',
    text: 'text-[var(--status-error)]',
    ring: 'ring-[var(--status-error)]',
    stroke: 'stroke-[var(--status-error)]',
    bg: 'bg-[var(--status-error)]/10',
  },
  running: {
    dot: 'bg-[var(--status-running)]',
    text: 'text-[var(--status-running)]',
    ring: 'ring-[var(--status-running)]',
    stroke: 'stroke-[var(--status-running)]',
    bg: 'bg-[var(--status-running)]/10',
  },
  warning: {
    dot: 'bg-[var(--status-warning)]',
    text: 'text-[var(--status-warning)]',
    ring: 'ring-[var(--status-warning)]',
    stroke: 'stroke-[var(--status-warning)]',
    bg: 'bg-[var(--status-warning)]/10',
  },
  aborted: {
    dot: 'bg-[var(--status-aborted)]',
    text: 'text-[var(--status-aborted)]',
    ring: 'ring-[var(--status-aborted)]',
    stroke: 'stroke-[var(--status-aborted)]',
    bg: 'bg-[var(--status-aborted)]/10',
  },
  neutral: {
    dot: 'bg-[var(--status-neutral)]',
    text: 'text-[var(--status-neutral)]',
    ring: 'ring-[var(--status-neutral)]',
    stroke: 'stroke-[var(--status-neutral)]',
    bg: 'bg-[var(--status-neutral)]/10',
  },
};

/**
 * The text class of a tone for small text on a light surface. The plain
 * green of the success tone is 4.2:1 on white: a darker green reaches 4.5:1.
 * The other tones keep their text class.
 */
export function readableToneText(tone: StatusTone): string {
  return tone === 'success'
    ? 'text-[color-mix(in_srgb,var(--status-success)_85%,black)] dark:text-[var(--status-success)]'
    : TONE_CLASSES[tone].text;
}

/** Gives the classes of a tone. */
export function toneClasses(tone: StatusTone): ToneClasses {
  return TONE_CLASSES[tone];
}

/** Gives the classes of the tone of a status. */
export function statusClasses(
  status: Status | NodeStatus | undefined
): ToneClasses {
  return TONE_CLASSES[statusTone(status)];
}
