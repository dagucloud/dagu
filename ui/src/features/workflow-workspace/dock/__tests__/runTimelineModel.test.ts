// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { Status, TriggerType } from '@/api/v1/schema';
import type { ApiDAGRunDetails } from '../../contracts';
import { run } from '../../data/__tests__/fixtures';
import {
  axisClock,
  BAR_GAP_PX,
  BAR_MAX_PX,
  BAR_MIN_PX,
  BAR_PX,
  barHeights,
  barTone,
  countOptions,
  durationAxis,
  adjacentVisibleRunId,
  cachedBarPx,
  cachedMsOf,
  durationParts,
  EDGE_PAD_PX,
  elapsedMsText,
  fillBackground,
  isTestRun,
  layoutLabels,
  longRunMedianMs,
  MIN_SPAN_MS,
  parseStoredCount,
  parseStoredMode,
  parseStoredRange,
  parseStoredShowCached,
  parseStoredHiddenTypes,
  parseStoredShowTests,
  triggerOf,
  rangeBounds,
  runInRange,
  placeBars,
  runDurationMs,
  runTimes,
  timelineLayout,
  timelineRuns,
  timelineWindow,
  timeTicks,
  totalDurationMs,
  TIMELINE_COUNT_DEFAULT,
  TIMELINE_COUNTS,
  Y_AXIS_PX,
  Y_TICK_GAP_PX,
  TEST_RUN_HATCH,
  Y_TICK_MAX,
} from '../runTimelineModel';

const NOW = Date.parse('2026-03-01T10:10:00Z');

describe('barHeights', () => {
  // The height is BAR_MAX_PX * d / longest, at least BAR_MIN_PX.
  it('grows with the run time, up to the longest run', () => {
    expect(barHeights([10_000, 40_000, 40_000, 0])).toEqual([
      12,
      BAR_MAX_PX,
      BAR_MAX_PX,
      BAR_MIN_PX,
    ]);
    expect(barHeights([25_000, 100_000])).toEqual([12, BAR_MAX_PX]);
  });

  it('keeps a bar that is under the minimum at the minimum', () => {
    expect(barHeights([1_000, 100_000])).toEqual([BAR_MIN_PX, BAR_MAX_PX]);
  });

  it('grows with the square root when one run would flatten the others', () => {
    // The median is 3% of the longest run: the linear scale is off.
    const heights = barHeights([1_000, 5_000, 20_000, 80_000, 600_000]);
    expect([...heights].sort((a, b) => a - b)).toEqual(heights);
    expect(heights[3]).toBe(Math.round(BAR_MAX_PX * Math.sqrt(80 / 600)));
    expect(heights[4]).toBe(BAR_MAX_PX);
    // One very long run does not flatten the others to the minimum.
    expect(heights[3]).toBeGreaterThan(BAR_MIN_PX + 10);
  });

  it('keeps runs without a duration at the minimum height', () => {
    expect(barHeights([null, 100_000])).toEqual([BAR_MIN_PX, BAR_MAX_PX]);
  });

  it('gives every bar the minimum when no run has a duration', () => {
    expect(barHeights([null, 0, null])).toEqual([
      BAR_MIN_PX,
      BAR_MIN_PX,
      BAR_MIN_PX,
    ]);
    expect(barHeights([])).toEqual([]);
  });

  it('uses 3px and 48px as the bounds', () => {
    expect(BAR_MIN_PX).toBe(3);
    expect(BAR_MAX_PX).toBe(48);
  });
});

describe('durationAxis', () => {
  const S = 1000;
  const M = 60 * S;
  const H = 60 * M;

  it('is linear, with round labels from the top, for runs of similar size', () => {
    const axis = durationAxis([2 * S, 30 * S, 180 * S]);
    expect(axis.scale).toBe('linear');
    expect(axis.max).toBe(180 * S);
    expect(axis.ticks.map((t) => t.v)).toEqual([2 * M, M]);
    expect(axis.ticks[0]!.y).toBeCloseTo((120 / 180) * BAR_MAX_PX, 6);
    expect(axis.ticks[1]!.y).toBeCloseTo((60 / 180) * BAR_MAX_PX, 6);
  });

  it('puts a bar at the height of its label', () => {
    const durations = [2 * S, 30 * S, 180 * S];
    const axis = durationAxis(durations);
    for (const tick of axis.ticks) {
      expect(barHeights([tick.v, 180 * S], axis)[0]).toBe(Math.round(tick.y));
    }
  });

  it('is square root for a 27 hour run among short runs, and labels its own heights', () => {
    const axis = durationAxis([2 * S, 30 * S, 180 * S, 3 * H, 27 * H]);
    expect(axis.scale).toBe('sqrt');
    expect(axis.ticks.map((t) => t.v)).toEqual([24 * H, 6 * H]);
    expect(axis.ticks[0]!.y).toBeCloseTo(BAR_MAX_PX * Math.sqrt(24 / 27), 6);
    // The 3 hour run is not flattened to the minimum: 16px of 48px.
    expect(barHeights([3 * H, 27 * H], axis)[0]).toBe(
      Math.round(BAR_MAX_PX * Math.sqrt(3 / 27))
    );
  });

  it('keeps its labels apart from each other and from the baseline', () => {
    const sets = [
      [S, 5 * S, 20 * S],
      [30 * S, 40 * S],
      [M, 2 * M, 90 * M, 5 * H],
      [S, 2 * S, 3 * S, 4 * S, 10 * H],
    ];
    for (const durations of sets) {
      const { ticks } = durationAxis(durations);
      expect(ticks.length).toBeLessThanOrEqual(Y_TICK_MAX);
      let above = Infinity;
      for (const tick of ticks) {
        expect(tick.y).toBeGreaterThanOrEqual(Y_TICK_GAP_PX);
        expect(tick.y).toBeLessThanOrEqual(BAR_MAX_PX);
        expect(above - tick.y).toBeGreaterThanOrEqual(Y_TICK_GAP_PX);
        above = tick.y;
      }
    }
  });

  it('draws no label above zero when no run has a duration', () => {
    for (const durations of [[], [null, 0, null]]) {
      const axis = durationAxis(durations);
      expect(axis.ticks).toEqual([]);
      expect(axis.max).toBe(0);
      expect(axis.heightOf(5)).toBe(0);
    }
  });

  it('labels a run that lasts less than a second with a round number of milliseconds', () => {
    const axis = durationAxis([400, 900]);
    expect(axis.ticks.map((t) => t.v)).toContain(500);
  });

  it('reserves a gutter of 40px', () => {
    expect(Y_AXIS_PX).toBe(40);
  });
});

