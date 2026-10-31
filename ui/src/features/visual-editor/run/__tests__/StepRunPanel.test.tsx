// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  NodeStatus,
  NodeStatusLabel,
  ReusedInputVia,
  Status,
  type components,
} from '@/api/v1/schema';
import { stepOutputsSample } from '../../testing/fixtures';
import { StepRunPanel } from '../StepRunPanel';

type Node = components['schemas']['Node'];

const { useQueryMock, useStepOutputsMock, drawerMock, logViewerMock } =
  vi.hoisted(() => ({
    useQueryMock: vi.fn(),
    useStepOutputsMock: vi.fn(),
    drawerMock: vi.fn(),
    logViewerMock: vi.fn(),
  }));

vi.mock('@/hooks/api', () => ({
  useQuery: useQueryMock,
  useClient: vi.fn(),
}));
vi.mock('../../api/useStepOutputs', () => ({
  useStepOutputs: useStepOutputsMock,
}));
// The drawer and the log viewer are heavy; only their props matter here.
vi.mock('@/features/dags/components/step-details', () => ({
  StepDetailsDrawer: (props: Record<string, unknown>) => {
    drawerMock(props);
    return null;
  },
}));
vi.mock('@/features/dags/components/dag-execution/LogViewer', () => ({
  default: (props: Record<string, unknown>) => {
    logViewerMock(props);
    return null;
  },
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'node-1',
}));

function makeNode(name: string, extra: Partial<Node> = {}): Node {
  return {
    step: { name },
    stdout: '',
    stderr: '',
    startedAt: '-',
    finishedAt: '-',
    status: NodeStatus.NotStarted,
    statusLabel: NodeStatusLabel.not_started,
    retryCount: 0,
    doneCount: 0,
    ...extra,
  } as Node;
}

function setRun(
  nodes: Node[] | undefined,
  extra: object = {},
  details: object = {}
) {
  useQueryMock.mockReturnValue({
    data: nodes && {
      dagRunDetails: { name: 'dag', dagRunId: 'r1', nodes, ...details },
    },
    error: undefined,
    isLoading: false,
    ...extra,
  });
}

const failed = makeNode('fetch', {
  status: NodeStatus.Failed,
  statusLabel: NodeStatusLabel.failed,
  startedAt: '2026-01-15T02:00:00Z',
  finishedAt: '2026-01-15T02:01:05Z',
  error: 'exit status 22',
});

