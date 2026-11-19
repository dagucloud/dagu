// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { RestartPolicy, ScheduleKind } from '@/api/v1/schema';
import {
  describeScheduleCoincidence,
  getRestartValue,
  isServiceDAG,
} from '../dagSchedule';

describe('service DAGs', () => {
  it('recognizes the DAGs kept alive by a restart policy', () => {
    expect(isServiceDAG({ restartPolicy: RestartPolicy.always })).toBe(true);
    expect(isServiceDAG({ restartPolicy: RestartPolicy.unless_stopped })).toBe(
      true
    );
  });

  it('does not take other DAGs for services', () => {
    expect(isServiceDAG({})).toBe(false);
  });

  it('labels the policy with the wait between two runs', () => {
    const dag = { restartPolicy: RestartPolicy.always, restartWaitSec: 5 };
    expect(getRestartValue(dag)).toBe('always · 5s');
  });

  it('labels the policy without a wait', () => {
    expect(
      getRestartValue({ restartPolicy: RestartPolicy.unless_stopped })
    ).toBe('unless-stopped');
  });
});

describe('describeScheduleCoincidence', () => {
  const ts = (source: string, values?: Record<string, string | number>) =>
    source.replace(/\{(\w+)\}/g, (_, name: string) => String(values?.[name]));
  const coincidences = [
    {
      scheduleIndex: 1,
      precedingScheduleIndex: 0,
      time: '2026-11-23T05:00:00+09:00',
      timezone: 'Asia/Tokyo',
    },
  ];
  const cron = (profile?: string) => ({
    kind: ScheduleKind.cron,
    expression: '0 5 * * *',
    profile,
  });

  it('names the schedule that takes precedence', () => {
    expect(
      describeScheduleCoincidence(1, coincidences, [cron(), cron()], ts, 'en')
    ).toBe(
      'Does not start at Mon 2026-11-23 05:00 Asia/Tokyo: 0 5 * * * is due then too and takes precedence.'
    );
    expect(
      describeScheduleCoincidence(0, coincidences, [cron(), cron()], ts, 'en')
    ).toBeUndefined();
  });

  // A schedule of a profile takes precedence over one without a profile
  // only while that profile is active.
  it('names the profile under which the other schedule takes precedence', () => {
    expect(
      describeScheduleCoincidence(
        1,
        coincidences,
        [cron('prod'), cron()],
        ts,
        'en'
      )
    ).toBe(
      'Does not start at Mon 2026-11-23 05:00 Asia/Tokyo while profile prod is active: 0 5 * * * (prod) is due then too and takes precedence.'
    );
  });
});
