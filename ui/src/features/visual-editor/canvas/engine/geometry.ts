// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { HandleType, Viewport, XY } from './types';

/** Side of a node where a handle sits. */
export enum Position {
  Left = 'left',
  Right = 'right',
  Top = 'top',
  Bottom = 'bottom',
}

export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export const MIN_ZOOM = 0.1;
export const MAX_ZOOM = 2;

/**
 * The zoom levels, as fractions: 5, 10, 25, 50, 75, 100, 125, 150, 200, 300
 * and 400 %. A zoom command moves one step on this ladder, and a fit lands
 * on a step, so that the zoom is always a round percentage.
 */
export const ZOOM_LADDER: readonly number[] = [
  0.05, 0.1, 0.25, 0.5, 0.75, 1, 1.25, 1.5, 2, 3, 4,
];

/** Two zooms closer than this are the same step. */
const ZOOM_EPSILON = 1e-6;

export interface ZoomLimits {
  min?: number;
  max?: number;
}

/**
 * The steps of the ladder inside the limits of the engine. The ladder keeps
 * at least one step: the one nearest to 100 % when no step fits.
 */
export function zoomSteps({
  min = MIN_ZOOM,
  max = MAX_ZOOM,
}: ZoomLimits = {}): number[] {
  const steps = ZOOM_LADDER.filter(
    (step) => step >= min - ZOOM_EPSILON && step <= max + ZOOM_EPSILON
  );
  return steps.length > 0 ? steps : [clamp(1, min, max)];
}

/**
 * The largest step that is not above `zoom`. A zoom under the smallest step
 * gives the smallest step.
 */
export function floorZoom(zoom: number, limits?: ZoomLimits): number {
  const steps = zoomSteps(limits);
  let floor = steps[0]!;
  for (const step of steps) {
    if (step <= zoom + ZOOM_EPSILON) {
      floor = step;
    }
  }
  return floor;
}

/** The step nearest to `zoom`; between two steps, the larger one. */
export function snapZoom(zoom: number, limits?: ZoomLimits): number {
  const steps = zoomSteps(limits);
  let nearest = steps[0]!;
  for (const step of steps) {
    if (Math.abs(step - zoom) <= Math.abs(nearest - zoom) + ZOOM_EPSILON) {
      nearest = step;
    }
  }
  return nearest;
}

/**
 * The step after `zoom` (`direction` 1) or before it (-1). A zoom between
 * two steps snaps to the next one in that direction. At the end of the
 * ladder the zoom stays on the last step.
 */
export function stepZoom(
  zoom: number,
  direction: 1 | -1,
  limits?: ZoomLimits
): number {
  const steps = zoomSteps(limits);
  if (direction === 1) {
    return (
      steps.find((step) => step > zoom + ZOOM_EPSILON) ??
      steps[steps.length - 1]!
    );
  }
  for (let i = steps.length - 1; i >= 0; i--) {
    if (steps[i]! < zoom - ZOOM_EPSILON) {
      return steps[i]!;
    }
  }
  return steps[0]!;
}

/** Size of a wheel or pinch move for one step, in octaves of zoom. */
const WHEEL_STEP = 0.2;

/** A pause that makes the next wheel move start from zero, in ms. */
const WHEEL_IDLE_MS = 250;

/** The pixels of one unit of a wheel delta, by `deltaMode`: a line or a page. */
export function wheelDeltaUnit(deltaMode: number): number {
  return deltaMode === 1 ? 16 : deltaMode === 2 ? 400 : 1;
}

/**
 * Turns the deltas of a wheel or of a pinch into steps of the ladder. Each
 * call gives 1, -1 or 0: the deltas add up, and a step starts when their
 * sum reaches one notch of a wheel. One call never gives more than one
 * step, and a pause, or a move the other way, restarts the sum.
 */
export function createWheelStepper(): (
  octaves: number,
  time: number
) => 1 | -1 | 0 {
  let sum = 0;
  let last = -Infinity;
  return (octaves, time) => {
    if (time - last > WHEEL_IDLE_MS || Math.sign(octaves) !== Math.sign(sum)) {
      sum = 0;
    }
    last = time;
    sum += octaves;
    if (Math.abs(sum) < WHEEL_STEP - ZOOM_EPSILON) {
      return 0;
    }
    const step = sum > 0 ? 1 : -1;
    sum = 0;
    return step;
  };
}

export function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

