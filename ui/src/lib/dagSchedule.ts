// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, RestartPolicy } from '../api/v1/schema';

type Schedule = components['schemas']['Schedule'];
type RestartPolicyFields = Pick<
  components['schemas']['DAG'],
  'restartPolicy' | 'restartWaitSec'
>;

export function getScheduleLabel(schedule: Schedule): string {
  if (schedule.kind === 'at') {
    return schedule.at ? `at ${schedule.at}` : 'at';
  }

  return schedule.expression || '';
}

export function getScheduleKey(schedule: Schedule, index: number): string {
  const base =
    schedule.kind === 'at'
      ? schedule.at || 'at'
      : schedule.expression || 'cron';

  return `${base}-${index}`;
}

export function parseNextRun(nextRun?: string): Date | null {
  if (!nextRun) {
    return null;
  }

  const parsed = new Date(nextRun);
  if (Number.isNaN(parsed.getTime())) {
    return null;
  }

  return parsed;
}

/**
 * An always-on DAG is kept alive by the scheduler (restart_policy) instead of
 * following a schedule.
 */
export function isAlwaysOnDAG(dag: RestartPolicyFields): boolean {
  return (
    dag.restartPolicy === RestartPolicy.always ||
    dag.restartPolicy === RestartPolicy.unless_stopped
  );
}

/** The policy and the wait between two runs, such as "always · 5s". */
export function getRestartValue(dag: RestartPolicyFields): string {
  const wait = dag.restartWaitSec ? ` · ${dag.restartWaitSec}s` : '';
  return `${dag.restartPolicy}${wait}`;
}
