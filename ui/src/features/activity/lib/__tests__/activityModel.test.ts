// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { DAGRunActivityQueueBlockedReason, Status } from '@/api/v1/schema';
import {
  type ActivityRun,
  attentionItems,
  barSpan,
  type DAGRunActivity,
  fitRange,
  groupLanes,
  inProgressRuns,
  isQueueStalled,
  isSchedulerHoldingWork,
  runEnd,
  sortLanes,
  systemAlerts,
  timeTicks,
  withEarlierActiveRuns,
} from '../activityModel';

const HOUR = 3600;
const FROM = 1_790_000_000 - (1_790_000_000 % 900);
const WINDOW = { from: FROM, to: FROM + 24 * HOUR };

function run(
  name: string,
  dagRunId: string,
  status: Status,
  startedAt: number,
  finishedAt?: number
): ActivityRun {
  return { name, dagRunId, status, startedAt, finishedAt };
}

function activity(overrides: Partial<DAGRunActivity> = {}): DAGRunActivity {
  return {
    ...WINDOW,
    runs: [],
    attention: [],
    active: [],
    upcoming: [],
    queues: [],
    ...overrides,
  };
}

describe('groupLanes', () => {
  it("keeps each workflow's runs newest first with the newest as latest", () => {
    const lanes = groupLanes([
      run('etl', 'etl-3', Status.Failed, FROM + 3 * HOUR),
      run('ping', 'ping-1', Status.Success, FROM + 2 * HOUR),
      run('etl', 'etl-2', Status.Success, FROM + HOUR),
      run('etl', 'etl-1', Status.Rejected, FROM),
    ]);

    expect(lanes).toEqual([
      expect.objectContaining({
        name: 'etl',
        latest: expect.objectContaining({ dagRunId: 'etl-3' }),
        failedCount: 2,
      }),
      expect.objectContaining({ name: 'ping', failedCount: 0 }),
    ]);
    expect(lanes[0]!.runs.map((item) => item.dagRunId)).toEqual([
      'etl-3',
      'etl-2',
      'etl-1',
    ]);
  });
});

describe('attentionItems', () => {
  it('lists failing workflows, then waiting runs, then slow runs', () => {
    const now = FROM + 10 * HOUR;
    // train usually takes 10 minutes; quick has too few runs to judge.
    const loaded = [
      run('train', 't-a', Status.Success, FROM, FROM + 600),
      run('train', 't-b', Status.Success, FROM + HOUR, FROM + HOUR + 500),
      run(
        'train',
        't-c',
        Status.Success,
        FROM + 2 * HOUR,
        FROM + 2 * HOUR + 700
      ),
      run('quick', 'q-a', Status.Success, FROM, FROM + 10),
    ];
    const items = attentionItems(
      activity({
        attention: [
          run('newer-failure', 'n-1', Status.Failed, FROM + 3 * HOUR),
          run('older-failure', 'o-1', Status.Failed, FROM + HOUR),
        ],
        active: [
          { name: 'approve', dagRunId: 'a-1', status: Status.Waiting },
          run('train', 't-1', Status.Running, FROM + 9 * HOUR),
          run('quick', 'q-1', Status.Running, FROM + 9 * HOUR),
        ],
      }),
      loaded,
      now
    );

    expect(items.map((item) => `${item.kind}:${item.name}`)).toEqual([
      'failing:newer-failure',
      'failing:older-failure',
      'waiting:approve',
      'slow:train',
    ]);
    expect(items[3]).toMatchObject({ usualSeconds: 600 });
  });
});

describe('inProgressRuns', () => {
  // Waiting runs need attention, and a queue line covers its queued runs.
  it('lists running runs and queued runs no queue line covers', () => {
    const runs = inProgressRuns(
      activity({
        active: [
          { name: 'a', dagRunId: '1', status: Status.Running, queue: 'a' },
          { name: 'b', dagRunId: '2', status: Status.Waiting },
          { name: 'c', dagRunId: '3', status: Status.Queued, queue: 'c' },
          { name: 'd', dagRunId: '4', status: Status.Queued, queue: 'd' },
          { name: 'e', dagRunId: '5', status: Status.Queued },
        ],
        queues: [{ name: 'c', running: 0, queued: 1 }],
      })
    );

    expect(runs.map((item) => item.dagRunId)).toEqual(['1', '4', '5']);
  });
});

