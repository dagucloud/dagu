// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { Rect } from '../engine/geometry';
import { keepInViewDelta, REVEAL_ZONE, revealDelta } from '../revealDelta';

const view: Rect = { x: 0, y: 0, width: 1000, height: 600 };
const none = { top: 0, right: 0, bottom: 0, left: 0 };
const card = (x: number, y: number, scale = 1): Rect => ({
  x,
  y,
  width: 200 * scale,
  height: 80 * scale,
});

// The rect of a card after a pan.
function moved(rect: Rect, delta: { dx: number; dy: number }): Rect {
  return { ...rect, x: rect.x + delta.dx, y: rect.y + delta.dy };
}

describe('revealDelta', () => {
  it('does not pan a card in the central zone', () => {
    expect(revealDelta(card(400, 260), view, none)).toEqual({ dx: 0, dy: 0 });
    // Off the exact centre but still inside the zone.
    expect(revealDelta(card(250, 150), view, none)).toEqual({ dx: 0, dy: 0 });
  });

  it('pans the least distance for a card visible near the right edge', () => {
    const rect = card(780, 260);
    const delta = revealDelta(rect, view, none);
    // The centre goes to the edge of the zone: 500 + 0.3 * 1000.
    expect(delta.dy).toBe(0);
    expect(moved(rect, delta).x + 100).toBeCloseTo(
      500 + (1000 * REVEAL_ZONE) / 2,
      6
    );
    expect(delta.dx).toBeLessThan(0);
    expect(delta.dx).toBeGreaterThan(-100);
  });

  it('pans the least distance for a card near the top edge', () => {
    const rect = card(400, 5);
    const delta = revealDelta(rect, view, none);
    expect(delta.dx).toBe(0);
    expect(moved(rect, delta).y + 40).toBeCloseTo(300 - 600 * 0.3, 6);
  });

  it('keeps a wide card whole in the area when its centre is in the zone', () => {
    const wide: Rect = { x: 100, y: 260, width: 780, height: 80 };
    const next = moved(wide, revealDelta(wide, view, none));
    expect(next.x).toBeGreaterThanOrEqual(0);
    expect(next.x + next.width).toBeLessThanOrEqual(1000);
  });

  it('centres a card that is off screen', () => {
    const rect = card(2400, -900);
    const next = moved(rect, revealDelta(rect, view, none));
    expect(next.x + 100).toBeCloseTo(500, 6);
    expect(next.y + 40).toBeCloseTo(300, 6);
  });

  it('centres a card that is off screen on one axis only', () => {
    const rect = card(400, 1500);
    const delta = revealDelta(rect, view, none);
    expect(delta.dx).toBe(0);
    expect(moved(rect, delta).y + 40).toBeCloseTo(300, 6);
  });

  it('pans a card that a dock or a panel hides', () => {
    // A panel covers the right 400 px: the area is 0..600.
    const panel = { ...none, right: 400 };
    const rect = card(700, 260);
    const next = moved(rect, revealDelta(rect, view, panel));
    expect(next.x + 100).toBeCloseTo(300, 6);
    // A dock covers the bottom 250 px: the area is 0..350.
    const dock = { ...none, bottom: 250 };
    const low = card(400, 400);
    const after = moved(low, revealDelta(low, view, dock));
    expect(after.y + 40).toBeCloseTo(175, 6);
  });

  it('uses the area that the obstructions leave free for the zone', () => {
    // Area 0..600: the zone is 120..480. A card centred at 450 stays.
    const panel = { ...none, right: 400 };
    expect(revealDelta(card(350, 260), view, panel).dx).toBe(0);
    // Centred at 520: visible, but outside the zone.
    expect(revealDelta(card(420, 260), view, panel).dx).toBeLessThan(0);
  });

  it('centres a card that is larger than the area', () => {
    const tiny: Rect = { x: 0, y: 0, width: 120, height: 60 };
    const rect = card(300, 200);
    const next = moved(rect, revealDelta(rect, tiny, none));
    expect(next.x + 100).toBeCloseTo(60, 6);
    expect(next.y + 40).toBeCloseTo(30, 6);
  });

  it('survives obstructions larger than the canvas', () => {
    const delta = revealDelta(card(300, 200), view, {
      top: 700,
      right: 1200,
      bottom: 0,
      left: 0,
    });
    expect(Number.isFinite(delta.dx)).toBe(true);
    expect(Number.isFinite(delta.dy)).toBe(true);
  });

  // The card size follows the zoom: the rule is the same in px.
  it.each([0.25, 2])('works at zoom %s', (zoom) => {
    const centred = card(500 - 100 * zoom, 300 - 40 * zoom, zoom);
    expect(revealDelta(centred, view, none)).toEqual({ dx: 0, dy: 0 });
    // Centre at 900: outside the zone (200..800), whatever the card size.
    const edge = card(900 - 100 * zoom, 300 - 40 * zoom, zoom);
    const delta = revealDelta(edge, view, none);
    expect(delta.dx).toBeLessThan(0);
    expect(moved(edge, delta).x + 100 * zoom).toBeLessThanOrEqual(800 + 1e-6);
  });
});

describe('keepInViewDelta', () => {
  const area = { top: 48, right: 24, bottom: 48, left: 24 };
  const graph = (x: number, y: number, width = 400, height = 300): Rect => ({
    x,
    y,
    width,
    height,
  });

  it('does not pan a graph that is in the free area', () => {
    expect(keepInViewDelta(graph(24, 48), view, area)).toEqual({
      dx: 0,
      dy: 0,
    });
    expect(keepInViewDelta(graph(500, 200), view, area)).toEqual({
      dx: 0,
      dy: 0,
    });
  });

  it('pans the least distance for a graph that the right edge cuts', () => {
    // The free area ends at 976: the graph ends at 1100.
    expect(keepInViewDelta(graph(700, 200), view, area)).toEqual({
      dx: -124,
      dy: 0,
    });
  });

  it('pans the least distance for a graph above or at the left of the area', () => {
    expect(keepInViewDelta(graph(-40, 10), view, area)).toEqual({
      dx: 64,
      dy: 38,
    });
  });

  // The graph is longer than the area: its start side stays, and no part of
  // the area stays empty at the other side.
  it('keeps the start of a graph that is longer than the area', () => {
    const long = graph(24, 48, 1200, 900);
    expect(keepInViewDelta(long, view, area)).toEqual({ dx: 0, dy: 0 });
    // A gap at the start of more than 24 px closes.
    expect(keepInViewDelta(graph(300, 48, 1200, 300), view, area)).toEqual({
      dx: -252,
      dy: 0,
    });
    // A graph that has moved up stays while it covers the area.
    expect(keepInViewDelta(graph(24, -200, 400, 900), view, area)).toEqual({
      dx: 0,
      dy: 0,
    });
    // It does not leave an empty strip at its end.
    expect(keepInViewDelta(graph(24, -500, 400, 900), view, area)).toEqual({
      dx: 0,
      dy: 152,
    });
  });
});
