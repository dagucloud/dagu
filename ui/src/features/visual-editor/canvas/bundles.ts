// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { NodeStatus } from '@/api/v1/schema';
import {
  curveSegments,
  HANDLE_RADIUS,
  Position,
  type CurveSegment,
  type Waypoint,
} from './engine/geometry';
import type { XY } from './engine/types';

// Edge bundling. Edges that join the same sources to the same targets, in two
// ranks that follow each other, share one trunk: every source curve merges
// into a junction, a short straight trunk runs along the flow, and the trunk
// splits towards each target. The layout places the junctions; the edges draw
// the pieces.

/** Edge as the detection reads it. `kind` is null for an edge that stays alone. */
export interface BundleCandidate {
  id: string;
  source: string;
  target: string;
  kind: BundleKind | null;
}

/**
 * 'depends': a plain dependency, bundled from two sources to two targets.
 * 'launcher': an edge of a launcher, bundled from any count.
 */
export type BundleKind = 'depends' | 'launcher';

/** A set of edges that share one trunk. */
export interface Bundle {
  id: string;
  kind: BundleKind;
  /** Rank of the sources. The targets are in the next rank. */
  rank: number;
  sources: string[];
  targets: string[];
  /** Ids of the edges, in the order of the input. */
  edges: string[];
}

/** Shortest straight run of a trunk, in px. */
export const MIN_TRUNK = 14;

/** Longest straight run of a trunk, in px. */
export const MAX_TRUNK = 28;

/** Shortest curve in px on each side of a trunk, along the flow. */
export const MIN_BRANCH = 20;

/** Longest extra room in px that a bundle takes between two ranks. */
const MAX_EXTRA_GAP = 140;

/**
 * Bundles of a layout. Only an edge that goes from a rank to the next rank
 * can join a bundle. The sources that have the same set of targets form one
 * group. Two sources and two targets at least make a bundle of `depends`
 * edges, so that a partial bipartite graph stays as it is. A bundle of
 * launcher edges needs two edges: all the launchers go into one trunk, even
 * for one start step.
 */
export function detectBundles(
  edges: readonly BundleCandidate[],
  rankOf: (key: string) => number | undefined
): Bundle[] {
  const bySource = new Map<string, BundleCandidate[]>();
  const seen = new Set<string>();
  for (const edge of edges) {
    const from = rankOf(edge.source);
    const to = rankOf(edge.target);
    if (
      edge.kind === null ||
      edge.source === edge.target ||
      from === undefined ||
      to === undefined ||
      to !== from + 1
    ) {
      continue;
    }
    const pair = `${edge.kind}\u0000${edge.source}\u0000${edge.target}`;
    if (seen.has(pair)) {
      continue;
    }
    seen.add(pair);
    const list = bySource.get(edge.source);
    if (list) {
      list.push(edge);
    } else {
      bySource.set(edge.source, [edge]);
    }
  }
  const groups = new Map<string, BundleCandidate[][]>();
  for (const list of bySource.values()) {
    const targets = [...new Set(list.map((edge) => edge.target))].sort();
    const signature = `${list[0]!.kind}\u0000${targets.join('\u0000')}`;
    const group = groups.get(signature);
    if (group) {
      group.push(list);
    } else {
      groups.set(signature, [list]);
    }
  }
  const bundles: Bundle[] = [];
  for (const group of groups.values()) {
    const kind = group[0]![0]!.kind!;
    const sources = group.map((list) => list[0]!.source);
    const first = group[0]!;
    const targets = first.map((edge) => edge.target);
    const enough =
      kind === 'launcher'
        ? sources.length * targets.length >= 2
        : sources.length >= 2 && targets.length >= 2;
    if (!enough) {
      continue;
    }
    bundles.push({
      id: `bundle:${sources.join(',')}>${targets.join(',')}`,
      kind,
      rank: rankOf(sources[0]!)!,
      sources,
      targets,
      edges: group.flatMap((list) => list.map((edge) => edge.id)),
    });
  }
  return bundles;
}

/**
 * Cross position of the trunk of a bundle. One target: the trunk runs
 * straight into it. One source: it runs straight out of it. Otherwise it
 * lies in the middle between the mean of the sources and the mean of the
 * targets.
 */
