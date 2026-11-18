// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
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

  // Each run of a DAG with dates tells the date it loads.
  it('tells the date each run loads', () => {
    render(
      <UpcomingRuns
        schedules={[{ expression: '0 2 * * *', kind: ScheduleKind.cron }]}
        runs={[
          {
            time: '2027-01-25T02:00:00Z',
            scheduleIndex: 0,
            timezone: 'UTC',
            dates: [{ name: 'target_day', value: '2027-01-24' }],
          },
        ]}
      />
    );

    expect(screen.getByRole('listitem')).toHaveTextContent('loads 2027-01-24');
  });

  it('shows how many parameters the matching schedule overrides', () => {
    render(
      <UpcomingRuns
        schedules={[
          { expression: '0 5 * * *', paramOverrideCount: 1 },
          { expression: '0 6 * * *', paramOverrideCount: 2 },
        ]}
        runs={[
          { time: '2027-01-01T05:00:00Z', scheduleIndex: 0, timezone: 'UTC' },
          { time: '2027-01-01T06:00:00Z', scheduleIndex: 1, timezone: 'UTC' },
        ]}
      />
    );

    expect(
      screen.getByText('1 parameter override', { exact: false })
    ).toBeInTheDocument();
    expect(
      screen.getByText('2 parameter overrides', { exact: false })
    ).toBeInTheDocument();
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
      <MemoryRouter>
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
      </MemoryRouter>
    );

    expect(screen.getByRole('link', { name: 'jp-banking' })).toHaveAttribute(
      'href',
      '/calendars/jp-banking?remoteNode=local'
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

  // Two schedules that override the same parameter must look different.
  it('shows the overrides of each run beside its time', () => {
    render(
      <UpcomingRuns
        schedules={[
          {
            expression: '0 5 * * *',
            kind: ScheduleKind.cron,
            paramOverrideCount: 1,
          },
          {
            expression: '0 6 * * *',
            kind: ScheduleKind.cron,
            paramOverrideCount: 1,
          },
        ]}
        overrides={[
          { expression: '0 5 * * *', params: { mode: 'y' } },
          { expression: '0 6 * * *', params: { mode: 'z' } },
        ]}
        runs={[
          { time: '2027-01-25T05:00:00Z', scheduleIndex: 0, timezone: 'UTC' },
          { time: '2027-01-25T06:00:00Z', scheduleIndex: 1, timezone: 'UTC' },
        ]}
      />
    );
    expect(screen.getByText('· mode: y')).toBeInTheDocument();
    expect(screen.getByText('· mode: z')).toBeInTheDocument();
    expect(screen.queryByText(/parameter override/)).toBeNull();
  });

  // A profile entry that shares an expression must not take another entry's overrides.
  it('skips entries the panel does not edit when it matches overrides', () => {
    render(
      <UpcomingRuns
        schedules={[
          { expression: '0 5 * * *', kind: ScheduleKind.cron, profile: 'x' },
          { expression: '0 5 * * *', kind: ScheduleKind.cron },
        ]}
        overrides={[{ expression: '0 5 * * *', params: { mode: 'b' } }]}
        runs={[
          { time: '2027-01-25T05:00:00Z', scheduleIndex: 0, timezone: 'UTC' },
          { time: '2027-01-26T05:00:00Z', scheduleIndex: 1, timezone: 'UTC' },
        ]}
      />
    );
    const items = screen.getAllByRole('listitem');
    expect(items[0]).not.toHaveTextContent('mode: b');
    expect(items[1]).toHaveTextContent('mode: b');
  });

  // A `kind: cron` entry is editable, so it takes the first override.
  it('treats a kind cron entry as editable', () => {
    render(
      <UpcomingRuns
        schedules={[
          { expression: '0 5 * * *', kind: ScheduleKind.cron },
          { expression: '0 5 * * *', kind: ScheduleKind.cron },
        ]}
        overrides={[
          { expression: '0 5 * * *', params: {} },
          { expression: '0 5 * * *', params: { mode: 'b' } },
        ]}
        runs={[
          { time: '2027-01-25T05:00:00Z', scheduleIndex: 0, timezone: 'UTC' },
          { time: '2027-01-26T05:00:00Z', scheduleIndex: 1, timezone: 'UTC' },
        ]}
      />
    );
    const items = screen.getAllByRole('listitem');
    expect(items[0]).not.toHaveTextContent('mode: b');
    expect(items[1]).toHaveTextContent('mode: b');
  });

  it('tells the duplicates of one expression apart by their order', () => {
    render(
      <UpcomingRuns
        schedules={[
          { expression: '0 5 * * *', kind: ScheduleKind.cron },
          { expression: '0 5 * * *', kind: ScheduleKind.cron },
        ]}
        overrides={[
          { expression: '0 5 * * *', params: { mode: 'a' } },
          { expression: '0 5 * * *', params: { mode: 'b' } },
        ]}
        runs={[
          { time: '2027-01-25T05:00:00Z', scheduleIndex: 1, timezone: 'UTC' },
        ]}
      />
    );
    expect(screen.getByText('· mode: b')).toBeInTheDocument();
  });

  it('falls back to the count without a matching entry', () => {
    render(
      <UpcomingRuns
        schedules={[
          {
            expression: '0 5 * * *',
            kind: ScheduleKind.cron,
            paramOverrideCount: 2,
          },
        ]}
        runs={[
          { time: '2027-01-25T05:00:00Z', scheduleIndex: 0, timezone: 'UTC' },
        ]}
      />
    );
    expect(screen.getByText('· 2 parameter overrides')).toBeInTheDocument();
  });
});
