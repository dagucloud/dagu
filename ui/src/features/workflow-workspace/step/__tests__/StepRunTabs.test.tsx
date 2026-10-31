// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, NodeStatusLabel, Status } from '@/api/v1/schema';
import type { ApiDAGRunDetails, WorkspaceRunValue } from '../../contracts';
import { DAG_NAME, FILE_NAME, node, run } from '../../data/__tests__/fixtures';
import { WorkspaceDataContext } from '../../data/context';
import { nodesByStep } from '../../data/runFacts';
import type { WorkspaceDataValue } from '../../data/context';
import { DockTabStrip } from '../../dock/DockTabStrip';
import { StepRunTabs, stepRunTabs } from '../StepRunTabs';

const h = vi.hoisted(() => ({
  followed: {
    details: null as ApiDAGRunDetails | null,
    active: false,
    error: null,
  },
  bodies: [] as Record<string, unknown>[],
  read: vi.fn(),
}));

// The own read of a followed run: the hook that the selected run must not use.
vi.mock('@/features/dag-runs/hooks/useBoundedDAGRunDetails', () => ({
  useBoundedDAGRunDetails: (args: unknown) => {
    h.read(args);
    return { data: h.followed.details ?? undefined, error: null };
  },
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
// The bodies have their own tests; here only what the host gives them. The
// Actions and Agent bodies are real, for what they show for one step.
vi.mock('../../dock/tabs', async () => {
  const actual = await vi.importActual<
    typeof import('../../dock/tabs/runTabBodies')
  >('../../dock/tabs/runTabBodies');
  const body = (tab: string) =>
    function Body(props: Record<string, unknown>) {
      h.bodies.push(props);
      return <div data-testid={`body-${tab}`} />;
    };
  return {
    dockTabBodies: {
      log: body('log'),
      timeline: body('timeline'),
      outputs: body('outputs'),
      actions: actual.ActionsTab,
      agent: actual.AgentTab,
    },
  };
});
vi.mock('@/features/dags/components/approval/ApprovalTab', () => ({
  ApprovalTab: ({ stepName }: { stepName?: string }) => (
    <button type="button">{`Approve ${stepName}`}</button>
  ),
}));
vi.mock('@/features/dags/components/human-task/HumanTasksTab', () => ({
  HumanTasksTab: () => <div data-testid="human-tasks" />,
}));
vi.mock('@/features/dags/components/approval/ApprovalResumeAlert', () => ({
  ApprovalResumeAlert: () => <div data-testid="resume-alert" />,
}));
vi.mock('@/features/dags/components/agent-session/AgentSessionTab', () => ({
  AgentSessionTab: () => <div data-testid="agent-session" />,
}));
vi.mock('@/features/dags/components/chat-history/ChatHistoryTab', () => ({
  ChatHistoryTab: ({ stepName }: { stepName?: string }) => (
    <div data-testid="chat" data-step={stepName} />
  ),
  hasChatHistory: (n: { step: { executorConfig?: { type?: string } } }) =>
    ['chat', 'agent'].includes(n.step.executorConfig?.type ?? ''),
}));

const data = {
  fileName: FILE_NAME,
  dagData: { dag: { name: DAG_NAME } },
  run: { ref: null },
} as unknown as WorkspaceDataValue;

function setup(runId: string | null, value: WorkspaceDataValue = data) {
  return render(
    <MemoryRouter>
      <WorkspaceDataContext.Provider value={value}>
        <StepRunTabs
          step="merge"
          runId={runId}
          declared={<div data-testid="declared">declared outputs</div>}
        />
      </WorkspaceDataContext.Provider>
    </MemoryRouter>
  );
}

/** The workspace data with this run selected: the run that StepRunTabs reads. */
function selected(details: ApiDAGRunDetails) {
  const value = {
    ...data,
    run: {
      ref: { name: DAG_NAME, runId: details.dagRunId },
      details,
      hasRun: true,
      isActive: false,
      nodeByStep: nodesByStep(details),
      refresh: vi.fn(),
    } as unknown as WorkspaceRunValue,
  } as WorkspaceDataValue;
  return value;
}

const tabNames = () => screen.getAllByRole('tab').map((t) => t.textContent);

beforeEach(() => {
  h.followed = { details: null, active: false, error: null };
  h.bodies = [];
  h.read.mockClear();
});

describe('StepRunTabs without a run', () => {
  it('lists Log and Output, with the declared outputs open', () => {
    setup(null);
    expect(screen.queryByTestId('ww-step-facts-line')).toBeNull();
    expect(tabNames()).toEqual(['Log', 'Output']);
    expect(screen.getByRole('tab', { name: 'Output' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(screen.getByTestId('declared')).toBeInTheDocument();
    expect(h.bodies).toHaveLength(0);
  });

  it('asks to execute the step in the Log tab', () => {
    setup(null);
    fireEvent.click(screen.getByRole('tab', { name: 'Log' }));
    expect(
      screen.getByText('Execute the step to see the log.')
    ).toBeInTheDocument();
    expect(screen.queryByTestId('declared')).toBeNull();
  });
});

describe('StepRunTabs with a run', () => {
  it('shows the tabs of the Run panel for the facts of the run', () => {
    const details = run('r2', [node('merge')]);
    h.followed = { details, active: false, error: null };
    setup('r2');
    expect(tabNames()).toEqual(['Log', 'Output']);
  });

  it('opens on Output and gives the body the followed run and the step', () => {
    h.followed = {
      details: run('r2', [node('merge')]),
      active: false,
      error: null,
    };
    setup('r2');
    expect(screen.getByTestId('body-outputs')).toBeInTheDocument();
    expect(screen.queryByTestId('declared')).toBeNull();
    const props = h.bodies[h.bodies.length - 1] as {
      run: { ref: { runId: string }; hasRun: boolean };
      step: string;
    };
    expect(props.run.ref.runId).toBe('r2');
    expect(props.step).toBe('merge');
  });

  it('opens a failed run on its log', () => {
    h.followed = {
      details: run('r3', [node('merge', NodeStatus.Failed)], Status.Failed),
      active: false,
      error: null,
    };
    setup('r3');
    expect(screen.getByTestId('body-log')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Log' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
  });

  // The log of a foreach step in the node view lists its items.
  it('opens a foreach step on its log', () => {
    h.followed = {
      details: run('r2', [
        node('merge', NodeStatus.Success, {
          step: { executorConfig: { type: 'foreach' } },
        }),
      ]),
      active: false,
      error: null,
    };
    setup('r2');
    expect(screen.getByTestId('body-log')).toBeInTheDocument();
  });

  it('keeps the tab that the user chose', () => {
    h.followed = {
      details: run('r2', [node('merge')]),
      active: false,
      error: null,
    };
    setup('r2');
    fireEvent.click(screen.getByRole('tab', { name: 'Log' }));
    expect(screen.getByTestId('body-log')).toBeInTheDocument();
    expect(screen.queryByRole('tab', { name: 'Timeline' })).toBeNull();
  });

  it('gives the body a run without details while the run loads', () => {
    h.followed = { details: null, active: true, error: null };
    setup('r4');
    expect(tabNames()).toEqual(['Log', 'Output']);
    const props = h.bodies[h.bodies.length - 1] as {
      run: { details: unknown; hasRun: boolean; isActive: boolean };
    };
    expect(props.run.details).toBeNull();
    expect(props.run.hasRun).toBe(true);
    expect(props.run.isActive).toBe(true);
  });

  it('reads a run that is not the selected one by itself', () => {
    h.followed = {
      details: run('r6', [node('merge')]),
      active: false,
      error: null,
    };
    setup('r6', selected(run('r2', [node('merge')])));
    expect(h.read).toHaveBeenCalled();
    const props = h.bodies[h.bodies.length - 1] as {
      run: { ref: { runId: string } };
    };
    expect(props.run.ref.runId).toBe('r6');
  });

  it('follows the new run when Execute step runs again', () => {
    h.followed = {
      details: run('r2', [node('merge')]),
      active: false,
      error: null,
    };
    const view = setup('r2');
    h.followed = {
      details: run('r5', [node('merge')], Status.Running),
      active: true,
      error: null,
    };
    view.rerender(
      <MemoryRouter>
        <WorkspaceDataContext.Provider value={data}>
          <StepRunTabs step="merge" runId="r5" declared={null} />
        </WorkspaceDataContext.Provider>
      </MemoryRouter>
    );
    const props = h.bodies[h.bodies.length - 1] as {
      run: { ref: { runId: string } };
    };
    expect(props.run.ref.runId).toBe('r5');
  });
});

describe('StepRunTabs with the selected run', () => {
  it('uses the workspace run and makes no read of its own', () => {
    const value = selected(run('r2', [node('merge')]));
    setup('r2', value);
    expect(h.read).not.toHaveBeenCalled();
    const props = h.bodies[h.bodies.length - 1] as { run: unknown };
    expect(props.run).toBe(value.run);
  });

  it('shows the facts of the step above the tabs', () => {
    const merge = node('merge', NodeStatus.Failed, {
      retryCount: 2,
      error: 'exit status 3',
      statusLabel: NodeStatusLabel.failed,
    });
    setup('r2', selected(run('r2', [merge], Status.Failed)));
    const line = screen.getByTestId('ww-step-facts-line');
    expect(line).toHaveTextContent('Failed');
    expect(line).toHaveTextContent('3s');
    expect(within(line).getByLabelText('Retried 2×')).toBeInTheDocument();
    expect(line).toHaveTextContent('Exit code 3');
    // The line is above the strip.
    expect(
      line.compareDocumentPosition(screen.getByRole('tablist')) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
  });

  it('shows the repeat badge with the run count on the facts line of a repeating step', () => {
    const loop = node('merge', NodeStatus.Running, {
      doneCount: 3,
      step: { repeatPolicy: { repeat: 'while', interval: 10 } },
      statusLabel: NodeStatusLabel.running,
    });
    setup('r2', selected(run('r2', [loop], Status.Running)));
    const line = screen.getByTestId('ww-step-facts-line');
    expect(within(line).getByTestId('repeat-badge')).toHaveTextContent('×3');
    expect(within(line).getByTestId('repeat-badge')).toHaveAttribute(
      'data-running',
      'true'
    );
    expect(within(line).getByTestId('ww-repeat-summary')).toHaveTextContent(
      'repeat while exit 0 · every 10s · no limit'
    );
  });

  // A test run of an unsaved draft: the server drops it after a short time.
  it('says on the facts line that a test run is not kept', () => {
    const labels = ['dagu.source=editor', 'dagu.ephemeral=true'];
    setup(
      'r2',
      selected(run('r2', [node('merge')], Status.Success, { labels }))
    );
    expect(
      within(screen.getByTestId('ww-step-facts-line')).getByTestId(
        'ww-step-test-run'
      )
    ).toHaveTextContent('Test run, not kept');
  });

  it('says nothing about a test run for a run in the history', () => {
    const labels = ['dagu.source=editor'];
    setup(
      'r2',
      selected(run('r2', [node('merge')], Status.Success, { labels }))
    );
    expect(screen.queryByTestId('ww-step-test-run')).toBeNull();
  });

  it('says it for a followed test run that is not the selected run', () => {
    h.followed = {
      details: run('r6', [node('merge')], Status.Success, {
        labels: ['dagu.ephemeral=true'],
      }),
      active: false,
      error: null,
    };
    setup('r6', selected(run('r2', [node('merge')])));
    expect(screen.getByTestId('ww-step-test-run')).toBeInTheDocument();
  });

  it('leaves the facts line out for a step that is not in the run', () => {
    setup('r2', selected(run('r2', [node('other')])));
    expect(screen.queryByTestId('ww-step-facts-line')).toBeNull();
  });

  it('opens a waiting step on Actions, with its manual action', () => {
    const merge = node('merge', NodeStatus.Waiting, {
      step: { approval: {} },
      statusLabel: NodeStatusLabel.waiting,
    });
    setup('r2', selected(run('r2', [merge, node('other')], Status.Waiting)));
    expect(screen.getByRole('tab', { name: /Actions/ })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(
      screen.getByRole('button', { name: 'Approve merge' })
    ).toBeInTheDocument();
  });

  // The strip is about this step: another step that waits adds no tab.
  it('lists no Actions tab when another step waits', () => {
    const other = node('other', NodeStatus.Waiting, {
      step: { approval: {} },
    });
    setup('r2', selected(run('r2', [node('merge'), other], Status.Waiting)));
    expect(tabNames()).toEqual(['Log', 'Output']);
  });

  it('shows the chat of an agent step in the Agent tab', () => {
    const merge = node('merge', NodeStatus.Success, {
      step: { executorConfig: { type: 'agent' } },
      agentSession: { state: 'completed', provider: 'x' },
    });
    setup('r2', selected(run('r2', [merge])));
    fireEvent.click(screen.getByRole('tab', { name: /Agent/ }));
    expect(screen.getByTestId('chat')).toHaveAttribute('data-step', 'merge');
    expect(screen.getByTestId('agent-session')).toBeInTheDocument();
  });
});

describe('the strip of the step view and of the Run panel', () => {
  // Both hosts render DockTabStrip: the same tabs give the same buttons.
  it('renders the same tab bar markup apart from the ids', () => {
    const tabs = stepRunTabs(null, 'merge');
    const bar = (idPrefix?: string) => {
      const { container, unmount } = render(
        <DockTabStrip
          tabs={tabs}
          active="outputs"
          badges={{}}
          onSelect={() => undefined}
          idPrefix={idPrefix}
        />
      );
      const html = container.innerHTML.replace(/ww-[a-z-]+-(tab|panel)/g, 'ID');
      unmount();
      return html;
    };
    expect(bar('ww-step-output')).toBe(bar());
  });
});
