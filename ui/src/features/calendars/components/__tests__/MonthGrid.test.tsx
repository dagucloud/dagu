// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CalendarDayStatus } from '@/api/v1/schema';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { CalendarDay } from '../calendarDays';
import { describeCalendarDay } from '../calendarDays';
import { MonthGrid } from '../MonthGrid';

const ts = (text: string, values?: Record<string, string | number>) =>
  text.replace(/\{(\w+)\}/g, (_, key: string) => String(values?.[key]));

// January 2027 starts on a Friday holiday; the 11th is a named holiday.
function january2027(): CalendarDay[] {
  const days: CalendarDay[] = [];
  let ordinal = 0;
  for (let day = 1; day <= 31; day++) {
    const date = `2027-01-${String(day).padStart(2, '0')}`;
    const weekday = new Date(`${date}T00:00:00Z`).getUTCDay();
    if (day === 1 || day === 11) {
      days.push({
        date,
        status: CalendarDayStatus.holiday,
        holidayName: day === 11 ? 'Coming of Age Day' : undefined,
        changed: day === 11,
      });
    } else if (weekday === 0 || weekday === 6) {
      days.push({
        date,
        status: CalendarDayStatus.nonBusiness,
        changed: false,
      });
    } else {
      ordinal++;
      days.push({
        date,
        status: CalendarDayStatus.business,
        ordinal,
        changed: false,
      });
    }
  }
  return days.map((day) =>
    day.ordinal ? { ...day, ordinalFromEnd: day.ordinal - ordinal - 1 } : day
  );
}

describe('MonthGrid', () => {
  it('lays out the month from Monday and describes each date', () => {
    const onSelect = vi.fn();
    const { container } = render(
      <MonthGrid
        days={january2027()}
        today="2027-01-04"
        selected={null}
        onSelect={onSelect}
        locale="en"
      />
    );

    // Seven weekday headings, then Monday to Thursday left blank.
    expect(container.querySelectorAll('[aria-hidden="true"]')).toHaveLength(11);
    expect(
      screen.getByRole('button', {
        name: 'Mon 2027-01-11: Holiday: Coming of Age Day',
      })
    ).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole('button', {
        name: 'Mon 2027-01-04: Business day 1 of 19',
      })
    );
    expect(onSelect).toHaveBeenCalledWith('2027-01-04');
  });
});

describe('describeCalendarDay', () => {
  it('counts a business day from both ends of its month', () => {
    expect(
      describeCalendarDay(
        {
          date: '2027-10-09',
          status: CalendarDayStatus.extraBusinessDay,
          ordinal: 7,
          ordinalFromEnd: -16,
          changed: false,
        },
        ts
      )
    ).toBe('Extra business day, 7 of 22');
    expect(
      describeCalendarDay(
        {
          date: '2027-01-01',
          status: CalendarDayStatus.holiday,
          changed: false,
        },
        ts
      )
    ).toBe('Holiday');
  });
});
