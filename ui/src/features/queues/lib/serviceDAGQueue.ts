// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { QueueType, type components } from '@/api/v1/schema';

/**
 * Whether a service DAG runs in a DAG queue of its own, which is named after
 * it. A DAG that sets another queue shares that queue, so it owns none, and so
 * does one from a server older than the queue field, which leaves it out.
 */
export function ownsQueue(dag: components['schemas']['ServiceDAG']): boolean {
  return dag.queue === dag.name;
}

/**
 * The queue of a service DAG while no run is alive or waiting. The server
 * lists a DAG queue only then, so for the wait before each restart the page
 * draws the queue as idle and not as missing.
 */
export function idleServiceDAGQueue(
  name: string
): components['schemas']['Queue'] {
  return {
    name,
    type: QueueType.dag_based,
    maxConcurrency: 1,
    runningCount: 0,
    queuedCount: 0,
    running: [],
  };
}
