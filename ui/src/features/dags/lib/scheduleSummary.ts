// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CronExpressionParser } from 'cron-parser';
import { ScheduleKind, type components } from '@/api/v1/schema';

type Schedule = components['schemas']['Schedule'];

/** A message key of the schedule wording, with the values that fill it. */
export type ScheduleWording = {
  key: string;
  values?: Record<string, string | number>;
};

export interface ScheduleEntry {
  /** Stable key: the position of the schedule in the workflow definition. */
  id: string;
  kind: 'cron' | 'at';
  /** The expression of a cron schedule, or the timestamp of a one-off one. */
  expression: string;
  /** Short wording of the schedule, or null when only the expression says it. */
  wording: ScheduleWording | null;
  profile?: string;
  /** The next fire time; null when the schedule has none to come. */
  next: Date | null;
}

export interface ScheduleSummary {
  /** All schedules, soonest first; the ones without a next time follow. */
  entries: ScheduleEntry[];
  /** The schedule that fires next; the first schedule when none is to come. */
  lead: ScheduleEntry | null;
  /** The next run of the workflow; null when suspended or none is to come. */
  nextRun: Date | null;
  /** The number of schedules besides the lead. */
  extra: number;
  suspended: boolean;
}

export interface ScheduleSummaryOptions {
  suspended?: boolean;
  /**
   * The scheduler's own next run: it wins over the computed time. null says
   * that the scheduler plans none, as while it is paused; absent, the next
   * run comes from the expressions.
   */
  serverNextRun?: Date | null;
  /** IANA zone of the server for the expressions without a zone. */
  timeZone?: string;
}

const MINUTE_MS = 60_000;
// The server rounds a fire time to the minute; the match allows that gap.
const MATCH_WINDOW_MS = MINUTE_MS;

const DESCRIPTORS = new Map<string, string>([
  ['@hourly', '0 * * * *'],
  ['@daily', '0 0 * * *'],
  ['@midnight', '0 0 * * *'],
  ['@weekly', '0 0 * * 0'],
  ['@monthly', '0 0 1 * *'],
  ['@yearly', '0 0 1 1 *'],
  ['@annually', '0 0 1 1 *'],
]);

function validTimeZone(zone: string | undefined): string | undefined {
  if (!zone) return undefined;
  try {
    new Intl.DateTimeFormat(undefined, { timeZone: zone });
    return zone;
  } catch {
    return undefined;
  }
}

// "TZ=Asia/Tokyo 0 9 * * *" and "CRON_TZ=..." name the zone of one schedule.
function splitZone(expression: string): { zone?: string; body: string } {
  const match = /^(?:CRON_)?TZ=(\S+)\s+(.+)$/.exec(expression.trim());
  return match
    ? { zone: match[1], body: (match[2] ?? '').trim() }
    : { body: expression.trim() };
}

// "@every 15m", "@every 1h30m": whole minutes, as the scheduler needs.
function parseEveryMs(body: string): number | null {
  const match = /^@every\s+(?:(\d+)h)?(?:(\d+)m)?$/.exec(body);
  if (!match || (!match[1] && !match[2])) return null;
  const minutes = Number(match[1] ?? 0) * 60 + Number(match[2] ?? 0);
  return minutes > 0 ? minutes * MINUTE_MS : null;
}

const pad = (value: string) => value.padStart(2, '0');
const isNumber = (value: string) => /^\d+$/.test(value);

function describeMinutes(minutes: number): ScheduleWording {
  if (minutes % 60 === 0) {
    return { key: 'every {n} h', values: { n: minutes / 60 } };
  }
  return { key: 'every {n} min', values: { n: minutes } };
}

function describeCron(body: string): ScheduleWording | null {
  const every = parseEveryMs(body);
  if (every !== null) return describeMinutes(every / MINUTE_MS);

  const fields = (DESCRIPTORS.get(body) ?? body).split(/\s+/);
  if (fields.length !== 5) return null;
  const [minute = '', hour = '', dom = '', month = '', dow = ''] = fields;
  if (dom !== '*' || month !== '*') return null;

  if (dow === '*') {
    if (minute === '*' && hour === '*') return { key: 'every minute' };
    // A step that does not divide its range leaves an uneven last gap.
    const step = /^\*\/(\d+)$/.exec(minute);
    if (step && hour === '*') {
      const n = Number(step[1]);
      if (n > 0 && 60 % n === 0) return describeMinutes(n);
    }
    if (isNumber(minute) && hour === '*') {
      return minute === '0'
        ? { key: 'hourly' }
        : { key: 'hourly at :{minute}', values: { minute: pad(minute) } };
    }
    const hourStep = /^\*\/(\d+)$/.exec(hour);
    if (minute === '0' && hourStep) {
      const n = Number(hourStep[1]);
      if (n > 0 && 24 % n === 0) return describeMinutes(n * 60);
    }
  }

  if (isNumber(minute) && isNumber(hour)) {
    const time = `${pad(hour)}:${pad(minute)}`;
    if (dow === '*') return { key: 'daily at {time}', values: { time } };
    if (dow === '1-5' || dow.toUpperCase() === 'MON-FRI') {
      return { key: 'weekdays at {time}', values: { time } };
    }
  }
  return null;
}

