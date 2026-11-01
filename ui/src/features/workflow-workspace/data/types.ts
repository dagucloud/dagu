// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components, NodeStatus, paths, Status } from '@/api/v1/schema';
import type { RunOverlay } from '@/features/visual-editor/contracts';

export type ApiDAGRunDetails = components['schemas']['DAGRunDetails'];
export type ApiNode = components['schemas']['Node'];
/** One row of GET /dag-runs/{name}/{dagRunId}/sub-dag-runs. It is assignable to SubDAGRun. */
export type ApiSubRunDetail = components['schemas']['SubDAGRunDetail'];
/** Body of GET /dags/{fileName}. */
export type DAGPageData =
  paths['/dags/{fileName}']['get']['responses']['200']['content']['application/json'];

export type HandlerEvent =
  | 'onInit'
  | 'onWait'
  | 'onSuccess'
  | 'onFailure'
  | 'onAbort'
  | 'onExit';

export interface RunHandler {
  event: HandlerEvent;
  node: ApiNode;
}

export interface WaitingCounts {
  approvals: number;
  humanTasks: number;
  agent: number;
  firstWaitingStep: string | null;
}

/**
 * 'latest' when the shown run is the latest run of the workflow (the URL
 * names it, or names nothing yet); a run ID when the URL names another run.
 */
export type RunSelection = 'latest' | { runId: string };

export interface RunRef {
  name: string;
  runId: string;
}

export interface WorkspaceRunValue {
  selection: RunSelection;
  /** null when the workflow has never run. */
  ref: RunRef | null;
  /** The selected run, with the optimistic patch applied. */
  details: ApiDAGRunDetails | null;
  hasRun: boolean;
  isLatest: boolean;
  isActive: boolean;
  /** The pinned run was not found, so the selection fell back to latest. */
  notFound: boolean;
  /** The latest run of the workflow, whatever the selection. */
  latestRef: { runId: string; status: Status; at?: number | null } | null;
  overlay: RunOverlay;
  nodeByStep: ReadonlyMap<string, ApiNode>;
  handlers: RunHandler[];
  waiting: WaitingCounts;
  failedStep: string | null;
  select(sel: 'latest' | string, opts?: { replace?: boolean }): void;
  /**
   * Optimistic: patches details, overlay and nodeByStep at once. The patch is
   * dropped when server details of other content for the same run arrive, or
   * when the selection changes. An equal copy of the details keeps it.
   * Call it after a successful POST, then refresh().
   */
  applyNodeStatus(stepName: string, status: NodeStatus): void;
  refresh(): Promise<void>;
}

export interface WorkspacePermissions {
  /** Edit, Save, Rename, Delete, Copy to editor. */
  canWrite: boolean;
  /** Run..., Stop, Retry, Reschedule, Reject, Dequeue, Retry step, Set status. */
  canRunDags: boolean;
  /** Run unsaved draft, Run step. */
  canExecute: boolean;
  /** Approve, reject and submit of manual actions (together with canRunDags). */
  canExecuteForWorkspace: boolean;
  isAdmin: boolean;
  canManageProfiles: boolean;
  /** False on a remote node: the gate describes the local server only. */
  canManageWebhooks: boolean;
  canManageNotifications: boolean;
}
