// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it, vi } from 'vitest';
import { NodeStatus, Status, type components } from '@/api/v1/schema';
import {
  coverFromCache,
  defaultChoice,
  missingIn,
  paramsDiffer,
  pickReuseSource,
  planRun,
  staleUpstream,
  stepNeeds,
  type ReuseRun,
} from '../reuseSource';
import type { CacheEntry } from '../outputCache';
import type { RunSpecDiff } from '../runSpecDiff';

type Node = components['schemas']['Node'];

function node(name: string, status: NodeStatus = NodeStatus.Success): Node {
  return {
    step: { name },
    status,
    finishedAt: '2026-10-01T00:00:05Z',
    doneCount: 1,
  } as Node;
}

function runOf(
  id: string,
  status: Status,
  nodes: Node[] = [node('a'), node('b')],
  params?: string
): ReuseRun {
  return {
    dagRunId: id,
    status,
    nodes,
    ...(params !== undefined ? { params } : {}),
  };
}

function diffOf(p: Partial<RunSpecDiff> = {}): RunSpecDiff {
  return {
    headChanged: false,
    changed: new Set(),
    removedFromLive: new Set(),
    addedInLive: new Set(),
    ...p,
  };
}

const yes = () => Promise.resolve(true);

describe('pickReuseSource', () => {
  const reads = ['a', 'b'];

  it('skips an active run and picks the newest finished run that holds every read', async () => {
    const history = [
      runOf('active', Status.Running),
      runOf('partial', Status.Failed, [
        node('a'),
        node('b', NodeStatus.Failed),
      ]),
      runOf('good', Status.Success),
      runOf('older', Status.Success),
    ];
    const pick = await pickReuseSource({
      followedUpstream: null,
      selected: null,
      history,
      reads,
      holds: yes,
    });
    expect(pick.source?.dagRunId).toBe('good');
    expect(pick.waitingFor).toBeNull();
  });

  it('prefers the selected run when it qualifies', async () => {
    const selected = runOf('selected', Status.Success);
    const pick = await pickReuseSource({
      followedUpstream: null,
      selected,
      history: [runOf('newer', Status.Success), selected],
      reads,
      holds: yes,
    });
    expect(pick.source?.dagRunId).toBe('selected');
  });

  it('takes the newest run when the selected run does not qualify', async () => {
    const pick = await pickReuseSource({
      followedUpstream: null,
      selected: runOf('selected', Status.Failed, [node('a')]),
      history: [runOf('newer', Status.Success)],
      reads,
      holds: yes,
    });
    expect(pick.source?.dagRunId).toBe('newer');
  });

  it('gives no source when no run qualifies', async () => {
    const pick = await pickReuseSource({
      followedUpstream: null,
      selected: runOf('selected', Status.Failed, []),
      history: [runOf('x', Status.Success, [node('a')])],
      reads,
      holds: yes,
    });
    expect(pick).toEqual({ source: null, waitingFor: null });
  });

  it('skips a run that the outputs check refuses', async () => {
    const holds = vi.fn(async (r: ReuseRun) => r.dagRunId !== 'first');
    const pick = await pickReuseSource({
      followedUpstream: null,
      selected: null,
      history: [
        runOf('first', Status.Success),
        runOf('second', Status.Success),
      ],
      reads,
      holds,
    });
    expect(pick.source?.dagRunId).toBe('second');
    expect(holds).toHaveBeenCalledTimes(2);
  });

  it('waits for an active run when only an active run qualifies', async () => {
    const pick = await pickReuseSource({
      followedUpstream: null,
      selected: null,
      history: [runOf('active', Status.Running)],
      reads,
      holds: yes,
    });
    expect(pick).toEqual({ source: null, waitingFor: 'active' });
  });

  it('uses the finished followed run without a check', async () => {
    const holds = vi.fn(yes);
    const followed = runOf('followed', Status.Success, []);
    const pick = await pickReuseSource({
      followedUpstream: followed,
      selected: runOf('selected', Status.Success),
      history: [runOf('newer', Status.Success)],
      reads,
      holds,
    });
    expect(pick.source).toBe(followed);
    expect(holds).not.toHaveBeenCalled();
  });

  it('waits for an active followed run', async () => {
    const pick = await pickReuseSource({
      followedUpstream: runOf('followed', Status.Running, []),
      selected: runOf('selected', Status.Success),
      history: [runOf('newer', Status.Success)],
      reads,
      holds: yes,
    });
    expect(pick).toEqual({ source: null, waitingFor: 'followed' });
  });

  it('counts a reused node as holding its outputs', async () => {
    const reused = {
      ...node('a', NodeStatus.Skipped),
      doneCount: 1,
    } as Node;
    const pick = await pickReuseSource({
      followedUpstream: null,
      selected: null,
      history: [runOf('chain', Status.Success, [reused, node('b')])],
      reads,
      holds: yes,
    });
    expect(pick.source?.dagRunId).toBe('chain');
  });
});

