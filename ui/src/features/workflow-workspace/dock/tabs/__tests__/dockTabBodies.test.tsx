// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { useEffect, type ReactNode } from 'react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  NodeStatus,
  Status,
  StatusLabel,
  type components,
} from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { useBoundedDAGRunDetails } from '@/features/dag-runs/hooks/useBoundedDAGRunDetails';
import {
  useWorkspaceEditorBinding,
  WorkspaceEditorProvider,
} from '../../../canvas/WorkspaceEditorContext';
import type {
  ApiDAGDetails,
  ApiDAGRunDetails,
  WorkspaceEditorHandle,
} from '../../../contracts';
import { useWorkspaceRun } from '../../../data';
import {
  connectedSSE,
  dagPage,
  FILE_NAME,
  node,
  run,
  zeroRun,
} from '../../../data/__tests__/fixtures';
import { serve, type FakeServer } from '../../../data/__tests__/harness';
import { WorkspaceDataProvider } from '../../../data/WorkspaceDataProvider';
import { WorkspaceRunHosts } from '../../../hosts/WorkspaceRunHosts';
import { useWorkspaceUrl, type DockTab } from '../../../state/workspaceUrl';
import { DOCK_OPEN_KEY, DockProvider } from '../../DockContext';
import { LogsDock } from '../../LogsDock';
import { dockTabBodies } from '..';
import { leaves } from './leafStubs';

type AgentEvent = components['schemas']['AgentEvent'];

const h = vi.hoisted(() => ({
  GET: vi.fn(),
  refreshDag: vi.fn(async () => undefined),
  requestStart: vi.fn(),
}));

vi.mock('@/hooks/api', async () => {
  const { createQueryHook } = await import('swr-openapi');
  return {
    useQuery: createQueryHook({ GET: h.GET } as never, '/'),
    useClient: () => ({ GET: h.GET, POST: vi.fn(), PATCH: vi.fn() }),
  };
});
vi.mock('@/hooks/useDAGRunSSE', () => ({ useDAGRunSSE: () => connectedSSE }));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ permissions: { runDags: true } }),
}));
vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: vi.fn() }),
}));
vi.mock('../../../bar/RunStartContext', () => ({
  useRunStart: () => ({ canStart: true, requestStart: h.requestStart }),
}));
vi.mock('@/features/dag-runs/hooks/useBoundedDAGRunDetails', () => ({
  useBoundedDAGRunDetails: vi.fn(),
}));
// The stack content renders the canvas; this stand-in shows the opened run.
vi.mock('../../../hosts/SubRunView', () => ({
  SubRunView: ({ run }: { run: { dagRunId: string } }) => (
    <div data-testid="child-content" data-run={run.dagRunId} />
  ),
}));

// Shared leaves with their own tests. TimelineChart and AgentTimeline stay
// real, for the sub-run stack.
vi.mock(
  '@/features/dag-runs/components/dag-run-details/DAGRunOutputs',
  async () => ({ default: (await import('./leafStubs')).leaf('outputs') })
);
vi.mock('@/features/dags/components/dag-execution/ExecutionLog', async () => ({
  default: (await import('./leafStubs')).leaf('executionLog'),
}));
vi.mock('@/features/dags/components/artifacts/ArtifactsTab', async () => ({
  default: (await import('./leafStubs')).leaf('artifacts'),
}));
vi.mock('@/features/dags/components/human-task/HumanTasksTab', async () => ({
  HumanTasksTab: (await import('./leafStubs')).leaf('humanTasks'),
}));
vi.mock('@/features/dags/components/approval/ApprovalTab', async () => ({
  ApprovalTab: (await import('./leafStubs')).leaf('approval'),
}));
vi.mock(
  '@/features/dags/components/approval/ApprovalResumeAlert',
  async () => ({
    ApprovalResumeAlert: (await import('./leafStubs')).leaf('approvalResume'),
  })
);
vi.mock(
  '@/features/dags/components/agent-session/AgentSessionTab',
  async () => ({
    AgentSessionTab: (await import('./leafStubs')).leaf('agentSession'),
  })
);
vi.mock('@/features/dags/components/chat-history/ChatHistoryTab', async () => ({
  ChatHistoryTab: (await import('./leafStubs')).leaf('chat'),
  hasChatHistory: (n: { step: { executorConfig?: { type?: string } } }) =>
    ['chat', 'agent'].includes(n.step.executorConfig?.type ?? ''),
}));
vi.mock('@/features/dags/components/agent/TaskChecklistTab', async () => ({
  TaskChecklistTab: (await import('./leafStubs')).leaf('tasks'),
}));
vi.mock('../../../step/StepRunView', async () => ({
  StepRunView: (await import('./leafStubs')).leaf('stepRun'),
}));
vi.mock('../../../step/StepForeach', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../step/StepForeach')>()),
  StepForeach: (await import('./leafStubs')).leaf('foreach'),
}));

let server: FakeServer;
const location = { search: '' };

function Location() {
  location.search = useLocation().search;
  return null;
}

function Workspace({ children }: { children: ReactNode }) {
  return (
    <WorkspaceDataProvider
      fileName={FILE_NAME}
      dagData={server.dag}
      refreshDag={h.refreshDag}
      workspaceName={null}
    >
      <WorkspaceRunHosts>
        <DockProvider>
          <Location />
          {children}
        </DockProvider>
      </WorkspaceRunHosts>
    </WorkspaceDataProvider>
  );
}

function renderAt(ui: ReactNode, url: string) {
  return render(
    <SWRConfig value={{ provider: () => new Map(), shouldRetryOnError: false }}>
      <UserPreferencesProvider>
        <MemoryRouter initialEntries={[url]}>
          <Routes>
            <Route
              path="/dags/:fileName"
              element={<Workspace>{ui}</Workspace>}
            />
          </Routes>
        </MemoryRouter>
      </UserPreferencesProvider>
    </SWRConfig>
  );
}

