// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

type DAGRunPageURLInput = {
  rootDAGRunName: string;
  rootDAGRunId: string;
  /**
   * The workflow file of the root run. A caller that has it passes it: the
   * name of a DAG can differ from the name of its file.
   */
  fileName?: string;
  remoteNode: string;
  /** Workspace scope (?workspace) to keep on the target page. */
  workspace?: string;
  /** A sub-run of the root run, which opens on the sub-run stack (?subRun). */
  subDAGRunId?: string;
  /** A step of the sub-run when subDAGRunId is set, else of the root run. */
  step?: string;
  /** Dock tab, for example 'artifacts'. */
  dock?: string;
};

type LegacyDAGPageURLInput = {
  fileName: string;
  remoteNode: string;
  workspace?: string;
  tab?: string;
  rootDAGRunName?: string;
  rootDAGRunId?: string;
  subDAGRunId?: string;
  step?: string;
  stream?: string;
  run?: never;
  dock?: never;
  mode?: never;
};

/** A workflow page URL in the form of the workspace: ?run selects the run, never ?dagRunId. */
type WorkspaceDAGPageURLInput = {
  fileName: string;
  remoteNode: string;
  workspace?: string;
  run?: string;
  /** A sub-run of the run to open on the sub-run stack. ?step is then its step. */
  subRun?: string;
  step?: string;
  /** Dock tab, for example 'log'. */
  dock?: string;
  tab?: never;
  rootDAGRunName?: never;
  rootDAGRunId?: never;
  subDAGRunId?: never;
  stream?: never;
};

type DAGPageURLInput = LegacyDAGPageURLInput | WorkspaceDAGPageURLInput;

function appendQuery(path: string, params: URLSearchParams): string {
  const query = params.toString();
  return query ? `${path}?${query}` : path;
}

/**
 * The URL of a run: the workflow page of the root run with ?run, and with
 * ?subRun for a sub-run. Callers that hold only a run list row pass its name
 * and id as the root.
 */
export function buildDAGRunPageURL({
  rootDAGRunName,
  rootDAGRunId,
  fileName,
  remoteNode,
  workspace,
  subDAGRunId,
  step,
  dock,
}: DAGRunPageURLInput): string {
  return buildWorkspaceDAGPageURL({
    fileName: fileName ?? rootDAGRunName,
    remoteNode,
    workspace,
    run: rootDAGRunId,
    subRun:
      subDAGRunId && subDAGRunId !== rootDAGRunId ? subDAGRunId : undefined,
    step,
    dock,
  });
}

export function buildDAGPageURL(input: DAGPageURLInput): string {
  if (input.run || input.dock) {
    return buildWorkspaceDAGPageURL(input as WorkspaceDAGPageURLInput);
  }
  const {
    fileName,
    remoteNode,
    workspace,
    tab,
    rootDAGRunName,
    rootDAGRunId,
    subDAGRunId,
    step,
    stream,
  } = input as LegacyDAGPageURLInput;
  const params = new URLSearchParams();
  params.set('remoteNode', remoteNode);
  if (workspace) {
    params.set('workspace', workspace);
  }
  if (rootDAGRunId) {
    params.set('dagRunId', rootDAGRunId);
  }
  if (rootDAGRunName) {
    params.set('dagRunName', rootDAGRunName);
  }
  if (subDAGRunId) {
    params.set('subDAGRunId', subDAGRunId);
  }
  if (step) {
    params.set('step', step);
  }
  if (stream) {
    params.set('stream', stream);
  }

  const path = tab
    ? `/dags/${encodeURIComponent(fileName)}/${encodeURIComponent(tab)}`
    : `/dags/${encodeURIComponent(fileName)}`;
  return appendQuery(path, params);
}

function buildWorkspaceDAGPageURL({
  fileName,
  remoteNode,
  workspace,
  run,
  subRun,
  step,
  dock,
}: WorkspaceDAGPageURLInput): string {
  const params = new URLSearchParams();
  params.set('remoteNode', remoteNode);
  if (workspace) {
    params.set('workspace', workspace);
  }
  if (run) {
    params.set('run', run);
  }
  if (subRun) {
    params.set('subRun', subRun);
  }
  if (step) {
    params.set('step', step);
  }
  if (dock) {
    params.set('dock', dock);
  }
  const path = `/dags/${encodeURIComponent(fileName)}`;
  return appendQuery(path, params);
}

/** The status tabs of the old run page that the Run panel of the workspace has as dock tabs. */
const LEGACY_RUN_TAB_DOCK: Readonly<Record<string, string>> = {
  timeline: 'timeline',
  outputs: 'outputs',
  artifacts: 'artifacts',
  agent: 'agent',
  chat: 'chat',
  tasks: 'tasks',
  approval: 'actions',
  'human-tasks': 'actions',
};

/**
 * The workflow page URL for a link to the removed run page
 * /dag-runs/:name/:dagRunId. A sub-run link (?subDAGRunId with the root run
 * in ?dagRunId and ?dagRunName) opens its root run with the sub-run in
 * ?subRun, and ?step is a step of the sub-run. remoteNode and workspace stay;
 * the old ?tab becomes ?dock.
 */
export function legacyRunPageRedirectURL(
  name: string,
  dagRunId: string,
  search: URLSearchParams,
  fallbackRemoteNode: string
): string {
  const subDAGRunId = search.get('subDAGRunId');
  const rootId = subDAGRunId ? search.get('dagRunId') : null;
  const rootName = subDAGRunId ? search.get('dagRunName') : null;
  const runId = rootId || dagRunId;
  const subRun = subDAGRunId && subDAGRunId !== runId ? subDAGRunId : undefined;
  // The tab of a sub-run link is a tab of the sub-run, not of the dock of
  // its root.
  const tab = subRun ? null : search.get('tab');
  return buildWorkspaceDAGPageURL({
    fileName: rootName || name,
    remoteNode: search.get('remoteNode')?.trim() || fallbackRemoteNode,
    workspace: search.get('workspace') || undefined,
    run: runId === 'latest' ? undefined : runId,
    subRun,
    step: search.get('step') || undefined,
    dock: (tab && LEGACY_RUN_TAB_DOCK[tab]) || undefined,
  });
}

type RunningTaskLike = {
  dagName: string;
  dagRunId: string;
  rootDagRunName?: string;
  rootDagRunId?: string;
  parentDagRunName?: string;
  parentDagRunId?: string;
};

/**
 * The run that a worker task opens: the task itself, or for a child task its
 * root run, or its parent when the root is not known.
 */
export function runningTaskRoot(task: RunningTaskLike): {
  name: string;
  dagRunId: string;
} {
  const isChild = Boolean(task.parentDagRunName && task.parentDagRunId);
  return {
    name:
      (isChild ? task.rootDagRunName || task.parentDagRunName : undefined) ||
      task.dagName,
    dagRunId:
      (isChild ? task.rootDagRunId || task.parentDagRunId : undefined) ||
      task.dagRunId,
  };
}

/** The URL of the run of a worker task, the run of runningTaskRoot. */
export function buildRunningTaskURL(
  task: RunningTaskLike,
  remoteNode: string
): string {
  const root = runningTaskRoot(task);
  return buildDAGRunPageURL({
    rootDAGRunName: root.name,
    rootDAGRunId: root.dagRunId,
    remoteNode,
  });
}
