// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ScheduleKind, UpcomingRunsHold } from '@/api/v1/schema';
import { UpcomingRuns } from '../UpcomingRuns';

describe('UpcomingRuns', () => {
  it('lists runs in the wall-clock time of their zone', () => {
    render(
      <UpcomingRuns
        schedules={[
          {
            expression: 'CRON_TZ=Asia/Tokyo 0 9 25 * *',
            kind: ScheduleKind.cron,
          },
        ]}
        runs={[
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
        ]}
      />
    );

    expect(screen.getByText('(Asia/Tokyo)')).toBeInTheDocument();
    expect(screen.getByText('Mon 2027-01-25 09:00')).toBeInTheDocument();
    expect(screen.getByText('Thu 2027-02-25 09:00')).toBeInTheDocument();
  });

  it('names the zone and schedule of each run when they differ', () => {
    render(
      <UpcomingRuns
        schedules={[
          { expression: '0 18 * * *', kind: ScheduleKind.cron },
          {
            expression: 'CRON_TZ=Asia/Tokyo 0 9 * * *',
            kind: ScheduleKind.cron,
            profile: 'prod',
          },
        ]}
        runs={[
          {
            time: '2027-01-01T09:00:00+09:00',
            scheduleIndex: 1,
            timezone: 'Asia/Tokyo',
          },
          { time: '2027-01-01T18:00:00Z', scheduleIndex: 0, timezone: 'UTC' },
        ]}
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
      <UpcomingRuns
        schedules={[
          {
            expression: '0 23 * * *',
            kind: ScheduleKind.cron,
            calendar: 'jp-banking',
            businessDays: [3, -1],
          },
        ]}
        runs={[
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
        ]}
      />
    );

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

  it.each([
    [
      UpcomingRunsHold.suspended,
      'This DAG is suspended. None of these runs will start until it is resumed.',
    ],
    [
      UpcomingRunsHold.schedulerPaused,
      'Scheduler is paused. No scheduled runs will start.',
    ],
  ])('says when the runs will not start (%s)', (hold, message) => {
    render(
      <UpcomingRuns
        hold={hold}
        schedules={[{ expression: '0 12 * * *', kind: ScheduleKind.cron }]}
        runs={[
          { time: '2027-01-01T12:00:00Z', scheduleIndex: 0, timezone: 'UTC' },
        ]}
      />
    );
    expect(screen.getByText(message)).toBeInTheDocument();
  });

  it('expands the runs past the first five', () => {
    const runs = Array.from({ length: 7 }, (_, day) => ({
      time: `2027-01-0${day + 1}T12:00:00Z`,
      scheduleIndex: 0,
      timezone: 'UTC',
    }));
    render(
      <UpcomingRuns
        schedules={[{ expression: '0 12 * * *', kind: ScheduleKind.cron }]}
        runs={runs}
      />
    );

    expect(screen.getAllByRole('listitem')).toHaveLength(5);
    fireEvent.click(screen.getByRole('button', { name: 'Show more' }));
    expect(screen.getAllByRole('listitem')).toHaveLength(7);
  });
});