export function trunkCross(
  bundle: Pick<Bundle, 'sources' | 'targets'>,
  cross: (key: string) => number
): number {
  const mean = (keys: string[]) =>
    keys.reduce((sum, key) => sum + cross(key), 0) / keys.length;
  if (bundle.targets.length === 1) {
    return cross(bundle.targets[0]!);
  }
  if (bundle.sources.length === 1) {
    return cross(bundle.sources[0]!);
  }
  return (mean(bundle.sources) + mean(bundle.targets)) / 2;
}

/** Length in px of the trunk of a gap between two ranks. */
export function trunkLength(gap: number): number {
  return Math.min(MAX_TRUNK, Math.max(MIN_TRUNK, gap * 0.25));
}

/** Smallest radius in px that a bend of a branch keeps. */
const BRANCH_RADIUS = 11;

/** The handle dot: a branch to a target ends this far before the card. */
const END_GAP = HANDLE_RADIUS;

/** Smallest radius of the bends of a cubic curve, in px. */
function minRadius(segment: CurveSegment): number {
  if (segment.kind === 'line') {
    return Infinity;
  }
  let min = Infinity;
  for (let k = 0; k <= 32; k++) {
    const t = k / 32;
    const u = 1 - t;
    const d1x =
      3 * u * u * (segment.c1.x - segment.from.x) +
      6 * u * t * (segment.c2.x - segment.c1.x) +
      3 * t * t * (segment.to.x - segment.c2.x);
    const d1y =
      3 * u * u * (segment.c1.y - segment.from.y) +
      6 * u * t * (segment.c2.y - segment.c1.y) +
      3 * t * t * (segment.to.y - segment.c2.y);
    const d2x =
      6 * u * (segment.c2.x - 2 * segment.c1.x + segment.from.x) +
      6 * t * (segment.to.x - 2 * segment.c2.x + segment.c1.x);
    const d2y =
      6 * u * (segment.c2.y - 2 * segment.c1.y + segment.from.y) +
      6 * t * (segment.to.y - 2 * segment.c2.y + segment.c1.y);
    const speed = Math.hypot(d1x, d1y);
    if (speed < 1e-6) {
      continue;
    }
    const curvature = Math.abs(d1x * d2y - d1y * d2x) / speed ** 3;
    if (curvature > 0) {
      min = Math.min(min, 1 / curvature);
    }
  }
  return min;
}

/**
 * Room along the flow, in px, that a branch needs to move `cross` px to the
 * side with no bend sharper than the radius of a branch. It counts the gap
 * that a branch to a target leaves before the card, so that a gap of twice
 * this room, and a trunk, holds the two branches.
 */
export function branchRoom(cross: number): number {
  const move = Math.abs(cross);
  if (move < 1) {
    return 0;
  }
  for (let room = 8; room < 400; room += 2) {
    const [segment] = curveSegments({
      sourceX: 0,
      sourceY: 0,
      sourcePosition: Position.Bottom,
      targetX: move,
      targetY: room + END_GAP,
      targetGap: END_GAP,
    });
    if (!segment || minRadius(segment) >= BRANCH_RADIUS) {
      return room + END_GAP;
    }
  }
  return 400;
}

/**
 * Extra room in px that each gap between ranks needs, so that the curves of
 * the bundles of the gap bend smoothly. Index `r` is the gap that follows
 * rank `r`. A gap that needs nothing has 0.
 */
export function bundleGapExtra(
  bundles: readonly Bundle[],
  cross: (key: string) => number,
  rankSep: number,
  ranks: number
): number[] {
  const extra = new Array<number>(ranks).fill(0);
  const trunk = trunkLength(rankSep);
  for (const bundle of bundles) {
    const centre = trunkCross(bundle, cross);
    const far = Math.max(
      ...[...bundle.sources, ...bundle.targets].map((key) =>
        Math.abs(cross(key) - centre)
      )
    );
    const need = 2 * branchRoom(far) + trunk - rankSep;
    extra[bundle.rank] = Math.min(
      MAX_EXTRA_GAP,
      Math.max(extra[bundle.rank] ?? 0, Math.ceil(need))
    );
  }
  return extra.map((value) => Math.max(0, value));
}

