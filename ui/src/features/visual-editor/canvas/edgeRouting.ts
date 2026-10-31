// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  curveSegments,
  Position,
  roomFor,
  sampleCurve,
  type CurveOptions,
  type CurveSegment,
  type Waypoint,
  widestMove,
} from './engine/geometry';
import type { XY } from './engine/types';

/** A card that an edge must not cross, in flow coordinates. */
export interface Obstacle {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** Clear space in px that an edge keeps between its line and a card. */
export const CARD_MARGIN = 6;

/** Distance in px from a card to the straight run that goes around it. */
const SIDE_GAP = 10;

/** Distance in px from a card to the point where the detour leaves the path. */
const LEAD = 14;

const MAX_DETOURS = 8;

function bounds(segment: CurveSegment): Obstacle {
  const xs =
    segment.kind === 'line'
      ? [segment.from.x, segment.to.x]
      : [segment.from.x, segment.c1.x, segment.c2.x, segment.to.x];
  const ys =
    segment.kind === 'line'
      ? [segment.from.y, segment.to.y]
      : [segment.from.y, segment.c1.y, segment.c2.y, segment.to.y];
  const x = Math.min(...xs);
  const y = Math.min(...ys);
  return {
    x,
    y,
    width: Math.max(...xs) - x,
    height: Math.max(...ys) - y,
  };
}

function overlaps(a: Obstacle, b: Obstacle): boolean {
  return (
    a.x < b.x + b.width &&
    b.x < a.x + a.width &&
    a.y < b.y + b.height &&
    b.y < a.y + a.height
  );
}

/** True when the point is inside the box grown by `margin` on each side. */
export function insideBox(point: XY, box: Obstacle, margin: number): boolean {
  return (
    point.x > box.x - margin &&
    point.x < box.x + box.width + margin &&
    point.y > box.y - margin &&
    point.y < box.y + box.height + margin
  );
}

/** The first card that a segment crosses, with the index of the segment. */
function firstHit(
  segments: readonly CurveSegment[],
  obstacles: readonly Obstacle[],
  margin: number
): { index: number; box: Obstacle } | null {
  for (let index = 0; index < segments.length; index++) {
    const segment = segments[index]!;
    const near = obstacles.filter((box) =>
      overlaps(
        {
          x: box.x - margin,
          y: box.y - margin,
          width: box.width + 2 * margin,
          height: box.height + 2 * margin,
        },
        bounds(segment)
      )
    );
    if (near.length === 0) {
      continue;
    }
    for (const point of sampleCurve([segment], 6)) {
      const box = near.find((candidate) => insideBox(point, candidate, margin));
      if (box) {
        return { index, box };
      }
    }
  }
  return null;
}

/** Distance in px from a card to the side column of a loop. */
const LOOP_GAP = 28;

/** Along-distance in px that a loop keeps between a handle and its U turn. */
const LOOP_LEAD = 28;

/** Shortest run in px that a loop makes against the flow. */
const LOOP_RUN = 24;

/** Radius in px under which a bend counts as a corner. */
export const CORNER_RADIUS = 12;

/** The two cards that an edge links, for the side column of a loop. */
export interface EdgeEnds {
  source?: Obstacle;
  target?: Obstacle;
}

/**
 * True when a plain S from `from` to `to` would bend like a corner: the
 * target is not far enough along the flow for the sideways move.
 */
function tooTight(delta: number, cross: number): boolean {
  return delta < LOOP_RUN || cross > widestMove(delta, CORNER_RADIUS);
}

/**
 * The two points of a loop: the edge leaves its source, goes round the
 * cards by a side column, runs against the flow and enters the target from
 * outside. The column is between the two cards when they leave room, and
 * beside them otherwise.
 */
function loopPoints(
  from: XY,
  to: XY,
  vertical: boolean,
  flowSign: number,
  ends: EdgeEnds | undefined
): Waypoint[] {
  const a = (p: XY) => (vertical ? p.y : p.x) * flowSign;
  const b = (p: XY) => (vertical ? p.x : p.y);
  const at = (alongValue: number, crossValue: number): XY =>
    vertical
      ? { x: crossValue, y: alongValue * flowSign }
      : { x: alongValue * flowSign, y: crossValue };
  const span = (box: Obstacle | undefined, p: XY): [number, number] =>
    box
      ? vertical
        ? [box.x, box.x + box.width]
        : [box.y, box.y + box.height]
      : [b(p) - 120, b(p) + 120];
  const [s0, s1] = span(ends?.source, from);
  const [t0, t1] = span(ends?.target, to);
  let side: number;
  if (s1 + 2 * LOOP_GAP <= t0) {
    side = (s1 + t0) / 2;
  } else if (t1 + 2 * LOOP_GAP <= s0) {
    side = (t1 + s0) / 2;
  } else {
    const left = Math.min(s0, t0) - LOOP_GAP;
    const right = Math.max(s1, t1) + LOOP_GAP;
    const middle = (b(from) + b(to)) / 2;
    side = Math.abs(left - middle) <= Math.abs(right - middle) ? left : right;
  }
  const first = a(from) + LOOP_LEAD;
  const last = Math.min(a(to) - LOOP_LEAD, first - LOOP_RUN);
  return [
    { ...at(first, side), heading: 'back' },
    { ...at(last, side), heading: 'back' },
  ];
}

/** Free columns that a run tries, nearest to the ideal one first. */
const COLUMN_TRIES = 12;

/**
 * A straight run along the flow, in the free column that is nearest to the
 * middle of the edge, with a smooth S at each end. A column is free when no
 * card is within the margin along the whole edge, and the curve crosses no
 * card. Null when no column does it.
 */
function columnRoute(
  options: CurveOptions,
  obstacles: readonly Obstacle[],
  margin: number,
  start: XY,
  finish: XY,
  ends?: EdgeEnds
): Waypoint[] | null {
  const position = options.sourcePosition ?? Position.Bottom;
  const vertical = position === Position.Bottom || position === Position.Top;
  const sign =
    position === Position.Bottom || position === Position.Right ? 1 : -1;
  const a = (p: XY) => (vertical ? p.y : p.x);
  const b = (p: XY) => (vertical ? p.x : p.y);
  const at = (alongValue: number, crossValue: number): XY =>
    vertical
      ? { x: crossValue, y: alongValue }
      : { x: alongValue, y: crossValue };
  const lo = Math.min(a(start), a(finish));
  const hi = Math.max(a(start), a(finish));
  const pad = margin + SIDE_GAP;
  const blocked = obstacles
    .filter((box) => {
      const from = vertical ? box.y : box.x;
      const size = vertical ? box.height : box.width;
      return from < hi + margin && from + size > lo - margin;
    })
    .map((box): [number, number] => {
      const from = vertical ? box.x : box.y;
      const size = vertical ? box.width : box.height;
      return [from - pad, from + size + pad];
    })
    .sort((x, y) => x[0] - y[0]);
  const merged: [number, number][] = [];
  for (const interval of blocked) {
    const last = merged[merged.length - 1];
    if (last && interval[0] <= last[1]) {
      last[1] = Math.max(last[1], interval[1]);
    } else {
      merged.push([...interval]);
    }
  }
  const ideal = (b(start) + b(finish)) / 2;
  const free: Array<[number, number]> = [];
  let from = -Infinity;
  for (const [low, high] of [
    ...merged,
    [Infinity, Infinity] as [number, number],
  ]) {
    if (low > from) {
      free.push([from, low]);
    }
    from = Math.max(from, high);
  }
  const candidates = free
    .map(([low, high]) => Math.min(Math.max(ideal, low), high))
    .sort((x, y) => Math.abs(x - ideal) - Math.abs(y - ideal))
    .slice(0, COLUMN_TRIES);
  for (const column of candidates) {
    // The port of a router lies inside its box: the run starts beyond it.
    const clear = ends?.source
      ? vertical
        ? sign > 0
          ? ends.source.y + ends.source.height
          : ends.source.y
        : sign > 0
          ? ends.source.x + ends.source.width
          : ends.source.x
      : a(start);
    const first =
      sign *
      Math.max(
        sign * a(start) + Math.max(LEAD, roomFor(column - b(start))),
        sign * clear + LEAD
      );
    const last = a(finish) - sign * Math.max(LEAD, roomFor(column - b(finish)));
    if ((last - first) * sign < 1) {
      continue;
    }
    const waypoints = [at(first, column), at(last, column)];
    if (
      !firstHit(curveSegments({ ...options, waypoints }), obstacles, margin)
    ) {
      return waypoints;
    }
  }
  return null;
}

/**
 * The waypoints of an edge so that its curve crosses no card of `obstacles`
 * and has no corner. The given waypoints stay. An edge whose target is too
 * near along the flow, or behind its source, first gets a loop by a side
 * column. When the curve crosses a card, two points are added beside the
 * card, on the side that is nearer to the path, and the curve is drawn
 * again. A few rounds at most: a graph that leaves no room keeps the best
 * curve found.
 */
export function routeAround(
  options: CurveOptions,
  obstacles: readonly Obstacle[],
  margin = CARD_MARGIN,
  ends?: EdgeEnds
): Waypoint[] {
  const waypoints: Waypoint[] = [...(options.waypoints ?? [])];
  const position = options.sourcePosition ?? Position.Bottom;
  const vertical = position === Position.Bottom || position === Position.Top;
  const flowSign =
    position === Position.Bottom || position === Position.Right ? 1 : -1;
  const a = (p: XY) => (vertical ? p.y : p.x);
  const b = (p: XY) => (vertical ? p.x : p.y);
  const at = (alongValue: number, crossValue: number): XY =>
    vertical
      ? { x: crossValue, y: alongValue }
      : { x: alongValue, y: crossValue };

  if (waypoints.length === 0) {
    const probe = curveSegments(options);
    const start = probe[0]!.from;
    const finish = probe[probe.length - 1]!.to;
    if (
      tooTight(
        (a(finish) - a(start)) * flowSign,
        Math.abs(b(finish) - b(start))
      )
    ) {
      waypoints.push(...loopPoints(start, finish, vertical, flowSign, ends));
    } else if (firstHit(probe, obstacles, margin)) {
      // An edge that the layout did not route, such as one of a card that
      // the user moved: a straight run in the nearest free column.
      const column = columnRoute(
        options,
        obstacles,
        margin,
        start,
        finish,
        ends
      );
      if (column) {
        return column;
      }
    }
  }
  if (obstacles.length === 0) {
    return waypoints;
  }

  for (let round = 0; round < MAX_DETOURS; round++) {
    const segments = curveSegments({ ...options, waypoints });
    const hit = firstHit(segments, obstacles, margin);
    if (!hit) {
      break;
    }
    // A U turn has no straight way to detour: it keeps its curve.
    if (
      (hit.index === 0 ? undefined : waypoints[hit.index - 1]?.heading) !==
      waypoints[hit.index]?.heading
    ) {
      break;
    }
    const segment = segments[hit.index]!;
    const from = segment.from;
    const to = segment.to;
    const box = hit.box;
    const low = vertical ? box.y : box.x;
    const high = low + (vertical ? box.height : box.width);
    const lowSide = (vertical ? box.x : box.y) - margin - SIDE_GAP;
    const highSide =
      (vertical ? box.x + box.width : box.y + box.height) + margin + SIDE_GAP;
    const centre = (lowSide + highSide) / 2;
    const side = (b(from) + b(to)) / 2 < centre ? lowSide : highSide;
    // A straight run between two waypoints moves aside as a whole: the
    // curves before and after it keep their room.
    if (
      segment.kind === 'line' &&
      hit.index >= 1 &&
      hit.index < waypoints.length
    ) {
      for (const j of [hit.index - 1, hit.index]) {
        const moved = waypoints[j]!;
        waypoints[j] = { ...moved, ...at(a(moved), side) };
      }
      continue;
    }
    const forward = a(to) >= a(from);
    const lo = Math.min(a(from), a(to));
    const hi = Math.max(a(from), a(to));
    const first = forward ? low - margin - LEAD : high + margin + LEAD;
    const last = forward ? high + margin + LEAD : low - margin - LEAD;
    // The curve needs room before the card, and after it, for a smooth
    // turn. A card too near an end of the segment leaves none: the curve
    // keeps its way and does not get a corner.
    const roomFrom = forward ? first - lo : hi - first;
    const roomTo = forward ? hi - last : last - lo;
    if (
      roomFrom < roomFor(side - b(from)) ||
      roomTo < roomFor(side - b(to)) ||
      Math.abs(first - last) < 1
    ) {
      break;
    }
    // A detour of a loop keeps the heading of its run.
    const heading = flowSign > 0 === forward ? undefined : ('back' as const);
    const pair: Waypoint[] = [at(first, side), at(last, side)].map((p) =>
      heading ? { ...p, heading } : p
    );
    waypoints.splice(hit.index, 0, ...pair);
  }
  return mergeNear(waypoints, a, b, at);
}

/** Waypoints that are closer than this on the cross axis are on one line, in px. */
const MERGE_CROSS = 8;

/**
 * A shifted run can end beside the next waypoint: the two get the same cross
 * position, so that the curve between them is not a wobble of a few px.
 */
function mergeNear(
  waypoints: Waypoint[],
  a: (p: XY) => number,
  b: (p: XY) => number,
  at: (alongValue: number, crossValue: number) => XY
): Waypoint[] {
  for (let i = 1; i < waypoints.length; i++) {
    const prev = waypoints[i - 1]!;
    const point = waypoints[i]!;
    if (
      !prev.hold &&
      !point.hold &&
      prev.heading === point.heading &&
      b(prev) !== b(point) &&
      Math.abs(b(prev) - b(point)) < MERGE_CROSS
    ) {
      waypoints[i] = { ...point, ...at(a(point), b(prev)) };
    }
  }
  return waypoints;
}

/**
 * The segments of an edge that goes around the cards: the curve through its
 * layout waypoints, with the loop and the detours that `routeAround` adds.
 */
export function routedCurve(
  options: CurveOptions,
  obstacles: readonly Obstacle[],
  margin = CARD_MARGIN,
  ends?: EdgeEnds
): CurveSegment[] {
  return curveSegments({
    ...options,
    waypoints: routeAround(options, obstacles, margin, ends),
  });
}
