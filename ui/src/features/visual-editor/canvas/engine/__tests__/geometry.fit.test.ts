// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { fitViewport } from '../geometry';

const size = { width: 1000, height: 700 };
const options = { padding: 0.2, maxZoom: 1.25, minZoom: 0.1 };

// The fit of a tall agent grid: the zoom has a floor and the top shows.
describe('fitViewport with a readable zoom', () => {
  it('fits all of a content that fits above the floor', () => {
    const bounds = { x: 0, y: 0, width: 800, height: 600 };
    expect(
      fitViewport(bounds, size, { ...options, readableZoom: 0.5 })
    ).toEqual(fitViewport(bounds, size, options));
  });

  it('does not shrink a tall content below the floor', () => {
    // 1000 x 1800 px: the whole fit would be 0.28.
    const bounds = { x: 0, y: 0, width: 1000, height: 1800 };
    expect(fitViewport(bounds, size, options).zoom).toBeLessThan(0.3);
    const view = fitViewport(bounds, size, { ...options, readableZoom: 0.5 });
    expect(view.zoom).toBeGreaterThanOrEqual(0.5);
    // The top of the content is on screen, a little under the top edge.
    expect(view.y).toBeGreaterThan(0);
    expect(view.y).toBeLessThan(60);
  });

  it('fits the width when the width gives more than the floor', () => {
    // 900 x 3000 px at 1000 px of canvas: the width fit is 1000 / 1260 =
    // 0.79, and the ladder floors it to 75 %.
    const bounds = { x: 0, y: 0, width: 900, height: 3000 };
    const view = fitViewport(bounds, size, { ...options, readableZoom: 0.5 });
    expect(view.zoom).toBe(0.75);
    // Centred on the width.
    expect(view.x + (bounds.width / 2) * view.zoom).toBeCloseTo(500, 5);
  });

  // A narrow area flipped a long graph from a readable top view to a stripe
  // of a few percent: the readable view keeps a floor.
  it('keeps a floor when the width of the content is below the readable zoom', () => {
    const bounds = { x: 0, y: 0, width: 6000, height: 9000 };
    const whole = fitViewport(bounds, size, options);
    expect(whole.zoom).toBeLessThan(0.2);
    const view = fitViewport(bounds, size, { ...options, readableZoom: 0.5 });
    // The floor is 0.35: the ladder floors it to 25 %.
    expect(view.zoom).toBe(0.25);
    // A content wider than the area starts at its left edge, at the top.
    expect(view.x).toBe(24);
    expect(view.y).toBe(24);
    const left = fitViewport(bounds, size, {
      ...options,
      readableZoom: 0.5,
      readableFrom: 'left',
    });
    expect(left.zoom).toBe(0.25);
    expect(left.x).toBe(24);
    expect(left.y).toBe(24);
  });

  // The Fit button of a long narrow graph gave the same view as the first
  // fit, at 100 %: an overview uses the floor zoom instead.
  it('uses the readable zoom itself for an overview of a long graph', () => {
    const bounds = { x: 0, y: 0, width: 190, height: 12000 };
    const first = fitViewport(bounds, size, {
      ...options,
      readableZoom: 0.3,
      minZoom: 0.02,
    });
    const overview = fitViewport(bounds, size, {
      ...options,
      readableZoom: 0.3,
      minZoom: 0.02,
      overview: true,
    });
    expect(first.zoom).toBeGreaterThan(0.9);
    // A readable zoom of 30 % floors to the step of 25 %.
    expect(overview.zoom).toBe(0.25);
    // Centred on the width, from the top.
    expect(overview.x + (bounds.width / 2) * overview.zoom).toBeCloseTo(500, 5);
    expect(overview.y).toBe(24);
    const left = fitViewport(bounds, size, {
      ...options,
      readableZoom: 0.3,
      readableFrom: 'left',
      minZoom: 0.02,
      overview: true,
    });
    expect(left.zoom).toBe(0.25);
  });

  it('does not fall to a stripe on an area a few pixels too narrow', () => {
    // A column of 240 px in an area of 100 px: the width fit is 0.3.
    const bounds = { x: 0, y: 0, width: 240, height: 12000 };
    const narrow = { width: 100, height: 700 };
    const view = fitViewport(bounds, narrow, {
      ...options,
      readableZoom: 0.5,
      minZoom: 0.02,
    });
    expect(view.zoom).toBeGreaterThanOrEqual(0.25);
    const slightly = fitViewport(
      bounds,
      { width: 160, height: 700 },
      {
        ...options,
        readableZoom: 0.5,
        minZoom: 0.02,
      }
    );
    expect(slightly.zoom).toBeGreaterThanOrEqual(0.25);
  });

  it('takes the floor from the option', () => {
    const bounds = { x: 0, y: 0, width: 4000, height: 6000 };
    const view = fitViewport(bounds, size, {
      ...options,
      readableZoom: 0.8,
      readableFloor: 0.5,
    });
    expect(view.zoom).toBeCloseTo(0.5, 5);
  });

  it('keeps the old fit without the option', () => {
    const bounds = { x: 0, y: 0, width: 1000, height: 1800 };
    // 700 / (1800 * 1.4) = 0.28: the largest step under it is 25 %.
    expect(fitViewport(bounds, size, options).zoom).toBe(0.25);
  });
});

// The inset keeps space free for the overlays of the canvas.
describe('fitViewport with an inset', () => {
  const inset = { top: 72, right: 24, left: 24 };

  it('keeps all the content in the area inside the inset', () => {
    const bounds = { x: 100, y: 50, width: 800, height: 600 };
    const view = fitViewport(bounds, size, { ...options, inset });
    const top = view.y + bounds.y * view.zoom;
    const bottom = top + bounds.height * view.zoom;
    const left = view.x + bounds.x * view.zoom;
    const right = left + bounds.width * view.zoom;
    expect(top).toBeGreaterThanOrEqual(72);
    expect(bottom).toBeLessThanOrEqual(700);
    expect(left).toBeGreaterThanOrEqual(24);
    expect(right).toBeLessThanOrEqual(1000 - 24);
  });

  it('centres the content in the area that the inset leaves', () => {
    const bounds = { x: 0, y: 0, width: 400, height: 200 };
    const view = fitViewport(bounds, size, { ...options, inset });
    expect(view.x + (bounds.width / 2) * view.zoom).toBeCloseTo(500, 5);
    expect(view.y + (bounds.height / 2) * view.zoom).toBeCloseTo(
      72 + (700 - 72) / 2,
      5
    );
  });

  it('fits in the smaller area', () => {
    const bounds = { x: 0, y: 0, width: 800, height: 1000 };
    const view = fitViewport(bounds, size, { ...options, inset });
    // (700 - 72) / (1000 * 1.4) = 0.45: the largest step under it is 25 %.
    expect(view.zoom).toBe(0.25);
  });

  it('puts the top of a tall content at the top inset', () => {
    const bounds = { x: 0, y: 30, width: 1000, height: 1800 };
    const view = fitViewport(bounds, size, {
      ...options,
      readableZoom: 0.5,
      inset,
    });
    expect(view.zoom).toBeGreaterThanOrEqual(0.5);
    expect(view.y + bounds.y * view.zoom).toBeCloseTo(72, 5);
    expect(view.x + (bounds.width / 2) * view.zoom).toBeCloseTo(500, 5);
  });

  it('is the same as no inset when the inset is empty', () => {
    const bounds = { x: 0, y: 0, width: 800, height: 600 };
    expect(fitViewport(bounds, size, { ...options, inset: {} })).toEqual(
      fitViewport(bounds, size, options)
    );
  });
});
