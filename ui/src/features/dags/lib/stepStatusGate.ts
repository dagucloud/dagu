// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, NodeStatus, Status } from '@/api/v1/schema';

type Node = components['schemas']['Node'];

/** The result of a gate. reason is English text that tells why ok is false. */
export interface StepGate {
  ok: boolean;
  reason?: string;
}

/** The result of the retry gate. */
export interface StepRetryGate extends StepGate {
  /** The retry is offered, but it cannot start now. reason tells why. */
  disabled: boolean;
}

const STATUS_LOCKED_RUN_STATES: readonly Status[] = [
  Status.NotStarted,
  Status.Running,
  Status.Queued,
  Status.Waiting,
];

/**
 * Tells whether a person can set the status of a step by hand. The rule is the
 * same for the step table and the status graph.
 */
export function canChangeStepStatus(
  run: { status: Status } | null,
  node: Node,
  p: { runDags: boolean }
): StepGate {
  if (!p.runDags) {
    return {
      ok: false,
      reason: 'You do not have permission to run workflows.',
    };
  }
  if (!run) {
    return { ok: false, reason: 'No DAG run is selected.' };
  }
  if (STATUS_LOCKED_RUN_STATES.includes(run.status)) {
    return {
      ok: false,
      reason: 'Step status cannot change while the DAG run is active.',
    };
  }
  if (node.step.humanTask) {
    return { ok: false, reason: 'A human task step completes from its task.' };
  }
  return { ok: true };
}

/**
 * Tells whether the step table offers 'Retry step' for a step. A child run
 * cannot retry its steps while its root run is running.
 */
export function canRetryStep(
  run: { status: Status } | null,
  node: Node,
  p: { runDags: boolean; rootRunning: boolean }
): StepRetryGate {
  if (!p.runDags) {
    return {
      ok: false,
      disabled: true,
      reason: 'You do not have permission to run workflows.',
    };
  }
  if (!run) {
    return { ok: false, disabled: true, reason: 'No DAG run is selected.' };
  }
  if (run.status === Status.Waiting || run.status === Status.Queued) {
    return {
      ok: false,
      disabled: true,
      reason: 'A waiting or queued DAG run cannot retry a step.',
    };
  }
  if (node.step.humanTask) {
    return {
      ok: false,
      disabled: true,
      reason: 'A human task step completes from its task.',
    };
  }
  if (
    node.status === NodeStatus.Waiting ||
    node.status === NodeStatus.Rejected
  ) {
    return {
      ok: false,
      disabled: true,
      reason: 'A waiting or rejected step cannot be retried.',
    };
  }
  if (p.rootRunning) {
    return {
      ok: true,
      disabled: true,
      reason: 'Retry unavailable while the root DAG run is running.',
    };
  }
  if (run.status === Status.Running) {
    return {
      ok: true,
      disabled: true,
      reason: 'Retry unavailable while the DAG run is running.',
    };
  }
  return { ok: true, disabled: false };
}
