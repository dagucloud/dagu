// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { NodeStatus } from '@/api/v1/schema';
import { previousOfExecuted } from '@/features/visual-editor/run/upstreamSteps';
import { buildRunOverlay } from '@/features/visual-editor/run/runOverlay';
import type { ApiDAGRunDetails } from '../contracts';

/** What the run did with a step that it did not execute. */
export interface StepReuse {
  /** The source run succeeded for the step, or the step failed there. */
  outcome: 'success' | 'failed';
  /** The run that the outputs come from, when the run records it. */
  runId?: string;
}

/**
 * The steps that a run did not execute but reused, by server step name. The
 * classification is the one of the reuse outlines of the canvas
 * (`markReuseOutlines`): a step with a record of reused outputs succeeded in
 * its source run; a previous step of the executed steps that the source run
 * could not give failed there. A step on another branch that was skipped
 * without outputs is not listed.
 */
export function stepReuse(
  details: ApiDAGRunDetails
): ReadonlyMap<string, StepReuse> {
  const found = new Map<string, StepReuse>();
  const overlay = buildRunOverlay(details);
  const nodes = details.nodes ?? [];
  const executed = nodes
    .filter((node) => {
      const state = overlay[node.step.name];
      return (
        state &&
        !state.reused &&
        node.status !== NodeStatus.Skipped &&
        node.status !== NodeStatus.NotStarted
      );
    })
    .map((node) => node.step.name);
  const previous = previousOfExecuted(
    nodes.map((node) => ({
      name: node.step.name,
      depends: node.step.depends,
      inferredDepends: node.step.inferredDepends,
    })),
    executed
  );
  for (const node of nodes) {
    const state = overlay[node.step.name];
    if (state?.reused) {
      found.set(node.step.name, {
        outcome: 'success',
        runId: state.reused.runId,
      });
    } else if (state?.reuseFailed && previous.has(node.step.name)) {
      found.set(node.step.name, {
        outcome: 'failed',
        runId: state.reuseFailed.runId,
      });
    }
  }
  return found;
}
