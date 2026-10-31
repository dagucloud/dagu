// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import type { ApiDAGRunDetails, ApiNode, DAGPageData } from '../types';

export const FILE_NAME = 'demo';
export const DAG_NAME = 'demo';

export function node(
  name: string,
  status: NodeStatus = NodeStatus.Success,
  extra: Record<string, unknown> = {}
): ApiNode {
  const { step, ...rest } = extra as { step?: Record<string, unknown> };
  return {
    step: { name, ...step },
    stdout: '',
    stderr: '',
    startedAt: '2026-01-15T02:00:00Z',
    finishedAt: '2026-01-15T02:00:03Z',
    status,
    statusLabel: NodeStatusLabel.succeeded,
    retryCount: 0,
    doneCount: 1,
    ...rest,
  } as ApiNode;
}

export function run(
  dagRunId: string,
  nodes: ApiNode[] = [node('a')],
  status: Status = Status.Success,
  extra: Partial<ApiDAGRunDetails> = {}
): ApiDAGRunDetails {
  return {
    dagRunId,
    name: DAG_NAME,
    status,
    statusLabel: StatusLabel.succeeded,
    nodes,
    ...extra,
  } as ApiDAGRunDetails;
}

/** The latestDAGRun the server sends for a workflow that never ran. */
export function zeroRun(): ApiDAGRunDetails {
  return {
    dagRunId: '',
    name: '',
    status: Status.NotStarted,
    statusLabel: StatusLabel.not_started,
    nodes: [],
  } as unknown as ApiDAGRunDetails;
}

export function dagPage(latestDAGRun: ApiDAGRunDetails): DAGPageData {
  return {
    dag: { name: DAG_NAME },
    latestDAGRun,
    localDags: [],
    suspended: false,
    pinned: false,
  } as unknown as DAGPageData;
}

export const idleSSE = {
  data: null,
  error: null,
  isConnected: false,
  isConnecting: false,
  shouldUseFallback: false,
};

export const connectedSSE = { ...idleSSE, isConnected: true };

export function deferred<T = unknown>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** The key prefixes that App.tsx revalidates on a workspace or node change. */
export const APP_WORKSPACE_PREFIXES = [
  '/dags/{fileName}',
  '/dag-runs/{name}/{dagRunId}',
] as const;
