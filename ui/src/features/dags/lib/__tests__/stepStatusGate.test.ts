// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  components,
  NodeStatus,
  NodeStatusLabel,
  Status,
} from '@/api/v1/schema';
import { canChangeStepStatus, canRetryStep } from '../stepStatusGate';

type Node = components['schemas']['Node'];

function node(status: NodeStatus, humanTask = false): Node {
  return {
    step: { name: 'build', ...(humanTask ? { humanTask: {} } : {}) },
    status,
    statusLabel: NodeStatusLabel.succeeded,
    startedAt: '',
    finishedAt: '',
    retryCount: 0,
    doneCount: 1,
  } as Node;
}

const plain = node(NodeStatus.Success);
const humanTask = node(NodeStatus.Success, true);

// parity:crit-history-status-update-gate
describe('canChangeStepStatus', () => {
  it.each<[string, Status | null, Node, boolean, boolean]>([
    ['finished run', Status.Success, plain, true, true],
    ['failed run', Status.Failed, plain, true, true],
    ['aborted run', Status.Aborted, plain, true, true],
    ['partially succeeded run', Status.PartialSuccess, plain, true, true],
    ['rejected run', Status.Rejected, plain, true, true],
    ['run not started', Status.NotStarted, plain, true, false],
    ['running run', Status.Running, plain, true, false],
    ['queued run', Status.Queued, plain, true, false],
    ['waiting run', Status.Waiting, plain, true, false],
    ['human task step', Status.Success, humanTask, true, false],
    ['no runDags permission', Status.Success, plain, false, false],
    ['no run', null, plain, true, false],
  ])('%s', (_, status, n, runDags, ok) => {
    const gate = canChangeStepStatus(status === null ? null : { status }, n, {
      runDags,
    });
    expect(gate.ok).toBe(ok);
    if (ok) {
      expect(gate.reason).toBeUndefined();
    } else {
      expect(gate.reason).toEqual(expect.any(String));
    }
  });

  // parity:chg-history-status-gate
  // The History tab (DAGExecutionHistory.tsx) blocked only Running and
  // NotStarted runs, and it did not check human task steps. The shared gate
  // drops that looser rule: these cases were allowed there and are blocked now.
  it.each<[string, Status, Node]>([
    ['queued run', Status.Queued, plain],
    ['waiting run', Status.Waiting, plain],
    ['human task step', Status.Success, humanTask],
  ])('blocks the %s that the History rule allowed', (_, status, n) => {
    const historyRule = (s: Status) =>
      !(s === Status.Running || s === Status.NotStarted);
    expect(historyRule(status)).toBe(true);
    expect(canChangeStepStatus({ status }, n, { runDags: true }).ok).toBe(
      false
    );
  });

  it.each([Status.Running, Status.NotStarted])(
    'blocks %s in both rules',
    (status) => {
      expect(canChangeStepStatus({ status }, plain, { runDags: true }).ok).toBe(
        false
      );
    }
  );
});

// parity:run-step-retry
describe('canRetryStep', () => {
  it.each<[string, Status | null, Node, boolean, boolean, boolean, boolean]>([
    // name, run status, node, runDags, rootRunning, ok, disabled
    ['finished run', Status.Success, plain, true, false, true, false],
    [
      'failed step',
      Status.Failed,
      node(NodeStatus.Failed),
      true,
      false,
      true,
      false,
    ],
    ['aborted run', Status.Aborted, plain, true, false, true, false],
    ['run not started', Status.NotStarted, plain, true, false, true, false],
    ['running run', Status.Running, plain, true, false, true, true],
    ['root run running', Status.Success, plain, true, true, true, true],
    ['waiting run', Status.Waiting, plain, true, false, false, true],
    ['queued run', Status.Queued, plain, true, false, false, true],
    ['human task step', Status.Success, humanTask, true, false, false, true],
    [
      'waiting step',
      Status.Failed,
      node(NodeStatus.Waiting),
      true,
      false,
      false,
      true,
    ],
    [
      'rejected step',
      Status.Rejected,
      node(NodeStatus.Rejected),
      true,
      false,
      false,
      true,
    ],
    ['no runDags permission', Status.Success, plain, false, false, false, true],
    ['no run', null, plain, true, false, false, true],
  ])('%s', (_, status, n, runDags, rootRunning, ok, disabled) => {
    const gate = canRetryStep(status === null ? null : { status }, n, {
      runDags,
      rootRunning,
    });
    expect(gate.ok).toBe(ok);
    expect(gate.disabled).toBe(disabled);
    if (disabled) {
      expect(gate.reason).toEqual(expect.any(String));
    } else {
      expect(gate.reason).toBeUndefined();
    }
  });

  it('gives the root run reason the step table shows', () => {
    expect(
      canRetryStep({ status: Status.Success }, plain, {
        runDags: true,
        rootRunning: true,
      }).reason
    ).toBe('Retry unavailable while the root DAG run is running.');
  });
});
