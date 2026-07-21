// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, NodeStatus, Status } from '@/api/v1/schema';

type DAGRun =
  | components['schemas']['DAGRunSummary']
  | components['schemas']['DAGRunDetails'];
type DAGRunDetails = components['schemas']['DAGRunDetails'];

function hasNodeDetails(dagRun: DAGRun): dagRun is DAGRunDetails {
  return 'nodes' in dagRun && Array.isArray(dagRun.nodes);
}

export function getManualActionState(dagRun?: DAGRun) {
  const isWaiting = dagRun?.status === Status.Waiting;
  if (!dagRun || !hasNodeDetails(dagRun)) {
    return {
      isWaiting,
      waitingApprovalNodes: [],
      humanTaskBlocksRetry: false,
    };
  }

  return {
    isWaiting,
    waitingApprovalNodes: dagRun.nodes.filter(
      (node) =>
        node.status === NodeStatus.Waiting && node.step.approval !== undefined
    ),
    humanTaskBlocksRetry:
      isWaiting &&
      (Boolean(dagRun.humanTaskResumePending) ||
        dagRun.nodes.some((node) => node.step.humanTask !== undefined)),
  };
}
