// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/** A node to place, with the size of its card. */
export interface LayeredNode {
  key: string;
  width: number;
  height: number;
}

/** An edge from `source` to `target`, by node key. */
export interface LayeredEdge {
  source: string;
  target: string;
}

export interface LayeredInput {
  /** Nodes in their preferred order. When nothing else decides, this order applies. */
  nodes: LayeredNode[];
  /** Edges can make cycles. Self edges and edges to unknown keys do nothing. */
  edges: LayeredEdge[];
  /** 'LR': ranks go from left to right. 'TB': ranks go from top to bottom. */
  direction: 'LR' | 'TB';
  /** Gap between two nodes of a rank. */
  nodeSep: number;
  /** Gap between two ranks. */
  rankSep: number;
  /**
   * Extra gap after each rank, in px: index `r` widens the space between
   * rank `r` and rank `r + 1`. Ranks and orders do not change.
   */
  gapExtra?: readonly number[];
}

/** Top-left position of each node. */
export type LayeredPositions = Record<string, { x: number; y: number }>;

/**
 * The way of an edge that spans more than one rank. `points` are the
 * entry and the exit of the band of each rank that the edge crosses, in
 * the order from `source` to `target`. The edge runs along the flow
 * through the free column of each band, clear of the cards of the rank.
 */
export interface LayeredRoute {
  source: string;
  target: string;
  points: { x: number; y: number }[];
}

/** The rank of a node and the band that its rank takes along the flow. */
export interface LayeredBand {
  rank: number;
  start: number;
  end: number;
}

export interface LayeredResult {
  positions: LayeredPositions;
  routes: LayeredRoute[];
  /** By node key. The space between two bands holds no card. */
  bands: Record<string, LayeredBand>;
}

const ORDER_SWEEPS = 8;
const TRANSPOSE_PASSES = 4;
const POSITION_SWEEPS = 8;
// A long edge pushes nodes aside and does not give way, so that it stays
// straight.
const VIRTUAL_WEIGHT = 1e6;
// A node without neighbors in the fixed rank gives way to any push.
const FREE_WEIGHT = 1e-3;

type Pair = [number, number];

/**
 * Nodes and edges in ranks. Nodes from `real` on are virtual: they carry an
 * edge across a rank.
 */
interface Layers {
  real: number;
  /** The virtual nodes of each long edge, from `[source, target]` of the acyclic pair. */
  chains: { pair: Pair; virtuals: number[] }[];
  rank: number[];
  preds: number[][];
  succs: number[][];
  order: number[][];
}

/**
 * Layered (Sugiyama) layout. The rank of a node is its longest path from a
 * source; barycenter sweeps order each rank, and median sweeps give the
 * cross-axis position. The min x and the min y are 0. The same input always
 * gives the same positions.
 */
export function layeredLayout(input: LayeredInput): LayeredPositions {
  return layeredRoutes(input).positions;
}

/**
 * The layout of `layeredLayout`, and the way of each long edge: the points
 * where it enters and leaves the band of each rank that it crosses.
 */
export function layeredRoutes(input: LayeredInput): LayeredResult {
  const { nodes, nodeSep, rankSep } = input;
  if (nodes.length === 0) {
    return { positions: {}, routes: [], bands: {} };
  }
  const edgePairs = indexEdges(input);
  const pairs = acyclic(nodes.length, edgePairs);
  const layers = buildLayers(
    nodes.length,
    pairs,
    assignRanks(nodes.length, pairs)
  );
  orderLayers(layers);

  const horizontal = input.direction === 'LR';
  const crossSize = (v: number) => {
    const node = nodes[v];
    return node ? (horizontal ? node.height : node.width) : 0;
  };
  const rankSize = (v: number) => {
    const node = nodes[v];
    return node ? (horizontal ? node.width : node.height) : 0;
  };
  const center = crossCenters(layers, crossSize, nodeSep);

  // A rank is as thick as its largest node, and its nodes center on it.
  const thickness = layers.order.map((layer) =>
    layer.reduce((max, v) => Math.max(max, rankSize(v)), 0)
  );
  const starts: number[] = [];
  let start = 0;
  thickness.forEach((t, r) => {
    starts.push(start);
    start += t + rankSep + (input.gapExtra?.[r] ?? 0);
  });
  let minCross = Infinity;
  for (let v = 0; v < layers.real; v++) {
    minCross = Math.min(minCross, center[v]! - crossSize(v) / 2);
  }
  // No prototype: a step named `__proto__` keeps a key of its own.
  const positions: LayeredPositions = Object.create(null);
  const bands: Record<string, LayeredBand> = Object.create(null);
  nodes.forEach((node, v) => {
    const r = layers.rank[v]!;
    bands[node.key] = {
      rank: r,
      start: starts[r]!,
      end: starts[r]! + thickness[r]!,
    };
    const along = starts[r]! + (thickness[r]! - rankSize(v)) / 2;
    const across = center[v]! - crossSize(v) / 2 - minCross;
    positions[node.key] = horizontal
      ? { x: along, y: across }
      : { x: across, y: along };
  });

  const spot = (along: number, across: number) =>
    horizontal ? { x: along, y: across } : { x: across, y: along };
  const routes: LayeredRoute[] = [];
  const chainOf = new Map(
    layers.chains.map((chain) => [
      chain.pair[0] * nodes.length + chain.pair[1],
      chain,
    ])
  );
  for (const [s, t] of edgePairs) {
    // A back edge was reversed for the layout: its way runs the other way.
    const forward = chainOf.get(s * nodes.length + t);
    const chain = forward ?? chainOf.get(t * nodes.length + s);
    if (!chain || chain.virtuals.length === 0) {
      continue;
    }
    const points = chain.virtuals.flatMap((v) => {
      const r = layers.rank[v]!;
      const across = center[v]! - minCross;
      return [
        spot(starts[r]!, across),
        spot(starts[r]! + thickness[r]!, across),
      ];
    });
    if (!forward) {
      points.reverse();
    }
    routes.push({
      source: nodes[s]!.key,
      target: nodes[t]!.key,
      points,
    });
  }
  return { positions, routes, bands };
}

