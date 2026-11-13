// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { CLEARANCE, clearance, type Block, type Rect } from '../clearance';

const box = (x: number, y: number, width: number, height: number): Rect => ({
  x,
  y,
  width,
  height,
});

// A sub-node of the step `owner` in `rank` that hangs out under its step.
const reach = (owner: string, rank: number, rect: Rect): Block => ({
  owner,
  top: rank,
  bottom: rank,
  rect,
  after: true,
});

const card = (owner: string, rank: number, rect: Rect): Block => ({
  owner,
  top: rank,
  bottom: rank,
  rect,
});

describe('clearance', () => {
  it('asks nothing when the sub-node touches no other block', () => {
    const found = clearance(false, [
      reach('a', 0, box(300, 0, 100, 300)),
      card('b', 1, box(0, 100, 100, 50)),
    ]);
    expect(found.gaps.size).toBe(0);
    expect(found.flips).toEqual([]);
  });

  it('grows the gap after the rank of the sub-node until the card of a later rank is clear', () => {
    const found = clearance(false, [
      reach('a', 0, box(0, 0, 100, 300)),
      card('b', 1, box(0, 100, 100, 50)),
    ]);
    expect(found.gaps.get(0)).toBe(300 + CLEARANCE - 100);
    expect(found.flips).toEqual([]);
  });

  it('counts a block that is before the sub-node now, as the column moves past it', () => {
    const found = clearance(false, [
      reach('a', 0, box(0, 100, 100, 200)),
      card('b', 1, box(0, 0, 100, 50)),
    ]);
    expect(found.gaps.get(0)).toBe(300 + CLEARANCE);
  });

  it('grows the gap before the rank of a sub-node that reaches up to a card of an earlier rank', () => {
    const found = clearance(false, [
      { ...reach('b', 1, box(0, 0, 100, 300)), after: false, before: true },
      card('a', 0, box(0, 250, 100, 50)),
    ]);
    expect(found.gaps.get(0)).toBe(300 + CLEARANCE - 0);
  });

  it('lets the first of two sub-nodes that reach out decide', () => {
    const found = clearance(false, [
      reach('a', 0, box(0, 0, 100, 300)),
      { ...reach('b', 1, box(0, 200, 100, 300)), before: true },
    ]);
    expect(found.gaps.size).toBe(1);
    expect(found.gaps.get(0)).toBe(300 + CLEARANCE - 200);
  });

  it('works along x when the flow is left to right', () => {
    const found = clearance(true, [
      reach('a', 0, box(0, 0, 300, 100)),
      card('b', 1, box(100, 0, 50, 100)),
    ]);
    expect(found.gaps.get(0)).toBe(300 + CLEARANCE - 100);
  });

  it('flips the side of a sub-node that touches a block of its own rank', () => {
    const found = clearance(false, [
      reach('a', 0, box(0, 0, 100, 300)),
      card('b', 0, box(0, 200, 100, 50)),
    ]);
    expect(found.gaps.size).toBe(0);
    expect(found.flips).toEqual([{ owner: 'a', side: 'after' }]);
  });

  it('flips the side of a sub-node that touches an edge between its rank and the next', () => {
    const found = clearance(false, [
      reach('a', 0, box(0, 0, 100, 300)),
      { owner: null, top: 0, bottom: 1, rect: box(0, 280, 200, 60) },
    ]);
    expect(found.flips).toEqual([{ owner: 'a', side: 'after' }]);
  });

  it('keeps an edge of a farther rank apart by growing the gap', () => {
    const found = clearance(false, [
      reach('a', 0, box(0, 0, 100, 300)),
      { owner: null, top: 1, bottom: 2, rect: box(0, 280, 200, 60) },
    ]);
    expect(found.flips).toEqual([]);
    expect(found.gaps.get(0)).toBe(300 + CLEARANCE - 280);
  });

  it('ignores the blocks of its own step', () => {
    const found = clearance(false, [
      reach('a', 0, box(0, 0, 100, 300)),
      card('a', 0, box(0, 100, 100, 50)),
    ]);
    expect(found.gaps.size).toBe(0);
    expect(found.flips).toEqual([]);
  });
});
