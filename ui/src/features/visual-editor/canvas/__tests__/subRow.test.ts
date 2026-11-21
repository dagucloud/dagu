// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  PROMPT_CARD_WIDTH,
  SLOT_CARD_MAX_ROWS,
  SLOT_CARD_WIDTH,
  SUB_ADD_SIZE,
  SUB_BAND,
  SUB_LEAF_HEIGHT,
  SUB_LEAF_WIDTH,
  SUB_SIDE_GAP,
  SUB_TILE_SIZE,
  planSubRow,
  promptCardHeight,
  slotCardHeight,
  slotCardRows,
  slotCardSize,
  slotColumn,
  slotSide,
  subItemSize,
  subNodeSize,
  type SubBox,
  type SubItem,
  type SubRowShape,
} from '../subRow';

const BASE = { width: 336, height: 104 };

/**
 * The slots of a chat step: a count of 0 gives the small round "+" of an
 * empty slot. The Prompt is for an agent step: a card, a round "+" when
 * `empty`, and no node at all when the argument is omitted.
 */
function shape(
  tools: number,
  skills: number,
  prompt?: 'card' | 'empty'
): SubRowShape {
  const slot = (
    key: string,
    slotName: 'tools' | 'skills',
    count: number
  ): SubItem =>
    count === 0
      ? { key, slot: slotName, empty: true }
      : { key, slot: slotName, rows: slotCardRows(count, true) };
  const promptItem: SubItem[] =
    prompt === undefined
      ? []
      : [
          prompt === 'empty'
            ? { key: 'p', slot: 'prompt', empty: true }
            : { key: 'p', slot: 'prompt', prompt: true },
        ];
  // The order of the items is not the order of the layout: the plan sorts them.
  return {
    items: [
      slot('t', 'tools', tools),
      ...promptItem,
      slot('s', 'skills', skills),
      { key: 'm', slot: 'model' },
    ],
  };
}

type Boxes = Record<string, SubBox>;

const overlap = (a: SubBox, b: SubBox) =>
  a.x < b.x + b.width &&
  b.x < a.x + a.width &&
  a.y < b.y + b.height &&
  b.y < a.y + a.height;

describe('the height of the Prompt card', () => {
  it('follows its fit, and a card without a fit is the full card', () => {
    const full = promptCardHeight();
    expect(subItemSize({ prompt: true }).height).toBe(full);
    const short = subItemSize({ prompt: { system: 1, prompt: 2 } }).height;
    expect(short).toBeLessThan(full);
    expect(
      subNodeSize({ promptCard: { system: '', prompt: 'Fix it.' } }, false)
        .height
    ).toBe(short);
    expect(
      subNodeSize(
        { promptCard: { system: 'a\n\nb', prompt: 'x '.repeat(200) } },
        false
      ).height
    ).toBe(full);
  });

  // A compact canvas draws a tile for each card, whatever its rows.
  it('is a tile of one size in a compact canvas, for each card', () => {
    const tile = { width: SUB_TILE_SIZE, height: SUB_TILE_SIZE };
    expect(subItemSize({ prompt: true }, true)).toEqual(tile);
    expect(subItemSize({ rows: 12, cols: 3 }, true)).toEqual(tile);
    expect(subItemSize({ dagCard: { entries: 3, rows: 2 } }, true)).toEqual(
      tile
    );
    expect(
      subNodeSize({ promptCard: { system: '', prompt: 'Fix it.' } }, true)
    ).toEqual(tile);
    // The round Model node and the small "+" keep their size.
    expect(subItemSize({}, true)).toEqual({
      width: SUB_LEAF_WIDTH,
      height: SUB_LEAF_HEIGHT,
    });
    expect(subItemSize({ empty: true }, true).width).toBe(SUB_ADD_SIZE);
  });
});