/** One registered body, as LogsDock mounts it, with a run picker. */
function Body({
  tab,
  step,
  selectTab,
}: {
  tab: DockTab;
  step?: string;
  selectTab?: (tab: DockTab) => void;
}) {
  const workspaceRun = useWorkspaceRun();
  const Tab = dockTabBodies[tab]!;
  return (
    <>
      <button type="button" onClick={() => workspaceRun.select('latest')}>
        select latest
      </button>
      <Tab
        run={workspaceRun}
        fileName={FILE_NAME}
        dag={server.dag.dag as ApiDAGDetails}
        step={step}
        selectTab={selectTab}
      />
    </>
  );
}

async function renderBody(
  tab: DockTab,
  url = `/dags/${FILE_NAME}`,
  step?: string
) {
  const view = renderAt(<Body tab={tab} step={step} />, url);
  // A pinned run loads first; the latest run is in the DAG data.
  await vi.waitFor(() =>
    expect(screen.queryByText('Loading run...')).not.toBeInTheDocument()
  );
  return view;
}

function renderDock(url = `/dags/${FILE_NAME}`) {
  return renderAt(
    <LogsDock fileName={FILE_NAME} dag={server.dag.dag as ApiDAGDetails} />,
    url
  );
}

function propsOf(name: string) {
  const props = leaves.props[name];
  if (!props) throw new Error(`leaf ${name} is not mounted`);
  return props;
}

function tabNames(): string[] {
  return screen
    .getAllByRole('tab')
    .map((tab) => tab.getAttribute('aria-label') ?? tab.textContent ?? '');
}

function setLatest(
  details: ApiDAGRunDetails,
  dag: Record<string, unknown> = {}
) {
  server.dag = dagPage(details);
  server.dag.dag = { ...server.dag.dag, ...dag } as typeof server.dag.dag;
}

const fanOut = node('fan-out', NodeStatus.Success, {
  step: { call: 'child-dag', parallel: { items: ['c-1'] } },
  subRuns: [{ dagRunId: 'c-1', dagName: 'child-dag', params: '' }],
});

const agentEvents: AgentEvent[] = [
  {
    turn: 1,
    kind: 'action' as AgentEvent['kind'],
    name: 'delegate',
    childDagRunId: 'a-1',
    childDagName: 'helper',
  },
];

const agentRun = run(
  'run-1',
  [
    node('plan', NodeStatus.Success, { agentSession: { state: 'succeeded' } }),
    node('ask', NodeStatus.Running, { agentSession: { state: 'waiting' } }),
    node('plain'),
  ],
  Status.Running,
  {
    agentEvents,
    agentTasks: [{ name: 'Plan', status: 'open' }],
  } as unknown as Partial<ApiDAGRunDetails>
);

