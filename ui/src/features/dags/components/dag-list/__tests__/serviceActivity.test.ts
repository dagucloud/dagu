// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { components } from '@/api/v1/schema';
import {
  SERVICE_MAX_CELLS,
  serviceActivity,
  serviceTone,
} from '../serviceActivity';

type Bucket = components['schemas']['SampleBucket'];

const NOW = Date.parse('2026-10-29T12:30:00Z');
const MINUTE = 60_000;
const HOUR = 3_600_000;
const DAY = 24 * HOUR;

function bucket(
  start: number,
  count: number,
  failures = 0,
  upMs?: number
): Bucket {
  return {
    ...(upMs === undefined ? {} : { upMs }),
    time: new Date(start).toISOString(),
    count,
    failures,
    minMs: 1,
    maxMs: 5,
    p50Ms: 2,
    p95Ms: 4,
  };
}

describe('serviceActivity', () => {
  it('gives 24 hourly slots for a day, the last one holding now', () => {
    const model = serviceActivity([], 3600, NOW);
    expect(model.cells).toHaveLength(24);
    expect(model.cells[23]!.start).toBe(Date.parse('2026-10-29T12:00:00Z'));
    expect(model.cells[0]!.start).toBe(Date.parse('2026-10-28T13:00:00Z'));
    expect(model.windowSeconds).toBe(86_400);
  });

  it('gives 60 one-minute slots for a window of one hour', () => {
    const model = serviceActivity([], 60, NOW, 3600);
    expect(model.cells).toHaveLength(60);
    expect(model.cells[59]!.start).toBe(Date.parse('2026-10-29T12:30:00Z'));
    expect(model.windowSeconds).toBe(3600);
  });

  it('gives 7 daily slots for a window of 7 days', () => {
    const model = serviceActivity([], 86_400, NOW, 7 * 86_400);
    expect(model.cells).toHaveLength(7);
    expect(model.cells[6]!.start).toBe(Date.parse('2026-10-29T00:00:00Z'));
  });

  it('never gives more than 60 cells', () => {
    const model = serviceActivity([], 60, NOW, 86_400);
    expect(model.cells).toHaveLength(SERVICE_MAX_CELLS);
    // The cells cover 60 minutes only: the window says so.
    expect(model.windowSeconds).toBe(3600);
  });

  it('cuts the window in 60 slots when the resolution is unknown', () => {
    const model = serviceActivity([], undefined, NOW);
    expect(model.cells).toHaveLength(60);
  });

  it('puts each bucket in its slot and leaves the others empty', () => {
    const model = serviceActivity(
      [
        bucket(NOW - 2 * HOUR - 30 * MINUTE, 10),
        bucket(NOW - 30 * MINUTE, 4, 1),
      ],
      3600,
      NOW
    );
    const filled = model.cells.filter((cell) => cell.count > 0);
    expect(filled.map((cell) => cell.start)).toEqual([
      Date.parse('2026-10-29T10:00:00Z'),
      Date.parse('2026-10-29T12:00:00Z'),
    ]);
    expect(model.cells.filter((cell) => cell.tone === null)).toHaveLength(22);
  });

  it('sums the counts, the failures and the availability', () => {
    const model = serviceActivity(
      [bucket(NOW - 3 * HOUR, 100, 1), bucket(NOW - HOUR, 300, 3)],
      3600,
      NOW
    );
    expect(model.count).toBe(400);
    expect(model.failures).toBe(4);
    expect(model.availability).toBeCloseTo(0.99);
  });

  // A gap counts after the first run only: the slots before it are not a
  // gap, and the slots after the last run are.
  it('finds the longest series of empty slots after the first run', () => {
    const model = serviceActivity(
      [bucket(NOW - 10 * HOUR, 5), bucket(NOW - 5 * HOUR, 5)],
      3600,
      NOW
    );
    expect(model.longestGapSeconds).toBe(5 * 3600);
    expect(serviceActivity([], 3600, NOW).longestGapSeconds).toBe(0);
    const full = Array.from({ length: 24 }, (_, i) =>
      bucket(NOW - i * HOUR, 1)
    );
    expect(serviceActivity(full, 3600, NOW).longestGapSeconds).toBe(0);
  });

  it('has no availability for a window with no run', () => {
    expect(serviceActivity([], 3600, NOW).availability).toBeUndefined();
  });

  it('leaves a bucket before the window out of the cells but counts it', () => {
    const model = serviceActivity([bucket(NOW - 3 * DAY, 5)], 3600, NOW);
    expect(model.cells.every((cell) => cell.count === 0)).toBe(true);
    expect(model.count).toBe(5);
  });

  it('puts a bucket after now in the last slot', () => {
    const model = serviceActivity([bucket(NOW + 5 * HOUR, 2)], 3600, NOW);
    expect(model.cells[23]!.count).toBe(2);
  });

  it('adds the buckets that name the same slot', () => {
    const model = serviceActivity(
      [bucket(NOW - 10 * MINUTE, 2, 1), bucket(NOW - 20 * MINUTE, 3)],
      3600,
      NOW
    );
    expect(model.cells[23]).toMatchObject({ count: 5, failures: 1 });
  });
});

describe('serviceActivity up time', () => {
  // A bucket with an up time and no run is green and changes no run figure.
  it('colours a cell that has up time and no run', () => {
    const model = serviceActivity(
      [
        bucket(NOW - 5 * HOUR, 1, 0, HOUR),
        bucket(NOW - 4 * HOUR, 0, 0, HOUR),
        bucket(NOW - 3 * HOUR, 0, 0, 1),
        bucket(NOW - 2 * HOUR, 5, 1, HOUR),
      ],
      3600,
      NOW
    );
    expect(model.cells.slice(-6).map((cell) => cell.tone)).toEqual([
      'success',
      'success',
      'success',
      'warning',
      null,
      null,
    ]);
    expect(model.cells[19]).toMatchObject({ count: 0, upMs: HOUR });
    expect(model.count).toBe(6);
    expect(model.failures).toBe(1);
    expect(model.availability).toBeCloseTo(5 / 6);
    // The gap is the cells that no run started in, up or not.
    expect(model.longestGapSeconds).toBe(2 * 3600);
  });

  it('keeps a failure in a cell that is also up', () => {
    const model = serviceActivity([bucket(NOW - HOUR, 1, 1, HOUR)], 3600, NOW);
    expect(model.cells[22]!.tone).toBe('error');
  });
});

describe('serviceTone', () => {
  it('is empty without run', () => {
    expect(serviceTone(0, 0)).toBeNull();
    expect(serviceTone(0, 0, 0)).toBeNull();
  });
  it('is success without failure when the service was up', () => {
    expect(serviceTone(0, 0, 1)).toBe('success');
  });
  it('is success without failure', () => {
    expect(serviceTone(60, 0)).toBe('success');
  });
  it('keeps a rare failure visible as a warning', () => {
    expect(serviceTone(1000, 1)).toBe('warning');
    expect(serviceTone(60, 14)).toBe('warning');
  });
  it('is an error from a quarter of failed runs', () => {
    expect(serviceTone(60, 15)).toBe('error');
    expect(serviceTone(1, 1)).toBe('error');
  });
});
