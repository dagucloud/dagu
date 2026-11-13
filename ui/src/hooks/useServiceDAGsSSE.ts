// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '../api/v1/schema';
import { buildSSEEndpoint, SSEState, useSSE } from './useSSE';

type ServiceDAGsResponse = components['schemas']['ServiceDAGsResponse'];

interface ServiceDAGsParams {
  workspace?: components['parameters']['Workspace'];
}

/** The live list of the service DAGs. The server sends it at most every 2 s. */
export function useServiceDAGsSSE(
  params: ServiceDAGsParams = {},
  enabled: boolean = true,
  remoteNode?: string
): SSEState<ServiceDAGsResponse> {
  const endpoint = buildSSEEndpoint('/events/service-dags', params);
  return useSSE<ServiceDAGsResponse>(endpoint, enabled, remoteNode);
}

export type { ServiceDAGsParams };