describe('isQueueStalled', () => {
  const now = FROM + HOUR;
  const queue = {
    name: 'default',
    running: 0,
    maxConcurrency: 2,
    queued: 3,
    nextQueuedAt: now - 10 * 60,
  };

  it('flags a queue whose next run waits while it has room', () => {
    expect(isQueueStalled(queue, now)).toBe(true);
  });

  it('accepts a long wait in a full queue or a short one', () => {
    expect(isQueueStalled({ ...queue, running: 2 }, now)).toBe(false);
    expect(isQueueStalled({ ...queue, nextQueuedAt: now - 60 }, now)).toBe(
      false
    );
  });

  // A running count that is only a lower bound cannot show room.
  it('accepts a long wait when the running count is a lower bound', () => {
    expect(isQueueStalled({ ...queue, runningCapped: true }, now)).toBe(false);
  });
});

describe('isSchedulerHoldingWork', () => {
  const scheduled = activity({
    upcoming: [{ name: 'nightly', fileName: 'nightly', nextRun: FROM + HOUR }],
  });
  const queued = activity({
    queues: [{ name: 'default', running: 0, queued: 1 }],
  });

  it('flags a stopped scheduler while runs are scheduled or queued', () => {
    expect(isSchedulerHoldingWork(scheduled, false)).toBe(true);
    expect(isSchedulerHoldingWork(queued, false)).toBe(true);
  });

  // A queued run counts even when no queue line lists it, such as after a
  // failed queue read.
  it('flags a stopped scheduler with a queued run no queue lists', () => {
    const waiting = activity({
      active: [{ name: 'etl', dagRunId: 'etl-1', status: Status.Queued }],
    });

    expect(isSchedulerHoldingWork(waiting, false)).toBe(true);
  });

  // A node without schedules may run no scheduler on purpose, and an older
  // node does not report one at all.
  it('stays quiet without waiting work or a known stopped scheduler', () => {
    expect(isSchedulerHoldingWork(activity(), false)).toBe(false);
    expect(isSchedulerHoldingWork(scheduled, true)).toBe(false);
    expect(isSchedulerHoldingWork(scheduled, undefined)).toBe(false);
  });
});

describe('systemAlerts', () => {
  it('lists a held-back scheduler, then blocked queues, then the disk', () => {
    const alerts = systemAlerts(
      activity({
        queues: [
          { name: 'default', running: 0, queued: 1 },
          {
            name: 'gpu',
            running: 0,
            queued: 2,
            blockedReason: DAGRunActivityQueueBlockedReason.noMatchingWorker,
          },
        ],
        diskUsedPercent: 94,
      }),
      false
    );

    expect(alerts).toEqual([
      { kind: 'scheduler' },
      {
        kind: 'queue',
        queue: 'gpu',
        reason: DAGRunActivityQueueBlockedReason.noMatchingWorker,
      },
      { kind: 'disk', usedPercent: 94 },
    ]);
  });

  it('lists nothing for a healthy system', () => {
    expect(systemAlerts(activity(), true)).toEqual([]);
  });
});

describe('sortLanes', () => {
  it('puts failing workflows first, then running ones, then recent activity', () => {
    const lanes = sortLanes(
      groupLanes([
        run('recent', 'r-1', Status.Success, FROM + 6 * HOUR),
        run('busy', 'b-1', Status.Success, FROM + 5 * HOUR),
        run('quiet', 'q-1', Status.Success, FROM + HOUR),
        run('broken', 'x-1', Status.Failed, FROM),
      ]),
      [run('busy', 'b-0', Status.Running, FROM - HOUR)]
    );

    expect(lanes.map((lane) => lane.name)).toEqual([
      'broken',
      'busy',
      'recent',
      'quiet',
    ]);
  });
});

