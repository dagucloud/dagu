// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, operations } from '../api/v1/schema';
import { buildSSEEndpoint, SSEState, useSSE } from './useSSE';

type DAGListActivityPage = components['schemas']['DAGListActivityPage'];
type DAGListActivityParams =
  operations['getDAGListActivity']['parameters']['query'];

/**
 * Streams the activity of the DAGs of one DAG list page over a window that
 * ends now. The parameters are those of `GET /dag-list-activity`.
 */
export function useDAGListActivitySSE(
  params: DAGListActivityParams,
  enabled: boolean = true,
  remoteNode?: string
): SSEState<DAGListActivityPage> {
  const endpoint = buildSSEEndpoint('/events/dag-list-activity', params);
  return useSSE<DAGListActivityPage>(endpoint, enabled, remoteNode);
}
