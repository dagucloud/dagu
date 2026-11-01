// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { NodeStatus, Status } from '@/api/v1/schema';
import {
  buildSourceOptions,
  CACHE_OPTION_ID,
  runOptionId,
  type SourceRun,
} from '../runSourceOptions';

// A run with the given steps as successful nodes.
function run(
  id: string,
  steps: string[],
  extra: Partial<SourceRun> = {}
): SourceRun {
  return {
    dagRunId: id,
    status: Status.Success,
    startedAt: '2026-10-01T00:00:00Z',
    finishedAt: '2026-10-01T00:00:09Z',
    nodes: steps.map((name) => ({
      step: { name },
      status: NodeStatus.Success,
    })),
    ...extra,
  } as SourceRun;
}

describe('buildSourceOptions', () => {
  const reads = ['slow', 'middle'];

  it('puts the automatic choice first, then the other runs in the order given', () => {
    const a = run('a', reads);
    const b = run('b', reads);
    const c = run('c', reads);
    const options = buildSourceOptions({
      runs: [a, b, c],
      auto: b,
      reads,
      cache: null,
    });
    expect(options.map((o) => o.id)).toEqual(['run:b', 'run:a', 'run:c']);
    expect(options.map((o) => o.auto === true)).toEqual([true, false, false]);
  });

  it('adds the automatic run when the history does not list it', () => {
    const a = run('a', reads);
    const options = buildSourceOptions({
      runs: [],
      auto: a,
      reads,
      cache: null,
    });
    expect(options.map((o) => o.id)).toEqual(['run:a']);
  });

  it('blocks a run that lacks an output that the step reads, and names the steps', () => {
    const options = buildSourceOptions({
      runs: [run('a', ['slow']), run('b', reads)],
      auto: null,
      reads,
      cache: null,
    });
    expect(options[0]?.blocked).toEqual({
      reason: 'missing',
      steps: ['middle'],
    });
    expect(options[1]?.blocked).toBeUndefined();
  });

  it('blocks a run that still runs', () => {
    const active = run('a', reads, { status: Status.Running });
    const [option] = buildSourceOptions({
      runs: [active],
      auto: null,
      reads,
      cache: null,
    });
    expect(option?.blocked).toEqual({ reason: 'active' });
  });

  it('keeps a skipped step that carries the state of an earlier run as an output', () => {
    const carried = run('a', ['slow']);
    carried.nodes = [
      ...(carried.nodes ?? []),
      {
        step: { name: 'middle' },
        status: NodeStatus.Skipped,
        doneCount: 1,
        finishedAt: '2026-10-01T00:00:05Z',
      },
    ] as never;
    const [option] = buildSourceOptions({
      runs: [carried],
      auto: null,
      reads,
      cache: null,
    });
    expect(option?.blocked).toBeUndefined();
  });

  it('marks a test run and keeps it choosable', () => {
    const [option] = buildSourceOptions({
      runs: [run('t', reads, { ephemeral: true })],
      auto: null,
      reads,
      cache: null,
    });
    expect(option).toMatchObject({ ephemeral: true, runId: 't' });
    expect(option?.blocked).toBeUndefined();
  });

  it('offers the cached values last, with the runs that they were read from', () => {
    const options = buildSourceOptions({
      runs: [run('a', reads)],
      auto: null,
      reads,
      cache: { runIds: ['r1', 'r2'], capturedAt: 5000 },
    });
    const last = options[options.length - 1];
    expect(last).toMatchObject({
      id: CACHE_OPTION_ID,
      kind: 'cache',
      cache: { runIds: ['r1', 'r2'], capturedAt: 5000 },
    });
  });

  it('offers no cached values without a run to name', () => {
    expect(
      buildSourceOptions({
        runs: [],
        auto: null,
        reads,
        cache: { runIds: [], capturedAt: 1 },
      })
    ).toEqual([]);
    expect(
      buildSourceOptions({ runs: [], auto: null, reads, cache: null })
    ).toEqual([]);
  });

  it('writes the id of a run option', () => {
    expect(runOptionId('x')).toBe('run:x');
  });
});
