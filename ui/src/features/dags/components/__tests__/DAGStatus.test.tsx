// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  AgentSessionState,
  components,
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
  Stream,
} from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useClient } from '@/hooks/api';
import { toMermaidNodeId } from '@/lib/utils';
import { DAGContext } from '../../contexts/DAGContext';
import DAGStatus from '../DAGStatus';

const patchMock = vi.hoisted(() => vi.fn());
const approvalTabMock = vi.hoisted(() => vi.fn());
const humanTasksTabMock = vi.hoisted(() => vi.fn());
const nodeStatusTableMock = vi.hoisted(() => vi.fn());
const logViewerMock = vi.hoisted(() => vi.fn());

vi.mock('@/hooks/api', () => ({
  useClient: vi.fn(),
}));

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({
    permissions: {
      runDags: true,
    },
  }),
}));

vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({
    showError: vi.fn(),
  }),
}));

vi.mock('react-cookie', () => ({
  useCookies: () => [{}, vi.fn()],
}));

vi.mock('../visualization', () => ({
  Graph: ({
    onClickNode,
    onRightClickNode,
    steps,
  }: {
    onClickNode?: (id: string) => void;
    onRightClickNode?: (id: string) => void;
    steps?: components['schemas']['Node'][];
  }) => (
    <div>
      <div>Graph status: {steps?.[0]?.status}</div>
      <button
        type="button"
        onClick={() => onClickNode?.(toMermaidNodeId('step'))}
      >
        Open step details
      </button>
      <button
        type="button"
        onClick={() => onRightClickNode?.(toMermaidNodeId('step'))}
      >
        Open status modal
      </button>
    </div>
  ),
  TimelineChart: () => <div>Timeline</div>,
}));

vi.mock('../dag-execution', () => ({
  LogViewer: (props: unknown) => {
    logViewerMock(props);
    return null;
  },
  ParallelExecutionModal: () => null,
  StatusUpdateModal: ({
    visible,
    step,
    onSubmit,
  }: {
    visible: boolean;
    step?: components['schemas']['Step'];
    onSubmit: (
      step: components['schemas']['Step'],
      status: NodeStatus
    ) => void | Promise<void>;
  }) =>
    visible && step ? (
      <button
        type="button"
        onClick={() => void onSubmit(step, NodeStatus.Failed)}
      >
        Mark failed
      </button>
    ) : null,
}));

vi.mock('../dag-details', () => ({
  DAGStatusOverview: () => <div>Status overview</div>,
  NodeStatusTable: (props: unknown) => {
    nodeStatusTableMock(props);
    return <div>Node status table</div>;
  },
}));

vi.mock('../approval', () => ({
  ApprovalTab: (props: unknown) => {
    approvalTabMock(props);
    return null;
  },
}));

vi.mock('../human-task', () => ({
  HumanTasksTab: (props: {
    dagRun: components['schemas']['DAGRunDetails'];
  }) => {
    humanTasksTabMock(props);
    return (
      <div>
        <div>Human task panel</div>
        <label>
          Human task draft
          <input />
        </label>
      </div>
    );
  },
}));

vi.mock('../artifacts/ArtifactsTab', () => ({
  default: () => null,
}));

vi.mock('../chat-history', () => ({
  ChatHistoryTab: () => null,
}));

vi.mock('../dag-editor', () => ({
  DAGSpecReadOnly: () => null,
}));

vi.mock('../../../dag-runs/components/dag-run-details', () => ({
  DAGRunOutputs: () => <div>Outputs panel</div>,
}));

