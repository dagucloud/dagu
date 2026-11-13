// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { CanvasDirection } from '../contracts';
import { PROMPT_FULL, promptFit, type PromptFit } from './promptCard';

/**
 * The slots of a chat step or an agent step where sub-nodes connect: its
 * model, its prompt (an agent step only), its skills, its tools (MCP servers
 * and built-in tools) and its DAGs (the DAG tools).
 */
export type SubSlot = 'prompt' | 'model' | 'tools' | 'skills' | 'dags';

/** The slots that have a card of rows. The model has one round node, the prompt a card of text. */
export type CardSlot = 'tools' | 'skills' | 'dags';

/** One sub-node of a chat step, as the layout needs it. */
export interface SubItem {
  key: string;
  slot: SubSlot;
  /**
   * Set on the card of a Tools or Skills slot: the rows under its header,
   * the add row included. The model has no rows: it is a round node.
   */
  rows?: number;
  /** Set on a card of several columns, each as wide as `SLOT_CARD_WIDTH`: the Tools card. One column when unset. */
  cols?: number;
  /**
   * Set on the card of the DAGs slot: the DAG entries that it lists, each
   * with its miniature, and `rows` rows of the height of a Tools row under
   * them (limits, "+N more", add).
   */
  dagCard?: { entries: number; rows: number };
  /**
   * Set on the small round "+" that stands for a Tools, Skills or Prompt
   * slot with no entry: it has no rows and no card.
   */
  empty?: boolean;
  /**
   * Set on the card of the Prompt slot: a card of text. Its height follows
   * the fit of its texts; `true` is the full height.
   */
  prompt?: true | PromptFit;
  /**
   * Set on the Model node that has controls: a card of `rows` models, with a
   * muted line for the CLI of an agent step when `cli` is true, and an Effort
   * control when `effort` is true (a single model). A compact
   * canvas draws the round node instead.
   */
  modelCard?: { rows: number; cli: boolean; effort?: boolean };
}

/** The sub-nodes of a chat step: the Model, the Prompt, and the Skills and Tools slots. */
export interface SubRowShape {
  items: SubItem[];
}

/**
 * Box of the round Model node: a round icon, up to two lines of name and a
 * line of detail.
 */
export const SUB_LEAF_WIDTH = 96;
export const SUB_LEAF_HEIGHT = 118;
/** Diameter of the round icon of the Model node. */
export const SUB_ICON_SIZE = 64;
/** Side of the small round "+" of an empty Tools or Skills slot. */
export const SUB_ADD_SIZE = 24;
/** Side of the tile that stands for a slot card or the Prompt card in a compact canvas. */
export const SUB_TILE_SIZE = 48;

/** Width of the card of a Tools or Skills slot, a small form block. */
export const SLOT_CARD_WIDTH = 220;
/** Width of the card of the Prompt slot: a little wider, for the text. */
export const PROMPT_CARD_WIDTH = 260;
/** Width of the card of the DAGs slot: wide enough for the miniature of a DAG. */
export const DAG_CARD_WIDTH = 260;
/** The DAGs card lists this many DAG tools, then a "+N more" row. */
export const DAG_CARD_MAX_ENTRIES = 3;
/**
 * Heights of the parts of one DAG entry of the DAGs card: the row of its
 * name, one line of description, its miniature, and one line of inputs.
 */
export const DAG_ENTRY = {
  title: 26,
  description: 16,
  graph: 52,
  inputs: 22,
  pad: 6,
} as const;

/** Height of one DAG entry of the DAGs card. */
export const DAG_ENTRY_HEIGHT =
  DAG_ENTRY.title +
  DAG_ENTRY.description +
  DAG_ENTRY.graph +
  DAG_ENTRY.inputs +
  DAG_ENTRY.pad;
/**
 * Heights of the parts of the Prompt card: the header, and for each of the
 * system message and the prompt a label and a preview of the first lines. A
 * preview has two heights, by the lines of its text (see `PromptFit`).
 */
const PROMPT_PARTS = {
  header: 28,
  label: 16,
  system: { 1: 22, 2: 40 },
  prompt: { 2: 38, 4: 72 },
  pad: 8,
} as const;