describe('StepRunPanel', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  beforeEach(() => {
    useQueryMock.mockReset();
    useStepOutputsMock.mockReset();
    drawerMock.mockReset();
    logViewerMock.mockReset();
    useStepOutputsMock.mockReturnValue({ data: undefined, error: undefined });
    setRun([failed]);
  });

  it('shows status, times and error of a failed step', () => {
    render(<StepRunPanel dagName="dag" runId="r1" stepName="fetch" />);
    expect(screen.getByText('Failed')).toBeTruthy();
    expect(screen.getByText('Started')).toBeTruthy();
    expect(screen.getAllByText(/^2026-01-1[45] \d\d:\d\d:\d\d$/)).toHaveLength(
      2
    );
    expect(screen.getByText('1m 5s')).toBeTruthy();
    expect(screen.getByText('exit status 22')).toBeTruthy();
  });

  it('reads the run with the remote node', () => {
    render(<StepRunPanel dagName="dag" runId="latest" stepName="fetch" />);
    expect(useQueryMock).toHaveBeenCalledWith(
      '/dag-runs/{name}/{dagRunId}',
      {
        params: {
          path: { name: 'dag', dagRunId: 'latest' },
          query: { remoteNode: 'node-1' },
        },
      },
      expect.anything()
    );
  });

  it('polls the run only while it is active', () => {
    render(<StepRunPanel dagName="dag" runId="latest" stepName="fetch" />);
    const options = useQueryMock.mock.calls[0]?.[2] as {
      refreshInterval: (latest?: unknown) => number;
    };
    const running = { dagRunDetails: { status: Status.Running } };
    const finished = { dagRunDetails: { status: Status.Success } };
    expect(options.refreshInterval(running)).toBeGreaterThan(0);
    expect(options.refreshInterval(finished)).toBe(0);
    expect(options.refreshInterval(undefined)).toBe(0);
  });

  it('makes no request without a run', () => {
    render(<StepRunPanel dagName="dag" runId={null} stepName="fetch" />);
    expect(useQueryMock).toHaveBeenCalledWith(
      '/dag-runs/{name}/{dagRunId}',
      null,
      expect.anything()
    );
    expect(useQueryMock).toHaveBeenCalledTimes(1);
    expect(screen.getByText('No run selected')).toBeTruthy();
  });

  it('shows a subtle loading text while the run loads', () => {
    setRun(undefined, { isLoading: true });
    render(<StepRunPanel dagName="dag" runId="r1" stepName="fetch" />);
    expect(screen.getByText('Loading run...')).toBeTruthy();
  });

  it('lists outputs and marks a truncated value', () => {
    useStepOutputsMock.mockReturnValue({
      data: stepOutputsSample,
      error: undefined,
    });
    render(<StepRunPanel dagName="dag" runId="r1" stepName="fetch" />);
    expect(useStepOutputsMock).toHaveBeenCalledWith('dag', 'r1', 'fetch');
    expect(screen.getByText('status')).toBeTruthy();
    expect(screen.getByText('200')).toBeTruthy();
    expect(screen.getByText('body')).toBeTruthy();
    // A JSON value shows prettified, so a key has its own line.
    expect(screen.getAllByText('"name"').length).toBe(2);
    // Only the cut value carries the marker.
    expect(screen.getAllByText(/^truncated \(/)).toHaveLength(1);
    expect(screen.getByText('truncated (128.0 KiB)')).toBeTruthy();
  });

  it('shows the output variable', () => {
    useStepOutputsMock.mockReturnValue({
      data: {
        ...stepOutputsSample,
        outputs: [],
        outputVariable: {
          name: 'RESULT',
          value: 'ok',
          truncated: false,
          size: 2,
        },
      },
      error: undefined,
    });
    render(<StepRunPanel dagName="dag" runId="r1" stepName="fetch" />);
    expect(screen.getByText('Output variable: RESULT')).toBeTruthy();
    expect(screen.getByText('ok')).toBeTruthy();
  });

  it('says so when no output was collected', () => {
    useStepOutputsMock.mockReturnValue({
      data: { ...stepOutputsSample, outputs: [] },
      error: undefined,
    });
    render(<StepRunPanel dagName="dag" runId="r1" stepName="fetch" />);
    expect(screen.getByText('No outputs collected')).toBeTruthy();
  });

  it('keeps the rest visible when the outputs request fails', () => {
    useStepOutputsMock.mockReturnValue({
      data: undefined,
      error: new Error('boom'),
    });
    render(<StepRunPanel dagName="dag" runId="r1" stepName="fetch" />);
    expect(screen.getByText('Failed to load data')).toBeTruthy();
    expect(screen.getByText('exit status 22')).toBeTruthy();
  });

  it('says an absent step did not run, without asking for outputs', () => {
    setRun([makeNode('other')]);
    render(<StepRunPanel dagName="dag" runId="r1" stepName="fetch" />);
    expect(screen.getByText('Not run in this run')).toBeTruthy();
    expect(useStepOutputsMock).toHaveBeenCalledWith('dag', 'r1', null);
  });

  it('says a plain skipped step did not run', () => {
    setRun([
      makeNode('fetch', {
        status: NodeStatus.Skipped,
        statusLabel: NodeStatusLabel.skipped,
      }),
    ]);
    render(<StepRunPanel dagName="dag" runId="r1" stepName="fetch" />);
    expect(screen.getByText('Not run in this run')).toBeTruthy();
    expect(screen.queryByText('Reused from an earlier run')).toBeNull();
  });

  // The runner gives a step whose precondition failed a finish time too.
  it('says a skipped step that the run does not carry did not run', () => {
    setRun([
      makeNode('fetch', {
        status: NodeStatus.Skipped,
        statusLabel: NodeStatusLabel.skipped,
        finishedAt: '2026-01-15T02:00:03Z',
      }),
    ]);
    render(<StepRunPanel dagName="dag" runId="r1" stepName="fetch" />);
    expect(screen.getByText('Not run in this run')).toBeTruthy();
    expect(screen.queryByText('Reused from an earlier run')).toBeNull();
    expect(useStepOutputsMock).toHaveBeenCalledWith('dag', 'r1', null);
  });

  it('labels a seeded skipped step as reused and shows its outputs', () => {
    setRun(
      [
        makeNode('fetch', {
          status: NodeStatus.Skipped,
          statusLabel: NodeStatusLabel.skipped,
          startedAt: '2026-01-15T02:00:00Z',
          finishedAt: '2026-01-15T02:00:03Z',
          doneCount: 1,
        }),
      ],
      {},
      {
        reusedInputs: [{ step: 'fetch', runId: 'r0', via: ReusedInputVia.run }],
      }
    );
    useStepOutputsMock.mockReturnValue({
      data: stepOutputsSample,
      error: undefined,
    });
    render(<StepRunPanel dagName="dag" runId="r1" stepName="fetch" />);
    expect(screen.getByText('Reused from an earlier run')).toBeTruthy();
    expect(screen.queryByText('Not run in this run')).toBeNull();
    expect(screen.getByText('200')).toBeTruthy();
    expect(useStepOutputsMock).toHaveBeenCalledWith('dag', 'r1', 'fetch');
  });

  // The run that the step view follows is on its way: the step waits its
  // turn, then runs, and its outputs come when it has finished.
  describe('while the run goes on', () => {
    function setActiveRun(nodes: Node[]) {
      useQueryMock.mockReturnValue({
        data: {
          dagRunDetails: {
            name: 'dag',
            dagRunId: 'r1',
            status: Status.Running,
            nodes,
          },
        },
        error: undefined,
        isLoading: false,
      });
    }

    it('shows a loader for a step that has not started', () => {
      setActiveRun([makeNode('fetch')]);
      render(<StepRunPanel dagName="dag" runId="r1" stepName="fetch" />);
      expect(screen.getByTestId('step-run-waiting')).toBeTruthy();
      expect(screen.queryByText('Not run in this run')).toBeNull();
    });

    it('shows a loader for a step that runs and asks for no output yet', () => {
      // The status chip of a running step reads the motion preference.
      vi.stubGlobal('matchMedia', () => ({
        matches: false,
        addEventListener: () => undefined,
        removeEventListener: () => undefined,
      }));
      setActiveRun([
        makeNode('fetch', {
          status: NodeStatus.Running,
          statusLabel: NodeStatusLabel.running,
          startedAt: '2026-01-15T02:00:00Z',
        }),
      ]);
      render(<StepRunPanel dagName="dag" runId="r1" stepName="fetch" />);
      expect(screen.getByTestId('step-run-running')).toBeTruthy();
      expect(useStepOutputsMock).toHaveBeenLastCalledWith('dag', 'r1', null);
    });

    // The outputs of a human task come when it completes, and the request
    // key stays the same: an early read would stay empty.
    it('asks for no output while the step waits', () => {
      setActiveRun([
        makeNode('fetch', {
          status: NodeStatus.Waiting,
          statusLabel: NodeStatusLabel.waiting,
          startedAt: '2026-01-15T02:00:00Z',
        }),
      ]);
      render(<StepRunPanel dagName="dag" runId="r1" stepName="fetch" />);
      expect(screen.getByText('Waiting')).toBeTruthy();
      expect(useStepOutputsMock).toHaveBeenLastCalledWith('dag', 'r1', null);
    });

    it('shows the outputs once the step has finished', () => {
      setActiveRun([
        makeNode('fetch', {
          status: NodeStatus.Success,
          statusLabel: NodeStatusLabel.succeeded,
          startedAt: '2026-01-15T02:00:00Z',
          finishedAt: '2026-01-15T02:00:03Z',
          doneCount: 1,
        }),
      ]);
      useStepOutputsMock.mockReturnValue({
        data: stepOutputsSample,
        error: undefined,
      });
      render(<StepRunPanel dagName="dag" runId="r1" stepName="fetch" />);
      expect(screen.queryByTestId('step-run-running')).toBeNull();
      expect(screen.getByText('200')).toBeTruthy();
      expect(useStepOutputsMock).toHaveBeenLastCalledWith('dag', 'r1', 'fetch');
    });
  });

  it('does not claim the step is absent when the run fails to load', () => {
    setRun(undefined, { error: new Error('404') });
    render(<StepRunPanel dagName="dag" runId="r1" stepName="fetch" />);
    expect(screen.getByText('Failed to load DAG run details')).toBeTruthy();
    expect(screen.queryByText('Not run in this run')).toBeNull();
  });

  it('opens the step drawer from the logs button', () => {
    render(<StepRunPanel dagName="dag" runId="r1" stepName="fetch" />);
    const last = () => drawerMock.mock.lastCall?.[0];
    expect(last()).toMatchObject({ isOpen: false });

    fireEvent.click(screen.getByRole('button', { name: /View logs.*fetch/ }));
    expect(last()).toMatchObject({
      dagName: 'dag',
      isOpen: true,
      step: failed.step,
      node: failed,
    });
    expect(last().dagRun).toMatchObject({ dagRunId: 'r1' });

    act(() => last().onClose());
    expect(last()).toMatchObject({ isOpen: false });
  });

  it('opens the log viewer for the stream the drawer asks for', () => {
    render(<StepRunPanel dagName="dag" runId="r1" stepName="fetch" />);
    expect(logViewerMock.mock.lastCall?.[0]).toMatchObject({ isOpen: false });

    fireEvent.click(screen.getByRole('button', { name: /View logs.*fetch/ }));
    act(() => drawerMock.mock.lastCall?.[0].onViewLog(failed, 'stderr'));
    expect(logViewerMock.mock.lastCall?.[0]).toMatchObject({
      isOpen: true,
      logType: 'step',
      dagName: 'dag',
      dagRunId: 'r1',
      stepName: 'fetch',
      stream: 'stderr',
    });
  });

  it('offers no logs button for a step that did not run', () => {
    setRun([makeNode('other')]);
    render(<StepRunPanel dagName="dag" runId="r1" stepName="fetch" />);
    expect(screen.queryByRole('button')).toBeNull();
  });
});
