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

  // A leading TZ= or CRON_TZ= field wins over the zone of the scheduler,
  // and TZ=Local names the zone of the scheduler, as the server reads them.
  it('reads the zone that the expression names', () => {
    const at = (expression: string) =>
      nextRuns(expression, 1, FROM, 'UTC')?.map((d) => d.toISOString());
    expect(at('TZ=Asia/Tokyo 0 9 * * *')).toEqual(['2026-10-31T00:00:00.000Z']);
    expect(at('CRON_TZ=Asia/Tokyo 0 9 * * *')).toEqual([
      '2026-10-31T00:00:00.000Z',
    ]);
    expect(at('TZ=Local 0 9 * * *')).toEqual(['2026-10-30T09:00:00.000Z']);
  });

  // The server refuses a zone that is not a name of the time zone database.
  it('gives null for a zone that the server does not know', () => {
    expect(nextRuns('TZ=Mars/Olympus 0 9 * * *', 1, FROM, 'UTC')).toBeNull();
    expect(nextRuns('TZ=UTC+9 0 9 * * *', 1, FROM, 'UTC')).toBeNull();
  });

  it('gives null for an expression that the parser does not read', () => {
    expect(nextRuns('not cron', 1, FROM, 'UTC')).toBeNull();
  });
});