describe('elapsedMsText', () => {
  it('writes a duration as the elapsed text, and none as 0', () => {
    expect(elapsedMsText(0)).toBe('0');
    expect(elapsedMsText(-5)).toBe('0');
    expect(elapsedMsText(850)).toBe('850ms');
    expect(elapsedMsText(125_000)).toBe('2m 5s');
  });
});

describe('runDurationMs', () => {
  it('is finished minus started', () => {
    expect(
      runDurationMs(
        {
          status: Status.Success,
          startedAt: '2026-03-01T10:00:00Z',
          finishedAt: '2026-03-01T10:01:05Z',
        },
        NOW
      )
    ).toBe(65_000);
  });

  it('counts a running run up to now', () => {
    expect(
      runDurationMs(
        { status: Status.Running, startedAt: '2026-03-01T10:09:00Z' },
        NOW
      )
    ).toBe(60_000);
  });

  it('is null without a start, without a finish for a finished run, and for a negative span', () => {
    expect(runDurationMs({ status: Status.Queued }, NOW)).toBeNull();
    expect(
      runDurationMs(
        { status: Status.Success, startedAt: '2026-03-01T10:00:00Z' },
        NOW
      )
    ).toBeNull();
    expect(
      runDurationMs(
        {
          status: Status.Success,
          startedAt: '2026-03-01T10:00:10Z',
          finishedAt: '2026-03-01T10:00:00Z',
        },
        NOW
      )
    ).toBeNull();
    expect(
      runDurationMs({ status: Status.Success, startedAt: '-' }, NOW)
    ).toBeNull();
  });
});

describe('barTone', () => {
  it.each([
    [Status.Success, 'success'],
    [Status.Failed, 'error'],
    [Status.Rejected, 'error'],
    [Status.Running, 'running'],
    [Status.Queued, 'warning'],
    [Status.Waiting, 'warning'],
    [Status.Aborted, 'aborted'],
    [Status.PartialSuccess, 'partial'],
    [Status.NotStarted, 'neutral'],
  ])('maps status %s to %s', (status, tone) => {
    expect(barTone(status)).toBe(tone);
  });
});

// Local-time instants: the ticks are in the time zone of the browser.
const at = (
  day: number,
  hour: number,
  minute = 0,
  second = 0,
  month = 2
): string => new Date(2026, month, day, hour, minute, second).toISOString();
const runAt = (id: string, startedAt: string, status = Status.Success) =>
  run(id, [], status, { startedAt });

describe('timelineRuns', () => {
  it('keeps the n newest runs, oldest first', () => {
    const runs = ['r5', 'r4', 'r3', 'r2', 'r1'].map((id) => run(id));
    expect(timelineRuns(runs, 3).map((r) => r.dagRunId)).toEqual([
      'r3',
      'r4',
      'r5',
    ]);
  });

  it('keeps 100 runs at the most by default', () => {
    const runs = Array.from({ length: 130 }, (_, i) => run(`r${i}`));
    expect(timelineRuns(runs)).toHaveLength(100);
  });
});

describe('run count', () => {
  it('offers 5, 10, 20 and 30 runs', () => {
    expect(countOptions(3)).toEqual([5, 10, 20, 30]);
    expect(countOptions(30)).toEqual([5, 10, 20, 30]);
  });

  it('offers the number of runs when the server sends more than 30', () => {
    expect(countOptions(45)).toEqual([5, 10, 20, 30, 45]);
    expect(countOptions(500)).toEqual([5, 10, 20, 30, 100]);
  });

  it('reads a stored count that is one of the fixed ones', () => {
    expect(parseStoredCount('10')).toBe(10);
    expect(parseStoredCount('30')).toBe(30);
  });

  it('falls back to 30 for a missing or a wrong stored count', () => {
    expect(TIMELINE_COUNT_DEFAULT).toBe(30);
    expect(TIMELINE_COUNTS).toEqual([5, 10, 20, 30]);
    for (const raw of [null, '', 'abc', '7', '-5']) {
      expect(parseStoredCount(raw)).toBe(30);
    }
  });
});

describe('timelineWindow', () => {
  const runs = Array.from({ length: 12 }, (_, i) => run(`r${i}`));
  const ids = (list: { dagRunId: string }[]) => list.map((r) => r.dagRunId);

  it('keeps every run that fits', () => {
    expect(timelineWindow(runs, null, 12)).toHaveLength(12);
    expect(timelineWindow(runs, null, 20)).toHaveLength(12);
  });

  it('keeps the newest runs, oldest first', () => {
    expect(ids(timelineWindow(runs, 'r11', 3))).toEqual(['r9', 'r10', 'r11']);
    expect(ids(timelineWindow(runs, 'r11', 5))).toHaveLength(5);
  });

  it('moves back to show a selected run that is older, at the same size', () => {
    expect(ids(timelineWindow(runs, 'r4', 3))).toEqual(['r4', 'r5', 'r6']);
    expect(ids(timelineWindow(runs, 'unknown', 3))).toEqual([
      'r9',
      'r10',
      'r11',
    ]);
  });
});

