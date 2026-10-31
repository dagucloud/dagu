// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { NodeStatus } from '@/api/v1/schema';
import type { CanvasEdge, CanvasGraph, CanvasNode } from '../../contracts';
import {
  launcherStates,
  withLaunchers,
  type LauncherState,
} from '../../launchers/model';
import {
  assignPieces,
  detectBundles,
  splitAtJunction,
  trunkCross,
  type BundleCandidate,
} from '../bundles';
import {
  CanvasAffordancesContext,
  type CanvasAffordances,
} from '../affordances';
import { CanvasActionsContext, type CanvasActions } from '../context';
import { DependencyEdge } from '../edges/DependencyEdge';
import { routedCurve } from '../edgeRouting';
import { Flow, FlowProvider, Position, type EdgeProps } from '../engine';
import {
  EDGE_HOVER_RELEASE_MS,
  EDGE_HOVER_SETTLE_MS,
} from '../engine/edgeParts';
import { buildExportSvg } from '../exportCanvas';
import {
  toFlowEdges,
  launcherFlowEdges,
  withBundles,
  withWaypoints,
} from '../flow';
import { layoutRoutes, nodeSize, RANK_SEP_LR, RANK_SEP_TB } from '../layout';
import type { CanvasEdgeData, CanvasFlowEdge } from '../types';
import { place, bundleProblems, problems } from './routingHarness';

function node(key: string): CanvasNode {
  return {
    key,
    label: key,
    kind: 'run',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
  };
}

function edge(
  source: string,
  target: string,
  kind: CanvasEdge['kind'] = 'depends',
  label?: string
): CanvasEdge {
  return { id: `${kind}:${source}->${target}`, source, target, kind, label };
}

function graphOf(keys: string[], pairs: string[], kind?: CanvasEdge['kind']) {
  return {
    nodes: keys.map(node),
    edges: pairs.map((pair) => {
      const [source = '', target = ''] = pair.split('>');
      return edge(source, target, kind);
    }),
    editableEdges: true,
  } satisfies CanvasGraph;
}

/** Candidates from `a>b` pairs, with a rank for each key by its first letter. */
function candidates(
  pairs: string[],
  kind: BundleCandidate['kind'] = 'depends'
): BundleCandidate[] {
  return pairs.map((pair) => {
    const [source = '', target = ''] = pair.split('>');
    return { id: pair, source, target, kind };
  });
}

const RANK: Record<string, number> = {
  a: 0,
  b: 0,
  c: 0,
  d: 1,
  e: 1,
  f: 1,
  g: 2,
  h: 2,
};
const rankOf = (key: string) => RANK[key[0]!];

describe('detectBundles', () => {
  it('bundles two sources that feed the same two targets (K2,2)', () => {
    const bundles = detectBundles(
      candidates(['a>d', 'a>e', 'b>d', 'b>e']),
      rankOf
    );
    expect(bundles).toHaveLength(1);
    expect(bundles[0]).toMatchObject({
      sources: ['a', 'b'],
      targets: ['d', 'e'],
      edges: ['a>d', 'a>e', 'b>d', 'b>e'],
    });
  });

  it('bundles three sources that feed two targets (K3,2)', () => {
    const bundles = detectBundles(
      candidates(['a>d', 'a>e', 'b>d', 'b>e', 'c>d', 'c>e']),
      rankOf
    );
    expect(bundles).toHaveLength(1);
    expect(bundles[0]!.sources).toEqual(['a', 'b', 'c']);
  });

  it('does not bundle a partial bipartite graph', () => {
    expect(detectBundles(candidates(['a>d', 'a>e', 'b>d']), rankOf)).toEqual(
      []
    );
  });

  it('bundles the complete part of a graph that has one more edge', () => {
    const bundles = detectBundles(
      candidates(['a>d', 'a>e', 'b>d', 'b>e', 'c>d']),
      rankOf
    );
    expect(bundles).toHaveLength(1);
    expect(bundles[0]!.sources).toEqual(['a', 'b']);
  });

  it('does not bundle a fan-in or a fan-out of plain dependencies', () => {
    expect(detectBundles(candidates(['a>d', 'b>d']), rankOf)).toEqual([]);
    expect(detectBundles(candidates(['a>d', 'a>e']), rankOf)).toEqual([]);
  });

  it('does not bundle edges that are not between two ranks that follow', () => {
    expect(
      detectBundles(candidates(['a>g', 'a>h', 'b>g', 'b>h']), rankOf)
    ).toEqual([]);
  });

  it('does not bundle edges with a meaning of their own', () => {
    // Routes with labels, inferred and order links have a null kind.
    expect(
      detectBundles(candidates(['a>d', 'a>e', 'b>d', 'b>e'], null), rankOf)
    ).toEqual([]);
  });

  it('bundles all the launchers into one trunk, for any count of steps', () => {
    const many = detectBundles(
      candidates(
        ['a>d', 'a>e', 'a>f', 'b>d', 'b>e', 'b>f', 'c>d', 'c>e', 'c>f'],
        'launcher'
      ),
      rankOf
    );
    expect(many).toHaveLength(1);
    expect(many[0]).toMatchObject({ kind: 'launcher' });
    expect(many[0]!.targets).toEqual(['d', 'e', 'f']);
    const one = detectBundles(
      candidates(['a>d', 'b>d', 'c>d'], 'launcher'),
      rankOf
    );
    expect(one).toHaveLength(1);
    expect(one[0]!.targets).toEqual(['d']);
  });

  it('bundles only the start steps in the rank that follows the launchers', () => {
    // g is two ranks after a: its edge stays alone.
    const bundles = detectBundles(
      candidates(['a>d', 'b>d', 'a>g', 'b>g'], 'launcher'),
      rankOf
    );
    expect(bundles.map((bundle) => bundle.targets)).toEqual([['d']]);
  });
});

