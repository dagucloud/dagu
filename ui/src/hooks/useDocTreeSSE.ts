// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  components,
  PathsDocsGetParametersQueryOrder,
  PathsDocsGetParametersQuerySort,
} from '../api/v1/schema';
import { buildSSEEndpoint, SSEState, useSSE } from './useSSE';

type DocListResponse = components['schemas']['DocListResponse'];

export function useDocTreeSSE(
  params: {
    sort?: PathsDocsGetParametersQuerySort;
    order?: PathsDocsGetParametersQueryOrder;
    remoteNode?: components['parameters']['RemoteNode'];
    workspace?: components['parameters']['Workspace'];
  } = {},
  enabled: boolean = true
): SSEState<DocListResponse> {
  const endpoint = buildSSEEndpoint('/events/docs-tree', {
    perPage: 200,
    ...params,
  });
  return useSSE<DocListResponse>(endpoint, enabled);
}
