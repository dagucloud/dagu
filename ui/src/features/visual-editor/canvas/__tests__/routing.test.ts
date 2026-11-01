// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { CanvasDirection, CanvasGraph, CanvasNode } from '../../contracts';
import { insideBox, routedCurve, type Obstacle } from '../edgeRouting';
import { Position, sampleCurve } from '../engine/geometry';
import { layoutRoutes, nodeSize, routePorts } from '../layout';

// An edge never crosses a card that it does not link: the layout gives each
// long edge the free column of every rank between its ends, and the curve
// goes through these points.

const MARGIN = 3;

function node(key: string, routes?: string[]): CanvasNode {
  return {
    key,
    label: key,
    kind: routes ? 'router' : 'run',
    lane: 'main',
    ...(routes ? { routes } : {}),
    issues: { errors: 0, warnings: 0 },
  };
}

/** Graph from step keys, `source>target` pairs and routers (`key=pattern|pattern`). */
function graphOf(keys: string[], pairs: string[]): CanvasGraph {
  const routers = new Map<string, string[]>();
  const nodes = keys.map((spec) => {
    const [key = '', patterns] = spec.split('=');
    if (patterns !== undefined) {
      routers.set(key, patterns.split('|'));
    }
    return node(key, patterns?.split('|'));
  });
  const edges = pairs.map((pair) => {
    const [source = '', rest = ''] = pair.split('>');
    const [target = '', label] = rest.split('@');
    return label === undefined
      ? {
          id: `depends:${source}->${target}`,
          source,
          target,
          kind: 'depends' as const,
        }
      : {
          id: `route:${source}->${target}:${label}`,
          source,
          target,
          kind: 'route' as const,
          label,
        };
  });
  return { nodes, edges, editableEdges: true };
}

/** The shape of the demo workflows long-classic-N: stages with routers, fan-outs and joins. */
function classic(stages: number): CanvasGraph {
  const keys: string[] = [];
  const pairs: string[] = [];
  for (let s = 1; s <= stages; s++) {
    keys.push(
      `p${s}`,
      `r${s}=ok|other`,
      `ok${s}`,
      `other${s}`,
      `j${s}`,
      `prep${s}`,
      `a${s}`,
      `b${s}`,
      `c${s}`,
      `fan${s}`
    );
    if (s > 1) {
      pairs.push(`fan${s - 1}>p${s}`);
    }
    pairs.push(
      `p${s}>r${s}`,
      `r${s}>ok${s}@ok`,
      `r${s}>other${s}@other`,
      `ok${s}>j${s}`,
      `other${s}>j${s}`,
      `j${s}>prep${s}`,
      `prep${s}>a${s}`,
      `prep${s}>b${s}`,
      `prep${s}>c${s}`,
      `a${s}>fan${s}`,
      `b${s}>fan${s}`,
      `c${s}>fan${s}`
    );
  }
  // A last step reads the output of the first step of each stage: long edges.
  keys.push('report');
  for (let s = 1; s <= stages; s++) {
    pairs.push(`p${s}>report`);
  }
  pairs.push(`fan${stages}>report`);
  return graphOf(keys, pairs);
}

const FIXTURES: Record<string, CanvasGraph> = {
  'three ranks with a step between': graphOf(
    ['a', 'b', 'c', 'd'],
    ['a>b', 'b>c', 'c>d', 'a>d']
  ),
  'two long edges beside a chain': graphOf(
    ['a', 'b', 'c', 'd', 'e', 'f'],
    ['a>b', 'b>c', 'c>d', 'd>e', 'a>d', 'a>e', 'b>f', 'e>f']
  ),
  'router with a long edge': graphOf(
    ['produce', 'route=ok|other', 'ok', 'other', 'join'],
    [
      'produce>route',
      'route>ok@ok',
      'route>other@other',
      'ok>join',
      'other>join',
      'produce>join',
    ]
  ),
  'long-classic-20': classic(2),
  'long-classic-50': classic(5),
};

interface Placed {
  curves: { id: string; points: { x: number; y: number }[] }[];
  boxes: Map<string, Obstacle>;
}

