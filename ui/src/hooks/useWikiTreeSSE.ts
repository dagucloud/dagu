// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  components,
  PathsWikiGetParametersQueryOrder,
  PathsWikiGetParametersQuerySort,
} from '../api/v1/schema';
import { buildSSEEndpoint, SSEState, useSSE } from './useSSE';

type WikiPageListResponse = components['schemas']['WikiPageListResponse'];

export function useWikiTreeSSE(
  params: {
    sort?: PathsWikiGetParametersQuerySort;
    order?: PathsWikiGetParametersQueryOrder;
    remoteNode?: components['parameters']['RemoteNode'];
    workspace?: components['parameters']['Workspace'];
  } = {},
  enabled: boolean = true
): SSEState<WikiPageListResponse> {
  const endpoint = buildSSEEndpoint('/events/wiki-tree', {
    perPage: 200,
    ...params,
  });
  return useSSE<WikiPageListResponse>(endpoint, enabled);
}
