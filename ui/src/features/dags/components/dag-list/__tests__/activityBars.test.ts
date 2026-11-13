// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { Status } from '@/api/v1/schema';
import {
  ACTIVITY_BAR_MIN_PX,
  ACTIVITY_BAR_PX,
  ACTIVITY_SLOT_PX,
  ACTIVITY_HEIGHT_PX,
  ACTIVITY_WIDTH_PX,
  activityBars,
  type ActivityRun,
} from '../activityBars';

const NOW = Date.parse('2026-10-29T12:00:00Z');
const HOUR = 3_600_000;

function run(
  id: string,
  agoMs: number,
  tookMs: number,
  extra: Partial<ActivityRun> = {}
): ActivityRun {
  const start = NOW - agoMs;
  return {
    dagRunId: id,
    status: Status.Success,
    startedAt: new Date(start).toISOString(),
    finishedAt: new Date(start + tookMs).toISOString(),
    ...extra,
  };
}

describe('activityBars', () => {
  it('places a run in the slot of its start in the window', () => {
    const [oldest, middle, newest] = activityBars(
      [run('a', 24 * HOUR, 1000), run('b', 12 * HOUR, 1000), run('c', 0, 1000)],
      NOW
    );
    const slots = Math.floor(ACTIVITY_WIDTH_PX / ACTIVITY_SLOT_PX);
    expect(oldest?.left).toBe(0);
    expect(middle?.left).toBe(Math.floor(slots / 2) * ACTIVITY_SLOT_PX);
    expect(newest?.left).toBe((slots - 1) * ACTIVITY_SLOT_PX);
  });

  it('moves a run to the next free slot toward the past when its slot is taken', () => {
    const bars = activityBars(
      [
        run('newest', 1 * 60_000, 1000),
        run('next', 3 * 60_000, 1000),
        run('last', 6 * 60_000, 1000),
      ],
      NOW
    );
    const slots = Math.floor(ACTIVITY_WIDTH_PX / ACTIVITY_SLOT_PX);
    // The three runs started in the last slot: they stand side by side, newest on the right.
    expect(bars.map((b) => b.id)).toEqual(['newest', 'next', 'last']);
    const lefts = bars.map((b) => b.left).sort((a, b) => b - a);
    expect(lefts).toEqual([
      (slots - 1) * ACTIVITY_SLOT_PX,
      (slots - 2) * ACTIVITY_SLOT_PX,
      (slots - 3) * ACTIVITY_SLOT_PX,
    ]);
    expect(bars.find((b) => b.id === 'newest')?.left).toBe(
      (slots - 1) * ACTIVITY_SLOT_PX
    );
  });

  it('leaves out the runs that find no free slot', () => {
    const slots = Math.floor(ACTIVITY_WIDTH_PX / ACTIVITY_SLOT_PX);
    const runs = Array.from({ length: slots + 5 }, (_, i) =>
      run(`r${i}`, i * 1000, 10)
    );
    expect(activityBars(runs, NOW)).toHaveLength(slots);
  });

  it('keeps a run that started before the window at the left edge', () => {
    const [bar] = activityBars(
      [
        run('long', 30 * HOUR, 0, {
          status: Status.Running,
          finishedAt: undefined,
        }),
      ],
      NOW
    );
    expect(bar?.left).toBe(0);
  });

  it('gives the longest run the whole strip and no run less than the minimum', () => {
    const bars = activityBars(
      [run('quick', HOUR, 1), run('slow', 2 * HOUR, 60_000)],
      NOW
    );
    expect(bars[1]?.height).toBe(ACTIVITY_HEIGHT_PX);
    expect(bars[0]?.height).toBe(ACTIVITY_BAR_MIN_PX);
  });

  it('counts the cached time in the height and shows it as a foot', () => {
    const [own, cached] = activityBars(
      [
        run('own', HOUR, 10_000),
        run('cached', 2 * HOUR, 1_000, { cachedMs: 9_000 }),
      ],
      NOW
    );
    expect(own?.cachedPx).toBe(0);
    expect(cached?.cached).toBe(9_000);
    expect(cached?.cachedPx).toBeGreaterThan(0);
    // 1 s own + 9 s cached stands as tall as 10 s of own time.
    expect(cached?.height).toBe(own?.height);
  });

  it('draws a running run up to now', () => {
    const [bar] = activityBars(
      [
        run('live', 5 * 60_000, 0, {
          status: Status.Running,
          finishedAt: undefined,
        }),
      ],
      NOW
    );
    expect(bar?.own).toBe(5 * 60_000);
    expect(bar?.tone).toBe('running');
  });

  it('skips a run without a readable start', () => {
    expect(activityBars([run('bad', 0, 0, { startedAt: '-' })], NOW)).toEqual(
      []
    );
  });

  it('holds more slots in a wider strip and ends at its right edge', () => {
    const runs = Array.from({ length: 60 }, (_, i) =>
      run(`r${i}`, i * 10 * 60_000, 1_000)
    );
    const narrow = activityBars(runs, NOW, undefined, 196);
    const wide = activityBars(runs, NOW, undefined, 490);
    expect(wide.length).toBeGreaterThan(narrow.length);
    const last = wide.reduce((a, b) => (b.left > a.left ? b : a));
    expect(last.left + ACTIVITY_BAR_PX).toBeLessThanOrEqual(490);
    expect(last.left).toBeGreaterThan(490 - 2 * 8);
  });
});
