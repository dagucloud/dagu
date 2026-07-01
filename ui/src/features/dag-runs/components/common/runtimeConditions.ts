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
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1 $2')
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/\s+/g, ' ')
    .trim()
    .replace(/^./, (char) => char.toUpperCase());
}

export function runtimeConditionLabel(condition: RuntimeCondition): string {
  const isReady = condition.status === DAGRunConditionStatus.True;
  const isUnknown = condition.status === DAGRunConditionStatus.Unknown;

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
      if (isReady) return 'Concurrency ready';
      if (isUnknown) return 'Concurrency status unknown';
      return 'Concurrency not ready';
    case 'WorkerReady':
      if (isReady) return 'Worker ready';
      if (isUnknown) return 'Worker readiness unknown';
      return 'Worker not ready';
    case 'QueueReady':
      if (isReady) return 'Queue ready';
      if (isUnknown) return 'Queue state unavailable';
      return 'Queue not ready';
    case 'RunRecordReady':
      if (isReady) return 'Run record ready';
      if (isUnknown) return 'Run record status unknown';
      return 'Run record not ready';
    case 'WorkerAssignmentReady':
      if (isReady) return 'Worker assignment ready';
      if (isUnknown) return 'Worker assignment status unknown';
      return 'Worker assignment not ready';
    case 'StartObserved':
      if (isReady) return 'Startup observed';
      if (isUnknown) return 'Startup status unknown';
      return 'Startup not observed';
    default: {
      const label = humanizeIdentifier(condition.type);
      if (!label) {
        if (isReady) return 'Condition ready';
        if (isUnknown) return 'Condition status unknown';
        return 'Condition not ready';
      }
      if (isReady) return label;
      if (isUnknown) return `${label} status unknown`;
      return `${label} not ready`;
    }
  }
}
