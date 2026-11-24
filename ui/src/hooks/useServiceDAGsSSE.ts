// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '../api/v1/schema';
import { buildSSEEndpoint, SSEState, useSSE } from './useSSE';

type ServiceDAGsResponse = components['schemas']['ServiceDAGsResponse'];

interface ServiceDAGsParams {
  workspace?: components['parameters']['Workspace'];
}

export function useServiceDAGsSSE(
  params: ServiceDAGsParams = {},
  enabled: boolean = true
): SSEState<ServiceDAGsResponse> {
  const endpoint = buildSSEEndpoint('/events/service-dags', params);
  return useSSE<ServiceDAGsResponse>(endpoint, enabled);
}

export type { ServiceDAGsParams };
