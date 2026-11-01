// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback } from 'react';
import { useConfig } from '@/contexts/ConfigContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { sanitizeWorkspaceName } from '@/lib/workspace';
import { useWorkspaceUrl } from '../state/workspaceUrl';

/**
 * The link to the workflow, for the copy-link button of the bar. The link
 * names the workflow only: no run, no step.
 */
export function useWorkflowShare(fileName: string) {
  const config = useConfig();
  const remoteNode = useRemoteNode();
  const [url] = useWorkspaceUrl();

  const scopedRemote = !!url.carry.remoteNode || remoteNode !== 'local';
  // As the page reads it: an invalid workspace name scopes nothing.
  const scopedWorkspace = sanitizeWorkspaceName(url.carry.workspace ?? '');

  const getPageLink = useCallback(() => {
    const basePrefix = config.basePath === '/' ? '' : (config.basePath ?? '');
    const query = new URLSearchParams();
    if (scopedRemote) query.set('remoteNode', remoteNode);
    if (scopedWorkspace) query.set('workspace', scopedWorkspace);
    const search = query.toString();
    const path = `/dags/${encodeURIComponent(fileName)}`;
    return `${window.location.origin}${basePrefix}${path}${search ? `?${search}` : ''}`;
  }, [config.basePath, fileName, remoteNode, scopedRemote, scopedWorkspace]);

  return { getPageLink };
}
