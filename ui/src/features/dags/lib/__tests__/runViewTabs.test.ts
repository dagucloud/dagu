// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  components,
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import {
  autoRunTab,
  fallbackRunTab,
  runViewFacts,
  visibleRunTabs,
  type RunViewFacts,
  type RunViewTab,
} from '../runViewTabs';

type DAGRunDetails = components['schemas']['DAGRunDetails'];
type Node = components['schemas']['Node'];

const baseFacts: RunViewFacts = {
  status: Status.Success,
  hasNodes: true,
  hasChatSteps: false,
  hasAgentSessions: false,
  agentWaiting: 0,
  hasAgentTasks: false,
  waitingApprovals: 0,
  hasHumanTaskWork: false,
  humanTasksWaiting: 0,
  hasArtifacts: false,
};

function facts(patch: Partial<RunViewFacts>): RunViewFacts {
  return { ...baseFacts, ...patch };
}

function node(patch: Partial<Node> & { step?: Partial<Node['step']> }): Node {
  return {
    status: NodeStatus.Success,
    statusLabel: NodeStatusLabel.succeeded,
    startedAt: '',
    finishedAt: '',
    retryCount: 0,
    doneCount: 1,
    ...patch,
    step: { name: 'step', ...patch.step },
  } as Node;
}

function run(patch: Partial<DAGRunDetails>): DAGRunDetails {
  return {
    name: 'example',
    dagRunId: 'run-1',
    status: Status.Success,
    statusLabel: StatusLabel.succeeded,
    startedAt: '',
    finishedAt: '',
    autoRetryCount: 0,
    nodes: [],
    ...patch,
  } as DAGRunDetails;
}

// parity:run-tabs-bar
describe('visibleRunTabs', () => {
  it('always lists status, outputs and spec', () => {
    expect(visibleRunTabs(facts({ status: Status.NotStarted }))).toEqual([
      'status',
      'outputs',
      'spec',
    ]);
  });

  it('hides the timeline only on a run that has not started', () => {
    expect(visibleRunTabs(facts({ status: Status.NotStarted }))).not.toContain(
      'timeline'
    );
    for (const status of [
      Status.Queued,
      Status.Running,
      Status.Waiting,
      Status.Success,
      Status.Failed,
      Status.Aborted,
    ]) {
      expect(visibleRunTabs(facts({ status }))).toContain('timeline');
    }
  });

  it.each<[string, Partial<RunViewFacts>, RunViewTab]>([
    ['chat steps', { hasChatSteps: true }, 'chat'],
    ['agent sessions', { hasAgentSessions: true }, 'agent'],
    ['agent tasks', { hasAgentTasks: true }, 'tasks'],
    ['waiting approvals', { waitingApprovals: 1 }, 'approval'],
    ['human task work', { hasHumanTaskWork: true }, 'human-tasks'],
    ['artifacts', { hasArtifacts: true }, 'artifacts'],
  ])('shows a tab for %s', (_, patch, tab) => {
    expect(visibleRunTabs(baseFacts)).not.toContain(tab);
    expect(visibleRunTabs(facts(patch))).toContain(tab);
  });

  it('shows approval only while an approval waits', () => {
    expect(
      visibleRunTabs(facts({ waitingApprovals: 0, status: Status.Waiting }))
    ).not.toContain('approval');
  });

  it('keeps the order of the tab bar', () => {
    expect(
      visibleRunTabs(
        facts({
          hasChatSteps: true,
          hasAgentSessions: true,
          hasAgentTasks: true,
          waitingApprovals: 2,
          hasHumanTaskWork: true,
          hasArtifacts: true,
        })
      )
    ).toEqual([
      'status',
      'agent',
      'human-tasks',
      'approval',
      'timeline',
      'outputs',
      'artifacts',
      'tasks',
      'chat',
      'spec',
    ]);
  });
});

