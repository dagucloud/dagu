// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '../api/v1/schema';
import { buildSSEEndpoint, SSEState, useSSE } from './useSSE';

type AlwaysOnDAGsResponse = components['schemas']['AlwaysOnDAGsResponse'];

interface AlwaysOnDAGsParams {
  workspace?: components['parameters']['Workspace'];
}

export function useAlwaysOnDAGsSSE(
  params: AlwaysOnDAGsParams = {},
  enabled: boolean = true
): SSEState<AlwaysOnDAGsResponse> {
  const endpoint = buildSSEEndpoint('/events/always-on-dags', params);
  return useSSE<AlwaysOnDAGsResponse>(endpoint, enabled);
}

export type { AlwaysOnDAGsParams };