/** Parts of the Prompt card, in px. */
export function promptCardParts(fit: PromptFit = PROMPT_FULL) {
  return {
    header: PROMPT_PARTS.header,
    label: PROMPT_PARTS.label,
    system: PROMPT_PARTS.system[fit.system],
    prompt: PROMPT_PARTS.prompt[fit.prompt],
    pad: PROMPT_PARTS.pad,
  };
}

/** Height of the Prompt card. */
export function promptCardHeight(fit: PromptFit = PROMPT_FULL): number {
  const p = promptCardParts(fit);
  return p.header + 2 * p.label + p.system + p.prompt + p.pad;
}
/** Width of the Model node: the round icon over the floating controls. */
export const MODEL_CARD_WIDTH = 200;
/** The parts of the Model node with controls, in px. */
const MODEL_CARD = {
  /** The round icon, with the room under it. */
  header: SUB_ICON_SIZE + 6,
  /** A model: the Endpoint select and the Model field, with their gap. */
  row: 60,
  /** The room over each model of a list after the first one. */
  gap: 6,
  /** The line over a model of a list, with its gap. */
  heading: 18,
  /** The muted line of the CLI of an agent step. */
  cli: 18,
  /** The Effort control of a single model, with its gap. */
  effort: 32,
  /** The room under the last control. */
  pad: 6,
} as const;

/** Height of the Model node with controls: its round icon and its floating controls. */
export function modelCardHeight(
  rows: number,
  cli: boolean,
  effort = false
): number {
  return (
    MODEL_CARD.header +
    rows * MODEL_CARD.row +
    (effort ? MODEL_CARD.effort : 0) +
    (rows > 1 ? rows * MODEL_CARD.heading + (rows - 1) * MODEL_CARD.gap : 0) +
    (cli ? MODEL_CARD.cli : 0) +
    MODEL_CARD.pad
  );
}

/** A card lists this many entries, then a "+N more" row. */
export const SLOT_CARD_MAX_ROWS = 8;
/** Heights of the header and of a row of a slot card. */
export const SLOT_CARD_HEADER = 28;
export const SLOT_CARD_ROW = 26;
/** The room under the last row of a slot card. */
const CARD_PAD = 4;

/**
 * Rows of a slot card for `count` entries: the entries shown, the group
 * headings (`headings`), "+N more", the add row, and the row of limits
 * (`limits`) under the header.
 */
export function slotCardRows(
  count: number,
  editable: boolean,
  headings = 0,
  limits = false
): number {
  if (count === 0) return 1;
  const more = count > SLOT_CARD_MAX_ROWS ? 1 : 0;
  return (
    Math.min(count, SLOT_CARD_MAX_ROWS) +
    headings +
    more +
    (editable ? 1 : 0) +
    (limits ? 1 : 0)
  );
}

/** Size of a slot card of `cols` columns and `rows` rows under its header. */
export function slotCardSize(
  cols: number,
  rows: number
): { width: number; height: number } {
  return {
    width: cols * SLOT_CARD_WIDTH,
    height: slotCardHeight(rows),
  };
}

/** Height of a slot card with `rows` rows under its header. */
export function slotCardHeight(rows: number): number {
  return SLOT_CARD_HEADER + rows * SLOT_CARD_ROW + CARD_PAD;
}

/**
 * Height of the DAGs card: its header, `entries` DAG entries and `rows` rows
 * of the height of a Tools row (limits, "+N more", add).
 */
export function dagCardHeight(entries: number, rows: number): number {
  return slotCardHeight(rows) + Math.max(0, entries) * DAG_ENTRY_HEIGHT;
}

/** The gap between two sub-nodes of one column or one row. */
const SUB_GAP = 20;
/**
 * Left to right: from the top or the bottom of the step to the near side of
 * the sub-nodes: the slot label, and enough line to read as a link.
 */
export const SUB_BAND = 56;
const SUB_BOTTOM = 8;
/**
 * Top to bottom: from the side of the step to the near side of the
 * sub-nodes: enough line to read as a link, with the slot label above it.
 */
export const SUB_SIDE_GAP = 56;
/** The connectors are at least this far apart, for a narrow step, so that their labels never touch. */
export const SLOT_MIN_GAP = 56;
/**
 * The slots in the order of their sub-nodes. The first two make the left
 * column, top to bottom: the Model, then the Prompt. The last three make the
 * right column: the Skills, the Tools, then the DAGs, whose cards are the
 * tallest.
 */
