// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  components,
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import TimelineChart from '../TimelineChart';

vi.mock('@/hooks/api', () => ({
  useQuery: vi.fn(),
}));

type DAGRunDetails = components['schemas']['DAGRunDetails'];
type Node = components['schemas']['Node'];
type SubDAGRun = components['schemas']['SubDAGRun'];
type SubDAGRunDetail = components['schemas']['SubDAGRunDetail'];

const useQueryMock = useQuery as unknown as {
  mockImplementation: (
    fn: (path: string, init?: unknown, config?: unknown) => unknown
  ) => void;
  mockReturnValue: (value: unknown) => void;
};

const appBarValue = {
  title: 'DAGs',
  setTitle: vi.fn(),
  remoteNodes: ['local', 'remote-a'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'remote-a',
  selectRemoteNode: vi.fn(),
};

function node(overrides: Partial<Node> = {}): Node {
  const { step: stepOverrides, ...nodeOverrides } = overrides;

  return {
    stdout: '',
    stderr: '',
    startedAt: '2026-01-01T00:00:00Z',
    finishedAt: '2026-01-01T00:00:10Z',
    status: NodeStatus.Success,
    statusLabel: NodeStatusLabel.succeeded,
    retryCount: 0,
    doneCount: 1,
    ...nodeOverrides,
    step: {
      name: 'step-a',
      ...stepOverrides,
    },
  };
}

function dagRun(overrides: Partial<DAGRunDetails> = {}): DAGRunDetails {
  return {
    dagRunId: 'root-run',
    name: 'root-dag',
    status: Status.Success,
    statusLabel: StatusLabel.succeeded,
    autoRetryCount: 0,
    startedAt: '2026-01-01T00:00:00Z',
    finishedAt: '2026-01-01T00:01:00Z',
    artifactsAvailable: false,
    rootDAGRunName: 'root-dag',
    rootDAGRunId: 'root-run',
    log: '',
    nodes: [node()],
    ...overrides,
  };
}

function subRun(
  dagRunId: string,
  overrides: Partial<SubDAGRun> = {}
): SubDAGRun {
  return {
    dagRunId,
    dagName: 'child-dag',
    params: `{"ITEM":"${dagRunId}"}`,
    ...overrides,
  };
}

function subRunDetail(
  dagRunId: string,
  overrides: Partial<SubDAGRunDetail> = {}
): SubDAGRunDetail {
  return {
    dagRunId,
    dagName: 'child-dag',
    params: `{"ITEM":"${dagRunId}"}`,
    status: Status.Success,
    statusLabel: StatusLabel.succeeded,
    startedAt: '2026-01-01T00:00:01Z',
    finishedAt: '2026-01-01T00:00:05Z',
    ...overrides,
  };
}

function parallelNode(
  stepName: string,
  runs: SubDAGRun[],
  overrides: Partial<Node> = {}
): Node {
  return node({
    step: {
      name: stepName,
      call: 'child-dag',
      parallel: { items: runs.map((run) => run.dagRunId) },
      ...overrides.step,
    },
    subRuns: runs,
    ...overrides,
  });
}

function renderChart(
  status: DAGRunDetails,
  appBarOverride: Partial<typeof appBarValue> = {}
) {
  return render(
    <AppBarContext.Provider value={{ ...appBarValue, ...appBarOverride }}>
      <TimelineChart status={status} />
    </AppBarContext.Provider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  useQueryMock.mockReturnValue({
    data: { subRuns: [] },
    mutate: vi.fn(),
  });
});

describe('TimelineChart', () => {
  it('passes null query init when there are no eligible parallel child rows', () => {
    const queryCalls: Array<{ path: string; init?: unknown }> = [];
    useQueryMock.mockImplementation((path, init) => {
      queryCalls.push({ path, init });
      return { data: { subRuns: [] }, mutate: vi.fn() } as never;
    });

    renderChart(dagRun());

    expect(queryCalls).toContainEqual({
      path: '/dag-runs/{name}/{dagRunId}/sub-dag-runs',
      init: null,
    });
  });

  it('does not render child rows for non-parallel sub-DAG calls', () => {
    useQueryMock.mockReturnValue({
      data: { subRuns: [subRunDetail('child-run-1')] },
      mutate: vi.fn(),
    });

    renderChart(
      dagRun({
        nodes: [
          node({
            step: { name: 'call-child', call: 'child-dag' },
            subRuns: [subRun('child-run-1')],
          }),
        ],
      })
    );

    expect(screen.getByText('call-child')).toBeInTheDocument();
    expect(screen.queryByText('#01')).not.toBeInTheDocument();
  });

  it('does not render child rows for repeat-policy-only sub-DAG runs', () => {
    useQueryMock.mockReturnValue({
      data: { subRuns: [subRunDetail('repeat-run-1')] },
      mutate: vi.fn(),
    });

    renderChart(
      dagRun({
        nodes: [
          node({
            step: { name: 'repeat-child', call: 'child-dag' },
            subRunsRepeated: [subRun('repeat-run-1')],
          }),
        ],
      })
    );

    expect(screen.getByText('repeat-child')).toBeInTheDocument();
    expect(screen.queryByText('#01')).not.toBeInTheDocument();
  });

  it('queries root timeline details with the root DAG name and run ID', () => {
    const queryCalls: Array<{
      path: string;
      init?: unknown;
      config?: unknown;
    }> = [];
    useQueryMock.mockImplementation((path, init, config) => {
      queryCalls.push({ path, init, config });
      return { data: { subRuns: [] }, mutate: vi.fn() } as never;
    });

    renderChart(
      dagRun({
        status: Status.Running,
        nodes: [parallelNode('parallel-call', [subRun('child-run-1')])],
      })
    );

    expect(queryCalls[0]).toEqual(
      expect.objectContaining({
        path: '/dag-runs/{name}/{dagRunId}/sub-dag-runs',
        init: {
          params: {
            path: {
              name: 'root-dag',
              dagRunId: 'root-run',
            },
            query: {
              remoteNode: 'remote-a',
              parentSubDAGRunId: undefined,
            },
          },
        },
        config: {
          refreshInterval: 3000,
        },
      })
    );
  });

  it('queries nested timeline details with parentSubDAGRunId set to the current sub-DAG run ID', () => {
    const queryCalls: Array<{ path: string; init?: unknown }> = [];
    useQueryMock.mockImplementation((path, init) => {
      queryCalls.push({ path, init });
      return { data: { subRuns: [] }, mutate: vi.fn() } as never;
    });

    renderChart(
      dagRun({
        name: 'child-dag',
        dagRunId: 'child-run',
        rootDAGRunName: 'root-dag',
        rootDAGRunId: 'root-run',
        nodes: [parallelNode('nested-parallel', [subRun('grandchild-run')])],
      })
    );

    expect(queryCalls[0]?.init).toEqual({
      params: {
        path: {
          name: 'root-dag',
          dagRunId: 'root-run',
        },
        query: {
          remoteNode: 'remote-a',
          parentSubDAGRunId: 'child-run',
        },
      },
    });
  });

  it('renders child rows under a parallel parent with child tooltip details', async () => {
    useQueryMock.mockReturnValue({
      data: {
        subRuns: [
          subRunDetail('child-run-1', { params: '{"SCOPE":"item1"}' }),
          subRunDetail('child-run-2', { params: '{"SCOPE":"item2"}' }),
        ],
      },
      mutate: vi.fn(),
    });

    renderChart(
      dagRun({
        nodes: [
          parallelNode('parallel-call', [
            subRun('child-run-1'),
            subRun('child-run-2'),
          ]),
        ],
      })
    );

    const rows = screen.getAllByTestId('timeline-row');
    expect(rows.map((row) => row.getAttribute('data-row-id'))).toEqual([
      'step:parallel-call',
      'subdag:parallel-call:child-run-1',
      'subdag:parallel-call:child-run-2',
    ]);
    expect(screen.getByText('#01')).toBeInTheDocument();
    expect(screen.getByText('#02')).toBeInTheDocument();

    await userEvent.hover(
      screen.getByTestId('timeline-bar-subdag:parallel-call:child-run-1')
    );

    expect(await screen.findAllByText('DAG: child-dag')).not.toHaveLength(0);
    expect(screen.getAllByText('Run ID: child-run-1')).not.toHaveLength(0);
    expect(screen.getAllByText('Params: {"SCOPE":"item1"}')).not.toHaveLength(
      0
    );
  });

  it('filters endpoint details for multiple parallel nodes to matching run IDs', () => {
    useQueryMock.mockReturnValue({
      data: {
        subRuns: [
          subRunDetail('b-run', { dagName: 'child-b' }),
          subRunDetail('unrelated-run'),
          subRunDetail('a-run', { dagName: 'child-a' }),
        ],
      },
      mutate: vi.fn(),
    });

    renderChart(
      dagRun({
        nodes: [
          parallelNode('parallel-a', [subRun('a-run')], {
            startedAt: '2026-01-01T00:00:00Z',
            finishedAt: '2026-01-01T00:00:10Z',
          }),
          parallelNode('parallel-b', [subRun('b-run')], {
            startedAt: '2026-01-01T00:00:20Z',
            finishedAt: '2026-01-01T00:00:30Z',
          }),
        ],
      })
    );

    expect(
      screen
        .getAllByTestId('timeline-row')
        .map((row) => row.getAttribute('data-row-id'))
    ).toEqual([
      'step:parallel-a',
      'subdag:parallel-a:a-run',
      'step:parallel-b',
      'subdag:parallel-b:b-run',
    ]);
    expect(screen.getAllByText('#01')).toHaveLength(2);
  });

  it('keeps parent errors in the step tooltip', async () => {
    renderChart(
      dagRun({
        nodes: [
          node({
            step: { name: 'failing-step' },
            status: NodeStatus.Failed,
            statusLabel: NodeStatusLabel.failed,
            error: 'parent exploded',
          }),
        ],
      })
    );

    await userEvent.hover(screen.getByTestId('timeline-bar-step:failing-step'));

    expect(
      await screen.findAllByText('Error: parent exploded')
    ).not.toHaveLength(0);
  });

  it('displays child DAG-run queued status as Queued, not Skipped', async () => {
    useQueryMock.mockReturnValue({
      data: {
        subRuns: [
          subRunDetail('queued-run', {
            status: Status.Queued,
            statusLabel: StatusLabel.queued,
          }),
        ],
      },
      mutate: vi.fn(),
    });

    renderChart(
      dagRun({
        nodes: [parallelNode('parallel-call', [subRun('queued-run')])],
      })
    );

    await userEvent.hover(
      screen.getByTestId('timeline-bar-subdag:parallel-call:queued-run')
    );

    const tooltip = (await screen.findAllByText('Queued'))
      .map((element) => element.closest('[data-slot="tooltip-content"]'))
      .find((element): element is HTMLElement => element !== null);
    expect(tooltip).toBeInTheDocument();
    expect(
      within(tooltip as HTMLElement).queryByText('Skipped')
    ).not.toBeInTheDocument();
  });
});
