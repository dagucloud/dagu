// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  BackfillOnFailure,
  BackfillRerun,
  BackfillSlotState,
  BackfillStatus,
} from '@/api/v1/schema';
import {
  type Backfill,
  type BackfillSlot,
  groupSlots,
  summarizeBackfill,
} from '../backfillState';

function backfill(
  status: BackfillStatus,
  counts: Partial<Record<BackfillSlotState, number>>
): Backfill {
  return {
    id: 'bf123456',
    dag: 'etl',
    status,
    submittedAt: '2026-11-01T00:00:00Z',
    rerun: BackfillRerun.none,
    maxConcurrency: 1,
    onFailure: BackfillOnFailure.stop,
    slotCount: Object.values(counts).reduce((sum, n) => sum + (n ?? 0), 0),
    counts,
  };
}

describe('summarizeBackfill', () => {
  it('gives one sentence for each state of a backfill', () => {
    const cases: Array<[Backfill, string, Record<string, number>]> = [
      [
        backfill(BackfillStatus.running, {
          succeeded: 12,
          queued: 1,
          pending: 15,
        }),
        'Running: {done} of {total} slots done',
        { done: 12, total: 28 },
      ],
      [
        backfill(BackfillStatus.running, {
          succeeded: 11,
          failed: 1,
          running: 1,
          pending: 15,
        }),
        'Running: {done} of {total} slots done, {failed} failed',
        { done: 12, total: 28, failed: 1 },
      ],
      [
        backfill(BackfillStatus.running, { succeeded: 28 }),
        'Finishing: all {total} slots are done',
        { total: 28 },
      ],
      [
        backfill(BackfillStatus.waiting_for_scheduler, {
          succeeded: 2,
          pending: 26,
        }),
        'Waiting for a scheduler: {done} of {total} slots done',
        { done: 2, total: 28 },
      ],
      [
        backfill(BackfillStatus.succeeded, { succeeded: 28 }),
        'Succeeded: all {total} slots',
        { total: 28 },
      ],
      [
        backfill(BackfillStatus.succeeded, { succeeded: 25, skipped: 3 }),
        'Succeeded: {ran} slots ran, {skipped} skipped',
        { ran: 25, skipped: 3 },
      ],
      [
        backfill(BackfillStatus.failed, {
          succeeded: 8,
          failed: 1,
          'not run': 14,
        }),
        'Stopped: {failed} failed, {notRun} not run',
        { failed: 1, notRun: 14 },
      ],
      [
        backfill(BackfillStatus.failed, { succeeded: 26, failed: 2 }),
        'Failed: {failed} of {total} slots failed',
        { failed: 2, total: 28 },
      ],
      [
        backfill(BackfillStatus.cancelled, { succeeded: 3, cancelled: 25 }),
        'Cancelled: {cancelled} slots were not run',
        { cancelled: 25 },
      ],
    ];
    for (const [input, message, values] of cases) {
      const summary = summarizeBackfill(input);
      expect(summary.message).toBe(message);
      expect(summary.values).toEqual(values);
    }
  });
});

describe('groupSlots', () => {
  const slot = (at: string): BackfillSlot => ({
    slot: at,
    state: BackfillSlotState.succeeded,
  });
  const keyOf = (s: BackfillSlot, unit: 'day' | 'month') =>
    unit === 'day' ? s.slot.slice(0, 10) : s.slot.slice(0, 7);

  it('puts the slots of a day in a row when a day has several', () => {
    const groups = groupSlots(
      [
        slot('2026-10-01T00:00:00Z'),
        slot('2026-10-01T01:00:00Z'),
        slot('2026-10-02T00:00:00Z'),
      ],
      keyOf
    );
    expect(groups.map((g) => [g.key, g.slots.length])).toEqual([
      ['2026-10-01', 2],
      ['2026-10-02', 1],
    ]);
  });

  it('puts the slots of a month in a row for a daily or slower cadence', () => {
    const groups = groupSlots(
      [
        slot('2026-09-30T02:00:00Z'),
        slot('2026-10-01T02:00:00Z'),
        slot('2026-10-02T02:00:00Z'),
      ],
      keyOf
    );
    expect(groups.map((g) => [g.key, g.slots.length])).toEqual([
      ['2026-09', 1],
      ['2026-10', 2],
    ]);
  });
});
