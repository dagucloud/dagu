// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
  type components,
} from '@/api/v1/schema';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import {
  SubRunOpenProvider,
  type SubRunStackEntry,
} from '@/features/dags/components/common/SubRunStackModal';
import type { RunOverlay } from '@/features/visual-editor/contracts';
import { WorkspaceDataContext } from '../../data/context';
import { nodesByStep } from '../../data/runFacts';
import type {
  ApiSubRunDetail,
  DAGPageData,
  RunRef,
  WorkspaceRunValue,
} from '../../data/types';
import { StepSubRuns } from '../StepSubRuns';

type Node = components['schemas']['Node'];
type Details = components['schemas']['DAGRunDetails'];

const subRunsApi = vi.hoisted(() => ({
  rows: [] as ApiSubRunDetail[],
  calls: [] as Array<[RunRef | null, string | undefined]>,
}));

vi.mock('../../data/useSubRuns', () => ({
  useSubRuns: (ref: RunRef | null, parentSubDAGRunId?: string) => {
    subRunsApi.calls.push([ref, parentSubDAGRunId]);
    return { subRuns: ref ? subRunsApi.rows : [], isLoading: false };
  },
}));
vi.mock('@/hooks/api', () => ({
  useClient: () => ({}),
  useQuery: () => ({ data: undefined, isLoading: false, mutate: vi.fn() }),
}));

function node(subRuns: Node['subRuns'], extra: Partial<Node> = {}): Node {
  return {
    step: { name: 'fan', call: 'worker' },
    status: NodeStatus.Success,
    statusLabel: NodeStatusLabel.succeeded,
    stdout: '',
    stderr: '',
    startedAt: '2026-10-01T00:00:00Z',
    finishedAt: '2026-10-01T00:00:05Z',
    retryCount: 0,
    doneCount: 1,
    subRuns,
    ...extra,
  } as Node;
}

const ONE = node([{ dagRunId: 'c-1', dagName: 'child', params: 'A=1' }]);
const THREE = node(
  [
    { dagRunId: 'w-1', dagName: 'worker', params: 'N=1' },
    { dagRunId: 'w-2', dagName: 'worker', params: 'N=2' },
  ],
  { subRunsRepeated: [{ dagRunId: 'w-3', dagName: 'worker', params: 'N=3' }] }
);

function detail(
  dagRunId: string,
  status: Status,
  statusLabel: StatusLabel,
  startedAt: string
): ApiSubRunDetail {
  return { dagRunId, dagName: 'worker', status, statusLabel, startedAt };
}

const holder = {
  name: 'example',
  dagRunId: 'run-1',
  rootDAGRunName: 'example',
  rootDAGRunId: 'run-1',
  status: Status.Success,
  statusLabel: StatusLabel.succeeded,
  nodes: [THREE],
} as Details;

function runValue(): WorkspaceRunValue {
  return {
    selection: 'latest',
    ref: { name: 'example', runId: 'run-1' },
    details: holder,
    hasRun: true,
    isLatest: true,
    isActive: false,
    notFound: false,
    latestRef: null,
    overlay: {} as RunOverlay,
    nodeByStep: nodesByStep(holder),
    handlers: [],
    waiting: { approvals: 0, humanTasks: 0, agent: 0, firstWaitingStep: null },
    failedStep: null,
    select: vi.fn(),
    applyNodeStatus: vi.fn(),
    refresh: vi.fn().mockResolvedValue(undefined),
  };
}

let path = '';
function Probe() {
  const location = useLocation();
  path = `${location.pathname}${location.search}`;
  return null;
}

function renderSubRuns(
  n: Node,
  opts: {
    openOnStack?: ((e: SubRunStackEntry) => void) | null;
    parentSubDAGRunId?: string;
  } = {}
) {
  const openOnStack =
    opts.openOnStack === undefined ? vi.fn() : opts.openOnStack;
  render(
    <MemoryRouter initialEntries={['/dags/example.yaml']}>
      <RemoteNodeProvider remoteNode="edge">
        <WorkspaceDataContext.Provider
          value={{
            fileName: 'example.yaml',
            dagData: { dag: { name: 'example' } } as DAGPageData,
            workspaceName: null,
            remoteNode: 'edge',
            run: runValue(),
            refresh: vi.fn(),
            registerHistory: () => () => undefined,
          }}
        >
          <SubRunOpenProvider value={openOnStack}>
            <Routes>
              <Route
                path="*"
                element={
                  <>
                    <StepSubRuns
                      node={n}
                      holder={holder}
                      parentSubDAGRunId={opts.parentSubDAGRunId}
                    />
                    <Probe />
                  </>
                }
              />
            </Routes>
          </SubRunOpenProvider>
        </WorkspaceDataContext.Provider>
      </RemoteNodeProvider>
    </MemoryRouter>
  );
  return { openOnStack };
}

