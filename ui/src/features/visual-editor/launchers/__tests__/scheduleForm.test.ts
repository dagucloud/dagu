// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  cronToForm,
  formOfFrequency,
  formToCron,
  type ScheduleForm,
} from '../scheduleForm';

describe('formToCron', () => {
  it.each<[ScheduleForm, string]>([
    [{ frequency: 'minutes', every: 1 }, '* * * * *'],
    [{ frequency: 'minutes', every: 15 }, '*/15 * * * *'],
    [{ frequency: 'hourly', minute: 30 }, '30 * * * *'],
    [{ frequency: 'daily', hour: 9, minute: 5 }, '5 9 * * *'],
    [
      { frequency: 'weekly', hour: 18, minute: 0, days: [5, 1, 3] },
      '0 18 * * 1,3,5',
    ],
    [
      { frequency: 'weekly', hour: 9, minute: 0, days: [1, 2, 3, 4, 5] },
      '0 9 * * 1-5',
    ],
    [
      { frequency: 'weekly', hour: 9, minute: 0, days: [0, 1, 3, 4, 5, 6] },
      '0 9 * * 0,1,3-6',
    ],
    [{ frequency: 'monthly', hour: 0, minute: 0, day: 31 }, '0 0 31 * *'],
  ])('writes %o as %s', (form, expression) => {
    expect(formToCron(form)).toBe(expression);
    // The form reads back what it writes.
    expect(formToCron(cronToForm(expression)!)).toBe(expression);
  });
});

describe('cronToForm', () => {
  it('reads the days of the week by number, name and range', () => {
    const weekdays = { frequency: 'weekly', hour: 9, minute: 0 };
    expect(cronToForm('0 9 * * MON-FRI')).toEqual({
      ...weekdays,
      days: [1, 2, 3, 4, 5],
    });
    expect(cronToForm('0 9 * * sat,0')).toEqual({ ...weekdays, days: [0, 6] });
    // 7 is Sunday too.
    expect(cronToForm('0 9 * * 5-7')).toEqual({ ...weekdays, days: [0, 5, 6] });
  });

  it('reads ? as any day', () => {
    expect(cronToForm('0 9 1 * ?')).toEqual({
      frequency: 'monthly',
      hour: 9,
      minute: 0,
      day: 1,
    });
  });

  it.each([
    ['', 'an empty expression'],
    ['@daily', 'a descriptor'],
    ['CRON_TZ=Asia/Tokyo 0 9 * * *', 'a time zone prefix'],
    ['0 9 * 1 *', 'a month'],
    ['0 9 1 * 1', 'a day of the month with a day of the week'],
    ['0 9,18 * * *', 'a list of hours'],
    ['0 */2 * * *', 'a step of the hours'],
    ['*/7 * * * *', 'a step that does not divide the hour'],
    ['*/5 9 * * *', 'a step of the minutes in one hour'],
    ['0 9 * * 5-1', 'a range of days that goes backwards'],
    ['0 9 * * 8', 'a day of the week that does not exist'],
    ['0 24 * * *', 'an hour that does not exist'],
    ['0 9 32 * *', 'a day of the month that does not exist'],
  ])('gives null for %s (%s)', (expression) => {
    expect(cronToForm(expression)).toBeNull();
  });
});

describe('formOfFrequency', () => {
  it('keeps the time of day between forms that have one', () => {
    const daily: ScheduleForm = { frequency: 'daily', hour: 18, minute: 30 };
    expect(formOfFrequency('weekly', daily)).toEqual({
      frequency: 'weekly',
      hour: 18,
      minute: 30,
      days: [1],
    });
    expect(formOfFrequency('monthly', daily)).toEqual({
      frequency: 'monthly',
      hour: 18,
      minute: 30,
      day: 1,
    });
  });

  it('starts from a default without a previous time of day', () => {
    expect(formOfFrequency('daily', null)).toEqual({
      frequency: 'daily',
      hour: 9,
      minute: 0,
    });
    expect(
      formOfFrequency('daily', { frequency: 'hourly', minute: 30 })
    ).toEqual({ frequency: 'daily', hour: 9, minute: 0 });
    expect(formOfFrequency('minutes', null)).toEqual({
      frequency: 'minutes',
      every: 15,
    });
  });
});
