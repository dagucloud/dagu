// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  MODEL_CARD_WIDTH,
  SUB_BAND,
  SUB_LEAF_HEIGHT,
  SUB_ICON_SIZE,
  SUB_LEAF_WIDTH,
  SUB_SIDE_GAP,
  modelCardHeight,
  planSubRow,
  slotCardRows,
  subItemSize,
  type SubBox,
  type SubItem,
  type SubRowShape,
} from '../subRow';

// Spec 093, "The UI contract": the Model node of an edit view is a card of
// controls. It keeps the places that the layout of the sub-nodes decided: top to
// bottom the left column holds the Model and then the Prompt; left to right the
// row above the step holds them from left to right.

const BASE = { width: 336, height: 104 };

const overlap = (a: SubBox, b: SubBox) =>
  a.x < b.x + b.width &&
  b.x < a.x + a.width &&
  a.y < b.y + b.height &&
  b.y < a.y + a.height;

function withCard(
  rows: number,
  cli: boolean,
  prompt: boolean,
  effort = false
): SubRowShape {
  const items: SubItem[] = [
    { key: 'm', slot: 'model', modelCard: { rows, cli, effort } },
    { key: 's', slot: 'skills', empty: true },
    { key: 't', slot: 'tools', rows: slotCardRows(2, true) },
  ];
  if (prompt) items.splice(1, 0, { key: 'p', slot: 'prompt', prompt: true });
  return { items };
}

function expectClean(plan: ReturnType<typeof planSubRow>) {
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
}

describe('the card of controls of the Model node', () => {
  it('is MODEL_CARD_WIDTH wide and as tall as its models, and a round node in a compact canvas', () => {
    expect(subItemSize({ modelCard: { rows: 1, cli: false } })).toEqual({
      width: MODEL_CARD_WIDTH,
      height: modelCardHeight(1, false),
    });
    // An agent step has one more line, for its CLI.
    expect(modelCardHeight(1, true)).toBeGreaterThan(modelCardHeight(1, false));
    // A single model has one more control, for its effort: 28 px and a gap.
    expect(modelCardHeight(1, false, true) - modelCardHeight(1, false)).toBe(
      32
    );
    expect(
      subItemSize({ modelCard: { rows: 1, cli: false, effort: true } })
    ).toEqual({
      width: MODEL_CARD_WIDTH,
      height: modelCardHeight(1, false, true),
    });
    // A list has one block for each model, with a heading.
    expect(modelCardHeight(3, false)).toBeGreaterThan(
      3 * (modelCardHeight(1, false) - SUB_ICON_SIZE - 6 - 2)
    );
    expect(subItemSize({ modelCard: { rows: 2, cli: false } }, true)).toEqual({
      width: SUB_LEAF_WIDTH,
      height: SUB_LEAF_HEIGHT,
    });
  });

  it('keeps every sub-node in the box, off the step and off each other, in both directions', () => {
    for (const direction of ['TB', 'LR'] as const) {
      for (const [rows, cli, prompt] of [
        [1, true, true],
        [1, false, false],
        [3, false, false],
      ] as const) {
        for (const compact of [false, true]) {
          expectClean(
            planSubRow(
              withCard(rows, cli, prompt, rows === 1),
              BASE,
              direction,
              compact
            )
          );
        }
      }
    }
  });

  it('keeps the Model above the Prompt left of the step, top to bottom', () => {
    const plan = planSubRow(withCard(1, true, true), BASE, 'TB');
    const { m, p } = plan.items;
    expect(m!.width).toBe(MODEL_CARD_WIDTH);
    expect(m!.x + m!.width + SUB_SIDE_GAP).toBeLessThanOrEqual(plan.base.x);
    expect(m!.y + m!.height).toBeLessThan(p!.y);
    expect(plan.slots.model.side).toBe('left');
  });

  it('keeps the Model left of the Prompt, in the row above the step, left to right', () => {
    const plan = planSubRow(withCard(1, true, true), BASE, 'LR');
    const { m, p } = plan.items;
    expect(m!.y + m!.height + SUB_BAND).toBeLessThanOrEqual(plan.base.y);
    expect(p!.x).toBeGreaterThan(m!.x + m!.width);
    expect(plan.slots.model.side).toBe('top');
  });
});
