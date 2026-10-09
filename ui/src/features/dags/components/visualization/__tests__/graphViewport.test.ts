// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  alignedScroll,
  anchorScrollDelta,
  autoGraphHeight,
  autoGraphMaxHeight,
  clampScale,
  fitScale,
  fittedScale,
  GRAPH_DEFAULT_HEIGHT,
  GRAPH_MAX_SCALE,
  GRAPH_MIN_HEIGHT,
  GRAPH_MIN_SCALE,
  GRAPH_READABLE_MIN_SCALE,
  wheelZoomFactor,
} from '../graphViewport';

// A 864x464 viewport leaves 800x400 inside the 32px content padding.
const VIEWPORT = { width: 864, height: 464 };

describe('clampScale', () => {
  it('keeps the scale inside the zoom range', () => {
    expect(clampScale(0.01)).toBe(GRAPH_MIN_SCALE);
    expect(clampScale(10)).toBe(GRAPH_MAX_SCALE);
    expect(clampScale(1.5)).toBe(1.5);
  });
});

describe('fitScale', () => {
  it('is limited by whichever side overflows more', () => {
    expect(fitScale({ width: 1600, height: 400 }, VIEWPORT)).toBe(0.5);
    expect(fitScale({ width: 400, height: 1600 }, VIEWPORT)).toBe(0.25);
  });

  it('is 1 when a size is empty', () => {
    expect(fitScale({ width: 0, height: 100 }, VIEWPORT)).toBe(1);
    expect(
      fitScale({ width: 100, height: 100 }, { width: 40, height: 40 })
    ).toBe(1);
  });
});

describe('fittedScale', () => {
  it('never enlarges a drawing that already fits', () => {
    expect(fittedScale({ width: 200, height: 100 }, VIEWPORT, 'full')).toBe(1);
  });

  // A full view must show the whole drawing, even below the zoom floor.
  it('stops at the readable minimum unless a full view is requested', () => {
    const drawing = { width: 16000, height: 400 };

    expect(fittedScale(drawing, VIEWPORT, 'readable')).toBe(
      GRAPH_READABLE_MIN_SCALE
    );
    expect(fittedScale(drawing, VIEWPORT, 'full')).toBe(0.05);
  });
});

describe('autoGraphMaxHeight', () => {
  it('grows with the window but never below the default height', () => {
    expect(autoGraphMaxHeight(1000)).toBe(600);
    expect(autoGraphMaxHeight(400)).toBe(GRAPH_DEFAULT_HEIGHT);
  });
});

describe('autoGraphHeight', () => {
  it('fits the drawing plus padding and inset', () => {
    // 300 drawing + 64 padding + 56 inset.
    expect(autoGraphHeight({ width: 400, height: 300 }, 1, 800, 56, 600)).toBe(
      420
    );
  });

  it('leaves room for a horizontal scrollbar when the drawing is wider', () => {
    expect(autoGraphHeight({ width: 2000, height: 300 }, 1, 800, 0, 600)).toBe(
      380
    );
  });

  it('stays between the minimum and the cap', () => {
    expect(autoGraphHeight({ width: 100, height: 20 }, 1, 800, 0, 600)).toBe(
      GRAPH_MIN_HEIGHT
    );
    expect(autoGraphHeight({ width: 400, height: 5000 }, 1, 800, 0, 600)).toBe(
      600
    );
  });
});

describe('wheelZoomFactor', () => {
  it('zooms in on scroll up and out on scroll down', () => {
    expect(wheelZoomFactor(-4, WheelEvent.DOM_DELTA_PIXEL)).toBeGreaterThan(1);
    expect(wheelZoomFactor(4, WheelEvent.DOM_DELTA_PIXEL)).toBeLessThan(1);
  });

  it('limits a mouse-wheel notch to one step', () => {
    expect(wheelZoomFactor(100, WheelEvent.DOM_DELTA_PIXEL)).toBe(
      wheelZoomFactor(10, WheelEvent.DOM_DELTA_PIXEL)
    );
    expect(wheelZoomFactor(1, WheelEvent.DOM_DELTA_LINE)).toBe(
      wheelZoomFactor(10, WheelEvent.DOM_DELTA_PIXEL)
    );
  });
});

describe('anchorScrollDelta', () => {
  // The drawing point under the cursor must land back under the cursor.
  it('keeps the anchored drawing point under the anchor', () => {
    const anchor = 300;
    const startBefore = 100;
    const scaleBefore = 1;
    const point = (anchor - startBefore) / scaleBefore;
    const startAfter = 100;
    const scaleAfter = 2;

    const delta = anchorScrollDelta(point, scaleAfter, startAfter, anchor);

    expect(startAfter - delta + point * scaleAfter).toBe(anchor);
  });
});

describe('alignedScroll', () => {
  const metrics = {
    scrollWidth: 1000,
    scrollHeight: 600,
    clientWidth: 400,
    clientHeight: 200,
  };

  it('shows the top centre of a top-down graph', () => {
    expect(alignedScroll('TD', metrics)).toEqual({ left: 300, top: 0 });
  });

  it('shows the left middle of a left-to-right graph', () => {
    expect(alignedScroll('LR', metrics)).toEqual({ left: 0, top: 200 });
  });

  it('does not scroll a graph that fits', () => {
    const fitting = { ...metrics, scrollWidth: 400, scrollHeight: 200 };

    expect(alignedScroll('TD', fitting)).toEqual({ left: 0, top: 0 });
  });
});
