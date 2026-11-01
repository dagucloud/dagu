// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import { NodeStatus, Status } from '@/api/v1/schema';
import { isUnsetTime } from '@/lib/dagRunTiming';

type Node = components['schemas']['Node'];
type DAGRunDetails = components['schemas']['DAGRunDetails'];

/** Finds the node of a step, in the main steps first and then in the handlers. */
export function findRunNode(
  details: DAGRunDetails | undefined,
  stepName: string
): Node | undefined {
  if (!details) return undefined;
  const handlers = [
    details.onInit,
    details.onSuccess,
    details.onFailure,
    details.onAbort,
    details.onExit,
    details.onWait,
  ];
  return [...(details.nodes ?? []), ...handlers].find(
    (n) => n?.step.name === stepName
  );
}

/** Formats a byte count as "512 B", "1.5 KiB" or "2.0 MiB". */
export function formatOutputSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

/**
 * True for a Skipped node that carries the state of an earlier run.
 * A run started with `steps` and `outputsFromRunId` marks every other step Skipped
 * and copies the source node state into it. The API hides the internal flag, so
 * this guesses from the copied state: a done count or a finish time. A step
 * whose precondition failed has a finish time too, so the guess holds only in
 * a run that carries outputs.
 */
export function isReusedNode(node: Node): boolean {
  return (
    node.status === NodeStatus.Skipped &&
    (node.doneCount > 0 || !isUnsetTime(node.finishedAt))
  );
}

export type StepParticipation = 'absent' | 'notRun' | 'reused' | 'ran';

/**
 * Tells whether the step ran in this run, was reused, or did not take part.
 * `reused` tells whether the run carries the outputs of the step from an
 * earlier run (`runInputProvenance`): the node alone does not tell a reused
 * step from a plain skip.
 */
export function stepParticipation(
  node: Node | undefined,
  reused: boolean
): StepParticipation {
  if (!node) return 'absent';
  if (reused && node.status === NodeStatus.Skipped) return 'reused';
  if (
    node.status === NodeStatus.NotStarted ||
    node.status === NodeStatus.Skipped
  ) {
    return 'notRun';
  }
  return 'ran';
}

const ACTIVE_RUN_STATUSES: ReadonlySet<Status> = new Set([
  Status.NotStarted,
  Status.Queued,
  Status.Running,
  Status.Waiting,
]);

/** True while the run can still change: queued, starting, running or waiting. */
export function isRunActive(details: DAGRunDetails | undefined): boolean {
  return details !== undefined && ACTIVE_RUN_STATUSES.has(details.status);
}
