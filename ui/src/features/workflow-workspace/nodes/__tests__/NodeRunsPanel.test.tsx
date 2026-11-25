// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeRunStatus, NodeStatus, type components } from '@/api/v1/schema';
import { SubRunOpenProvider } from '@/features/dags/components/common/SubRunStackModal';
import type { ApiDAGDetails } from '../../contracts';
import { node, run } from '../../data/__tests__/fixtures';
import type { ApiDAGRunDetails, WorkspaceRunValue } from '../../data/types';
import { NodesTab } from '../../dock/tabs/runTabBodies';
import { NodeRunsPanel } from '../NodeRunsPanel';

type NodeRunList = components['schemas']['NodeRunList'];
type NodeRunCounts = components['schemas']['NodeRunCounts'];

const api = vi.hoisted(() => ({
  list: null as NodeRunList | null,
  calls: [] as Array<{
    path: string;
    query: Record<string, unknown>;
    params: Record<string, string>;
  }>,
  error: undefined as Error | undefined,
  mutate: vi.fn(),
}));

vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('../../dock/useDockStep', () => ({ useDockStep: () => null }));
vi.mock('@/hooks/api', () => ({
  useQuery: (
    path: string,
    init: {
      params: { query: Record<string, unknown>; path: Record<string, string> };
    } | null
  ) => {
    if (!init) return { data: undefined, isLoading: false, mutate: api.mutate };
    api.calls.push({
      path,
      query: init.params.query,
      params: init.params.path,
    });
    return {
      data: api.list ?? undefined,
      isLoading: !api.list && !api.error,
      error: api.error,
      mutate: api.mutate,
    };
  },
}));

const counts = (over: Partial<NodeRunCounts>): NodeRunCounts => ({
  total: 0,
  notStarted: 0,
  running: 0,
  waiting: 0,
  succeeded: 0,
  failed: 0,
  aborted: 0,
  disabled: 0,
  removed: 0,
  ...over,
});

const deployCounts = counts({ total: 3, succeeded: 1, failed: 1, disabled: 1 });

// web03 failed in deploy and was not started in clean; web01 succeeded in
// both; web02 is disabled and clean did not select it.
const list: NodeRunList = {
  steps: [
    { name: 'deploy', counts: deployCounts },
    {
      name: 'clean',
      counts: counts({ total: 2, succeeded: 1, notStarted: 1 }),
    },
  ],
  items: [
    {
      node: 'web03',
      runs: [
        {
          step: 'deploy',
          status: NodeRunStatus.failed,
          dagName: 'deploy-one',
          dagRunId: 'child-3',
          startedAt: '2026-11-24T00:00:00Z',
          finishedAt: '2026-11-24T00:00:05Z',
          steps: { done: 1, total: 2 },
          failedStep: 'restart',
        },
        {
          step: 'clean',
          status: NodeRunStatus.not_started,
          dagName: 'demo',
          dagRunId: 'clean-3',
        },
      ],
    },
    {
      node: 'web01',
      runs: [
        {
          step: 'deploy',
          status: NodeRunStatus.succeeded,
          dagName: 'deploy-one',
          dagRunId: 'child-1',
          steps: { done: 2, total: 2 },
        },
        {
          step: 'clean',
          status: NodeRunStatus.succeeded,
          dagName: 'demo',
          dagRunId: 'clean-1',
          steps: { done: 1, total: 1 },
        },
      ],
    },
    {
      node: 'web02',
      runs: [{ step: 'deploy', status: NodeRunStatus.disabled }],
    },
  ],
  pagination: {
    totalRecords: 3,
    currentPage: 1,
    totalPages: 1,
    nextPage: 1,
    prevPage: 1,
    hasNextPage: false,
  },
};

function details(
  deploy: NodeRunCounts = deployCounts,
  extra: Partial<ApiDAGRunDetails> = {}
) {
  return run(
    'root-1',
    [node('deploy', NodeStatus.Failed, { nodeRuns: deploy }), node('clean')],
    undefined,
    extra
  );
}

function renderPanel(value: ApiDAGRunDetails, open = vi.fn()) {
  const result = render(
    <SubRunOpenProvider value={open}>
      <NodeRunsPanel details={value} />
    </SubRunOpenProvider>
  );
  return { ...result, open };
}

function rowTexts() {
  return screen
    .getAllByTestId('node-runs-row')
    .map((row) => within(row).getAllByRole('rowheader')[0]?.textContent);
}

