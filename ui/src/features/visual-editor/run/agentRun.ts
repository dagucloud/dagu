// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import type { AgentRunInfo, AgentTaskState } from '../canvas/types';

type DAGRunDetails = components['schemas']['DAGRunDetails'];

const TASK_STATES: readonly string[] = [
  'open',
  'completed',
  'skipped',
  'failed',
];

function isTaskState(value: string): value is AgentTaskState {
  return TASK_STATES.includes(value);
}

/**
 * The agent facts of a run for the canvas: the turns of the agent, the calls
 * of each tool and the status of each task. The server omits both lists for
 * an agent run that has no task and no decision yet, so the caller that shows
 * an agent DAG sets `isAgentDag`. Null for a run of another type of DAG.
 */
export function buildAgentRunInfo(
  details?: DAGRunDetails | null,
  isAgentDag = false
): AgentRunInfo | null {
  if (!details) {
    return null;
  }
  if (
    !isAgentDag &&
    details.agentTasks === undefined &&
    details.agentEvents === undefined
  ) {
    return null;
  }
  // No prototype: a tool or a task can be named `constructor`.
  const info: AgentRunInfo = {
    iterations: 0,
    calls: Object.create(null),
    tasks: Object.create(null),
  };
  for (const event of details.agentEvents ?? []) {
    info.iterations = Math.max(info.iterations, event.turn);
    if (event.kind === 'action' && event.name) {
      info.calls[event.name] = (info.calls[event.name] ?? 0) + 1;
    }
  }
  for (const task of details.agentTasks ?? []) {
    info.tasks[task.name] = isTaskState(task.status) ? task.status : 'open';
  }
  return info;
}
