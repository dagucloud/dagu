// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { RestartPolicy, Status } from '@/api/v1/schema';
import {
  getRestartLabel,
  getRestartValue,
  isServiceDAG,
  isStoppedService,
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
    expect(getRestartLabel(dag)).toBe('restart: always · 5s');
  });

  it('labels the policy without a wait', () => {
    expect(
      getRestartLabel({ restartPolicy: RestartPolicy.unless_stopped })
    ).toBe('restart: unless-stopped');
  });

  // An operator's stop is what unless-stopped honors, so the DAG is not live.
  it('tells an unless-stopped DAG that an operator stopped', () => {
    const dag = { restartPolicy: RestartPolicy.unless_stopped };
    expect(isStoppedService(dag, false, Status.Aborted)).toBe(true);
    expect(isStoppedService(dag, false, Status.Running)).toBe(false);
    expect(isStoppedService(dag, true, Status.Aborted)).toBe(false);
    expect(
      isStoppedService(
        { restartPolicy: RestartPolicy.always },
        false,
        Status.Aborted
      )
    ).toBe(false);
  });
});
