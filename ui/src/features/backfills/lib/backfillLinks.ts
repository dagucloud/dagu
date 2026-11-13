// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/** The query that names a remote node in a link, empty for the local one. */
function nodeQuery(remoteNode: string): string {
  return remoteNode && remoteNode !== 'local'
    ? `remoteNode=${encodeURIComponent(remoteNode)}`
    : '';
}

/** The page of a backfill on a node. */
export function backfillPath(id: string, remoteNode: string): string {
  const query = nodeQuery(remoteNode);
  return `/backfills/${encodeURIComponent(id)}${query ? `?${query}` : ''}`;
}

/** The runs of a backfill on a node. */
export function backfillRunsPath(id: string, remoteNode: string): string {
  const query = nodeQuery(remoteNode);
  return `/dag-runs?backfillId=${encodeURIComponent(id)}${query ? `&${query}` : ''}`;
}

/** The page of a workflow on a node, by its file. */
export function workflowPath(fileName: string, remoteNode: string): string {
  const query = nodeQuery(remoteNode);
  return `/dags/${encodeURIComponent(fileName)}${query ? `?${query}` : ''}`;
}
