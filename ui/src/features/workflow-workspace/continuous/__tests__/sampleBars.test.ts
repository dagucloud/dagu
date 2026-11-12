// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { components } from '@/api/v1/schema';
import {
  barAt,
  barModel,
  bucketWindow,
  MIN_SPAN_MS,
  p50Path,
  panView,
  pointsFor,
  pointsForWindow,
  presetView,
  viewAt,
  zoomView,
} from '../sampleBars';

type SampleBucket = components['schemas']['SampleBucket'];

const DAY = 86_400_000;
const HOUR = 3_600_000;
const NOW = Date.UTC(2026, 9, 28, 12, 0, 0);

function bucket(time: number, over: Partial<SampleBucket> = {}): SampleBucket {
  return {
    time: new Date(time).toISOString(),
    count: 100,
    failures: 0,
    minMs: 5,
    maxMs: 80,
    p50Ms: 20,
    p95Ms: 40,
    ...over,
  };
}

describe('pointsFor', () => {
  it('asks for one bucket every 4 px, between 10 and 2000', () => {
    expect(pointsFor(800)).toBe(200);
    expect(pointsFor(10)).toBe(10);
    expect(pointsFor(0)).toBe(10);
    expect(pointsFor(Number.NaN)).toBe(10);
    expect(pointsFor(100_000)).toBe(2000);
  });
});

describe('pointsForWindow', () => {
  // The day buckets start at midnight UTC: a window that starts at another
  // time touches one day bucket more than its span holds.
  it('keeps pointsFor, and asks a bucket for each day that a window of years touches', () => {
    expect(pointsForWindow(800, DAY)).toBe(200);
    expect(pointsForWindow(800, 30 * DAY)).toBe(200);
    expect(pointsForWindow(800, 365 * DAY)).toBe(366);
    expect(pointsForWindow(800, 3 * 365 * DAY)).toBe(1096);
    expect(pointsForWindow(800, 30 * 365 * DAY)).toBe(2000);
  });
});

describe('barModel', () => {
  // Three years of day buckets in a plot of 800 px: a bar for each bucket
  // that is in the window, none outside, none over another.
  it('draws 3 years of daily buckets as bars that keep inside the plot', () => {
    const from = NOW - 3 * 365 * DAY;
    const buckets = Array.from({ length: 3 * 365 }, (_, i) =>
      bucket(from + i * DAY, {
        p95Ms: 10 + (i % 90),
        failures: i % 50 === 0 ? 3 : 0,
      })
    );
    const model = barModel({
      buckets,
      resolutionSeconds: 86_400,
      from,
      to: NOW,
      width: 800,
      height: 96,
    });
    expect(model.bars).toHaveLength(buckets.length);
    let previous = -Infinity;
    for (const bar of model.bars) {
      expect(bar.x).toBeGreaterThanOrEqual(0);
      expect(bar.x + bar.width).toBeLessThanOrEqual(800 + 1e-6);
      expect(bar.width).toBeGreaterThanOrEqual(1);
      expect(bar.x).toBeGreaterThanOrEqual(previous);
      previous = bar.x;
      expect(bar.height).toBeLessThanOrEqual(96);
      expect(bar.height).toBeGreaterThanOrEqual(2);
    }
    expect(model.maxMs).toBe(99);
  });

  it('draws 600 hourly buckets, wide enough to tell apart', () => {
    const from = NOW - 600 * HOUR;
    const buckets = Array.from({ length: 600 }, (_, i) =>
      bucket(from + i * HOUR)
    );
    const model = barModel({
      buckets,
      resolutionSeconds: 3600,
      from,
      to: NOW,
      width: 2400,
      height: 96,
    });
    expect(model.bars).toHaveLength(600);
    // 4 px of pitch: a bar of 3 px and a gap of 1 px.
    expect(model.pitch).toBeCloseTo(4);
    expect(model.bars[1]!.x - model.bars[0]!.x).toBeCloseTo(4);
    expect(model.bars[0]!.width).toBeCloseTo(3);
  });

  it('leaves out a bucket that is outside the window', () => {
    const model = barModel({
      buckets: [bucket(NOW - 5 * HOUR), bucket(NOW - 30 * 60_000)],
      resolutionSeconds: 60,
      from: NOW - HOUR,
      to: NOW,
      width: 600,
      height: 96,
    });
    expect(model.bars).toHaveLength(1);
  });

  // The red part is the share of the failures, with a floor so that it shows.
  it('sets the failed part to the share of failures', () => {
    const model = barModel({
      buckets: [
        bucket(NOW - 3 * 60_000, { count: 10, failures: 5, p95Ms: 100 }),
        bucket(NOW - 2 * 60_000, { count: 1000, failures: 1, p95Ms: 100 }),
        bucket(NOW - 60_000, { count: 10, failures: 0, p95Ms: 100 }),
      ],
      resolutionSeconds: 60,
      from: NOW - 10 * 60_000,
      to: NOW,
      width: 600,
      height: 100,
    });
    const [half, tick, none] = model.bars;
    expect(half!.height).toBe(100);
    expect(half!.failHeight).toBe(50);
    expect(tick!.failHeight).toBe(2);
    expect(none!.failHeight).toBe(0);
  });

  it('has no bar and no tick for no bucket', () => {
    const model = barModel({
      buckets: [],
      resolutionSeconds: 1,
      from: NOW - HOUR,
      to: NOW,
      width: 600,
      height: 96,
    });
    expect(model.bars).toEqual([]);
    expect(model.ticks).toEqual([]);
    expect(p50Path(model, 96)).toBe('');
  });
});

