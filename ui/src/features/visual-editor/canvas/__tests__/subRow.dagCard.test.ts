// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  DAG_CARD_MAX_ENTRIES,
  DAG_CARD_WIDTH,
  SLOT_CARD_WIDTH,
  SLOT_ORDER,
  DAG_ENTRY_HEIGHT,
  dagCardHeight,
  planSubRow,
  slotCardHeight,
  slotColumn,
  slotSide,
  subItemSize,
  subNodeSize,
  type SubBox,
  type SubItem,
} from '../subRow';

// The DAGs card is the fifth slot of a chat step. It sits with the Skills and
// the Tools: beside the step on the right, or under it, after the Tools. It is
// as wide as the miniature of a DAG needs, and as tall as its entries.

const BASE = { width: 336, height: 104 };

const overlap = (a: SubBox, b: SubBox) =>
  a.x < b.x + b.width &&
  b.x < a.x + a.width &&
  a.y < b.y + b.height &&
  b.y < a.y + a.height;

const items = (entries: number, rows = 1): SubItem[] => [
  { key: 'm', slot: 'model', modelCard: { rows: 1, cli: false } },
  { key: 's', slot: 'skills', empty: true },
  { key: 't', slot: 'tools', rows: 3 },
  { key: 'd', slot: 'dags', rows, dagCard: { entries, rows } },
];

describe('the size of the DAGs card', () => {
  it('is as wide as a Prompt-like card and as tall as its entries, their rows and its header', () => {
    expect(subItemSize({ dagCard: { entries: 2, rows: 1 } })).toEqual({
      width: DAG_CARD_WIDTH,
      height: dagCardHeight(2, 1),
    });
    expect(DAG_CARD_WIDTH).toBeGreaterThan(SLOT_CARD_WIDTH);
    // The header, one row of the Tools size and the pad are those of a slot card.
    expect(dagCardHeight(0, 1)).toBe(slotCardHeight(1));
    expect(dagCardHeight(2, 1) - dagCardHeight(1, 1)).toBe(DAG_ENTRY_HEIGHT);
  });

  it('reads the card of a canvas node, counting at most the entries that it lists', () => {
    const entries = Array.from({ length: 12 }, () => ({}));
    expect(
      subNodeSize({ slotCard: { slot: 'dags', rows: 2, entries } }, false)
    ).toEqual({
      width: DAG_CARD_WIDTH,
      height: dagCardHeight(DAG_CARD_MAX_ENTRIES, 2),
    });
    // The Tools card keeps its own size.
    expect(
      subNodeSize({ slotCard: { slot: 'tools', rows: 4, entries } }, false)
    ).toEqual({ width: SLOT_CARD_WIDTH, height: slotCardHeight(4) });
  });
});

describe('the place of the DAGs slot', () => {
  it('is the last slot, in the right column or under the step', () => {
    expect(SLOT_ORDER[SLOT_ORDER.length - 1]).toBe('dags');
    expect(slotColumn('dags')).toBe('right');
    expect(slotSide('dags', 'TB')).toBe('right');
    expect(slotSide('dags', 'LR')).toBe('bottom');
  });

  it.each([
    ['TB', false],
    ['TB', true],
    ['LR', false],
    ['LR', true],
  ] as const)(
    'keeps every sub-node apart and inside the box (%s, compact %s)',
    (direction, compact) => {
      const plan = planSubRow({ items: items(3, 2) }, BASE, direction, compact);
      const boxes = Object.values(plan.items);
      for (const box of boxes) {
        expect(box.x).toBeGreaterThanOrEqual(0);
        expect(box.y).toBeGreaterThanOrEqual(0);
        expect(box.x + box.width).toBeLessThanOrEqual(plan.width);
        expect(box.y + box.height).toBeLessThanOrEqual(plan.height);
        expect(overlap(box, plan.base)).toBe(false);
      }
      boxes.forEach((a, i) =>
        boxes.slice(i + 1).forEach((b) => expect(overlap(a, b)).toBe(false))
      );
      expect(plan.present).toEqual(['model', 'skills', 'tools', 'dags']);
    }
  );

  it('puts the DAGs card after the Tools card, top to bottom and left to right', () => {
    const tb = planSubRow({ items: items(2) }, BASE, 'TB');
    expect(tb.items.t!.y + tb.items.t!.height).toBeLessThanOrEqual(
      tb.items.d!.y
    );
    expect(tb.items.d!.x).toBe(tb.items.t!.x);
    const lr = planSubRow({ items: items(2) }, BASE, 'LR');
    expect(lr.items.t!.x + lr.items.t!.width).toBeLessThanOrEqual(
      lr.items.d!.x
    );
    expect(lr.items.d!.y).toBe(lr.items.t!.y);
  });

  it('gives each slot its own connector, in the order of the slots', () => {
    const tb = planSubRow({ items: items(2) }, BASE, 'TB');
    const ys = ['skills', 'tools', 'dags'].map(
      (slot) => tb.slots[slot as 'skills'].y
    );
    expect(ys).toEqual([...ys].sort((a, b) => a - b));
    expect(new Set(ys).size).toBe(3);
    const lr = planSubRow({ items: items(2) }, BASE, 'LR');
    const xs = ['skills', 'tools', 'dags'].map(
      (slot) => lr.slots[slot as 'skills'].x
    );
    expect(xs).toEqual([...xs].sort((a, b) => a - b));
    expect(new Set(xs).size).toBe(3);
  });

  it('makes the box taller with more entries, up to the limit of the card', () => {
    const one = planSubRow({ items: items(1) }, BASE, 'TB');
    const three = planSubRow({ items: items(3) }, BASE, 'TB');
    expect(three.height).toBeGreaterThan(one.height);
  });
});
