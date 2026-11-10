// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement, ReactNode } from 'react';
import { useConfig } from '@/contexts/ConfigContext';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import {
  cronToForm,
  formOfFrequency,
  formToCron,
  FREQUENCIES,
  LAST_COMMON_MONTH_DAY,
  MINUTE_INTERVALS,
  WEEK_DAYS,
  type Frequency,
  type ScheduleForm,
} from './scheduleForm';

type Translate = ReturnType<typeof useI18n>['ts'];

const FREQUENCY_LABELS: Record<Frequency, string> = {
  minutes: 'Every few minutes',
  hourly: 'Hourly',
  daily: 'Daily',
  weekly: 'Weekly',
  monthly: 'Monthly',
};

const MINUTES_OF_HOUR = Array.from({ length: 60 }, (_, minute) => minute);
const DAYS_OF_MONTH = Array.from({ length: 31 }, (_, index) => index + 1);

const CONTROL_CLASS =
  'h-8 min-w-0 rounded-md border border-input bg-background px-2 text-sm text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50';

const twoDigits = (value: number): string => String(value).padStart(2, '0');

const timeOfDay = (form: { hour: number; minute: number }): string =>
  `${twoDigits(form.hour)}:${twoDigits(form.minute)}`;

/** The name of a cron day of the week. 2023-01-01 is a Sunday. */
function weekDayName(
  day: number,
  locale: string,
  width: 'short' | 'long'
): string {
  return new Intl.DateTimeFormat(locale, {
    weekday: width,
    timeZone: 'UTC',
  }).format(new Date(Date.UTC(2023, 0, 1 + day)));
}

function describeForm(form: ScheduleForm, ts: Translate, locale: string) {
  switch (form.frequency) {
    case 'minutes':
      return form.every === 1
        ? ts('Every minute')
        : ts('Every {n} minutes', { n: form.every });
    case 'hourly':
      return ts('Every hour at minute {minute}', {
        minute: twoDigits(form.minute),
      });
    case 'daily':
      return ts('Every day at {time}', { time: timeOfDay(form) });
    case 'weekly':
      return ts('Every week on {days} at {time}', {
        days: form.days
          .map((day) => weekDayName(day, locale, 'short'))
          .join(', '),
        time: timeOfDay(form),
      });
    case 'monthly':
      return ts('Every month on day {day} at {time}', {
        day: form.day,
        time: timeOfDay(form),
      });
  }
}

/**
 * A cron expression in plain language. Renders nothing for an expression
 * that the form cannot show.
 */
export function ScheduleSummary({
  expression,
}: {
  expression: string;
}): ReactElement | null {
  const { ts, locale } = useI18n();
  const form = cronToForm(expression);
  if (form === null) return null;
  return (
    <p className="text-xs text-foreground" data-testid="ve-schedule-summary">
      {describeForm(form, ts, locale)}
    </p>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}): ReactElement {
  return (
    <label className="flex flex-col gap-1 text-xs font-medium text-muted-foreground">
      {label}
      {children}
    </label>
  );
}

/**
 * A form that writes a cron expression. The form is a view of the
 * expression: it shows the expression when it can, and a control writes a
 * new expression through onChange. An expression outside the form shows as
 * a custom expression and stays as it is until a frequency is chosen.
 */
export function ScheduleBuilder({
  expression,
  onChange,
}: {
  expression: string;
  onChange(expression: string): void;
}): ReactElement {
  const { ts, locale } = useI18n();
  const { tz } = useConfig();
  const form = cronToForm(expression);
  const write = (next: ScheduleForm) => onChange(formToCron(next));

  return (
    <div className="space-y-2" data-testid="ve-schedule-builder">
      <div className="flex flex-wrap items-end gap-2">
        <Field label={ts('Frequency')}>
          <select
            className={CONTROL_CLASS}
            value={form?.frequency ?? ''}
            onChange={(event) => {
              const frequency = event.target.value as Frequency | '';
              if (frequency !== '') write(formOfFrequency(frequency, form));
            }}
          >
            {form === null && (
              <option value="">
                {ts(
                  expression.trim() === ''
                    ? 'Choose a frequency'
                    : 'Custom expression'
                )}
              </option>
            )}
            {FREQUENCIES.map((frequency) => (
              <option key={frequency} value={frequency}>
                {ts(FREQUENCY_LABELS[frequency])}
              </option>
            ))}
          </select>
        </Field>
        {form?.frequency === 'minutes' && (
          <Field label={ts('Minutes between runs')}>
            <select
              className={CONTROL_CLASS}
              value={form.every}
              onChange={(event) =>
                write({ ...form, every: Number(event.target.value) })
              }
            >
              {MINUTE_INTERVALS.map((every) => (
                <option key={every} value={every}>
                  {every}
                </option>
              ))}
            </select>
          </Field>
        )}
        {form?.frequency === 'hourly' && (
          <Field label={ts('Minute of the hour')}>
            <select
              className={CONTROL_CLASS}
              value={form.minute}
              onChange={(event) =>
                write({ ...form, minute: Number(event.target.value) })
              }
            >
              {MINUTES_OF_HOUR.map((minute) => (
                <option key={minute} value={minute}>
                  {twoDigits(minute)}
                </option>
              ))}
            </select>
          </Field>
        )}
        {form?.frequency === 'monthly' && (
          <Field label={ts('Day of the month')}>
            <select
              className={CONTROL_CLASS}
              value={form.day}
              onChange={(event) =>
                write({ ...form, day: Number(event.target.value) })
              }
            >
              {DAYS_OF_MONTH.map((day) => (
                <option key={day} value={day}>
                  {day}
                </option>
              ))}
            </select>
          </Field>
        )}
        {form !== null && 'hour' in form && (
          <Field label={ts('Time of day')}>
            <input
              type="time"
              className={CONTROL_CLASS}
              value={timeOfDay(form)}
              onChange={(event) => {
                // A cleared time field has no value to write.
                const time = /^(\d{2}):(\d{2})/.exec(event.target.value);
                if (!time) return;
                write({
                  ...form,
                  hour: Number(time[1]),
                  minute: Number(time[2]),
                });
              }}
            />
          </Field>
        )}
      </div>
      {form?.frequency === 'weekly' && (
        <div
          role="group"
          aria-label={ts('Days of the week')}
          className="flex flex-wrap gap-1"
        >
          {WEEK_DAYS.map((day) => {
            const selected = form.days.includes(day);
            return (
              <button
                key={day}
                type="button"
                aria-pressed={selected}
                aria-label={weekDayName(day, locale, 'long')}
                // A weekly schedule needs at least one day.
                disabled={selected && form.days.length === 1}
                onClick={() =>
                  write({
                    ...form,
                    days: selected
                      ? form.days.filter((other) => other !== day)
                      : [...form.days, day],
                  })
                }
                className={cn(
                  'rounded-md border px-2 py-1 text-xs disabled:cursor-not-allowed',
                  selected
                    ? 'border-primary bg-primary text-primary-foreground'
                    : 'text-foreground hover:bg-accent'
                )}
              >
                {weekDayName(day, locale, 'short')}
              </button>
            );
          })}
        </div>
      )}
      {form?.frequency === 'monthly' && form.day > LAST_COMMON_MONTH_DAY && (
        <p className="text-xs text-warning">
          {ts('A month without day {day} has no run.', { day: form.day })}
        </p>
      )}
      {form !== null && form.frequency !== 'minutes' && (
        <p className="text-xs text-muted-foreground">
          {ts('Times are in the time zone of the scheduler ({tz}).', { tz })}
        </p>
      )}
    </div>
  );
}
