// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CronExpressionParser } from 'cron-parser';
import dayjs from '@/lib/dayjs';

/** A leading `TZ=` or `CRON_TZ=` field, as the scheduler reads it. */
const ZONE_PREFIX = /^(?:CRON_)?TZ=(\S+)\s+(.*)$/;

/**
 * The next fire times of a cron expression, or null when the parser does not
 * read it. The server decides what is valid: a null is no error, only no
 * preview.
 *
 * The scheduler evaluates an expression in its own zone (`tz`, the zone that
 * the server reports) unless the expression starts with `TZ=` or `CRON_TZ=`,
 * which wins. Without `tz` the zone of the browser applies.
 */
export function nextRuns(
  expression: string,
  count = 3,
  from: Date = new Date(),
  tz?: string
): Date[] | null {
  const prefixed = ZONE_PREFIX.exec(expression);
  const body = prefixed?.[2] ?? expression;
  const zone = prefixed?.[1] ?? (tz || undefined);
  // The scheduler has no zone for an interval: it refuses a prefix there.
  if (prefixed && body.startsWith('@every ')) return null;
  try {
    return CronExpressionParser.parse(body, {
      currentDate: from,
      ...(zone ? { tz: zone } : {}),
    })
      .take(count)
      .map((date) => date.toDate());
  } catch {
    return null;
  }
}

/** The zone in which the scheduler reads an expression, if it is known. */
export function scheduleZone(
  expression: string,
  tz?: string
): string | undefined {
  return ZONE_PREFIX.exec(expression)?.[1] ?? (tz || undefined);
}

/**
 * The wall clock of an instant in a zone, as `YYYY-MM-DD HH:mm`. The zone is
 * a name or the `UTC+9` form that the server reports; without a readable zone
 * the clock of the browser applies.
 */
export function formatInZone(date: Date, zone?: string): string {
  const value = dayjs(date);
  const offset = zone ? /^UTC([+-]\d+)$/.exec(zone) : null;
  let zoned = value;
  if (offset) {
    zoned = value.utcOffset(Number(offset[1]) * 60);
  } else if (zone) {
    try {
      zoned = value.tz(zone);
    } catch {
      // An unreadable zone gives the clock of the browser.
    }
  }
  return zoned.format('YYYY-MM-DD HH:mm');
}

/** Starter schedules, as a static message key and its expression. */
export const SCHEDULE_PRESETS: ReadonlyArray<{
  label: string;
  expression: string;
}> = [
  { label: 'Hourly', expression: '0 * * * *' },
  { label: 'Daily at 09:00', expression: '0 9 * * *' },
  { label: 'Weekdays at 09:00', expression: '0 9 * * 1-5' },
];

/** The schedule that the Cron item of the Add step panel writes. */
export const DEFAULT_SCHEDULE = '0 * * * *';
