// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { placeToolbar, toolbarScale } from '../nodes/toolbarPlacement';

const canvas = { left: 0, top: 0, right: 800, bottom: 600 };
const size = { width: 112, height: 32 };
const box = (left: number, top: number, w = 160, h = 60) => ({
  left,
  top,
  right: left + w,
  bottom: top + h,
});

describe('toolbarScale', () => {
  it('cancels the zoom between 60 % and 150 %', () => {
    for (const zoom of [0.6, 0.8, 1, 1.25, 1.5]) {
      expect(toolbarScale(zoom) * zoom).toBeCloseTo(1, 6);
    }
  });

  it('holds the nearest limit beyond', () => {
    expect(toolbarScale(0.25)).toBeCloseTo(1 / 0.6, 6);
    expect(toolbarScale(4)).toBeCloseTo(1 / 1.5, 6);
  });
});

describe('placeToolbar', () => {
  it('stays above a node with room', () => {
    expect(placeToolbar(box(300, 200), canvas, size)).toEqual({
      below: false,
      shift: 0,
    });
  });

  it('flips below a node at the top edge', () => {
    expect(placeToolbar(box(300, 10), canvas, size).below).toBe(true);
  });

  it('stays above when the room below is smaller', () => {
    expect(placeToolbar(box(300, 10, 160, 580), canvas, size).below).toBe(
      false
    );
  });

  it('shifts left at the right edge', () => {
    expect(placeToolbar(box(700, 200), canvas, size).shift).toBe(-64);
  });

  it('shifts right at the left edge', () => {
    // The node ends at 40 px: the toolbar would start at -72.
    expect(placeToolbar(box(-120, 200), canvas, size).shift).toBe(76);
  });
});