export const SLOT_ORDER: readonly SubSlot[] = [
  'model',
  'prompt',
  'skills',
  'tools',
  'dags',
];
/** The side of the step where the sub-nodes of a slot sit. */
export type SubSide = 'left' | 'right' | 'top' | 'bottom';

/**
 * The column of a slot: the Model and the Prompt are on the left, the Skills,
 * the Tools and the DAGs on the right, in the same way in both directions.
 */
export function slotColumn(slot: SubSlot): 'left' | 'right' {
  return slot === 'skills' || slot === 'tools' || slot === 'dags'
    ? 'right'
    : 'left';
}

/**
 * Where the sub-node of a slot sits. Top to bottom, the sub-nodes sit beside
 * the step, in the column of their slot. Left to right, the Model and the
 * Prompt sit above the step, and the Skills and the Tools under it.
 */
export function slotSide(slot: SubSlot, direction: CanvasDirection): SubSide {
  if (direction !== 'LR') return slotColumn(slot);
  return slotColumn(slot) === 'left' ? 'top' : 'bottom';
}

/**
 * Size of a sub-node: the round Model node, a small "+", the Prompt card, or a
 * slot card of `rows` rows. A compact canvas draws a tile for each card.
 */
export function subItemSize(
  item: Pick<
    SubItem,
    'rows' | 'cols' | 'empty' | 'prompt' | 'modelCard' | 'dagCard'
  >,
  compact = false
): {
  width: number;
  height: number;
} {
  if (item.empty) return { width: SUB_ADD_SIZE, height: SUB_ADD_SIZE };
  if (item.modelCard && !compact) {
    return {
      width: MODEL_CARD_WIDTH,
      height: modelCardHeight(
        item.modelCard.rows,
        item.modelCard.cli,
        item.modelCard.effort
      ),
    };
  }
  const card = item.prompt || item.dagCard || item.rows !== undefined;
  if (compact && card) return { width: SUB_TILE_SIZE, height: SUB_TILE_SIZE };
  if (item.prompt) {
    return {
      width: PROMPT_CARD_WIDTH,
      height: promptCardHeight(
        item.prompt === true ? PROMPT_FULL : item.prompt
      ),
    };
  }
  if (item.dagCard) {
    return {
      width: DAG_CARD_WIDTH,
      height: dagCardHeight(item.dagCard.entries, item.dagCard.rows),
    };
  }
  return item.rows === undefined
    ? { width: SUB_LEAF_WIDTH, height: SUB_LEAF_HEIGHT }
    : slotCardSize(item.cols ?? 1, item.rows);
}

/** Size of a sub-node of the canvas: its `slotCard` rows decide a card. */
export function subNodeSize(
  node: {
    slotCard?: {
      slot?: CardSlot;
      rows: number;
      cols?: number;
      entries?: readonly unknown[];
    };
    promptCard?: { system: string; prompt: string };
    modelSat?: {
      card?: { rows: number; cli: boolean; effort?: boolean } | undefined;
    };
    addSat?: { slot: SubSlot };
  },
  compact: boolean
): { width: number; height: number } {
  if (node.slotCard) {
    const { rows, cols, slot, entries } = node.slotCard;
    if (slot === 'dags') {
      const shown = Math.min(entries?.length ?? 0, DAG_CARD_MAX_ENTRIES);
      return subItemSize({ dagCard: { entries: shown, rows } }, compact);
    }
    return subItemSize({ rows, ...(cols ? { cols } : {}) }, compact);
  }
  if (node.promptCard) {
    const { system, prompt } = node.promptCard;
    return subItemSize({ prompt: promptFit(system, prompt) }, compact);
  }
  if (node.modelSat?.card) {
    return subItemSize({ modelCard: node.modelSat.card }, compact);
  }
  // The Model has a dashed "+" of its own size; the other slots a small one.
  return subItemSize(
    node.addSat && node.addSat.slot !== 'model' ? { empty: true } : {},
    compact
  );
}

