// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '../api/v1/schema';
import { SSEState, useSSE } from './useSSE';

type DAGDetails = components['schemas']['DAGDetails'];
type DAGRunDetails = components['schemas']['DAGRunDetails'];
type LocalDag = components['schemas']['LocalDag'];

interface DAGSSEResponse {
  dag: DAGDetails;
  latestDAGRun: DAGRunDetails;
  suspended: boolean;
  localDags: LocalDag[];
  errors: string[];
  spec?: string;
}

export function useDAGSSE(
  fileName: string,
  enabled: boolean = true,
  remoteNode?: string
): SSEState<DAGSSEResponse> {
  const endpoint = `/events/dags/${encodeURIComponent(fileName)}`;
  return useSSE<DAGSSEResponse>(endpoint, enabled, remoteNode);
}
