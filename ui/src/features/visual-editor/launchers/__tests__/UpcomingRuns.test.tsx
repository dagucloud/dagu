// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ScheduleKind } from '@/api/v1/schema';
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
