// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CalendarDayStatus } from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import React from 'react';
import {
  describeCalendarDay,
  formatCalendarDate,
  type CalendarDay,
} from './calendarDays';

// 2024-01-01 is a Monday; weeks start on Monday, as business weeks do.
const WEEK_DATES = Array.from(
  { length: 7 },
  (_, i) => new Date(Date.UTC(2024, 0, 1 + i))
);

export const DAY_STATUS_CLASSES: Record<CalendarDayStatus, string> = {
  [CalendarDayStatus.business]: 'text-foreground hover:bg-muted',
  [CalendarDayStatus.nonBusiness]: 'text-muted-foreground/50 hover:bg-muted',
  [CalendarDayStatus.holiday]:
    'bg-destructive/15 font-medium text-destructive hover:bg-destructive/25',
  [CalendarDayStatus.extraBusinessDay]:
    'bg-primary/15 font-medium text-primary hover:bg-primary/25',
};

type Props = {
  /** The month's dates, in order. */
  days: CalendarDay[];
  today: string;
  /** Dates after it count the weekdays alone. */
  through?: string;
  selected: string | null;
  onSelect: (date: string) => void;
  /** dayjs locale for date labels. */
  locale: string;
};

/** One month of a business calendar, a button per date. */
export function MonthGrid({
  days,
  today,
  through,
  selected,
  onSelect,
  locale,
}: Props): React.ReactElement | null {
  const { ts } = useI18n();
  const first = days[0];
  if (!first) {
    return null;
  }
  const firstDate = new Date(`${first.date}T00:00:00Z`);
  const offset = (firstDate.getUTCDay() + 6) % 7;
  const monthName = new Intl.DateTimeFormat(locale, {
    month: 'long',
    timeZone: 'UTC',
  }).format(firstDate);
  const weekdayFormat = new Intl.DateTimeFormat(locale, {
    weekday: 'narrow',
    timeZone: 'UTC',
  });

  return (
    <div role="group" aria-label={monthName}>
      <div className="mb-1 text-xs font-medium text-foreground">
        {monthName}
      </div>
      <div className="grid grid-cols-7 gap-px text-center text-[11px] tabular-nums">
        {WEEK_DATES.map((date) => (
          <div
            key={date.toISOString()}
            aria-hidden="true"
            className="text-[10px] text-muted-foreground"
          >
            {weekdayFormat.format(date)}
          </div>
        ))}
        {Array.from({ length: offset }, (_, i) => (
          <div key={`blank-${i}`} aria-hidden="true" />
        ))}
        {days.map((day) => {
          const label = `${formatCalendarDate(day.date, locale)}: ${describeCalendarDay(day, ts)}`;
          return (
            <button
              key={day.date}
              type="button"
              title={label}
              aria-label={label}
              aria-current={day.date === today ? 'date' : undefined}
              aria-pressed={selected === day.date}
              onClick={() => onSelect(day.date)}
              className={cn(
                'h-6 rounded-sm',
                DAY_STATUS_CLASSES[day.status],
                day.changed && 'ring-1 ring-inset ring-amber-500',
                through && day.date > through && 'opacity-50',
                day.date === today &&
                  'underline decoration-2 underline-offset-2',
                selected === day.date && 'outline outline-2 outline-primary'
              )}
            >
              {Number(day.date.slice(8))}
            </button>
          );
        })}
      </div>
    </div>
  );
}