const CHILD_PAGE = '/dags/example?remoteNode=edge&run=run-1&subRun=c-1';

beforeEach(() => {
  subRunsApi.calls = [];
  subRunsApi.rows = [
    detail(
      'w-1',
      Status.Success,
      StatusLabel.succeeded,
      '2026-10-01T00:00:01Z'
    ),
    detail('w-2', Status.Failed, StatusLabel.failed, '2026-10-01T00:00:02Z'),
    detail('w-3', Status.Running, StatusLabel.running, '2026-10-01T00:00:03Z'),
    // A child of another step is not counted.
    detail('x-9', Status.Failed, StatusLabel.failed, '2026-10-01T00:00:04Z'),
  ];
});

afterEach(() => {
  vi.restoreAllMocks();
});

// parity:steps-subdag-runs-list parity:drawer-subruns parity:run-step-subruns
describe('StepSubRuns with one child', () => {
  // parity:steps-subrun-open
  it('opens the child on the sub-run stack', () => {
    const { openOnStack } = renderSubRuns(ONE);

    expect(screen.getByText('Parameters:')).toBeVisible();
    expect(screen.getByText('A=1')).toBeVisible();
    fireEvent.click(
      screen.getByRole('button', { name: /View Sub DAG Run: child/ })
    );

    expect(openOnStack).toHaveBeenCalledWith({
      name: 'child',
      dagRunId: 'c-1',
    });
    expect(subRunsApi.calls.every(([ref]) => ref === null)).toBe(true);
  });

  it.each([{ ctrlKey: true }, { metaKey: true }])(
    'opens the child run page in a new tab on %o',
    (modifier) => {
      const open = vi.spyOn(window, 'open').mockReturnValue(null);
      const { openOnStack } = renderSubRuns(ONE);

      fireEvent.click(
        screen.getByRole('button', { name: /View Sub DAG Run: child/ }),
        modifier
      );

      expect(open).toHaveBeenCalledWith(CHILD_PAGE, '_blank');
      expect(openOnStack).not.toHaveBeenCalled();
    }
  );

  // parity:crit-history-subrun-navigation
  it('navigates to the child run page outside a sub-run stack', () => {
    renderSubRuns(ONE, { openOnStack: null });

    fireEvent.click(
      screen.getByRole('button', { name: /View Sub DAG Run: child/ })
    );

    expect(path).toBe(CHILD_PAGE);
  });
});

