// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';

type DAGRunDetails = components['schemas']['DAGRunDetails'];

/** One sub-run of a tool DAG, as the run records it. */
export interface SubRunRef {
  dagRunId: string;
  /** The inputs of the call, as JSON text. */
  params?: string;
}

/**
 * The sub-runs of each tool DAG of each step, in the order of the run. The
 * keys are the server step name and the DAG name of the sub-run.
 */
export type ToolSubRuns = Record<string, Record<string, SubRunRef[]>>;

/**
 * The tool DAGs that ran as sub-runs of each step: the tool calls of a chat
 * step, with the id of each child run.
 */
export function buildToolSubRuns(details?: DAGRunDetails | null): ToolSubRuns {
  const runs: ToolSubRuns = {};
  for (const node of details?.nodes ?? []) {
    const step = node.step?.name;
    if (!step) continue;
    for (const sub of [
      ...(node.subRuns ?? []),
      ...(node.subRunsRepeated ?? []),
    ]) {
      if (!sub.dagName) continue;
      const own = (runs[step] ??= {});
      (own[sub.dagName] ??= []).push({
        dagRunId: sub.dagRunId,
        ...(sub.params ? { params: sub.params } : {}),
      });
    }
  }
  return runs;
}

/**
 * How many times each tool DAG ran as a sub-run of each step: the tool calls
 * of a chat step. The keys are the server step name and the DAG name of the
 * sub-run.
 */
export function buildToolCalls(
  details?: DAGRunDetails | null
): Record<string, Record<string, number>> {
  const calls: Record<string, Record<string, number>> = {};
  for (const [step, byDag] of Object.entries(buildToolSubRuns(details))) {
    calls[step] = Object.fromEntries(
      Object.entries(byDag).map(([dag, list]) => [dag, list.length])
    );
  }
  return calls;
}
