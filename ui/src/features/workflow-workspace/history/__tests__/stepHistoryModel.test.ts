// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { NodeStatus, ReusedInputVia, Status } from '@/api/v1/schema';
import type { ApiDAGDetails, ApiDAGRunDetails } from '../../contracts';
import { node, run } from '../../data/__tests__/fixtures';
import {
  hasRunningStep,
  isFailedEntry,
  stepGrid,
  stepHistory,
  summarizeStepHistory,
  withLiveRun,
  type StepRunEntry,
} from '../stepHistoryModel';

const NOW = Date.parse('2026-01-15T03:00:00Z');
const gone = { startedAt: '-', finishedAt: '-', doneCount: 0 };

/** A run that started at the minute `at` of 2026-01-15 02:00. */
function runAt(
  id: string,
  at: number,
  nodes = [node('a')],
  status = Status.Success,
  extra: Partial<ApiDAGRunDetails> = {}
): ApiDAGRunDetails {
  const minute = String(at).padStart(2, '0');
  return run(id, nodes, status, {
    startedAt: `2026-01-15T02:${minute}:00Z`,
    ...extra,
  });
}

// fetch -> extract -> load: the run executed load, carried fetch from the
// source run, and could not get extract from it.
const reusing = runAt(
  'r-reuse',
  30,
  [
    node('fetch', NodeStatus.Skipped),
    node('extract', NodeStatus.Skipped, {
      ...gone,
      step: { depends: ['fetch'] },
    }),
    node('load', NodeStatus.Success, { step: { depends: ['extract'] } }),
  ],
  Status.Success,
  {
    labels: ['dagu.outputs-from=r-source'],
    reusedInputs: [
      {
        step: 'fetch',
        runId: 'r-source',
        startedAt: '2026-01-01T00:00:00Z',
        finishedAt: '2026-01-01T00:00:05Z',
        via: ReusedInputVia.run,
      },
    ],
  }
);

/** An entry that only sets what a summary reads. */
function entry(over: Partial<StepRunEntry>): StepRunEntry {
  return {
    runId: 'r',
    at: null,
    runStatus: Status.Success,
    testRun: false,
    state: 'ran',
    status: NodeStatus.Success,
    startedAt: null,
    finishedAt: null,
    durationMs: 1000,
    running: false,
    reusedFrom: null,
    reuseOutcome: null,
    error: null,
    ...over,
  };
}

describe('stepHistory', () => {
  it('tells a step that ran from one that was skipped, not started or absent', () => {
    const runs = [
      runAt('r1', 1, [node('a')]),
      runAt('r2', 2, [node('a', NodeStatus.Skipped, gone)]),
      runAt('r3', 3, [node('a', NodeStatus.NotStarted, gone)]),
      runAt('r4', 4, [node('b')]),
    ];
    const entries = stepHistory(runs, 'a', NOW);
    expect(entries.map((e) => [e.runId, e.state, e.status])).toEqual([
      ['r1', 'ran', NodeStatus.Success],
      ['r2', 'skipped', NodeStatus.Skipped],
      ['r3', 'notStarted', NodeStatus.NotStarted],
      ['r4', 'absent', null],
    ]);
    expect(entries[0]).toMatchObject({
      at: '2026-01-15T02:01:00Z',
      durationMs: 3000,
      running: false,
      startedAt: '2026-01-15T02:00:00Z',
      finishedAt: '2026-01-15T02:00:03Z',
    });
    expect(entries.slice(1).map((e) => e.durationMs)).toEqual([
      null,
      null,
      null,
    ]);
  });

  it('gives a reused step the time and the run of its source', () => {
    const [fetch] = stepHistory([reusing], 'fetch', NOW);
    expect(fetch).toMatchObject({
      state: 'reused',
      reusedFrom: 'r-source',
      reuseOutcome: 'success',
      durationMs: 5000,
    });
    const [extract] = stepHistory([reusing], 'extract', NOW);
    expect(extract).toMatchObject({
      state: 'reused',
      reuseOutcome: 'failed',
      durationMs: null,
    });
    expect(stepHistory([reusing], 'load', NOW)[0]?.state).toBe('ran');
  });

  it('counts a running step up to now', () => {
    const live = runAt(
      'r1',
      59,
      [
        node('a', NodeStatus.Running, {
          startedAt: '2026-01-15T02:59:00Z',
          finishedAt: '-',
        }),
      ],
      Status.Running
    );
    const [a] = stepHistory([live], 'a', NOW);
    expect(a).toMatchObject({ running: true, durationMs: 60_000 });
    expect(a?.finishedAt).toBeNull();
  });

  it('keeps the first line of the error and marks a test run', () => {
    const failed = runAt(
      'r1',
      1,
      [
        node('a', NodeStatus.Failed, {
          error: '\n  exit status 3  \nstack trace',
        }),
      ],
      Status.Failed,
      { ephemeral: true }
    );
    const [a] = stepHistory([failed], 'a', NOW);
    expect(a).toMatchObject({
      error: 'exit status 3',
      testRun: true,
      runStatus: Status.Failed,
    });
  });

  it('finds a handler step in the handler slots of the run', () => {
    const withHandler = runAt('r1', 1, [node('a')], Status.Success, {
      onExit: node('cleanup', NodeStatus.Failed),
    });
    expect(stepHistory([withHandler], 'cleanup', NOW)[0]).toMatchObject({
      state: 'ran',
      status: NodeStatus.Failed,
    });
  });
});

