// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { TOOL_KINDS, kindOfCategory, type ToolKind } from '../tools/kinds';
import type { BuiltinEntryModel, ToolEntryModel } from '../tools/model';
import { SLOT_CARD_MAX_ROWS } from './subRow';

/** One tool of a step: an entry of the tool map, or a built-in tool. */
export type ToolItem =
  | { type: 'entry'; entry: ToolEntryModel }
  | { type: 'builtin'; entry: BuiltinEntryModel };

/** The listed tools of one kind. */
export interface ToolGroup {
  kind: ToolKind;
  /** The number of tools of this kind that the step has, listed or not. */
  total: number;
  /** The tools of this kind that the card lists, in the order of the YAML. */
  items: ToolItem[];
  /** In a card of columns: the tools of this kind that it does not list. The group has its own "+N more" row. */
  hidden: ToolItem[];
}

export interface ToolCardPlan {
  /** The groups that have a listed tool, in the order of `TOOL_KINDS`. */
  groups: ToolGroup[];
  /** True when each group has a heading: the step has more than one kind. */
  headed: boolean;
  /** The tools that the card does not list: the "+N more" row stands for them. */
  hidden: ToolItem[];
  /** The number of group headings that the card draws. */
  headings: number;
  /**
   * The groups of each column, left to right. A card has one column, except
   * for more than one kind of tool when the step has more than `max` tools,
   * or when one column would have more than `TOOL_COLUMN_MAX_ROWS` rows. Then
   * each kind lists up to `max` tools, and the kinds in the order of
   * `TOOL_KINDS` share two or more columns: the fewest whose tallest column
   * has at most `TOOL_COLUMN_MAX_ROWS` rows.
   */
  columns: ToolGroup[][];
  /** The rows of the tallest column: its headings, its tools and its "+N more" rows. */
  rows: number;
}

/**
 * The entries of a step that belong to the DAGs card (the DAG tools, and an
 * entry of `dags` that is not a mapping) and those that belong to the Tools
 * card (the MCP servers), each in the order of the YAML.
 */
export function splitDagEntries(entries: readonly ToolEntryModel[]): {
  dags: ToolEntryModel[];
  others: ToolEntryModel[];
} {
  const dags: ToolEntryModel[] = [];
  const others: ToolEntryModel[] = [];
  for (const entry of entries) {
    (kindOfCategory(entry.category) === 'dag' ? dags : others).push(entry);
  }
  return { dags, others };
}

/** A column of the Tools card is short enough up to this many rows (headings, tools and "+N more" rows). */
export const TOOL_COLUMN_MAX_ROWS = 10;

// The rows of a group of a card of columns.
const columnRows = (groups: ToolGroup[]) =>
  groups.reduce(
    (sum, group) =>
      sum + 1 + group.items.length + (group.hidden.length > 0 ? 1 : 0),
    0
  );

// The split of `groups` in `k` contiguous columns that has the shortest tallest column.
function balance(
  groups: ToolGroup[],
  k: number
): { columns: ToolGroup[][]; rows: number } {
  if (k === 1) return { columns: [groups], rows: columnRows(groups) };
  let best: { columns: ToolGroup[][]; rows: number } | undefined;
  for (let cut = 1; cut <= groups.length - k + 1; cut++) {
    const head = groups.slice(0, cut);
    const rest = balance(groups.slice(cut), k - 1);
    const rows = Math.max(columnRows(head), rest.rows);
    if (!best || rows < best.rows) {
      best = { columns: [head, ...rest.columns], rows };
    }
  }
  return best ?? { columns: [groups], rows: columnRows(groups) };
}

/**
 * Plans the rows of the Tools card of a step. The tools go in groups, one for
 * each kind. A group has a heading only when the step has more than one kind
 * of tool. At most `max` tools show: the others are `hidden`. When the step
 * has more than one kind and a single column would pass `TOOL_COLUMN_MAX_ROWS`
 * rows (or the step has more than `max` tools), each kind shows up to `max`
 * tools, and the kinds go in columns (see `columns`).
 */
export function planToolCard(
  entries: readonly ToolEntryModel[],
  builtin: readonly BuiltinEntryModel[],
  max: number = SLOT_CARD_MAX_ROWS
): ToolCardPlan {
  const byKind = new Map<ToolKind, ToolItem[]>();
  const add = (kind: ToolKind, item: ToolItem) => {
    const list = byKind.get(kind);
    if (list) list.push(item);
    else byKind.set(kind, [item]);
  };
  for (const entry of entries) {
    add(kindOfCategory(entry.category), { type: 'entry', entry });
  }
  for (const entry of builtin) add('builtin', { type: 'builtin', entry });
  const kinds = TOOL_KINDS.filter((kind) => byKind.has(kind));
  const groups: ToolGroup[] = [];
  const hidden: ToolItem[] = [];
  const total = entries.length + builtin.length;
  // The card of one column has a row for each tool, each heading and "+N more".
  const singleRows =
    Math.min(total, max) + kinds.length + (total > max ? 1 : 0);
  const columnar =
    kinds.length > 1 && (total > max || singleRows > TOOL_COLUMN_MAX_ROWS);
  let shown = 0;
  for (const kind of kinds) {
    const all = byKind.get(kind) ?? [];
    const room = columnar ? max : Math.max(0, max - shown);
    const items = all.slice(0, room);
    const rest = all.slice(room);
    hidden.push(...rest);
    if (items.length === 0) continue;
    groups.push({
      kind,
      total: all.length,
      items,
      hidden: columnar ? rest : [],
    });
    shown += items.length;
  }
  const headed = kinds.length > 1;
  const headings = headed ? groups.length : 0;
  let columns = { columns: [groups], rows: 0 };
  if (columnar) {
    // The smallest number of columns whose tallest column is short enough: a
    // column may hold two kinds. When no split is short enough, the one with
    // the shortest tallest column.
    let best: ReturnType<typeof balance> | undefined;
    for (let k = 2; k <= groups.length; k++) {
      const split = balance(groups, k);
      if (split.rows <= TOOL_COLUMN_MAX_ROWS) {
        best = split;
        break;
      }
      if (!best || split.rows < best.rows) best = split;
    }
    columns = best ?? columns;
  } else {
    columns.rows = shown + headings + (hidden.length > 0 ? 1 : 0);
  }
  return {
    groups,
    headed,
    hidden,
    headings,
    columns: groups.length === 0 ? [] : columns.columns,
    rows: columns.rows,
  };
}
