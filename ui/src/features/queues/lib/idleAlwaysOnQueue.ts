// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { QueueType, type components } from '@/api/v1/schema';

/**
 * The queue of an always-on DAG while no run is alive or waiting. The server
 * lists a DAG queue only then, so for the wait before each restart the page
 * draws the queue as idle and not as missing.
 */
export function idleAlwaysOnQueue(
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