describe('stepGrid', () => {
  const dag = {
    name: 'demo',
    steps: [{ name: 'load', depends: ['extract'] }, { name: 'extract' }],
    handlerOn: { exit: { name: 'cleanup' } },
  } as unknown as ApiDAGDetails;

  it('orders the steps by execution, the handlers next, the removed steps last', () => {
    const runs = [
      runAt('r1', 1, [node('extract'), node('old-one')]),
      runAt('r2', 2, [node('extract'), node('load'), node('old-two')]),
    ];
    const grid = stepGrid(dag, runs, NOW);
    expect(grid.rows).toEqual([
      { step: 'extract', handler: false, removed: false },
      { step: 'load', handler: false, removed: false },
      { step: 'cleanup', handler: true, removed: false },
      // The newest run comes first.
      { step: 'old-two', handler: false, removed: true },
      { step: 'old-one', handler: false, removed: true },
    ]);
    expect(grid.cells).toHaveLength(5);
    expect(grid.cells[1]!.map((e) => e.state)).toEqual(['absent', 'ran']);
    expect(grid.cells[3]!.map((e) => e.runId)).toEqual(['r1', 'r2']);
  });

  it('marks no step removed when the definition has no steps', () => {
    const grid = stepGrid(
      { name: 'demo' } as ApiDAGDetails,
      [runAt('r1', 1, [node('a')])],
      NOW
    );
    expect(grid.rows).toEqual([{ step: 'a', handler: false, removed: false }]);
  });
});

describe('summarizeStepHistory', () => {
  // An aborted step was stopped before its own outcome: it is not counted.
  it('counts the finished runs where the step ran, and no test run', () => {
    const summary = summarizeStepHistory([
      entry({ durationMs: 1000 }),
      entry({ durationMs: 3000 }),
      entry({ status: NodeStatus.Failed, durationMs: 50 }),
      entry({ status: NodeStatus.Aborted }),
      entry({ testRun: true }),
      entry({ state: 'reused', status: NodeStatus.Skipped }),
      entry({ state: 'skipped', status: NodeStatus.Skipped }),
      entry({ state: 'absent', status: null }),
      entry({ status: NodeStatus.Running, running: true }),
      entry({ durationMs: 2000 }),
    ]);
    expect(summary).toEqual({
      counted: 4,
      succeeded: 3,
      // The median of the succeeded runs only.
      usualMs: 2000,
      failingSince: null,
    });
  });

  // A partial success is a success of the step: it counts in the rate and
  // in the usual time.
  it('counts a partial success as a success', () => {
    const summary = summarizeStepHistory([
      entry({ status: NodeStatus.Failed, durationMs: 50 }),
      entry({ durationMs: 1000 }),
      entry({ status: NodeStatus.PartialSuccess, durationMs: 4000 }),
    ]);
    expect(summary).toEqual({
      counted: 3,
      succeeded: 2,
      usualMs: 2500,
      failingSince: null,
    });
  });

  it('gives the start of the failures that end the counted runs', () => {
    const summary = summarizeStepHistory([
      entry({ at: 't1', status: NodeStatus.Failed }),
      entry({ at: 't2' }),
      entry({ at: 't3', status: NodeStatus.Failed }),
      entry({ at: 't4', status: NodeStatus.Rejected }),
      // A test run and a skip do not break the streak.
      entry({ at: 't5', testRun: true }),
      entry({ at: 't6', state: 'skipped', status: NodeStatus.Skipped }),
    ]);
    expect(summary.failingSince).toBe('t3');
    expect(summary.usualMs).toBe(1000);
  });

  it('has no usual time and no streak without counted runs', () => {
    expect(summarizeStepHistory([entry({ state: 'absent' })])).toEqual({
      counted: 0,
      succeeded: 0,
      usualMs: null,
      failingSince: null,
    });
  });
});

describe('hasRunningStep', () => {
  const runs = [
    runAt('r1', 1, [node('a', NodeStatus.Failed)], Status.Failed),
    runAt('r2', 2, [node('a'), node('b', NodeStatus.Retrying)], Status.Running),
  ];

  it('is true while a step of the runs, or the named one, runs', () => {
    expect(hasRunningStep(runs)).toBe(true);
    expect(hasRunningStep(runs, 'b')).toBe(true);
    expect(hasRunningStep(runs, 'a')).toBe(false);
    expect(hasRunningStep(runs.slice(0, 1))).toBe(false);
  });
});

describe('isFailedEntry', () => {
  it('is true for a step that ran and failed or was rejected', () => {
    expect(isFailedEntry(entry({ status: NodeStatus.Failed }))).toBe(true);
    expect(isFailedEntry(entry({ status: NodeStatus.Rejected }))).toBe(true);
    expect(isFailedEntry(entry({ status: NodeStatus.Aborted }))).toBe(false);
    expect(isFailedEntry(entry({}))).toBe(false);
    expect(
      isFailedEntry(
        entry({ state: 'reused', reuseOutcome: 'failed', status: null })
      )
    ).toBe(false);
  });
});

describe('withLiveRun', () => {
  const runs = [runAt('r1', 1), runAt('r2', 2)];

  it('puts the live details in place of the copy in the history', () => {
    const live = runAt('r2', 2, [node('a', NodeStatus.Failed)], Status.Failed);
    const merged = withLiveRun(runs, live);
    expect(merged.map((r) => r.status)).toEqual([
      Status.Success,
      Status.Failed,
    ]);
    expect(merged[1]).toBe(live);
  });

  it('gives the same list when the selected run is not in it', () => {
    expect(withLiveRun(runs, null)).toBe(runs);
    expect(withLiveRun(runs, runAt('r9', 9))).toBe(runs);
    expect(withLiveRun(runs, runs[0]!)).toBe(runs);
  });
});
