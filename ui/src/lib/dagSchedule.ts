// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  components,
  RestartPolicy,
  ServiceDAGState,
  Status,
} from '../api/v1/schema';
import dayjs from './dayjs';

type Schedule = components['schemas']['Schedule'];
type ScheduleCoincidence = components['schemas']['ScheduleCoincidence'];
type Translate = (
  source: string,
  values?: Record<string, string | number>
) => string;
type ServiceDAG = components['schemas']['ServiceDAG'];
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

/** The label of a schedule, with the runtime profile that activates it. */
export function getScheduleSummary(schedule: Schedule): string {
  const label = getScheduleLabel(schedule);
  return schedule.profile ? `${label} (${schedule.profile})` : label;
}

/**
 * Why the index-th schedule starts no run at a time it shares with another
 * one, or undefined when it shares none. dayjsLocale names a dayjs locale.
 */
export function describeScheduleCoincidence(
  index: number,
  coincidences: ScheduleCoincidence[] | undefined,
  schedules: Schedule[],
  ts: Translate,
  dayjsLocale: string
): string | undefined {
  const coincidence = coincidences?.find(
    (item) => item.scheduleIndex === index
  );
  const own = schedules[index];
  const preceding = schedules[coincidence?.precedingScheduleIndex ?? -1];
  if (!coincidence || !own || !preceding) return undefined;
  const time = formatFireTime(
    coincidence.time,
    'ddd YYYY-MM-DD HH:mm',
    dayjsLocale
  );
  // Overrides tell apart two entries with the same timing.
  const summary = getScheduleSummary(preceding);
  const overrides =
    summary === getScheduleSummary(own)
      ? formatParamOverrides(preceding.paramOverrides)
      : '';
  return ts('Does not start at {time}: {schedule} starts instead.', {
    time: `${time} ${coincidence.timezone}`,
    schedule: [summary, overrides].filter(Boolean).join(' · '),
  });
}

/**
 * The parameter overrides of a schedule as `name: value` pairs, or an empty
 * string without overrides. Past max pairs, the rest are counted as `+N`.
 */
export function formatParamOverrides(
  overrides: Record<string, string> | undefined,
  max = Infinity
): string {
  const pairs = Object.entries(overrides ?? {}).map(
    ([name, value]) => `${name}: ${value}`
  );
  if (pairs.length <= max) return pairs.join(', ');
  return [...pairs.slice(0, max), `+${pairs.length - max}`].join(', ');
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

/**
 * What the list of service DAGs tells of the restarts of a DAG. A host passes
 * the service DAG it read.
 */
export type ServiceRestartFields = Pick<
  ServiceDAG,
  'state' | 'restartHold' | 'readError'
> & { suspended?: boolean };

/**
 * True when the scheduler starts the next run of a service DAG by itself: it
 * waits to restart or it fails and restarts. A manual start or retry adds
 * nothing. False while the restarts are on hold and for a DAG whose state
 * cannot be read: no run follows then, so a manual start applies.
 */
export function restartsByItself(
  service: ServiceRestartFields | undefined
): boolean {
  if (!service || service.restartHold || service.readError) {
    return false;
  }
  return (
    service.state === ServiceDAGState.restarting ||
    service.state === ServiceDAGState.failing
  );
}

// A run that has not started counts as active for this long, as on the server.
const NOT_STARTED_ACTIVE_MS = 5 * 60 * 1000;

/**
 * True when the server refuses a start, an enqueue or a retry of the DAG: it
 * is a service DAG and its latest run is running, queued or waiting, or has
 * not started for less than 5 minutes (spec 080).
 */
export function hasActiveServiceRun(
  dag: RestartPolicyFields | undefined,
  run: { status?: Status; queuedAt?: string; startedAt?: string } | undefined,
  now: number = Date.now()
): boolean {
  if (!dag || !run || !isServiceDAG(dag)) {
    return false;
  }
  if (
    run.status === Status.Running ||
    run.status === Status.Queued ||
    run.status === Status.Waiting
  ) {
    return true;
  }
  if (run.status !== Status.NotStarted) {
    return false;
  }
  const since = Date.parse(run.queuedAt || run.startedAt || '');
  return !Number.isNaN(since) && now - since < NOT_STARTED_ACTIVE_MS;
}

/**
 * The seconds after which a stopped run restarts, or null when no run is known
 * to follow a stop: `always` starts the next run after the wait,
 * `unless-stopped` does not, and neither does a DAG that is suspended or whose
 * restarts are on hold. Without the service DAG its state is unknown, so no
 * restart is promised.
 */
export function getRestartAfterStopSec(
  dag: RestartPolicyFields | undefined,
  service: ServiceRestartFields | undefined
): number | null {
  if (!service || service.suspended || service.restartHold) {
    return null;
  }
  return dag?.restartPolicy === RestartPolicy.always && dag.restartWaitSec
    ? dag.restartWaitSec
    : null;
}

/** The policy and the wait between two runs, such as "always · 5s". */
export function getRestartValue(dag: RestartPolicyFields): string {
  const wait = dag.restartWaitSec ? ` · ${dag.restartWaitSec}s` : '';
  return `${dag.restartPolicy}${wait}`;
}
