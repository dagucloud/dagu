// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { NodeStatus, Status } from '@/api/v1/schema';
import { getManualActionState } from '@/features/dag-runs/lib/manualActionState';
import { HANDLER_EVENTS } from './handlerMapping';
import type { ApiDAGRunDetails, ApiNode, WaitingCounts } from './types';

/**
 * The server sends a zero-value latestDAGRun for a workflow that never ran:
 * no run ID, or NotStarted without nodes.
 */
export function isNeverRun(details: ApiDAGRunDetails | null | undefined) {
  if (!details) return true;
  if (!details.dagRunId) return true;
  return (
    details.status === Status.NotStarted && (details.nodes?.length ?? 0) === 0
  );
}

/**
 * When the run was created, in ms: the queue time for a queued run, else the
 * start time. null when the details carry neither.
 */
export function runCreatedAt(
  details: Pick<ApiDAGRunDetails, 'queuedAt' | 'startedAt'> | null | undefined
): number | null {
  for (const value of [details?.queuedAt, details?.startedAt]) {
    const ms = value ? Date.parse(value) : NaN;
    if (Number.isFinite(ms)) return ms;
  }
  return null;
}

/**
 * Whether the latest run is really newer than the selected one. The API
 * prefers an older run that is still active as the latest run, so the ID
 * alone does not tell. A run that started in the same second as the latest
 * run is older: the times have a resolution of one second, and the API names
 * the newest run as the latest one, but for an older active run. With no
 * time on either side the IDs decide.
 */
export function isNewerRun(
  latest: { runId: string; at?: number | null } | null,
  selected: Pick<ApiDAGRunDetails, 'dagRunId' | 'queuedAt' | 'startedAt'>
): boolean {
  if (latest === null || latest.runId === selected.dagRunId) return false;
  const selectedAt = runCreatedAt(selected);
  if (latest.at == null || selectedAt === null) return true;
  return latest.at >= selectedAt;
}

/** Step nodes first, then handler nodes whose names are not taken. */
export function nodesByStep(
  details: ApiDAGRunDetails | null
): ReadonlyMap<string, ApiNode> {
  const map = new Map<string, ApiNode>();
  if (!details) return map;
  for (const node of details.nodes ?? []) {
    if (node.step?.name) map.set(node.step.name, node);
  }
  for (const event of HANDLER_EVENTS) {
    const node = details[event];
    const name = node?.step?.name;
    if (node && name && !map.has(name)) map.set(name, node);
  }
  return map;
}

function isAgentWaiting(node: ApiNode): boolean {
  const state = node.agentSession?.state;
  return state === 'waiting' || state === 'unavailable';
}

export const NO_WAITING: WaitingCounts = {
  approvals: 0,
  humanTasks: 0,
  agent: 0,
  firstWaitingStep: null,
};

/** Approvals and human tasks as getManualActionState, plus agent sessions that wait. */
export function waitingCounts(details: ApiDAGRunDetails | null): WaitingCounts {
  if (!details) return NO_WAITING;
  const { waitingApprovalNodes, waitingHumanTaskNodes } =
    getManualActionState(details);
  const waitingAgentNodes = (details.nodes ?? []).filter(isAgentWaiting);
  const waitingNodes = new Set<ApiNode>([
    ...waitingApprovalNodes,
    ...waitingHumanTaskNodes,
    ...waitingAgentNodes,
  ]);
  const first = (details.nodes ?? []).find((node) => waitingNodes.has(node));
  return {
    approvals: waitingApprovalNodes.length,
    humanTasks: waitingHumanTaskNodes.length,
    agent: waitingAgentNodes.length,
    firstWaitingStep: first?.step.name ?? null,
  };
}

/** The first step node that failed or was rejected. */
export function firstFailedStep(
  details: ApiDAGRunDetails | null
): string | null {
  const node = details?.nodes?.find(
    (n) => n.status === NodeStatus.Failed || n.status === NodeStatus.Rejected
  );
  return node?.step.name ?? null;
}