describe('fitRange', () => {
  it('starts a quarter hour before the oldest run', () => {
    const range = fitRange(WINDOW, [
      run('a', '2', Status.Success, FROM + 10 * HOUR),
      run('a', '1', Status.Success, FROM + 6 * HOUR),
    ]);

    expect(range).toEqual({ from: FROM + 6 * HOUR - 900, to: WINDOW.to });
  });

  it('keeps at least an hour in view', () => {
    const range = fitRange(WINDOW, [
      run('a', '1', Status.Success, WINDOW.to - 60),
    ]);

    expect(range.to - range.from).toBe(HOUR);
  });
});

describe('timeTicks', () => {
  it('chooses whole-hour ticks that fit the width', () => {
    const ticks = timeTicks({ from: FROM, to: FROM + 12 * HOUR }, 600);

    expect(ticks.length).toBeLessThanOrEqual(600 / 72);
    expect(ticks.every((tick) => tick % HOUR === 0)).toBe(true);
  });

  it('aligns ticks to the server time zone', () => {
    const offset = 30 * 60;
    const ticks = timeTicks({ from: FROM, to: FROM + 6 * HOUR }, 800, offset);

    expect(ticks.every((tick) => (tick + offset) % HOUR === 0)).toBe(true);
  });
});

describe('runEnd', () => {
  const base = { name: 'etl', dagRunId: 'r1', startedAt: 1000 };

  it('ends a finished run when it finished', () => {
    expect(
      runEnd({ ...base, status: Status.Success, finishedAt: 8200 }, 9000)
    ).toBe(8200);
  });

  // A waiting run keeps the time it paused at as its finish time.
  it.each([
    ['running', Status.Running],
    ['waiting', Status.Waiting],
  ])('extends a %s run to the current time', (_, status) => {
    expect(runEnd({ ...base, status, finishedAt: 2000 }, 5000)).toBe(5000);
  });

  it('ends a queued run where it starts', () => {
    expect(runEnd({ ...base, status: Status.Queued }, 5000)).toBe(1000);
  });
});

describe('barSpan', () => {
  const range = { from: 1000, to: 2000 };

  it('clamps a run that started before the range to its left edge', () => {
    expect(
      barSpan(run('etl', 'r1', Status.Success, 500, 1500), range, 1900)
    ).toEqual({ left: 0, right: 50 });
  });

  it('clamps a run that is still running to the range end', () => {
    expect(
      barSpan(run('etl', 'r1', Status.Running, 1500), range, 2500)
    ).toEqual({ left: 50, right: 100 });
  });

  it('gives a queued run no width', () => {
    expect(barSpan(run('etl', 'r1', Status.Queued, 1500), range, 1900)).toEqual(
      { left: 50, right: 50 }
    );
  });
});

describe('withEarlierActiveRuns', () => {
  const loaded = [
    run('etl', 'etl-2', Status.Success, FROM + 2 * HOUR),
    run('etl', 'etl-1', Status.Running, FROM + HOUR),
  ];

  it('adds runs still going from before the window, newest first', () => {
    const older = run('backfill', 'b-1', Status.Running, FROM - 2 * HOUR);
    const newer = run('sync', 's-1', Status.Running, FROM - HOUR);

    expect(withEarlierActiveRuns(loaded, [older, newer], FROM)).toEqual([
      ...loaded,
      newer,
      older,
    ]);
  });

  it('skips loaded runs and runs that began inside the window', () => {
    const inside = run('sync', 's-2', Status.Running, FROM + 3 * HOUR);

    expect(withEarlierActiveRuns(loaded, [loaded[1]!, inside], FROM)).toBe(
      loaded
    );
  });

  it('keeps another workflow’s run that shares a loaded run ID', () => {
    const report = run('report', 'daily', Status.Success, FROM + HOUR);
    const backfill = run('backfill', 'daily', Status.Running, FROM - HOUR);

    expect(withEarlierActiveRuns([report], [backfill], FROM)).toEqual([
      report,
      backfill,
    ]);
  });
});
