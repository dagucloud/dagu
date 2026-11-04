// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback } from 'react';
import { useConfig } from '@/contexts/ConfigContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { sanitizeWorkspaceName } from '@/lib/workspace';
import { useWorkspaceUrl } from '../state/workspaceUrl';

/**
 * The links to the workflow, for the copy-link buttons: getPageLink names the
 * workflow only, with no run and no step; getRunLink names one run of it,
 * and a sub-run of that run that the link opens on the sub-run stack.
 * A run link names the run by its DAG name, as the run links of the server
 * do, and always names its node: it opens the same run for anyone, also
 * after the file is renamed.
 */
export function useWorkflowShare(fileName: string) {
  const config = useConfig();
  const remoteNode = useRemoteNode();
  const [url] = useWorkspaceUrl();

  const scopedRemote = !!url.carry.remoteNode || remoteNode !== 'local';
  // As the page reads it: an invalid workspace name scopes nothing.
  const scopedWorkspace = sanitizeWorkspaceName(url.carry.workspace ?? '');

  const getRunLink = useCallback(
    (run?: { name: string; dagRunId: string }, subRunId?: string) => {
      const basePrefix = config.basePath === '/' ? '' : (config.basePath ?? '');
      const query = new URLSearchParams();
      if (scopedRemote || run) query.set('remoteNode', remoteNode);
      if (scopedWorkspace) query.set('workspace', scopedWorkspace);
      if (run) query.set('run', run.dagRunId);
      if (run && subRunId) query.set('subRun', subRunId);
      const search = query.toString();
      const path = `/dags/${encodeURIComponent(run?.name ?? fileName)}`;
      return `${window.location.origin}${basePrefix}${path}${search ? `?${search}` : ''}`;
    },
    [config.basePath, fileName, remoteNode, scopedRemote, scopedWorkspace]
  );
  const getPageLink = useCallback(() => getRunLink(), [getRunLink]);

  return { getPageLink, getRunLink };
}