describe('StepSubRuns with several children', () => {
  it('lists the children newest first with status filters and counts', () => {
    renderSubRuns(THREE);

    expect(subRunsApi.calls[subRunsApi.calls.length - 1]).toEqual([
      { name: 'example', runId: 'run-1' },
      undefined,
    ]);
    const toggle = screen.getByRole('button', {
      name: /Multiple executions: 3 sub DAG runs/,
    });
    expect(screen.queryByText(/^#0/)).not.toBeInTheDocument();
    fireEvent.click(toggle);

    const rows = screen.getAllByRole('button', { name: /^#0\d: worker$/ });
    expect(rows.map((r) => r.textContent)).toEqual([
      '#03: worker',
      '#02: worker',
      '#01: worker',
    ]);
    const chips = screen
      .getAllByRole('button', { pressed: false })
      .concat(screen.getAllByRole('button', { pressed: true }));
    expect(chips.map((c) => c.textContent).sort()).toEqual([
      'All3',
      'Failed1',
      'Running1',
      'Succeeded1',
    ]);

    fireEvent.click(screen.getByRole('button', { name: /^Failed/ }));

    expect(
      screen.getByRole('button', {
        name: /Multiple executions: 1 of 3 sub DAG runs/,
      })
    ).toBeVisible();
    expect(
      screen.getAllByRole('button', { name: /^#0\d: worker$/ })
    ).toHaveLength(1);
    expect(screen.getByRole('button', { name: /^Failed/ })).toHaveAttribute(
      'aria-pressed',
      'true'
    );
  });

  // The sub-run list can lag behind the step, for a child that is queued.
  it('keeps a child that the sub-run list does not have yet, last', () => {
    subRunsApi.rows = subRunsApi.rows.filter((r) => r.dagRunId !== 'w-3');
    renderSubRuns(THREE);

    fireEvent.click(
      screen.getByRole('button', {
        name: /Multiple executions: 3 sub DAG runs/,
      })
    );

    expect(
      screen
        .getAllByRole('button', { name: /^#0\d: worker$/ })
        .map((r) => r.textContent)
    ).toEqual(['#02: worker', '#01: worker', '#03: worker']);
  });

  it('hides the filters when every child has one status', () => {
    subRunsApi.rows = subRunsApi.rows.map((r) => ({
      ...r,
      status: Status.Success,
      statusLabel: StatusLabel.succeeded,
    }));
    renderSubRuns(THREE);

    fireEvent.click(
      screen.getByRole('button', { name: /Multiple executions: 3/ })
    );

    expect(
      screen.queryByRole('button', { name: /^All/ })
    ).not.toBeInTheDocument();
  });

  it('opens a listed child on the stack, or its page with Ctrl or Cmd', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null);
    const { openOnStack } = renderSubRuns(THREE);
    fireEvent.click(
      screen.getByRole('button', { name: /Multiple executions: 3/ })
    );

    fireEvent.click(screen.getByRole('button', { name: '#02: worker' }));
    expect(openOnStack).toHaveBeenCalledWith({
      name: 'worker',
      dagRunId: 'w-2',
    });

    fireEvent.click(screen.getByRole('button', { name: '#03: worker' }), {
      metaKey: true,
    });
    expect(open).toHaveBeenCalledWith(
      '/dags/example?remoteNode=edge&run=run-1&subRun=w-3',
      '_blank'
    );
    expect(openOnStack).toHaveBeenCalledTimes(1);
  });

  // parity:parallel-execution-modal parity:run-parallel-picker
  it('picks a child in ParallelExecutionModal and opens it on the stack', () => {
    const { openOnStack } = renderSubRuns(THREE);

    fireEvent.click(
      screen.getByRole('button', { name: 'Choose a sub DAG run' })
    );
    expect(screen.getByText('3 sub DAG-runs')).toBeVisible();
    fireEvent.click(screen.getByText('N=3'));

    expect(openOnStack).toHaveBeenCalledWith({
      name: 'worker',
      dagRunId: 'w-3',
    });
    expect(screen.queryByText('3 sub DAG-runs')).not.toBeInTheDocument();
  });

  it('reads the children of a nested step under its parent sub run', () => {
    renderSubRuns(THREE, { parentSubDAGRunId: 'sub-7' });

    expect(subRunsApi.calls[subRunsApi.calls.length - 1]).toEqual([
      { name: 'example', runId: 'run-1' },
      'sub-7',
    ]);
  });
});

// The sub-runs are a list for assistive technology, and their dots take the
// status tokens of lib/statusPalette.ts.
describe('StepSubRuns as a list', () => {
  it('renders the single child as a list item', () => {
    renderSubRuns(ONE);

    const items = within(screen.getByRole('list')).getAllByRole('listitem');
    expect(items).toHaveLength(1);
    expect(
      within(items[0]!).getByRole('button', { name: /View Sub DAG Run: child/ })
    ).toBeVisible();
    expect(within(items[0]!).getByText('A=1')).toBeVisible();
  });

  it('lists the expanded children with a status dot each', () => {
    renderSubRuns(THREE);
    fireEvent.click(
      screen.getByRole('button', { name: /Multiple executions: 3/ })
    );

    const items = within(screen.getByRole('list')).getAllByRole('listitem');
    expect(items).toHaveLength(3);
    const dots = items.map((item) => within(item).getByRole('img'));
    expect(dots.map((dot) => dot.getAttribute('aria-label'))).toEqual([
      'Running',
      'Failed',
      'Succeeded',
    ]);
    expect(dots[0]).toHaveClass('bg-[var(--status-running)]');
    expect(dots[1]).toHaveClass('bg-[var(--status-error)]');
    expect(dots[2]).toHaveClass('bg-[var(--status-success)]');
  });
});
