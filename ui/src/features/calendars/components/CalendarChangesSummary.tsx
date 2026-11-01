// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CalendarWeekday, type components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import React, { useState } from 'react';
import { calendarDayStatusLabel, formatCalendarDate } from './calendarDays';

type CalendarChanges = components['schemas']['CalendarChanges'];
type CalendarDateChange = components['schemas']['CalendarDateChange'];

// Monday first, as 2024-01-01 is a Monday.
const WEEKDAY_ORDER = [
  CalendarWeekday.MON,
  CalendarWeekday.TUE,
  CalendarWeekday.WED,
  CalendarWeekday.THU,
  CalendarWeekday.FRI,
  CalendarWeekday.SAT,
  CalendarWeekday.SUN,
];

type Props = {
  changes: CalendarChanges;
  /** Number of DAGs whose schedules the changes reach. */
  dagCount: number;
  /** dayjs locale for date labels. */
  locale: string;
};

/**
 * Says what saving would change: the listed dates and weekdays that differ
 * from the saved calendar, and how many DAGs that reaches.
 */
export function CalendarChangesSummary({
  changes,
  dagCount,
  locale,
}: Props): React.ReactElement | null {
  const { ts } = useI18n();
  const [expanded, setExpanded] = useState(false);
  const weekdayFormat = new Intl.DateTimeFormat(locale, {
    weekday: 'short',
    timeZone: 'UTC',
  });
  const weekdayName = (day: CalendarWeekday) =>
    weekdayFormat.format(Date.UTC(2024, 0, 1 + WEEKDAY_ORDER.indexOf(day)));
  const weekdays = [
    ...changes.weekdaysAdded.map((day) => `+${weekdayName(day)}`),
    ...changes.weekdaysRemoved.map((day) => `-${weekdayName(day)}`),
  ];
  if (changes.dates.length === 0 && weekdays.length === 0) {
    return null;
  }

  const parts: string[] = [];
  if (changes.dates.length > 0) {
    parts.push(
      ts(
        changes.dates.length === 1
          ? '{count} date changes'
          : '{count} dates change',
        {
          count: changes.dates.length,
        }
      )
    );
  }
  if (weekdays.length > 0) {
    parts.push(ts('weekdays {changes}', { changes: weekdays.join(' ') }));
  }
  parts.push(
    ts(dagCount === 1 ? 'used by {count} DAG' : 'used by {count} DAGs', {
      count: dagCount,
    })
  );

  return (
    <div
      role="status"
      className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm text-amber-900 dark:text-amber-100"
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium">
          <I18nText text={'Unsaved'} />
        </span>
        <span>{parts.join(' · ')}</span>
        {changes.dates.length > 0 && (
          <Button
            variant="link"
            size="xs"
            className="h-auto p-0"
            aria-expanded={expanded}
            onClick={() => setExpanded(!expanded)}
          >
            <I18nText text={expanded ? 'Hide dates' : 'Show dates'} />
          </Button>
        )}
      </div>
      {expanded && (
        <ul className="mt-1 space-y-0.5 tabular-nums">
          {changes.dates.map((change) => (
            <li key={change.date}>
              {formatCalendarDate(change.date, locale)}:{' '}
              {describeChange(change, ts)}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function describeChange(
  change: CalendarDateChange,
  ts: ReturnType<typeof useI18n>['ts']
): string {
  const side = (status: CalendarDateChange['before'], name?: string) =>
    name
      ? `${calendarDayStatusLabel(status, ts)} (${name})`
      : calendarDayStatusLabel(status, ts);
  return `${side(change.before, change.beforeHolidayName)} → ${side(change.after, change.afterHolidayName)}`;
}
