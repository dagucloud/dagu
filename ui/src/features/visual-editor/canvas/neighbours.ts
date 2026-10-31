// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { CanvasDirection, CanvasGraph, StepKey } from '../contracts';

/** Direction of an arrow key on the canvas. */
export type NavDirection = 'up' | 'down' | 'left' | 'right';

type Move = 'child' | 'parent' | 'previous' | 'next';

// The flow axis follows the edges; the other axis holds the siblings.
const MOVES: Record<CanvasDirection, Record<NavDirection, Move>> = {
  TB: { down: 'child', up: 'parent', left: 'previous', right: 'next' },
  LR: { right: 'child', left: 'parent', up: 'previous', down: 'next' },
};

/**
 * The step that an arrow key moves the selection to, or null when the
 * selection stays. `direction` is the layout direction of the canvas. Along
 * the flow axis the key goes to the first child or the first parent. Across
 * it, the key goes to the previous or next child of the same first parent,
 * or of the roots of the lane. Without a selection, the first root is the
 * answer.
 */
export function neighbourStep(
  graph: CanvasGraph,
  selected: StepKey | null,
  dir: NavDirection,
  direction: CanvasDirection
): StepKey | null {
  const order = new Map(graph.nodes.map((node, i) => [node.key, i]));
  const byOrder = (a: StepKey, b: StepKey) =>
    (order.get(a) ?? 0) - (order.get(b) ?? 0);
  const parents = new Map<StepKey, StepKey[]>();
  const children = new Map<StepKey, StepKey[]>();
  const link = (map: Map<StepKey, StepKey[]>, from: StepKey, to: StepKey) => {
    const list = map.get(from) ?? [];
    if (!list.includes(to)) list.push(to);
    map.set(from, list);
  };
  for (const edge of graph.edges) {
    if (!order.has(edge.source) || !order.has(edge.target)) continue;
    if (edge.source === edge.target) continue;
    link(children, edge.source, edge.target);
    link(parents, edge.target, edge.source);
  }
  for (const list of [...parents.values(), ...children.values()]) {
    list.sort(byOrder);
  }
  const roots = (lane: string) =>
    graph.nodes
      .filter((node) => node.lane === lane && !parents.has(node.key))
      .map((node) => node.key);

  if (selected === null) {
    return roots('main')[0] ?? graph.nodes[0]?.key ?? null;
  }
  const node = graph.nodes.find((candidate) => candidate.key === selected);
  if (!node) return null;

  const move = MOVES[direction][dir];
  if (move === 'child') return children.get(selected)?.[0] ?? null;
  if (move === 'parent') return parents.get(selected)?.[0] ?? null;
  const parent = parents.get(selected)?.[0];
  const siblings =
    parent === undefined ? roots(node.lane) : (children.get(parent) ?? []);
  const at = siblings.indexOf(selected);
  const next = siblings[move === 'next' ? at + 1 : at - 1];
  return at < 0 || next === undefined ? null : next;
}

/**
 * The step that an arrow key moves the selection to in an agent graph. The
 * tools are a grid of `columns`, row by row: left and right move one card,
 * up and down move one row, and up from the first row goes to the hub.
 * Without a selection, the hub is the answer.
 */
export function agentNeighbour(
  graph: CanvasGraph,
  selected: StepKey | null,
  dir: NavDirection,
  columns: number
): StepKey | null {
  const hub = graph.nodes.find((node) => node.kind === 'agent')?.key ?? null;
  const tools = graph.nodes
    .filter((node) => node.lane === 'main' && node.key !== hub)
    .map((node) => node.key);
  if (selected === null) {
    return hub ?? tools[0] ?? null;
  }
  if (selected === hub) {
    return dir === 'down' ? (tools[0] ?? null) : null;
  }
  const at = tools.indexOf(selected);
  if (at < 0) {
    return null;
  }
  const step = Math.max(1, columns);
  const target =
    dir === 'left'
      ? at - 1
      : dir === 'right'
        ? at + 1
        : dir === 'up'
          ? at - step
          : at + step;
  if (dir === 'up' && target < 0) {
    return hub;
  }
  return tools[target] ?? null;
}
