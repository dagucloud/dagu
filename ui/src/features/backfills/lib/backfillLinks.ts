// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * The query that names the node of a link. The local node is named too: a
 * page without it would take the node the app bar selects.
 */
function nodeQuery(remoteNode: string): string {
  return remoteNode ? `remoteNode=${encodeURIComponent(remoteNode)}` : '';
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

/** The page of a run on a node. */
export function runPath(
  dagName: string,
  runId: string,
  remoteNode: string
): string {
  const query = nodeQuery(remoteNode);
  return `/dag-runs/${encodeURIComponent(dagName)}/${encodeURIComponent(runId)}${query ? `?${query}` : ''}`;
}

/** The page of a workflow on a node, by its file. */
export function workflowPath(fileName: string, remoteNode: string): string {
  const query = nodeQuery(remoteNode);
  return `/dags/${encodeURIComponent(fileName)}${query ? `?${query}` : ''}`;
}
