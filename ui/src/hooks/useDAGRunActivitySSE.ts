// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '../api/v1/schema';
import { buildSSEEndpoint, SSEState, useSSE } from './useSSE';

type DAGRunActivity = components['schemas']['DAGRunActivity'];

type DAGRunActivitySSEParams = {
  hours?: number;
  labels?: string;
  workspace?: components['parameters']['Workspace'];
  /** Leave out the runs that a restart policy started. */
  excludeAlwaysOnRuns?: boolean;
};

/** Streams the activity of a window that ends now. */
export function useDAGRunActivitySSE(
  params: DAGRunActivitySSEParams,
  enabled: boolean = true,
  remoteNode?: string
): SSEState<DAGRunActivity> {
  const endpoint = buildSSEEndpoint('/events/dag-run-activity', params);
  return useSSE<DAGRunActivity>(endpoint, enabled, remoteNode);
}
