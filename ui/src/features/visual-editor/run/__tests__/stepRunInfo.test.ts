// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { NodeStatus, Status, type components } from '@/api/v1/schema';
import {
  findRunNode,
  formatOutputSize,
  isReusedNode,
  isRunActive,
  stepParticipation,
} from '../stepRunInfo';

type Node = components['schemas']['Node'];
type DAGRunDetails = components['schemas']['DAGRunDetails'];

const node = (name: string) => ({ step: { name } }) as Node;

describe('findRunNode', () => {
  const details = {
    nodes: [node('a'), node('b')],
    onFailure: node('notify'),
  } as DAGRunDetails;

  it('finds a step and a handler by name', () => {
    expect(findRunNode(details, 'b')?.step.name).toBe('b');
    expect(findRunNode(details, 'notify')?.step.name).toBe('notify');
  });

  it('returns undefined for an unknown step or no run', () => {
    expect(findRunNode(details, 'zzz')).toBeUndefined();
    expect(findRunNode(undefined, 'a')).toBeUndefined();
  });
});

describe('formatOutputSize', () => {
  it('uses bytes, KiB and MiB', () => {
    expect(formatOutputSize(0)).toBe('0 B');
    expect(formatOutputSize(1023)).toBe('1023 B');
    expect(formatOutputSize(1536)).toBe('1.5 KiB');
    expect(formatOutputSize(131072)).toBe('128.0 KiB');
    expect(formatOutputSize(3 * 1024 * 1024)).toBe('3.0 MiB');
  });
});

describe('stepParticipation', () => {
  const base = { startedAt: '-', finishedAt: '-', doneCount: 0 };
  const make = (extra: Partial<Node>) =>
    ({ step: { name: 'a' }, ...base, ...extra }) as Node;

  it('is absent without a node', () => {
    expect(stepParticipation(undefined, false)).toBe('absent');
  });

  it('is notRun for NotStarted and plain Skipped nodes', () => {
    expect(
      stepParticipation(make({ status: NodeStatus.NotStarted }), false)
    ).toBe('notRun');
    expect(stepParticipation(make({ status: NodeStatus.Skipped }), false)).toBe(
      'notRun'
    );
    expect(
      stepParticipation(
        make({
          status: NodeStatus.Skipped,
          finishedAt: '0001-01-01T00:00:00Z',
        }),
        false
      )
    ).toBe('notRun');
  });

  // The runner gives every skipped step a finish time, so a step whose
  // precondition failed has one in a run that carries no outputs.
  it('is notRun for a skipped step that the run does not carry', () => {
    expect(
      stepParticipation(
        make({
          status: NodeStatus.Skipped,
          finishedAt: '2026-01-15T02:00:03Z',
        }),
        false
      )
    ).toBe('notRun');
  });

  it('is reused for a Skipped node that the run carries', () => {
    expect(stepParticipation(make({ status: NodeStatus.Skipped }), true)).toBe(
      'reused'
    );
    expect(
      isReusedNode(
        make({ status: NodeStatus.Skipped, finishedAt: '2026-01-15T02:00:03Z' })
      )
    ).toBe(true);
  });

  it('is ran for other statuses, even with a finish time', () => {
    expect(
      stepParticipation(
        make({
          status: NodeStatus.Success,
          finishedAt: '2026-01-15T02:00:03Z',
        }),
        true
      )
    ).toBe('ran');
    expect(stepParticipation(make({ status: NodeStatus.Running }), false)).toBe(
      'ran'
    );
  });
});

describe('isRunActive', () => {
  const run = (status: Status) => ({ status }) as DAGRunDetails;

  it('is true for a run that can still change', () => {
    expect(isRunActive(run(Status.Queued))).toBe(true);
    expect(isRunActive(run(Status.Running))).toBe(true);
    expect(isRunActive(run(Status.Waiting))).toBe(true);
  });

  it('is false for a finished run or no run', () => {
    expect(isRunActive(run(Status.Success))).toBe(false);
    expect(isRunActive(run(Status.Failed))).toBe(false);
    expect(isRunActive(undefined)).toBe(false);
  });
});