describe('NodeRunsPanel', () => {
  beforeEach(() => {
    api.list = list;
    api.calls = [];
    api.error = undefined;
    api.mutate.mockReset();
  });

  it('shows one row per node and a column per step, in the order of the server', () => {
    renderPanel(details());

    expect(rowTexts()).toEqual(['web03', 'web01', 'web02']);
    const headers = screen
      .getAllByRole('columnheader')
      .map((h) => h.textContent);
    expect(headers[0]).toBe('Node');
    expect(headers[1]).toContain('deploy');
    expect(headers[2]).toContain('clean');

    const failed = screen.getAllByTestId('node-runs-row')[0]!;
    expect(within(failed).getByText('restart')).toHaveAttribute(
      'title',
      'Failed at restart'
    );
    expect(within(failed).getByText('1/2')).toBeInTheDocument();
    const disabled = screen.getAllByTestId('node-runs-row')[2]!;
    expect(within(disabled).getByText('Disabled')).toBeInTheDocument();
    expect(
      within(disabled).getByTitle('The step did not select this node')
    ).toBeInTheDocument();
  });

  // A child run that returned no result, such as one that failed to start,
  // has no step progress, yet it finished.
  it('shows how long a node ran without step progress', () => {
    const web03 = list.items[0]!;
    api.list = {
      ...list,
      items: [{ ...web03, runs: [{ ...web03.runs[0]!, steps: undefined }] }],
    };
    renderPanel(details());

    const row = screen.getByTestId('node-runs-row');
    expect(within(row).getByText('5s')).toBeInTheDocument();
  });

  it('shows a node that left the inventory before a retry as removed', () => {
    api.list = {
      ...list,
      items: [
        {
          node: 'web04',
          runs: [{ step: 'deploy', status: NodeRunStatus.removed }],
        },
      ],
    };
    renderPanel(details());

    expect(screen.getByText('Removed')).toBeInTheDocument();
  });

  it('opens the child run of a cell on the sub-run stack', () => {
    const { open } = renderPanel(details());

    fireEvent.click(
      screen.getByRole('button', { name: 'Open the run of web03 for deploy' })
    );

    expect(open).toHaveBeenCalledWith({
      name: 'deploy-one',
      dagRunId: 'child-3',
    });
    // A disabled node has no run to open.
    expect(
      screen.queryByRole('button', { name: 'Open the run of web02 for deploy' })
    ).toBeNull();
  });

  it('asks for the failed rows only', () => {
    renderPanel(details());

    fireEvent.click(screen.getByRole('button', { name: 'Failed only' }));

    expect(api.calls.at(-1)?.query).toMatchObject({
      failedOnly: true,
      page: 1,
    });
    expect(screen.getByRole('button', { name: 'Failed only' })).toHaveAttribute(
      'aria-pressed',
      'true'
    );
  });

  it('reads the page again when the counts of the run change', () => {
    const { rerender } = renderPanel(details());
    expect(api.mutate).not.toHaveBeenCalled();

    rerender(
      <SubRunOpenProvider value={vi.fn()}>
        <NodeRunsPanel
          details={details(
            counts({ ...deployCounts, failed: 2, succeeded: 0 })
          )}
        />
      </SubRunOpenProvider>
    );

    expect(api.mutate).toHaveBeenCalledTimes(1);
  });

  // The dock scopes a run tab to the step it shows, but the table holds every
  // step with nodes, so a change in any of them reads the page again.
  it('reads the page again for another step while the dock shows one', () => {
    const tab = (check: NodeRunCounts) => {
      const value = run('root-1', [
        node('deploy', NodeStatus.Success, { nodeRuns: deployCounts }),
        node('check', NodeStatus.Running, { nodeRuns: check }),
      ]);
      const workspaceRun = {
        ref: { name: value.name, runId: value.dagRunId },
        details: value,
      } as WorkspaceRunValue;
      return (
        <NodesTab
          run={workspaceRun}
          fileName="demo.yaml"
          dag={{} as ApiDAGDetails}
          step="deploy"
        />
      );
    };
    const { rerender } = render(tab(counts({ total: 2, running: 2 })));

    rerender(tab(counts({ total: 2, running: 1, failed: 1 })));

    expect(api.mutate).toHaveBeenCalledTimes(1);
  });

  it('reads a sub-run through the route of its root run', () => {
    renderPanel(
      details(deployCounts, { rootDAGRunName: 'root', rootDAGRunId: 'root-0' })
    );

    const call = api.calls.find((c) => c.path.includes('/sub-dag-runs/'));
    expect(call?.params).toEqual({
      name: 'root',
      dagRunId: 'root-0',
      subDAGRunId: 'root-1',
    });
  });

  it('says that the node runs could not be read, and reads them again', () => {
    api.list = null;
    api.error = new Error('boom');
    renderPanel(details());

    expect(
      screen.getByText('Could not load the node runs.')
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(api.mutate).toHaveBeenCalledTimes(1);
  });

  // Fewer rows can leave the page past the last one, such as failed rows
  // that a retry fixed.
  it('reads the last page when the page is past it', () => {
    const pages = (totalPages: number) => ({
      ...list,
      pagination: { ...list.pagination, totalPages, totalRecords: totalPages },
    });
    api.list = pages(3);
    const { rerender } = renderPanel(details());
    fireEvent.click(screen.getByRole('button', { name: /Page 3/ }));
    expect(api.calls.at(-1)?.query).toMatchObject({ page: 3 });

    api.list = pages(1);
    rerender(
      <SubRunOpenProvider value={vi.fn()}>
        <NodeRunsPanel details={details()} />
      </SubRunOpenProvider>
    );
    expect(api.calls.at(-1)?.query).toMatchObject({ page: 1 });
  });

  it('says when the run recorded no node runs', () => {
    api.list = { ...list, steps: [], items: [] };
    renderPanel(details());

    expect(screen.getByText('No node runs recorded yet.')).toBeInTheDocument();
  });
});