describe('slotCardRows', () => {
  it('counts the entries, the "+N more" row and the add row', () => {
    expect(slotCardRows(0, true)).toBe(1);
    expect(slotCardRows(0, false)).toBe(1);
    expect(slotCardRows(3, true)).toBe(4);
    expect(slotCardRows(3, false)).toBe(3);
    expect(slotCardRows(SLOT_CARD_MAX_ROWS, true)).toBe(SLOT_CARD_MAX_ROWS + 1);
    expect(slotCardRows(SLOT_CARD_MAX_ROWS + 5, true)).toBe(
      SLOT_CARD_MAX_ROWS + 2
    );
    expect(slotCardRows(SLOT_CARD_MAX_ROWS + 5, false)).toBe(
      SLOT_CARD_MAX_ROWS + 1
    );
  });

  it('counts the group headings too', () => {
    expect(slotCardRows(3, true, 2)).toBe(6);
    expect(slotCardRows(3, false, 3)).toBe(6);
    expect(slotCardRows(0, true, 0)).toBe(1);
  });
});

describe('slotCardSize', () => {
  it('has the width of its columns and the height of its rows', () => {
    expect(slotCardSize(1, 4)).toEqual({
      width: SLOT_CARD_WIDTH,
      height: slotCardHeight(4),
    });
    expect(slotCardSize(3, 12)).toEqual({
      width: 3 * SLOT_CARD_WIDTH,
      height: slotCardHeight(12),
    });
    expect(subItemSize({ rows: 12, cols: 2 })).toEqual(slotCardSize(2, 12));
    expect(subNodeSize({ slotCard: { rows: 12, cols: 2 } }, false).width).toBe(
      2 * SLOT_CARD_WIDTH
    );
    expect(subNodeSize({ slotCard: { rows: 12 } }, false).width).toBe(
      SLOT_CARD_WIDTH
    );
  });
});

describe('subItemSize', () => {
  it('is a round node for the model and a card as tall as its rows for a slot', () => {
    expect(subItemSize({})).toEqual({
      width: SUB_LEAF_WIDTH,
      height: SUB_LEAF_HEIGHT,
    });
    expect(subItemSize({ rows: 3 })).toEqual({
      width: SLOT_CARD_WIDTH,
      height: slotCardHeight(3),
    });
  });

  it('is a small round node for an empty slot', () => {
    expect(subItemSize({ empty: true })).toEqual({
      width: SUB_ADD_SIZE,
      height: SUB_ADD_SIZE,
    });
  });
});

describe('slotColumn and slotSide', () => {
  it('puts the Model and the Prompt in the left column, the Skills and the Tools in the right one', () => {
    expect(slotColumn('model')).toBe('left');
    expect(slotColumn('prompt')).toBe('left');
    expect(slotColumn('skills')).toBe('right');
    expect(slotColumn('tools')).toBe('right');
  });

  it('puts the columns beside the step, top to bottom, and the Model and the Prompt above it and the Skills and the Tools under it, left to right', () => {
    expect(slotSide('model', 'TB')).toBe('left');
    expect(slotSide('prompt', 'TB')).toBe('left');
    expect(slotSide('skills', 'TB')).toBe('right');
    expect(slotSide('tools', 'TB')).toBe('right');
    expect(slotSide('model', 'LR')).toBe('top');
    expect(slotSide('prompt', 'LR')).toBe('top');
    expect(slotSide('skills', 'LR')).toBe('bottom');
    expect(slotSide('tools', 'LR')).toBe('bottom');
  });
});

// Every sub-node is in the box, off the step and off each other.
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
  expect(plan.base.y + plan.base.height).toBeLessThanOrEqual(plan.height);
}

// The shapes that the canvas builds: a chat.completion step, and an agent.run
// step with a Prompt card or an empty Prompt; with empty and full slots.
const SHAPES: [number, number, 'card' | 'empty' | undefined][] = [
  [0, 0, undefined],
  [3, 0, undefined],
  [0, 5, undefined],
  [9, 9, undefined],
  [0, 0, 'empty'],
  [0, 0, 'card'],
  [3, 2, 'card'],
  [9, 9, 'card'],
];