beforeEach(() => {
  leaves.reset();
  h.GET.mockReset();
  h.refreshDag.mockClear();
  localStorage.clear();
  localStorage.setItem(DOCK_OPEN_KEY, 'true');
  server = {
    dag: dagPage(run('run-1', [node('a'), node('b')])),
    runs: {
      'run-2': run('run-2', [node('a')], Status.Success, {
        rootDAGRunName: 'demo',
        rootDAGRunId: 'run-2',
      }),
    },
    history: { dagRuns: [], gridData: [] },
    subRuns: [
      {
        dagRunId: 'c-1',
        dagName: 'child-dag',
        params: '',
        status: Status.Success,
        statusLabel: StatusLabel.succeeded,
        startedAt: '2026-01-15T02:00:01Z',
        finishedAt: '2026-01-15T02:00:02Z',
      },
    ],
  };
  h.GET.mockImplementation(serve(server));
  vi.mocked(useBoundedDAGRunDetails).mockImplementation(({ target }) => {
    return {
      data: target
        ? ({ ...run(target.dagRunId), name: target.name } as ApiDAGRunDetails)
        : undefined,
      isValidating: false,
      refresh: vi.fn(),
    } as unknown as ReturnType<typeof useBoundedDAGRunDetails>;
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('dock tab bodies', () => {
  it('registers a body for every dock tab, none for Steps and Spec', () => {
    expect(Object.keys(dockTabBodies).sort()).toEqual(
      [
        'actions',
        'agent',
        'artifacts',
        'chat',
        'continuous',
        'history',
        'log',
        'outputs',
        'tasks',
        'timeline',
      ].sort()
    );
  });

  // parity:timeline-tab parity:run-tab-timeline parity:test-timeline parity:dup-3
  describe('Timeline', () => {
    beforeEach(() => {
      server.runs['run-2'] = run('run-2', [fanOut], Status.Success, {
        rootDAGRunName: 'demo',
        rootDAGRunId: 'run-2',
      });
    });

    it('opens a child row on the sub-run stack', async () => {
      await renderBody('timeline', `/dags/${FILE_NAME}?run=run-2`);
      const bar = await screen.findByTestId('timeline-bar-subdag:fan-out:c-1');

      fireEvent.click(bar);

      expect(await screen.findByTestId('child-content')).toHaveAttribute(
        'data-run',
        'c-1'
      );
    });

    it('opens a child row in a new tab with Ctrl or Cmd', async () => {
      const open = vi.spyOn(window, 'open').mockReturnValue(null);
      await renderBody('timeline', `/dags/${FILE_NAME}?run=run-2`);
      const bar = await screen.findByTestId('timeline-bar-subdag:fan-out:c-1');

      fireEvent.click(bar, { ctrlKey: true });
      fireEvent.click(bar, { metaKey: true });

      expect(open).toHaveBeenCalledTimes(2);
      const [url, target] = open.mock.calls[0]!;
      expect(target).toBe('_blank');
      expect(String(url)).toMatch(/^\/dags\/demo\?.*run=run-2/);
      // The tab opens the child over its root run.
      expect(new URLSearchParams(String(url).split('?')[1]).get('subRun')).toBe(
        'c-1'
      );
      expect(screen.queryByTestId('child-content')).not.toBeInTheDocument();

      // A plain click after a modifier click uses the stack again.
      fireEvent.click(bar);
      expect(await screen.findByTestId('child-content')).toBeInTheDocument();
      expect(open).toHaveBeenCalledTimes(2);
    });
  });

  // parity:outputs-tab parity:run-tab-outputs
  it('gives Outputs the name and ID of the run', async () => {
    await renderBody('outputs');
    // The tab pads its body: the empty text adds no padding of its own.
    expect(propsOf('outputs')).toEqual({
      dagName: 'demo',
      dagRunId: 'run-1',
      padded: false,
      bare: true,
      prettyValues: true,
      pollWhileEmpty: false,
    });
  });

  // A run writes its outputs when it ends: a running run, which may never end,
  // is not polled for them.
  it('does not poll the outputs of a running run', async () => {
    setLatest(run('run-1', [node('a', NodeStatus.Running)], Status.Running));
    await renderBody('outputs');
    expect(propsOf('outputs').pollWhileEmpty).toBe(false);
  });

  // A run that ended with no outputs may still write them for a moment; the
  // list stops asking once that moment is over, with no other render.
  it('stops polling the outputs 15 s after the run ended', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] });
    try {
      vi.setSystemTime(new Date('2026-01-15T02:00:10Z'));
      setLatest(
        run('run-1', [node('a')], Status.Success, {
          finishedAt: '2026-01-15T02:00:05Z',
        })
      );
      await renderBody('outputs');
      expect(propsOf('outputs').pollWhileEmpty).toBe(true);

      act(() => {
        vi.advanceTimersByTime(11_000);
      });

      expect(propsOf('outputs').pollWhileEmpty).toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });

  // The OUTPUT column of a step: the Outputs tab shows the outputs of that
  // step, not the run level list that has no entry for its declared outputs.
  describe('Outputs of a step', () => {
    const STEP_OUTPUTS = '/dag-runs/{name}/{dagRunId}/steps/{stepName}/outputs';

    function serveStepOutputs(body: Record<string, unknown>) {
      const base = serve(server);
      h.GET.mockImplementation(async (path: string, init?: never) =>
        path === STEP_OUTPUTS ? { data: body } : base(path, init)
      );
    }

    it('shows the values of the step and not the run list', async () => {
      serveStepOutputs({
        outputs: [
          { name: 'token', value: 'abc123', truncated: false, size: 6 },
        ],
      });
      await renderBody('outputs', `/dags/${FILE_NAME}`, 'a');

      expect(await screen.findByText('token')).toBeInTheDocument();
      expect(screen.getByText('abc123')).toBeInTheDocument();
      expect(screen.queryByTestId('leaf-outputs')).not.toBeInTheDocument();
      const call = h.GET.mock.calls.find(([path]) => path === STEP_OUTPUTS);
      expect(call?.[1].params.path).toEqual({
        name: 'demo',
        dagRunId: 'run-1',
        stepName: 'a',
      });
    });

    it('shows the output variable of the step', async () => {
      serveStepOutputs({
        outputVariable: {
          name: 'RESULT',
          value: 'ok',
          truncated: false,
          size: 2,
        },
        outputs: [],
      });
      await renderBody('outputs', `/dags/${FILE_NAME}`, 'a');

      expect(await screen.findByText(/RESULT/)).toBeInTheDocument();
      expect(screen.getByText('ok')).toBeInTheDocument();
    });

    it('says that the step has no outputs', async () => {
      serveStepOutputs({ outputs: [] });
      await renderBody('outputs', `/dags/${FILE_NAME}`, 'a');

      expect(
        await screen.findByText('No outputs in this run')
      ).toBeInTheDocument();
      expect(screen.queryByTestId('leaf-outputs')).not.toBeInTheDocument();
    });

    it('keeps the run list for a run without a step', async () => {
      await renderBody('outputs');
      expect(screen.getByTestId('leaf-outputs')).toBeInTheDocument();
    });
  });

  // parity:artifacts-tree parity:artifacts-preview parity:run-tab-artifacts
  // parity:test-artifacts parity:api-artifacts
  describe('Artifacts', () => {
    it('passes the run and artifactEnabled of the workflow', async () => {
      setLatest(run('run-1'), { artifacts: { enabled: true } });
      await renderBody('artifacts');

      const props = propsOf('artifacts');
      expect((props.dagRun as ApiDAGRunDetails).dagRunId).toBe('run-1');
      expect(props.artifactEnabled).toBe(true);
      expect(props.fillHeight).toBe(true);
    });

    it('passes artifactEnabled false when the workflow has no artifacts', async () => {
      await renderBody('artifacts');
      expect(propsOf('artifacts').artifactEnabled).toBe(false);
    });
  });

  // parity:approval-tab parity:human-tasks-list parity:run-tab-human-tasks
  // parity:run-tab-approval parity:overview-summary-action-required parity:dup-22
  describe('Actions', () => {
    const waiting = run(
      'run-1',
      [
        node('review', NodeStatus.Waiting, { step: { humanTask: {} } }),
        node('gate', NodeStatus.Waiting, { step: { approval: {} } }),
      ],
      Status.Waiting,
      { approvalResumePending: true }
    );

    it('shows the resume alert, the human tasks and the approvals in order', async () => {
      setLatest(waiting);
      await renderBody('actions');

      const ids = screen
        .getAllByTestId(/^leaf-/)
        .map((el) => el.getAttribute('data-testid'));
      expect(ids).toEqual([
        'leaf-approvalResume',
        'leaf-humanTasks',
        'leaf-approval',
      ]);
      expect(propsOf('approval').dagName).toBe('demo');
      expect((propsOf('humanTasks').dagRun as ApiDAGRunDetails).dagRunId).toBe(
        'run-1'
      );
    });

    it('refreshes the run when a human task changes', async () => {
      setLatest(waiting);
      await renderBody('actions');

      await act(async () => {
        fireEvent.click(
          screen.getByRole('button', { name: 'humanTasks changed' })
        );
      });

      expect(h.refreshDag).toHaveBeenCalledTimes(1);
    });

    it('shows only the parts that wait', async () => {
      setLatest(
        run(
          'run-1',
          [node('gate', NodeStatus.Waiting, { step: { approval: {} } })],
          Status.Running
        )
      );
      await renderBody('actions');

      expect(screen.getByTestId('leaf-approval')).toBeInTheDocument();
      expect(screen.queryByTestId('leaf-humanTasks')).not.toBeInTheDocument();
      expect(
        screen.queryByTestId('leaf-approvalResume')
      ).not.toBeInTheDocument();
    });
  });

  // parity:agent-session-tabs parity:agent-session-card
  // parity:agent-permission-interaction parity:agent-question-interaction
  // parity:run-tab-agent parity:test-agent-session
  describe('Agent', () => {
    // parity:overview-agent-decision-timeline parity:chg-agent-timeline-move
    it('shows the decision timeline above the sessions', async () => {
      setLatest(agentRun);
      await renderBody('agent');

      const timeline = screen.getByRole('button', { name: /delegate/ });
      const sessions = screen.getByTestId('leaf-agentSession');
      expect(
        timeline.compareDocumentPosition(sessions) &
          Node.DOCUMENT_POSITION_FOLLOWING
      ).toBeTruthy();
    });

    it('shows the turns, tool calls and tasks above the timeline', async () => {
      setLatest(agentRun);
      await renderBody('agent');

      const summary = screen.getByTestId('agent-run-summary');
      expect(summary).toHaveTextContent('Tool calls:');
      expect(summary).toHaveTextContent('0 of 1 tasks settled');
      expect(
        summary.compareDocumentPosition(
          screen.getByRole('button', { name: /delegate/ })
        ) & Node.DOCUMENT_POSITION_FOLLOWING
      ).toBeTruthy();
    });

    it('opens a child run of the timeline on the sub-run stack', async () => {
      setLatest(agentRun);
      await renderBody('agent');

      fireEvent.click(screen.getByRole('button', { name: /delegate/ }));

      expect(await screen.findByTestId('child-content')).toHaveAttribute(
        'data-run',
        'a-1'
      );
    });

    it('has no timeline without agent events', async () => {
      setLatest(
        run('run-1', [
          node('ask', NodeStatus.Running, {
            agentSession: { state: 'waiting' },
          }),
        ])
      );
      await renderBody('agent');

      expect(screen.getByTestId('leaf-agentSession')).toBeInTheDocument();
      expect(screen.queryByTestId('agent-run-summary')).toBeNull();
      expect(
        screen.queryByRole('button', { name: /delegate/ })
      ).not.toBeInTheDocument();
    });

    it('preselects the ?step session and refreshes the run on a change', async () => {
      setLatest(agentRun);
      await renderBody('agent', `/dags/${FILE_NAME}?step=ask`);

      expect(propsOf('agentSession').selectedStep).toBe('ask');

      await act(async () => {
        fireEvent.click(
          screen.getByRole('button', { name: 'agentSession changed' })
        );
      });
      expect(h.refreshDag).toHaveBeenCalledTimes(1);
    });

    it('leaves the choice to the sessions for a step without one', async () => {
      setLatest(agentRun);
      await renderBody('agent', `/dags/${FILE_NAME}?step=plain`);

      expect(propsOf('agentSession').selectedStep).toBe('');
    });
  });

  // parity:chat-tab parity:run-tab-chat
  it('gives Chat the run', async () => {
    await renderBody('chat');
    expect((propsOf('chat').dagRun as ApiDAGRunDetails).dagRunId).toBe('run-1');
  });

  // parity:tasks-tab parity:run-tab-tasks
  it('gives Tasks the agent tasks of the run', async () => {
    setLatest(agentRun);
    await renderBody('tasks');
    expect(propsOf('tasks').tasks).toEqual(agentRun.agentTasks);
  });

  // parity:execution-log-viewer parity:run-log-viewer parity:run-log-execution
  // parity:runlog-display-mode parity:runlog-fetch-live parity:runlog-download
  // parity:api-run-log parity:dup-10
  describe('Log', () => {
    it('shows the scheduler log of the run without ?step', async () => {
      await renderBody('log');

      const props = propsOf('executionLog');
      expect(props.name).toBe('demo');
      expect(props.dagRunId).toBe('run-1');
      expect((props.dagRun as ApiDAGRunDetails).dagRunId).toBe('run-1');
      expect(screen.queryByTestId('leaf-stepRun')).not.toBeInTheDocument();
    });

    // The first row stays clear of the dock edges: the log scrolls inside a
    // padded box and does not scroll with it. Every tab body has the same
    // px-3 inset (the log had px-2).
    it('pads the scheduler log from the dock edges', async () => {
      await renderBody('log');

      const box = screen.getByTestId('ww-run-log');
      expect(box).toHaveClass('px-3', 'pt-2', 'pb-2', 'overflow-hidden');
      expect(box).toContainElement(screen.getByTestId('leaf-executionLog'));
    });

    // The reused steps ran before the run: the log puts their lines with its
    // own, in the order that the reader chose.
    describe('cached logs of the reused steps', () => {
      const reusedRun = () =>
        run(
          'run-1',
          [
            node('fetch', NodeStatus.Skipped),
            node('use', NodeStatus.Success, { step: { depends: ['fetch'] } }),
          ],
          Status.Success,
          {
            reusedInputs: [
              {
                step: 'fetch',
                runId: '034mroF1udXVyvPz00bn5a',
                startedAt: '2026-01-01T00:00:00Z',
                finishedAt: '2026-01-01T00:00:20Z',
                via: 'run',
              },
            ],
          } as unknown as Partial<ApiDAGRunDetails>
        );

      it('gives the log the cached steps of a run that reused steps', async () => {
        setLatest(reusedRun());
        await renderBody('log');

        expect(propsOf('executionLog').cachedLines).toBeTruthy();
      });

      it('gives the log no cached block for a run that reused nothing', async () => {
        await renderBody('log');
        expect(propsOf('executionLog').cachedLines).toBeUndefined();
      });
    });

    it('shows the step log that ?step and ?stream name', async () => {
      await renderBody('log', `/dags/${FILE_NAME}?step=a&stream=stderr`);

      expect(propsOf('stepRun')).toEqual({
        target: { step: 'a', stream: 'stderr' },
        variant: 'logs',
      });
      expect(screen.queryByTestId('leaf-executionLog')).not.toBeInTheDocument();
    });

    // A remount on a stream change would empty the log while the other
    // stream loads: the step view stays mounted and switches in place.
    it('keeps the step view mounted when ?stream changes', async () => {
      function SwitchStream() {
        const [, setUrl] = useWorkspaceUrl();
        return (
          <button
            type="button"
            onClick={() => setUrl({ stream: 'stderr' }, { replace: true })}
          >
            stream stderr
          </button>
        );
      }
      renderAt(
        <>
          <SwitchStream />
          <Body tab="log" />
        </>,
        `/dags/${FILE_NAME}?step=a&stream=stdout`
      );
      await vi.waitFor(() => expect(propsOf('stepRun')).toBeDefined());
      expect(leaves.mounts.stepRun).toBe(1);

      fireEvent.click(screen.getByRole('button', { name: 'stream stderr' }));

      expect(propsOf('stepRun').target).toMatchObject({ stream: 'stderr' });
      expect(leaves.mounts.stepRun).toBe(1);
    });

    // The step log has no error text: the banner above it gives the reason.
    it('shows the error of a failed step above its log', async () => {
      setLatest(
        run(
          'run-1',
          [node('a', NodeStatus.Failed, { error: 'exit status 1' })],
          Status.Failed
        )
      );
      await renderBody('log', `/dags/${FILE_NAME}?step=a&stream=stderr`);

      expect(screen.getByTestId('ww-step-failure')).toHaveTextContent(
        'exit status 1'
      );
    });

    // A step of a sub run is not the step of this run with the same name.
    it('shows no error banner for a step of a sub run', async () => {
      setLatest(
        run(
          'run-1',
          [
            node('fan-out', NodeStatus.Failed, {
              error: 'child failed',
              step: { call: 'child-dag', parallel: { items: ['c-1'] } },
              subRuns: [{ dagRunId: 'c-1', dagName: 'child-dag', params: '' }],
            }),
          ],
          Status.Failed
        )
      );
      await renderBody('log', `/dags/${FILE_NAME}?step=fan-out&sub=c-1`);

      expect(propsOf('stepRun').target).toMatchObject({
        subRun: { subDAGRunId: 'c-1' },
      });
      expect(screen.queryByTestId('ww-step-failure')).not.toBeInTheDocument();
    });

    // The text of a failure is in stderr.
    it('opens a failed step on stderr, and a step with a stream as named', async () => {
      setLatest(
        run('run-1', [node('a', NodeStatus.Failed), node('b')], Status.Failed)
      );
      await renderBody('log', `/dags/${FILE_NAME}?step=a`);
      expect(propsOf('stepRun').target).toEqual({
        step: 'a',
        stream: 'stderr',
      });
    });

    it('does not force stderr on a step that did not fail', async () => {
      await renderBody('log', `/dags/${FILE_NAME}?step=a`);
      expect(propsOf('stepRun').target).toEqual({ step: 'a' });
    });

    // A host that shows one step (the editor) names it: the URL does not.
    it('opens the step that the host names, not the one of the URL', async () => {
      renderAt(<Body tab="log" step="b" />, `/dags/${FILE_NAME}?step=a`);
      await vi.waitFor(() =>
        expect(propsOf('stepRun').target).toEqual({ step: 'b' })
      );
    });

    it('says that a step did not run in the selected run', async () => {
      await renderBody('log', `/dags/${FILE_NAME}?step=ghost`);
      expect(
        screen.getByText('This step did not run in the selected run.')
      ).toBeInTheDocument();
      expect(screen.queryByTestId('leaf-stepRun')).not.toBeInTheDocument();
    });

    // The facts of the run are in the run header: the Log tab has no block
    // of them above the scheduler log.
    it('shows no block of run facts above the scheduler log', async () => {
      setLatest(run('run-1', [node('a')], Status.Success, { params: 'A=1' }));
      await renderBody('log');
      expect(screen.queryByTestId('ww-run-summary')).toBeNull();
      expect(screen.queryByTestId('ww-run-params')).toBeNull();
      expect(document.querySelector('.max-h-\\[45\\%\\]')).toBeNull();
      expect(screen.getByTestId('leaf-executionLog')).toBeInTheDocument();
    });

    it('points a foreach step to its items', async () => {
      setLatest(
        run('run-1', [
          node('work', NodeStatus.Success, {
            step: { executorConfig: { type: 'foreach' } },
          }),
        ])
      );
      await renderBody('log', `/dags/${FILE_NAME}?step=work`);
      fireEvent.click(screen.getByRole('button', { name: 'View items' }));
      expect(location.search).toContain('ndv=expanded');
    });

    // The node view is where the items of a foreach step are: its Log tab
    // lists them over the raw log, and a body step log opens in the panel.
    it('lists the items of a foreach step in a step view', async () => {
      setLatest(
        run('run-1', [
          node('work', NodeStatus.Success, {
            step: { executorConfig: { type: 'foreach' } },
          }),
        ])
      );
      await renderBody('log', `/dags/${FILE_NAME}`, 'work');
      expect(screen.getByTestId('leaf-foreach')).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'View items' })).toBeNull();

      act(() =>
        (propsOf('foreach').onOpenLogs as (t: unknown) => void)({
          step: 'work',
          foreach: { item: '1', bodyStep: 'fetch' },
        })
      );
      expect(location.search).toContain('body=fetch');
    });

    it('adds the foreach item and the sub run of the URL', async () => {
      setLatest(run('run-1', [fanOut]));
      await renderBody(
        'log',
        `/dags/${FILE_NAME}?step=fan-out&item=2&body=fetch&sub=c-1`
      );

      expect(propsOf('stepRun').target).toEqual({
        step: 'fan-out',
        foreach: { item: '2', bodyStep: 'fetch' },
        subRun: { dagName: 'child-dag', subDAGRunId: 'c-1' },
      });
    });
  });

  // The steps by run without a step, the runs of the step with one. The
  // runs are the history list of the run timeline.
  describe('History', () => {
    const newer = run('run-1', [node('a'), node('b')], Status.Success, {
      startedAt: '2026-01-15T02:05:00Z',
    });
    const older = run('run-2', [node('a', NodeStatus.Failed)], Status.Failed, {
      startedAt: '2026-01-15T02:01:00Z',
      rootDAGRunName: 'demo',
      rootDAGRunId: 'run-2',
    });
    const query = () => new URLSearchParams(location.search);

    beforeEach(() => {
      setLatest(newer, {
        steps: [{ name: 'a' }, { name: 'b', depends: ['a'] }],
      });
      server.runs['run-2'] = older;
      server.history = { dagRuns: [newer, older], gridData: [] };
    });

    it('shows the steps by run without a step', async () => {
      await renderBody('history');
      const grid = await screen.findByRole('grid', { name: 'Steps by run' });
      expect(
        within(grid)
          .getAllByRole('rowheader')
          .map((header) => header.textContent)
      ).toEqual(['a', 'b']);
      expect(screen.getByTestId('ww-history-cell-a-run-2')).toHaveAttribute(
        'data-status',
        String(NodeStatus.Failed)
      );
      expect(screen.getByTestId('ww-history-cell-b-run-2')).toHaveAttribute(
        'data-state',
        'absent'
      );
    });

    it('selects the run and the step of a cell', async () => {
      await renderBody('history');
      fireEvent.click(await screen.findByTestId('ww-history-cell-a-run-2'));
      await vi.waitFor(() => {
        expect(query().get('run')).toBe('run-2');
        expect(query().get('step')).toBe('a');
      });
    });

    it('selects a run alone from its header and keeps the grid', async () => {
      await renderBody('history');
      fireEvent.click(await screen.findByTestId('ww-history-run-run-2'));
      await vi.waitFor(() => expect(query().get('run')).toBe('run-2'));
      expect(query().get('step')).toBeNull();
      expect(
        screen.getByRole('grid', { name: 'Steps by run' })
      ).toBeInTheDocument();
    });

    it('shows the runs of the step in a step view', async () => {
      await renderBody('history', `/dags/${FILE_NAME}`, 'a');
      const section = await screen.findByRole('region', {
        name: 'History of a',
      });
      expect(
        within(section).getByTestId('ww-step-history-summary')
      ).toHaveTextContent('50% succeeded');
      expect(screen.queryByRole('grid')).toBeNull();
    });

    // A running step counts up: its bar reads the time so far, and that time
    // grows with the clock.
    it('counts up the time of a running step', async () => {
      vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] });
      try {
        vi.setSystemTime(new Date('2026-01-15T02:05:10Z'));
        const running = run(
          'run-1',
          [
            node('a', NodeStatus.Running, {
              startedAt: '2026-01-15T02:05:00Z',
              finishedAt: '-',
            }),
          ],
          Status.Running,
          { startedAt: '2026-01-15T02:05:00Z' }
        );
        setLatest(running, { steps: [{ name: 'a' }] });
        server.history = { dagRuns: [running, older], gridData: [] };
        await renderBody('history', `/dags/${FILE_NAME}`, 'a');
        const bar = await screen.findByTestId('ww-step-history-bar-run-1');
        expect(bar).toHaveAccessibleName(/, 10s$/);

        act(() => {
          vi.advanceTimersByTime(5_000);
        });

        expect(bar).toHaveAccessibleName(/, 15s$/);
      } finally {
        vi.useRealTimers();
      }
    });

    it('selects the run of a bar and keeps the step', async () => {
      await renderBody('history', `/dags/${FILE_NAME}`, 'a');
      fireEvent.click(await screen.findByTestId('ww-step-history-bar-run-2'));
      await vi.waitFor(() => {
        expect(query().get('run')).toBe('run-2');
        expect(query().get('step')).toBe('a');
      });
    });

    it('opens the log of the step in a run on the Log tab', async () => {
      const selectTab = vi.fn();
      renderAt(
        <Body tab="history" step="a" selectTab={selectTab} />,
        `/dags/${FILE_NAME}`
      );
      fireEvent.click(
        await screen.findByRole('button', { name: 'Open the log of run run-2' })
      );
      expect(selectTab).toHaveBeenCalledWith('log');
      await vi.waitFor(() => {
        expect(query().get('run')).toBe('run-2');
        expect(query().get('step')).toBe('a');
      });
    });

    // The canvas lights the node of the row under the pointer, and the
    // light ends when the pointer leaves or the grid goes.
    it('spotlights the step of the row under the pointer on the canvas', async () => {
      const highlightStep = vi.fn();
      const handle = { highlightStep } as unknown as WorkspaceEditorHandle;
      function BindHandle() {
        const { editorHandleRef } = useWorkspaceEditorBinding();
        useEffect(() => editorHandleRef(handle), [editorHandleRef]);
        return null;
      }
      const view = renderAt(
        <WorkspaceEditorProvider>
          <BindHandle />
          <Body tab="history" />
        </WorkspaceEditorProvider>,
        `/dags/${FILE_NAME}`
      );
      const cell = await screen.findByTestId('ww-history-cell-b-run-1');

      fireEvent.pointerEnter(cell.closest('[role="row"]')!);
      expect(highlightStep).toHaveBeenLastCalledWith('b');
      fireEvent.pointerLeave(screen.getByRole('grid'));
      expect(highlightStep).toHaveBeenLastCalledWith(null);

      fireEvent.pointerEnter(cell.closest('[role="row"]')!);
      expect(highlightStep).toHaveBeenLastCalledWith('b');
      view.unmount();
      expect(highlightStep).toHaveBeenLastCalledWith(null);
    });

    // A pick loads another run: while its read is pending, the strip keeps
    // History and its body keeps its state.
    it('keeps the body mounted while the picked run loads', async () => {
      const reply = serve(server);
      let release = () => {};
      const pinned = new Promise<void>((resolve) => {
        release = resolve;
      });
      const readsRun2 = ([path, init]: Parameters<typeof reply>) =>
        path === '/dag-runs/{name}/{dagRunId}' &&
        init?.params?.path?.dagRunId === 'run-2';
      h.GET.mockImplementation(async (...args: Parameters<typeof reply>) => {
        if (readsRun2(args)) await pinned;
        return reply(...args);
      });
      const historyKept = () => {
        expect(screen.getByRole('tab', { name: 'History' })).toHaveAttribute(
          'aria-selected',
          'true'
        );
        expect(
          screen.getByRole('button', { name: 'Failures only' })
        ).toHaveAttribute('aria-pressed', 'true');
      };

      renderDock(`/dags/${FILE_NAME}?dock=history&step=a`);
      fireEvent.click(
        await screen.findByRole('button', { name: 'Failures only' })
      );
      fireEvent.click(screen.getByTestId('ww-step-history-bar-run-2'));

      await vi.waitFor(() => expect(query().get('run')).toBe('run-2'));
      await vi.waitFor(() =>
        expect(
          h.GET.mock.calls.some((call) =>
            readsRun2(call as Parameters<typeof reply>)
          )
        ).toBe(true)
      );
      historyKept();

      await act(async () => release());
      await vi.waitFor(() =>
        expect(screen.getByTestId('ww-step-history-bar-run-2')).toHaveAttribute(
          'aria-selected',
          'true'
        )
      );
      historyKept();
    });
  });

  describe('remount on run change', () => {
    it.each<[DockTab, string]>([
      ['outputs', 'outputs'],
      ['log', 'executionLog'],
    ])('mounts %s again for another run', async (tab, leafName) => {
      await renderBody(tab, `/dags/${FILE_NAME}?run=run-2`);
      expect(leaves.mounts[leafName]).toBe(1);
      expect(propsOf(leafName).dagRunId).toBe('run-2');

      // The latest run has its details at once, so nothing unmounts the
      // body on the way: only its key can mount it again.
      fireEvent.click(screen.getByRole('button', { name: 'select latest' }));

      await vi.waitFor(() => expect(propsOf(leafName).dagRunId).toBe('run-1'));
      expect(leaves.mounts[leafName]).toBe(2);
    });
  });
});

