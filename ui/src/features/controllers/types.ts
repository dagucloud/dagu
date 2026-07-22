// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

export const ControllerStatus = {
  NotStarted: 0,
  Running: 1,
  Failed: 2,
  Aborted: 3,
  Success: 4,
  Queued: 5,
  PartialSuccess: 6,
  Waiting: 7,
  Rejected: 8,
} as const;

export type ControllerTransition = {
  to: string;
  when: string;
};

export type ControllerState = {
  description?: string;
  dags: string[];
  transitions: ControllerTransition[];
  terminal?: 'succeeded' | 'failed';
};

export type ControllerDefinition = {
  type: 'controller';
  version: 1;
  id?: string;
  name: string;
  description?: string;
  maxTurns: number;
  labels: string[];
  llm: {
    provider: string;
    model: string;
    system?: string;
  };
  dags: string[];
  states: Record<string, ControllerState>;
};

export type ControllerDAGRunRef = {
  state: string;
  dag: string;
  dagRunId: string;
};

export type ControllerToolCall = {
  id: string;
  type: 'function';
  function: {
    name: string;
    arguments: string;
  };
};

export type ControllerContextMessage = {
  role: 'user' | 'assistant' | 'tool';
  content?: string;
  tool_calls?: ControllerToolCall[];
  tool_call_id?: string;
  name?: string;
  metadata?: Record<string, unknown>;
};

export type ControllerRuntime = {
  status: number;
  statusLabel: string;
  currentState: string;
  turnCount: number;
  waitingQuestion?: string;
  activeDAGRun?: ControllerDAGRunRef;
  dagRunRefs: ControllerDAGRunRef[];
  context: ControllerContextMessage[];
  startedAt?: string;
  updatedAt?: string;
  finishedAt?: string;
  lastError?: {
    code: string;
    message?: string;
  };
};

export type ControllerSummary = {
  id: string;
  name: string;
  description?: string;
  workspace: string;
  status: number;
  statusLabel: string;
  currentState: string;
  turnCount: number;
  maxTurns: number;
  waitingQuestion?: string;
  activeDAGRun?: ControllerDAGRunRef;
  latestDAGRun?: {
    state: string;
    dag: string;
    dagRunId: string;
    status?: number;
    statusLabel?: string;
  };
  lastError?: {
    code: string;
    message?: string;
  };
  finishedAt?: string;
  resourceUpdatedAt: string;
};

export type ControllerDAGRunSummary = {
  dagRunId: string;
  name: string;
  workspace?: string;
  status: number;
  statusLabel: string;
  startedAt: string;
  finishedAt: string;
};

export type ControllerValidationIssue = {
  code: string;
  path: string;
  message: string;
  line?: number;
  column?: number;
};

export type ControllerDetail = {
  id: string;
  definition: ControllerDefinition;
  runtime: ControllerRuntime;
  dagRuns: ControllerDAGRunSummary[];
  spec: string;
  errors: ControllerValidationIssue[];
  warnings: ControllerValidationIssue[];
  resourceUpdatedAt: string;
};

export type ControllerListResponse = {
  controllers: ControllerSummary[];
};

export function isControllerActive(runtime?: ControllerRuntime): boolean {
  if (!runtime) return false;
  return (
    runtime.status === ControllerStatus.Running ||
    runtime.status === ControllerStatus.Waiting ||
    (runtime.status === ControllerStatus.Aborted && !runtime.finishedAt)
  );
}

export function controllerStatusText(
  status: number,
  finishedAt?: string
): string {
  switch (status) {
    case ControllerStatus.NotStarted:
      return 'None';
    case ControllerStatus.Running:
      return 'Running';
    case ControllerStatus.Waiting:
      return 'Waiting';
    case ControllerStatus.Success:
      return 'Success';
    case ControllerStatus.Failed:
    case ControllerStatus.Rejected:
      return 'Failed';
    case ControllerStatus.Aborted:
      return finishedAt ? 'Canceled' : 'Canceling…';
    default:
      return 'None';
  }
}

export function canStartController(
  status: number,
  finishedAt?: string
): boolean {
  return (
    status === ControllerStatus.NotStarted ||
    status === ControllerStatus.Success ||
    status === ControllerStatus.Failed ||
    status === ControllerStatus.Rejected ||
    (status === ControllerStatus.Aborted && Boolean(finishedAt))
  );
}

export function canEditController(runtime?: ControllerRuntime): boolean {
  return runtime ? !isControllerActive(runtime) && !runtime.activeDAGRun : true;
}