function place(
  graph: CanvasGraph,
  direction: CanvasDirection,
  compact: boolean,
  net: boolean,
  useWaypoints = true
): Placed {
  const layout = layoutRoutes(graph, direction, 6, compact);
  const { positions } = layout;
  const waypoints = useWaypoints ? layout.waypoints : {};
  const lr = direction === 'LR';
  const boxes = new Map<string, Obstacle>();
  for (const n of graph.nodes) {
    const p = positions[n.key]!;
    boxes.set(n.key, { x: p.x, y: p.y, ...nodeSize(n, compact, direction) });
  }
  const curves = graph.edges.map((edge) => {
    const source = boxes.get(edge.source)!;
    const target = boxes.get(edge.target)!;
    const sourceNode = graph.nodes.find((n) => n.key === edge.source)!;
    let from = lr
      ? { x: source.x + source.width, y: source.y + source.height / 2 }
      : { x: source.x + source.width / 2, y: source.y + source.height };
    if (edge.kind === 'route' && sourceNode.routes) {
      const index = sourceNode.routes.indexOf(edge.label ?? '');
      const port = routePorts(
        sourceNode.routes.length,
        source.width,
        direction
      )[index];
      if (port) {
        from = { x: source.x + port.x, y: source.y + port.y };
      }
    }
    const to = lr
      ? { x: target.x, y: target.y + target.height / 2 }
      : { x: target.x + target.width / 2, y: target.y };
    const obstacles = [...boxes.entries()]
      .filter(([key]) => key !== edge.source && key !== edge.target)
      .map(([, box]) => box);
    const options = {
      sourceX: from.x,
      sourceY: from.y,
      sourcePosition: lr ? Position.Right : Position.Bottom,
      targetX: to.x,
      targetY: to.y,
      targetPosition: lr ? Position.Left : Position.Top,
      waypoints: waypoints[edge.id] ?? [],
    };
    const segments = net
      ? routedCurve(options, obstacles)
      : routedCurve(options, []);
    return { id: edge.id, points: sampleCurve(segments, 4) };
  });
  return { curves, boxes };
}

/** The edges that have a sample inside a card that they do not link. */
function crossings(graph: CanvasGraph, placed: Placed): string[] {
  const bad: string[] = [];
  for (const edge of graph.edges) {
    const curve = placed.curves.find((c) => c.id === edge.id)!;
    for (const [key, box] of placed.boxes) {
      if (key === edge.source || key === edge.target) {
        continue;
      }
      if (curve.points.some((point) => insideBox(point, box, MARGIN))) {
        bad.push(`${edge.id} crosses ${key}`);
      }
    }
  }
  return bad;
}

describe('edges around the cards', () => {
  const cases = Object.entries(FIXTURES).flatMap(([name, graph]) =>
    (['TB', 'LR'] as const).flatMap((direction) =>
      [false, true].map((compact) => [name, graph, direction, compact] as const)
    )
  );

  // The waypoints of the layout alone keep the long edges clear of the cards.
  it.each(cases)(
    'the layout waypoints cross no card: %s, %s, compact %s',
    (_name, graph, direction, compact) => {
      expect(crossings(graph, place(graph, direction, compact, false))).toEqual(
        []
      );
    }
  );

  // With the safety net of the canvas, which also covers a card that the user
  // dragged onto an edge.
  it.each(cases)(
    'the routed curves cross no card: %s, %s, compact %s',
    (_name, graph, direction, compact) => {
      expect(crossings(graph, place(graph, direction, compact, true))).toEqual(
        []
      );
    }
  );

  // The control: the same curves without the waypoints do cross cards, so
  // that the tests above would fail without the layout.
  it('would cross a card without the waypoints', () => {
    const crossed = Object.values(FIXTURES).filter(
      (graph) =>
        crossings(graph, place(graph, 'TB', false, false, false)).length > 0
    );
    expect(crossed.length).toBeGreaterThanOrEqual(2);
  });

  it('gives a long edge a waypoint pair in each rank that it crosses', () => {
    const graph = FIXTURES['three ranks with a step between']!;
    const { waypoints } = layoutRoutes(graph, 'TB');
    // a>d spans two ranks between its ends: an entry and an exit in each.
    expect(waypoints['depends:a->d']).toHaveLength(4);
    expect(waypoints['depends:a->b']).toBeUndefined();
    const points = waypoints['depends:a->d']!;
    // The edge runs straight down the free column of each rank.
    expect(points[0]!.x).toBe(points[1]!.x);
    expect(points[2]!.x).toBe(points[3]!.x);
    expect(points[0]!.y).toBeLessThan(points[1]!.y);
    expect(points[1]!.y).toBeLessThan(points[2]!.y);
  });

  it('moves a long edge aside when a card is dragged onto it', () => {
    const graph = FIXTURES['three ranks with a step between']!;
    const { positions, waypoints } = layoutRoutes(graph, 'TB');
    const a = positions.a!;
    const d = positions.d!;
    const size = nodeSize(graph.nodes[0]!);
    // A card on the straight line from a to d, which the layout did not know.
    const blocker: Obstacle = {
      x: (a.x + d.x) / 2 + size.width / 2 - 40,
      y: (a.y + d.y) / 2,
      width: 80,
      height: 40,
    };
    const options = {
      sourceX: a.x + size.width / 2,
      sourceY: a.y + size.height,
      sourcePosition: Position.Bottom,
      targetX: d.x + size.width / 2,
      targetY: d.y,
      targetPosition: Position.Top,
      waypoints: [] as { x: number; y: number }[],
    };
    void waypoints;
    const straight = sampleCurve(routedCurve(options, []), 4);
    expect(straight.some((p) => insideBox(p, blocker, MARGIN))).toBe(true);
    const around = sampleCurve(routedCurve(options, [blocker]), 4);
    expect(around.some((p) => insideBox(p, blocker, MARGIN))).toBe(false);
  });
});