describe('placeBars', () => {
  const pitch = BAR_PX + BAR_GAP_PX;

  it('centres a bar on its pixel', () => {
    expect(placeBars([100], 400)).toEqual([100 - BAR_PX / 2]);
  });

  it('puts bars of the same pixel side by side, never overlapping', () => {
    const lefts = placeBars([200, 200, 200, 200], 400);
    for (let i = 1; i < lefts.length; i += 1) {
      expect(lefts[i]! - lefts[i - 1]!).toBe(pitch);
    }
  });

  it('leaves bars that are far apart where the time puts them', () => {
    expect(placeBars([50, 150, 350], 400)).toEqual([48, 148, 348]);
  });

  it('pulls a crowd at the end back inside the track, in order', () => {
    const lefts = placeBars([398, 398, 398], 400);
    expect(lefts[2]).toBe(400 - BAR_PX);
    expect(lefts[1]).toBe(lefts[2]! - pitch);
    expect(lefts[0]).toBe(lefts[1]! - pitch);
  });

  it('pushes a crowd at the start to the right of 0', () => {
    expect(placeBars([0, 0, 0], 400)).toEqual([0, pitch, 2 * pitch]);
  });
});

describe('runTimes', () => {
  it('reads the start, else the queue time, else the neighbour', () => {
    const times = runTimes(
      [
        run('a', [], Status.Success, { startedAt: at(2, 18) }),
        run('b', [], Status.Queued, { queuedAt: at(2, 19) }),
        run('c', [], Status.NotStarted),
      ],
      0
    );
    expect(times).toEqual([
      Date.parse(at(2, 18)),
      Date.parse(at(2, 19)),
      Date.parse(at(2, 19)),
    ]);
  });

  it('takes now when no run has a time', () => {
    expect(runTimes([run('a', [], Status.NotStarted)], 123)).toEqual([123]);
  });
});

describe('timelineLayout', () => {
  const WIDTH = 800;
  const inner = WIDTH - 2 * EDGE_PAD_PX;
  const now = Date.parse(at(3, 9));
  const two18AndOne22 = [
    runAt('a', at(2, 18, 0)),
    runAt('b', at(2, 18, 1)),
    runAt('c', at(2, 22, 0)),
  ];

  it('leaves the gap between 18:00 and 22:00 empty and in proportion', () => {
    const { bars } = timelineLayout(two18AndOne22, now, WIDTH);
    // 4h = 240 minutes over the inner width; the runs 1 minute apart are
    // 3.3px apart: side by side at the pitch of the bars.
    const [a, b, c] = bars;
    // Whole pixels: the bars are within 2px of the exact scale.
    expect(Math.abs(c!.centre - a!.centre - inner)).toBeLessThan(2);
    expect(b!.left - a!.left).toBe(BAR_PX + BAR_GAP_PX);
    expect(c!.left - b!.left).toBeGreaterThan(inner - 10);
    // No bar between the two clusters.
    expect(bars.filter((x) => x.centre > 20 && x.centre < 780)).toHaveLength(0);
  });

  it('scales the gap with time on the same axis', () => {
    const runs = [
      runAt('a', at(2, 12, 0)),
      runAt('b', at(2, 14, 0)),
      runAt('c', at(2, 20, 0)),
    ];
    const { bars } = timelineLayout(runs, now, WIDTH);
    // 2h, then 6h: one to three.
    const first = bars[1]!.centre - bars[0]!.centre;
    const second = bars[2]!.centre - bars[1]!.centre;
    expect(second / first).toBeCloseTo(3, 1);
  });

  it('keeps the order of the input in the bars, whatever the times', () => {
    const { bars } = timelineLayout(
      [runAt('late', at(2, 20)), runAt('early', at(2, 8))],
      now,
      WIDTH
    );
    expect(bars.map((b) => b.index)).toEqual([0, 1]);
    expect(bars[0]!.centre).toBeGreaterThan(bars[1]!.centre);
  });

  it('draws runs of the same instant side by side, none outside the track', () => {
    const same = Array.from({ length: 12 }, (_, i) =>
      runAt(`s${i}`, at(2, 18))
    );
    const { bars } = timelineLayout(same, now, WIDTH);
    const sorted = [...bars].sort((x, y) => x.left - y.left);
    for (let i = 1; i < sorted.length; i += 1) {
      expect(sorted[i]!.left - sorted[i - 1]!.left).toBeGreaterThanOrEqual(
        BAR_PX + BAR_GAP_PX
      );
    }
    for (const bar of bars) {
      expect(bar.left).toBeGreaterThanOrEqual(0);
      expect(bar.left + BAR_PX).toBeLessThanOrEqual(WIDTH);
    }
    // One instant: the axis spans a minute, the cluster is in the middle.
    const middle = (sorted[0]!.left + sorted[11]!.left + BAR_PX) / 2;
    expect(Math.abs(middle - WIDTH / 2)).toBeLessThan(1);
  });

  it('gives each bar a hit range that does not cross its neighbour', () => {
    const { bars } = timelineLayout(two18AndOne22, now, WIDTH);
    for (const bar of bars) {
      expect(bar.hitLeft).toBeLessThanOrEqual(bar.left);
      expect(bar.hitRight).toBeGreaterThanOrEqual(bar.left + BAR_PX);
    }
    expect(bars[0]!.hitRight).toBeLessThanOrEqual(bars[1]!.hitLeft + 0.001);
    // The empty space between 18h and 22h is not a target.
    expect(bars[1]!.hitRight - bars[1]!.centre).toBeLessThanOrEqual(14);
    expect(bars[2]!.hitLeft).toBeGreaterThan(bars[1]!.hitRight);
  });

  it('reaches now while a run runs', () => {
    const runs = [
      runAt('a', at(2, 18)),
      runAt('b', at(3, 8, 30), Status.Running),
    ];
    const { bars, nowX, t1 } = timelineLayout(runs, now, WIDTH);
    expect(t1).toBe(now);
    // Started 30 minutes before now, on a 14.5 hour axis.
    expect(nowX).toBeCloseTo(WIDTH - EDGE_PAD_PX, 5);
    expect(Math.abs(nowX! - bars[1]!.centre - inner * (30 / 870))).toBeLessThan(
      2
    );
  });

  it('has no now line when nothing runs, and ends at the newest start', () => {
    const { nowX, t1 } = timelineLayout(two18AndOne22, now, WIDTH);
    expect(nowX).toBeNull();
    expect(t1).toBe(Date.parse(at(2, 22)));
  });

  it('spans one minute at the least and centres a single run', () => {
    const { t0, t1, bars } = timelineLayout(
      [runAt('a', at(2, 18))],
      now,
      WIDTH
    );
    expect(t1 - t0).toBe(MIN_SPAN_MS);
    expect(bars[0]!.centre).toBeCloseTo(WIDTH / 2, 0);
  });

  it('has an empty layout for no run', () => {
    const layout = timelineLayout([], now, WIDTH);
    expect(layout.bars).toEqual([]);
  });
});

