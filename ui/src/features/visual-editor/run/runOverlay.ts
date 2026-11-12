// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { NodeStatus, type components } from '@/api/v1/schema';
import type { RunOverlay, StepRunState } from '../contracts';
import { outputsFromRunId, runInputProvenance } from './inputProvenance';

type DAGRunDetails = components['schemas']['DAGRunDetails'];
type Node = components['schemas']['Node'];

const HANDLER_FIELDS = [
  'onInit',
  'onSuccess',
  'onFailure',
  'onAbort',
  'onExit',
  'onWait',
] as const;

// The server sends "-" for a time that is not set.
function time(value?: string): string | undefined {
  return value && value !== '-' ? value : undefined;
}

function toState(node: Node): StepRunState {
  const state: StepRunState = {
    status: node.status,
    statusLabel: node.statusLabel,
    retryCount: node.retryCount,
    doneCount: node.doneCount,
  };
  const startedAt = time(node.startedAt);
  const finishedAt = time(node.finishedAt);
  if (startedAt) state.startedAt = startedAt;
  if (finishedAt) state.finishedAt = finishedAt;
  if (node.error) state.error = node.error;
  return state;
}

/** Maps the nodes and handler nodes of a run to step states, keyed by server step name. */
export function buildRunOverlay(details?: DAGRunDetails): RunOverlay {
  const overlay: RunOverlay = {};
  if (!details) return overlay;
  const reused = runInputProvenance(details);
  // A run that took outputs from a source run: a step that it skipped without
  // carrying any is a step that the source could not give. The server writes
  // the label in lower case: a record of the run gives the id as it is.
  const labelled = outputsFromRunId(details.labels);
  const sourceRunId =
    labelled === null
      ? null
      : ([...reused.values()].find(
          (record) => record.runId?.toLowerCase() === labelled.toLowerCase()
        )?.runId ?? labelled);
  const nodes: (Node | undefined)[] = [
    ...(details.nodes ?? []),
    ...HANDLER_FIELDS.map((field) => details[field]),
  ];
  for (const node of nodes) {
    const name = node?.step?.name;
    if (!node || !name) continue;
    const state = toState(node);
    const source = reused.get(name);
    if (source) state.reused = source;
    else if (sourceRunId !== null && node.status === NodeStatus.Skipped) {
      state.reuseFailed = { runId: sourceRunId };
    }
    overlay[name] = state;
  }
  return overlay;
}
