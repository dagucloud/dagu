// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, RestartPolicy, Status } from '../api/v1/schema';
import dayjs from './dayjs';

type Schedule = components['schemas']['Schedule'];
type RestartPolicyFields = Pick<
  components['schemas']['DAG'],
  'restartPolicy' | 'restartWaitSec'
>;

export function getScheduleLabel(schedule: Schedule): string {
  if (schedule.kind === 'at') {
    return schedule.at ? `at ${schedule.at}` : 'at';
  }

  const parts = [schedule.expression || ''];
  if (schedule.calendar) {
    const days = schedule.businessDays?.length
      ? ` [${schedule.businessDays.join(', ')}]`
      : '';
    parts.push(`${schedule.calendar}${days}`);
  }
  return parts.join(' · ');
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

const UTC_OFFSET_SUFFIX = /([+-])(\d{2}):(\d{2})$/;

// formatFireTime formats an RFC 3339 time in the offset it was written with,
// so a fire shows the wall-clock time of the zone its schedule runs in.
export function formatFireTime(
  time: string,
  format: string,
  locale: string
): string {
  const match = UTC_OFFSET_SUFFIX.exec(time);
  const offsetMinutes = match
    ? (match[1] === '-' ? -1 : 1) * (Number(match[2]) * 60 + Number(match[3]))
    : 0;
  return dayjs(time).utcOffset(offsetMinutes).locale(locale).format(format);
}

/**
 * A service DAG is kept alive by the scheduler (restart_policy) instead of
 * following a schedule.
 */
export function isServiceDAG(dag: RestartPolicyFields): boolean {
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
 * Whether an unless-stopped DAG that is not suspended starts no new run: its
 * latest run was aborted, and an operator's stop is what the policy honors.
 */
export function isStoppedService(
  dag: RestartPolicyFields,
  suspended: boolean | undefined,
  latestStatus: Status | undefined
): boolean {
  return (
    dag.restartPolicy === RestartPolicy.unless_stopped &&
    !suspended &&
    latestStatus === Status.Aborted
  );
}

export function getRestartLabel(dag: RestartPolicyFields): string {
  return `restart: ${getRestartValue(dag)}`;
}