export interface SubBox {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** A connector of a slot on the step: where it is, and the side it leaves by. */
export interface SlotConnector {
  x: number;
  y: number;
  side: SubSide;
}

/** Where everything of a chat step is in its box, in px from the top left. */
export interface SubRowPlan {
  /** The box of the step with its sub-nodes. */
  width: number;
  height: number;
  /** The place of the step without its sub-nodes (its card and caption). */
  base: SubBox;
  /**
   * The place of the card itself, in the box. It is `base` for a normal
   * card, and the tile without its caption for a compact card.
   */
  card: SubBox;
  /** The sub-nodes, by key. */
  items: Record<string, SubBox>;
  /** The connectors of the slots, on the sides of the step. */
  slots: Record<SubSlot, SlotConnector>;
  /** The slots that the step has a sub-node for, in the order of the layout: only they get a drawn connector. */
  present: SubSlot[];
}

/** The base of the step: its box, and where its card is in it. */
export interface SubRowBase {
  width: number;
  height: number;
  overhang?: number;
  /** The card in the box, when the box holds more than the card (a caption). */
  tile?: { left: number; top: number; width: number; height: number };
}

/**
 * Plans the box of a chat step. `base` is the step without sub-nodes: its
 * card, or its compact card with its caption.
 *
 * Top to bottom, the sub-nodes sit in two columns beside the step, centred
 * on its card: the Model, then the Prompt, on its left, and the Skills, then
 * the Tools, on its right. A column is as wide as its widest sub-node, and the
 * box has the same room on both sides, so that the middle of the box stays on
 * the middle of the step. The box is as tall as the taller of the step and the
 * columns. The connectors of a side sit on the side of the card, spread in the
 * order of the column, and each is joined to its sub-node by a link.
 *
 * Left to right, the sub-nodes sit in two rows, one above the step and one
 * under it: the Model, then the Prompt, from left to right above, and the
 * Skills, then the Tools, from left to right under. Each row is centred on the
 * step and flush with it. The connectors sit on the upper edge of the step
 * (Model, Prompt) and on its lower edge (Skills, Tools), centred on its middle
 * and a fixed distance apart (the labels need it). The box is as wide as the
 * widest of the step and the rows. It has the same room above the step as
 * under it, the taller row deciding, so that the handles on the middle of its
 * sides stay on the middle of the step.
 */
export function planSubRow(
  shape: SubRowShape,
  base: SubRowBase,
  direction: CanvasDirection,
  compact = false
): SubRowPlan {
  return direction === 'LR'
    ? planAround(shape, base, compact)
    : planBeside(shape, base, compact);
}

function planBeside(
  shape: SubRowShape,
  base: SubRowBase,
  compact: boolean
): SubRowPlan {
  const tile = base.tile ?? {
    left: 0,
    top: 0,
    width: base.width,
    height: base.height,
  };
  const columns = splitColumns(shape);
  const { sizeOf, widthOf, heightOf } = measures(compact);
  // A step with no sub-node keeps the box of its card.
  const reserve =
    shape.items.length === 0
      ? 0
      : Math.max(widthOf(columns.left), widthOf(columns.right)) + SUB_SIDE_GAP;
  const columnHeight = Math.max(
    heightOf(columns.left),
    heightOf(columns.right)
  );
  // The columns are centred on the card: the box reaches as far above and
  // below the middle of the card as the step or the columns need.
  const middle = tile.top + tile.height / 2;
  const above = Math.max(middle, columnHeight / 2);
  const below = Math.max(base.height - middle, columnHeight / 2);
  const baseTop = above - middle;
  const baseLeft = reserve;
  const card: SubBox = {
    x: baseLeft + tile.left,
    y: baseTop + tile.top,
    width: tile.width,
    height: tile.height,
  };

  const items: Record<string, SubBox> = {};
  const slots = {} as Record<SubSlot, SlotConnector>;
  for (const side of ['left', 'right'] as const) {
    const column = columns[side];
    let y = above - heightOf(column) / 2;
    column.forEach((item, index) => {
      const size = sizeOf(item);
      // A column is flush with the step: its near side is a gap away.
      const x =
        side === 'left'
          ? reserve - SUB_SIDE_GAP - size.width
          : reserve + base.width + SUB_SIDE_GAP;
      items[item.key] = { x, y, ...size };
      y += size.height + SUB_GAP;
      slots[item.slot] = {
        x: side === 'left' ? card.x : card.x + card.width,
        y: card.y + (card.height * (index + 1)) / (column.length + 1),
        side,
      };
    });
  }
  return {
    width: base.width + 2 * reserve,
    height: above + below,
    base: { x: baseLeft, y: baseTop, width: base.width, height: base.height },
    card,
    items,
    slots: withMissingSlots(slots, card),
    present: presentSlots(shape),
  };
}

function planAround(
  shape: SubRowShape,
  base: SubRowBase,
  compact: boolean
): SubRowPlan {
  const columns = splitColumns(shape);
  const { sizeOf } = measures(compact);
  const present = presentSlots(shape);
  // The left column of the columns beside the step is the row above the step.
  const rows = { top: columns.left, bottom: columns.right };
  const rowWidth = (row: SubItem[]) =>
    row.reduce((sum, item) => sum + sizeOf(item).width, 0) +
    Math.max(0, row.length - 1) * SUB_GAP;
  const rowHeight = (row: SubItem[]) =>
    Math.max(0, ...row.map((item) => sizeOf(item).height));
  // A caption that hangs out of the lower side of the step pushes the lower row down.
  const overhang = base.overhang ?? 0;
  const reserve =
    SUB_BAND +
    Math.max(rowHeight(rows.top), rowHeight(rows.bottom)) +
    SUB_BOTTOM +
    overhang;
  const width = Math.max(base.width, rowWidth(rows.top), rowWidth(rows.bottom));
  const baseLeft = (width - base.width) / 2;
  const baseTop = reserve;
  const lower = baseTop + base.height + overhang;
  const middle = baseLeft + base.width / 2;

  const items: Record<string, SubBox> = {};
  const slots = {} as Record<SubSlot, SlotConnector>;
  for (const side of ['top', 'bottom'] as const) {
    const row = rows[side];
    let x = (width - rowWidth(row)) / 2;
    row.forEach((item, index) => {
      const size = sizeOf(item);
      // A row is flush with the step: its near side is a band away.
      const y =
        side === 'top' ? baseTop - SUB_BAND - size.height : lower + SUB_BAND;
      items[item.key] = { x, y, ...size };
      x += size.width + SUB_GAP;
      slots[item.slot] = {
        x: middle + (index - (row.length - 1) / 2) * SLOT_MIN_GAP,
        y: side === 'top' ? baseTop : lower,
        side,
      };
    });
  }
  const box: SubBox = {
    x: baseLeft,
    y: baseTop,
    width: base.width,
    height: base.height,
  };
  return {
    width,
    height: base.height + 2 * reserve,
    base: box,
    card: box,
    items,
    slots: withMissingSlots(slots, box),
    present,
  };
}

// The sub-nodes of the shape in two columns, each in the order of the slots.
function splitColumns(shape: SubRowShape): Record<'left' | 'right', SubItem[]> {
  const columns: Record<'left' | 'right', SubItem[]> = { left: [], right: [] };
  for (const item of shape.items) {
    columns[slotColumn(item.slot)].push(item);
  }
  for (const column of Object.values(columns)) {
    column.sort(
      (a, b) => SLOT_ORDER.indexOf(a.slot) - SLOT_ORDER.indexOf(b.slot)
    );
  }
  return columns;
}

// The sizes of the sub-nodes, and of a column of them.
function measures(compact: boolean) {
  const sizeOf = (item: SubItem) => subItemSize(item, compact);
  return {
    sizeOf,
    widthOf: (column: SubItem[]) =>
      Math.max(0, ...column.map((item) => sizeOf(item).width)),
    heightOf: (column: SubItem[]) =>
      column.reduce((sum, item) => sum + sizeOf(item).height, 0) +
      Math.max(0, column.length - 1) * SUB_GAP,
  };
}

// The slots of the shape, in the order of the layout.
function presentSlots(shape: SubRowShape): SubSlot[] {
  return SLOT_ORDER.filter((slot) =>
    shape.items.some((item) => item.slot === slot)
  );
}

// A slot that has no sub-node in the plan still has a connector, on the lower
// edge of the card, so that the record is whole.
function withMissingSlots(
  slots: Record<SubSlot, SlotConnector>,
  card: SubBox
): Record<SubSlot, SlotConnector> {
  for (const slot of SLOT_ORDER) {
    slots[slot] ??= {
      x: card.x + card.width / 2,
      y: card.y + card.height,
      side: 'bottom',
    };
  }
  return slots;
}