describe('timelineLayout extents', () => {
  const WIDTH = 800;
  const inner = WIDTH - 2 * EDGE_PAD_PX;
  const now = Date.parse(at(3, 9));
  const span = (
    id: string,
    from: string,
    to: string,
    status = Status.Success
  ) => run(id, [], status, { startedAt: from, finishedAt: to });

  it('draws one extent from the start to the finish of a run', () => {
    const runs = [
      span('a', at(2, 8), at(2, 9)),
      span('b', at(2, 10), at(2, 12)),
    ];
    const { extents, xOf } = timelineLayout(runs, now, WIDTH);
    expect(extents.map((e) => e.id)).toEqual(['a', 'b']);
    expect(extents[0]!.x0).toBeCloseTo(xOf(Date.parse(at(2, 8))), 5);
    expect(extents[1]!.x1).toBeCloseTo(xOf(Date.parse(at(2, 12))), 5);
    expect(extents.every((e) => e.lane === 0 && !e.clippedLeft)).toBe(true);
  });

  it('takes the latest finish for the end of the axis', () => {
    const runs = [
      span('long', at(2, 8), at(3, 8)),
      span('short', at(2, 9), at(2, 9, 5)),
    ];
    const { t0, t1, extents } = timelineLayout(runs, now, WIDTH);
    expect(t0).toBe(Date.parse(at(2, 8)));
    expect(t1).toBe(Date.parse(at(3, 8)));
    // The long run covers the whole axis and may carry its duration.
    const long = extents.find((e) => e.id === 'long')!;
    expect(long.x1 - long.x0).toBeCloseTo(inner, 5);
    expect(long.labelled).toBe(true);
    expect(extents.find((e) => e.id === 'short')!.labelled).toBe(false);
  });

  it('labels only the longest extent when several span the view', () => {
    const runs = [
      span('a', at(2, 8), at(3, 8)),
      span('b', at(2, 8, 30), at(3, 8)),
      span('c', at(2, 9), at(2, 9, 5)),
    ];
    const { extents } = timelineLayout(runs, now, WIDTH);
    expect(extents.filter((e) => e.labelled).map((e) => e.id)).toEqual(['a']);
  });

  it('puts overlapping runs in different lanes and lists the overlaps', () => {
    const runs = [
      span('a', at(2, 8), at(2, 12)),
      span('b', at(2, 9), at(2, 10)),
      span('c', at(2, 13), at(2, 14)),
    ];
    const { extents } = timelineLayout(runs, now, WIDTH);
    const by = (id: string) => extents.find((e) => e.id === id)!;
    expect(by('a').lane).toBe(0);
    expect(by('b').lane).toBe(1);
    expect(by('c').lane).toBe(0);
    expect(by('a').overlaps).toEqual(['b']);
    expect(by('c').overlaps).toEqual([]);
  });

  it('extends a running run to now', () => {
    const runs = [
      span('a', at(2, 8), at(2, 9)),
      run('b', [], Status.Running, { startedAt: at(3, 8) }),
    ];
    const { extents, nowX, t1 } = timelineLayout(runs, now, WIDTH);
    const live = extents.find((e) => e.id === 'b')!;
    expect(t1).toBe(now);
    expect(live.running).toBe(true);
    expect(live.end).toBe(now);
    expect(live.x1).toBeCloseTo(nowX!, 5);
  });

  it('caps the lanes at four and counts the hidden extents', () => {
    const runs = Array.from({ length: 6 }, (_, i) =>
      span(`r${i}`, at(2, 8, i), at(2, 12))
    );
    const { extents, hidden } = timelineLayout(runs, now, WIDTH);
    expect(Math.max(...extents.map((e) => e.lane))).toBe(3);
    expect(hidden).toBe(2);
    expect(extents.filter((e) => e.overflow)).toHaveLength(2);
    expect(extents[0]!.overlaps).toHaveLength(5);
  });

  it('clips an older run that lasts into the view at the left edge', () => {
    const older = [span('old', at(1, 8), at(2, 9))];
    const runs = [
      span('a', at(2, 8), at(2, 8, 30)),
      span('b', at(2, 10), at(2, 11)),
    ];
    const { extents, t0 } = timelineLayout(runs, now, WIDTH, older);
    const clipped = extents.find((e) => e.id === 'old')!;
    expect(t0).toBe(Date.parse(at(2, 8)));
    expect(clipped.index).toBe(-1);
    expect(clipped.clippedLeft).toBe(true);
    expect(clipped.x0).toBe(0);
    expect(clipped.overlaps).toEqual(['a']);
    // Its start is before the view: its true start stays in the data.
    expect(clipped.start).toBe(Date.parse(at(1, 8)));
  });

  it('leaves out an older run that ended before the view', () => {
    const older = [span('old', at(1, 8), at(1, 9))];
    const runs = [span('a', at(2, 8), at(2, 9))];
    const { extents } = timelineLayout(runs, now, WIDTH, older);
    expect(extents.map((e) => e.id)).toEqual(['a']);
  });

  it('lets an older run that still runs reach now', () => {
    const older = [run('old', [], Status.Running, { startedAt: at(1, 8) })];
    const runs = [span('a', at(2, 8), at(2, 9))];
    const { extents, nowX, t1 } = timelineLayout(runs, now, WIDTH, older);
    expect(t1).toBe(now);
    expect(nowX).not.toBeNull();
    expect(extents.find((e) => e.id === 'old')!.x1).toBeCloseTo(nowX!, 5);
  });

  it('draws no extent for a run that has not started', () => {
    const { extents } = timelineLayout(
      [run('q', [], Status.Queued, { queuedAt: at(2, 8) })],
      now,
      WIDTH
    );
    expect(extents).toEqual([]);
  });

  it('gives an instant run a visible extent', () => {
    const { extents } = timelineLayout(
      [run('i', [], Status.Failed, { startedAt: at(2, 8) })],
      now,
      WIDTH
    );
    expect(extents[0]!.x1 - extents[0]!.x0).toBeGreaterThanOrEqual(2);
  });
});