describe('planSubRow top to bottom', () => {
  it('puts the Model, then the Prompt, left of the step, and the Skills, then the Tools, right of it', () => {
    const plan = planSubRow(shape(3, 2, 'card'), BASE, 'TB');
    const { m, p, s, t } = plan.items as Boxes;
    // The box has the same room on both sides: its middle is the middle of the step.
    expect(plan.base.x + BASE.width / 2).toBeCloseTo(plan.width / 2);
    expect(plan.base.x).toBe(plan.width - plan.base.x - BASE.width);
    for (const left of [m!, p!]) {
      expect(left.x + left.width + SUB_SIDE_GAP).toBeLessThanOrEqual(
        plan.base.x
      );
    }
    for (const right of [s!, t!]) {
      expect(right.x).toBeGreaterThanOrEqual(
        plan.base.x + BASE.width + SUB_SIDE_GAP
      );
    }
    // Top to bottom: the Model above the Prompt, the Skills above the Tools.
    expect(m!.y + m!.height).toBeLessThan(p!.y);
    expect(s!.y + s!.height).toBeLessThan(t!.y);
    expect(t!.width).toBe(SLOT_CARD_WIDTH);
    expect(t!.height).toBe(slotCardHeight(slotCardRows(3, true)));
    expect(plan.present).toEqual(['model', 'prompt', 'skills', 'tools']);
  });

  it('keeps every sub-node in the box, off the step and off each other', () => {
    for (const [tools, skills, prompt] of SHAPES) {
      for (const compact of [false, true]) {
        expectClean(
          planSubRow(shape(tools, skills, prompt), BASE, 'TB', compact)
        );
      }
    }
  });

  it('draws a chat.completion step with the Model on the left, and the Skills above the Tools on the right', () => {
    const plan = planSubRow(shape(3, 2), BASE, 'TB');
    const { m, s, t } = plan.items as Boxes;
    expect(plan.present).toEqual(['model', 'skills', 'tools']);
    expect(plan.slots.model.side).toBe('left');
    expect(m!.x + m!.width).toBeLessThan(plan.base.x);
    expect(s!.x).toBeGreaterThan(plan.base.x + BASE.width);
    expect(t!.x).toBe(s!.x);
    expect(s!.y + s!.height).toBeLessThan(t!.y);
  });

  it('reserves the height of the taller column, and centres the step in it', () => {
    const tall = planSubRow(shape(9, 0), BASE, 'TB');
    const short = planSubRow(shape(0, 0), BASE, 'TB');
    const t = tall.items.t!;
    expect(tall.height).toBeGreaterThanOrEqual(t.height);
    expect(tall.height).toBeGreaterThan(short.height);
    expect(tall.base.y + BASE.height / 2).toBeCloseTo(tall.height / 2);
    // The columns are centred on the card.
    const column = [tall.items.s!, t];
    const top = Math.min(...column.map((box) => box.y));
    const bottom = Math.max(...column.map((box) => box.y + box.height));
    expect((top + bottom) / 2).toBeCloseTo(tall.height / 2);
  });

  it('is as wide as the widest column needs, on both sides', () => {
    const cards = planSubRow(shape(3, 3), BASE, 'TB');
    const small = planSubRow(shape(0, 0), BASE, 'TB');
    const prompt = planSubRow(shape(0, 0, 'card'), BASE, 'TB');
    expect(cards.width).toBe(BASE.width + 2 * (SLOT_CARD_WIDTH + SUB_SIDE_GAP));
    expect(small.width).toBe(BASE.width + 2 * (SUB_LEAF_WIDTH + SUB_SIDE_GAP));
    expect(small.width).toBeLessThan(cards.width);
    expect(prompt.width).toBe(
      BASE.width + 2 * (PROMPT_CARD_WIDTH + SUB_SIDE_GAP)
    );
  });

  it('reserves the width of a Tools card of several columns', () => {
    const items: SubItem[] = [
      { key: 'm', slot: 'model' },
      { key: 't', slot: 'tools', rows: 12, cols: 3 },
    ];
    const plan = planSubRow({ items }, BASE, 'TB');
    expect(plan.items.t!.width).toBe(3 * SLOT_CARD_WIDTH);
    expect(plan.width).toBe(
      BASE.width + 2 * (3 * SLOT_CARD_WIDTH + SUB_SIDE_GAP)
    );
    expectClean(plan);
    expectClean(planSubRow({ items }, BASE, 'LR'));
  });

  it('puts each connector on the side of the card that faces its sub-node, in the order of the column', () => {
    const plan = planSubRow(shape(1, 1, 'card'), BASE, 'TB');
    const { model, prompt, skills, tools } = plan.slots;
    expect([model.side, prompt.side]).toEqual(['left', 'left']);
    expect([skills.side, tools.side]).toEqual(['right', 'right']);
    expect(model.x).toBe(plan.card.x);
    expect(prompt.x).toBe(plan.card.x);
    expect(skills.x).toBe(plan.card.x + plan.card.width);
    expect(tools.x).toBe(plan.card.x + plan.card.width);
    expect(model.y).toBeLessThan(prompt.y);
    expect(skills.y).toBeLessThan(tools.y);
    for (const slot of [model, prompt, skills, tools]) {
      expect(slot.y).toBeGreaterThan(plan.card.y);
      expect(slot.y).toBeLessThan(plan.card.y + plan.card.height);
    }
    // The link of a connector reaches the near side of its sub-node.
    expect(plan.items.m!.x + plan.items.m!.width).toBeLessThan(model.x);
    expect(plan.items.p!.x + plan.items.p!.width).toBeLessThan(prompt.x);
    expect(plan.items.s!.x).toBeGreaterThan(skills.x);
    expect(plan.items.t!.x).toBeGreaterThan(tools.x);
  });

  it('puts the small "+" of an empty slot beside the card, in its column, with room for its link', () => {
    for (const prompt of ['empty', 'card'] as const) {
      const plan = planSubRow(shape(0, 0, prompt), BASE, 'TB');
      const { t, s, p, m } = plan.items as Boxes;
      expect(t!.width).toBe(SUB_ADD_SIZE);
      expect(s!.height).toBe(SUB_ADD_SIZE);
      expect(t!.x).toBe(plan.base.x + BASE.width + SUB_SIDE_GAP);
      expect(s!.x).toBe(t!.x);
      expect(s!.y).toBeLessThan(t!.y);
      expect(m!.y).toBeLessThan(p!.y);
      expect(p!.x + p!.width).toBe(plan.base.x - SUB_SIDE_GAP);
      expect(plan.slots.tools.x).toBe(plan.base.x + BASE.width);
    }
  });

  it('has the connectors of a compact step on its tile, not on its caption', () => {
    const base = {
      width: 120,
      height: 108,
      tile: { left: 20, top: 0, width: 80, height: 88 },
    };
    const plan = planSubRow(shape(1, 1, 'card'), base, 'TB', true);
    expect(plan.card).toEqual({
      x: plan.base.x + 20,
      y: plan.base.y,
      width: 80,
      height: 88,
    });
    expect(plan.slots.tools.x).toBe(plan.card.x + 80);
    expect(plan.slots.skills.x).toBe(plan.card.x + 80);
    expect(plan.slots.model.x).toBe(plan.card.x);
    expect(plan.slots.prompt.x).toBe(plan.card.x);
    // The caption stays inside the box.
    expect(plan.base.y + 108).toBeLessThanOrEqual(plan.height);
  });

  it('does not change the box of the step when it has no sub-node', () => {
    const plan = planSubRow({ items: [] }, BASE, 'TB');
    expect(plan.width).toBe(336);
    expect(plan.height).toBe(BASE.height);
  });
});

