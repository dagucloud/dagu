// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, LoopOnFailure, RestartPolicy } from '../api/v1/schema';

type Schedule = components['schemas']['Schedule'];
type RestartPolicyFields = Pick<
  components['schemas']['DAG'],
  'restartPolicy' | 'restartWaitSec' | 'loopWaitSec' | 'loopOnFailure'
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

/**
 * For a DAG that loops inside one run, the wait between two cycles and whether
 * a failed cycle ends the run: "loop 7s", "loop 7s · continue". Empty for a DAG
 * that does not loop.
 */
export function getLoopValue(dag: RestartPolicyFields): string {
  if (!dag.loopWaitSec) {
    return '';
  }
  const onFailure =
    dag.loopOnFailure === LoopOnFailure.continue ? ' · continue' : '';
  return `loop ${dag.loopWaitSec}s${onFailure}`;
}
