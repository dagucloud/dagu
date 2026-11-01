// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  backgroundDots,
  boundsOf,
  clampPanToContent,
  fitViewport,
  curveSegments,
  getCurvePath,
  HANDLE_RADIUS,
  sampleCurve,
  nearestHandle,
  Position,
  screenToFlow,
  visibleRect,
  zoomAt,
  type PlacedHandle,
} from '../geometry';

describe('boundsOf', () => {
  it('wraps all the rectangles, and is null for none', () => {
    expect(boundsOf([])).toBeNull();
    expect(
      boundsOf([
        { x: 0, y: 0, width: 10, height: 10 },
        { x: 20, y: -5, width: 10, height: 10 },
      ])
    ).toEqual({ x: 0, y: -5, width: 30, height: 15 });
  });
});

describe('fitViewport', () => {
  const size = { width: 1000, height: 500 };

  it('centers the content and leaves the padding', () => {
    const view = fitViewport({ x: 0, y: 0, width: 400, height: 100 }, size, {
      padding: 0.25,
    });
    // 400 * 1.5 = 600 wide in 1000 px, 100 * 1.5 = 150 high in 500 px: the
    // fit is 1.67, and the largest step of the ladder under it is 150 %.
    expect(view.zoom).toBe(1.5);
    expect(view.x + 200 * view.zoom).toBeCloseTo(500);
    expect(view.y + 50 * view.zoom).toBeCloseTo(250);
  });

  it('never zooms past the maximum', () => {
    const view = fitViewport({ x: 0, y: 0, width: 10, height: 10 }, size, {
      maxZoom: 1.25,
    });
    expect(view.zoom).toBe(1.25);
  });

  it('never zooms below the minimum', () => {
    const view = fitViewport(
      { x: 0, y: 0, width: 100000, height: 100000 },
      size,
      { minZoom: 0.1 }
    );
    expect(view.zoom).toBe(0.1);
  });
});

describe('zoomAt', () => {
  it('keeps the flow point under the anchor in place', () => {
    const before = { x: 30, y: -20, zoom: 1 };
    const anchor = { x: 200, y: 120 };
    const flowPoint = screenToFlow(anchor, before);
    const after = zoomAt(before, 1.6, anchor);
    expect(after.zoom).toBe(1.6);
    const again = screenToFlow(anchor, after);
    expect(again.x).toBeCloseTo(flowPoint.x);
    expect(again.y).toBeCloseTo(flowPoint.y);
  });

  it('clamps to the limits', () => {
    const view = { x: 0, y: 0, zoom: 1 };
    expect(zoomAt(view, 50, { x: 0, y: 0 }).zoom).toBe(2);
    expect(zoomAt(view, 0.001, { x: 0, y: 0 }).zoom).toBe(0.1);
    expect(zoomAt(view, 5, { x: 0, y: 0 }, { max: 3 }).zoom).toBe(3);
  });
});

describe('visibleRect', () => {
  it('is the part of the flow in the area', () => {
    expect(
      visibleRect({ x: -100, y: -50, zoom: 2 }, { width: 400, height: 200 })
    ).toEqual({ x: 50, y: 25, width: 200, height: 100 });
  });
});

