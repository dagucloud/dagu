// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { NodeStatus, Status } from '@/api/v1/schema';
import { readRepeatPolicy } from '@/components/ui/repeat-summary';
import { runStatusText } from '@/lib/status-utils';
import { statusTone, type StatusTone } from '@/lib/statusPalette';

/** The words of a step that matter here: its repeat policy. */
interface StepLike {
  repeatPolicy?: unknown;
}

/** The parts of a node that matter here. */
interface NodeLike {
  status: NodeStatus;
  doneCount?: number;
  step?: StepLike | null;
}

/** The parts of a run that matter here. */
interface RunLike {
  status: Status;
  nodes?: readonly NodeLike[] | null;
}

/**
 * The status that the UI prints for a run. `status` is the API status: logic
 * and data attributes keep reading it. `text` is the English key to translate.
 */
export interface RunDisplayStatus {
  status: Status;
  /** A run that is meant to run for ever and has no failed step. */
  healthy: boolean;
  text: string;
  tone: StatusTone;
}

/**
 * A step that repeats with no limit (`repeat: while` or `until`, with no
 * `limit` or a limit of 0): it is meant to run for ever.
 */
export function isContinuousStep(step: StepLike | null | undefined): boolean {
  const policy = readRepeatPolicy(step?.repeatPolicy);
  return policy !== null && policy.limit === undefined;
}

/** True when at least one step of a definition is continuous. */
export function hasContinuousStep(
  steps: readonly StepLike[] | null | undefined
): boolean {
  return (steps ?? []).some(isContinuousStep);
}

const HEALTHY: Pick<RunDisplayStatus, 'healthy' | 'text' | 'tone'> = {
  healthy: true,
  text: 'Healthy',
  tone: 'success',
};

function normal(status: Status): RunDisplayStatus {
  return {
    status,
    healthy: false,
    text: runStatusText(status),
    tone: statusTone(status),
  };
}

/**
 * The one place that decides the status of a run for display. A run whose
 * status is Running is "Healthy" when a continuous step runs (or waits for
 * its next iteration) and no node has failed. A failed node, a stopped
 * continuous step or any other status shows the normal status, so that a
 * failure stays visible.
 */
export function runDisplayStatus(run: RunLike): RunDisplayStatus {
  if (run.status !== Status.Running) return normal(run.status);
  const nodes = run.nodes ?? [];
  const failed = nodes.some(
    (n) => n.status === NodeStatus.Failed || n.status === NodeStatus.Rejected
  );
  if (failed) return normal(run.status);
  // A continuous step that repeats has the status Running between two runs.
  const continuous = nodes.some(
    (n) => n.status === NodeStatus.Running && isContinuousStep(n.step)
  );
  return continuous ? { status: run.status, ...HEALTHY } : normal(run.status);
}

/**
 * The display status of a run that is known only by its summary (a list row,
 * a bar of the history): without nodes, the definition decides, through
 * hasContinuousStep. A failed node cannot show here; the run page shows it.
 */
export function summaryDisplayStatus(
  status: Status,
  continuous: boolean
): RunDisplayStatus {
  return status === Status.Running && continuous
    ? { status, ...HEALTHY }
    : normal(status);
}

/**
 * The repeat count of the continuous step of a healthy run, for the place of
 * the progress "0/1": the running one when another has stopped. null when
 * the step has not finished a run yet.
 */
export function continuousRunCount(run: RunLike): number | null {
  const continuous = (run.nodes ?? []).filter((n) => isContinuousStep(n.step));
  const node =
    continuous.find((n) => n.status === NodeStatus.Running) ?? continuous[0];
  const count = node?.doneCount ?? 0;
  return count > 0 ? count : null;
}