describe('timeTicks', () => {
  const ticks = (from: string, to: string, width = 800) =>
    timeTicks(Date.parse(from), Date.parse(to), width);
  const labels = (list: { label: string }[]) => list.map((t) => t.label);

  it('ticks every second or so on a span of seconds', () => {
    const list = ticks(at(2, 18, 0, 0), at(2, 18, 0, 40));
    expect(list.length).toBeGreaterThan(3);
    expect(list[0]!.label).toMatch(/^18:00:\d\d$/);
  });

  it('ticks on whole minutes on a span of minutes', () => {
    const list = ticks(at(2, 18, 3, 20), at(2, 18, 40, 0));
    expect(list.every((t) => /^18:\d\d$/.test(t.label))).toBe(true);
    expect(new Date(list[0]!.v).getSeconds()).toBe(0);
  });

  it('ticks on whole hours on a span of 4 hours', () => {
    const list = ticks(at(2, 18, 0), at(2, 22, 0));
    expect(labels(list)).toContain('20:00');
    for (const t of list) expect(new Date(t.v).getMinutes() % 30).toBe(0);
    expect(list.length).toBeLessThanOrEqual(20);
  });

  it('gives a coarser step on a narrow axis', () => {
    const wide = ticks(at(2, 6, 0), at(2, 22, 0), 1000);
    const narrow = ticks(at(2, 6, 0), at(2, 22, 0), 200);
    expect(narrow.length).toBeLessThan(wide.length);
    expect(narrow.length).toBeGreaterThan(0);
  });

  it('carries the date under a clock label', () => {
    const [first] = ticks(at(2, 18, 0), at(2, 22, 0));
    expect(first!.date).toBe('Mar 2');
    expect(first!.year).toBe('2026');
  });

  it('puts midnight on a span of days, and labels it with the date', () => {
    const list = ticks(at(1, 6), at(4, 6));
    const midnights = list.filter((t) => new Date(t.v).getHours() === 0);
    expect(midnights.length).toBeGreaterThanOrEqual(3);
  });

  it('ticks on days for a span of weeks', () => {
    const list = ticks(at(1, 6), at(21, 6));
    expect(list.length).toBeGreaterThan(2);
    for (const t of list) {
      expect(new Date(t.v).getHours()).toBe(0);
      expect(t.label).toMatch(/^[A-Z][a-z]{2} \d{1,2}$/);
      expect(t.date).toBe('');
    }
  });

  it('ticks on months for a span of months', () => {
    const list = ticks(at(15, 0, 0, 0, 0), at(15, 0, 0, 0, 8), 600);
    expect(list.length).toBeGreaterThan(2);
    for (const t of list) expect(new Date(t.v).getDate()).toBe(1);
  });

  it('keeps ticks inside the span, in order', () => {
    const t0 = Date.parse(at(2, 18, 20));
    const t1 = Date.parse(at(2, 21, 40));
    const list = timeTicks(t0, t1, 800);
    for (const t of list) {
      expect(t.v).toBeGreaterThanOrEqual(t0);
      expect(t.v).toBeLessThanOrEqual(t1);
    }
    expect(list.map((t) => t.v)).toEqual([...list.map((t) => t.v)].sort());
  });
});

describe('layoutLabels', () => {
  const t0 = Date.parse(at(2, 18, 0));
  const t1 = Date.parse(at(3, 6, 0));
  const width = 800;
  const xOf = (t: number) => ((t - t0) / (t1 - t0)) * width;
  const laid = layoutLabels(timeTicks(t0, t1, width), xOf, width);

  it('keeps the labels apart and inside the track', () => {
    expect(laid.length).toBeGreaterThan(3);
    for (let i = 1; i < laid.length; i += 1) {
      expect(laid[i]!.x - laid[i - 1]!.x).toBeGreaterThan(30);
    }
    for (const l of laid) {
      expect(l.x).toBeGreaterThanOrEqual(0);
      expect(l.x).toBeLessThanOrEqual(width);
    }
  });

  it('shows the date under the first label and where the day changes', () => {
    const withContext = laid.filter((l) => l.context);
    expect(withContext[0]).toBe(laid[0]);
    expect(withContext[0]!.context).toBe('Mar 2 2026');
    const midnight = laid.find((l) => l.label === '00:00');
    expect(midnight?.context).toBe('Mar 3');
  });

  it('skips a label that has no room', () => {
    const crowded = layoutLabels(timeTicks(t0, t1, 4000), xOf, width);
    for (let i = 1; i < crowded.length; i += 1) {
      expect(crowded[i]!.x - crowded[i - 1]!.x).toBeGreaterThan(30);
    }
  });
});

