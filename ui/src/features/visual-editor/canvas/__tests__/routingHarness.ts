// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// A harness for the edges of the canvas: it builds seeded graphs, lays them
// out as the canvas does, draws every edge with the routing of the app, and
// finds the edges that pass under a card or that bend like a corner.

import type { CanvasDirection, CanvasGraph, CanvasNode } from '../../contracts';
import {
  launcherStates,
  withLaunchers,
  type LauncherState,
} from '../../launchers/model';
import {
  DRAGGED_BRANCH_RADIUS,
  splitAtJunction,
  type EdgeBundleRef,
} from '../bundles';
import { CARD_MARGIN, routedCurve, type Obstacle } from '../edgeRouting';
import { Position, sampleCurve, type CurveSegment } from '../engine/geometry';
import { planGhost } from '../ghost';
import { layoutRoutes, nodeSize, reformRouting, routePorts } from '../layout';

/** Deterministic random numbers (mulberry32). */
export function rng(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function stepNode(key: string, routes?: string[]): CanvasNode {
  return {
    key,
    label: key,
    kind: routes ? 'router' : 'run',
    lane: 'main',
    ...(routes ? { routes } : {}),
    issues: { errors: 0, warnings: 0 },
  };
}

/**
 * A random workflow of `size` steps: branches, joins, long skips, routers
 * with several routes, a few handlers and, sometimes, several roots.
 */
export function randomGraph(seed: number, size: number): CanvasGraph {
  const next = rng(seed);
  const pairs = new Set<string>();
  const edges: Array<[number, number]> = [];
  const link = (s: number, t: number) => {
    const id = `${s}>${t}`;
    if (s !== t && !pairs.has(id)) {
      pairs.add(id);
      edges.push([s, t]);
    }
  };
  for (let i = 1; i < size; i++) {
    if (next() < 0.06) {
      continue; // a new root
    }
    const parents = next() < 0.3 ? 2 : 1;
    for (let k = 0; k < parents; k++) {
      const local = next() < 0.8;
      const back = local
        ? 1 + Math.floor(next() ** 2 * Math.min(i, 6))
        : 1 + Math.floor(next() * i);
      link(Math.max(0, i - back), i);
    }
  }
  // A fan-out: one step feeds several later steps.
  for (let f = 0; f < Math.floor(size / 12); f++) {
    const s = Math.floor(next() * (size - 3));
    for (let k = 0; k < 3; k++) {
      link(s, Math.min(size - 1, s + 1 + Math.floor(next() * 5)));
    }
  }
  const out = new Map<number, number[]>();
  for (const [s, t] of edges) {
    out.set(s, [...(out.get(s) ?? []), t]);
  }
  const routers = new Map<number, string[]>();
  for (const [s, targets] of out) {
    if (targets.length >= 2 && next() < 0.5) {
      const count = Math.min(4, targets.length);
      routers.set(
        s,
        Array.from({ length: count }, (_, i) => `r${s}_${i}`)
      );
    }
  }
  const nodes: CanvasNode[] = Array.from({ length: size }, (_, i) =>
    stepNode(`s${i}`, routers.get(i))
  );
  const seen = new Map<number, number>();
  const graphEdges = edges
    .sort((a, b) => a[0] - b[0] || a[1] - b[1])
    .map(([s, t]) => {
      const patterns = routers.get(s);
      if (!patterns) {
        return {
          id: `depends:s${s}->s${t}`,
          source: `s${s}`,
          target: `s${t}`,
          kind: 'depends' as const,
        };
      }
      const index = seen.get(s) ?? 0;
      seen.set(s, index + 1);
      const label = patterns[Math.min(index, patterns.length - 1)]!;
      return {
        id: `route:s${s}->s${t}:${label}`,
        source: `s${s}`,
        target: `s${t}`,
        kind: 'route' as const,
        label,
      };
    });
  const handlers = Math.floor(next() * 3);
  for (let h = 0; h < handlers; h++) {
    nodes.push({ ...stepNode(`h${h}`), lane: 'handler' });
  }
  return { nodes, edges: graphEdges, editableEdges: true };
}

/**
 * Free space in px that the user leaves around a card that they drop. A card
 * dropped closer than this to the end of an edge leaves no room for a smooth
 * detour: the harness does not drop one there.
 */
export const DROP_CLEARANCE = 100;

export type Drag = 'none' | 'onEdge' | 'random';

export interface Scenario {
  graph: CanvasGraph;
  direction: CanvasDirection;
  compact: boolean;
  launchers?: boolean;
  ghost?: boolean;
  drag?: Drag;
  /** Cards that the user moved, by key: the shift in px, after the layout. */
  move?: Record<string, { dx: number; dy: number }>;
  seed?: number;
}

export interface Placed {
  graph: CanvasGraph;
  /**
   * The edges of a bundle that was formed again after a card moved. The user
   * dragged the cards close to it: its branches may bend tighter than the
   * others, down to `DRAGGED_BRANCH_RADIUS`, so that the trunk stays single.
   */
  reformed: Set<string>;
  /** Top-left place of each card, after the moves. */
  positions: Record<string, { x: number; y: number }>;
  boxes: Map<string, Obstacle>;
  /** The bundle of each edge that still shares a trunk, by edge id. */
  bundles: Record<string, EdgeBundleRef>;
  curves: Array<{
    id: string;
    source: string;
    target: string;
    segments: CurveSegment[];
  }>;
}

function overlapping(a: Obstacle, b: Obstacle, gap: number): boolean {
  return (
    a.x < b.x + b.width + gap &&
    b.x < a.x + a.width + gap &&
    a.y < b.y + b.height + gap &&
    b.y < a.y + a.height + gap
  );
}

/**
 * The graph as the canvas draws it: with the ghost and the launchers when
 * asked, the layout, then a card that the user dragged, and the curve of
 * each edge with the routing of the app.
 */
export function place(scenario: Scenario): Placed {
  const { direction, compact } = scenario;
  const next = rng(scenario.seed ?? 1);
  let graph = scenario.graph;
  if (scenario.ghost) {
    const first = graph.nodes.find((n) => n.lane === 'main');
    const plan = first ? planGhost(graph, first.key, { mode: 'after' }) : null;
    if (plan) {
      graph = plan.graph;
    }
  }
  if (scenario.launchers) {
    const states: LauncherState[] = launcherStates({
      scheduleCount: 1,
      webhookCount: 1,
      webhooksAvailable: true,
    });
    graph = withLaunchers(graph, states).graph;
  }
  const layout = layoutRoutes(graph, direction, 6, compact);
  const positions = { ...layout.positions };
  let waypoints = { ...layout.waypoints };
  let bundles = { ...layout.bundles };
  const boxes = new Map<string, Obstacle>();
  const sizes = new Map(
    graph.nodes.map((n) => [n.key, nodeSize(n, compact, direction)] as const)
  );
  const boxAt = (key: string): Obstacle => {
    const p = positions[key]!;
    return { x: p.x, y: p.y, ...sizes.get(key)! };
  };
  const lr = direction === 'LR';
  const handleOf = (key: string, side: 'source' | 'target', label?: string) => {
    const box = boxAt(key);
    const node = graph.nodes.find((n) => n.key === key)!;
    if (side === 'target') {
      return lr
        ? { x: box.x, y: box.y + box.height / 2 }
        : { x: box.x + box.width / 2, y: box.y };
    }
    if (label !== undefined && node.routes) {
      const index = node.routes.indexOf(label);
      const port = routePorts(node.routes.length, box.width, direction)[index];
      if (port) {
        return { x: box.x + port.x, y: box.y + port.y };
      }
    }
    return lr
      ? { x: box.x + box.width, y: box.y + box.height / 2 }
      : { x: box.x + box.width / 2, y: box.y + box.height };
  };

  if (scenario.move) {
    for (const [key, shift] of Object.entries(scenario.move)) {
      const at = positions[key]!;
      positions[key] = { x: at.x + shift.dx, y: at.y + shift.dy };
    }
    ({ waypoints, bundles } = reformRouting(
      graph,
      direction,
      compact,
      positions,
      { waypoints, bundles },
      new Set(Object.keys(scenario.move))
    ));
  }
  const drag = scenario.drag ?? 'none';
  if (drag !== 'none') {
    const movable = graph.nodes.filter((n) => n.lane === 'main');
    for (let attempt = 0; attempt < 40; attempt++) {
      const node = movable[Math.floor(next() * movable.length)]!;
      const size = sizes.get(node.key)!;
      let centre: { x: number; y: number };
      if (drag === 'onEdge') {
        const candidates = graph.edges.filter(
          (e) => e.source !== node.key && e.target !== node.key
        );
        const edge = candidates[Math.floor(next() * candidates.length)];
        if (!edge) {
          continue;
        }
        const s = handleOf(edge.source, 'source', edge.label);
        const t = handleOf(edge.target, 'target');
        const f = 0.3 + next() * 0.4;
        centre = { x: s.x + (t.x - s.x) * f, y: s.y + (t.y - s.y) * f };
      } else {
        const all = [...graph.nodes].map((n) => boxAt(n.key));
        const minX = Math.min(...all.map((b) => b.x));
        const maxX = Math.max(...all.map((b) => b.x + b.width));
        const minY = Math.min(...all.map((b) => b.y));
        const maxY = Math.max(...all.map((b) => b.y + b.height));
        centre = {
          x: minX + next() * (maxX - minX),
          y: minY + next() * (maxY - minY),
        };
      }
      const moved: Obstacle = {
        x: Math.round(centre.x - size.width / 2),
        y: Math.round(centre.y - size.height / 2),
        ...size,
      };
      // The user drops a card on free canvas: not on another card.
      const free = graph.nodes.every(
        (n) =>
          n.key === node.key ||
          !overlapping(moved, boxAt(n.key), DROP_CLEARANCE)
      );
      // The card stays in the flow: its edges still run forward, with room
      // for a turn. A card dropped behind its parent is a loop, which has
      // its own test.
      const forward = graph.edges
        .filter((e) => e.source === node.key || e.target === node.key)
        .every((e) => {
          const from = e.source === node.key ? moved : boxAt(e.source);
          const to = e.target === node.key ? moved : boxAt(e.target);
          const leaves = lr ? from.x + from.width : from.y + from.height;
          const enters = lr ? to.x : to.y;
          return enters - leaves >= 60;
        });
      if (!free || !forward) {
        continue;
      }
      positions[node.key] = { x: moved.x, y: moved.y };
      // The canvas routes the edges of the moved card again and forms its
      // bundles again from the new places.
      ({ waypoints, bundles } = reformRouting(
        graph,
        direction,
        compact,
        positions,
        { waypoints, bundles },
        new Set([node.key])
      ));
      break;
    }
  }
  for (const node of graph.nodes) {
    boxes.set(node.key, boxAt(node.key));
  }
  const reformed = new Set(
    Object.keys(bundles).filter(
      (id) => JSON.stringify(bundles[id]) !== JSON.stringify(layout.bundles[id])
    )
  );
  const curves = graph.edges
    .filter((e) => e.kind !== 'agent')
    .map((edge) => {
      const from = handleOf(edge.source, 'source', edge.label);
      const to = handleOf(edge.target, 'target');
      const others = [...boxes.entries()]
        .filter(([key]) => key !== edge.source && key !== edge.target)
        .map(([, box]) => box);
      const segments = routedCurve(
        {
          sourceX: from.x,
          sourceY: from.y,
          sourcePosition: lr ? Position.Right : Position.Bottom,
          targetX: to.x,
          targetY: to.y,
          targetPosition: lr ? Position.Left : Position.Top,
          waypoints: waypoints[edge.id] ?? [],
        },
        others,
        CARD_MARGIN,
        { source: boxes.get(edge.source), target: boxes.get(edge.target) }
      );
      return {
        id: edge.id,
        source: edge.source,
        target: edge.target,
        segments,
      };
    });
  return { graph, positions, boxes, bundles, reformed, curves };
}

/** Clear space in px that the checks ask between a line and a card. */
export const CHECK_MARGIN = 3;

/** A sampled line may not bend with a radius under this, in px. */
export const MIN_RADIUS = 9;

/** Two pieces of a curve meet with a heading change under this, in degrees. */
export const MAX_JOIN_DEGREES = 4;

function inBox(p: { x: number; y: number }, box: Obstacle, margin: number) {
  return (
    p.x > box.x - margin &&
    p.x < box.x + box.width + margin &&
    p.y > box.y - margin &&
    p.y < box.y + box.height + margin
  );
}

/** Problems of the curves: a card crossed, or a corner. */
export function problems(placed: Placed): string[] {
  const bad: string[] = [];
  for (const curve of placed.curves) {
    const points = sampleCurve(curve.segments, 3);
    for (const [key, box] of placed.boxes) {
      const own = key === curve.source || key === curve.target;
      if (own) {
        const node = placed.graph.nodes.find((n) => n.key === key)!;
        const router = node.kind === 'router';
        const end =
          key === curve.target ? points[points.length - 1]! : points[0]!;
        const inside = points.some((p) => {
          if (Math.hypot(p.x - end.x, p.y - end.y) < 12) {
            return false;
          }
          if (router) {
            const cx = box.x + box.width / 2;
            const cy = box.y + box.height / 2;
            return Math.abs(p.x - cx) + Math.abs(p.y - cy) < box.width / 2 - 2;
          }
          return inBox(p, box, -2);
        });
        if (inside) {
          bad.push(`${curve.id} runs under its own card ${key}`);
        }
        continue;
      }
      if (points.some((p) => inBox(p, box, CHECK_MARGIN))) {
        bad.push(`${curve.id} crosses ${key}`);
      }
    }
    bad.push(
      ...corners(
        curve.id,
        curve.segments,
        placed.reformed.has(curve.id) ? DRAGGED_BRANCH_RADIUS : MIN_RADIUS
      )
    );
  }
  return bad;
}

type Vec = { x: number; y: number };

function tangentAt(segment: CurveSegment, end: 'start' | 'end'): Vec | null {
  const pairs: Array<[Vec, Vec]> =
    segment.kind === 'line'
      ? [[segment.from, segment.to]]
      : end === 'start'
        ? [
            [segment.from, segment.c1],
            [segment.from, segment.c2],
            [segment.from, segment.to],
          ]
        : [
            [segment.c2, segment.to],
            [segment.c1, segment.to],
            [segment.from, segment.to],
          ];
  for (const [a, b] of pairs) {
    const d = { x: b.x - a.x, y: b.y - a.y };
    if (Math.hypot(d.x, d.y) > 1e-6) {
      return d;
    }
  }
  return null;
}

function degrees(a: Vec, b: Vec): number {
  const cross = a.x * b.y - a.y * b.x;
  const dot = a.x * b.x + a.y * b.y;
  return (Math.abs(Math.atan2(cross, dot)) * 180) / Math.PI;
}

/** The corners of a curve: sharp joins between pieces and sharp bends. */
export function corners(
  id: string,
  segments: CurveSegment[],
  minRadius: number = MIN_RADIUS
): string[] {
  const bad: string[] = [];
  for (let i = 0; i + 1 < segments.length; i++) {
    const a = tangentAt(segments[i]!, 'end');
    const b = tangentAt(segments[i + 1]!, 'start');
    if (a && b && degrees(a, b) > MAX_JOIN_DEGREES) {
      bad.push(
        `${id} has a corner at piece ${i}: ${degrees(a, b).toFixed(0)} deg`
      );
    }
  }
  segments.forEach((segment, i) => {
    if (segment.kind === 'line') {
      return;
    }
    let min = Infinity;
    for (let k = 0; k <= 64; k++) {
      const t = k / 64;
      const u = 1 - t;
      const d1 = {
        x:
          3 * u * u * (segment.c1.x - segment.from.x) +
          6 * u * t * (segment.c2.x - segment.c1.x) +
          3 * t * t * (segment.to.x - segment.c2.x),
        y:
          3 * u * u * (segment.c1.y - segment.from.y) +
          6 * u * t * (segment.c2.y - segment.c1.y) +
          3 * t * t * (segment.to.y - segment.c2.y),
      };
      const d2 = {
        x:
          6 * u * (segment.c2.x - 2 * segment.c1.x + segment.from.x) +
          6 * t * (segment.to.x - 2 * segment.c2.x + segment.c1.x),
        y:
          6 * u * (segment.c2.y - 2 * segment.c1.y + segment.from.y) +
          6 * t * (segment.to.y - 2 * segment.c2.y + segment.c1.y),
      };
      const speed = Math.hypot(d1.x, d1.y);
      if (speed < 1e-6) {
        continue;
      }
      const curvature = Math.abs(d1.x * d2.y - d1.y * d2.x) / speed ** 3;
      min = Math.min(min, curvature === 0 ? Infinity : 1 / curvature);
    }
    if (min < minRadius) {
      bad.push(
        `${id} bends with a radius of ${min.toFixed(1)} px in piece ${i}`
      );
    }
  });
  return bad;
}

/**
 * Problems of the bundles: an edge of a bundle must go through the merge and
 * the split of its bundle, with a straight trunk between them, and every edge
 * of the bundle must have the same trunk. A card dropped on the trunk makes
 * an edge detour: that edge leaves the bundle, and the others keep their
 * trunk.
 */
export function bundleProblems(placed: Placed): string[] {
  const bad: string[] = [];
  const trunks = new Map<string, string>();
  for (const curve of placed.curves) {
    const bundle = placed.bundles[curve.id];
    if (!bundle) {
      continue;
    }
    // The trunk of a top-to-bottom layout is vertical.
    const vertical = Math.abs(bundle.merge.x - bundle.split.x) < 0.5;
    const pieces = splitAtJunction(
      curve.segments,
      bundle.merge,
      bundle.split,
      vertical,
      1
    );
    if (!pieces) {
      continue;
    }
    const trunk = pieces.trunk;
    if (trunk.length !== 1 || trunk[0]!.kind !== 'line') {
      bad.push(`${curve.id} has a bent trunk`);
    }
    const key = JSON.stringify([bundle.merge, bundle.split]);
    const seen = trunks.get(bundle.id);
    if (seen !== undefined && seen !== key) {
      bad.push(`${curve.id} has another trunk than its bundle`);
    }
    trunks.set(bundle.id, key);
  }
  return bad;
}