describe('staleUpstream', () => {
  const deps = stepNeeds([
    { name: 'a' },
    { name: 'b', depends: ['a'] },
    { name: 'c', depends: ['b'] },
    { name: 'd', inferredDepends: ['c'] },
    { name: 'x' },
  ]);
  const previous = ['a', 'b', 'c', 'd', 'x'];

  it('is empty when nothing changed', () => {
    expect(staleUpstream({ diff: diffOf(), previous, deps })).toEqual([]);
  });

  it('marks a changed step and the steps downstream of it', () => {
    expect(
      staleUpstream({
        diff: diffOf({ changed: new Set(['b']) }),
        previous,
        deps,
      })
    ).toEqual(['b', 'c', 'd']);
  });

  it('marks all previous steps when the head changed', () => {
    expect(
      staleUpstream({ diff: diffOf({ headChanged: true }), previous, deps })
    ).toEqual(previous);
  });

  it('marks a step that the live text renamed away', () => {
    expect(
      staleUpstream({
        diff: diffOf({ removedFromLive: new Set(['x']) }),
        previous,
        deps,
      })
    ).toEqual(['x']);
  });
});

describe('missingIn', () => {
  it('lists the steps without a reusable node', () => {
    const run = runOf('r', Status.Failed, [
      node('a'),
      node('b', NodeStatus.Failed),
    ]);
    expect(missingIn(run, ['a', 'b', 'c'])).toEqual(['b', 'c']);
    expect(missingIn(null, ['a'])).toEqual([]);
  });

  // The server reuses a partially succeeded node, for example a call step.
  it('counts a partially succeeded node as reusable', () => {
    const run = runOf('r', Status.PartialSuccess, [
      node('a', NodeStatus.PartialSuccess),
    ]);
    expect(missingIn(run, ['a'])).toEqual([]);
  });
});

describe('planRun', () => {
  it('builds the steps of the three choices in dependency order', () => {
    const plan = planRun({
      previous: ['a', 'b', 'c'],
      stale: ['c'],
      missing: ['a'],
      step: 'd',
      order: ['a', 'b', 'c'],
    });
    expect(plan.only).toEqual(['d']);
    expect(plan.changed).toEqual(['a', 'c', 'd']);
    expect(plan.full).toEqual(['a', 'b', 'c', 'd']);
  });

  it('lists a step once and never lists the step before the end', () => {
    const plan = planRun({
      previous: ['a'],
      stale: ['a', 'd'],
      missing: ['a'],
      step: 'd',
      order: ['a'],
    });
    expect(plan.changed).toEqual(['a', 'd']);
  });
});

describe('defaultChoice', () => {
  it('chooses the button for the state of the source', () => {
    const source = { dagRunId: 'r' };
    expect(defaultChoice({ source: null, stale: [], missing: [] })).toBe('run');
    expect(defaultChoice({ source, stale: [], missing: [] })).toBe('only');
    expect(defaultChoice({ source, stale: ['a'], missing: [] })).toBe(
      'changed'
    );
    expect(defaultChoice({ source, stale: [], missing: ['a'] })).toBe(
      'changed'
    );
  });
});

function entry(p: Partial<CacheEntry> = {}): CacheEntry {
  return {
    values: { out: 'v' },
    runId: 'r1',
    capturedAt: 1000,
    stepHash: 'h-a',
    ...p,
  };
}

const ok = () => 'ok' as const;