// At 390px the labels of the axis never touch, whatever the span of the runs:
// a span of a few seconds is the stress case, with the one-minute minimum.
describe('timelineLayout labels at 390px', () => {
  const CHAR_PX = 6.4;
  it.each([2, 5, 40, 300])(
    'keeps the labels apart for runs %is apart',
    (seconds) => {
      const runs = [
        runAt('a', at(2, 18, 0, 0)),
        runAt('b', at(2, 18, 0, seconds)),
        runAt('c', at(2, 18, 0, seconds * 2)),
      ];
      const { ticks } = timelineLayout(runs, Date.parse(at(2, 19)), 390);
      for (let i = 1; i < ticks.length; i += 1) {
        const before = ticks[i - 1]!;
        const tick = ticks[i]!;
        const gap =
          tick.x -
          (tick.label.length * CHAR_PX) / 2 -
          (before.x + (before.label.length * CHAR_PX) / 2);
        expect(gap).toBeGreaterThanOrEqual(0);
      }
      for (const tick of ticks) {
        expect(
          tick.x - (tick.label.length * CHAR_PX) / 2
        ).toBeGreaterThanOrEqual(0);
        expect(tick.x + (tick.label.length * CHAR_PX) / 2).toBeLessThanOrEqual(
          390
        );
      }
    }
  );
});

describe('test runs in the model', () => {
  const at = (id: string, seconds: number, extra = {}) =>
    ({
      dagRunId: id,
      status: Status.Success,
      startedAt: '2026-03-02T10:00:00Z',
      finishedAt: new Date(
        Date.parse('2026-03-02T10:00:00Z') + seconds * 1000
      ).toISOString(),
      ...extra,
    }) as unknown as ApiDAGRunDetails;

  it('tells a test run by the flag of the server', () => {
    expect(isTestRun({ ephemeral: true })).toBe(true);
    expect(isTestRun({ ephemeral: false })).toBe(false);
    expect(isTestRun({})).toBe(false);
  });

  it('puts the hatch over the layer of the tone, and gives no background to a run that stays', () => {
    expect(fillBackground({})).toBeUndefined();
    expect(fillBackground({}, 'linear-gradient(red, blue)')).toBe(
      'linear-gradient(red, blue)'
    );
    expect(fillBackground({ ephemeral: true })).toBe(TEST_RUN_HATCH);
    expect(
      fillBackground({ ephemeral: true }, 'linear-gradient(red, blue)')
    ).toBe(`${TEST_RUN_HATCH}, linear-gradient(red, blue)`);
    // The hatch keeps the colour of the bar: lines in the page colour.
    expect(TEST_RUN_HATCH).toContain('var(--background)');
  });

  it('leaves the test runs out of the median of the long run', () => {
    const runs = [
      at('a', 600),
      at('b', 700),
      at('x1', 1, { ephemeral: true }),
      at('x2', 1, { ephemeral: true }),
      at('x3', 1, { ephemeral: true }),
    ];
    expect(longRunMedianMs(runs, NOW)).toBe(650_000);
    expect(longRunMedianMs([at('x', 1, { ephemeral: true })], NOW)).toBe(0);
  });

  it('reads the stored choice to show the test runs: on unless it says 0', () => {
    expect(parseStoredShowTests(null)).toBe(true);
    expect(parseStoredShowTests('1')).toBe(true);
    expect(parseStoredShowTests('0')).toBe(false);
    expect(parseStoredShowTests('x')).toBe(true);
  });
});

// A run that reused two steps: 10 s and 20 s in their source runs.
const SRC = '2026-03-01T08:00:00Z';
const reusing = (
  id: string,
  startedAt: string,
  seconds: number,
  extra: Partial<ApiDAGRunDetails> = {}
) =>
  ({
    dagRunId: id,
    status: Status.Success,
    startedAt,
    finishedAt: new Date(Date.parse(startedAt) + seconds * 1000).toISOString(),
    nodes: [],
    reusedInputs: [
      {
        step: 'a',
        runId: 'src',
        via: 'run',
        startedAt: SRC,
        finishedAt: '2026-03-01T08:00:10Z',
      },
      {
        step: 'b',
        runId: 'src',
        via: 'run',
        startedAt: '2026-03-01T08:00:10Z',
        finishedAt: '2026-03-01T08:00:30Z',
      },
    ],
    ...extra,
  }) as unknown as ApiDAGRunDetails;