function nextFireTime(
  body: string,
  zone: string | undefined,
  now: Date
): Date | null {
  const every = parseEveryMs(body);
  if (every !== null) {
    // The scheduler fires on a grid aligned to the Unix epoch.
    return new Date((Math.floor(now.getTime() / every) + 1) * every);
  }
  try {
    return CronExpressionParser.parse(DESCRIPTORS.get(body) ?? body, {
      currentDate: now,
      tz: zone,
    })
      .next()
      .toDate();
  } catch {
    // The server decides what is valid: no parse is no preview.
    return null;
  }
}

interface Candidate {
  entry: ScheduleEntry;
  /** The time that the server match uses, also for a one-off in the past. */
  matchTime: Date | null;
}

function describeEntry(
  schedule: Schedule,
  index: number,
  now: Date,
  defaultZone: string | undefined
): Candidate {
  const profile = schedule.profile || undefined;
  if (schedule.kind === ScheduleKind.at) {
    const at = schedule.at ? new Date(schedule.at) : null;
    const valid = at !== null && !Number.isNaN(at.getTime());
    return {
      entry: {
        id: `at-${index}`,
        kind: 'at',
        expression: schedule.at ?? '',
        wording: { key: 'once' },
        profile,
        next: valid && at.getTime() > now.getTime() ? at : null,
      },
      matchTime: valid ? at : null,
    };
  }
  const expression = schedule.expression ?? '';
  const { zone, body } = splitZone(expression);
  const next = nextFireTime(body, validTimeZone(zone) ?? defaultZone, now);
  return {
    entry: {
      id: `cron-${index}`,
      kind: 'cron',
      expression,
      wording: describeCron(body),
      profile,
      next,
    },
    matchTime: next,
  };
}

/**
 * The schedules of a workflow, soonest first, with the one that fires next.
 * The scheduler's own next run, when given, names the lead schedule and gives
 * its time; the others come from the cron expressions.
 */
export function summarizeSchedules(
  schedules: readonly Schedule[],
  now: Date,
  options: ScheduleSummaryOptions = {}
): ScheduleSummary {
  const suspended = options.suspended === true;
  const defaultZone = validTimeZone(options.timeZone);
  const candidates = schedules.map((schedule, index) =>
    describeEntry(schedule, index, now, defaultZone)
  );

  const server =
    !suspended && options.serverNextRun && !Number.isNaN(+options.serverNextRun)
      ? options.serverNextRun
      : null;
  let matched: Candidate | null = null;
  if (server) {
    let best = MATCH_WINDOW_MS + 1;
    for (const candidate of candidates) {
      if (!candidate.matchTime) continue;
      const gap = Math.abs(candidate.matchTime.getTime() - server.getTime());
      if (gap < best) {
        best = gap;
        matched = candidate;
      }
    }
    if (matched) matched.entry.next = server;
  }

  const entries = candidates
    .map((candidate) => candidate.entry)
    .sort((a, b) => {
      if (a.next && b.next) return a.next.getTime() - b.next.getTime();
      if (a.next) return -1;
      if (b.next) return 1;
      return 0;
    });

  let lead: ScheduleEntry | null = null;
  let nextRun: Date | null = null;
  if (!suspended) {
    if (matched) {
      lead = matched.entry;
      nextRun = server;
    } else if (entries[0]?.next) {
      lead = entries[0];
      nextRun = server ?? (options.serverNextRun === null ? null : lead.next);
    } else if (server) {
      nextRun = server;
    }
  }
  return {
    entries,
    lead: lead ?? entries[0] ?? null,
    nextRun,
    extra: Math.max(0, entries.length - 1),
    suspended,
  };
}
