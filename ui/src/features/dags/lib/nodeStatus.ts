// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  components,
  NodeStatus,
  NodeStatusLabel,
} from '../../../api/v1/schema';

function nodeStatusLabel(status: NodeStatus): NodeStatusLabel {
  switch (status) {
    case NodeStatus.NotStarted:
      return NodeStatusLabel.not_started;
    case NodeStatus.Running:
      return NodeStatusLabel.running;
    case NodeStatus.Failed:
      return NodeStatusLabel.failed;
    case NodeStatus.Aborted:
      return NodeStatusLabel.aborted;
    case NodeStatus.Success:
      return NodeStatusLabel.succeeded;
    case NodeStatus.Skipped:
      return NodeStatusLabel.skipped;
    case NodeStatus.PartialSuccess:
      return NodeStatusLabel.partially_succeeded;
    case NodeStatus.Waiting:
      return NodeStatusLabel.waiting;
    case NodeStatus.Rejected:
      return NodeStatusLabel.rejected;
    case NodeStatus.Retrying:
      return NodeStatusLabel.retrying;
  }
  const exhaustive: never = status;
  return exhaustive;
}

function updateRequiredNodeStatus(
  node: components['schemas']['Node'],
  stepName: string,
  status: NodeStatus
): components['schemas']['Node'] {
  if (node.step.name !== stepName) {
    return node;
  }

  return {
    ...node,
    status,
    statusLabel: nodeStatusLabel(status),
  };
}

function updateOptionalNodeStatus(
  node: components['schemas']['Node'] | undefined,
  stepName: string,
  status: NodeStatus
): components['schemas']['Node'] | undefined {
  return node ? updateRequiredNodeStatus(node, stepName, status) : undefined;
}

export function updateDAGRunNodeStatus(
  dagRun: components['schemas']['DAGRunDetails'],
  stepName: string,
  status: NodeStatus
): components['schemas']['DAGRunDetails'] {
  return {
    ...dagRun,
    nodes: dagRun.nodes?.map((node) =>
      updateRequiredNodeStatus(node, stepName, status)
    ),
    onInit: updateOptionalNodeStatus(dagRun.onInit, stepName, status),
    onWait: updateOptionalNodeStatus(dagRun.onWait, stepName, status),
    onSuccess: updateOptionalNodeStatus(dagRun.onSuccess, stepName, status),
    onFailure: updateOptionalNodeStatus(dagRun.onFailure, stepName, status),
    onAbort: updateOptionalNodeStatus(dagRun.onAbort, stepName, status),
    onExit: updateOptionalNodeStatus(dagRun.onExit, stepName, status),
  };
}

export function updateDAGRunsNodeStatus(
  dagRuns: components['schemas']['DAGRunDetails'][] | null,
  dagRunId: string,
  stepName: string,
  status: NodeStatus
): components['schemas']['DAGRunDetails'][] | null {
  return (
    dagRuns?.map((dagRun) =>
      dagRun.dagRunId === dagRunId
        ? updateDAGRunNodeStatus(dagRun, stepName, status)
        : dagRun
    ) ?? null
  );
}