describe('runViewFacts', () => {
  it.each(['chat', 'agent', 'controller'])(
    'finds chat steps for the %s executor',
    (type) => {
      const f = runViewFacts(
        run({ nodes: [node({ step: { executorConfig: { type } } as never })] }),
        { artifactEnabled: false }
      );
      expect(f.hasChatSteps).toBe(true);
      expect(visibleRunTabs(f)).toContain('chat');
    }
  );

  it('finds no chat steps for a command executor', () => {
    const f = runViewFacts(
      run({
        nodes: [
          node({ step: { executorConfig: { type: 'command' } } as never }),
        ],
      }),
      { artifactEnabled: false }
    );
    expect(f.hasChatSteps).toBe(false);
  });

  it('counts agent sessions that wait or are unavailable', () => {
    const f = runViewFacts(
      run({
        nodes: [
          node({ agentSession: { state: 'waiting' } } as never),
          node({ agentSession: { state: 'unavailable' } } as never),
          node({ agentSession: { state: 'running' } } as never),
          node({}),
        ],
      }),
      { artifactEnabled: false }
    );
    expect(f.hasAgentSessions).toBe(true);
    expect(f.agentWaiting).toBe(2);
    expect(f.hasNodes).toBe(true);
  });

  it('finds agent tasks, waiting approvals and human task work', () => {
    const f = runViewFacts(
      run({
        status: Status.Waiting,
        agentTasks: [{ id: 't1' }] as never,
        nodes: [
          node({ status: NodeStatus.Waiting, step: { approval: {} } as never }),
          node({
            status: NodeStatus.Waiting,
            step: { humanTask: {} } as never,
          }),
        ],
      }),
      { artifactEnabled: false }
    );
    expect(f.hasAgentTasks).toBe(true);
    expect(f.waitingApprovals).toBe(1);
    expect(f.hasHumanTaskWork).toBe(true);
    expect(f.humanTasksWaiting).toBe(1);
  });

  it('shows artifacts when enabled or when the run has them', () => {
    expect(runViewFacts(run({}), { artifactEnabled: false }).hasArtifacts).toBe(
      false
    );
    expect(runViewFacts(run({}), { artifactEnabled: true }).hasArtifacts).toBe(
      true
    );
    expect(
      runViewFacts(run({ artifactsAvailable: true }), {
        artifactEnabled: false,
      }).hasArtifacts
    ).toBe(true);
  });
});

describe('autoRunTab', () => {
  it('returns null when nothing waits', () => {
    expect(autoRunTab(baseFacts)).toBeNull();
  });

  it('prefers agent over human tasks over approval', () => {
    const all = facts({
      hasAgentSessions: true,
      agentWaiting: 1,
      hasHumanTaskWork: true,
      waitingApprovals: 1,
    });
    expect(autoRunTab(all)).toBe('agent');
    expect(autoRunTab({ ...all, agentWaiting: 0 })).toBe('human-tasks');
    expect(
      autoRunTab({ ...all, agentWaiting: 0, hasHumanTaskWork: false })
    ).toBe('approval');
  });

  it('does not pick agent for a session that does not wait', () => {
    expect(autoRunTab(facts({ hasAgentSessions: true }))).toBeNull();
  });
});

// parity:overview-tab-unavailable-fallback
describe('fallbackRunTab', () => {
  it.each<[RunViewTab, Partial<RunViewFacts>]>([
    ['timeline', { status: Status.NotStarted }],
    ['chat', { hasChatSteps: false }],
    ['agent', { hasAgentSessions: false }],
    ['tasks', { hasAgentTasks: false }],
    ['approval', { waitingApprovals: 0 }],
    ['human-tasks', { hasHumanTaskWork: false }],
    ['artifacts', { hasArtifacts: false }],
  ])('falls back to status when %s disappears', (tab, patch) => {
    expect(fallbackRunTab(tab, visibleRunTabs(facts(patch)))).toBe('status');
  });

  it.each<RunViewTab>(['status', 'outputs', 'spec', 'timeline'])(
    'keeps %s while it is visible',
    (tab) => {
      expect(fallbackRunTab(tab, visibleRunTabs(baseFacts))).toBe(tab);
    }
  );

  it('keeps a conditional tab while it is visible', () => {
    expect(
      fallbackRunTab('chat', visibleRunTabs(facts({ hasChatSteps: true })))
    ).toBe('chat');
  });
});
