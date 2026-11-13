// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { NodeStatus, Status, type components } from '@/api/v1/schema';

type Run = components['schemas']['DAGRunDetails'];

/** The shortest wait in the queue that a run line tells, in ms. */
export const QUEUE_WAIT_MIN_MS = 1000;

function firstLine(text: string | undefined): string {
  return (text ?? '').trim().split('\n')[0]?.trim() ?? '';
}

/**
 * Why a failed run failed: the first failed step with its error, or the error
 * of the run when no step carries one. null for a run that did not fail.
 */
export function failureCause(run: Run): string | null {
  if (run.status !== Status.Failed) return null;
  const node = (run.nodes ?? []).find(
    (n) => n.status === NodeStatus.Failed || n.status === NodeStatus.Rejected
  );
  const nodeError = firstLine(node?.error);
  if (node && nodeError) return `${node.step.name}: ${nodeError}`;
  const runError = firstLine(run.error);
  if (runError) return runError;
  return node ? node.step.name : null;
}

/** The time that a run waited in the queue before it started, in ms. */
export function queueWaitMs(run: Run): number | null {
  if (!run.queuedAt || !run.startedAt) return null;
  const queued = Date.parse(run.queuedAt);
  const started = Date.parse(run.startedAt);
  if (!Number.isFinite(queued) || !Number.isFinite(started)) return null;
  const wait = started - queued;
  return wait >= QUEUE_WAIT_MIN_MS ? wait : null;
}

/**
 * The time that the status column tells next to the dot: when the run was
 * scheduled, else when it ended, else when it started.
 */
export function statusMoment(
  run: Pick<Run, 'scheduleTime' | 'finishedAt' | 'startedAt'>
): string | null {
  for (const at of [run.scheduleTime, run.finishedAt, run.startedAt]) {
    if (at && Number.isFinite(Date.parse(at)) && Date.parse(at) > 0) return at;
  }
  return null;
}
