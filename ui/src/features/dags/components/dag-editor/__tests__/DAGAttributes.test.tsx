// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it } from 'vitest';
import { ScheduleKind, UpcomingRunsHold } from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import dayjs from '@/lib/dayjs';
import DAGAttributes from '../DAGAttributes';

afterEach(() => {
  cleanup();
});

describe('DAGAttributes', () => {
  it('renders the next scheduled run when present', () => {
    const nextRun = '2026-04-03T12:00:00Z';

    render(
      <DAGAttributes
        dag={{
          name: 'scheduled-dag',
          schedule: [{ expression: '0 12 * * *', kind: ScheduleKind.cron }],
          nextRun,
        }}
      />
    );

    expect(screen.getByText('Next run:')).toBeInTheDocument();
    expect(
      screen.getByText(
        `${dayjs(nextRun).format('YYYY-MM-DD HH:mm:ss')} (${dayjs(nextRun).fromNow()})`
      )
    ).toBeInTheDocument();
  });

  it('renders no upcoming run when the next run is unavailable', () => {
    render(
      <DAGAttributes
        dag={{
          name: 'scheduled-dag',
          schedule: [{ expression: '0 12 * * *', kind: ScheduleKind.cron }],
        }}
      />
    );

    expect(screen.getByText('Next run:')).toBeInTheDocument();
    expect(screen.getByText('No upcoming run')).toBeInTheDocument();
  });

  it('lists upcoming runs in the wall-clock time of their zone', () => {
    render(
      <DAGAttributes
        dag={{
          name: 'payroll',
          schedule: [
            {
              expression: 'CRON_TZ=Asia/Tokyo 0 9 25 * *',
              kind: ScheduleKind.cron,
            },
          ],
          upcomingRuns: [
            {
              time: '2027-01-25T09:00:00+09:00',
              scheduleIndex: 0,
              timezone: 'Asia/Tokyo',
            },
            {
              time: '2027-02-25T09:00:00+09:00',
              scheduleIndex: 0,
              timezone: 'Asia/Tokyo',
            },
          ],
        }}
      />
    );

    expect(screen.getByText('Upcoming runs')).toBeInTheDocument();
    expect(screen.getByText('(Asia/Tokyo)')).toBeInTheDocument();
    expect(screen.getByText('Mon 2027-01-25 09:00')).toBeInTheDocument();
    expect(screen.getByText('Thu 2027-02-25 09:00')).toBeInTheDocument();
    // An unsaved spec has fire times but no scheduler projection.
    expect(screen.queryByText('No upcoming run')).not.toBeInTheDocument();
  });

  it('names the zone and schedule of each run when they differ', () => {
    render(
      <DAGAttributes
        dag={{
          name: 'two-zones',
          schedule: [
            { expression: '0 18 * * *', kind: ScheduleKind.cron },
            {
              expression: 'CRON_TZ=Asia/Tokyo 0 9 * * *',
              kind: ScheduleKind.cron,
              profile: 'prod',
            },
          ],
          upcomingRuns: [
            {
              time: '2027-01-01T09:00:00+09:00',
              scheduleIndex: 1,
              timezone: 'Asia/Tokyo',
            },
            { time: '2027-01-01T18:00:00Z', scheduleIndex: 0, timezone: 'UTC' },
          ],
        }}
      />
    );

    expect(
      screen.getByText('Fri 2027-01-01 09:00 Asia/Tokyo', { exact: false })
    ).toBeInTheDocument();
    expect(
      screen.getByText('· CRON_TZ=Asia/Tokyo 0 9 * * * (prod)')
    ).toBeInTheDocument();
    expect(
      screen.getByText('Fri 2027-01-01 18:00 UTC', { exact: false })
    ).toBeInTheDocument();
  });

  it('explains where a business calendar put each run', () => {
    render(
      <DAGAttributes
        dag={{
          name: 'payroll',
          schedule: [
            {
              expression: '0 23 * * *',
              kind: ScheduleKind.cron,
              calendar: 'jp-banking',
              businessDays: [3, -1],
            },
          ],
          upcomingRuns: [
            {
              time: '2027-01-06T23:00:00Z',
              scheduleIndex: 0,
              timezone: 'UTC',
              businessDay: 3,
            },
            {
              time: '2027-01-29T23:00:00Z',
              scheduleIndex: 0,
              timezone: 'UTC',
              businessDay: -1,
            },
            {
              time: '2027-01-12T18:00:00Z',
              scheduleIndex: 0,
              timezone: 'UTC',
              skippedHolidays: [
                { date: '2027-01-11', name: 'Coming of Age Day' },
              ],
            },
            {
              time: '2027-10-09T18:00:00Z',
              scheduleIndex: 0,
              timezone: 'UTC',
              extraBusinessDay: true,
            },
          ],
        }}
      />
    );

    expect(
      screen.getByText('0 23 * * * · jp-banking [3, -1]')
    ).toBeInTheDocument();
    expect(
      screen.getByText('· business day 3 of the month')
    ).toBeInTheDocument();
    expect(
      screen.getByText('· last business day of the month')
    ).toBeInTheDocument();
    expect(
      screen.getByText('· skips Mon 2027-01-11 (Coming of Age Day)')
    ).toBeInTheDocument();
    expect(screen.getByText('· extra business day')).toBeInTheDocument();
  });

  it('says when the listed runs will not start', () => {
    const { rerender } = render(
      <DAGAttributes
        dag={{
          name: 'held',
          schedule: [{ expression: '0 12 * * *', kind: ScheduleKind.cron }],
          upcomingRuns: [
            { time: '2027-01-01T12:00:00Z', scheduleIndex: 0, timezone: 'UTC' },
          ],
          upcomingRunsHold: UpcomingRunsHold.suspended,
        }}
      />
    );
    expect(
      screen.getByText(
        'This DAG is suspended. None of these runs will start until it is resumed.'
      )
    ).toBeInTheDocument();

    rerender(
      <DAGAttributes
        dag={{
          name: 'held',
          schedule: [{ expression: '0 12 * * *', kind: ScheduleKind.cron }],
          upcomingRuns: [
            { time: '2027-01-01T12:00:00Z', scheduleIndex: 0, timezone: 'UTC' },
          ],
          upcomingRunsHold: UpcomingRunsHold.schedulerPaused,
        }}
      />
    );
    expect(
      screen.getByText('Scheduler is paused. No scheduled runs will start.')
    ).toBeInTheDocument();
  });

  it('expands the upcoming runs past the first five', () => {
    const upcomingRuns = Array.from({ length: 7 }, (_, day) => ({
      time: `2027-01-0${day + 1}T12:00:00Z`,
      scheduleIndex: 0,
      timezone: 'UTC',
    }));

    render(
      <DAGAttributes
        dag={{
          name: 'daily',
          schedule: [{ expression: '0 12 * * *', kind: ScheduleKind.cron }],
          upcomingRuns,
        }}
      />
    );

    expect(screen.getAllByRole('listitem')).toHaveLength(5);
    fireEvent.click(screen.getByRole('button', { name: 'Show more' }));
    expect(screen.getAllByRole('listitem')).toHaveLength(7);
    expect(
      screen.getByRole('button', { name: 'Show less' })
    ).toBeInTheDocument();
  });

  it('localizes the relative next run time', () => {
    const nextRun = '2026-04-03T12:00:00Z';
    localStorage.setItem('user_preferences', JSON.stringify({ locale: 'ja' }));

    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <DAGAttributes
            dag={{
              name: 'scheduled-dag',
              schedule: [{ expression: '0 12 * * *', kind: ScheduleKind.cron }],
              nextRun,
            }}
          />
        </I18nProvider>
      </UserPreferencesProvider>
    );

    expect(
      screen.getByText(
        `${dayjs(nextRun).format('YYYY-MM-DD HH:mm:ss')} (${dayjs(nextRun).locale('ja').fromNow()})`
      )
    ).toBeInTheDocument();
  });
});