describe('getCurvePath', () => {
  const numbers = (path: string) =>
    [...path.matchAll(/-?\d+(?:\.\d+)?/g)].map((m) => Number(m[0]));

  it('draws one straight line between aligned handles', () => {
    const [path, labelX, labelY] = getCurvePath({
      sourceX: 10,
      sourceY: 0,
      sourcePosition: Position.Bottom,
      targetX: 10,
      targetY: 100,
      targetPosition: Position.Top,
    });
    // The end is on the outer edge of the dot, not on its centre.
    expect(path).toBe(`M10,0 L10,${100 - HANDLE_RADIUS}`);
    expect(labelX).toBe(10);
    expect(labelY).toBeCloseTo((100 - HANDLE_RADIUS) / 2, 5);
    const [lr] = getCurvePath({
      sourceX: 0,
      sourceY: 5,
      sourcePosition: Position.Right,
      targetX: 100,
      targetY: 5,
      targetPosition: Position.Left,
    });
    expect(lr).toBe(`M0,5 L${100 - HANDLE_RADIUS},5`);
  });

  it('ends on the outer circle of the entry dot, along the incoming side', () => {
    const tb = curveSegments({
      sourceX: 0,
      sourceY: 0,
      sourcePosition: Position.Bottom,
      targetX: 100,
      targetY: 96,
      targetPosition: Position.Top,
    });
    expect(tb[tb.length - 1]!.to).toEqual({ x: 100, y: 96 - HANDLE_RADIUS });
    const lr = curveSegments({
      sourceX: 0,
      sourceY: 0,
      sourcePosition: Position.Right,
      targetX: 96,
      targetY: 60,
      targetPosition: Position.Left,
    });
    expect(lr[lr.length - 1]!.to).toEqual({ x: 96 - HANDLE_RADIUS, y: 60 });
    // The tip is at the distance of the outer radius from the dot centre.
    const tip = lr[lr.length - 1]!.to;
    expect(Math.hypot(tip.x - 96, tip.y - 60)).toBe(HANDLE_RADIUS);
    // A connection that follows the pointer ends on it.
    const pointer = curveSegments({
      sourceX: 0,
      sourceY: 0,
      sourcePosition: Position.Bottom,
      targetX: 100,
      targetY: 96,
      targetPosition: Position.Top,
      targetGap: 0,
    });
    expect(pointer[pointer.length - 1]!.to).toEqual({ x: 100, y: 96 });
  });

  it('is a smooth curve with vertical tangents in TB, never an elbow', () => {
    const [path, labelX, labelY] = getCurvePath({
      sourceX: 0,
      sourceY: 0,
      sourcePosition: Position.Bottom,
      targetX: 100,
      targetY: 96,
      targetPosition: Position.Top,
    });
    expect(path).toMatch(/^M0,0 C0,\d/);
    expect(path).not.toMatch(/[AL]/);
    const [, , c1x, c1y, c2x, c2y, ex, ey] = numbers(path);
    // The first control point is straight under the source, the second one
    // straight above the end.
    expect(c1x).toBe(0);
    expect(c1y).toBeGreaterThan(0);
    expect(c2x).toBe(ex);
    expect(c2y).toBeLessThan(ey!);
    // The middle is half way across.
    expect(labelX).toBeCloseTo(50, 1);
    expect(labelY).toBeCloseTo(ey! / 2, 1);
  });

  it('has horizontal tangents in LR', () => {
    const [path] = getCurvePath({
      sourceX: 0,
      sourceY: 0,
      sourcePosition: Position.Right,
      targetX: 96,
      targetY: 60,
      targetPosition: Position.Left,
    });
    const [, , c1x, c1y, c2x, c2y, ex, ey] = numbers(path);
    expect(c1y).toBe(0);
    expect(c1x).toBeGreaterThan(0);
    expect(c2y).toBe(ey);
    expect(c2x).toBeLessThan(ex!);
  });

  it('starts exactly on the source', () => {
    const [path] = getCurvePath({
      sourceX: 12.5,
      sourceY: 3.25,
      sourcePosition: Position.Bottom,
      targetX: 200.75,
      targetY: 150.5,
      targetPosition: Position.Top,
    });
    expect(path.startsWith('M12.5,3.25 ')).toBe(true);
  });

  it('goes through the waypoints with a vertical tangent', () => {
    const segments = curveSegments({
      sourceX: 0,
      sourceY: 0,
      sourcePosition: Position.Bottom,
      targetX: 0,
      targetY: 400,
      targetPosition: Position.Top,
      waypoints: [
        { x: 150, y: 100 },
        { x: 150, y: 300 },
      ],
    });
    expect(segments.map((s) => s.kind)).toEqual(['cubic', 'line', 'cubic']);
    // The run between the two waypoints is straight and vertical.
    expect(segments[1]).toEqual({
      kind: 'line',
      from: { x: 150, y: 100 },
      to: { x: 150, y: 300 },
    });
    const points = sampleCurve(segments);
    expect(points.some((p) => p.x === 150 && p.y > 100 && p.y < 300)).toBe(
      true
    );
    expect(points[0]).toEqual({ x: 0, y: 0 });
  });

  it('keeps a curve that goes back against the flow finite', () => {
    const [path] = getCurvePath({
      sourceX: 0,
      sourceY: 200,
      sourcePosition: Position.Bottom,
      targetX: 80,
      targetY: 0,
      targetPosition: Position.Top,
    });
    expect(numbers(path).every(Number.isFinite)).toBe(true);
  });
});

