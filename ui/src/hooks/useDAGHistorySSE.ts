// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '../api/v1/schema';
import { SSEState, useSSE } from './useSSE';

type DAGRunDetails = components['schemas']['DAGRunDetails'];
type DAGGridItem = components['schemas']['DAGGridItem'];

interface DAGHistorySSEResponse {
  dagRuns: DAGRunDetails[];
  gridData: DAGGridItem[];
}

export function useDAGHistorySSE(
  fileName: string,
  enabled: boolean = true,
  remoteNode?: string
): SSEState<DAGHistorySSEResponse> {
  const endpoint = `/events/dags/${encodeURIComponent(fileName)}/dag-runs`;
  return useSSE<DAGHistorySSEResponse>(endpoint, enabled, remoteNode);
}
