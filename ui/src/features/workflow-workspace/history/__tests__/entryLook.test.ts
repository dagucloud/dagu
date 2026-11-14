// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { NodeStatus, Status } from '@/api/v1/schema';
import { entryFill } from '../entryLook';
import type { StepRunEntry } from '../stepHistoryModel';

function entry(over: Partial<StepRunEntry>): StepRunEntry {
  return {
    runId: 'r',
    at: null,
    runStatus: Status.Success,
    testRun: false,
    state: 'ran',
    status: NodeStatus.Success,
    startedAt: null,
    finishedAt: null,
    durationMs: 1000,
    running: false,
    reusedFrom: null,
    reuseOutcome: null,
    error: null,
    ...over,
  };
}

const image = (e: StepRunEntry) =>
  entryFill(e, { marked: true, split: 'to top' }).style.backgroundImage;

describe('entryFill', () => {
  // A failure differs from a success by its shape, not only its colour: a
  // reused step that failed in its source run carries the cross of a failure
  // over the look of a reused step.
  it('crosses a reused failure as a failure that ran', () => {
    const cross = image(entry({ status: NodeStatus.Failed }));
    const reused = image(
      entry({ state: 'reused', status: null, reuseOutcome: 'success' })
    );
    expect(cross).toBeTruthy();
    expect(
      image(entry({ state: 'reused', status: null, reuseOutcome: 'failed' }))
    ).toBe(`${cross}, ${reused}`);
  });
});
