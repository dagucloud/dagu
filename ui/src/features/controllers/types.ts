// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Status, type components } from '@/api/v1/schema';

export { Status as ControllerStatus };

type PersistedControllerDefinition =
  components['schemas']['ControllerDefinition'];

// Draft definitions do not receive an ID until they are created. Provider stays
// broad so the YAML editor can represent and report unsupported string values.
export type ControllerDefinition = Omit<
  PersistedControllerDefinition,
  'type' | 'version' | 'id' | 'llm' | 'states'
> & {
  type: 'controller';
  version: 1;
  id?: string;
  llm: Omit<PersistedControllerDefinition['llm'], 'provider'> & {
    provider: string;
  };
  states: Record<string, ControllerState>;
};

export type ControllerState = Omit<
  components['schemas']['ControllerState'],
  'terminal'
> & {
  terminal?: 'succeeded' | 'failed';
};
export type ControllerContextMessage =
  components['schemas']['ControllerContextMessage'];
export type ControllerSummary = components['schemas']['ControllerSummary'];
export type ControllerValidationIssue = {
  code: string;
  path: string;
  message: string;
};
export type ControllerDetail = components['schemas']['ControllerDetail'];

type ControllerRuntime = components['schemas']['ControllerRuntime'];

export function isControllerActive(runtime?: ControllerRuntime): boolean {
  if (!runtime) return false;
  return isControllerStatusActive(runtime.status, runtime.finishedAt);
}

export function isControllerStatusActive(
  status: Status,
  finishedAt?: string
): boolean {
  return (
    status === Status.Running ||
    status === Status.Waiting ||
    (status === Status.Aborted && !finishedAt)
  );
}

export function controllerStatusText(
  status: Status,
  finishedAt?: string
): string {
  switch (status) {
    case Status.NotStarted:
      return 'None';
    case Status.Running:
      return 'Running';
    case Status.Waiting:
      return 'Waiting';
    case Status.Success:
      return 'Success';
    case Status.Failed:
      return 'Failed';
    case Status.Aborted:
      return finishedAt ? 'Canceled' : 'Canceling…';
    default:
      return 'Unknown';
  }
}

export function canStartController(
  status: Status,
  finishedAt?: string
): boolean {
  return (
    status === Status.NotStarted ||
    status === Status.Success ||
    status === Status.Failed ||
    (status === Status.Aborted && Boolean(finishedAt))
  );
}

export function canEditController(runtime?: ControllerRuntime): boolean {
  return runtime ? !isControllerActive(runtime) && !runtime.activeDAGRun : true;
}
