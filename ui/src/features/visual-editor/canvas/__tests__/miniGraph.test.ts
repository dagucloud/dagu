// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { ApiStep } from '../../contracts';
import {
  MINI_MAX_BARS,
  MINI_MAX_STEPS,
  buildMiniGraph,
  edgePath,
  fitScale,
  rankSteps,
  type MiniGraph,
} from '../miniGraph';

const BOX = { width: 232, height: 44 };

function step(name: string, depends: string[] = [], over = {}): ApiStep {
  return { name, depends, ...over } as ApiStep;
}

function graphOf(steps: ApiStep[], max?: number): MiniGraph {
  return buildMiniGraph({ steps }, BOX, max);
}

function asGraph(mini: MiniGraph) {
  if (mini.kind !== 'graph') throw new Error(`expected a graph: ${mini.kind}`);
  return mini;
}

describe('buildMiniGraph', () => {
  it('has nothing to draw for a DAG with no step', () => {
    expect(buildMiniGraph({ steps: [] }, BOX)).toEqual({ kind: 'empty' });
    expect(buildMiniGraph({}, BOX)).toEqual({ kind: 'empty' });
  });

  it('draws one card for one step, with no edge', () => {
    const mini = asGraph(graphOf([step('only')]));
    expect(mini.nodes).toHaveLength(1);
    expect(mini.edges).toHaveLength(0);
    expect(mini.stats).toMatchObject({ steps: 1, depth: 1, conditional: 0 });
  });

  it('puts each step one rank after the last of its dependencies', () => {
    const steps = [
      step('a'),
      step('b', ['a']),
      step('c', ['a']),
      step('d', ['b', 'c']),
      step('e', ['d']),
    ];
    const ranks = rankSteps(
      steps.map((item) => item.name),
      new Map([
        ['a', []],
        ['b', ['a']],
        ['c', ['a']],
        ['d', ['b', 'c']],
        ['e', ['d']],
      ])
    );
    expect(Object.fromEntries(ranks)).toEqual({ a: 0, b: 1, c: 1, d: 2, e: 3 });
    const mini = asGraph(graphOf(steps));
    expect(mini.stats.depth).toBe(4);
    expect(mini.nodes.map((node) => node.name).sort()).toEqual([
      'a',
      'b',
      'c',
      'd',
      'e',
    ]);
    // Five edges: a>b, a>c, b>d, c>d, d>e.
    expect(mini.edges.map((edge) => edge.id).sort()).toEqual([
      'a>b',
      'a>c',
      'b>d',
      'c>d',
      'd>e',
    ]);
  });

  it('reads the inferred dependencies and the routes of a router as edges', () => {
    const mini = asGraph(
      graphOf([
        step('pick', [], {
          router: {
            value: '${X}',
            routes: [{ pattern: 'a', targets: ['left', 'missing'] }],
          },
        }),
        step('left'),
        step('after', [], { inferredDepends: ['left'] }),
      ])
    );
    expect(mini.edges.map((edge) => edge.id).sort()).toEqual([
      'left>after',
      'pick>left',
    ]);
    expect(mini.nodes.find((node) => node.name === 'pick')?.router).toBe(true);
  });

  it('flags the steps that call a DAG, have a condition, fan out or route', () => {
    const mini = asGraph(
      graphOf([
        step('call', [], { call: 'other' }),
        step('maybe', ['call'], { preconditions: [{ condition: 'x' }] }),
        step('each', ['maybe'], { parallel: { items: ['a'] } }),
      ])
    );
    const flags = Object.fromEntries(
      mini.nodes.map((node) => [
        node.name,
        [node.call, node.conditional, node.parallel],
      ])
    );
    expect(flags).toEqual({
      call: [true, false, false],
      maybe: [false, true, false],
      each: [false, false, true],
    });
    expect(mini.stats).toMatchObject({ conditional: 1, calls: ['other'] });
  });

  it('lists each called DAG once, in the order of the steps', () => {
    const mini = asGraph(
      graphOf([
        step('a', [], { call: 'x' }),
        step('b', ['a'], { call: 'y' }),
        step('c', ['b'], { call: 'x' }),
      ])
    );
    expect(mini.stats.calls).toEqual(['x', 'y']);
  });

  it('runs a long chain left to right, and a wide fan top to bottom, to fit the box', () => {
    const chain = Array.from({ length: 6 }, (_, i) =>
      step(`s${i}`, i === 0 ? [] : [`s${i - 1}`])
    );
    expect(asGraph(graphOf(chain)).direction).toBe('LR');
    const fan = Array.from({ length: 6 }, (_, i) => step(`p${i}`));
    expect(asGraph(graphOf(fan)).direction).toBe('TB');
  });

  it('keeps every card inside the drawing and apart from the others', () => {
    const mini = asGraph(
      graphOf([
        step('a'),
        step('b', ['a']),
        step('c', ['a']),
        step('d', ['b', 'c']),
      ])
    );
    for (const node of mini.nodes) {
      expect(node.x).toBeGreaterThanOrEqual(0);
      expect(node.y).toBeGreaterThanOrEqual(0);
      expect(node.x + node.width).toBeLessThanOrEqual(mini.width + 1e-9);
      expect(node.y + node.height).toBeLessThanOrEqual(mini.height + 1e-9);
    }
    mini.nodes.forEach((a, i) =>
      mini.nodes.slice(i + 1).forEach((b) => {
        const apart =
          a.x + a.width <= b.x ||
          b.x + b.width <= a.x ||
          a.y + a.height <= b.y ||
          b.y + b.height <= a.y;
        expect(apart, `${a.name} and ${b.name}`).toBe(true);
      })
    );
  });

  it('never loops on a cycle, which the server refuses', () => {
    const mini = asGraph(graphOf([step('a', ['b']), step('b', ['a'])]));
    expect(mini.nodes).toHaveLength(2);
    // An unknown dependency and a dependency on itself draw no edge.
    const odd = asGraph(graphOf([step('x', ['x', 'nowhere'])]));
    expect(odd.edges).toHaveLength(0);
  });

  it('makes a summary of one bar for each rank above the step limit', () => {
    const steps = Array.from({ length: MINI_MAX_STEPS + 1 }, (_, i) =>
      step(`s${i}`, i === 0 ? [] : [`s${Math.floor((i - 1) / 3)}`])
    );
    const mini = graphOf(steps);
    expect(mini.kind).toBe('summary');
    if (mini.kind !== 'summary') return;
    expect(mini.stats.steps).toBe(MINI_MAX_STEPS + 1);
    expect(mini.ranks.reduce((sum, n) => sum + n, 0)).toBe(MINI_MAX_STEPS + 1);
    expect(mini.ranks.length).toBe(mini.stats.depth);
  });

  it('merges the ranks of a very long chain into a bounded number of bars', () => {
    const steps = Array.from({ length: 100 }, (_, i) =>
      step(`s${i}`, i === 0 ? [] : [`s${i - 1}`])
    );
    const mini = graphOf(steps);
    expect(mini.kind).toBe('summary');
    if (mini.kind !== 'summary') return;
    expect(mini.stats.depth).toBe(100);
    expect(mini.ranks.length).toBeLessThanOrEqual(MINI_MAX_BARS);
  });

  it('does its work in a short time for a hundred steps', () => {
    const steps = Array.from({ length: 100 }, (_, i) =>
      step(`s${i}`, i < 10 ? [] : [`s${i - 10}`, `s${i - 9}`])
    );
    const t0 = performance.now();
    graphOf(steps);
    expect(performance.now() - t0).toBeLessThan(200);
  });
});

describe('the drawing helpers', () => {
  it('fits the drawing to the box by the tighter side', () => {
    expect(fitScale(10, 1, { width: 100, height: 50 })).toBe(10);
    expect(fitScale(1, 10, { width: 100, height: 50 })).toBe(5);
    expect(fitScale(0, 0, { width: 100, height: 50 })).toBe(1);
  });

  it('writes the curve of an edge as an SVG path', () => {
    expect(
      edgePath({
        id: 'a>b',
        from: 'a',
        to: 'b',
        points: [
          [1, 2],
          [3, 2],
          [3, 4],
          [5, 4],
        ],
      })
    ).toBe('M1 2C3 2 3 4 5 4');
  });
});
