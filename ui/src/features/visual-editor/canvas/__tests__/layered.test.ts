// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  layeredLayout,
  layeredRoutes,
  type LayeredEdge,
  type LayeredInput,
  type LayeredNode,
  type LayeredPositions,
} from '../layered';

const W = 100;
const H = 40;
const NODE_SEP = 10;
const RANK_SEP = 20;

/** Input from node keys and `source>target` pairs. Keys are `key` or `key:h`. */
function inputOf(
  keys: string[],
  pairs: string[] = [],
  direction: LayeredInput['direction'] = 'LR'
): LayeredInput {
  const nodes: LayeredNode[] = keys.map((spec) => {
    const [key = '', height] = spec.split(':');
    return { key, width: W, height: height ? Number(height) : H };
  });
  const edges: LayeredEdge[] = pairs.map((pair) => {
    const [source = '', target = ''] = pair.split('>');
    return { source, target };
  });
  return { nodes, edges, direction, nodeSep: NODE_SEP, rankSep: RANK_SEP };
}

interface Box {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

function boxes(input: LayeredInput, positions: LayeredPositions) {
  const result = new Map<string, Box>();
  for (const node of input.nodes) {
    const p = positions[node.key];
    if (!p) {
      throw new Error(`no position for ${node.key}`);
    }
    result.set(node.key, {
      left: p.x,
      top: p.y,
      right: p.x + node.width,
      bottom: p.y + node.height,
    });
  }
  return result;
}

function box(input: LayeredInput, positions: LayeredPositions, key: string) {
  const found = boxes(input, positions).get(key);
  if (!found) {
    throw new Error(`no box for ${key}`);
  }
  return found;
}

/** Center on the cross axis: y in 'LR', x in 'TB'. */
function cross(input: LayeredInput, positions: LayeredPositions, key: string) {
  const b = box(input, positions, key);
  return input.direction === 'LR'
    ? (b.top + b.bottom) / 2
    : (b.left + b.right) / 2;
}

function expectNoOverlap(input: LayeredInput, positions: LayeredPositions) {
  const list = [...boxes(input, positions).entries()];
  for (let i = 0; i < list.length; i++) {
    for (let j = i + 1; j < list.length; j++) {
      const [ka, a] = list[i]!;
      const [kb, b] = list[j]!;
      const hit =
        a.left < b.right &&
        b.left < a.right &&
        a.top < b.bottom &&
        b.top < a.bottom;
      expect(hit, `${ka} overlaps ${kb}`).toBe(false);
    }
  }
}

// Small seeded generator, so that the random graphs are the same each run.
function mulberry32(seed: number) {
  let a = seed;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Random graph. Edges go from a lower to a higher index unless `cycles`. */
function randomInput(
  seed: number,
  size: number,
  direction: LayeredInput['direction'],
  cycles = false
): LayeredInput {
  const random = mulberry32(seed);
  const keys = Array.from({ length: size }, (_, i) => {
    const tall = random() < 0.2;
    return tall ? `n${i}:${H + Math.floor(random() * 4) * 20}` : `n${i}`;
  });
  const pairs: string[] = [];
  for (let t = 1; t < size; t++) {
    const parents = 1 + Math.floor(random() * 2);
    for (let k = 0; k < parents; k++) {
      const s = Math.floor(random() * t);
      pairs.push(`n${s}>n${t}`);
    }
    if (cycles && random() < 0.1) {
      pairs.push(`n${t}>n${Math.floor(random() * t)}`);
    }
  }
  return inputOf(keys, pairs, direction);
}

describe('layeredLayout ranks', () => {
  it('gives nothing for no node', () => {
    expect(layeredLayout(inputOf([]))).toEqual({});
  });

  it('places a chain on one line', () => {
    const lr = layeredLayout(inputOf(['a', 'b', 'c'], ['a>b', 'b>c']));
    expect(lr).toEqual({
      a: { x: 0, y: 0 },
      b: { x: W + RANK_SEP, y: 0 },
      c: { x: 2 * (W + RANK_SEP), y: 0 },
    });
    const tb = layeredLayout(inputOf(['a', 'b', 'c'], ['a>b', 'b>c'], 'TB'));
    expect(tb).toEqual({
      a: { x: 0, y: 0 },
      b: { x: 0, y: H + RANK_SEP },
      c: { x: 0, y: 2 * (H + RANK_SEP) },
    });
  });

  it('survives cycles', () => {
    const input = inputOf(['a', 'b', 'c'], ['a>b', 'b>c', 'c>a']);
    const positions = layeredLayout(input);
    // The edge back to the first node is the one that turns.
    expect(positions.a?.x).toBe(0);
    expect(positions.b?.x).toBe(W + RANK_SEP);
    expect(positions.c?.x).toBe(2 * (W + RANK_SEP));
    const two = inputOf(['a', 'b'], ['a>b', 'b>a', 'a>a']);
    expect(layeredLayout(two)).toEqual({
      a: { x: 0, y: 0 },
      b: { x: W + RANK_SEP, y: 0 },
    });
  });

  it('ignores self edges and unknown keys', () => {
    const input = inputOf(['a', 'b'], ['a>a', 'a>x', 'x>b', 'a>b']);
    expect(layeredLayout(input)).toEqual(
      layeredLayout(inputOf(['a', 'b'], ['a>b']))
    );
  });

  it('moves a source next to its child', () => {
    // `a` feeds only `d`, so it goes to the rank of `c`, not to rank 0.
    const input = inputOf(['a', 'b', 'c', 'd'], ['b>c', 'c>d', 'a>d']);
    const positions = layeredLayout(input);
    expect(positions.a?.x).toBe(positions.c?.x);
    expect(positions.b?.x).toBe(0);
    expectNoOverlap(input, positions);
  });

  it('centers smaller nodes in a thick rank', () => {
    const input = inputOf(['r', 'a:100', 'b'], ['r>a', 'r>b'], 'TB');
    const positions = layeredLayout(input);
    expect(positions.a?.y).toBe(H + RANK_SEP);
    expect(positions.b?.y).toBe(H + RANK_SEP + 30);
  });
});

describe('layeredLayout order', () => {
  it('keeps siblings in input order', () => {
    const input = inputOf(['r', 'x', 'y', 'z'], ['r>x', 'r>y', 'r>z']);
    const positions = layeredLayout(input);
    expect(positions.x?.y).toBe(0);
    expect(positions.y?.y).toBe(H + NODE_SEP);
    expect(positions.z?.y).toBe(2 * (H + NODE_SEP));
    // The parent faces its middle child.
    expect(positions.r?.y).toBe(positions.y?.y);

    const reversed = inputOf(['r', 'z', 'y', 'x'], ['r>x', 'r>y', 'r>z']);
    const other = layeredLayout(reversed);
    expect(other.z?.y).toBeLessThan(other.y?.y ?? 0);
    expect(other.y?.y).toBeLessThan(other.x?.y ?? 0);
  });

  it('centers a fan-in on its parents', () => {
    const input = inputOf(['x', 'y', 'z', 'm'], ['x>m', 'y>m', 'z>m']);
    const positions = layeredLayout(input);
    expect(positions.x?.y).toBeLessThan(positions.y?.y ?? 0);
    expect(positions.y?.y).toBeLessThan(positions.z?.y ?? 0);
    expect(cross(input, positions, 'm')).toBe(cross(input, positions, 'y'));
  });

  it('draws a diamond symmetric', () => {
    const input = inputOf(
      ['a', 'b', 'c', 'd'],
      ['a>b', 'a>c', 'b>d', 'c>d'],
      'TB'
    );
    const positions = layeredLayout(input);
    expect(positions.b?.x).toBeLessThan(positions.c?.x ?? 0);
    const middle =
      (cross(input, positions, 'b') + cross(input, positions, 'c')) / 2;
    expect(cross(input, positions, 'a')).toBe(middle);
    expect(cross(input, positions, 'd')).toBe(middle);
  });

  it('reorders siblings to remove a crossing', () => {
    // In input order, the edges a>d and b>c cross.
    const input = inputOf(['a', 'b', 'c', 'd'], ['a>d', 'b>c']);
    const positions = layeredLayout(input);
    expect(positions.a?.y).toBeLessThan(positions.b?.y ?? 0);
    expect(positions.d?.y).toBeLessThan(positions.c?.y ?? 0);
  });

  // The edge runs through the free column of each rank that it crosses: the
  // way that the layout gives is clear of every card.
  it('keeps a long edge clear of the nodes it passes', () => {
    const input = inputOf(['a', 'b', 'c', 'd'], ['a>b', 'b>c', 'c>d', 'a>d']);
    const { positions, routes } = layeredRoutes(input);
    const route = routes.find((r) => r.source === 'a' && r.target === 'd');
    // One entry and one exit for each of the two ranks between a and d.
    expect(route?.points).toHaveLength(4);
    for (let i = 0; i + 1 < (route?.points.length ?? 0); i += 2) {
      const entry = route!.points[i]!;
      const exit = route!.points[i + 1]!;
      const middle = { x: (entry.x + exit.x) / 2, y: (entry.y + exit.y) / 2 };
      for (const key of ['b', 'c']) {
        const b = box(input, positions, key);
        for (const p of [entry, exit, middle]) {
          const inside =
            p.x > b.left && p.x < b.right && p.y > b.top && p.y < b.bottom;
          expect(inside, `a>d passes through ${key}`).toBe(false);
        }
      }
    }
  });

  it('gives no way to an edge between two ranks in a row', () => {
    const { routes } = layeredRoutes(inputOf(['a', 'b'], ['a>b']));
    expect(routes).toEqual([]);
  });

  it('gives the way of a back edge from its source to its target', () => {
    const input = inputOf(['a', 'b', 'c', 'd'], ['a>b', 'b>c', 'c>d', 'd>a']);
    const { routes } = layeredRoutes(input);
    const back = routes.find((r) => r.source === 'd' && r.target === 'a');
    expect(back?.points).toHaveLength(4);
    // It runs from d, the last rank, back up to a.
    expect(back!.points[0]!.x).toBeGreaterThan(back!.points[3]!.x);
  });

  it('places disconnected nodes in one rank without overlap', () => {
    const input = inputOf(['a', 'b:80', 'c']);
    const positions = layeredLayout(input);
    expect(positions.a).toEqual({ x: 0, y: 0 });
    expect(positions.b).toEqual({ x: 0, y: H + NODE_SEP });
    expect(positions.c).toEqual({ x: 0, y: H + 80 + 2 * NODE_SEP });
  });
});

describe('layeredLayout properties', () => {
  it('never overlaps nodes and keeps edges forward', () => {
    for (let seed = 1; seed <= 40; seed++) {
      for (const direction of ['LR', 'TB'] as const) {
        const input = randomInput(seed, 5 + (seed % 30), direction);
        const positions = layeredLayout(input);
        expectNoOverlap(input, positions);
        const all = boxes(input, positions);
        for (const edge of input.edges) {
          const s = all.get(edge.source)!;
          const t = all.get(edge.target)!;
          if (direction === 'LR') {
            expect(t.left).toBeGreaterThanOrEqual(s.right + RANK_SEP);
          } else {
            expect(t.top).toBeGreaterThanOrEqual(s.bottom + RANK_SEP);
          }
        }
      }
    }
  });

  it('never overlaps nodes with cycles', () => {
    for (let seed = 1; seed <= 20; seed++) {
      const input = randomInput(seed, 30, 'TB', true);
      expectNoOverlap(input, layeredLayout(input));
    }
  });

  it('starts at the origin', () => {
    for (let seed = 1; seed <= 10; seed++) {
      const positions = Object.values(
        layeredLayout(randomInput(seed, 20, 'LR'))
      );
      expect(Math.min(...positions.map((p) => p.x))).toBe(0);
      expect(Math.min(...positions.map((p) => p.y))).toBe(0);
    }
  });

  it('gives the same positions for the same input', () => {
    const input = randomInput(7, 40, 'LR', true);
    const first = layeredLayout(input);
    expect(layeredLayout(input)).toEqual(first);
    // The order of the edges does not count.
    const shuffled = { ...input, edges: [...input.edges].reverse() };
    expect(layeredLayout(shuffled)).toEqual(first);
  });

  it('gives the same shape in LR and TB', () => {
    const lr = randomInput(3, 25, 'LR');
    const tb: LayeredInput = {
      ...lr,
      direction: 'TB',
      nodes: lr.nodes.map((n) => ({ ...n, width: n.height, height: n.width })),
    };
    const a = layeredLayout(lr);
    const b = layeredLayout(tb);
    for (const node of lr.nodes) {
      expect(b[node.key]).toEqual({ x: a[node.key]?.y, y: a[node.key]?.x });
    }
  });

  it('lays out 300 nodes fast', () => {
    const input = randomInput(11, 300, 'LR', true);
    layeredLayout(input);
    const start = performance.now();
    layeredLayout(input);
    expect(performance.now() - start).toBeLessThan(400);
  });
});
