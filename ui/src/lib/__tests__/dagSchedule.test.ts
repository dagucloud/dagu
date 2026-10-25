// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { LoopOnFailure, RestartPolicy } from '@/api/v1/schema';
import { getLoopValue, getRestartValue, isAlwaysOnDAG } from '../dagSchedule';

describe('always-on DAGs', () => {
  it('recognizes the DAGs kept alive by a restart policy', () => {
    expect(isAlwaysOnDAG({ restartPolicy: RestartPolicy.always })).toBe(true);
    expect(isAlwaysOnDAG({ restartPolicy: RestartPolicy.unless_stopped })).toBe(
      true
    );
  });

  it('does not take other DAGs for always-on DAGs', () => {
    expect(isAlwaysOnDAG({})).toBe(false);
  });

  it('labels the policy with the wait between two runs', () => {
    const dag = { restartPolicy: RestartPolicy.always, restartWaitSec: 5 };
    expect(getRestartValue(dag)).toBe('always · 5s');
  });

  // The DAG list is built from an index without the loop, so only the Spec tab,
  // which has the whole DAG, shows it.
  it('gives the wait between two cycles of a DAG that loops', () => {
    const dag = {
      restartPolicy: RestartPolicy.always,
      restartWaitSec: 5,
      loopWaitSec: 7,
    };
    expect(getLoopValue(dag)).toBe('loop 7s');
  });

  it('says when a failed cycle does not end the run', () => {
    const dag = {
      restartPolicy: RestartPolicy.always,
      restartWaitSec: 5,
      loopWaitSec: 7,
      loopOnFailure: LoopOnFailure.continue,
    };
    expect(getLoopValue(dag)).toBe('loop 7s · continue');
    expect(getLoopValue({ ...dag, loopOnFailure: LoopOnFailure.restart })).toBe(
      'loop 7s'
    );
  });

  it('gives nothing for a DAG that does not loop', () => {
    expect(getLoopValue({ restartPolicy: RestartPolicy.always })).toBe('');
  });

  it('labels the policy without a wait', () => {
    expect(
      getRestartValue({ restartPolicy: RestartPolicy.unless_stopped })
    ).toBe('unless-stopped');
  });
});