describe('the cached time', () => {
  const r = reusing('r', '2026-03-01T10:00:00Z', 5);

  it('is the sum of the durations of the reused steps', () => {
    expect(cachedMsOf(r)).toBe(30_000);
    expect(cachedMsOf({} as ApiDAGRunDetails)).toBe(0);
  });

  it('makes the total time: own plus cached, only when it counts', () => {
    expect(durationParts(r, NOW, true)).toEqual({
      own: 5_000,
      cached: 30_000,
      total: 35_000,
    });
    expect(durationParts(r, NOW, false)).toEqual({
      own: 5_000,
      cached: 0,
      total: 5_000,
    });
    expect(totalDurationMs(r, NOW, true)).toBe(35_000);
    expect(
      durationParts({ status: Status.Queued } as ApiDAGRunDetails, NOW, true)
    ).toBeNull();
  });

  it('shares the height of a bar by the share of the cached time', () => {
    const parts = { own: 5_000, cached: 30_000, total: 35_000 };
    expect(cachedBarPx(35, parts)).toBe(30);
    // Both parts keep a pixel.
    expect(
      cachedBarPx(10, { own: 1, cached: 1_000_000, total: 1_000_001 })
    ).toBe(9);
    expect(
      cachedBarPx(10, { own: 1_000_000, cached: 1, total: 1_000_001 })
    ).toBe(1);
    expect(cachedBarPx(10, { own: 5, cached: 0, total: 5 })).toBe(0);
    expect(cachedBarPx(10, null)).toBe(0);
  });

  it('counts in the median of the long run only when it counts', () => {
    expect(longRunMedianMs([r], NOW, true)).toBe(35_000);
    expect(longRunMedianMs([r], NOW, false)).toBe(5_000);
  });

  it('reads the stored choice: on unless it says 0', () => {
    expect(parseStoredShowCached(null)).toBe(true);
    expect(parseStoredShowCached('1')).toBe(true);
    expect(parseStoredShowCached('0')).toBe(false);
  });
});

describe('the layout with cached time', () => {
  const T0 = Date.parse('2026-03-01T10:00:00Z');
  const r = reusing('r', '2026-03-01T10:00:00Z', 60);
  const plain = reusing('p', '2026-03-01T10:30:00Z', 60, {
    reusedInputs: undefined,
  });

  it('moves the start of the extent back by the cached time, and hatches that part', () => {
    const { extents, t0, xOf } = timelineLayout([r, plain], NOW, 640, [], {
      cachedOf: cachedMsOf,
    });
    const e = extents.find((x) => x.id === 'r')!;
    expect(e.start).toBe(T0 - 30_000);
    expect(e.ownStart).toBe(T0);
    expect(e.end).toBe(T0 + 60_000);
    expect(e.end - e.start).toBe(90_000);
    // The axis starts where the cached time starts.
    expect(t0).toBe(T0 - 30_000);
    expect(e.x0).toBeCloseTo(xOf(T0 - 30_000));
    expect(e.cachedPx).toBeCloseTo(xOf(T0) - xOf(T0 - 30_000));
    expect(extents.find((x) => x.id === 'p')!.cachedPx).toBe(0);
  });

  it('leaves a run without cached time as it is', () => {
    const a = timelineLayout([plain], NOW, 640, []);
    const b = timelineLayout([plain], NOW, 640, [], { cachedOf: cachedMsOf });
    expect(b.extents).toEqual(a.extents);
    expect([b.t0, b.t1]).toEqual([a.t0, a.t1]);
  });

  it('packs the lanes with the extended extents', () => {
    // The first run ends at 10:01; the second starts at 10:01:10 and its
    // cached time of 30 s starts at 10:00:40, in the first run.
    const first = reusing('f', '2026-03-01T10:00:00Z', 60, {
      reusedInputs: undefined,
    });
    const second = reusing('s', '2026-03-01T10:01:10Z', 60);
    const own = timelineLayout([first, second], NOW, 4000, []);
    const total = timelineLayout([first, second], NOW, 4000, [], {
      cachedOf: cachedMsOf,
    });
    expect(own.extents.find((x) => x.id === 's')!.lane).toBe(0);
    expect(total.extents.find((x) => x.id === 's')!.lane).toBe(1);
    expect(total.extents.find((x) => x.id === 's')!.overlaps).toEqual(['f']);
  });

  it('fits the axis to a range, also where no run is', () => {
    const from = T0 - 3_600_000;
    const to = T0 + 3_600_000;
    const { t0, t1 } = timelineLayout([plain], NOW, 640, [], {
      range: { from, to },
    });
    expect([t0, t1]).toEqual([from, to]);
  });
});

describe('the time range', () => {
  it('reads the stored mode and range', () => {
    expect(parseStoredMode('range')).toBe('range');
    expect(parseStoredMode('count')).toBe('count');
    expect(parseStoredMode(null)).toBe('count');
    expect(parseStoredRange('{"preset":"6h"}')).toEqual({ preset: '6h' });
    expect(parseStoredRange('{"preset":"custom","from":10,"to":20}')).toEqual({
      preset: 'custom',
      from: 10,
      to: 20,
    });
    // Not valid: the default applies.
    for (const raw of [
      null,
      'x',
      '{"preset":"2h"}',
      '{"preset":"custom","from":20,"to":10}',
      '{"preset":"custom","from":"a","to":1}',
    ]) {
      expect(parseStoredRange(raw)).toEqual({ preset: '24h' });
    }
  });

  it('ends a preset at now, and keeps the ends of a custom range', () => {
    expect(rangeBounds({ preset: '1h' }, NOW)).toEqual({
      from: NOW - 3_600_000,
      to: NOW,
    });
    expect(rangeBounds({ preset: '7d' }, NOW).from).toBe(NOW - 7 * 86_400_000);
    expect(rangeBounds({ preset: 'custom', from: 1, to: 2 }, NOW)).toEqual({
      from: 1,
      to: 2,
    });
  });

  it('keeps the runs whose extent overlaps the range', () => {
    const run10 = reusing('r', '2026-03-01T10:00:00Z', 60, {
      reusedInputs: undefined,
    });
    const at = (iso: string) => Date.parse(iso);
    const bounds = (a: string, b: string) => ({ from: at(a), to: at(b) });
    expect(
      runInRange(
        run10,
        NOW,
        bounds('2026-03-01T10:00:30Z', '2026-03-01T11:00:00Z'),
        false
      )
    ).toBe(true);
    expect(
      runInRange(
        run10,
        NOW,
        bounds('2026-03-01T10:02:00Z', '2026-03-01T11:00:00Z'),
        false
      )
    ).toBe(false);
    expect(
      runInRange(
        run10,
        NOW,
        bounds('2026-03-01T08:00:00Z', '2026-03-01T09:59:00Z'),
        false
      )
    ).toBe(false);
    // The cached time of 30 s starts before 10:00:00.
    const cached = reusing('c', '2026-03-01T10:00:00Z', 60);
    expect(
      runInRange(
        cached,
        NOW,
        bounds('2026-03-01T09:00:00Z', '2026-03-01T09:59:45Z'),
        true
      )
    ).toBe(true);
    expect(
      runInRange(
        cached,
        NOW,
        bounds('2026-03-01T09:00:00Z', '2026-03-01T09:59:45Z'),
        false
      )
    ).toBe(false);
    // A queued run counts at its queue time.
    const queued = {
      dagRunId: 'q',
      status: Status.Queued,
      queuedAt: '2026-03-01T10:00:00Z',
    } as unknown as ApiDAGRunDetails;
    expect(
      runInRange(
        queued,
        NOW,
        bounds('2026-03-01T09:00:00Z', '2026-03-01T11:00:00Z'),
        true
      )
    ).toBe(true);
  });
});

