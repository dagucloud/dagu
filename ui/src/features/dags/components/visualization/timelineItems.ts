// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import dayjs from '@/lib/dayjs';
import { components, NodeStatus, Status } from '../../../../api/v1/schema';

type DAGRunDetails = components['schemas']['DAGRunDetails'];
type Node = components['schemas']['Node'];
type SubDAGRun = components['schemas']['SubDAGRun'];
type SubDAGRunDetail = components['schemas']['SubDAGRunDetail'];

/** Render-ready timeline row for either a parent step or expanded sub-DAG run. */
export type TimelineRow = {
  id: string;
  kind: 'step' | 'subdag';
  label: string;
  parentStepName?: string;
  startMs: number;
  endMs: number;
  status: NodeStatus | Status;
  statusLabel?: string;
  statusSource: 'node' | 'dagrun';
  description?: string;
  error?: string;
  params?: string;
  dagRunId?: string;
  dagName?: string;
  depth: number;
};

/** API query context needed to load sub-DAG runs for a root or nested DAG run. */
export type SubRunQueryContext = {
  rootDagName: string;
  rootDagRunId: string;
  parentSubDAGRunId?: string;
};

/** Returns child runs that should expand under a parent timeline row. */
export function getTimelineSubRuns(node: Node): SubDAGRun[] {
  if (node.step.parallel) {
    return node.subRuns || [];
  }
  if (!node.step.repeatPolicy?.repeat) {
    return [];
  }
  return [...(node.subRunsRepeated || []), ...(node.subRuns || [])];
}

/** Checks whether a DAG run has any expandable child sub-runs. */
export function hasTimelineSubRuns(dagRun: DAGRunDetails): boolean {
  return (dagRun.nodes || []).some(
    (node) => getTimelineSubRuns(node).length > 0
  );
}

/** Builds the root-aware query context for fetching sub-DAG run details. */
export function getSubRunQueryContext(
  dagRun: DAGRunDetails
): SubRunQueryContext {
  const rootDagName = dagRun.rootDAGRunName || dagRun.name;
  const rootDagRunId = dagRun.rootDAGRunId || dagRun.dagRunId;
  const parentSubDAGRunId =
    dagRun.dagRunId !== rootDagRunId ? dagRun.dagRunId : undefined;

  return {
    rootDagName,
    rootDagRunId,
    parentSubDAGRunId,
  };
}

/** Builds sorted timeline rows from parent nodes and loaded sub-DAG details. */
export function buildTimelineRows({
  dagRun,
  subRunDetails,
  nowMs,
}: {
  dagRun: DAGRunDetails;
  subRunDetails: SubDAGRunDetail[];
  nowMs: number;
}): TimelineRow[] {
  const parentRows = (dagRun.nodes || [])
    .map((node) => buildStepRow(node, nowMs))
    .filter((row): row is TimelineRow & { node: Node } => row !== null)
    .sort((a, b) => a.startMs - b.startMs);

  const subRunDetailsById = new Map(
    subRunDetails.map((subRunDetail) => [subRunDetail.dagRunId, subRunDetail])
  );
  const rows: TimelineRow[] = [];

  for (const parentRow of parentRows) {
    const { node, ...row } = parentRow;
    rows.push(row);

    const subRuns = getTimelineSubRuns(node);
    subRuns.forEach((subRun, index) => {
      const detail = subRunDetailsById.get(subRun.dagRunId);
      if (!detail) {
        return;
      }

      const childRow = buildSubDAGRow({
        parentStepName: node.step.name,
        subRun,
        detail,
        index,
        nowMs,
      });
      if (childRow) {
        rows.push(childRow);
      }
    });
  }

  return rows;
}

function buildStepRow(
  node: Node,
  nowMs: number
): (TimelineRow & { node: Node }) | null {
  if (!node.startedAt || node.startedAt === '-') {
    return null;
  }

  const startMs = dayjs(node.startedAt).valueOf();
  const endMs = parseEndMs(node.finishedAt, nowMs);
  const normalized = normalizeTiming(startMs, endMs);

  if (!normalized) {
    return null;
  }

  return {
    id: `step:${node.step.name}`,
    kind: 'step',
    label: node.step.name,
    startMs: normalized.startMs,
    endMs: normalized.endMs,
    status: node.status,
    statusLabel: node.statusLabel,
    statusSource: 'node',
    description: node.step.description,
    error: node.error,
    depth: 0,
    node,
  };
}

function buildSubDAGRow({
  parentStepName,
  subRun,
  detail,
  index,
  nowMs,
}: {
  parentStepName: string;
  subRun: SubDAGRun;
  detail: SubDAGRunDetail;
  index: number;
  nowMs: number;
}): TimelineRow | null {
  const startMs = dayjs(detail.startedAt).valueOf();
  const endMs = parseEndMs(detail.finishedAt, nowMs);
  const normalized = normalizeTiming(startMs, endMs);

  if (!normalized) {
    return null;
  }

  return {
    id: `subdag:${parentStepName}:${detail.dagRunId}`,
    kind: 'subdag',
    label: `#${String(index + 1).padStart(2, '0')}`,
    parentStepName,
    startMs: normalized.startMs,
    endMs: normalized.endMs,
    status: detail.status,
    statusLabel: detail.statusLabel,
    statusSource: 'dagrun',
    params: detail.params ?? subRun.params,
    dagRunId: detail.dagRunId,
    dagName: detail.dagName ?? subRun.dagName,
    depth: 1,
  };
}

function parseEndMs(timestamp: string | undefined, nowMs: number): number {
  if (!timestamp || timestamp === '-') {
    return nowMs;
  }
  return dayjs(timestamp).valueOf();
}

function normalizeTiming(
  startMs: number,
  endMs: number
): { startMs: number; endMs: number } | null {
  if (Number.isNaN(startMs) || Number.isNaN(endMs)) {
    return null;
  }

  if (endMs < startMs) {
    return { startMs, endMs: startMs + 100 };
  }

  return { startMs, endMs };
}