describe('dock tab visibility', () => {
  it('hides Timeline on a run that has not started', async () => {
    setLatest(run('run-1', [node('a')], Status.NotStarted));
    renderDock();
    await screen.findByRole('tab', { name: 'Outputs' });

    expect(tabNames()).not.toContain('Timeline');
  });

  it('counts approvals and human tasks on the Actions badge', async () => {
    setLatest(
      run(
        'run-1',
        [
          node('review', NodeStatus.Waiting, { step: { humanTask: {} } }),
          node('gate1', NodeStatus.Waiting, { step: { approval: {} } }),
          node('gate2', NodeStatus.Waiting, { step: { approval: {} } }),
        ],
        Status.Waiting
      )
    );
    renderDock();

    expect(
      await screen.findByRole('tab', { name: 'Actions, 3 waiting' })
    ).toBeInTheDocument();
  });

  it('shows Agent and Tasks only for an agent run', async () => {
    setLatest(agentRun);
    const view = renderDock();
    expect(
      await screen.findByRole('tab', { name: /^Agent/ })
    ).toBeInTheDocument();
    expect(tabNames()).toContain('Tasks');
    view.unmount();

    setLatest(run('run-1'));
    renderDock();
    await screen.findByRole('tab', { name: 'Outputs' });
    expect(tabNames().some((name) => /^(Agent|Tasks)/.test(name))).toBe(false);
  });

  it('shows Chat only with chat steps', async () => {
    setLatest(
      run('run-1', [
        node('talk', NodeStatus.Success, {
          step: { executorConfig: { type: 'chat' } },
        }),
      ])
    );
    const view = renderDock();
    expect(
      await screen.findByRole('tab', { name: 'Chat' })
    ).toBeInTheDocument();
    view.unmount();

    setLatest(run('run-1'));
    renderDock();
    await screen.findByRole('tab', { name: 'Outputs' });
    expect(tabNames()).not.toContain('Chat');
  });

  // The OUTPUT column of a step view: every tab body reads the run as the
  // step sees it (scopeDetailsToStep), so no tab shows another step.
  describe('with a step', () => {
    const several = run(
      'run-1',
      [
        node('plan', NodeStatus.Success, {
          step: { executorConfig: { type: 'chat' } },
          agentSession: { state: 'succeeded' },
        }),
        node('ask', NodeStatus.Running, {
          step: { executorConfig: { type: 'chat' } },
          agentSession: { state: 'waiting' },
        }),
        node('plain'),
      ],
      Status.Running,
      {
        agentEvents,
        agentTasks: [{ name: 'Plan', status: 'open' }],
        artifactsAvailable: true,
      } as unknown as Partial<ApiDAGRunDetails>
    );
    const nodeNames = (props: Record<string, unknown>, key = 'dagRun') =>
      ((props[key] as ApiDAGRunDetails).nodes ?? []).map((n) => n.step.name);

    it('shows only the bar of the step in the Timeline', async () => {
      setLatest(several);
      await renderBody('timeline', `/dags/${FILE_NAME}`, 'plain');
      expect(screen.getByTestId('timeline-bar-step:plain')).toBeInTheDocument();
      expect(screen.queryByTestId('timeline-bar-step:plan')).toBeNull();
      expect(screen.queryByTestId('timeline-bar-step:ask')).toBeNull();
    });

    it('shows the bar of every step in the Timeline without a step', async () => {
      setLatest(several);
      await renderBody('timeline');
      for (const name of ['plain', 'plan', 'ask']) {
        expect(screen.getByTestId(`timeline-bar-step:${name}`)).toBeVisible();
      }
    });

    it('gives the Agent tab the session of the step and no run level timeline', async () => {
      setLatest(several);
      await renderBody('agent', `/dags/${FILE_NAME}`, 'plan');
      expect(nodeNames(propsOf('agentSession'))).toEqual(['plan']);
      expect(propsOf('agentSession').selectedStep).toBe('plan');
      // The transcript of the step, with no step selector.
      expect(propsOf('chat').stepName).toBe('plan');
      expect(nodeNames(propsOf('chat'))).toEqual(['plan']);
      expect(screen.queryByTestId('agent-run-summary')).toBeNull();
      expect(
        screen.queryByRole('button', { name: /delegate/ })
      ).not.toBeInTheDocument();
    });

    it('gives the Agent tab every session without a step', async () => {
      setLatest(several);
      await renderBody('agent');
      // The tab itself keeps the nodes that have a session.
      expect(nodeNames(propsOf('agentSession'))).toEqual([
        'plan',
        'ask',
        'plain',
      ]);
      expect(screen.getByTestId('agent-run-summary')).toBeInTheDocument();
    });

    it('gives the Chat tab the transcript of the step', async () => {
      setLatest(several);
      await renderBody('chat', `/dags/${FILE_NAME}`, 'ask');
      expect(propsOf('chat').stepName).toBe('ask');
      expect(nodeNames(propsOf('chat'))).toEqual(['ask']);
    });

    it('gives the Chat tab every chat step without a step', async () => {
      setLatest(several);
      await renderBody('chat');
      expect(propsOf('chat').stepName).toBeUndefined();
      expect(nodeNames(propsOf('chat'))).toEqual(['plan', 'ask', 'plain']);
    });

    it('shows no artifacts and no tasks of the run for a step', async () => {
      setLatest(several, { artifacts: { enabled: true } });
      const { unmount } = await renderBody(
        'artifacts',
        `/dags/${FILE_NAME}`,
        'plain'
      );
      expect(screen.queryByTestId('leaf-artifacts')).toBeNull();
      unmount();
      await renderBody('tasks', `/dags/${FILE_NAME}`, 'plain');
      expect(screen.queryByTestId('leaf-tasks')).toBeNull();
    });

    it('shows the artifacts and the tasks of the run without a step', async () => {
      setLatest(several, { artifacts: { enabled: true } });
      const { unmount } = await renderBody('artifacts');
      expect(screen.getByTestId('leaf-artifacts')).toBeInTheDocument();
      unmount();
      await renderBody('tasks');
      expect(propsOf('tasks').tasks).toEqual(several.agentTasks);
    });

    it('does not list the outputs of the run for a step of a sub-run', async () => {
      server.runs['run-2'] = run('run-2', [node('a')], Status.Success, {
        rootDAGRunName: 'demo',
        rootDAGRunId: 'run-1',
      });
      await renderBody('outputs', `/dags/${FILE_NAME}?run=run-2`, 'a');
      expect(
        screen.getByText(
          'The outputs of a step of a sub-run are not available here.'
        )
      ).toBeInTheDocument();
      expect(screen.queryByTestId('leaf-outputs')).toBeNull();
    });
  });

  it.each<
    [string, Record<string, unknown>, Partial<ApiDAGRunDetails>, boolean]
  >([
    ['enabled on the workflow', { artifacts: { enabled: true } }, {}, true],
    ['available on the run', {}, { artifactsAvailable: true }, true],
    ['neither', {}, {}, false],
  ])('shows Artifacts when %s', async (_, dag, extra, shown) => {
    setLatest(run('run-1', [node('a')], Status.Success, extra), dag);
    renderDock();
    await screen.findByRole('tab', { name: 'Outputs' });

    expect(tabNames().includes('Artifacts')).toBe(shown);
  });

  it('shows the empty state with no request for a workflow that never ran', () => {
    setLatest(zeroRun());
    renderDock(`/dags/${FILE_NAME}?dock=outputs`);

    expect(tabNames()).toEqual(['Log']);
    expect(
      screen.getByText('This workflow has not run yet.')
    ).toBeInTheDocument();
    expect(screen.queryByTestId(/^leaf-/)).not.toBeInTheDocument();
    // Only the history of the timeline is read: no run, no run request.
    expect(h.GET.mock.calls.map((call) => call[0])).not.toContain(
      '/dag-runs/{name}/{dagRunId}'
    );
  });
});
