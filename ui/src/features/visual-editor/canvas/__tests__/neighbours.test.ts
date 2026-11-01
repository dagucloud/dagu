// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type {
  CanvasDirection,
  CanvasEdge,
  CanvasGraph,
  CanvasNode,
} from '../../contracts';
import {
  agentNeighbour,
  neighbourStep,
  type NavDirection,
} from '../neighbours';

function node(key: string, lane: CanvasNode['lane'] = 'main'): CanvasNode {
  return {
    key,
    label: key,
    kind: 'run',
    lane,
    issues: { errors: 0, warnings: 0 },
  };
}

function edge(source: string, target: string): CanvasEdge {
  return { id: `${source}->${target}`, source, target, kind: 'depends' };
}

// a -> b -> d, a -> c -> d, a second root e, and a handler h.
const graph: CanvasGraph = {
  nodes: ['a', 'b', 'c', 'd', 'e']
    .map((key) => node(key))
    .concat(node('h', 'handler')),
  edges: [edge('a', 'b'), edge('a', 'c'), edge('b', 'd'), edge('c', 'd')],
  editableEdges: true,
};

// Each row: selected step, then the answer for up, down, left, right.
type Row = [string, string | null, string | null, string | null, string | null];

const TB: Row[] = [
  ['a', null, 'b', null, 'e'],
  ['b', 'a', 'd', null, 'c'],
  ['c', 'a', 'd', 'b', null],
  ['d', 'b', null, null, null],
  ['e', null, null, 'a', null],
  ['h', null, null, null, null],
];

const LR: Row[] = [
  ['a', null, 'e', null, 'b'],
  ['b', null, 'c', 'a', 'd'],
  ['c', 'b', null, 'a', 'd'],
  ['d', null, null, 'b', null],
  ['e', 'a', null, null, null],
  ['h', null, null, null, null],
];

const DIRS: NavDirection[] = ['up', 'down', 'left', 'right'];

function table(direction: CanvasDirection, rows: Row[]) {
  return rows.flatMap(([selected, ...answers]) =>
    DIRS.map(
      (dir, i) => [direction, selected, dir, answers[i] ?? null] as const
    )
  );
}

// parity:canvas-keymap
describe('neighbourStep', () => {
  it.each([...table('TB', TB), ...table('LR', LR)])(
    '%s: from %s, %s gives %s',
    (direction, selected, dir, expected) => {
      expect(neighbourStep(graph, selected, dir, direction)).toBe(expected);
    }
  );

  it('starts at the first root without a selection', () => {
    expect(neighbourStep(graph, null, 'down', 'TB')).toBe('a');
    expect(neighbourStep(graph, null, 'left', 'LR')).toBe('a');
  });

  it('gives null for an unknown step or an empty graph', () => {
    expect(neighbourStep(graph, 'zz', 'down', 'TB')).toBeNull();
    expect(
      neighbourStep(
        { nodes: [], edges: [], editableEdges: true },
        null,
        'down',
        'TB'
      )
    ).toBeNull();
  });
});

describe('agentNeighbour', () => {
  // The hub and seven tools in a grid of three columns: t0 t1 t2 / t3 t4 t5 / t6.
  const agent: CanvasGraph = {
    nodes: [
      { ...node('agent'), kind: 'agent' },
      ...Array.from({ length: 7 }, (_, i) => node(`t${i}`)),
      node('h', 'handler'),
    ],
    edges: [],
    editableEdges: false,
  };
  const go = (from: string | null, dir: NavDirection) =>
    agentNeighbour(agent, from, dir, 3);

  it('starts at the hub', () => {
    expect(go(null, 'down')).toBe('agent');
  });

  it('goes from the hub to the first tool, and nowhere else', () => {
    expect(go('agent', 'down')).toBe('t0');
    expect(go('agent', 'up')).toBeNull();
    expect(go('agent', 'left')).toBeNull();
    expect(go('agent', 'right')).toBeNull();
  });

  it('moves one card with left and right, in reading order', () => {
    expect(go('t1', 'right')).toBe('t2');
    expect(go('t2', 'right')).toBe('t3');
    expect(go('t3', 'left')).toBe('t2');
    expect(go('t0', 'left')).toBeNull();
    expect(go('t6', 'right')).toBeNull();
  });

  it('moves one row with up and down', () => {
    expect(go('t1', 'down')).toBe('t4');
    expect(go('t4', 'up')).toBe('t1');
    // t4 + 3 is past the last tool.
    expect(go('t4', 'down')).toBeNull();
    expect(go('t5', 'down')).toBeNull();
    expect(go('t6', 'up')).toBe('t3');
  });

  it('goes up from the first row to the hub', () => {
    expect(go('t0', 'up')).toBe('agent');
    expect(go('t2', 'up')).toBe('agent');
  });

  it('does not move from a handler or an unknown step', () => {
    expect(go('h', 'down')).toBeNull();
    expect(go('zz', 'down')).toBeNull();
  });
});
