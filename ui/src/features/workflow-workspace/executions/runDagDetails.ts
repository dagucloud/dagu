// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ApiStep, HandlerEvent } from '@/features/visual-editor/contracts';
import type { ApiDAGDetails, ApiDAGRunDetails } from '../contracts';

// The step that stands for the agent in the runs of an agent DAG.
const AGENT_RUN_STEP = '__agent__';

const RUN_HANDLERS: readonly [keyof ApiDAGRunDetails, HandlerEvent][] = [
  ['onInit', 'init'],
  ['onWait', 'wait'],
  ['onSuccess', 'success'],
  ['onFailure', 'failure'],
  ['onAbort', 'abort'],
  ['onExit', 'exit'],
];

/**
 * The DAG as the run saw it, for the names of the steps: the canvas then
 * gives unnamed steps the names of the run.
 */
export function runDagDetails(
  details: ApiDAGRunDetails,
  dagName: string
): ApiDAGDetails {
  const handlerOn: Partial<Record<HandlerEvent, ApiStep>> = {};
  for (const [field, event] of RUN_HANDLERS) {
    const node = details[field] as { step: ApiStep } | undefined;
    if (node) handlerOn[event] = node.step;
  }
  return {
    name: details.name || dagName,
    steps: (details.nodes ?? [])
      .map((node) => node.step)
      .filter((step) => step.name !== AGENT_RUN_STEP),
    handlerOn,
    tasks: details.agentTasks,
  } as ApiDAGDetails;
}
