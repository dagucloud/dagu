// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';

type DAGFile = components['schemas']['DAGFile'];

/** File of the first-launch example whose failing step recovers on retry. */
export const RETRY_EXAMPLE_FILE = 'example-06-retry-on-failure';

/**
 * Workflows read to tell an empty install from one with only examples. The
 * Activity empty state reads the same number, so both share one request.
 */
export const DAG_SAMPLE_SIZE = 10;

/** Reports whether a workflow is one of the first-launch examples. */
export function isExampleDAGName(name: string): boolean {
  return name.startsWith('example-');
}

/** How far a server is through its first steps, as one page of workflows shows. */
export type SampleProgress = {
  /** Some workflow has run. */
  ran: boolean;
  /** A workflow other than the examples exists. */
  created: boolean;
  /**
   * An unsuspended workflow has a schedule. Undefined when the page does not
   * hold every workflow, so the page cannot tell.
   */
  scheduled?: boolean;
  /** The retry example exists. */
  hasRetryExample: boolean;
};

/** Reads first-step progress from the first page of the workflow list. */
export function progressFromSample(list: {
  dags: DAGFile[];
  pagination: { totalRecords: number };
}): SampleProgress {
  const { dags } = list;
  const complete = list.pagination.totalRecords <= dags.length;
  return {
    ran: dags.some((dag) => !!dag.latestDAGRun?.dagRunId),
    created: !complete || dags.some((dag) => !isExampleDAGName(dag.dag.name)),
    scheduled: complete
      ? dags.some((dag) => !!dag.dag.schedule?.length && !dag.suspended)
      : undefined,
    hasRetryExample: dags.some((dag) => dag.fileName === RETRY_EXAMPLE_FILE),
  };
}