/** Smallest rectangle around the rectangles, or null for none. */
export function boundsOf(rects: readonly Rect[]): Rect | null {
  if (rects.length === 0) {
    return null;
  }
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const r of rects) {
    minX = Math.min(minX, r.x);
    minY = Math.min(minY, r.y);
    maxX = Math.max(maxX, r.x + r.width);
    maxY = Math.max(maxY, r.y + r.height);
  }
  return { x: minX, y: minY, width: maxX - minX, height: maxY - minY };
}

export interface FitOptions {
  /** Free space around the content, as a fraction of the content size. */
  padding?: number;
  maxZoom?: number;
  minZoom?: number;
  /**
   * A fit does not shrink the content below this zoom. When the whole
   * content would need less, the fit shows its top at the width of the
   * content, and the rest is reached by panning.
   */
  readableZoom?: number;
  /**
   * Which end the readable fit shows: 'top' (default) fits the width and
   * starts at the top, 'left' fits the height and starts at the left.
   */
  readableFrom?: 'top' | 'left';
  /**
   * Lowest zoom of the readable fit when the width of the content is below
   * `readableZoom` too. Default: 70 % of `readableZoom`.
   */
  readableFloor?: number;
  /**
   * With `readableZoom`: the fit of a graph that is too long for it uses
   * `readableZoom` itself, an overview of the structure, and not the zoom
   * that fits the width. Without it, a narrow graph stays at the zoom of
   * its width, which can be 100 %.
   */
  overview?: boolean;
  /**
   * Space in px that the fit keeps free on each side, for the overlays of
   * the canvas. The content is fitted and centred in the rest of the area.
   */
  inset?: Partial<Insets>;
}

export interface Insets {
  top: number;
  right: number;
  bottom: number;
  left: number;
}

/** Space above the content of a fit that keeps the top readable, in px. */
const TOP_MARGIN = 24;

/** Default floor of the readable fit, as a part of the readable zoom. */
const READABLE_FLOOR_RATIO = 0.7;

/**
 * The viewport that shows all of `bounds` in a `size` area. The zoom is a
 * step of `ZOOM_LADDER`: the largest one that fits the content.
 */
export function fitViewport(
  bounds: Rect,
  size: { width: number; height: number },
  {
    padding = 0.1,
    maxZoom = MAX_ZOOM,
    minZoom = MIN_ZOOM,
    readableZoom,
    readableFrom = 'top',
    readableFloor,
    overview = false,
    inset,
  }: FitOptions = {}
): Viewport {
  const { top = 0, right = 0, bottom = 0, left = 0 } = inset ?? {};
  const area = {
    width: Math.max(size.width - left - right, 1),
    height: Math.max(size.height - top - bottom, 1),
  };
  const centreX = left + area.width / 2;
  const centreY = top + area.height / 2;
  const spanX = Math.max(bounds.width, 1) * (1 + 2 * padding);
  const spanY = Math.max(bounds.height, 1) * (1 + 2 * padding);
  const limits = { min: minZoom, max: maxZoom };
  const zoom = clamp(
    Math.min(area.width / spanX, area.height / spanY),
    minZoom,
    maxZoom
  );
  if (readableZoom !== undefined && zoom < readableZoom) {
    // The graph is long on one axis: it opens at a readable zoom on its
    // start, and the rest is reached by panning. A graph that is also wide
    // keeps a floor, so that the zoom never falls to a stripe that nobody
    // can read.
    const cross = Math.min(
      readableFrom === 'left' ? area.height / spanY : area.width / spanX,
      1
    );
    const floor = readableFloor ?? readableZoom * READABLE_FLOOR_RATIO;
    const readable = floorZoom(
      clamp(
        overview
          ? readableZoom
          : cross >= readableZoom
            ? cross
            : Math.max(cross, floor),
        minZoom,
        maxZoom
      ),
      limits
    );
    if (readableFrom === 'left') {
      const wide = bounds.height * readable > area.height;
      return {
        zoom: readable,
        x: left + TOP_MARGIN - bounds.x * readable,
        y: wide
          ? top + TOP_MARGIN - bounds.y * readable
          : centreY - (bounds.y + bounds.height / 2) * readable,
      };
    }
    // A content wider than the area starts at its left edge, so that no
    // branch is cut on the start side.
    const wide = bounds.width * readable > area.width;
    return {
      zoom: readable,
      x: wide
        ? left + TOP_MARGIN - bounds.x * readable
        : centreX - (bounds.x + bounds.width / 2) * readable,
      y: (inset?.top ?? TOP_MARGIN) - bounds.y * readable,
    };
  }
  // The largest step that shows all the content, never under the first step.
  const step = floorZoom(zoom, limits);
  return {
    zoom: step,
    x: centreX - (bounds.x + bounds.width / 2) * step,
    y: centreY - (bounds.y + bounds.height / 2) * step,
  };
}

