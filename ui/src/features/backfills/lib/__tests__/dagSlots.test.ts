// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { DAGSlotStatus } from '@/api/v1/schema';
import { type DAGSlot, selectSlots, slotsInStates } from '../dagSlots';

const SLOTS: DAGSlot[] = [
  { slot: 's1', status: DAGSlotStatus.succeeded },
  { slot: 's2', status: DAGSlotStatus.missing },
  { slot: 's3', status: DAGSlotStatus.failed },
  { slot: 's4', status: DAGSlotStatus.missing },
];

describe('selectSlots', () => {
  it('adds and removes a slot with a click', () => {
    const once = selectSlots(SLOTS, new Set(), 1, null, false);
    expect([...once]).toEqual(['s2']);
    expect([...selectSlots(SLOTS, once, 1, 1, false)]).toEqual([]);
  });

  it('adds every slot from the last one clicked with Shift', () => {
    const next = selectSlots(SLOTS, new Set(['s1']), 3, 1, true);
    expect([...next].sort()).toEqual(['s1', 's2', 's3', 's4']);
  });

  it('adds a run of slots backwards too', () => {
    expect([...selectSlots(SLOTS, new Set(), 0, 2, true)].sort()).toEqual([
      's1',
      's2',
      's3',
    ]);
  });
});

describe('slotsInStates', () => {
  it('picks the slots in the states given', () => {
    expect(
      [
        ...slotsInStates(SLOTS, [DAGSlotStatus.missing, DAGSlotStatus.failed]),
      ].sort()
    ).toEqual(['s2', 's3', 's4']);
  });
});
