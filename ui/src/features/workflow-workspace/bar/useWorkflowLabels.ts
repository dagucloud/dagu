// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { withoutWorkspaceLabels } from '@/lib/workspace';
import type { BarSlotProps } from '../contracts';
import { useWorkspaceUrl } from '../state/workspaceUrl';

/**
 * Labels of the workflow without the workspace labels (they scope the page;
 * the list filter drops them too), and the list URL of each label.
 */
export function useWorkflowLabels(dag: BarSlotProps['dag']) {
  const remoteNode = useRemoteNode();
  const [url] = useWorkspaceUrl();
  const labels = [
    ...new Set(
      withoutWorkspaceLabels([...(dag.labels ?? []), ...(dag.tags ?? [])])
    ),
  ];
  const scopedRemote = !!url.carry.remoteNode || remoteNode !== 'local';
  const listUrl = (label: string) => {
    const query = new URLSearchParams({ labels: label });
    if (scopedRemote) query.set('remoteNode', remoteNode);
    return `/dags?${query.toString()}`;
  };
  return { labels, listUrl };
}