/**
 * Moves the view so that the content stays reachable: at least `margin` px
 * of its bounds stay on screen on each axis. A view that does so is returned
 * as it is.
 */
export function clampPanToContent(
  viewport: Viewport,
  bounds: Rect | null,
  size: { width: number; height: number },
  margin = 96
): Viewport {
  if (!bounds || size.width === 0 || size.height === 0) {
    return viewport;
  }
  const { zoom } = viewport;
  const keepX = Math.min(margin, bounds.width * zoom);
  const keepY = Math.min(margin, bounds.height * zoom);
  const x = clamp(
    viewport.x,
    keepX - (bounds.x + bounds.width) * zoom,
    size.width - keepX - bounds.x * zoom
  );
  const y = clamp(
    viewport.y,
    keepY - (bounds.y + bounds.height) * zoom,
    size.height - keepY - bounds.y * zoom
  );
  return x === viewport.x && y === viewport.y ? viewport : { zoom, x, y };
}

/**
 * Dot grid of the background for a zoom: the cell size in px and the opacity.
 * The dots fade out when they get too close, so that a small zoom keeps the
 * same background and does not turn it grey.
 */
export function backgroundDots(
  gap: number,
  zoom: number
): { step: number; opacity: number } {
  const step = gap * zoom;
  return { step, opacity: clamp((step - 4) / 6, 0, 1) };
}

/** Zooms to `zoom` and keeps the flow point under `anchor` (screen) in place. */
export function zoomAt(
  viewport: Viewport,
  zoom: number,
  anchor: XY,
  limits: { min?: number; max?: number } = {}
): Viewport {
  const next = clamp(zoom, limits.min ?? MIN_ZOOM, limits.max ?? MAX_ZOOM);
  const ratio = next / viewport.zoom;
  return {
    zoom: next,
    x: anchor.x - (anchor.x - viewport.x) * ratio,
    y: anchor.y - (anchor.y - viewport.y) * ratio,
  };
}

/** Flow coordinates of a screen point, relative to the canvas element. */
export function screenToFlow(point: XY, viewport: Viewport): XY {
  return {
    x: (point.x - viewport.x) / viewport.zoom,
    y: (point.y - viewport.y) / viewport.zoom,
  };
}

/** The part of the flow that a viewport shows in a `size` area. */
export function visibleRect(
  viewport: Viewport,
  size: { width: number; height: number }
): Rect {
  return {
    x: -viewport.x / viewport.zoom,
    y: -viewport.y / viewport.zoom,
    width: size.width / viewport.zoom,
    height: size.height / viewport.zoom,
  };
}

/**
 * Outer radius of the dot of a handle, in px: the 8 px dot of a node. An
 * edge ends on this circle, from outside, so that its arrowhead touches the
 * dot and does not cover it.
 */
export const HANDLE_RADIUS = 4;

/** Two points closer than this on the cross axis are in line, in px. */
const ALIGNED_EPSILON = 0.5;

/** Shortest pull of a curve that goes back against the flow, in px. */
const BACK_PULL = 24;

/**
 * Longest pull of a bend, as a part of the distance along the flow. Above 1
 * the S wobbles a little, and it stays inside the space between its points.
 */
const MAX_PULL = 1.4;

/** Smallest radius in px that a bend of a curve keeps, when the room allows it. */
export const CURVE_RADIUS = 18;

/**
 * Room along the flow, in px, that a smooth S needs to move `cross` px to
 * the side with no bend sharper than `radius`.
 */
export function roomFor(cross: number, radius = CURVE_RADIUS): number {
  return Math.sqrt((2 * Math.abs(cross) * radius) / 3);
}

/**
 * The widest sideways move, in px, that a smooth S can make in `delta` px
 * along the flow with no bend under `radius`.
 */
export function widestMove(delta: number, radius: number): number {
  return (3 * (delta * MAX_PULL) ** 2) / (2 * radius);
}

/** A point that the curve crosses; `back` means against the flow. */
export interface Waypoint extends XY {
  heading?: 'back';
  /** A junction of a bundle: the routing keeps its place and does not merge it. */
  hold?: boolean;
}