describe('coverFromCache', () => {
  const previous = ['a', 'b'];
  const hashes = new Map([
    ['a', 'h-a'],
    ['b', 'h-b'],
  ]);

  it('covers a step whose entry hash equals the current hash', () => {
    const cover = coverFromCache({
      previous,
      stale: ['a', 'b'],
      missing: [],
      hasSource: true,
      entries: { a: entry() },
      hashes,
      check: ok,
    });
    expect(cover.cached).toEqual(['a']);
    expect(cover.stale).toEqual(['b']);
    expect(Object.keys(cover.runs)).toEqual(['a']);
    expect(cover.capturedAt).toBe(1000);
  });

  // Every cached value is sent with the run that it was read from.
  it('names the run and the time of each covered step', () => {
    const cover = coverFromCache({
      previous,
      stale: [],
      missing: [],
      hasSource: false,
      entries: {
        a: entry({ runId: 'run-a', capturedAt: 3000 }),
        b: entry({ stepHash: 'h-b', runId: 'run-b', capturedAt: 2000 }),
      },
      hashes,
      check: ok,
    });
    expect(cover.runs).toEqual({
      a: { runId: 'run-a', capturedAt: 3000 },
      b: { runId: 'run-b', capturedAt: 2000 },
    });
  });

  it('leaves a step to the source run when the run holds it and it did not change', () => {
    const cover = coverFromCache({
      previous,
      stale: [],
      missing: ['b'],
      hasSource: true,
      entries: { a: entry(), b: entry({ stepHash: 'h-b' }) },
      hashes,
      check: ok,
    });
    expect(cover.cached).toEqual(['b']);
    expect(Object.keys(cover.runs)).toEqual(['b']);
    expect(cover.missing).toEqual([]);
  });

  it('leaves a stale entry as it was: it covers nothing', () => {
    const cover = coverFromCache({
      previous,
      stale: ['a'],
      missing: ['b'],
      hasSource: true,
      entries: { a: entry({ stepHash: 'old' }) },
      hashes,
      check: ok,
    });
    expect(cover.cached).toEqual([]);
    expect(cover.stale).toEqual(['a']);
    expect(cover.missing).toEqual(['b']);
    expect(cover.runs).toEqual({});
  });

  // A cache entry names a run: it covers the step only while the run exists
  // and holds the step. A step with a run that is gone counts as missing.
  it('does not cover a step whose run is gone, and names it', () => {
    const cover = coverFromCache({
      previous,
      stale: [],
      missing: ['a', 'b'],
      hasSource: true,
      entries: { a: entry(), b: entry({ stepHash: 'h-b', runId: 'r2' }) },
      hashes,
      check: (_step, e) => (e.runId === 'r2' ? 'gone' : 'ok'),
    });
    expect(cover.cached).toEqual(['a']);
    expect(cover.gone).toEqual(['b']);
    expect(cover.missing).toEqual(['b']);
    expect(cover.checking).toBe(false);
  });

  it('waits for a run that is not checked yet', () => {
    const cover = coverFromCache({
      previous,
      stale: [],
      missing: [],
      hasSource: false,
      entries: { a: entry() },
      hashes,
      check: () => 'checking',
    });
    expect(cover.checking).toBe(true);
    expect(cover.cached).toEqual([]);
  });

  it('covers nothing while the definition is not known', () => {
    const cover = coverFromCache({
      previous,
      stale: [],
      missing: [],
      hasSource: false,
      entries: { a: entry(), b: entry() },
      hashes: null,
      check: ok,
    });
    expect(cover.cached).toEqual([]);
    expect(cover.runs).toEqual({});
  });

  it('lists the uncovered steps as missing when no run is the source', () => {
    const cover = coverFromCache({
      previous,
      stale: [],
      missing: [],
      hasSource: false,
      entries: { a: entry() },
      hashes,
      check: ok,
    });
    expect(cover.cached).toEqual(['a']);
    expect(cover.missing).toEqual(['b']);
  });

  it('lists nothing as missing when no run and no entry cover a step', () => {
    const cover = coverFromCache({
      previous,
      stale: [],
      missing: [],
      hasSource: false,
      entries: {},
      hashes,
      check: ok,
    });
    expect(cover.missing).toEqual([]);
  });

  it('keeps the oldest capture time of the covered steps', () => {
    const cover = coverFromCache({
      previous,
      stale: [],
      missing: [],
      hasSource: false,
      entries: {
        a: entry({ capturedAt: 3000 }),
        b: entry({ stepHash: 'h-b', capturedAt: 2000 }),
      },
      hashes,
      check: ok,
    });
    expect(cover.capturedAt).toBe(2000);
    expect(cover.times).toEqual({ a: 3000, b: 2000 });
  });
});

describe('defaultChoice with the cache', () => {
  it('is Step only when the cache covers every previous step and no run exists', () => {
    expect(
      defaultChoice({ source: null, stale: [], missing: [], covered: 2 })
    ).toBe('only');
  });

  it('is Changed steps when the cache covers some steps only', () => {
    expect(
      defaultChoice({ source: null, stale: [], missing: ['b'], covered: 1 })
    ).toBe('changed');
  });

  it('is Run when nothing covers a previous step', () => {
    expect(
      defaultChoice({ source: null, stale: [], missing: [], covered: 0 })
    ).toBe('run');
  });
});

describe('paramsDiffer', () => {
  it('is true for other parameters', () => {
    expect(paramsDiffer('A=2', 'A=1')).toBe(true);
    expect(paramsDiffer('A=1', undefined)).toBe(true);
  });

  it('ignores spacing and treats no parameters as equal', () => {
    expect(paramsDiffer(' A=1   B=2 ', 'A=1 B=2')).toBe(false);
    expect(paramsDiffer(undefined, '')).toBe(false);
  });

  // The server quotes each default value and none of a run.
  it('reads the quoted defaults of the workflow', () => {
    expect(paramsDiffer('A=1 MSG=hi there', 'A="1" MSG="hi there"')).toBe(
      false
    );
    expect(paramsDiffer('A=2', 'A="1"')).toBe(true);
  });
});
