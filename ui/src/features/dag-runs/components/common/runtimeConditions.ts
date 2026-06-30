// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, DAGRunConditionStatus } from '@/api/v1/schema';

export type RuntimeCondition = components['schemas']['DAGRunCondition'];

export function humanizeIdentifier(value: string | undefined): string {
  if (!value) {
    return '';
  }
  return value
    .replace(/[_-]+/g, ' ')
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/\s+/g, ' ')
    .trim()
    .replace(/^./, (char) => char.toUpperCase());
}

export function runtimeConditionLabel(condition: RuntimeCondition): string {
  const isReady = condition.status === DAGRunConditionStatus.True;

  switch (condition.type) {
    case 'Runnable':
      if (isReady) {
        return 'Runnable';
      }
      if (condition.status === DAGRunConditionStatus.False) {
        return 'Cannot start';
      }
      switch (condition.reason) {
        case 'AssignmentUnavailable':
          return 'Worker assignment unavailable';
        case 'WorkerDispatchUnavailable':
          return 'Worker dispatch unavailable';
        case 'QueueStateUnavailable':
          return 'Queue state unavailable';
        case 'RunLivenessUnavailable':
          return 'Run liveness unavailable';
        case 'StartupNotObserved':
          return 'Startup not observed';
        default:
          return 'Start status unknown';
      }
    case 'ConcurrencyReady':
      return isReady ? 'Concurrency ready' : 'Concurrency not ready';
    case 'WorkerReady':
      return isReady ? 'Worker ready' : 'Worker not ready';
    case 'QueueReady':
      return isReady ? 'Queue ready' : 'Queue state unavailable';
    case 'RunRecordReady':
      return isReady ? 'Run record ready' : 'Run record not ready';
    case 'WorkerAssignmentReady':
      return isReady ? 'Worker assignment ready' : 'Worker assignment not ready';
    case 'StartObserved':
      return isReady ? 'Startup observed' : 'Startup not observed';
    default: {
      const label = humanizeIdentifier(condition.type);
      if (!label) {
        return isReady ? 'Condition ready' : 'Condition not ready';
      }
      return isReady ? label : `${label} not ready`;
    }
  }
}
