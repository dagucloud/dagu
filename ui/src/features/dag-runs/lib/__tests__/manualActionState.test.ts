// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';

import { components, NodeStatus, Status } from '@/api/v1/schema';
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
    });
  });

  it('finds actionable approvals and human tasks', () => {
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
  });

  it('ignores a completed human task while approval is waiting', () => {
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
  });

  it('reports resume-pending human task work without a waiting node', () => {
    const dagRun = {
      status: Status.Waiting,
      humanTaskResumePending: true,
      nodes: [] as DAGRunNode[],
    } as DAGRunDetails;

    expect(getManualActionState(dagRun).hasHumanTaskWork).toBe(true);
  });

  // A partial resume executes the run while other tasks stay open; their
  // cards must stay mounted so drafts survive until the run waits again.
  it.each([
    { name: 'queued', status: Status.Queued },
    { name: 'running', status: Status.Running },
  ])('keeps open human tasks while the run is $name', ({ status }) => {
    const humanTask = node(NodeStatus.Waiting, {
      name: 'review',
      humanTask: { prompt: 'Choose a region' },
    } as DAGRunNode['step']);
    const approvalOnly = {
      status,
      nodes: [
        node(NodeStatus.Waiting, {
          name: 'approve',
          approval: { prompt: 'Approve release' },
        } as DAGRunNode['step']),
      ],
    } as DAGRunDetails;

    const state = getManualActionState({
      status,
      nodes: [humanTask],
    } as DAGRunDetails);

    expect(state.isWaiting).toBe(false);
    expect(state.hasHumanTaskWork).toBe(true);
    expect(getManualActionState(approvalOnly).hasHumanTaskWork).toBe(false);
  });

  it.each([
    { name: 'failed', status: Status.Failed },
    { name: 'aborted', status: Status.Aborted },
  ])('hides open human tasks once the run is $name', ({ status }) => {
    const dagRun = {
      status,
      nodes: [
        node(NodeStatus.Waiting, {
          name: 'review',
          humanTask: { prompt: 'Choose a region' },
        } as DAGRunNode['step']),
      ],
    } as DAGRunDetails;

    expect(getManualActionState(dagRun).hasHumanTaskWork).toBe(false);
  });

  it('ignores resume-pending outside a waiting run', () => {
    const dagRun = {
      status: Status.Running,
      humanTaskResumePending: true,
      nodes: [] as DAGRunNode[],
    } as DAGRunDetails;

    expect(getManualActionState(dagRun).hasHumanTaskWork).toBe(false);
  });
});
