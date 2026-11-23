// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CronExpressionParser } from 'cron-parser';

/** A leading `TZ=` or `CRON_TZ=` field, as the scheduler reads it. */
export const CRON_ZONE_PREFIX = /^(?:CRON_)?TZ=(\S+)\s+/;

/**
 * Zone IDs that the browser takes and the time zone database of the server
 * lacks: the three-letter IDs that ICU keeps from Java.
 */
const BROWSER_ONLY_ZONES = new Set([
  'ACT',
  'AET',
  'AGT',
  'ART',
  'AST',
  'BET',
  'BST',
  'CAT',
  'CNT',
  'CST',
  'CTT',
  'EAT',
  'ECT',
  'IET',
  'IST',
  'JST',
  'MIT',
  'NET',
  'NST',
  'PLT',
  'PNT',
  'PRT',
  'PST',
  'SST',
  'VST',
]);

/** A UTC offset such as `+09:00`, which the browser takes as a zone. */
const UTC_OFFSET_ZONE = /^[+-]/;

/**
 * The zone that a `TZ=` field names: `Local` is the zone of the scheduler.
 * null for a name that the server refuses: one that the browser does not
 * know, a legacy ID of the browser or a UTC offset.
 */
function prefixZone(name: string, tz: string | undefined): string | null {
  if (name === 'Local') return tz ?? '';
  if (BROWSER_ONLY_ZONES.has(name) || UTC_OFFSET_ZONE.test(name)) return null;
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: name });
    return name;
  } catch {
    return null;
  }
}

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
  const prefixed = CRON_ZONE_PREFIX.exec(expression);
  const body = prefixed ? expression.slice(prefixed[0].length) : expression;
  const zone = prefixed ? prefixZone(prefixed[1]!, tz) : (tz ?? '');
  if (zone === null) return null;
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