function uniquePairs(pairs: Pair[]): Pair[] {
  pairs.sort((a, b) => a[0] - b[0] || a[1] - b[1]);
  return pairs.filter((p, i) => {
    const prev = pairs[i - 1];
    return !prev || prev[0] !== p[0] || prev[1] !== p[1];
  });
}

/** Edges as sorted, unique pairs of node indexes, without self edges. */
function indexEdges(input: LayeredInput): Pair[] {
  const index = new Map<string, number>();
  input.nodes.forEach((node, i) => {
    if (!index.has(node.key)) {
      index.set(node.key, i);
    }
  });
  const pairs: Pair[] = [];
  for (const edge of input.edges) {
    const s = index.get(edge.source);
    const t = index.get(edge.target);
    if (s !== undefined && t !== undefined && s !== t) {
      pairs.push([s, t]);
    }
  }
  return uniquePairs(pairs);
}

/**
 * Reverse the back edges of a depth-first search that starts from the nodes
 * in input order. The result has no cycle.
 */
function acyclic(n: number, pairs: Pair[]): Pair[] {
  const out: number[][] = Array.from({ length: n }, () => []);
  for (const [s, t] of pairs) {
    out[s]!.push(t);
  }
  // 0: not seen, 1: on the stack, 2: done.
  const state = new Uint8Array(n);
  const back = new Set<number>();
  for (let root = 0; root < n; root++) {
    if (state[root] !== 0) {
      continue;
    }
    state[root] = 1;
    const stack: Pair[] = [[root, 0]];
    while (stack.length > 0) {
      const top = stack[stack.length - 1]!;
      const [v, i] = top;
      const targets = out[v]!;
      if (i === targets.length) {
        state[v] = 2;
        stack.pop();
        continue;
      }
      top[1] = i + 1;
      const t = targets[i]!;
      if (state[t] === 1) {
        back.add(v * n + t);
      } else if (state[t] === 0) {
        state[t] = 1;
        stack.push([t, 0]);
      }
    }
  }
  return uniquePairs(
    pairs.map(([s, t]): Pair => (back.has(s * n + t) ? [t, s] : [s, t]))
  );
}

/**
 * Longest-path ranks. Then a node with more children than parents moves next
 * to its nearest child, and ranks without a node close up.
 */
function assignRanks(n: number, pairs: Pair[]): number[] {
  const preds: number[][] = Array.from({ length: n }, () => []);
  const succs: number[][] = Array.from({ length: n }, () => []);
  const indegree = new Int32Array(n);
  for (const [s, t] of pairs) {
    succs[s]!.push(t);
    preds[t]!.push(s);
    indegree[t]!++;
  }
  const topo: number[] = [];
  for (let v = 0; v < n; v++) {
    if (indegree[v] === 0) {
      topo.push(v);
    }
  }
  for (let head = 0; head < topo.length; head++) {
    for (const t of succs[topo[head]!]!) {
      if (--indegree[t]! === 0) {
        topo.push(t);
      }
    }
  }
  const rank = new Array<number>(n).fill(0);
  for (const v of topo) {
    for (const t of succs[v]!) {
      rank[t] = Math.max(rank[t]!, rank[v]! + 1);
    }
  }
  for (let i = topo.length - 1; i >= 0; i--) {
    const v = topo[i]!;
    const children = succs[v]!;
    if (children.length > preds[v]!.length) {
      const nearest = Math.min(...children.map((t) => rank[t]!));
      rank[v] = Math.max(rank[v]!, nearest - 1);
    }
  }
  const used = [...new Set(rank)].sort((a, b) => a - b);
  const compact = new Map(used.map((r, i) => [r, i]));
  return rank.map((r) => compact.get(r) ?? 0);
}