describe('trunkCross', () => {
  const at = (key: string) => ({ a: 0, b: 200, d: 20, e: 120, f: 500 })[key]!;
  it('runs straight into a single target', () => {
    expect(trunkCross({ sources: ['a', 'b'], targets: ['f'] }, at)).toBe(500);
  });
  it('runs straight out of a single source', () => {
    expect(trunkCross({ sources: ['a'], targets: ['d', 'e'] }, at)).toBe(0);
  });
  it('lies between the mean of the sources and of the targets', () => {
    expect(trunkCross({ sources: ['a', 'b'], targets: ['d', 'e'] }, at)).toBe(
      85
    );
  });
});

describe('assignPieces', () => {
  it('gives each edge of a K2,2 its own piece', () => {
    const pieces = assignPieces([
      { id: 'a>d', source: 'a', target: 'd' },
      { id: 'a>e', source: 'a', target: 'e' },
      { id: 'b>d', source: 'b', target: 'd' },
      { id: 'b>e', source: 'b', target: 'e' },
    ]);
    const given = [...pieces.values()];
    expect(given.every((piece) => piece !== null)).toBe(true);
  });

  it('gives each edge of a K3,2 its own piece', () => {
    const edges = ['a', 'b', 'c'].flatMap((source) =>
      ['d', 'e'].map((target) => ({
        id: `${source}>${target}`,
        source,
        target,
      }))
    );
    expect([...assignPieces(edges).values()].every(Boolean)).toBe(true);
  });
});

/** Two start steps that feed two steps, with a step before and one after. */
function k22(): CanvasGraph {
  return graphOf(
    ['x', 'y', 'm', 'n', 'z'],
    ['x>m', 'x>n', 'y>m', 'y>n', 'm>z', 'n>z']
  );
}