describe('adjacentVisibleRunId', () => {
  // Oldest first: a normal run, a test run, a normal run.
  const runs = [
    reusing('n1', '2026-03-01T10:00:00Z', 1, { reusedInputs: undefined }),
    reusing('t1', '2026-03-01T11:00:00Z', 1, {
      reusedInputs: undefined,
      ephemeral: true,
    }),
    reusing('n2', '2026-03-01T12:00:00Z', 1, { reusedInputs: undefined }),
  ];

  it('goes normal, test, normal, in the order of the timeline', () => {
    expect(adjacentVisibleRunId(runs, 'n1', 'newer')).toBe('t1');
    expect(adjacentVisibleRunId(runs, 't1', 'newer')).toBe('n2');
    expect(adjacentVisibleRunId(runs, 'n2', 'older')).toBe('t1');
    expect(adjacentVisibleRunId(runs, 't1', 'older')).toBe('n1');
  });

  it('stops at the ends', () => {
    expect(adjacentVisibleRunId(runs, 'n1', 'older')).toBeNull();
    expect(adjacentVisibleRunId(runs, 'n2', 'newer')).toBeNull();
    expect(adjacentVisibleRunId([], 'n2', 'older')).toBeNull();
  });

  // A test run that expired is not in the list any more: the selected run
  // sits at its own time between the listed runs.
  it('places a selected run that the list lacks by its time', () => {
    const at = Date.parse('2026-03-01T11:30:00Z');
    expect(adjacentVisibleRunId(runs, 'gone', 'older', at)).toBe('t1');
    expect(adjacentVisibleRunId(runs, 'gone', 'newer', at)).toBe('n2');
    expect(
      adjacentVisibleRunId(
        runs,
        'gone',
        'newer',
        Date.parse('2026-03-02T00:00:00Z')
      )
    ).toBeNull();
  });

  it('counts a selected run without a time as newer than every listed run', () => {
    expect(adjacentVisibleRunId(runs, 'gone', 'older')).toBe('n2');
    expect(adjacentVisibleRunId(runs, 'gone', 'newer')).toBeNull();
    expect(adjacentVisibleRunId(runs, null, 'older')).toBe('n2');
  });

  it('reads the stored trigger types to hide, and drops what is not one', () => {
    expect(parseStoredHiddenTypes(null)).toEqual([]);
    expect(parseStoredHiddenTypes('x')).toEqual([]);
    expect(parseStoredHiddenTypes('{"a":1}')).toEqual([]);
    expect(parseStoredHiddenTypes('["webhook","bogus","manual"]')).toEqual([
      TriggerType.manual,
      TriggerType.webhook,
    ]);
  });

  it('reads a run with no trigger type as Unknown', () => {
    expect(triggerOf({})).toBe(TriggerType.unknown);
    expect(triggerOf({ triggerType: TriggerType.webhook })).toBe(
      TriggerType.webhook
    );
  });
});

describe('axisClock', () => {
  const H = 3_600_000;

  it('keeps the second while a pixel of the axis lasts one second or less', () => {
    // One minute over 600 px: 100 ms for a pixel.
    expect(axisClock(10_999, 60_000, 600)).toBe(10_000);
    expect(axisClock(11_000, 60_000, 600)).toBe(11_000);
  });

  it('moves in steps of at most one pixel of the axis', () => {
    // Three hours over 1000 px: 10.8 s for a pixel, so a step of 8 s.
    const span = 3 * H;
    const step = 8_000;
    const base = 1_700_000_000_000 - (1_700_000_000_000 % step);
    expect(axisClock(base + 7_999, span, 1000)).toBe(base);
    expect(axisClock(base + 8_000, span, 1000)).toBe(base + step);
    for (const now of [base + 1, base + 4_000, base + 7_999]) {
      expect(axisClock(now, span, 1000)).toBe(base);
    }
  });

  it('is never ahead of the time, and never more than one pixel behind it', () => {
    const span = 26 * H;
    const plot = 900;
    const pixelMs = span / plot;
    for (let k = 0; k < 200; k += 1) {
      const now = 1_700_000_000_000 + k * 977;
      const clock = axisClock(now, span, plot);
      expect(clock).toBeLessThanOrEqual(now);
      expect(now - clock).toBeLessThan(pixelMs);
    }
  });

  it('reads a span of zero as the second', () => {
    expect(axisClock(5_500, 0, 600)).toBe(5_000);
    expect(axisClock(5_500, Number.NaN, 600)).toBe(5_000);
  });
});