/**
 * Split each long edge with one virtual node in each rank that it crosses.
 * The first order of a rank is the input order; a virtual node goes at the
 * place of the node that its edge enters.
 */
function buildLayers(n: number, pairs: Pair[], rank: number[]): Layers {
  const layers: Layers = {
    real: n,
    chains: [],
    rank: [...rank],
    preds: Array.from({ length: n }, () => []),
    succs: Array.from({ length: n }, () => []),
    order: [],
  };
  const seeds: Pair[] = rank.map((_, v) => [v, -1]);
  const link = (s: number, t: number) => {
    layers.succs[s]!.push(t);
    layers.preds[t]!.push(s);
  };
  for (const [s, t] of pairs) {
    let prev = s;
    const virtuals: number[] = [];
    layers.chains.push({ pair: [s, t], virtuals });
    for (let r = rank[s]! + 1; r < rank[t]!; r++) {
      const v = layers.rank.length;
      virtuals.push(v);
      layers.rank.push(r);
      layers.preds.push([]);
      layers.succs.push([]);
      seeds.push([t, s]);
      link(prev, v);
      prev = v;
    }
    link(prev, t);
  }
  const ranks = Math.max(...rank) + 1;
  layers.order = Array.from({ length: ranks }, () => []);
  layers.rank.forEach((r, v) => layers.order[r]!.push(v));
  for (const layer of layers.order) {
    layer.sort((a, b) => {
      const sa = seeds[a]!;
      const sb = seeds[b]!;
      return sa[0] - sb[0] || sa[1] - sb[1];
    });
  }
  return layers;
}

function positionsOf(layers: Layers): Int32Array {
  const pos = new Int32Array(layers.rank.length);
  for (const layer of layers.order) {
    layer.forEach((v, i) => {
      pos[v] = i;
    });
  }
  return pos;
}

/**
 * Barycenter sweeps down and up, each followed by swaps of neighbor nodes.
 * The order with the fewest crossings wins; on a tie the earlier one wins,
 * so the input order stays when no sweep improves it.
 */
function orderLayers(layers: Layers): void {
  const pos = positionsOf(layers);
  let best = layers.order.map((layer) => [...layer]);
  let fewest = crossings(layers, pos);
  for (let i = 0; i < ORDER_SWEEPS && fewest > 0; i++) {
    sweep(layers, pos, i % 2 === 0);
    transpose(layers, pos);
    const count = crossings(layers, pos);
    if (count < fewest) {
      fewest = count;
      best = layers.order.map((layer) => [...layer]);
    }
  }
  layers.order = best;
}

/**
 * Sort each rank by the mean position of its neighbors in the rank before it
 * (down) or after it (up). A node without such neighbors keeps its place.
 */
function sweep(layers: Layers, pos: Int32Array, down: boolean): void {
  const fixed = down ? layers.preds : layers.succs;
  const count = layers.order.length;
  for (let k = 1; k < count; k++) {
    const r = down ? k : count - 1 - k;
    const layer = layers.order[r]!;
    const movable: { v: number; at: number; i: number }[] = [];
    layer.forEach((v, i) => {
      const neighbors = fixed[v]!;
      if (neighbors.length > 0) {
        let sum = 0;
        for (const u of neighbors) {
          sum += pos[u]!;
        }
        movable.push({ v, at: sum / neighbors.length, i });
      }
    });
    movable.sort((a, b) => a.at - b.at || a.i - b.i);
    let next = 0;
    const sorted = layer.map((v) =>
      fixed[v]!.length > 0 ? movable[next++]!.v : v
    );
    sorted.forEach((v, i) => {
      pos[v] = i;
    });
    layers.order[r] = sorted;
  }
}

/** Crossings between `u` and `v` when `u` is before `v`. */
function pairCrossings(
  layers: Layers,
  pos: Int32Array,
  u: number,
  v: number
): number {
  let count = 0;
  for (const side of [layers.preds, layers.succs]) {
    for (const a of side[u]!) {
      for (const b of side[v]!) {
        if (pos[a]! > pos[b]!) {
          count++;
        }
      }
    }
  }
  return count;
}

