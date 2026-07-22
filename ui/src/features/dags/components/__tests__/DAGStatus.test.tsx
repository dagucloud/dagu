// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  components,
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useClient } from '@/hooks/api';
import { toMermaidNodeId } from '@/lib/utils';
import { DAGContext } from '../../contexts/DAGContext';
import DAGStatus from '../DAGStatus';

const patchMock = vi.hoisted(() => vi.fn());
const approvalTabMock = vi.hoisted(() => vi.fn());
const humanTasksTabMock = vi.hoisted(() => vi.fn());

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
  LogViewer: () => null,
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
  NodeStatusTable: () => <div>Node status table</div>,
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
  DAGRunOutputs: () => null,
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

afterEach(() => {
  vi.clearAllMocks();
});

describe('DAGStatus', () => {
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
      screen.getByText(`Graph status: ${NodeStatus.Failed}`)
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
});
