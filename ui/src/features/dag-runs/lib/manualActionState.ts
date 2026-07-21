// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, NodeStatus, Status } from '@/api/v1/schema';

type DAGRun =
  | components['schemas']['DAGRunSummary']
  | components['schemas']['DAGRunDetails'];
type DAGRunDetails = components['schemas']['DAGRunDetails'];
type DAGRunNode = DAGRunDetails['nodes'][number];

type ManualActionState = {
  isWaiting: boolean;
  waitingApprovalNodes: DAGRunNode[];
  waitingHumanTaskNodes: DAGRunNode[];
  hasHumanTaskWork: boolean;
  humanTaskBlocksRetry: boolean;
};

function hasNodeDetails(dagRun: DAGRun): dagRun is DAGRunDetails {
  return 'nodes' in dagRun && Array.isArray(dagRun.nodes);
}

export function getManualActionState(dagRun?: DAGRun): ManualActionState {
  const isWaiting = dagRun?.status === Status.Waiting;
  if (!dagRun || !hasNodeDetails(dagRun)) {
    return {
      isWaiting,
      waitingApprovalNodes: [],
      waitingHumanTaskNodes: [],
      hasHumanTaskWork: false,
      humanTaskBlocksRetry: false,
    };
  }

  const waitingApprovalNodes = dagRun.nodes.filter(
    (node) =>
      node.status === NodeStatus.Waiting && node.step.approval !== undefined
  );
  const waitingHumanTaskNodes = dagRun.nodes.filter(
    (node) =>
      node.status === NodeStatus.Waiting && node.step.humanTask !== undefined
  );
  const resumePending = Boolean(dagRun.humanTaskResumePending);
  const hasHumanTaskWork = waitingHumanTaskNodes.length > 0 || resumePending;

  return {
    isWaiting,
    waitingApprovalNodes,
    waitingHumanTaskNodes,
    hasHumanTaskWork,
    humanTaskBlocksRetry: isWaiting && hasHumanTaskWork,
  };
}