/** Swap two neighbor nodes of a rank when the swap removes crossings. */
function transpose(layers: Layers, pos: Int32Array): void {
  for (let pass = 0; pass < TRANSPOSE_PASSES; pass++) {
    let improved = false;
    for (const layer of layers.order) {
      for (let i = 0; i + 1 < layer.length; i++) {
        const u = layer[i]!;
        const v = layer[i + 1]!;
        if (
          pairCrossings(layers, pos, v, u) < pairCrossings(layers, pos, u, v)
        ) {
          layer[i] = v;
          layer[i + 1] = u;
          pos[v] = i;
          pos[u] = i + 1;
          improved = true;
        }
      }
    }
    if (!improved) {
      return;
    }
  }
}

/** Edge crossings between all ranks, counted with a Fenwick tree. */
function crossings(layers: Layers, pos: Int32Array): number {
  let total = 0;
  for (let r = 0; r + 1 < layers.order.length; r++) {
    const size = layers.order[r + 1]!.length;
    const tree = new Int32Array(size + 1);
    let inserted = 0;
    for (const u of layers.order[r]!) {
      const ends = layers.succs[u]!.map((t) => pos[t]!).sort((a, b) => a - b);
      for (const end of ends) {
        let atOrBefore = 0;
        for (let i = end + 1; i > 0; i -= i & -i) {
          atOrBefore += tree[i]!;
        }
        total += inserted - atOrBefore;
        for (let i = end + 1; i <= size; i += i & -i) {
          tree[i]!++;
        }
        inserted++;
      }
    }
  }
  return total;
}

function median(values: number[]): number {
  values.sort((a, b) => a - b);
  const mid = values.length >> 1;
  return values.length % 2 === 1
    ? values[mid]!
    : (values[mid - 1]! + values[mid]!) / 2;
}

/**
 * Cross-axis center of each node. Each rank starts packed and centered on 0.
 * Sweeps down and up then move each node toward the median of its neighbors
 * in the fixed rank. A weighted least-squares fit under the order and gap
 * constraints of the rank resolves overlaps (pool adjacent violators).
 */
function crossCenters(
  layers: Layers,
  size: (v: number) => number,
  nodeSep: number
): Float64Array {
  const margin = (v: number) => (v < layers.real ? nodeSep / 2 : nodeSep / 8);
  const gap = (a: number, b: number) =>
    (size(a) + size(b)) / 2 + margin(a) + margin(b);
  const x = new Float64Array(layers.rank.length);
  for (const layer of layers.order) {
    let at = 0;
    layer.forEach((v, i) => {
      if (i > 0) {
        at += gap(layer[i - 1]!, v);
      }
      x[v] = at;
    });
    for (const v of layer) {
      x[v] = x[v]! - at / 2;
    }
  }

  const isVirtual = (u: number) => u >= layers.real;

  const place = (layer: number[], down: boolean) => {
    const fixed = down ? layers.preds : layers.succs;
    const m = layer.length;
    // With y = x - offset, the gap constraints become y nondecreasing.
    const offset = new Float64Array(m);
    const target = new Float64Array(m);
    const weight = new Float64Array(m);
    layer.forEach((v, i) => {
      offset[i] = i > 0 ? offset[i - 1]! + gap(layer[i - 1]!, v) : 0;
      const neighbors = fixed[v]!;
      // A real node follows its real neighbors: a long edge leaves it with a
      // curve and does not pull it into its column. A virtual node follows
      // the edge that it carries and pushes the cards aside.
      let pull = neighbors;
      if (!isVirtual(v)) {
        const real = neighbors.filter((u) => !isVirtual(u));
        pull = real.length > 0 ? real : neighbors;
      }
      if (pull.length === 0) {
        target[i] = x[v]! - offset[i]!;
        weight[i] = FREE_WEIGHT;
      } else {
        target[i] = median(pull.map((u) => x[u]!)) - offset[i]!;
        weight[i] = isVirtual(v) ? VIRTUAL_WEIGHT : pull.length;
      }
    });
    const blocks: { sum: number; weight: number; start: number }[] = [];
    for (let i = 0; i < m; i++) {
      let block = {
        sum: target[i]! * weight[i]!,
        weight: weight[i]!,
        start: i,
      };
      let prev = blocks[blocks.length - 1];
      while (prev && prev.sum / prev.weight > block.sum / block.weight) {
        blocks.pop();
        block = {
          sum: prev.sum + block.sum,
          weight: prev.weight + block.weight,
          start: prev.start,
        };
        prev = blocks[blocks.length - 1];
      }
      blocks.push(block);
    }
    blocks.forEach((block, b) => {
      const end = blocks[b + 1]?.start ?? m;
      const y = block.sum / block.weight;
      for (let i = block.start; i < end; i++) {
        x[layer[i]!] = y + offset[i]!;
      }
    });
  };

  const count = layers.order.length;
  for (let s = 0; s < POSITION_SWEEPS; s++) {
    const down = s % 2 === 0;
    for (let k = 1; k < count; k++) {
      const r = down ? k : count - 1 - k;
      place(layers.order[r]!, down);
    }
  }
  return x;
}