export interface CurveOptions {
  sourceX: number;
  sourceY: number;
  sourcePosition?: Position;
  targetX: number;
  targetY: number;
  /** Side of the target that the edge enters. Default: the opposite of the source side. */
  targetPosition?: Position;
  /**
   * Points that the curve crosses between its ends, in the order of the
   * path. Each one is crossed along the flow, so a pair of points on one
   * line along the flow gives a straight run, and the bends of the curve
   * stay between the pairs. A point with `heading: 'back'` is crossed
   * against the flow: the curve makes a U turn to reach it.
   */
  waypoints?: readonly Waypoint[];
  /**
   * Distance in px between the end of the path and the target point, on the
   * side that the edge enters. Default `HANDLE_RADIUS`: the path ends on
   * the outer edge of the dot. A connection that follows the pointer uses 0.
   */
  targetGap?: number;
}

/** A piece of a curve: a straight line or a cubic Bezier. */
export type CurveSegment =
  | { kind: 'line'; from: XY; to: XY }
  | { kind: 'cubic'; from: XY; c1: XY; c2: XY; to: XY };

function oppositeSide(position: Position): Position {
  switch (position) {
    case Position.Left:
      return Position.Right;
    case Position.Right:
      return Position.Left;
    case Position.Top:
      return Position.Bottom;
    case Position.Bottom:
      return Position.Top;
  }
}

/** Unit vector that points out of a node at a side. */
function outward(position: Position): XY {
  switch (position) {
    case Position.Left:
      return { x: -1, y: 0 };
    case Position.Right:
      return { x: 1, y: 0 };
    case Position.Top:
      return { x: 0, y: -1 };
    case Position.Bottom:
      return { x: 0, y: 1 };
  }
}

/**
 * The segments of the curve of an edge. Between two points the curve leaves
 * along the flow and arrives along the flow, as a smooth S: vertical tangents
 * from a bottom or top handle, horizontal ones from a left or right handle.
 * The S gets as much room along the flow as it needs for a bend that is not
 * sharp, up to the distance between its two points. Two points in line give
 * a straight segment. The curve starts on the source handle and ends
 * `targetGap` px before the target handle.
 */
export function curveSegments({
  sourceX,
  sourceY,
  sourcePosition = Position.Bottom,
  targetX,
  targetY,
  targetPosition = oppositeSide(sourcePosition),
  waypoints = [],
  targetGap = HANDLE_RADIUS,
}: CurveOptions): CurveSegment[] {
  const out = outward(targetPosition);
  const end: XY = {
    x: targetX + out.x * targetGap,
    y: targetY + out.y * targetGap,
  };
  const flow = outward(sourcePosition);
  const vertical = flow.y !== 0;
  const along = (p: XY) => (vertical ? p.y * flow.y : p.x * flow.x);
  const across = (p: XY) => (vertical ? p.x : p.y);
  const points: Waypoint[] = [{ x: sourceX, y: sourceY }, ...waypoints, end];
  const segments: CurveSegment[] = [];
  for (let i = 0; i + 1 < points.length; i++) {
    const from = points[i]!;
    const to = points[i + 1]!;
    const dirFrom = from.heading === 'back' ? -1 : 1;
    const dirTo = to.heading === 'back' ? -1 : 1;
    const gap = Math.abs(across(from) - across(to));
    if (dirFrom !== dirTo) {
      // A U turn: the curve leaves one way and arrives the other way.
      const h = clamp(0.67 * gap, 20, 70);
      segments.push({
        kind: 'cubic',
        from,
        c1: {
          x: from.x + flow.x * dirFrom * h,
          y: from.y + flow.y * dirFrom * h,
        },
        c2: { x: to.x - flow.x * dirTo * h, y: to.y - flow.y * dirTo * h },
        to,
      });
      continue;
    }
    if (gap < ALIGNED_EPSILON) {
      segments.push({ kind: 'line', from, to });
      continue;
    }
    const delta = (along(to) - along(from)) * dirFrom;
    const pull =
      delta >= 0
        ? Math.min(Math.max(delta / 2, roomFor(gap)), delta * MAX_PULL)
        : BACK_PULL + 6 * Math.sqrt(-delta);
    segments.push({
      kind: 'cubic',
      from,
      c1: {
        x: from.x + flow.x * dirFrom * pull,
        y: from.y + flow.y * dirFrom * pull,
      },
      c2: { x: to.x - flow.x * dirTo * pull, y: to.y - flow.y * dirTo * pull },
      to,
    });
  }
  return segments;
}