describe('nearestHandle', () => {
  const handle = (nodeId: string, x: number, y: number): PlacedHandle => ({
    nodeId,
    handleId: 'target',
    type: 'target',
    position: Position.Left,
    x,
    y,
  });

  it('gives the closest handle within the radius', () => {
    const handles = [handle('a', 0, 0), handle('b', 30, 0), handle('c', 90, 0)];
    expect(nearestHandle(handles, { x: 26, y: 0 }, 20)?.nodeId).toBe('b');
    expect(nearestHandle(handles, { x: 200, y: 0 }, 20)).toBeNull();
  });

  it('gives the first of two equal distances', () => {
    const handles = [handle('a', -10, 0), handle('b', 10, 0)];
    expect(nearestHandle(handles, { x: 0, y: 0 }, 20)?.nodeId).toBe('a');
  });
});

describe('readable fit from the left', () => {
  it('fits the height and starts at the left of a wide content', () => {
    const view = fitViewport(
      { x: 100, y: 0, width: 20000, height: 200 },
      { width: 1000, height: 600 },
      { padding: 0.1, minZoom: 0.02, readableZoom: 0.5, readableFrom: 'left' }
    );
    // The height fit is capped at 1, as the width fit of the top fit.
    expect(view.zoom).toBe(1);
    expect(view.x).toBe(24 - 100);
    // The short side stays centred.
    expect(view.y).toBe(300 - 100);
  });
});

describe('clampPanToContent', () => {
  const bounds = { x: 0, y: 0, width: 200, height: 1000 };
  const size = { width: 800, height: 600 };

  it('leaves a view that shows the content as it is', () => {
    const view = { x: 10, y: -200, zoom: 1 };
    expect(clampPanToContent(view, bounds, size)).toBe(view);
  });

  it('keeps a margin of the content on screen at each edge', () => {
    const far = clampPanToContent({ x: 0, y: -5000, zoom: 1 }, bounds, size);
    // The content ends 96 px below the top edge.
    expect(far.y).toBe(96 - 1000);
    const below = clampPanToContent({ x: 0, y: 9000, zoom: 1 }, bounds, size);
    expect(below.y).toBe(600 - 96);
    const right = clampPanToContent({ x: 9000, y: 0, zoom: 1 }, bounds, size);
    expect(right.x).toBe(800 - 96);
    const left = clampPanToContent({ x: -9000, y: 0, zoom: 1 }, bounds, size);
    expect(left.x).toBe(96 - 200);
  });

  it('does nothing without content or size', () => {
    const view = { x: -9000, y: -9000, zoom: 1 };
    expect(clampPanToContent(view, null, size)).toBe(view);
    expect(clampPanToContent(view, bounds, { width: 0, height: 0 })).toBe(view);
  });
});

describe('backgroundDots', () => {
  it('keeps the dots at a normal zoom and fades them out when close', () => {
    expect(backgroundDots(16, 1)).toEqual({ step: 16, opacity: 1 });
    expect(backgroundDots(16, 0.1).opacity).toBe(0);
    const mid = backgroundDots(16, 0.4).opacity;
    expect(mid).toBeGreaterThan(0);
    expect(mid).toBeLessThan(1);
  });
});
