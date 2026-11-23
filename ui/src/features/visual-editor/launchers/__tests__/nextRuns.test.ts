// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { nextRuns } from '../nextRuns';

const FROM = new Date('2026-10-30T00:00:00Z');

describe('nextRuns', () => {
  // The scheduler fires in its own time zone, whatever the zone of the
  // browser.
  it('reads the expression in the time zone of the scheduler', () => {
    expect(
      nextRuns('0 9 * * *', 2, FROM, 'UTC')?.map((d) => d.toISOString())
    ).toEqual(['2026-10-30T09:00:00.000Z', '2026-10-31T09:00:00.000Z']);
    expect(
      nextRuns('0 9 * * *', 1, FROM, 'Asia/Tokyo')?.map((d) => d.toISOString())
    ).toEqual(['2026-10-31T00:00:00.000Z']);
  });

  it('reads the offset form that the server gives without a configured zone', () => {
    expect(
      nextRuns('0 9 * * *', 1, FROM, 'UTC-5')?.map((d) => d.toISOString())
    ).toEqual(['2026-10-30T14:00:00.000Z']);
  });

  // A leading zone field wins over the zone of the scheduler. `Local` is the
  // zone of the scheduler, here one that no browser of a test is likely in.
  it.each([
    ['TZ=Asia/Tokyo', 'UTC', '2026-10-31T00:00:00.000Z'],
    ['CRON_TZ=Asia/Tokyo', 'UTC', '2026-10-31T00:00:00.000Z'],
    ['TZ=Japan', 'UTC', '2026-10-31T00:00:00.000Z'],
    ['TZ=Local', 'Asia/Kathmandu', '2026-10-30T03:15:00.000Z'],
  ])('reads %s with the scheduler in %s', (prefix, tz, first) => {
    expect(
      nextRuns(`${prefix} 0 9 * * *`, 1, FROM, tz)?.map((d) => d.toISOString())
    ).toEqual([first]);
  });

  // The server refuses a zone that its time zone database lacks, even one
  // that the browser takes: a legacy ID such as JST, or a UTC offset.
  it.each(['TZ=Nowhere/City', 'TZ=JST', 'CRON_TZ=PST', 'TZ=+09:00'])(
    'gives null for %s',
    (prefix) => {
      expect(nextRuns(`${prefix} 0 9 * * *`, 1, FROM, 'UTC')).toBeNull();
    }
  );

  it('gives null for an expression that the parser does not read', () => {
    expect(nextRuns('not cron', 1, FROM, 'UTC')).toBeNull();
  });
});