describe('junctions of the layout', () => {
  it('puts one trunk in the gap between the bands, centred on the cards', () => {
    for (const direction of ['TB', 'LR'] as const) {
      const graph = k22();
      const layout = layoutRoutes(graph, direction);
      const ids = ['x>m', 'x>n', 'y>m', 'y>n'].map(
        (pair) => `depends:${pair.replace('>', '->')}`
      );
      const refs = ids.map((id) => layout.bundles[id]!);
      expect(refs.every(Boolean)).toBe(true);
      expect(new Set(refs.map((ref) => JSON.stringify(ref))).size).toBe(1);
      const { merge, split } = refs[0]!;
      const horizontal = direction === 'LR';
      const along = (p: { x: number; y: number }) => (horizontal ? p.x : p.y);
      const cross = (p: { x: number; y: number }) => (horizontal ? p.y : p.x);
      // A straight trunk along the flow.
      expect(cross(merge)).toBe(cross(split));
      expect(along(split)).toBeGreaterThan(along(merge));
      // In the middle of the cross axis of the four cards.
      const centre = (key: string) => {
        const p = layout.positions[key]!;
        const size = nodeSize(graph.nodes.find((n) => n.key === key)!);
        return horizontal ? p.y + size.height / 2 : p.x + size.width / 2;
      };
      const mean = (keys: string[]) =>
        keys.reduce((sum, key) => sum + centre(key), 0) / keys.length;
      expect(cross(merge)).toBeCloseTo(
        (mean(['x', 'y']) + mean(['m', 'n'])) / 2
      );
      // Inside the gap: below the sources, above the targets.
      const sourceEnd = (key: string) => {
        const p = layout.positions[key]!;
        const size = nodeSize(graph.nodes.find((n) => n.key === key)!);
        return horizontal ? p.x + size.width : p.y + size.height;
      };
      const targetStart = horizontal
        ? layout.positions['m']!.x
        : layout.positions['m']!.y;
      expect(along(merge)).toBeGreaterThan(sourceEnd('x'));
      expect(along(split)).toBeLessThan(targetStart);
      // The waypoints of each edge hold the junctions.
      for (const id of ids) {
        const way = layout.waypoints[id]!;
        expect(way.filter((p) => p.hold)).toHaveLength(2);
      }
    }
  });

  it('leaves the gap as it was when the curves bend smoothly', () => {
    // Left to right the cards are 64 px high: the branches move little.
    const lr = layoutRoutes(k22(), 'LR');
    expect(
      lr.positions['m']!.x - lr.positions['x']!.x - nodeSize(node('x')).width
    ).toBe(RANK_SEP_LR);
    // Top to bottom the cards are 240 px wide: the gap may grow, never shrink.
    const tb = layoutRoutes(k22(), 'TB');
    expect(
      tb.positions['m']!.y - tb.positions['x']!.y - nodeSize(node('x')).height
    ).toBeGreaterThanOrEqual(RANK_SEP_TB);
  });

  it('widens a gap whose branches move far to the side', () => {
    // Six sources that feed two targets: the outer branches move far.
    const keys = ['a1', 'a2', 'a3', 'a4', 'a5', 'a6', 'm', 'n'];
    const pairs = keys
      .slice(0, 6)
      .flatMap((source) => [`${source}>m`, `${source}>n`]);
    const graph = graphOf(keys, pairs);
    const layout = layoutRoutes(graph, 'TB');
    const gap =
      layout.positions['m']!.y -
      layout.positions['a1']!.y -
      nodeSize(node('a1')).height;
    expect(gap).toBeGreaterThan(RANK_SEP_TB);
  });

  it('draws every edge of a bundle through its junctions, with a straight trunk', () => {
    const graph = k22();
    const layout = layoutRoutes(graph, 'TB');
    const id = 'depends:x->n';
    const bundle = layout.bundles[id]!;
    const size = (key: string) =>
      nodeSize(graph.nodes.find((n) => n.key === key)!);
    const from = layout.positions['x']!;
    const to = layout.positions['n']!;
    const segments = routedCurve(
      {
        sourceX: from.x + size('x').width / 2,
        sourceY: from.y + size('x').height,
        sourcePosition: Position.Bottom,
        targetX: to.x + size('n').width / 2,
        targetY: to.y,
        targetPosition: Position.Top,
        waypoints: layout.waypoints[id],
      },
      []
    );
    const pieces = splitAtJunction(
      segments,
      bundle.merge,
      bundle.split,
      true,
      1
    )!;
    expect(pieces).not.toBeNull();
    expect(pieces.trunk).toHaveLength(1);
    expect(pieces.trunk[0]!.kind).toBe('line');
    expect(pieces.in.length + pieces.trunk.length + pieces.out.length).toBe(
      segments.length
    );
  });

  it('bundles the launchers into one trunk and keeps a single step straight', () => {
    const graph = graphOf(['s'], []);
    const states: LauncherState[] = launcherStates({
      scheduleCount: 1,
      webhookCount: 1,
      webhooksAvailable: true,
    });
    const launched = withLaunchers(graph, states);
    const layout = layoutRoutes(launched.graph, 'TB');
    const ids = launched.edges.map((e) => e.id);
    const refs = ids.map((id) => layout.bundles[id]!);
    expect(new Set(refs.map((ref) => ref.id)).size).toBe(1);
    expect(refs[0]!.kind).toBe('launcher');
    // The trunk runs straight into the only step.
    const s = layout.positions['s']!;
    expect(refs[0]!.merge.x).toBe(s.x + nodeSize(node('s')).width / 2);
    expect(refs[0]!.split.x).toBe(refs[0]!.merge.x);
  });

  it('does not bundle labelled routes, inferred links or order links', () => {
    const graph: CanvasGraph = {
      nodes: ['x', 'y', 'm', 'n'].map(node),
      edges: [
        edge('x', 'm', 'inferred'),
        edge('x', 'n', 'inferred'),
        edge('y', 'm', 'inferred'),
        edge('y', 'n', 'inferred'),
      ],
      editableEdges: true,
    };
    expect(layoutRoutes(graph, 'TB').bundles).toEqual({});
    const routes: CanvasGraph = {
      nodes: ['x', 'y', 'm', 'n'].map(node),
      edges: [
        edge('x', 'm', 'route', 'a'),
        edge('x', 'n', 'route', 'b'),
        edge('y', 'm', 'route', 'a'),
        edge('y', 'n', 'route', 'b'),
      ],
      editableEdges: true,
    };
    expect(layoutRoutes(routes, 'TB').bundles).toEqual({});
  });

  it('keeps the bundles of the seeded graphs smooth and straight', () => {
    const placed = place({
      graph: k22(),
      direction: 'TB',
      compact: false,
      launchers: true,
    });
    expect(Object.keys(placed.bundles).length).toBeGreaterThan(0);
    expect(bundleProblems(placed)).toEqual([]);
  });
});

