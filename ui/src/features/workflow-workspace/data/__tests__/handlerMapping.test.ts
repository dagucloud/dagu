// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { NodeStatus, Status } from '@/api/v1/schema';
import { getEventHandlers } from '@/features/dags/lib/getEventHandlers';
import { runHandlers } from '../handlerMapping';
import {
  firstFailedStep,
  isNeverRun,
  nodesByStep,
  waitingCounts,
} from '../runFacts';
import { node, run, zeroRun } from './fixtures';

// parity:canvas-node-handler parity:common-dags-lib
describe('runHandlers', () => {
  it('maps every handler in getEventHandlers order', () => {
    const details = run('r', [node('main')], Status.Success, {
      onExit: node('exit-step'),
      onAbort: node('abort-step'),
      onFailure: node('failure-step'),
      onSuccess: node('success-step'),
      onWait: node('wait-step'),
      onInit: node('init-step'),
    });
    const handlers = runHandlers(details);
    expect(handlers.map((h) => h.event)).toEqual([
      'onInit',
      'onWait',
      'onSuccess',
      'onFailure',
      'onAbort',
      'onExit',
    ]);
    expect(handlers.map((h) => h.node)).toEqual(getEventHandlers(details));
  });

  it('skips the handlers that the run does not have', () => {
    const details = run('r', [node('main')], Status.Success, {
      onExit: node('exit-step'),
      onInit: node('init-step'),
    });
    expect(runHandlers(details)).toEqual([
      { event: 'onInit', node: details.onInit },
      { event: 'onExit', node: details.onExit },
    ]);
    expect(runHandlers(details).map((h) => h.node)).toEqual(
      getEventHandlers(details)
    );
    expect(runHandlers(null)).toEqual([]);
  });
});

describe('runFacts', () => {
  it('treats the zero-value run as never run', () => {
    expect(isNeverRun(zeroRun())).toBe(true);
    expect(isNeverRun(undefined)).toBe(true);
    expect(isNeverRun(run('r', [], Status.NotStarted))).toBe(true);
    expect(isNeverRun(run('r', [node('a')], Status.NotStarted))).toBe(false);
    expect(isNeverRun(run('r', [], Status.Queued))).toBe(false);
  });

  it('keys nodes and handler nodes by step name, nodes first', () => {
    const details = run('r', [node('a', NodeStatus.Failed)], Status.Failed, {
      onExit: node('cleanup'),
      onFailure: node('a', NodeStatus.Success),
    });
    const map = nodesByStep(details);
    expect([...map.keys()]).toEqual(['a', 'cleanup']);
    expect(map.get('a')?.status).toBe(NodeStatus.Failed);
  });

  it('counts waiting work and the first waiting step in node order', () => {
    const details = run(
      'r',
      [
        node('approve', NodeStatus.Waiting, { step: { approval: {} } }),
        node('form', NodeStatus.Waiting, { step: { humanTask: {} } }),
        node('agent-a', NodeStatus.Running, {
          agentSession: { provider: 'x', state: 'unavailable' },
        }),
        node('agent-b', NodeStatus.Running, {
          agentSession: { provider: 'x', state: 'running' },
        }),
        node('agent-c', NodeStatus.Running, {
          agentSession: { provider: 'x', state: 'waiting' },
        }),
      ],
      Status.Waiting
    );
    expect(waitingCounts(details)).toEqual({
      approvals: 1,
      humanTasks: 1,
      agent: 2,
      firstWaitingStep: 'approve',
    });
    expect(waitingCounts(null).firstWaitingStep).toBeNull();
  });

  it('finds the first failed or rejected step', () => {
    expect(
      firstFailedStep(
        run('r', [
          node('ok'),
          node('no', NodeStatus.Rejected),
          node('bad', NodeStatus.Failed),
        ])
      )
    ).toBe('no');
    expect(firstFailedStep(run('r', [node('ok')]))).toBeNull();
    expect(firstFailedStep(null)).toBeNull();
  });
});
