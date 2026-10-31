// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, Status } from '@/api/v1/schema';
import { getManualActionState } from '@/features/dag-runs/lib/manualActionState';

type DAGRunDetails = components['schemas']['DAGRunDetails'];

/** A view of one DAG run. The order of the union is the order of the tab bar. */
export type RunViewTab =
  | 'status'
  | 'agent'
  | 'human-tasks'
  | 'approval'
  | 'timeline'
  | 'outputs'
  | 'artifacts'
  | 'tasks'
  | 'chat'
  | 'spec';

/** The facts of a run that decide which run views exist. */
export interface RunViewFacts {
  status: Status;
  hasNodes: boolean;
  hasChatSteps: boolean;
  hasAgentSessions: boolean;
  /** Agent steps that wait for an answer or whose session is unavailable. */
  agentWaiting: number;
  hasAgentTasks: boolean;
  waitingApprovals: number;
  hasHumanTaskWork: boolean;
  humanTasksWaiting: number;
  /** Artifacts are enabled on the server or the run has artifacts. */
  hasArtifacts: boolean;
}

// Runs recorded before agent DAGs were renamed carry the 'controller' type.
const CHAT_EXECUTOR_TYPES = ['chat', 'agent', 'controller'];

/** Collects the run view facts of a DAG run. */
export function runViewFacts(
  dagRun: DAGRunDetails,
  opts: { artifactEnabled: boolean }
): RunViewFacts {
  const nodes = dagRun.nodes ?? [];
  const agentNodes = nodes.filter((node) => !!node.agentSession);
  const { waitingApprovalNodes, waitingHumanTaskNodes, hasHumanTaskWork } =
    getManualActionState(dagRun);
  return {
    status: dagRun.status,
    hasNodes: nodes.length > 0,
    hasChatSteps: nodes.some((node) =>
      CHAT_EXECUTOR_TYPES.includes(node.step.executorConfig?.type ?? '')
    ),
    hasAgentSessions: agentNodes.length > 0,
    agentWaiting: agentNodes.filter(
      (node) =>
        node.agentSession?.state === 'waiting' ||
        node.agentSession?.state === 'unavailable'
    ).length,
    hasAgentTasks: (dagRun.agentTasks ?? []).length > 0,
    waitingApprovals: waitingApprovalNodes.length,
    hasHumanTaskWork,
    humanTasksWaiting: waitingHumanTaskNodes.length,
    hasArtifacts: opts.artifactEnabled || !!dagRun.artifactsAvailable,
  };
}

/** Returns the run views that exist for the facts, in tab bar order. */
export function visibleRunTabs(f: RunViewFacts): RunViewTab[] {
  const tabs: RunViewTab[] = ['status'];
  if (f.hasAgentSessions) tabs.push('agent');
  if (f.hasHumanTaskWork) tabs.push('human-tasks');
  if (f.waitingApprovals > 0) tabs.push('approval');
  if (f.status !== Status.NotStarted) tabs.push('timeline');
  tabs.push('outputs');
  if (f.hasArtifacts) tabs.push('artifacts');
  if (f.hasAgentTasks) tabs.push('tasks');
  if (f.hasChatSteps) tabs.push('chat');
  tabs.push('spec');
  return tabs;
}

/**
 * Returns the view of the manual action that waits, or null. The order is
 * agent, then human tasks, then approval.
 */
export function autoRunTab(f: RunViewFacts): RunViewTab | null {
  if (f.agentWaiting > 0) return 'agent';
  if (f.hasHumanTaskWork) return 'human-tasks';
  if (f.waitingApprovals > 0) return 'approval';
  return null;
}

/** Returns active when it is visible, else 'status'. */
export function fallbackRunTab(
  active: RunViewTab,
  visible: readonly RunViewTab[]
): RunViewTab {
  return visible.includes(active) ? active : 'status';
}
