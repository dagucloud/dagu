// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { RunHistoryEventKind, StepEventKind } from '@/api/v1/schema';
import {
  eventRunId,
  runHistorySource,
  stepSource,
  unitOf,
} from '../sampleSource';

describe('sample source', () => {
  it('names a step by its run and a history by its file', () => {
    expect(stepSource('demo', 'run-1', 'beat')).toEqual({
      kind: 'step',
      name: 'demo',
      runId: 'run-1',
      step: 'beat',
    });
    expect(runHistorySource('svc.yaml')).toEqual({
      kind: 'run-history',
      fileName: 'svc.yaml',
    });
  });

  it('counts executions for a step and runs for a history', () => {
    expect(unitOf(stepSource('d', 'r', 's'))).toBe('executions');
    expect(unitOf(runHistorySource('svc.yaml'))).toBe('runs');
  });

  it('reads the run of an event of a history only', () => {
    expect(
      eventRunId({
        time: '2026-01-01T00:00:00Z',
        kind: RunHistoryEventKind.failed,
        runId: 'r1',
      })
    ).toBe('r1');
    expect(
      eventRunId({ time: '2026-01-01T00:00:00Z', kind: StepEventKind.stopped })
    ).toBeUndefined();
  });
});
