// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { CanvasGraph, CanvasNode } from '../../contracts';
import {
  modelArraySteps,
  namesArraySteps,
  overlaySample,
} from '../../testing/fixtures';
import { buildCanvasGraph } from '../graph';
import type { NodePositions } from '../types';
import {
  COMPACT_CAPTION_HEIGHT,
  COMPACT_CAPTION_WIDTH,
  COMPACT_NODE_SIZE,
  compactCardOffset,
  COMPACT_RANK_SEP_LR,
  COMPACT_RANK_SEP_TB,
  NODE_HEIGHT,
  NODE_WIDTH,
  RANK_SEP_LR,
  RANK_SEP_TB,
  connectedComponents,
  graphShapeKey,
  layoutGraph,
  nodeSize,
} from '../layout';

function node(key: string, lane: CanvasNode['lane'] = 'main'): CanvasNode {
  return {
    key,
    label: key,
    kind: 'run',
    lane,
    issues: { errors: 0, warnings: 0 },
  };
}

/** Graph from node keys and `source>target` pairs. */
function graphOf(keys: string[], pairs: string[] = []): CanvasGraph {
  return {
    nodes: keys.map((key) =>
      node(key, key.startsWith('handler') ? 'handler' : 'main')
    ),
    edges: pairs.map((pair) => {
      const [source = '', target = ''] = pair.split('>');
      return {
        id: `depends:${source}->${target}`,
        source,
        target,
        kind: 'depends',
      };
    }),
    editableEdges: true,
  };
}

describe('connectedComponents', () => {
  it('splits the main lane in components', () => {
    const graph = graphOf(['a', 'b', 'c', 'd', 'e'], ['c>a', 'd>e']);
    expect(connectedComponents(graph)).toEqual([['a', 'c'], ['b'], ['d', 'e']]);
  });

  it('ignores handlers and edge direction', () => {
    const graph = graphOf(['a', 'b', 'handler_on.failure'], ['b>a']);
    expect(connectedComponents(graph)).toEqual([['a', 'b']]);
  });

  it('merges a component through a late edge', () => {
    const graph = graphOf(['a', 'b', 'c'], ['c>b', 'b>a']);
    expect(connectedComponents(graph)).toEqual([['a', 'b', 'c']]);
  });

  it('gives no component for an empty main lane', () => {
    expect(connectedComponents(graphOf([]))).toEqual([]);
  });
});

describe('nodeSize', () => {
  it('gives step cards a fixed size', () => {
    expect(nodeSize(node('a'))).toEqual({
      width: NODE_WIDTH,
      height: NODE_HEIGHT,
    });
  });

  // A router is a square turned by 45 degrees: its box is a square.
  it('gives a router a square box that grows with its ports', () => {
    const router = { ...node('r'), kind: 'router' as const };
    expect(nodeSize({ ...router, routes: ['a'] })).toEqual({
      width: 160,
      height: 160,
    });
    expect(nodeSize({ ...router, routes: ['a', 'b', 'c', 'd'] })).toEqual({
      width: 192,
      height: 192,
    });
    expect(nodeSize({ ...router, routes: [] })).toEqual({
      width: 160,
      height: 160,
    });
  });
});

describe('layoutGraph one component', () => {
  const chain = graphOf(['a', 'b', 'c'], ['a>b', 'b>c']);

  it('places a chain in ranks along the direction', () => {
    const lr = layoutGraph(chain, 'LR');
    expect(lr.a).toEqual({ x: 0, y: 0 });
    expect(lr.b).toEqual({ x: NODE_WIDTH + RANK_SEP_LR, y: 0 });
    expect(lr.c).toEqual({ x: 2 * (NODE_WIDTH + RANK_SEP_LR), y: 0 });
    const tb = layoutGraph(chain, 'TB');
    expect(tb.a).toEqual({ x: 0, y: 0 });
    expect(tb.b).toEqual({ x: 0, y: NODE_HEIGHT + RANK_SEP_TB });
  });

  // The buttons of a link lie in a row across a vertical link, so that a
  // top-to-bottom graph has shorter links than a left-to-right one.
  it('has shorter links top to bottom, with room for the line and the arrow', () => {
    expect(RANK_SEP_LR).toBe(96);
    expect(COMPACT_RANK_SEP_LR).toBe(96);
    expect(RANK_SEP_TB).toBe(72);
    expect(COMPACT_RANK_SEP_TB).toBe(64);
    // A button is 24 px high: 20 px or more of the line stay on each side.
    expect((COMPACT_RANK_SEP_TB - 24) / 2).toBeGreaterThanOrEqual(20);
    const tb = layoutGraph(chain, 'TB', undefined, true);
    expect(tb.b?.y).toBe(
      COMPACT_NODE_SIZE + COMPACT_CAPTION_HEIGHT + COMPACT_RANK_SEP_TB
    );
  });

  it('keeps siblings in YAML order', () => {
    const graph = graphOf(['r', 'x', 'y', 'z'], ['r>x', 'r>y', 'r>z']);
    const lr = layoutGraph(graph, 'LR');
    expect(lr.x?.y).toBeLessThan(lr.y?.y ?? 0);
    expect(lr.y?.y).toBeLessThan(lr.z?.y ?? 0);
    expect(lr.y?.y).toBe(NODE_HEIGHT + 32);
  });

  it('gives the same positions for the same input', () => {
    const graph = graphOf(['a', 'b', 'c', 'd'], ['a>c', 'b>c', 'c>d', 'a>d']);
    expect(layoutGraph(graph, 'LR')).toEqual(layoutGraph(graph, 'LR'));
  });

  it('gives integer positions and survives cycles', () => {
    const graph = graphOf(['a', 'b'], ['a>b', 'b>a', 'a>a']);
    const positions = layoutGraph(graph, 'TB');
    for (const p of Object.values(positions)) {
      expect(Number.isInteger(p.x)).toBe(true);
      expect(Number.isInteger(p.y)).toBe(true);
    }
  });

  // The graph of a DAG that the editor does not edit has step names as keys.
  it('places a step named __proto__', () => {
    const positions = layoutGraph(
      graphOf(['__proto__', 'b'], ['__proto__>b']),
      'LR'
    );
    expect(Object.keys(positions)).toEqual(['__proto__', 'b']);
    expect(positions['__proto__']).toEqual({ x: 0, y: 0 });
  });
});

