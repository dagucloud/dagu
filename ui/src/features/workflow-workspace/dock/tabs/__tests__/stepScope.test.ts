// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { NodeStatus, Status } from '@/api/v1/schema';
import type { ApiDAGRunDetails } from '../../../contracts';
import { node, run } from '../../../data/__tests__/fixtures';
import {
  scopeDetailsToStep,
  STEP_TAB_LABELS,
  stepDockTabs,
  stepViewTabs,
} from '../stepScope';

const full = run(
  'r1',
  [
    node('plain'),
    node('chat step', NodeStatus.Success, {
      step: { executorConfig: { type: 'chat' } },
    }),
    node('agent step', NodeStatus.Running, {
      step: { executorConfig: { type: 'agent' } },
      agentSession: { state: 'running', provider: 'x' },
    }),
    node('gate', NodeStatus.Waiting, { step: { approval: {} } }),
    node('review', NodeStatus.Waiting, { step: { humanTask: {} } }),
    node('later', NodeStatus.NotStarted, {
      startedAt: '-',
      finishedAt: '-',
    }),
  ],
  Status.Waiting,
  {
    agentEvents: [{ turn: 1 }],
    agentTasks: [{ name: 'Plan', status: 'open' }],
    artifactsAvailable: true,
    approvalResumePending: true,
    humanTaskResumePending: true,
    onExit: node('cleanup'),
    onFailure: node('alert'),
  } as unknown as Partial<ApiDAGRunDetails>
);

describe('scopeDetailsToStep', () => {
  it('keeps the node of the step and none of the others', () => {
    const scoped = scopeDetailsToStep(full, 'plain');
    expect(scoped.nodes?.map((n) => n.step.name)).toEqual(['plain']);
    expect(scoped.dagRunId).toBe('r1');
    expect(scoped.status).toBe(Status.Waiting);
  });

  it('clears the data that no step owns', () => {
    const scoped = scopeDetailsToStep(full, 'plain');
    // Absent: a run with no list is not an agent run.
    expect(scoped).not.toHaveProperty('agentEvents');
    expect(scoped).not.toHaveProperty('agentTasks');
    expect(scoped.artifactsAvailable).toBe(false);
    expect(scoped.onExit).toBeUndefined();
    expect(scoped.onFailure).toBeUndefined();
  });

  it('keeps the resume flags only for a step with an approval or a human task', () => {
    expect(scopeDetailsToStep(full, 'plain').approvalResumePending).toBe(false);
    expect(scopeDetailsToStep(full, 'plain').humanTaskResumePending).toBe(
      false
    );
    expect(scopeDetailsToStep(full, 'gate').approvalResumePending).toBe(true);
    expect(scopeDetailsToStep(full, 'review').humanTaskResumePending).toBe(
      true
    );
  });

  it('keeps the handler slot of a handler step and no node', () => {
    const scoped = scopeDetailsToStep(full, 'cleanup');
    expect(scoped.nodes).toEqual([]);
    expect(scoped.onExit?.step.name).toBe('cleanup');
    expect(scoped.onFailure).toBeUndefined();
  });

  it('gives no node for a step that the run does not have', () => {
    expect(scopeDetailsToStep(full, 'ghost').nodes).toEqual([]);
  });

  it('does not change the run that it reads', () => {
    scopeDetailsToStep(full, 'plain');
    expect(full.nodes).toHaveLength(6);
    expect(full.onExit).toBeDefined();
  });
});

describe('stepDockTabs', () => {
  it('gives a plain step Log, History and Outputs, without the Timeline', () => {
    expect(stepDockTabs(full, 'plain')).toEqual(['log', 'history', 'outputs']);
  });

  it('adds Chat for a chat step and Agent with Chat for an agent step', () => {
    expect(stepDockTabs(full, 'chat step')).toEqual([
      'log',
      'history',
      'outputs',
      'chat',
    ]);
    expect(stepDockTabs(full, 'agent step')).toEqual([
      'log',
      'agent',
      'history',
      'outputs',
      'chat',
    ]);
  });

  it('adds Actions only for the step that waits', () => {
    expect(stepDockTabs(full, 'gate')).toContain('actions');
    expect(stepDockTabs(full, 'review')).toContain('actions');
    expect(stepDockTabs(full, 'plain')).not.toContain('actions');
  });

  it('never lists Artifacts or Tasks', () => {
    for (const step of ['plain', 'chat step', 'agent step', 'gate']) {
      const tabs = stepDockTabs(full, step);
      expect(tabs).not.toContain('artifacts');
      expect(tabs).not.toContain('tasks');
    }
  });

  it('lists no Timeline for any step', () => {
    for (const step of ['plain', 'chat step', 'agent step', 'gate', 'later']) {
      expect(stepDockTabs(full, step)).not.toContain('timeline');
    }
  });

  it('gives Log, History and Outputs for a step that the run does not have', () => {
    expect(stepDockTabs(full, 'ghost')).toEqual(['log', 'history', 'outputs']);
  });
});

describe('stepViewTabs', () => {
  it('removes the Timeline and keeps the order of the other tabs', () => {
    expect(
      stepViewTabs(['log', 'agent', 'timeline', 'outputs', 'artifacts'])
    ).toEqual(['log', 'agent', 'outputs', 'artifacts']);
  });

  it('names the outputs tab Output in a step view', () => {
    expect(STEP_TAB_LABELS.outputs).toBe('Output');
  });
});