// The owner drags the steps of a bundle around: the bundle is formed again
// from the current places, with one trunk centred between the actual sources
// and targets.
describe('bundles of dragged cards', () => {
  const K22_IDS = ['x>m', 'x>n', 'y>m', 'y>n'].map(
    (pair) => `depends:${pair.replace('>', '->')}`
  );

  /** The distinct junctions of the edges, as JSON. */
  const trunks = (placed: ReturnType<typeof place>, ids: string[]) =>
    new Set(ids.map((id) => JSON.stringify(placed.bundles[id] ?? null)));

  /** Centre of a card on the cross axis, and the cards' ends along the flow. */
  const centreOf = (
    placed: ReturnType<typeof place>,
    key: string,
    horizontal: boolean
  ) => {
    const box = placed.boxes.get(key)!;
    return horizontal ? box.y + box.height / 2 : box.x + box.width / 2;
  };

  it('keeps one trunk, centred on the new places, after a source moved sideways', () => {
    for (const direction of ['TB', 'LR'] as const) {
      const horizontal = direction === 'LR';
      const placed = place({
        graph: k22(),
        direction,
        compact: false,
        move: { x: horizontal ? { dx: 0, dy: -60 } : { dx: -60, dy: 0 } },
      });
      expect(trunks(placed, K22_IDS).size).toBe(1);
      const bundle = placed.bundles[K22_IDS[0]!]!;
      expect(bundle).toBeDefined();
      const mean = (keys: string[]) =>
        keys.reduce((sum, key) => sum + centreOf(placed, key, horizontal), 0) /
        keys.length;
      const at = horizontal ? 'y' : 'x';
      expect(bundle.merge[at]).toBeCloseTo(
        (mean(['x', 'y']) + mean(['m', 'n'])) / 2
      );
      expect(bundle.split[at]).toBe(bundle.merge[at]);
      expect(bundleProblems(placed)).toEqual([]);
      expect(problems(placed)).toEqual([]);
      // Every edge goes through the trunk.
      for (const curve of placed.curves.filter((c) => K22_IDS.includes(c.id))) {
        const pieces = splitAtJunction(
          curve.segments,
          bundle.merge,
          bundle.split,
          !horizontal,
          1
        );
        expect(pieces, curve.id).not.toBeNull();
        expect(pieces!.trunk).toHaveLength(1);
      }
    }
  });

  it('moves the trunk with the cards when a target moves along the flow', () => {
    const before = place({ graph: k22(), direction: 'TB', compact: false });
    const after = place({
      graph: k22(),
      direction: 'TB',
      compact: false,
      move: {
        m: { dx: 40, dy: 120 },
        n: { dx: 40, dy: 120 },
        z: { dx: 40, dy: 120 },
      },
    });
    const a = before.bundles[K22_IDS[0]!]!;
    const b = after.bundles[K22_IDS[0]!]!;
    expect(trunks(after, K22_IDS).size).toBe(1);
    // The gap grew by 120 px: the trunk stays in its middle.
    expect(b.merge.y).toBeCloseTo(a.merge.y + 60);
    // The targets moved 40 px to the right: so does half of the trunk.
    expect(b.merge.x).toBeCloseTo(a.merge.x + 20);
    expect(problems(after)).toEqual([]);
  });

  it('forms the bundle again for a dragged card in every direction and mode', () => {
    for (const direction of ['TB', 'LR'] as const) {
      for (const compact of [false, true]) {
        for (const moved of ['x', 'y', 'm', 'n']) {
          const shift =
            direction === 'LR' ? { dx: 0, dy: 40 } : { dx: 40, dy: 0 };
          const placed = place({
            graph: k22(),
            direction,
            compact,
            move: { [moved]: shift },
          });
          const label = `${direction} ${compact} ${moved}`;
          expect(trunks(placed, K22_IDS).size, label).toBe(1);
          expect(placed.bundles[K22_IDS[0]!], label).toBeDefined();
          expect(bundleProblems(placed), label).toEqual([]);
          expect(problems(placed), label).toEqual([]);
        }
      }
    }
  });

  it('lets a bundle fall apart when a card leaves it no room', () => {
    // The source is dropped beside the targets: no trunk fits between them.
    const placed = place({
      graph: k22(),
      direction: 'TB',
      compact: false,
      move: { x: { dx: 400, dy: 160 } },
    });
    expect(K22_IDS.every((id) => placed.bundles[id] === undefined)).toBe(true);
  });

  it('leaves the other bundles as the layout made them', () => {
    const graph = graphOf(
      ['x', 'y', 'm', 'n', 'p', 'q', 'r', 's'],
      ['x>m', 'x>n', 'y>m', 'y>n', 'p>r', 'p>s', 'q>r', 'q>s']
    );
    const base = place({ graph, direction: 'TB', compact: false });
    const moved = place({
      graph,
      direction: 'TB',
      compact: false,
      move: { x: { dx: 30, dy: 0 } },
    });
    const other = ['p>r', 'p>s', 'q>r', 'q>s'].map(
      (pair) => `depends:${pair.replace('>', '->')}`
    );
    for (const id of other) {
      expect(moved.bundles[id]).toEqual(base.bundles[id]);
    }
    expect(moved.bundles[K22_IDS[0]!]).not.toEqual(base.bundles[K22_IDS[0]!]);
  });

  it('keeps one trunk for the launchers after a start step was dragged', () => {
    const graph = graphOf(['a', 'b', 'c'], ['a>c', 'b>c']);
    for (const direction of ['TB', 'LR'] as const) {
      const horizontal = direction === 'LR';
      const base = place({ graph, direction, compact: false, launchers: true });
      const launcherIds = Object.keys(base.bundles).filter((id) =>
        id.includes('launcher:')
      );
      expect(launcherIds.length).toBeGreaterThan(1);
      const placed = place({
        graph,
        direction,
        compact: false,
        launchers: true,
        move: { a: horizontal ? { dx: 0, dy: -50 } : { dx: -50, dy: 0 } },
      });
      expect(trunks(placed, launcherIds).size).toBe(1);
      const bundle = placed.bundles[launcherIds[0]!]!;
      expect(bundle.kind).toBe('launcher');
      // The trunk is centred between the launchers and the start steps.
      const at = horizontal ? 'y' : 'x';
      const mean = (keys: string[]) =>
        keys.reduce((sum, key) => sum + centreOf(placed, key, horizontal), 0) /
        keys.length;
      expect(bundle.merge[at]).toBeCloseTo(
        (mean(bundle.sources) + mean(bundle.targets)) / 2
      );
      expect(bundleProblems(placed)).toEqual([]);
      expect(problems(placed)).toEqual([]);
    }
  });

  it('reforms the trunk of the launchers for a single start step that moved', () => {
    const graph = graphOf(['s'], []);
    const placed = place({
      graph,
      direction: 'TB',
      compact: false,
      launchers: true,
      move: { s: { dx: 90, dy: 30 } },
    });
    const ids = Object.keys(placed.bundles);
    expect(ids.length).toBeGreaterThan(1);
    expect(trunks(placed, ids).size).toBe(1);
    // The trunk runs straight into the only step, at its new place.
    expect(placed.bundles[ids[0]!]!.merge.x).toBe(
      placed.positions['s']!.x + nodeSize(node('s')).width / 2
    );
  });
});

