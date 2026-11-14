// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CalendarDayStatus } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { cn } from '@/lib/utils';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import React, { useMemo, useState } from 'react';
import type { CalendarPreview } from '../hooks/useCalendarPreview';
import {
  calendarDayStatusLabel,
  describeCalendarDay,
  formatCalendarDate,
  type CalendarDay,
} from './calendarDays';
import { DAY_STATUS_CLASSES, MonthGrid } from './MonthGrid';

const LEGEND_STATUSES = [
  CalendarDayStatus.business,
  CalendarDayStatus.holiday,
  CalendarDayStatus.extraBusinessDay,
  CalendarDayStatus.nonBusiness,
];

type Props = {
  preview: CalendarPreview | null;
  year: number;
  /** The last preview request failed. */
  failed: boolean;
  onRetry: () => void;
  onYearChange: (year: number) => void;
  /** dayjs locale for date labels. */
  locale: string;
};

/**
 * A year of a business calendar: each date colored by its status, with
 * unsaved changes ringed and the selected date described below.
 */
export function CalendarYearView({
  preview: lastPreview,
  year,
  failed,
  onRetry,
  onYearChange,
  locale,
}: Props): React.ReactElement {
  const { ts } = useI18n();
  const [selected, setSelected] = useState<string | null>(null);
  // A preview of another year stays out of view until this year's arrives.
  const preview = lastPreview?.year === year ? lastPreview : null;
  const months = useMemo(() => {
    const byMonth: CalendarDay[][] = Array.from({ length: 12 }, () => []);
    for (const day of preview?.days ?? []) {
      byMonth[Number(day.date.slice(5, 7)) - 1]?.push(day);
    }
    return byMonth;
  }, [preview]);
  const selectedDay =
    preview?.days.find((day) => day.date === selected) ?? null;
  const through = preview?.through;

  return (
    <div className="flex min-h-0 flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-1">
          <I18nProps>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="Previous year"
              onClick={() => onYearChange(year - 1)}
            >
              <ChevronLeft className="h-4 w-4" />
            </Button>
          </I18nProps>
          <span className="min-w-[3rem] text-center text-sm font-semibold tabular-nums">
            {year}
          </span>
          <I18nProps>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="Next year"
              onClick={() => onYearChange(year + 1)}
            >
              <ChevronRight className="h-4 w-4" />
            </Button>
          </I18nProps>
        </div>
        <ul className="flex flex-wrap items-center gap-3 text-[11px] text-muted-foreground">
          {LEGEND_STATUSES.map((status) => (
            <li key={status} className="flex items-center gap-1">
              <span
                aria-hidden="true"
                className={cn(
                  'inline-block h-3 w-3 rounded-sm border',
                  DAY_STATUS_CLASSES[status]
                )}
              />
              {calendarDayStatusLabel(status, ts)}
            </li>
          ))}
          <li className="flex items-center gap-1">
            <span
              aria-hidden="true"
              className="inline-block h-3 w-3 rounded-sm ring-1 ring-inset ring-amber-500"
            />
            <I18nText text={'unsaved change'} />
          </li>
        </ul>
      </div>

      {failed && (
        <p
          role="alert"
          className="flex flex-wrap items-center gap-2 text-sm text-destructive"
        >
          <I18nText text={"Couldn't preview the calendar."} />
          <Button variant="outline" size="xs" onClick={onRetry}>
            <I18nText text={'Retry'} />
          </Button>
        </p>
      )}
      {!preview ? (
        !failed && (
          <p className="text-sm text-muted-foreground">
            <I18nText text={'Loading {year}...'} values={{ year }} />
          </p>
        )
      ) : preview.days.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          <I18nText
            text={'Fix the problems in the file to see its business days.'}
          />
        </p>
      ) : (
        <div className="grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-3 2xl:grid-cols-4">
          {months.map((days, index) => (
            <MonthGrid
              key={`${year}-${index}`}
              days={days}
              today={preview?.today ?? ''}
              through={through}
              selected={selected}
              onSelect={setSelected}
              locale={locale}
            />
          ))}
        </div>
      )}

      <p className="min-h-5 text-sm text-muted-foreground" aria-live="polite">
        {selectedDay ? (
          <>
            <span className="font-medium text-foreground">
              {formatCalendarDate(selectedDay.date, locale)}
            </span>
            {' · '}
            {describeCalendarDay(selectedDay, ts)}
            {selectedDay.changed && <> · {ts('unsaved change')}</>}
            {through && selectedDay.date > through && (
              <>
                {' · '}
                {ts('after {through}, when the holidays end', { through })}
              </>
            )}
          </>
        ) : (
          <I18nText text={'Select a date to see its business day.'} />
        )}
      </p>
    </div>
  );
}