describe('planSubRow left to right', () => {
  it('puts the Model, then the Prompt, in a row above the step, and the Skills, then the Tools, in a row under it', () => {
    const plan = planSubRow(shape(3, 2, 'card'), BASE, 'LR');
    expect(plan.base.x).toBe((plan.width - 336) / 2);
    const { m, p, s, t } = plan.items as Boxes;
    const upper = plan.base.y - SUB_BAND;
    const lower = plan.base.y + BASE.height + SUB_BAND;
    // The upper row is flush with the step, the Model on the left of the Prompt.
    expect(m!.y + m!.height).toBe(upper);
    expect(p!.y + p!.height).toBe(upper);
    expect(m!.x + m!.width).toBeLessThan(p!.x);
    // The lower row is flush with the step, the Skills on the left of the Tools.
    expect(s!.y).toBe(lower);
    expect(t!.y).toBe(lower);
    expect(s!.x + s!.width).toBeLessThan(t!.x);
    // Each row is centred on the step.
    expect((m!.x + p!.x + p!.width) / 2).toBeCloseTo(plan.width / 2);
    expect((s!.x + t!.x + t!.width) / 2).toBeCloseTo(plan.width / 2);
    expect(t!.width).toBe(SLOT_CARD_WIDTH);
    expect(t!.height).toBe(slotCardHeight(slotCardRows(3, true)));
    expect(plan.present).toEqual(['model', 'prompt', 'skills', 'tools']);
  });

  it('keeps every sub-node in the box, off the step and off each other', () => {
    for (const [tools, skills, prompt] of SHAPES) {
      for (const compact of [false, true]) {
        expectClean(
          planSubRow(shape(tools, skills, prompt), BASE, 'LR', compact)
        );
      }
    }
  });

  it('draws a chat.completion step with the Model above, and the Skills, then the Tools, under', () => {
    const plan = planSubRow(shape(3, 2), BASE, 'LR');
    const { m, s, t } = plan.items as Boxes;
    expect(plan.present).toEqual(['model', 'skills', 'tools']);
    expect(m!.y + m!.height).toBeLessThan(plan.base.y);
    expect(s!.y).toBeGreaterThan(plan.base.y + BASE.height);
    expect(t!.y).toBe(s!.y);
    expect(s!.x + s!.width).toBeLessThan(t!.x);
    // The Model is alone in its row, centred on the step.
    expect(m!.x + m!.width / 2).toBeCloseTo(plan.width / 2);
  });

  it('puts the connectors on the upper edge (Model, Prompt) and the lower edge (Skills, Tools), centred', () => {
    const plan = planSubRow(shape(1, 1, 'card'), BASE, 'LR');
    const { model, prompt, skills, tools } = plan.slots;
    const middle = plan.base.x + BASE.width / 2;
    expect([model.side, prompt.side]).toEqual(['top', 'top']);
    expect([skills.side, tools.side]).toEqual(['bottom', 'bottom']);
    expect(model.y).toBe(plan.base.y);
    expect(prompt.y).toBe(plan.base.y);
    expect(skills.y).toBe(plan.base.y + BASE.height);
    expect(tools.y).toBe(plan.base.y + BASE.height);
    // Left to right in the order of the row, and a label apart.
    expect(prompt.x - model.x).toBeGreaterThanOrEqual(56);
    expect(tools.x - skills.x).toBeGreaterThanOrEqual(56);
    expect((model.x + prompt.x) / 2).toBeCloseTo(middle);
    expect((skills.x + tools.x) / 2).toBeCloseTo(middle);
    // A row of one sub-node has its connector on the middle.
    const one = planSubRow(shape(1, 1), BASE, 'LR');
    expect(one.slots.model.x).toBeCloseTo(middle);
  });

  it('reserves the height of the taller row on both sides of the step', () => {
    const tall = planSubRow(shape(6, 0), BASE, 'LR');
    const short = planSubRow(shape(0, 0), BASE, 'LR');
    expect(tall.height).toBeGreaterThan(short.height);
    const { t, m } = tall.items as Boxes;
    // The taller row ends 8 px from the edge of the box.
    expect(t!.y + t!.height + 8).toBe(tall.height);
    // The Model is shorter: the room above is the same as under, and the Model is flush with the step.
    expect(m!.y + m!.height).toBe(tall.base.y - SUB_BAND);
    expect(tall.base.y).toBe(tall.height - tall.base.y - BASE.height);
  });

  it('has the same room above the step as under it', () => {
    const plan = planSubRow(shape(2, 1, 'card'), BASE, 'LR');
    expect(plan.base.y).toBeGreaterThan(0);
    expect(plan.height).toBe(2 * plan.base.y + BASE.height);
    expect(plan.height / 2).toBe(plan.base.y + BASE.height / 2);
    const { m, p, s, t } = plan.items as Boxes;
    for (const box of [m!, p!]) {
      expect(box.y + box.height).toBeLessThanOrEqual(plan.base.y - SUB_BAND);
    }
    for (const box of [s!, t!]) {
      expect(box.y).toBeGreaterThanOrEqual(
        plan.base.y + BASE.height + SUB_BAND
      );
    }
  });

  it('is as wide as the step or the wider row, and keeps the step in the middle', () => {
    const wide = planSubRow(shape(3, 3, 'card'), BASE, 'LR');
    const narrow = planSubRow(shape(0, 0), BASE, 'LR');
    expect(wide.width).toBeGreaterThan(BASE.width);
    expect(wide.base.x + BASE.width / 2).toBeCloseTo(wide.width / 2);
    expect(narrow.width).toBeGreaterThanOrEqual(BASE.width);
  });

  it('keeps the labels apart on a narrow compact step, with a caption that hangs out of it', () => {
    const narrow = { width: 80, height: 108, overhang: 10 };
    const plan = planSubRow(shape(2, 2, 'card'), narrow, 'LR', true);
    const { model, prompt, skills, tools } = plan.slots;
    expect(prompt.x - model.x).toBeGreaterThanOrEqual(56);
    expect(tools.x - skills.x).toBeGreaterThanOrEqual(56);
    expect((model.x + prompt.x) / 2).toBeCloseTo(plan.base.x + 40);
    // The lower connectors are below the caption, which hangs out of the box.
    expect(skills.y).toBe(plan.base.y + 108 + 10);
    expect(model.y).toBe(plan.base.y);
    expect(plan.height / 2).toBe(plan.base.y + 108 / 2);
    // The compact cards are tiles.
    expect(plan.items.t!.height).toBe(SUB_TILE_SIZE);
    expect(plan.items.p!.height).toBe(SUB_TILE_SIZE);
    expectClean(plan);
  });

  it('does not change the box of the step when no sub-node is wider than it', () => {
    const plan = planSubRow({ items: [] }, BASE, 'LR');
    expect(plan.width).toBe(336);
  });
});