describe('withBundles', () => {
  const graph = k22();
  const layout = layoutRoutes(graph, 'TB');
  const flow = (statuses: Record<string, NodeStatus> = {}) => {
    const withRun: CanvasGraph = {
      ...graph,
      nodes: graph.nodes.map((n) =>
        statuses[n.key]
          ? {
              ...n,
              run: {
                status: statuses[n.key]!,
                statusLabel: 'x',
                retryCount: 0,
                doneCount: 0,
              },
            }
          : n
      ),
    };
    return withBundles(
      withWaypoints(
        toFlowEdges(withRun, { editable: true, selectedEdgeId: null }),
        layout.waypoints
      ),
      layout.bundles
    );
  };
  const find = (edges: CanvasFlowEdge[], id: string) =>
    edges.find((e) => e.id === id)!.data!.bundle;

  it('marks the edges of the bundle and leaves the others alone', () => {
    const edges = flow();
    expect(find(edges, 'depends:x->m')).toBeDefined();
    expect(find(edges, 'depends:m->z')).toBeUndefined();
  });

  it('gives each edge a piece to point at', () => {
    const edges = flow();
    const pieces = ['x->m', 'x->n', 'y->m', 'y->n'].map(
      (pair) => find(edges, `depends:${pair}`)!.piece
    );
    expect(pieces.every(Boolean)).toBe(true);
  });

  it('draws the trunk neutral when the statuses differ', () => {
    const edges = flow({
      x: NodeStatus.Success,
      y: NodeStatus.Failed,
    });
    const bundle = find(edges, 'depends:x->m')!;
    expect(bundle.trunkStatus).toBeUndefined();
    // All the edges of a source carry its status.
    expect(bundle.inStatus).toBe(NodeStatus.Success);
    expect(find(edges, 'depends:y->m')!.inStatus).toBe(NodeStatus.Failed);
  });

  it('tints the trunk when every edge has the same status', () => {
    const edges = flow({
      x: NodeStatus.Success,
      y: NodeStatus.Success,
    });
    expect(find(edges, 'depends:x->m')!.trunkStatus).toBe(NodeStatus.Success);
  });

  it('draws each shared piece of the launchers once', () => {
    const states = launcherStates({
      scheduleCount: 0,
      webhookCount: 0,
      webhooksAvailable: true,
    });
    const launched = withLaunchers(graphOf(['s', 't'], []), states);
    const lay = layoutRoutes(launched.graph, 'TB');
    const edges = withBundles(
      withWaypoints(launcherFlowEdges(launched, states), lay.waypoints),
      lay.bundles
    );
    const draws = edges.map((e) => e.data!.bundle!.draws);
    // One line to the trunk for each launcher, one trunk, one line to each step.
    expect(draws.filter((d) => d.in)).toHaveLength(3);
    expect(draws.filter((d) => d.trunk)).toHaveLength(1);
    expect(draws.filter((d) => d.out)).toHaveLength(2);
  });
});