function boxOf(graph: CanvasGraph, positions: NodePositions, key: string) {
  const found = graph.nodes.find((n) => n.key === key);
  const p = positions[key];
  if (!found || !p) {
    throw new Error(`no position for ${key}`);
  }
  const { width, height } = nodeSize(found);
  return { left: p.x, top: p.y, right: p.x + width, bottom: p.y + height };
}

function overlaps(a: ReturnType<typeof boxOf>, b: ReturnType<typeof boxOf>) {
  return (
    a.left < b.right && b.left < a.right && a.top < b.bottom && b.top < a.bottom
  );
}

describe('layoutGraph components', () => {
  const keys = ['a', 'b', 'c', 'd', 'e'];
  const pairs = ['a>b', 'c>d', 'c>e'];

  it('stacks components with a 48 px gap', () => {
    const graph = graphOf(keys, pairs);
    const lr = layoutGraph(graph, 'LR');
    expect(lr.a).toEqual({ x: 0, y: 0 });
    expect(lr.d).toEqual({ x: NODE_WIDTH + RANK_SEP_LR, y: NODE_HEIGHT + 48 });
    const tb = layoutGraph(graph, 'TB');
    expect(tb.a).toEqual({ x: 0, y: 0 });
    expect(tb.d).toEqual({ x: NODE_WIDTH + 48, y: NODE_HEIGHT + RANK_SEP_TB });
  });

  it('does not overlap components', () => {
    const graph = graphOf(keys, pairs);
    for (const direction of ['LR', 'TB'] as const) {
      const positions = layoutGraph(graph, direction);
      for (const a of keys) {
        for (const b of keys) {
          if (a < b) {
            const hit = overlaps(
              boxOf(graph, positions, a),
              boxOf(graph, positions, b)
            );
            expect(hit).toBe(false);
          }
        }
      }
    }
  });

  it('keeps other components in place when a step appears', () => {
    const before = graphOf(keys, pairs);
    const isolated = graphOf([...keys, 'f'], pairs);
    const leaf = graphOf([...keys, 'f'], [...pairs, 'e>f']);
    for (const direction of ['LR', 'TB'] as const) {
      const base = layoutGraph(before, direction);
      const withIsolated = layoutGraph(isolated, direction);
      const withLeaf = layoutGraph(leaf, direction);
      for (const key of keys) {
        expect(withIsolated[key]).toEqual(base[key]);
      }
      for (const key of ['a', 'b']) {
        expect(withLeaf[key]).toEqual(base[key]);
      }
    }
  });
});

describe('layoutGraph handler lane', () => {
  const graph = graphOf(
    ['a', 'b', 'handler_on.success', 'handler_on.failure'],
    ['a>b']
  );

  it('puts handlers below the main graph in TB', () => {
    const positions = layoutGraph(graph, 'TB');
    const mainBottom = (positions.b?.y ?? 0) + NODE_HEIGHT;
    expect(positions['handler_on.success']).toEqual({
      x: 0,
      y: mainBottom + 96,
    });
    expect(positions['handler_on.failure']).toEqual({
      x: NODE_WIDTH + 32,
      y: mainBottom + 96,
    });
  });

  it('puts handlers right of the main graph in LR', () => {
    const positions = layoutGraph(graph, 'LR');
    const mainRight = (positions.b?.x ?? 0) + NODE_WIDTH;
    expect(positions['handler_on.success']).toEqual({
      x: mainRight + 96,
      y: 0,
    });
    expect(positions['handler_on.failure']).toEqual({
      x: mainRight + 96,
      y: NODE_HEIGHT + 32,
    });
  });

  it('starts at the origin without main steps', () => {
    const only = graphOf(['handler_on.exit']);
    expect(layoutGraph(only, 'LR')['handler_on.exit']).toEqual({ x: 0, y: 0 });
    expect(layoutGraph(only, 'TB')['handler_on.exit']).toEqual({ x: 0, y: 0 });
  });
});

