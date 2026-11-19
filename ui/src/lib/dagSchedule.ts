// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, RestartPolicy } from '../api/v1/schema';
import dayjs from './dayjs';

type Schedule = components['schemas']['Schedule'];
type ScheduleCoincidence = components['schemas']['ScheduleCoincidence'];
type Translate = (
  source: string,
  values?: Record<string, string | number>
) => string;
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

/** The policy and the wait between two runs, such as "always · 5s". */
export function getRestartValue(dag: RestartPolicyFields): string {
  const wait = dag.restartWaitSec ? ` · ${dag.restartWaitSec}s` : '';
  return `${dag.restartPolicy}${wait}`;
}