/** What the layout tells an edge about its bundle. */
export interface EdgeBundleRef {
  id: string;
  kind: BundleKind;
  /** Where the sources merge, and where the trunk splits. */
  merge: XY;
  split: XY;
  sources: string[];
  targets: string[];
}

/** The pieces of the path of an edge that a bundle shares. */
export type BundlePiece = 'in' | 'out' | 'trunk';

/** The bundle of an edge, with what the edge draws and reaches. */
export interface EdgeBundleData extends EdgeBundleRef {
  /** The piece where the pointer reaches this edge, or null for none. */
  piece: BundlePiece | null;
  /** The pieces that this edge draws: a shared piece of a launcher is drawn once. */
  draws: { in: boolean; trunk: boolean; out: boolean };
  /** Run status that tints the piece, undefined for the neutral colour. */
  inStatus?: NodeStatus;
  trunkStatus?: NodeStatus;
  outStatus?: NodeStatus;
}

/**
 * Gives each edge of a bundle one piece to point at, so that every edge can
 * be reached when the pieces are enough: a piece belongs to one edge, and the
 * edge must pass through it. This is a maximum matching of edges to pieces.
 * An edge with no piece is not reachable by the pointer.
 */
export function assignPieces(
  edges: readonly { id: string; source: string; target: string }[]
): Map<string, BundlePiece | null> {
  const options = edges.map((edge) => [
    { piece: 'out' as const, slot: `out:${edge.target}` },
    { piece: 'in' as const, slot: `in:${edge.source}` },
    { piece: 'trunk' as const, slot: 'trunk' },
  ]);
  const owner = new Map<string, number>();
  const place = (index: number, visited: Set<string>): boolean => {
    for (const { slot } of options[index]!) {
      if (visited.has(slot)) {
        continue;
      }
      visited.add(slot);
      const current = owner.get(slot);
      if (current === undefined || place(current, visited)) {
        owner.set(slot, index);
        return true;
      }
    }
    return false;
  };
  edges.forEach((_, index) => place(index, new Set()));
  const result = new Map<string, BundlePiece | null>();
  edges.forEach((edge, index) => {
    const slot = [...owner].find(([, value]) => value === index)?.[0];
    const piece = options[index]!.find((option) => option.slot === slot);
    result.set(edge.id, piece ? piece.piece : null);
  });
  return result;
}

/** The three pieces of a curve that goes through a bundle. */
export interface BundlePieces {
  in: CurveSegment[];
  trunk: CurveSegment[];
  out: CurveSegment[];
}

/**
 * Splits the segments of an edge at its junction: the curve before the
 * merge, the trunk, and the curve after the split. Null when the curve no
 * longer goes through the junction, for example after a detour around a card
 * that the user dropped on it.
 */
export function splitAtJunction(
  segments: readonly CurveSegment[],
  merge: XY,
  split: XY,
  vertical: boolean,
  flowSign: number
): BundlePieces | null {
  const along = (p: XY) => (vertical ? p.y : p.x) * flowSign;
  const near = (p: XY, target: XY) =>
    Math.abs(along(p) - along(target)) < 0.5 &&
    Math.abs((vertical ? p.x : p.y) - (vertical ? target.x : target.y)) < 0.5;
  const m = segments.findIndex((segment) => near(segment.to, merge));
  if (m < 0) {
    return null;
  }
  const s = segments.findIndex(
    (segment, index) => index > m && near(segment.to, split)
  );
  if (s < 0 || s !== m + 1) {
    return null;
  }
  return {
    in: segments.slice(0, m + 1),
    trunk: segments.slice(m + 1, s + 1),
    out: segments.slice(s + 1),
  };
}