const appBarValue = {
  title: 'DAGs',
  setTitle: vi.fn(),
  remoteNodes: ['local'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'local',
  selectRemoteNode: vi.fn(),
};

const dagRun = {
  name: 'example',
  dagRunId: 'run-1',
  status: Status.Failed,
  statusLabel: StatusLabel.failed,
  autoRetryCount: 0,
  startedAt: '',
  finishedAt: '',
  artifactsAvailable: false,
  nodes: [
    {
      step: {
        name: 'step',
      },
      status: NodeStatus.Success,
      statusLabel: NodeStatusLabel.succeeded,
    },
  ],
} as components['schemas']['DAGRunDetails'];

function waitingHumanTaskRun(
  dagRunId: string,
  name = dagRun.name
): components['schemas']['DAGRunDetails'] {
  return {
    ...dagRun,
    name,
    dagRunId,
    status: Status.Waiting,
    statusLabel: StatusLabel.waiting,
    nodes: [
      {
        step: {
          id: 'review',
          name: 'step',
          humanTask: { prompt: 'Confirm deployment' },
        },
        status: NodeStatus.Waiting,
        statusLabel: NodeStatusLabel.waiting,
      },
    ],
  } as components['schemas']['DAGRunDetails'];
}

function agentSessionRun(): components['schemas']['DAGRunDetails'] {
  return {
    ...dagRun,
    status: Status.Running,
    statusLabel: StatusLabel.running,
    nodes: [
      {
        step: { name: 'analyze' },
        status: NodeStatus.Success,
        statusLabel: NodeStatusLabel.succeeded,
        agentSession: {
          provider: 'opencode',
          state: AgentSessionState.succeeded,
          events: [],
        },
      },
      {
        step: { name: 'implement' },
        status: NodeStatus.Running,
        statusLabel: NodeStatusLabel.running,
        agentSession: {
          provider: 'opencode',
          state: AgentSessionState.running,
          events: [],
        },
      },
    ],
  } as unknown as components['schemas']['DAGRunDetails'];
}

function dagStatusView(
  selectedRun: components['schemas']['DAGRunDetails'],
  selectedRemoteNode = 'local'
): React.JSX.Element {
  return (
    <MemoryRouter>
      <AppBarContext.Provider value={{ ...appBarValue, selectedRemoteNode }}>
        <DAGContext.Provider
          value={{
            refresh: vi.fn(),
            name: selectedRun.name,
            fileName: 'example.yaml',
          }}
        >
          <DAGStatus dagRun={selectedRun} fileName="example.yaml" />
        </DAGContext.Provider>
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

beforeEach(() => {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('DAGStatus', () => {
  it('surfaces the failed step and error before the graph details', () => {
    const failedRun = {
      ...dagRun,
      nodes: [
        {
          ...dagRun.nodes[0],
          status: NodeStatus.Failed,
          statusLabel: NodeStatusLabel.failed,
          error: 'connection refused',
        },
      ],
    } as components['schemas']['DAGRunDetails'];

    render(dagStatusView(failedRun));

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent('Failed at step');
    expect(alert).toHaveTextContent('connection refused');
    expect(
      within(alert).getByRole('button', { name: 'View stderr' })
    ).toBeInTheDocument();
    expect(
      within(alert).getByRole('button', { name: 'Inspect step' })
    ).toBeInTheDocument();
  });

  it('surfaces a rejected step and its rejection reason', () => {
    const rejectedRun = {
      ...dagRun,
      nodes: [
        {
          ...dagRun.nodes[0],
          status: NodeStatus.Rejected,
          statusLabel: NodeStatusLabel.rejected,
          rejectionReason: 'approval was denied',
        },
      ],
    } as components['schemas']['DAGRunDetails'];

    render(dagStatusView(rejectedRun));

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent('Rejected at step');
    expect(alert).toHaveTextContent('approval was denied');
    expect(
      within(alert).getByRole('button', { name: 'View stderr' })
    ).toBeInTheDocument();
    expect(
      within(alert).getByRole('button', { name: 'Inspect step' })
    ).toBeInTheDocument();
  });

  it('passes its workflow filename to the step table', () => {
    vi.mocked(useClient).mockReturnValue({
      PATCH: patchMock,
    } as unknown as ReturnType<typeof useClient>);

    render(
      <MemoryRouter>
        <AppBarContext.Provider value={appBarValue}>
          <DAGStatus dagRun={dagRun} fileName="example.yaml" />
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    expect(nodeStatusTableMock).toHaveBeenCalledWith(
      expect.objectContaining({
        fileName: 'example.yaml',
      })
    );
  });

  it('opens step details from a status graph click', async () => {
    vi.mocked(useClient).mockReturnValue({
      PATCH: patchMock,
    } as unknown as ReturnType<typeof useClient>);

    render(
      <MemoryRouter>
        <AppBarContext.Provider value={appBarValue}>
          <DAGContext.Provider
            value={{
              refresh: vi.fn(),
              name: 'example',
              fileName: 'example.yaml',
            }}
          >
            <DAGStatus dagRun={dagRun} fileName="example.yaml" />
          </DAGContext.Provider>
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    fireEvent.click(screen.getByRole('button', { name: 'Open step details' }));

    expect(await screen.findByRole('dialog', { name: 'step' })).toBeVisible();
    expect(
      screen.queryByRole('button', { name: 'Mark failed' })
    ).not.toBeInTheDocument();
  });

  it('shows the failure and opens stderr from the step drawer', async () => {
    const failedRun = {
      ...dagRun,
      nodes: [
        {
          ...dagRun.nodes[0],
          status: NodeStatus.Failed,
          statusLabel: NodeStatusLabel.failed,
          error: 'connection refused',
          startedAt: '2026-08-08T10:00:00Z',
          finishedAt: '2026-08-08T10:00:12Z',
        },
      ],
    } as components['schemas']['DAGRunDetails'];

    render(dagStatusView(failedRun));

    fireEvent.click(screen.getByRole('button', { name: 'Open step details' }));

    const drawer = await screen.findByRole('dialog', { name: 'step' });
    expect(drawer).toHaveTextContent('connection refused');
    expect(drawer).toHaveTextContent('failed');
    expect(drawer).toHaveTextContent('Duration');

    fireEvent.click(
      within(drawer).getByRole('button', { name: 'View stderr' })
    );

    await waitFor(() => {
      expect(logViewerMock).toHaveBeenCalledWith(
        expect.objectContaining({
          isOpen: true,
          stepName: 'step',
          stream: Stream.stderr,
        })
      );
    });
    // The drawer removal waits for its exit animation.
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: 'step' })
      ).not.toBeInTheDocument();
    });
  });

  it('keeps the open drawer in sync with live node updates', async () => {
    const failedRun = {
      ...dagRun,
      nodes: [
        {
          ...dagRun.nodes[0],
          status: NodeStatus.Running,
          statusLabel: NodeStatusLabel.running,
        },
      ],
    } as components['schemas']['DAGRunDetails'];

    const { rerender } = render(dagStatusView(failedRun));

    fireEvent.click(screen.getByRole('button', { name: 'Open step details' }));
    const drawer = await screen.findByRole('dialog', { name: 'step' });
    expect(drawer).toHaveTextContent('running');

    rerender(
      dagStatusView({
        ...failedRun,
        nodes: [
          {
            ...failedRun.nodes[0],
            status: NodeStatus.Failed,
            statusLabel: NodeStatusLabel.failed,
            error: 'exit status 1',
          },
        ],
      } as components['schemas']['DAGRunDetails'])
    );

    await waitFor(() => {
      expect(screen.getByRole('dialog', { name: 'step' })).toHaveTextContent(
        'exit status 1'
      );
    });
  });

  it('updates the rendered graph immediately after graph status updates succeed', async () => {
    const refresh = vi.fn();
    vi.mocked(useClient).mockReturnValue({
      PATCH: patchMock.mockResolvedValue({ error: undefined }),
    } as unknown as ReturnType<typeof useClient>);

    render(
      <MemoryRouter>
        <AppBarContext.Provider value={appBarValue}>
          <DAGContext.Provider
            value={{
              refresh,
              name: 'example',
              fileName: 'example.yaml',
            }}
          >
            <DAGStatus dagRun={dagRun} fileName="example.yaml" />
          </DAGContext.Provider>
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    fireEvent.click(screen.getByRole('button', { name: 'Open status modal' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Mark failed' }));

    await waitFor(() => {
      expect(patchMock).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/steps/{stepName}/status',
        expect.objectContaining({
          body: {
            status: NodeStatus.Failed,
          },
        })
      );
    });
    expect(
      await screen.findByText(`Graph status: ${NodeStatus.Failed}`)
    ).toBeInTheDocument();
    await waitFor(() => expect(refresh).toHaveBeenCalledTimes(1));
  });

  it('does not optimistically update the graph when graph status updates fail', async () => {
    const refresh = vi.fn();
    vi.mocked(useClient).mockReturnValue({
      PATCH: patchMock.mockResolvedValue({
        error: { message: 'update failed' },
      }),
    } as unknown as ReturnType<typeof useClient>);

    render(
      <MemoryRouter>
        <AppBarContext.Provider value={appBarValue}>
          <DAGContext.Provider
            value={{
              refresh,
              name: 'example',
              fileName: 'example.yaml',
            }}
          >
            <DAGStatus dagRun={dagRun} fileName="example.yaml" />
          </DAGContext.Provider>
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    fireEvent.click(screen.getByRole('button', { name: 'Open status modal' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Mark failed' }));

    await waitFor(() => {
      expect(patchMock).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/steps/{stepName}/status',
        expect.objectContaining({
          body: {
            status: NodeStatus.Failed,
          },
        })
      );
    });
    expect(
      screen.queryByText(`Graph status: ${NodeStatus.Failed}`)
    ).not.toBeInTheDocument();
    expect(
      screen.getByText(`Graph status: ${NodeStatus.Success}`)
    ).toBeInTheDocument();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('passes the DAG run name to the approval tab when fileName differs', () => {
    vi.mocked(useClient).mockReturnValue({
      PATCH: patchMock,
    } as unknown as ReturnType<typeof useClient>);

    const waitingDagRun = {
      ...dagRun,
      name: 'test_name',
      nodes: [
        {
          step: {
            name: 'wait-step',
            approval: {
              prompt: 'Approve this step',
            },
          },
          status: NodeStatus.Waiting,
          statusLabel: NodeStatusLabel.waiting,
        },
      ],
    } as components['schemas']['DAGRunDetails'];

    render(
      <MemoryRouter>
        <AppBarContext.Provider value={appBarValue}>
          <DAGContext.Provider
            value={{
              refresh: vi.fn(),
              name: 'test_name',
              fileName: 'approvaltest',
            }}
          >
            <DAGStatus
              dagRun={waitingDagRun}
              fileName="approvaltest"
              initialTab="approval"
            />
          </DAGContext.Provider>
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    expect(approvalTabMock).toHaveBeenCalledWith(
      expect.objectContaining({
        dagName: 'test_name',
      })
    );
  });

  it('routes human tasks to their own tab and disables graph status mutation', async () => {
    vi.mocked(useClient).mockReturnValue({
      PATCH: patchMock,
    } as unknown as ReturnType<typeof useClient>);
    const humanTaskDagRun = waitingHumanTaskRun('run-1');

    render(dagStatusView(humanTaskDagRun));

    expect(await screen.findByText('Human task panel')).toBeVisible();
    expect(humanTasksTabMock).toHaveBeenCalled();
    expect(
      screen.queryByRole('button', { name: /Approval/ })
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Status' }));
    fireEvent.click(screen.getByRole('button', { name: 'Open status modal' }));
    expect(
      screen.queryByRole('button', { name: 'Mark failed' })
    ).not.toBeInTheDocument();
    expect(patchMock).not.toHaveBeenCalled();
  });

  it('keeps the selected agent conversation when switching status tabs', () => {
    vi.mocked(useClient).mockReturnValue({
      PATCH: patchMock,
      POST: vi.fn(),
    } as unknown as ReturnType<typeof useClient>);
    render(dagStatusView(agentSessionRun()));

    fireEvent.click(screen.getByRole('button', { name: 'Agent' }));
    const analyzeTab = screen.getByRole('tab', { name: /analyze/ });
    fireEvent.click(analyzeTab);
    expect(analyzeTab).toHaveAttribute('aria-selected', 'true');

    fireEvent.click(screen.getByRole('button', { name: 'Status' }));
    fireEvent.click(screen.getByRole('button', { name: 'Agent' }));

    expect(screen.getByRole('tab', { name: /analyze/ })).toHaveAttribute(
      'aria-selected',
      'true'
    );
  });

  it('resets the selected agent conversation when the DAG run changes', () => {
    vi.mocked(useClient).mockReturnValue({
      PATCH: patchMock,
      POST: vi.fn(),
    } as unknown as ReturnType<typeof useClient>);
    const { rerender } = render(dagStatusView(agentSessionRun()));

    fireEvent.click(screen.getByRole('button', { name: 'Agent' }));
    fireEvent.click(screen.getByRole('tab', { name: /analyze/ }));

    rerender(
      dagStatusView({
        ...agentSessionRun(),
        dagRunId: 'run-2',
      })
    );
    fireEvent.click(screen.getByRole('button', { name: 'Agent' }));

    expect(screen.getByRole('tab', { name: /implement/ })).toHaveAttribute(
      'aria-selected',
      'true'
    );
  });

  it('disables graph status mutation while waiting for approval', () => {
    vi.mocked(useClient).mockReturnValue({
      PATCH: patchMock,
    } as unknown as ReturnType<typeof useClient>);
    const waitingApprovalRun = {
      ...dagRun,
      status: Status.Waiting,
      statusLabel: StatusLabel.waiting,
      nodes: [
        {
          step: {
            name: 'step',
            approval: { prompt: 'Approve deployment' },
          },
          status: NodeStatus.Waiting,
          statusLabel: NodeStatusLabel.waiting,
        },
      ],
    } as components['schemas']['DAGRunDetails'];

    render(dagStatusView(waitingApprovalRun));

    fireEvent.click(screen.getByRole('button', { name: 'Status' }));
    fireEvent.click(screen.getByRole('button', { name: 'Open status modal' }));
    expect(
      screen.queryByRole('button', { name: 'Mark failed' })
    ).not.toBeInTheDocument();
    expect(patchMock).not.toHaveBeenCalled();
  });

  it('selects human tasks after switching between waiting DAG runs', async () => {
    vi.mocked(useClient).mockReturnValue({
      PATCH: patchMock,
    } as unknown as ReturnType<typeof useClient>);
    const { rerender } = render(dagStatusView(waitingHumanTaskRun('run-1')));

    expect(await screen.findByText('Human task panel')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Status' }));
    expect(screen.queryByText('Human task panel')).not.toBeInTheDocument();

    rerender(dagStatusView(waitingHumanTaskRun('run-2')));

    expect(await screen.findByText('Human task panel')).toBeVisible();
  });

  it('keeps the selected tab when polling discovers a human task', async () => {
    vi.mocked(useClient).mockReturnValue({
      PATCH: patchMock,
    } as unknown as ReturnType<typeof useClient>);
    const { rerender } = render(dagStatusView(dagRun));

    fireEvent.click(screen.getByRole('button', { name: 'Outputs' }));
    expect(screen.getByText('Outputs panel')).toBeVisible();

    rerender(dagStatusView(waitingHumanTaskRun(dagRun.dagRunId)));

    expect(
      await screen.findByRole('button', { name: 'Human tasks' })
    ).toBeVisible();
    expect(screen.getByText('Outputs panel')).toBeVisible();
    expect(screen.queryByText('Human task panel')).not.toBeInTheDocument();
  });

  it.each([
    {
      dimension: 'DAG name',
      firstRun: waitingHumanTaskRun('shared-run', 'deploy-a'),
      firstRemoteNode: 'local',
      secondRun: waitingHumanTaskRun('shared-run', 'deploy-b'),
      secondRemoteNode: 'local',
    },
    {
      dimension: 'remote node',
      firstRun: waitingHumanTaskRun('shared-run'),
      firstRemoteNode: 'edge-a',
      secondRun: waitingHumanTaskRun('shared-run'),
      secondRemoteNode: 'edge-b',
    },
  ])(
    'isolates human-task state when the $dimension changes',
    async ({ firstRun, firstRemoteNode, secondRun, secondRemoteNode }) => {
      vi.mocked(useClient).mockReturnValue({
        PATCH: patchMock,
      } as unknown as ReturnType<typeof useClient>);
      const { rerender } = render(dagStatusView(firstRun, firstRemoteNode));

      const draft = await screen.findByLabelText('Human task draft');
      fireEvent.change(draft, { target: { value: 'first run input' } });
      expect(draft).toHaveValue('first run input');

      rerender(dagStatusView(secondRun, secondRemoteNode));

      expect(await screen.findByLabelText('Human task draft')).toHaveValue('');
    }
  );

  // Agent steps recorded before the rename carry the 'controller' executor
  // type, and their LLM transcript still belongs on the Chat tab.
  it('offers the chat transcript for a legacy controller step', () => {
    const legacyRun = {
      ...dagRun,
      nodes: [
        {
          step: {
            name: '__controller__',
            executorConfig: { type: 'controller' },
          },
          status: NodeStatus.Success,
          statusLabel: NodeStatusLabel.succeeded,
        },
      ],
    } as unknown as components['schemas']['DAGRunDetails'];

    render(dagStatusView(legacyRun));

    expect(screen.getByRole('button', { name: 'Chat' })).toBeVisible();
  });
});
