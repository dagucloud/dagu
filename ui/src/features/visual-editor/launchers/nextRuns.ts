// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CronExpressionParser } from 'cron-parser';

/**
 * The next fire times of a cron expression, or null when the parser does not
 * read it. The server decides what is valid: a null is no error, only no
 * preview. `tz` is the time zone of the scheduler (`config.tz`); without it
 * the expression reads in the time zone of the browser.
 */
export function nextRuns(
  expression: string,
  count = 3,
  from: Date = new Date(),
  tz?: string
): Date[] | null {
  try {
    return CronExpressionParser.parse(expression, {
      currentDate: from,
      ...(tz ? { tz } : {}),
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
