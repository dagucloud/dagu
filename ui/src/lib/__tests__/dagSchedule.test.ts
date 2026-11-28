// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  RestartPolicy,
  ScheduleKind,
  ServiceDAGRestartHold,
  ServiceDAGState,
  Status,
} from '@/api/v1/schema';
import {
  describeScheduleCoincidence,
  getRestartAfterStopSec,
  getRestartValue,
  hasActiveServiceRun,
  isServiceDAG,
  restartsByItself,
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
  const cron = (
    expression: string,
    paramOverrides?: Record<string, string>
  ) => ({ kind: ScheduleKind.cron, expression, paramOverrides });

  it('names the schedule that starts instead', () => {
    const schedules = [cron('0 * * * *', { mode: 'x' }), cron('0 5 * * *')];
    expect(
      describeScheduleCoincidence(1, coincidences, schedules, ts, 'en')
    ).toBe(
      'Does not start at Mon 2026-11-23 05:00 Asia/Tokyo: 0 * * * * starts instead.'
    );
    expect(
      describeScheduleCoincidence(0, coincidences, schedules, ts, 'en')
    ).toBeUndefined();
  });

  it('tells apart two entries with the same timing by their overrides', () => {
    const schedules = [cron('0 5 * * *', { mode: 'x' }), cron('0 5 * * *')];
    expect(
      describeScheduleCoincidence(1, coincidences, schedules, ts, 'en')
    ).toBe(
      'Does not start at Mon 2026-11-23 05:00 Asia/Tokyo: 0 5 * * * · mode: x starts instead.'
    );
  });
});

describe('active run of a service DAG', () => {
  const now = Date.parse('2026-11-08T10:00:00Z');
  const service = { restartPolicy: RestartPolicy.always, restartWaitSec: 5 };

  it.each([Status.Running, Status.Queued, Status.Waiting])(
    'counts status %s as active',
    (status) => {
      expect(hasActiveServiceRun(service, { status }, now)).toBe(true);
    }
  );

  it.each([
    Status.Failed,
    Status.Aborted,
    Status.Success,
    Status.PartialSuccess,
    Status.Rejected,
  ])('does not count status %s as active', (status) => {
    expect(hasActiveServiceRun(service, { status }, now)).toBe(false);
  });

  // The server counts a run that has not started for 5 minutes.
  it('counts a run that has not started for less than 5 minutes', () => {
    const run = (at: string) => ({
      status: Status.NotStarted,
      queuedAt: at,
      startedAt: '-',
    });
    expect(hasActiveServiceRun(service, run('2026-11-08T09:56:00Z'), now)).toBe(
      true
    );
    expect(hasActiveServiceRun(service, run('2026-11-08T09:54:00Z'), now)).toBe(
      false
    );
    expect(
      hasActiveServiceRun(service, { status: Status.NotStarted }, now)
    ).toBe(false);
  });

  it('is false for a plain DAG, without a run, or without a DAG', () => {
    const running = { status: Status.Running };
    expect(hasActiveServiceRun({}, running, now)).toBe(false);
    expect(hasActiveServiceRun(service, undefined, now)).toBe(false);
    expect(hasActiveServiceRun(undefined, running, now)).toBe(false);
  });

  it('gives the restart wait of a stop only for the policy always', () => {
    const running = { state: ServiceDAGState.running };
    expect(getRestartAfterStopSec(service, running)).toBe(5);
    expect(
      getRestartAfterStopSec(
        { restartPolicy: RestartPolicy.unless_stopped, restartWaitSec: 5 },
        running
      )
    ).toBeNull();
    expect(getRestartAfterStopSec({}, running)).toBeNull();
  });

  // No restart is known to follow a stop before the state of the DAG is read.
  it('gives no restart wait without the state of the service', () => {
    expect(getRestartAfterStopSec(service, undefined)).toBeNull();
  });

  // Start stays offered while the scheduler starts no run.
  it('tells whether the scheduler starts the next run by itself', () => {
    const failing = { state: ServiceDAGState.failing };
    expect(restartsByItself({ state: ServiceDAGState.restarting })).toBe(true);
    expect(restartsByItself(failing)).toBe(true);
    expect(restartsByItself({ state: ServiceDAGState.stopped })).toBe(false);
    expect(restartsByItself(undefined)).toBe(false);
    expect(
      restartsByItself({
        ...failing,
        restartHold: ServiceDAGRestartHold.noScheduler,
      })
    ).toBe(false);
    expect(
      restartsByItself({ ...failing, readError: 'status file is corrupt' })
    ).toBe(false);
  });
});
