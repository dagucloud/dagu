// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { NodeStatus, Status, type components } from '@/api/v1/schema';
import { failureCause, queueWaitMs } from '../recentRunFacts';

type Run = components['schemas']['DAGRunDetails'];

function run(patch: Record<string, unknown>): Run {
  return {
    dagRunId: 'r1',
    status: Status.Failed,
    startedAt: '2026-10-29T12:00:10Z',
    nodes: [],
    ...patch,
  } as unknown as Run;
}

function node(name: string, status: NodeStatus, error?: string) {
  return { step: { name }, status, error };
}

describe('failureCause', () => {
  it('names the first failed step with the first line of its error', () => {
    const r = run({
      nodes: [
        node('fetch', NodeStatus.Success),
        node('load', NodeStatus.Failed, 'exit status 2\nsecond line'),
        node('later', NodeStatus.Failed, 'other'),
      ],
    });
    expect(failureCause(r)).toBe('load: exit status 2');
  });

  it('falls back to the error of the run, then to the step name', () => {
    expect(failureCause(run({ error: 'could not start' }))).toBe(
      'could not start'
    );
    expect(
      failureCause(run({ nodes: [node('load', NodeStatus.Failed)] }))
    ).toBe('load');
  });

  it('is null for a run that did not fail', () => {
    expect(
      failureCause(
        run({
          status: Status.Success,
          error: 'x',
          nodes: [node('a', NodeStatus.Failed, 'x')],
        })
      )
    ).toBeNull();
    expect(failureCause(run({}))).toBeNull();
  });
});

describe('queueWaitMs', () => {
  it('is the time between the queue and the start', () => {
    expect(queueWaitMs(run({ queuedAt: '2026-10-29T12:00:00Z' }))).toBe(10_000);
  });

  it('is null without a queue time or for a wait under one second', () => {
    expect(queueWaitMs(run({}))).toBeNull();
    expect(
      queueWaitMs(run({ queuedAt: '2026-10-29T12:00:09.500Z' }))
    ).toBeNull();
  });
});