describe('edges without a corner', () => {
  const R = 9;

  function bends(segments: ReturnType<typeof routedCurve>): number {
    let min = Infinity;
    for (const segment of segments) {
      if (segment.kind === 'line') continue;
      for (let k = 0; k <= 64; k++) {
        const t = k / 64;
        const u = 1 - t;
        const dx =
          3 * u * u * (segment.c1.x - segment.from.x) +
          6 * u * t * (segment.c2.x - segment.c1.x) +
          3 * t * t * (segment.to.x - segment.c2.x);
        const dy =
          3 * u * u * (segment.c1.y - segment.from.y) +
          6 * u * t * (segment.c2.y - segment.c1.y) +
          3 * t * t * (segment.to.y - segment.c2.y);
        const ax =
          6 * u * (segment.c2.x - 2 * segment.c1.x + segment.from.x) +
          6 * t * (segment.to.x - 2 * segment.c2.x + segment.c1.x);
        const ay =
          6 * u * (segment.c2.y - 2 * segment.c1.y + segment.from.y) +
          6 * t * (segment.to.y - 2 * segment.c2.y + segment.c1.y);
        const speed = Math.hypot(dx, dy);
        const curvature = Math.abs(dx * ay - dy * ax) / speed ** 3;
        if (curvature > 0) min = Math.min(min, 1 / curvature);
      }
    }
    return min;
  }

  const base = {
    sourcePosition: Position.Bottom,
    targetPosition: Position.Top,
    waypoints: [] as { x: number; y: number }[],
  };

  it('goes round by the side when the target is above the source', () => {
    const options = {
      ...base,
      sourceX: 200,
      sourceY: 400,
      targetX: 200,
      targetY: 100,
    };
    const source = { x: 80, y: 336, width: 240, height: 64 };
    const target = { x: 80, y: 100, width: 240, height: 64 };
    const segments = routedCurve(options, [], 6, { source, target });
    const points = sampleCurve(segments, 2);
    // The way passes neither under the source nor under the target.
    expect(
      points.some((p) => insideBox(p, { ...source, y: source.y + 2 }, -2))
    ).toBe(false);
    expect(points.some((p) => insideBox(p, target, -2))).toBe(false);
    expect(bends(segments)).toBeGreaterThanOrEqual(R);
  });

  it('turns a target that is too near along the flow into a loop', () => {
    const options = {
      ...base,
      sourceX: 200,
      sourceY: 100,
      targetX: 900,
      targetY: 130,
    };
    const segments = routedCurve(options, [], 6, {
      source: { x: 80, y: 36, width: 240, height: 64 },
      target: { x: 780, y: 130, width: 240, height: 64 },
    });
    expect(bends(segments)).toBeGreaterThanOrEqual(R);
  });

  it('keeps an S that has room as a plain S', () => {
    const options = {
      ...base,
      sourceX: 200,
      sourceY: 100,
      targetX: 360,
      targetY: 196,
    };
    expect(routedCurve(options, [])).toHaveLength(1);
  });

  it('moves a straight run aside, with room for the curves', () => {
    const graph = FIXTURES['three ranks with a step between']!;
    const { positions, waypoints } = layoutRoutes(graph, 'TB');
    const a = positions.a!;
    const d = positions.d!;
    const size = nodeSize(graph.nodes[0]!);
    // A card dropped on the straight run of the long edge a>d.
    const run = waypoints['depends:a->d']!;
    const blocker: Obstacle = {
      x: run[0]!.x - 40,
      y: (run[0]!.y + run[1]!.y) / 2 - 20,
      width: 80,
      height: 40,
    };
    const options = {
      sourceX: a.x + size.width / 2,
      sourceY: a.y + size.height,
      sourcePosition: Position.Bottom,
      targetX: d.x + size.width / 2,
      targetY: d.y,
      targetPosition: Position.Top,
      waypoints: run,
    };
    const segments = routedCurve(options, [blocker]);
    expect(
      sampleCurve(segments, 2).some((p) => insideBox(p, blocker, MARGIN))
    ).toBe(false);
    expect(bends(segments)).toBeGreaterThanOrEqual(R);
  });
});