describe('an edge of a bundle', () => {
  const graph = k22();
  const layout = layoutRoutes(graph, 'TB');
  const base = withBundles(
    withWaypoints(
      toFlowEdges(graph, { editable: true, selectedEdgeId: null }),
      layout.waypoints
    ),
    layout.bundles
  );

  function props(id: string, selected = false): EdgeProps<CanvasFlowEdge> {
    const flowEdge = base.find((e) => e.id === id)!;
    const size = (key: string) =>
      nodeSize(graph.nodes.find((n) => n.key === key)!);
    const from = layout.positions[flowEdge.source]!;
    const to = layout.positions[flowEdge.target]!;
    return {
      id,
      data: { ...(flowEdge.data as CanvasEdgeData), insertable: true },
      selected,
      sourceX: from.x + size(flowEdge.source).width / 2,
      sourceY: from.y + size(flowEdge.source).height,
      targetX: to.x + size(flowEdge.target).width / 2,
      targetY: to.y,
      sourcePosition: Position.Bottom,
      targetPosition: Position.Top,
    };
  }

  const actions: CanvasActions = {
    direction: 'TB',
    editable: true,
    onCommand: () => {},
    onDeleteStep: () => {},
    onInsertOnEdge: () => {},
  };
  const affordances: CanvasAffordances = {
    openPalette: vi.fn(),
    deleteEdge: vi.fn(),
    restoreEdge: vi.fn(),
  };

  function renderEdge(id: string, selected = false) {
    return render(
      <FlowProvider>
        <CanvasActionsContext.Provider value={actions}>
          <CanvasAffordancesContext.Provider value={affordances}>
            <Flow nodes={[]} edges={[]} nodeTypes={{}} edgeTypes={{}}>
              <svg>
                <DependencyEdge {...props(id, selected)} />
              </svg>
            </Flow>
          </CanvasAffordancesContext.Provider>
        </CanvasActionsContext.Provider>
      </FlowProvider>
    );
  }

  it('draws the branch, the trunk and the branch to its target', () => {
    const { container } = renderEdge('depends:x->m');
    const pieces = [
      ...container.querySelectorAll('path[data-bundle-piece]'),
    ].map((p) => p.getAttribute('data-bundle-piece'));
    expect(pieces).toContain('in');
    expect(pieces).toContain('trunk');
    expect(pieces).toContain('out');
  });

  // The full path is drawn and hidden: the group of the edge lights it by CSS
  // on hover, so that a hover mounts nothing.
  it('is reached on one piece only, and the full path lights on hover', () => {
    vi.useFakeTimers();
    try {
      const { container, queryByTestId } = renderEdge('depends:x->m');
      const hits = container.querySelectorAll(
        'path[data-flow-edge-interaction]'
      );
      expect(hits).toHaveLength(1);
      expect(container.querySelector('path[data-active]')).toBeNull();
      const lit = container.querySelector('path[data-flow-edge-overlay]')!;
      expect(lit).toHaveClass('hidden', 'group-data-[hovered]/edge:inline');
      expect(lit.getAttribute('d')).toMatch(/^M/);
      // The full path: it starts at the source and ends at the target.
      const visible = [
        ...container.querySelectorAll('path[data-bundle-piece]'),
      ];
      const first = visible.find(
        (p) => p.getAttribute('data-bundle-piece') === 'in'
      )!;
      expect(lit.getAttribute('d')!.split(' ')[0]).toBe(
        first.getAttribute('d')!.split(' ')[0]
      );
      expect(queryByTestId('ve-canvas-edge-buttons')).toBeNull();
      fireEvent.mouseEnter(hits[0]!);
      act(() => vi.advanceTimersByTime(EDGE_HOVER_SETTLE_MS));
      expect(queryByTestId('ve-canvas-edge-buttons')).not.toBeNull();
      fireEvent.mouseLeave(hits[0]!);
      act(() => vi.advanceTimersByTime(EDGE_HOVER_RELEASE_MS));
      expect(queryByTestId('ve-canvas-edge-buttons')).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it('shows the buttons on the piece that it owns', () => {
    const { container, getByTestId } = renderEdge('depends:x->m', true);
    const hit = container.querySelector('path[data-flow-edge-interaction]')!;
    const piece = hit.getAttribute('data-bundle-piece');
    expect(piece).toBeTruthy();
    const transform = getByTestId('ve-canvas-edge-buttons').style.transform;
    expect(transform).toMatch(/translate\(-50%, -50%\) translate\(/);
  });

  it('draws the lines of a selected edge in the colour of the selection', () => {
    const { container } = renderEdge('depends:y->n', true);
    const lit = container.querySelector<SVGPathElement>('path[data-active]')!;
    expect(lit.style.stroke).toBe('var(--primary)');
  });
});

describe('export of a bundle', () => {
  it('draws every edge of a bundle through the same trunk', () => {
    const graph = k22();
    const layout = layoutRoutes(graph, 'TB');
    const { svg } = buildExportSvg(graph, layout.positions, 'TB', {
      waypoints: layout.waypoints,
    });
    const doc = new DOMParser().parseFromString(svg, 'image/svg+xml');
    const bundle = layout.bundles['depends:x->m']!;
    const n = (v: number) => String(Math.round(v * 100) / 100);
    const trunk = ` L${n(bundle.split.x)},${n(bundle.split.y)}`;
    const merge = ` ${n(bundle.merge.x)},${n(bundle.merge.y)}`;
    for (const pair of ['x->m', 'x->n', 'y->m', 'y->n']) {
      const path = doc.querySelector(`path[data-edge="depends:${pair}"]`)!;
      const d = path.getAttribute('d')!;
      expect(d).toContain(trunk);
      expect(d).toContain(merge);
    }
    // An edge out of the bundle keeps its own line.
    const alone = doc.querySelector('path[data-edge="depends:m->z"]')!;
    expect(alone.getAttribute('d')).not.toContain(trunk);
  });
});
