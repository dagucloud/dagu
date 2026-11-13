// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  createWheelStepper,
  fitViewport,
  floorZoom,
  snapZoom,
  stepZoom,
  ZOOM_LADDER,
  zoomSteps,
} from '../geometry';

// The zoom is always one of a few round percentages.
describe('zoom ladder', () => {
  it('lists the round percentages inside the limits', () => {
    expect(ZOOM_LADDER.map((z) => z * 100)).toEqual([
      5, 10, 25, 50, 75, 100, 125, 150, 200, 300, 400,
    ]);
    expect(zoomSteps({ min: 0.02, max: 4 })).toEqual(ZOOM_LADDER);
    // The 5 % step exists only when the engine goes that low.
    expect(zoomSteps({ min: 0.1, max: 2 })).toEqual([
      0.1, 0.25, 0.5, 0.75, 1, 1.25, 1.5, 2,
    ]);
    expect(zoomSteps({ min: 0.3, max: 1.25 })).toEqual([0.5, 0.75, 1, 1.25]);
  });

  it('moves one step in each direction, and stops at the ends', () => {
    const limits = { min: 0.02, max: 4 };
    expect(stepZoom(1, 1, limits)).toBe(1.25);
    expect(stepZoom(1.25, 1, limits)).toBe(1.5);
    expect(stepZoom(1.5, 1, limits)).toBe(2);
    expect(stepZoom(2, 1, limits)).toBe(3);
    expect(stepZoom(1, -1, limits)).toBe(0.75);
    expect(stepZoom(0.1, -1, limits)).toBe(0.05);
    expect(stepZoom(0.05, -1, limits)).toBe(0.05);
    expect(stepZoom(4, 1, limits)).toBe(4);
  });

  it('snaps a zoom between two steps to the next step of the direction', () => {
    expect(stepZoom(1.08, 1)).toBe(1.25);
    expect(stepZoom(1.08, -1)).toBe(1);
    expect(stepZoom(0.86, 1)).toBe(1);
    expect(stepZoom(0.86, -1)).toBe(0.75);
  });

  it('floors a zoom to a step, and never goes under the first one', () => {
    expect(floorZoom(1.54)).toBe(1.5);
    expect(floorZoom(1.5)).toBe(1.5);
    expect(floorZoom(0.49999999)).toBe(0.5);
    expect(floorZoom(0.01, { min: 0.02, max: 4 })).toBe(0.05);
    expect(floorZoom(9, { min: 0.1, max: 1.25 })).toBe(1.25);
  });

  it('snaps to the nearest step', () => {
    expect(snapZoom(1.1)).toBe(1);
    expect(snapZoom(1.2)).toBe(1.25);
    expect(snapZoom(0.001, { min: 0.02, max: 4 })).toBe(0.05);
    expect(snapZoom(50, { min: 0.02, max: 4 })).toBe(4);
  });

  // Fit chooses the largest step that shows all the content, then centres.
  it('fits on the largest step that shows all the content', () => {
    const size = { width: 1000, height: 600 };
    const bounds = { x: 100, y: 50, width: 400, height: 200 };
    // With the padding of 10 %: 1000 / 480 = 2.08 wide, 600 / 240 = 2.5 high.
    const view = fitViewport(bounds, size, { maxZoom: 4 });
    expect(view.zoom).toBe(2);
    expect(view.x + (bounds.x + bounds.width / 2) * view.zoom).toBeCloseTo(500);
    expect(view.y + (bounds.y + bounds.height / 2) * view.zoom).toBeCloseTo(
      300
    );
    // The content always fits at the step that was chosen.
    expect(bounds.width * 1.2 * view.zoom).toBeLessThanOrEqual(1000);
    expect(bounds.height * 1.2 * view.zoom).toBeLessThanOrEqual(600);
  });

  it('gives the smallest step to a content that fits nowhere', () => {
    const view = fitViewport(
      { x: 0, y: 0, width: 100000, height: 100000 },
      { width: 800, height: 600 },
      { minZoom: 0.02, maxZoom: 4 }
    );
    expect(view.zoom).toBe(0.05);
  });

  it('is deterministic: the same input gives the same view', () => {
    const size = { width: 913, height: 577 };
    for (let i = 0; i < 40; i++) {
      const bounds = {
        x: i * 7,
        y: i * 3,
        width: 100 + i * 61,
        height: 80 + i * 47,
      };
      const options = { padding: 0.2, maxZoom: 1.25, minZoom: 0.02 };
      const a = fitViewport(bounds, size, options);
      const b = fitViewport({ ...bounds }, { ...size }, { ...options });
      expect(b).toEqual(a);
      expect(zoomSteps({ min: 0.02, max: 1.25 })).toContain(a.zoom);
    }
  });

  it('uses ladder values for the readable and the overview rules', () => {
    const size = { width: 1000, height: 700 };
    const options = { padding: 0.2, maxZoom: 1.25, minZoom: 0.02 };
    const tall = { x: 0, y: 0, width: 190, height: 12000 };
    const overview = fitViewport(tall, size, {
      ...options,
      readableZoom: 0.3,
      overview: true,
    });
    expect(overview.zoom).toBe(0.25);
    const agent = fitViewport({ x: 0, y: 0, width: 4000, height: 6000 }, size, {
      ...options,
      readableZoom: 0.75,
      readableFloor: 0.5,
    });
    expect(agent.zoom).toBe(0.5);
  });
});

describe('wheel stepper', () => {
  it('gives one step for a notch of the wheel', () => {
    const step = createWheelStepper();
    expect(step(0.2, 1000)).toBe(1);
    expect(step(-0.2, 1050)).toBe(-1);
  });

  it('adds up small moves, and starts again after a pause', () => {
    const step = createWheelStepper();
    expect([0.05, 0.05, 0.05].map((d, i) => step(d, 1000 + i * 10))).toEqual([
      0, 0, 0,
    ]);
    expect(step(0.05, 1040)).toBe(1);
    // The sum starts at zero after a step, and after a pause.
    expect(step(0.1, 1050)).toBe(0);
    expect(step(0.1, 2000)).toBe(0);
    expect(step(0.1, 2010)).toBe(1);
  });

  it('restarts the sum when the move turns around', () => {
    const step = createWheelStepper();
    expect(step(0.15, 1000)).toBe(0);
    expect(step(-0.15, 1010)).toBe(0);
    expect(step(-0.1, 1020)).toBe(-1);
  });

  it('never gives more than one step for one move', () => {
    const step = createWheelStepper();
    expect(step(5, 1000)).toBe(1);
  });
});
