// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { parseSchedulerLogLine } from '../scheduler-log';

describe('parseSchedulerLogLine', () => {
  it('extracts the readable fields from text scheduler logs', () => {
    expect(
      parseSchedulerLogLine(
        'time=2026-04-23T23:58:52.652+09:00 level=INFO msg="Step started" dag=daily-report run-id=run-1 attempt-id=attempt-1 worker-id=worker-1 trace-id=trace-1 span-id=span-1 trace-flags=01 step=collect_metrics'
      )
    ).toEqual({
      timestamp: '2026-04-23T23:58:52.652+09:00',
      level: 'INFO',
      message: 'Step started',
      step: 'collect_metrics',
      details: undefined,
      structured: true,
    });
  });

  it('keeps useful metadata from JSON scheduler logs', () => {
    expect(
      parseSchedulerLogLine(
        '{"time":"2026-04-23T23:58:52Z","level":"ERROR","msg":"Step failed","dag":"daily-report","run-id":"run-1","step":"publish","err":"exit status 1"}'
      )
    ).toEqual({
      timestamp: '2026-04-23T23:58:52Z',
      level: 'ERROR',
      message: 'Step failed',
      step: 'publish',
      details: 'err=exit status 1',
      structured: true,
    });
  });

  it('shows unrecognized lines unchanged', () => {
    expect(parseSchedulerLogLine('plain output')).toEqual({
      message: 'plain output',
      structured: false,
    });
  });
});
