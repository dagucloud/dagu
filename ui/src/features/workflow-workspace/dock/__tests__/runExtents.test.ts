// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { Status } from '@/api/v1/schema';
import { run } from '../../data/__tests__/fixtures';
import {
  isLongRun,
  laneBlockHeight,
  LONG_RUN_MIN_MS,
  medianMs,
  overlapIds,
  packLanes,
  runSpan,
} from '../runExtents';

const NOW = Date.parse('2026-03-02T12:00:00Z');
const MINUTE = 60_000;

describe('runSpan', () => {
  it('runs from the start to the finish', () => {
    const span = runSpan(
      run('a', [], Status.Success, {
        startedAt: '2026-03-02T10:00:00Z',
        finishedAt: '2026-03-02T10:05:00Z',
      }),
      NOW
    );
    expect(span).toEqual({
      id: 'a',
      start: Date.parse('2026-03-02T10:00:00Z'),
      ownStart: Date.parse('2026-03-02T10:00:00Z'),
      end: Date.parse('2026-03-02T10:05:00Z'),
      running: false,
      cached: 0,
    });
  });

  it('begins earlier by the cached time', () => {
    const span = runSpan(
      run('a', [], Status.Success, {
        startedAt: '2026-03-02T10:00:00Z',
        finishedAt: '2026-03-02T10:05:00Z',
      }),
      NOW,
      2 * MINUTE
    );
    expect(span).toMatchObject({
      start: Date.parse('2026-03-02T09:58:00Z'),
      ownStart: Date.parse('2026-03-02T10:00:00Z'),
      cached: 2 * MINUTE,
    });
  });

  it('extends a running run to now', () => {
    const span = runSpan(
      run('a', [], Status.Running, { startedAt: '2026-03-02T11:00:00Z' }),
      NOW
    );
    expect(span).toMatchObject({ end: NOW, running: true });
  });

  it('keeps an unfinished run that does not run as an instant', () => {
    const span = runSpan(
      run('a', [], Status.Failed, { startedAt: '2026-03-02T11:00:00Z' }),
      NOW
    );
    expect(span).toMatchObject({ start: span!.end, running: false });
  });

  it('has no span for a run that has not started', () => {
    expect(runSpan(run('a', [], Status.Queued, {}), NOW)).toBeNull();
  });
});

describe('packLanes', () => {
  const item = (id: string, x0: number, x1: number) => ({ id, x0, x1 });

  it('puts runs that do not overlap in one lane', () => {
    const { places, hidden } = packLanes([
      item('a', 0, 50),
      item('b', 60, 100),
      item('c', 120, 130),
    ]);
    expect([...places.values()].map((p) => p.lane)).toEqual([0, 0, 0]);
    expect(hidden).toBe(0);
  });

  it('puts two runs that overlap in two lanes', () => {
    const { places } = packLanes([item('a', 0, 80), item('b', 40, 120)]);
    expect(places.get('a')!.lane).toBe(0);
    expect(places.get('b')!.lane).toBe(1);
  });

  it('keeps 4 pixels between two extents of one lane', () => {
    const { places } = packLanes([item('a', 0, 50), item('b', 53, 90)]);
    expect(places.get('b')!.lane).toBe(1);
    const apart = packLanes([item('a', 0, 50), item('b', 54, 90)]);
    expect(apart.places.get('b')!.lane).toBe(0);
  });

  it('reuses the first lane that is free', () => {
    const { places } = packLanes([
      item('a', 0, 100),
      item('b', 10, 30),
      item('c', 40, 60),
    ]);
    expect(places.get('c')!.lane).toBe(1);
  });

  it('draws 6 overlapping runs in 4 lanes and counts 2 hidden', () => {
    const items = Array.from({ length: 6 }, (_, i) =>
      item(`r${i}`, i, 100 + i)
    );
    const { places, hidden } = packLanes(items);
    expect(hidden).toBe(2);
    const lanes = items.map((i) => places.get(i.id)!);
    expect(Math.max(...lanes.map((p) => p.lane))).toBe(3);
    expect(lanes.filter((p) => p.overflow)).toHaveLength(2);
    expect(lanes.slice(0, 4).every((p) => !p.overflow)).toBe(true);
    expect(lanes[4]).toEqual({ lane: 3, overflow: true });
  });

  it('is empty for no run', () => {
    expect(packLanes([])).toEqual({ places: new Map(), hidden: 0 });
  });
});

describe('laneBlockHeight', () => {
  it('is 5px a lane with 2px between lanes', () => {
    expect(laneBlockHeight(4)).toBe(26);
    expect(laneBlockHeight(1)).toBe(5);
  });
});

describe('overlapIds', () => {
  const span = (id: string, start: number, end: number) => ({
    id,
    start,
    end,
    running: false,
  });

  it('lists the other runs that overlap in time', () => {
    const map = overlapIds([
      span('a', 0, 100),
      span('b', 50, 150),
      span('c', 200, 300),
    ]);
    expect(map.get('a')).toEqual(['b']);
    expect(map.get('b')).toEqual(['a']);
    expect(map.get('c')).toEqual([]);
  });

  it('does not count runs that only touch', () => {
    const map = overlapIds([span('a', 0, 100), span('b', 100, 200)]);
    expect(map.get('a')).toEqual([]);
  });

  it('counts an instant run inside a long run', () => {
    const map = overlapIds([span('a', 0, 100), span('i', 50, 50)]);
    expect(map.get('a')).toEqual(['i']);
    expect(map.get('i')).toEqual(['a']);
  });
});

describe('medianMs and isLongRun', () => {
  it('takes the middle duration', () => {
    expect(medianMs([5, 1, 3])).toBe(3);
    expect(medianMs([1, 2, 3, 10])).toBe(2.5);
    expect(medianMs([])).toBe(0);
  });

  it('marks a run of more than 10 times the median and more than 10 minutes', () => {
    expect(isLongRun(20 * 60 * MINUTE, 5 * MINUTE)).toBe(true);
    expect(isLongRun(50 * MINUTE, 5 * MINUTE)).toBe(false);
    expect(isLongRun(51 * MINUTE, 5 * MINUTE)).toBe(true);
  });

  it('needs more than 10 minutes, whatever the median', () => {
    expect(isLongRun(LONG_RUN_MIN_MS, 1000)).toBe(false);
    expect(isLongRun(LONG_RUN_MIN_MS + 1, 1000)).toBe(true);
    expect(isLongRun(9 * MINUTE, 1000)).toBe(false);
  });
});
