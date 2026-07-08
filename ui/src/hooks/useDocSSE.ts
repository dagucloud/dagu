// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '../api/v1/schema';
import { SSEState, useSSE } from './useSSE';

type DocResponse = components['schemas']['DocResponse'];

export function useDocSSE(
  docPath: string,
  enabled: boolean = true,
  workspaceQuery: {
    workspace?: components['parameters']['Workspace'];
  } = {},
  remoteNode: components['parameters']['RemoteNode'] = 'local'
): SSEState<DocResponse> {
  const encodedPath = docPath.split('/').map(encodeURIComponent).join('/');
  const params = new URLSearchParams();
  params.set('remoteNode', remoteNode);
  if (workspaceQuery.workspace) {
    params.set('workspace', workspaceQuery.workspace);
  }
  const query = params.toString();
  const endpoint = `/events/docs/${encodedPath}${query ? `?${query}` : ''}`;
  return useSSE<DocResponse>(endpoint, enabled && !!docPath);
}
