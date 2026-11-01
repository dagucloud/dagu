// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { Status } from '@/api/v1/schema';
import type { RunViewFacts, RunViewTab } from '@/features/dags/lib/runViewTabs';
import type { DockTab } from '../../state/workspaceUrl';
import type { ApiDAGRunDetails } from '../../data/types';
import {
  dockFacts,
  dockTabBadges,
  defaultDockTab,
  dockTabsFor,
  fallbackDockTab,
  toDockTab,
} from '../dockTabs';

const BASE: RunViewFacts = {
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

describe('toDockTab', () => {
  it.each<[RunViewTab, DockTab]>([
    ['status', 'log'],
    ['agent', 'agent'],
    ['human-tasks', 'actions'],
    ['approval', 'actions'],
    ['timeline', 'timeline'],
    ['outputs', 'outputs'],
    ['artifacts', 'artifacts'],
    ['tasks', 'tasks'],
    ['chat', 'chat'],
  ])('maps %s to %s', (runTab, dockTab) => {
    expect(toDockTab(runTab)).toBe(dockTab);
  });

  // The run spec is no tab of the Run panel: the header opens it.
  it('maps spec to no tab', () => {
    expect(toDockTab('spec')).toBeNull();
  });
});

// parity:run-tabs-bar
// parity:overview-inner-tab-bar
describe('dockTabsFor', () => {
  it.each<[string, RunViewFacts | null, DockTab[]]>([
    ['never run', null, ['log']],
    ['finished run', BASE, ['log', 'timeline', 'outputs']],
    ['not started', { ...BASE, status: Status.NotStarted }, ['log', 'outputs']],
    [
      'human tasks and approvals share one Actions tab',
      {
        ...BASE,
        status: Status.Waiting,
        hasHumanTaskWork: true,
        humanTasksWaiting: 1,
        waitingApprovals: 2,
      },
      ['log', 'actions', 'timeline', 'outputs'],
    ],
    [
      'every view',
      {
        ...BASE,
        hasChatSteps: true,
        hasAgentSessions: true,
        hasAgentTasks: true,
        hasArtifacts: true,
        waitingApprovals: 1,
      },
      [
        'log',
        'agent',
        'actions',
        'timeline',
        'outputs',
        'artifacts',
        'tasks',
        'chat',
      ],
    ],
  ])('%s', (_name, facts, tabs) => {
    expect(dockTabsFor(facts)).toEqual(tabs);
  });

  // parity:run-approval-resume-alert
  // The Actions tab holds Retry resume, also without a waiting action.
  it('shows Actions for a failed approval resume', () => {
    const waiting = { ...BASE, status: Status.Waiting };
    expect(dockTabsFor({ ...waiting, approvalResumePending: true })).toEqual([
      'log',
      'actions',
      'timeline',
      'outputs',
    ]);
    expect(
      dockTabsFor({
        ...waiting,
        hasAgentSessions: true,
        approvalResumePending: true,
      }).slice(0, 3)
    ).toEqual(['log', 'agent', 'actions']);
    expect(
      dockTabsFor({
        ...waiting,
        waitingApprovals: 1,
        approvalResumePending: true,
      }).filter((tab) => tab === 'actions')
    ).toHaveLength(1);
  });
});

describe('dockTabsFor agent run', () => {
  // parity:chg-agent-timeline-move
  it('shows Agent for an agent DAG run without agent sessions', () => {
    expect(dockTabsFor({ ...BASE, isAgentRun: true })).toEqual([
      'log',
      'agent',
      'timeline',
      'outputs',
    ]);
    expect(
      dockTabsFor({
        ...BASE,
        status: Status.Waiting,
        isAgentRun: true,
        approvalResumePending: true,
      }).slice(0, 3)
    ).toEqual(['log', 'agent', 'actions']);
  });
});

describe('dockFacts', () => {
  const run = (
    status: Status,
    approvalResumePending: boolean,
    over: Partial<ApiDAGRunDetails> = {}
  ) =>
    ({
      dagRunId: 'r1',
      name: 'demo',
      status,
      nodes: [],
      approvalResumePending,
      ...over,
    }) as unknown as ApiDAGRunDetails;

  it('marks a run with agent events or agent tasks as an agent run', () => {
    const opts = { artifactEnabled: false };
    expect(dockFacts(run(Status.Success, false), opts).isAgentRun).toBe(false);
    expect(
      dockFacts(
        run(Status.Success, false, {
          agentEvents: [{}] as ApiDAGRunDetails['agentEvents'],
        }),
        opts
      ).isAgentRun
    ).toBe(true);
    expect(
      dockFacts(
        run(Status.Success, false, {
          agentTasks: [{}] as ApiDAGRunDetails['agentTasks'],
        }),
        opts
      ).isAgentRun
    ).toBe(true);
  });

  it('sets approvalResumePending only for a waiting run', () => {
    const opts = { artifactEnabled: false };
    expect(
      dockFacts(run(Status.Waiting, true), opts).approvalResumePending
    ).toBe(true);
    expect(
      dockFacts(run(Status.Running, true), opts).approvalResumePending
    ).toBe(false);
    expect(
      dockFacts(run(Status.Waiting, false), opts).approvalResumePending
    ).toBe(false);
  });
});

describe('dockTabBadges', () => {
  it('counts waiting agents and waiting manual actions', () => {
    expect(
      dockTabBadges({
        ...BASE,
        agentWaiting: 2,
        waitingApprovals: 1,
        humanTasksWaiting: 3,
      })
    ).toEqual({ agent: 2, actions: 4 });
  });

  it('leaves out zero counts and never-run workflows', () => {
    expect(dockTabBadges(BASE)).toEqual({});
    expect(dockTabBadges(null)).toEqual({});
  });
});

// parity:overview-tab-unavailable-fallback
describe('fallbackDockTab', () => {
  it('keeps a visible tab and falls back to log otherwise', () => {
    expect(fallbackDockTab('timeline', ['log', 'timeline'])).toBe('timeline');
    expect(fallbackDockTab('agent', ['log', 'timeline'])).toBe('log');
    expect(fallbackDockTab('timeline', ['log'])).toBe('log');
  });
});

describe('defaultDockTab', () => {
  it('opens the log of a step or of a failed run, else the timeline', () => {
    expect(defaultDockTab({ stepShown: true, failed: false })).toBe('log');
    expect(defaultDockTab({ stepShown: false, failed: true })).toBe('log');
    expect(defaultDockTab({ stepShown: false, failed: false })).toBe(
      'timeline'
    );
  });
});

describe('dockTabsFor continuous run', () => {
  // The Continuous tab follows the Log tab, for a run with a continuous step only.
  it('shows Continuous after Log when a step is continuous', () => {
    expect(dockTabsFor({ ...BASE, hasContinuous: true })).toEqual([
      'log',
      'continuous',
      'timeline',
      'outputs',
    ]);
    expect(dockTabsFor({ ...BASE, hasContinuous: false })).not.toContain(
      'continuous'
    );
    expect(dockTabsFor(BASE)).not.toContain('continuous');
  });

  it('reads the facts from the nodes of the run', () => {
    const details = (continuous: boolean) =>
      ({
        status: Status.Running,
        nodes: [{ step: { name: 'a' }, continuous }],
      }) as unknown as ApiDAGRunDetails;
    expect(
      dockFacts(details(true), { artifactEnabled: false }).hasContinuous
    ).toBe(true);
    expect(
      dockFacts(details(false), { artifactEnabled: false }).hasContinuous
    ).toBe(false);
  });
});
