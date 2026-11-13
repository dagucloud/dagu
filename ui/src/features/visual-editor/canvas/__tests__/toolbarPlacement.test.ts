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

  // A pan that takes the node out of the canvas takes its toolbar with it: it
  // keeps 24 px over the node and does not stay at the edge.
  it('leaves the canvas with a node that goes out at the right', () => {
    const node = box(2000, 200);
    const { shift } = placeToolbar(node, canvas, size);
    expect(node.right + shift).toBeGreaterThanOrEqual(node.left + 24);
    expect(node.right + shift).toBeGreaterThan(canvas.right);
  });

  it('leaves the canvas with a node that goes out at the left', () => {
    const node = box(-1000, 200);
    const { shift } = placeToolbar(node, canvas, size);
    const left = node.right + shift - size.width;
    expect(left).toBeLessThanOrEqual(node.right - 24);
    expect(node.right + shift).toBeLessThan(canvas.left);
  });

  // The Model pickers stand left of a node. A node at the right edge of a
  // canvas with a dock pushes its toolbar left, over those pickers.
  describe('with an obstacle', () => {
    // The pickers: left of the node, level with the band above it.
    const pickers = { left: 560, top: 150, right: 760, bottom: 250 };
    const node = box(700, 200, 360, 60);

    it('goes under the node when the toolbar would cover the pickers', () => {
      expect(placeToolbar(node, canvas, size, 'right', [pickers]).below).toBe(
        true
      );
    });

    it('stays above when nothing is in the way', () => {
      const far = { left: 0, top: 150, right: 100, bottom: 250 };
      expect(placeToolbar(node, canvas, size, 'right', [far]).below).toBe(
        false
      );
    });

    it('stays above when the other side has no room', () => {
      const low = box(700, 540, 360, 40);
      const near = { left: 560, top: 480, right: 760, bottom: 580 };
      expect(placeToolbar(low, canvas, size, 'right', [near]).below).toBe(
        false
      );
    });

    it('stays where it is when both sides are covered', () => {
      const below = { left: 560, top: 262, right: 760, bottom: 300 };
      expect(
        placeToolbar(node, canvas, size, 'right', [pickers, below]).below
      ).toBe(false);
    });
  });

  // In a top-to-bottom layout the link enters at the centre of the top of a
  // narrow node. The bar, right aligned, would lie across it.
  describe('with the end of a link', () => {
    const narrow = box(300, 200, 120, 60);
    // The link that enters and the link that leaves: 4 px wide at the centre.
    const link = { left: 358, top: 160, right: 362, bottom: 200 };
    const out = { left: 358, top: 260, right: 362, bottom: 300 };

    it('goes under the node when only the entry is in the way', () => {
      expect(placeToolbar(narrow, canvas, size, 'right', [link]).below).toBe(
        true
      );
    });

    it('goes right of the links', () => {
      const { below, shift } = placeToolbar(narrow, canvas, size, 'right', [
        link,
        out,
      ]);
      expect(below).toBe(false);
      // The bar starts right of the link, and keeps a part over the node.
      const left = narrow.right + shift - size.width;
      expect(left).toBeGreaterThanOrEqual(link.right);
      expect(left).toBeLessThan(narrow.right - 24);
    });

    it('stays where it is when it does not cover the link', () => {
      const wide = box(300, 200, 360, 60);
      expect(placeToolbar(wide, canvas, size, 'right', [link, out]).shift).toBe(
        0
      );
    });

    it('stays where it is when the canvas has no room right of the link', () => {
      const edge = box(660, 200, 120, 60);
      const at = { left: 718, top: 160, right: 722, bottom: 200 };
      const to = { left: 718, top: 260, right: 722, bottom: 300 };
      const { shift } = placeToolbar(edge, canvas, size, 'right', [at, to]);
      expect(edge.right + shift).toBeLessThanOrEqual(canvas.right - 4);
    });
  });

  // The label of a connector sits on the node, in the band of the bar.
  it('goes right of the label of a connector when the other side is covered', () => {
    const node = box(300, 200, 120, 60);
    const label = { left: 310, top: 170, right: 340, bottom: 185 };
    const under = { left: 300, top: 262, right: 420, bottom: 290 };
    const { below, shift } = placeToolbar(node, canvas, size, 'right', [
      label,
      under,
    ]);
    expect(below).toBe(false);
    expect(node.right + shift - size.width).toBeGreaterThanOrEqual(label.right);
  });
});
