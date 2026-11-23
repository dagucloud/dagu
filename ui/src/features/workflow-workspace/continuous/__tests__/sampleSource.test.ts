// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { runHistorySource, stepSource, unitOf } from '../sampleSource';

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
});