// The Prompt card of an agent step: a card of text with a fixed height, after
// the Model: below it in the left column, right of it in the row above the step.
describe('the Prompt slot', () => {
  it('has a fixed size, and a small "+" when empty', () => {
    expect(subItemSize({ prompt: true })).toEqual({
      width: PROMPT_CARD_WIDTH,
      height: promptCardHeight(),
    });
    expect(subItemSize({ empty: true }).width).toBe(SUB_ADD_SIZE);
  });

  it('sits below the Model, in the same column, top to bottom', () => {
    for (const empty of [false, true]) {
      const plan = planSubRow(
        shape(3, 2, empty ? 'empty' : 'card'),
        BASE,
        'TB'
      );
      const { m, p } = plan.items as Boxes;
      expect(p!.y).toBeGreaterThan(m!.y + m!.height);
      expect(p!.x + p!.width).toBe(m!.x + m!.width);
    }
  });

  it('sits right of the Model, in the same row above the step, left to right', () => {
    for (const empty of [false, true]) {
      const plan = planSubRow(
        shape(3, 2, empty ? 'empty' : 'card'),
        BASE,
        'LR'
      );
      const { m, p } = plan.items as Boxes;
      expect(p!.x).toBeGreaterThan(m!.x + m!.width);
      expect(p!.y + p!.height).toBe(m!.y + m!.height);
    }
  });

  it('leaves a step with no prompt with three connectors', () => {
    for (const direction of ['TB', 'LR'] as const) {
      expect(planSubRow(shape(3, 2), BASE, direction).present).toEqual([
        'model',
        'skills',
        'tools',
      ]);
    }
  });
});
