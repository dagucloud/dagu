// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '../api/v1/schema';
import { SSEState, useSSE } from './useSSE';

type DAGRunChanges = components['schemas']['DAGRunChanges'];

/**
 * Streams the DAG files whose root runs changed lately, the same feed as
 * `GET /dag-run-changes`.
 */
export function useDAGRunChangesSSE(
  enabled: boolean = true,
  remoteNode?: string
): SSEState<DAGRunChanges> {
  return useSSE<DAGRunChanges>('/events/dag-run-changes', enabled, remoteNode);
}
