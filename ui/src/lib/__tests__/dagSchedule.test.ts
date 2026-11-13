// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { RestartPolicy, Status } from '@/api/v1/schema';
import {
  getRestartAfterStopSec,
  getRestartValue,
  hasActiveServiceRun,
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
    expect(getRestartAfterStopSec(service)).toBe(5);
    expect(
      getRestartAfterStopSec({
        restartPolicy: RestartPolicy.unless_stopped,
        restartWaitSec: 5,
      })
    ).toBeNull();
    expect(getRestartAfterStopSec({})).toBeNull();
  });
});