describe('graphShapeKey', () => {
  const build = (overrides: object = {}, model = modelArraySteps) =>
    buildCanvasGraph({
      model,
      names: namesArraySteps,
      issues: { byStep: {}, dag: [] },
      ...overrides,
    });

  it('ignores label, issue and run changes', () => {
    const base = graphShapeKey(build(), 'LR');
    const renamed = {
      ...modelArraySteps,
      steps: modelArraySteps.steps.map((step) => ({
        ...step,
        explicitName: `${step.explicitName}-x`,
      })),
    };
    expect(graphShapeKey(build({}, renamed), 'LR')).toBe(base);
    expect(graphShapeKey(build({ overlay: overlaySample }), 'LR')).toBe(base);
    const issues = {
      byStep: {
        'steps[0]': [{ message: 'x', severity: 'error' as const }],
      },
      dag: [],
    };
    expect(graphShapeKey(build({ issues }), 'LR')).toBe(base);
  });

  it('changes with the direction', () => {
    const graph = build();
    expect(graphShapeKey(graph, 'LR')).not.toBe(graphShapeKey(graph, 'TB'));
  });

  it('changes with a node, an edge or a route', () => {
    const base = graphShapeKey(build(), 'LR');
    const [first, second, third] = modelArraySteps.steps;
    const fewer = { ...modelArraySteps, steps: [first, second] };
    expect(graphShapeKey(build({}, fewer as never), 'LR')).not.toBe(base);
    const unlinked = {
      ...modelArraySteps,
      steps: [first, { ...second, depends: [] }, third],
    };
    expect(graphShapeKey(build({}, unlinked as never), 'LR')).not.toBe(base);
    const router = (routes: object) => ({
      ...modelArraySteps,
      steps: [
        { ...first, action: 'router.route', value: { with: { routes } } },
        second,
        third,
      ],
    });
    const one = graphShapeKey(build({}, router({ a: [] }) as never), 'LR');
    const two = graphShapeKey(
      build({}, router({ a: [], b: [] }) as never),
      'LR'
    );
    expect(one).not.toBe(two);
  });
});

describe('compact layout', () => {
  // The owner asked for the name under the compact card, always: the layout
  // reserves the height of the caption, and the width of the longest one in a
  // top-to-bottom layout, so that two captions never touch.
  it('sizes a step as a 56 px square with room for its caption', () => {
    expect(COMPACT_NODE_SIZE).toBe(56);
    expect(COMPACT_CAPTION_HEIGHT).toBe(20);
    expect(nodeSize(node('a'), true, 'LR')).toEqual({
      width: COMPACT_NODE_SIZE,
      height: COMPACT_NODE_SIZE + COMPACT_CAPTION_HEIGHT,
    });
    expect(nodeSize(node('a'), true, 'TB')).toEqual({
      width: COMPACT_CAPTION_WIDTH,
      height: COMPACT_NODE_SIZE + COMPACT_CAPTION_HEIGHT,
    });
    // The card is centred on the cross axis, so that the handles on the
    // middle of the sides of the box are on the middle of the card.
    expect(compactCardOffset('LR')).toEqual({ left: 0, top: 10 });
    expect(compactCardOffset('TB')).toEqual({ left: 32, top: 0 });
    expect(nodeSize(node('a'))).toEqual({
      width: NODE_WIDTH,
      height: NODE_HEIGHT,
    });
  });

  it('packs the ranks closer than the full cards do', () => {
    const graph = graphOf(['a', 'b', 'c'], ['a>b', 'b>c']);
    const full = layoutGraph(graph, 'LR');
    const compact = layoutGraph(graph, 'LR', undefined, true);
    expect(compact.c!.x).toBeLessThan(full.c!.x);
    // The gap between two ranks is the rank separation, without overlap.
    expect(compact.b!.x - compact.a!.x).toBeGreaterThanOrEqual(
      COMPACT_NODE_SIZE
    );
    const column = layoutGraph(graph, 'TB', undefined, true);
    expect(column.b!.y - column.a!.y).toBeGreaterThanOrEqual(COMPACT_NODE_SIZE);
  });

  it('changes the shape key with the compact flag', () => {
    const graph = graphOf(['a', 'b'], ['a>b']);
    expect(graphShapeKey(graph, 'LR', undefined, true)).not.toBe(
      graphShapeKey(graph, 'LR', undefined, false)
    );
  });
});
