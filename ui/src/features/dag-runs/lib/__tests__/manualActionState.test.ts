// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, NodeStatus, Status } from '@/api/v1/schema';
import { describe, expect, it } from 'vitest';
import { getManualActionState } from '../manualActionState';

type DAGRunDetails = components['schemas']['DAGRunDetails'];
type DAGRunSummary = components['schemas']['DAGRunSummary'];
type DAGRunNode = DAGRunDetails['nodes'][number];

function node(status: NodeStatus, step: DAGRunNode['step']): DAGRunNode {
  return { status, step } as DAGRunNode;
}

describe('getManualActionState', () => {
  it.each([
    { name: 'undefined input', dagRun: undefined, isWaiting: false },
    {
      name: 'summary without node details',
      dagRun: { status: Status.Waiting } as DAGRunSummary,
      isWaiting: true,
    },
  ])('returns safe defaults for $name', ({ dagRun, isWaiting }) => {
    expect(getManualActionState(dagRun)).toEqual({
      isWaiting,
      waitingApprovalNodes: [],
      waitingHumanTaskNodes: [],
      hasHumanTaskWork: false,
      humanTaskBlocksRetry: false,
    });
  });

  it('finds actionable approvals and blocks retry at a human-task checkpoint', () => {
    const approval = node(NodeStatus.Waiting, {
      name: 'approve',
      approval: { prompt: 'Approve release' },
    } as DAGRunNode['step']);
    const humanTask = node(NodeStatus.Waiting, {
      name: 'review',
      humanTask: { prompt: 'Choose a region' },
    } as DAGRunNode['step']);
    const dagRun = {
      status: Status.Waiting,
      nodes: [approval, humanTask],
    } as DAGRunDetails;

    const state = getManualActionState(dagRun);

    expect(state.isWaiting).toBe(true);
    expect(state.waitingApprovalNodes).toEqual([approval]);
    expect(state.waitingHumanTaskNodes).toEqual([humanTask]);
    expect(state.hasHumanTaskWork).toBe(true);
    expect(state.humanTaskBlocksRetry).toBe(true);
  });

  it('allows retry after a run has left the human-task checkpoint', () => {
    const dagRun = {
      status: Status.Failed,
      humanTaskResumePending: true,
      nodes: [
        node(NodeStatus.Failed, {
          name: 'review',
          humanTask: { prompt: 'Choose a region' },
        } as DAGRunNode['step']),
      ],
    } as DAGRunDetails;

    const state = getManualActionState(dagRun);

    expect(state.isWaiting).toBe(false);
    expect(state.waitingApprovalNodes).toEqual([]);
    expect(state.waitingHumanTaskNodes).toEqual([]);
    expect(state.hasHumanTaskWork).toBe(true);
    expect(state.humanTaskBlocksRetry).toBe(false);
  });

  it('allows retry while waiting on approval after a human task completed', () => {
    const approval = node(NodeStatus.Waiting, {
      name: 'approve',
      approval: { prompt: 'Approve release' },
    } as DAGRunNode['step']);
    const dagRun = {
      status: Status.Waiting,
      nodes: [
        node(NodeStatus.Success, {
          name: 'review',
          humanTask: { prompt: 'Choose a region' },
        } as DAGRunNode['step']),
        approval,
      ],
    } as DAGRunDetails;

    const state = getManualActionState(dagRun);

    expect(state.waitingApprovalNodes).toEqual([approval]);
    expect(state.waitingHumanTaskNodes).toEqual([]);
    expect(state.hasHumanTaskWork).toBe(false);
    expect(state.humanTaskBlocksRetry).toBe(false);
  });
});
