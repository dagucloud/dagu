// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/** A schedule that the form of the Cron panel can show and edit. */
export type ScheduleForm =
  | { frequency: 'minutes'; every: number }
  | { frequency: 'hourly'; minute: number }
  | { frequency: 'daily'; hour: number; minute: number }
  | { frequency: 'weekly'; hour: number; minute: number; days: number[] }
  | { frequency: 'monthly'; hour: number; minute: number; day: number };

export type Frequency = ScheduleForm['frequency'];

export const FREQUENCIES: readonly Frequency[] = [
  'minutes',
  'hourly',
  'daily',
  'weekly',
  'monthly',
];

/**
 * Minutes between two runs that a step of the minute field keeps exact. A
 * value that does not divide the hour restarts at minute 0 of each hour.
 */
export const MINUTE_INTERVALS: readonly number[] = [
  1, 2, 3, 4, 5, 6, 10, 12, 15, 20, 30,
];

/** Days of the week as cron numbers them: 0 is Sunday. */
export const WEEK_DAYS: readonly number[] = [0, 1, 2, 3, 4, 5, 6];

/** The last day of the month that every month has. */
export const LAST_COMMON_MONTH_DAY = 28;

const DAY_NAMES = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'];
const DEFAULT_HOUR = 9;
const DEFAULT_MINUTE_INTERVAL = 15;
const MONDAY = 1;
/** Consecutive days from this count on are written as a range. */
const MIN_RANGE_LENGTH = 3;

const isAny = (field: string): boolean => field === '*' || field === '?';

function toInteger(field: string, min: number, max: number): number | null {
  if (!/^\d{1,2}$/.test(field)) return null;
  const value = Number(field);
  return value >= min && value <= max ? value : null;
}

/** A day of the week by number (7 is Sunday too) or by name. */
function toWeekDay(token: string): number | null {
  const named = DAY_NAMES.indexOf(token.toLowerCase());
  return named >= 0 ? named : toInteger(token, 0, 7);
}

function toWeekDays(field: string): number[] | null {
  const days = new Set<number>();
  for (const part of field.split(',')) {
    const bounds = part.split('-');
    if (bounds.length > 2) return null;
    const first = toWeekDay(bounds[0] ?? '');
    const last = bounds.length === 2 ? toWeekDay(bounds[1] ?? '') : first;
    if (first === null || last === null || first > last) return null;
    for (let day = first; day <= last; day++) days.add(day % 7);
  }
  return [...days].sort((a, b) => a - b);
}

function toMinuteInterval(field: string): number | null {
  if (field === '*') return 1;
  const step = /^\*\/(\d{1,2})$/.exec(field);
  if (!step) return null;
  const every = Number(step[1]);
  return MINUTE_INTERVALS.includes(every) ? every : null;
}

/**
 * The form of a cron expression, or null when the form cannot show it. A
 * time zone prefix, a descriptor such as `@daily`, a month, a step of the
 * hours and a list of times are all outside the form.
 */
export function cronToForm(expression: string): ScheduleForm | null {
  const fields = expression.trim().split(/\s+/);
  if (fields.length !== 5) return null;
  const [minuteField, hourField, monthDayField, monthField, weekDayField] =
    fields as [string, string, string, string, string];
  if (monthField !== '*') return null;
  const anyMonthDay = isAny(monthDayField);
  const anyWeekDay = isAny(weekDayField);

  if (hourField === '*') {
    if (!anyMonthDay || !anyWeekDay) return null;
    const every = toMinuteInterval(minuteField);
    if (every !== null) return { frequency: 'minutes', every };
    const minute = toInteger(minuteField, 0, 59);
    return minute === null ? null : { frequency: 'hourly', minute };
  }

  const minute = toInteger(minuteField, 0, 59);
  const hour = toInteger(hourField, 0, 23);
  if (minute === null || hour === null) return null;
  if (anyMonthDay && anyWeekDay) return { frequency: 'daily', hour, minute };
  if (anyMonthDay) {
    const days = toWeekDays(weekDayField);
    return days === null ? null : { frequency: 'weekly', hour, minute, days };
  }
  if (!anyWeekDay) return null;
  const day = toInteger(monthDayField, 1, 31);
  return day === null ? null : { frequency: 'monthly', hour, minute, day };
}

/** Days as a cron field, with each run of consecutive days as a range. */
function weekDayField(days: readonly number[]): string {
  const sorted = [...new Set(days)].sort((a, b) => a - b);
  const parts: string[] = [];
  for (let start = 0; start < sorted.length; ) {
    let end = start;
    while (sorted[end + 1] === (sorted[end] as number) + 1) end++;
    if (end - start + 1 >= MIN_RANGE_LENGTH) {
      parts.push(`${sorted[start]}-${sorted[end]}`);
    } else {
      parts.push(...sorted.slice(start, end + 1).map(String));
    }
    start = end + 1;
  }
  return parts.join(',');
}

/** The cron expression of a form. */
export function formToCron(form: ScheduleForm): string {
  switch (form.frequency) {
    case 'minutes':
      return form.every === 1 ? '* * * * *' : `*/${form.every} * * * *`;
    case 'hourly':
      return `${form.minute} * * * *`;
    case 'daily':
      return `${form.minute} ${form.hour} * * *`;
    case 'weekly':
      return `${form.minute} ${form.hour} * * ${weekDayField(form.days)}`;
    case 'monthly':
      return `${form.minute} ${form.hour} ${form.day} * *`;
  }
}

/**
 * The form to show after a change of frequency. The time of day of the
 * previous form stays when both forms have one.
 */
export function formOfFrequency(
  frequency: Frequency,
  previous: ScheduleForm | null
): ScheduleForm {
  const timed = previous !== null && 'hour' in previous ? previous : null;
  const hour = timed?.hour ?? DEFAULT_HOUR;
  const minute = timed?.minute ?? 0;
  switch (frequency) {
    case 'minutes':
      return { frequency, every: DEFAULT_MINUTE_INTERVAL };
    case 'hourly':
      return { frequency, minute: 0 };
    case 'daily':
      return { frequency, hour, minute };
    case 'weekly':
      return { frequency, hour, minute, days: [MONDAY] };
    case 'monthly':
      return { frequency, hour, minute, day: 1 };
  }
}