function pointOf(segment: CurveSegment, t: number): XY {
  if (segment.kind === 'line') {
    return {
      x: segment.from.x + (segment.to.x - segment.from.x) * t,
      y: segment.from.y + (segment.to.y - segment.from.y) * t,
    };
  }
  const u = 1 - t;
  const k0 = u * u * u;
  const k1 = 3 * u * u * t;
  const k2 = 3 * u * t * t;
  const k3 = t * t * t;
  return {
    x:
      k0 * segment.from.x +
      k1 * segment.c1.x +
      k2 * segment.c2.x +
      k3 * segment.to.x,
    y:
      k0 * segment.from.y +
      k1 * segment.c1.y +
      k2 * segment.c2.y +
      k3 * segment.to.y,
  };
}

/**
 * Points on the curve, about `step` px apart, with the two ends. Used to
 * test a curve against boxes.
 */
export function sampleCurve(segments: readonly CurveSegment[], step = 8): XY[] {
  const out: XY[] = [];
  for (const segment of segments) {
    const chord = Math.hypot(
      segment.to.x - segment.from.x,
      segment.to.y - segment.from.y
    );
    const count = Math.max(1, Math.ceil(chord / step));
    for (let i = 0; i < count; i++) {
      out.push(pointOf(segment, i / count));
    }
  }
  const last = segments[segments.length - 1];
  if (last) {
    out.push(last.to);
  }
  return out;
}

function lengthOf(segment: CurveSegment): number {
  if (segment.kind === 'line') {
    return Math.hypot(
      segment.to.x - segment.from.x,
      segment.to.y - segment.from.y
    );
  }
  let length = 0;
  let prev = segment.from;
  for (let i = 1; i <= 8; i++) {
    const next = pointOf(segment, i / 8);
    length += Math.hypot(next.x - prev.x, next.y - prev.y);
    prev = next;
  }
  return length;
}

/** The point at `fraction` (0 to 1) of the length of the curve. */
export function curvePointAt(
  segments: readonly CurveSegment[],
  fraction: number
): XY {
  const lengths = segments.map(lengthOf);
  const total = lengths.reduce((sum, l) => sum + l, 0);
  let left = clamp(fraction, 0, 1) * total;
  for (let i = 0; i < segments.length; i++) {
    const length = lengths[i]!;
    if (left <= length || i === segments.length - 1) {
      return pointOf(segments[i]!, length > 0 ? clamp(left / length, 0, 1) : 0);
    }
    left -= length;
  }
  return { x: 0, y: 0 };
}

/** SVG path data of the segments, to 2 decimals. */
export function curvePathData(segments: readonly CurveSegment[]): string {
  const n = (v: number) => String(Math.round(v * 100) / 100);
  const first = segments[0];
  if (!first) {
    return '';
  }
  let d = `M${n(first.from.x)},${n(first.from.y)}`;
  for (const segment of segments) {
    d +=
      segment.kind === 'line'
        ? ` L${n(segment.to.x)},${n(segment.to.y)}`
        : ` C${n(segment.c1.x)},${n(segment.c1.y)} ${n(segment.c2.x)},${n(segment.c2.y)} ${n(segment.to.x)},${n(segment.to.y)}`;
  }
  return d;
}

/**
 * Path of an edge: a smooth curve between two handles, through the
 * waypoints. Returns the path and the point for labels and buttons, which
 * is the middle of the path.
 */
export function getCurvePath(
  options: CurveOptions
): [path: string, labelX: number, labelY: number] {
  const segments = curveSegments(options);
  const middle = curvePointAt(segments, 0.5);
  return [curvePathData(segments), middle.x, middle.y];
}

/** A handle with its place in flow coordinates. */
export interface PlacedHandle {
  nodeId: string;
  handleId: string | null;
  type: HandleType;
  position: Position;
  x: number;
  y: number;
}

/**
 * The handle nearest to `point` that is within `radius`, or null. Ties go to
 * the first handle in the list, so the result is stable.
 */
export function nearestHandle(
  handles: readonly PlacedHandle[],
  point: XY,
  radius: number
): PlacedHandle | null {
  let best: PlacedHandle | null = null;
  let bestDistance = Infinity;
  for (const handle of handles) {
    const distance = Math.hypot(handle.x - point.x, handle.y - point.y);
    if (distance <= radius && distance < bestDistance) {
      best = handle;
      bestDistance = distance;
    }
  }
  return best;
}