/** A card, in flow coordinates. */
interface Box {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** Shortest trunk and shortest branch, in px, of a bundle that a drag squeezes. */
const MIN_DRAGGED_TRUNK = 6;
const MIN_DRAGGED_BRANCH = 8;

/**
 * Smallest radius in px of a bend of a branch of a bundle that the user
 * squeezed by dragging a card. The branches of a laid-out bundle bend with a
 * radius of 9 px at least. A dragged card can be far to the side of a small
 * gap: the branch then bends tighter, down to this radius, and keeps the one
 * trunk. Under it, the bundle falls apart.
 */
export const DRAGGED_BRANCH_RADIUS = 2;

/** A bundle drawn again from the current places of its cards. */
export interface ReformedBundle {
  merge: Waypoint;
  split: Waypoint;
  /** The waypoints of each edge of the bundle, by edge id. */
  waypoints: Record<string, Waypoint[]>;
}

/**
 * The junctions of a bundle for the current places of its cards, which a
 * user can drag. The trunk lies on the middle between the mean of the sources
 * and the mean of the targets, and in the middle of the gap between the
 * last source and the first target along the flow. Every edge gets the
 * waypoints of the trunk, and a straight run to the near end of the gap.
 * Null when the cards leave no room for a trunk and two smooth branches: the
 * bundle then falls apart, and its edges are routed one by one.
 */
export function reformBundle(
  bundle: Pick<Bundle, 'sources' | 'targets'> & {
    edges: readonly { id: string; source: string; target: string }[];
  },
  boxOf: (key: string) => Box | undefined,
  horizontal: boolean,
  rankSep: number
): ReformedBundle | null {
  const boxes = new Map<string, Box>();
  for (const key of [...bundle.sources, ...bundle.targets]) {
    const box = boxOf(key);
    if (!box) {
      return null;
    }
    boxes.set(key, box);
  }
  const start = (box: Box) => (horizontal ? box.x : box.y);
  const end = (box: Box) =>
    horizontal ? box.x + box.width : box.y + box.height;
  const centre = (key: string) => {
    const box = boxes.get(key)!;
    return horizontal ? box.y + box.height / 2 : box.x + box.width / 2;
  };
  const fromEnd = Math.max(...bundle.sources.map((k) => end(boxes.get(k)!)));
  const toStart = Math.min(...bundle.targets.map((k) => start(boxes.get(k)!)));
  const gap = toStart - fromEnd;
  // The trunk shortens, down to a stub, when the user drags cards closer: the
  // smoothness check below decides whether the branches still fit.
  const trunk = Math.max(
    MIN_DRAGGED_TRUNK,
    Math.min(trunkLength(rankSep), gap - 2 * MIN_DRAGGED_BRANCH)
  );
  if (gap < MIN_DRAGGED_TRUNK + 2 * MIN_DRAGGED_BRANCH) {
    return null;
  }
  const cross = trunkCross(bundle, centre);
  const spot = (along: number, across: number): XY =>
    horizontal ? { x: along, y: across } : { x: across, y: along };
  const mergeAt = fromEnd + (gap - trunk) / 2;
  const merge: Waypoint = { ...spot(mergeAt, cross), hold: true };
  const split: Waypoint = { ...spot(mergeAt + trunk, cross), hold: true };
  const waypoints: Record<string, Waypoint[]> = {};
  for (const edge of bundle.edges) {
    const source = boxes.get(edge.source);
    const target = boxes.get(edge.target);
    if (!source || !target) {
      continue;
    }
    const points: Waypoint[] = [];
    if (fromEnd - end(source) > 0.5) {
      points.push(spot(fromEnd, centre(edge.source)));
    }
    points.push(merge, split);
    if (start(target) - toStart > 0.5) {
      points.push(spot(toStart, centre(edge.target)));
    }
    waypoints[edge.id] = points;
    // Every curve of the bundle must bend as smoothly as the routing asks:
    // a card dragged far to the side makes a bend that is a corner.
    const from = spot(end(source), centre(edge.source));
    const to = spot(start(target), centre(edge.target));
    const bends = curveSegments({
      sourceX: from.x,
      sourceY: from.y,
      sourcePosition: horizontal ? Position.Right : Position.Bottom,
      targetX: to.x,
      targetY: to.y,
      targetPosition: horizontal ? Position.Left : Position.Top,
      waypoints: points,
    });
    if (bends.some((segment) => minRadius(segment) < DRAGGED_BRANCH_RADIUS)) {
      return null;
    }
  }
  return { merge, split, waypoints };
}
