// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CalendarDayStatus, type components } from '@/api/v1/schema';
import type { useI18n } from '@/i18n/I18nProvider';
import dayjs from '@/lib/dayjs';

export type CalendarDay = components['schemas']['CalendarDay'];

type Translate = ReturnType<typeof useI18n>['ts'];

/** The day of a business calendar as a sentence, for labels and details. */
export function describeCalendarDay(day: CalendarDay, ts: Translate): string {
  switch (day.status) {
    case CalendarDayStatus.business:
      return ts('Business day {ordinal} of {total}', businessDayValues(day));
    case CalendarDayStatus.extraBusinessDay:
      return ts(
        'Extra business day, {ordinal} of {total}',
        businessDayValues(day)
      );
    case CalendarDayStatus.holiday:
      return day.holidayName
        ? ts('Holiday: {name}', { name: day.holidayName })
        : ts('Holiday');
    case CalendarDayStatus.nonBusiness:
      return ts('Not a business day');
  }
}

/** A calendar day status as a short noun phrase. */
export function calendarDayStatusLabel(
  status: CalendarDayStatus,
  ts: Translate
): string {
  switch (status) {
    case CalendarDayStatus.business:
      return ts('business day');
    case CalendarDayStatus.extraBusinessDay:
      return ts('extra business day');
    case CalendarDayStatus.holiday:
      return ts('holiday');
    case CalendarDayStatus.nonBusiness:
      return ts('not a business day');
  }
}

/** Formats a YYYY-MM-DD date with its weekday, as "Mon 2027-01-11". */
export function formatCalendarDate(date: string, locale: string): string {
  return dayjs(date).locale(locale).format('ddd YYYY-MM-DD');
}

// The API sets both ordinals on business days: the total follows from
// counting the day from both ends of its month.
function businessDayValues(day: CalendarDay): {
  ordinal: number;
  total: number;
} {
  const ordinal = day.ordinal ?? 0;
  const fromEnd = day.ordinalFromEnd ?? 0;
  return { ordinal, total: ordinal - fromEnd - 1 };
}
