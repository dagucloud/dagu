// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ApiDAGDetails, ApiStep } from '../contracts';

/** A DAG with more steps than this shows as a summary of its ranks, not as a graph. */
export const MINI_MAX_STEPS = 24;
/** A summary has at most this many bars: longer chains are merged. */
export const MINI_MAX_BARS = 40;

/** The size of the box that the miniature fits in, in px. */
export interface MiniBox {
  width: number;
  height: number;
}

/** What marks a step in the miniature, besides its place. */
export interface MiniFlags {
  /** The step calls another DAG. */
  call: boolean;
  /** The step has a precondition: it may not run. */
  conditional: boolean;
  /** The step fans out (`parallel`). */
  parallel: boolean;
  /** The step routes to others by the value of an expression. */
  router: boolean;
}

export interface MiniNode extends MiniFlags {
  name: string;
  /** Top left corner, in the units of `MiniGraph`. */
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface MiniEdge {
  id: string;
  from: string;
  to: string;
  /** The points of the curve: start, two controls, end. */
  points: [number, number][];
}

/** What the miniature says about the DAG in words, for its tooltip. */
export interface MiniStats {
  steps: number;
  /** The ranks of the flow: the longest chain of steps. */
  depth: number;
  conditional: number;
  /** The distinct DAGs that the steps call, in order. */
  calls: string[];
}

export type MiniGraph =
  | { kind: 'empty' }
  | {
      kind: 'graph';
      /** The units of the coordinates: the box of the drawing. */
      width: number;
      height: number;
      direction: 'LR' | 'TB';
      nodes: MiniNode[];
      edges: MiniEdge[];
      stats: MiniStats;
    }
  | {
      kind: 'summary';
      /** The steps in each rank, in the order of the flow. */
      ranks: number[];
      stats: MiniStats;
    };

const NODE_LONG = 1.5;
const NODE_SHORT = 1;
const GAP_ALONG = 1.1;
const GAP_CROSS = 0.45;

function flagsOf(step: ApiStep): MiniFlags {
  return {
    call: !!step.call,
    conditional: (step.preconditions?.length ?? 0) > 0,
    parallel: !!step.parallel,
    router: !!step.router,
  };
}

/** The upstream step names of each step that the DAG holds: depends, inferred depends and routes. */
function upstreamOf(steps: readonly ApiStep[]): Map<string, string[]> {
  const known = new Set(steps.map((step) => step.name));
  const up = new Map<string, string[]>(steps.map((step) => [step.name, []]));
  const link = (from: string, to: string) => {
    if (!known.has(from) || !known.has(to) || from === to) return;
    const list = up.get(to);
    if (list && !list.includes(from)) list.push(from);
  };
  for (const step of steps) {
    for (const dep of step.depends ?? []) link(dep, step.name);
    for (const dep of step.inferredDepends ?? []) link(dep, step.name);
    for (const route of step.router?.routes ?? []) {
      for (const target of route.targets) link(step.name, target);
    }
  }
  return up;
}

/**
 * The rank of each step: the length of the longest chain of steps before it.
 * A cycle (the server refuses it) never loops: a step that is on the path
 * being walked counts as rank 0.
 */
export function rankSteps(
  names: readonly string[],
  up: ReadonlyMap<string, readonly string[]>
): Map<string, number> {
  const rank = new Map<string, number>();
  const walking = new Set<string>();
  const visit = (name: string): number => {
    const known = rank.get(name);
    if (known !== undefined) return known;
    if (walking.has(name)) return 0;
    walking.add(name);
    let best = 0;
    for (const from of up.get(name) ?? [])
      best = Math.max(best, visit(from) + 1);
    walking.delete(name);
    rank.set(name, best);
    return best;
  };
  for (const name of names) visit(name);
  return rank;
}

function statsOf(steps: readonly ApiStep[], depth: number): MiniStats {
  const calls: string[] = [];
  let conditional = 0;
  for (const step of steps) {
    if ((step.preconditions?.length ?? 0) > 0) conditional++;
    if (step.call && !calls.includes(step.call)) calls.push(step.call);
  }
  return { steps: steps.length, depth, conditional, calls };
}

// The order of the steps inside each rank: a sweep by the mean place of the
// steps before, which keeps the edges short.
function orderRanks(
  byRank: string[][],
  up: ReadonlyMap<string, readonly string[]>
): void {
  const place = new Map<string, number>();
  byRank.forEach((rank) => rank.forEach((name, i) => place.set(name, i)));
  for (let r = 1; r < byRank.length; r++) {
    const rank = byRank[r]!;
    const key = (name: string) => {
      const from = (up.get(name) ?? []).filter((n) => place.has(n));
      return from.length === 0
        ? (place.get(name) ?? 0)
        : from.reduce((sum, n) => sum + (place.get(n) ?? 0), 0) / from.length;
    };
    const keyed = rank.map((name, i) => ({ name, i, k: key(name) }));
    keyed.sort((a, b) => a.k - b.k || a.i - b.i);
    byRank[r] = keyed.map((item) => item.name);
    byRank[r]!.forEach((name, i) => place.set(name, i));
  }
}

interface Placed {
  nodes: MiniNode[];
  edges: MiniEdge[];
  width: number;
  height: number;
}

// The drawing of the ranks in one direction: the ranks run along the flow.
function place(
  steps: readonly ApiStep[],
  byRank: string[][],
  up: ReadonlyMap<string, readonly string[]>,
  direction: 'LR' | 'TB'
): Placed {
  const lr = direction === 'LR';
  // Along the flow a node is as long as its rank axis needs.
  const nodeAlong = lr ? NODE_LONG : NODE_SHORT;
  const nodeCross = lr ? NODE_SHORT : NODE_LONG;
  const pitchAlong = nodeAlong + GAP_ALONG;
  const pitchCross = nodeCross + GAP_CROSS;
  const widest = Math.max(1, ...byRank.map((rank) => rank.length));
  const crossSize = widest * pitchCross - GAP_CROSS;
  const alongSize = byRank.length * pitchAlong - GAP_ALONG;
  const byName = new Map(steps.map((step) => [step.name, step]));
  const nodes: MiniNode[] = [];
  const at = new Map<string, MiniNode>();
  byRank.forEach((rank, r) => {
    const span = rank.length * pitchCross - GAP_CROSS;
    const start = (crossSize - span) / 2;
    rank.forEach((name, i) => {
      const along = r * pitchAlong;
      const cross = start + i * pitchCross;
      const node: MiniNode = {
        name,
        x: lr ? along : cross,
        y: lr ? cross : along,
        width: lr ? nodeAlong : nodeCross,
        height: lr ? nodeCross : nodeAlong,
        ...flagsOf(byName.get(name)!),
      };
      nodes.push(node);
      at.set(name, node);
    });
  });
  const edges: MiniEdge[] = [];
  for (const node of nodes) {
    for (const from of up.get(node.name) ?? []) {
      const source = at.get(from);
      if (!source) continue;
      const sx = lr ? source.x + source.width : source.x + source.width / 2;
      const sy = lr ? source.y + source.height / 2 : source.y + source.height;
      const tx = lr ? node.x : node.x + node.width / 2;
      const ty = lr ? node.y + node.height / 2 : node.y;
      const mid = lr ? (sx + tx) / 2 : (sy + ty) / 2;
      edges.push({
        id: `${from}>${node.name}`,
        from,
        to: node.name,
        points: lr
          ? [
              [sx, sy],
              [mid, sy],
              [mid, ty],
              [tx, ty],
            ]
          : [
              [sx, sy],
              [sx, mid],
              [tx, mid],
              [tx, ty],
            ],
      });
    }
  }
  return {
    nodes,
    edges,
    width: lr ? alongSize : crossSize,
    height: lr ? crossSize : alongSize,
  };
}

/** The scale that fits a drawing of `width` x `height` units in the box. */
export function fitScale(width: number, height: number, box: MiniBox): number {
  if (width <= 0 || height <= 0) return 1;
  return Math.min(box.width / width, box.height / height);
}

/**
 * The miniature of a DAG, from the steps that the API describes: the steps
 * in ranks along the flow, with the edges between them. The flow runs left
 * to right, or top to bottom when that fits the box better (a wide DAG of few
 * ranks). A DAG with no step is `empty`. A DAG with more than `MINI_MAX_STEPS`
 * steps is a `summary`: one bar of the height of the steps for each rank. The
 * work is linear in the steps and never walks the DAGs that the steps call.
 */
export function buildMiniGraph(
  dag: Pick<ApiDAGDetails, 'steps'>,
  box: MiniBox,
  maxSteps: number = MINI_MAX_STEPS
): MiniGraph {
  const steps = dag.steps ?? [];
  if (steps.length === 0) return { kind: 'empty' };
  const up = upstreamOf(steps);
  const names = steps.map((step) => step.name);
  const rank = rankSteps(names, up);
  const depth = Math.max(0, ...rank.values()) + 1;
  const byRank: string[][] = Array.from({ length: depth }, () => []);
  for (const name of names) byRank[rank.get(name) ?? 0]!.push(name);
  const stats = statsOf(steps, depth);
  if (steps.length > maxSteps) {
    return { kind: 'summary', ranks: mergeRanks(byRank), stats };
  }
  orderRanks(byRank, up);
  const lr = place(steps, byRank, up, 'LR');
  const tb = place(steps, byRank, up, 'TB');
  const useLr =
    fitScale(lr.width, lr.height, box) >= fitScale(tb.width, tb.height, box);
  const chosen = useLr ? lr : tb;
  return {
    kind: 'graph',
    width: chosen.width,
    height: chosen.height,
    direction: useLr ? 'LR' : 'TB',
    nodes: chosen.nodes,
    edges: chosen.edges,
    stats,
  };
}

// The count of steps in each rank, merged into at most `MINI_MAX_BARS` bars.
function mergeRanks(byRank: string[][]): number[] {
  const counts = byRank.map((rank) => rank.length);
  if (counts.length <= MINI_MAX_BARS) return counts;
  const size = Math.ceil(counts.length / MINI_MAX_BARS);
  const merged: number[] = [];
  for (let i = 0; i < counts.length; i += size) {
    merged.push(Math.max(...counts.slice(i, i + size)));
  }
  return merged;
}

/** The SVG path of a curve of an edge, in the units of the graph. */
export function edgePath(edge: MiniEdge): string {
  const [a, b, c, d] = edge.points;
  return `M${a![0]} ${a![1]}C${b![0]} ${b![1]} ${c![0]} ${c![1]} ${d![0]} ${d![1]}`;
}