describe('p50Path', () => {
  it('breaks the line where buckets are missing', () => {
    const model = barModel({
      buckets: [
        bucket(NOW - 10 * 60_000),
        bucket(NOW - 9 * 60_000),
        bucket(NOW - 2 * 60_000),
      ],
      resolutionSeconds: 60,
      from: NOW - 10 * 60_000,
      to: NOW,
      width: 600,
      height: 96,
    });
    const path = p50Path(model, 96);
    expect(path.match(/M/g)).toHaveLength(2);
    expect(path.match(/L/g)).toHaveLength(1);
  });
});

describe('barAt', () => {
  const model = barModel({
    buckets: [bucket(NOW - 10 * 60_000), bucket(NOW - 5 * 60_000)],
    resolutionSeconds: 60,
    from: NOW - 10 * 60_000,
    to: NOW,
    width: 600,
    height: 96,
  });

  it('finds the bar under the pointer, and none far from a bar', () => {
    const first = model.bars[0]!;
    expect(barAt(model.bars, first.x + first.width / 2)).toBe(first);
    expect(barAt(model.bars, model.bars[1]!.x + 1)).toBe(model.bars[1]);
    expect(barAt(model.bars, 200)).toBeNull();
    expect(barAt([], 10)).toBeNull();
  });
});

describe('the window', () => {
  it('slides a preset with the clock and keeps a zoomed window', () => {
    const preset = presetView('1h', NOW);
    expect(viewAt(preset, NOW + 10_000)).toEqual({
      from: NOW + 10_000 - HOUR,
      to: NOW + 10_000,
    });
    const zoomed = zoomView(preset, 0.5, 0.5, NOW);
    expect(zoomed.preset).toBeNull();
    expect(viewAt(zoomed, NOW + 10_000)).toEqual({
      from: zoomed.from,
      to: zoomed.to,
    });
  });

  it('zooms around the pointer and keeps the span in its bounds', () => {
    const view = { from: NOW - HOUR, to: NOW - 1000, preset: null };
    const zoomed = zoomView(view, 0.5, 0.25, NOW);
    expect(zoomed.to - zoomed.from).toBeCloseTo((HOUR - 1000) / 2);
    const anchor = view.from + (HOUR - 1000) * 0.25;
    expect(zoomed.from + (zoomed.to - zoomed.from) * 0.25).toBeCloseTo(anchor);
    expect(
      zoomView(view, 1e-9, 0.5, NOW).to - zoomView(view, 1e-9, 0.5, NOW).from
    ).toBe(MIN_SPAN_MS);
  });

  it('does not go after now', () => {
    const view = presetView('24h', NOW);
    expect(panView(view, HOUR, NOW).to).toBe(NOW);
    expect(panView(view, -HOUR, NOW).to).toBe(NOW - HOUR);
    expect(zoomView(view, 2, 1, NOW).to).toBe(NOW);
  });

  it('starts All at the start of the run, at least one hour back', () => {
    expect(presetView('all', NOW, NOW - 40 * DAY).from).toBe(NOW - 40 * DAY);
    expect(presetView('all', NOW, NOW - 60_000).from).toBe(NOW - HOUR);
    expect(presetView('all', NOW).from).toBe(NOW - HOUR);
  });

  it('gives the window of a bucket', () => {
    expect(bucketWindow(1000, 60)).toEqual({ from: 1000, to: 61_000 });
  });
});
