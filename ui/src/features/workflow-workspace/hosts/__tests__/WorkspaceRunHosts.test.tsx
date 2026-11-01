// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { useContext, type ReactNode } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  NodeStatus,
  Status,
  StatusLabel,
  type components,
} from '@/api/v1/schema';
import { AgentTimeline } from '@/features/dags/components/agent/AgentTimeline';
import { useOpenSubRun } from '@/features/dags/components/common/SubRunStackModal';
import TimelineChart from '@/features/dags/components/visualization/TimelineChart';
import { RootDAGRunContext } from '@/features/dags/contexts/RootDAGRunContext';
import { useBoundedDAGRunDetails } from '@/features/dag-runs/hooks/useBoundedDAGRunDetails';
import {
  connectedSSE,
  dagPage,
  FILE_NAME,
  node,
  run,
} from '../../data/__tests__/fixtures';
import { serve, type FakeServer } from '../../data/__tests__/harness';
import { useWorkspaceRun } from '../../data/useWorkspaceRun';
import { WorkspaceDataProvider } from '../../data/WorkspaceDataProvider';
import { WorkspaceRunHosts } from '../WorkspaceRunHosts';

type AgentEvent = components['schemas']['AgentEvent'];
type DAGRunDetails = components['schemas']['DAGRunDetails'];

const h = vi.hoisted(() => ({
  GET: vi.fn(),
  navigate: vi.fn(),
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
vi.mock('react-router-dom', async (importOriginal) => ({
  ...(await importOriginal<typeof import('react-router-dom')>()),
  useNavigate: () => h.navigate,
}));
vi.mock('@/features/dag-runs/hooks/useBoundedDAGRunDetails', () => ({
  useBoundedDAGRunDetails: vi.fn(),
}));
// The real content renders DAGStatus. This stand-in shows the level and opens
// a deeper child through the opener that the modal gives its content.
vi.mock(
  '@/features/dag-runs/components/dag-run-details/DAGRunDetailsContent',
  () => ({
    default: function ChildContent({
      dagRunId,
      initialStepName,
    }: {
      dagRunId: string;
      initialStepName?: string;
    }) {
      const open = useOpenSubRun();
      return (
        <div
          data-testid="child-content"
          data-run={dagRunId}
          data-step={initialStepName}
        >
          <button
            type="button"
            onClick={() => open?.({ name: 'grandchild', dagRunId: 'g-1' })}
          >
            open grandchild
          </button>
        </div>
      );
    },
  })
);

let server: FakeServer;

function subRunDetail(dagRunId: string) {
  return {
    dagRunId,
    dagName: 'child-dag',
    params: '',
    status: Status.Success,
    statusLabel: StatusLabel.succeeded,
    startedAt: '2026-01-15T02:00:01Z',
    finishedAt: '2026-01-15T02:00:02Z',
  };
}

const parallelNode = node('fan-out', NodeStatus.Success, {
  step: { call: 'child-dag', parallel: { items: ['c-1'] } },
  subRuns: [{ dagRunId: 'c-1', dagName: 'child-dag', params: '' }],
});

const callNode = node('call-child', NodeStatus.Success, {
  step: { call: 'child-dag' },
  subRuns: [{ dagRunId: 'c-9', dagName: 'child-dag', params: '' }],
});

/** Reads the hosts' RootDAGRunContext and offers the run selection. */
function Probe() {
  const root = useContext(RootDAGRunContext);
  const run = useWorkspaceRun();
  const open = useOpenSubRun();
  return (
    <div>
      <output data-testid="root-run">{root.data?.dagRunId ?? 'none'}</output>
      <output data-testid="has-opener">{String(open !== null)}</output>
      <button
        type="button"
        onClick={() =>
          root.setData({
            ...(run.details as DAGRunDetails),
            dagRunId: 'other',
          })
        }
      >
        set root
      </button>
      <button
        type="button"
        onClick={() => open?.({ name: 'child-dag', dagRunId: 'c-1' })}
      >
        open child
      </button>
      <button type="button" onClick={() => run.select('latest')}>
        select latest
      </button>
      <button type="button" onClick={() => run.select('run-2')}>
        select run-2
      </button>
    </div>
  );
}

/** Wires AgentTimeline as DAGStatus does (openAgentChildRun). */
function AgentProbe({ events }: { events: AgentEvent[] }) {
  const open = useOpenSubRun();
  return (
    <AgentTimeline
      events={events}
      onOpenChildRun={(event) => {
        if (!event.childDagRunId || !open) return;
        open({
          name: event.childDagName || event.name || 'child',
          dagRunId: event.childDagRunId,
        });
      }}
    />
  );
}

function renderHosts(children: ReactNode, url = `/dags/${FILE_NAME}`) {
  // The page-level provider of classic mode. It holds a run that is neither
  // the latest nor the pinned run, so a read of it shows in the probe.
  const page = { data: run('page-run'), setData: vi.fn() };
  const view = render(
    <SWRConfig value={{ provider: () => new Map(), shouldRetryOnError: false }}>
      <MemoryRouter initialEntries={[url]}>
        <RootDAGRunContext.Provider value={page}>
          <WorkspaceDataProvider
            fileName={FILE_NAME}
            dagData={server.dag}
            refreshDag={async () => undefined}
            workspaceName={null}
          >
            <WorkspaceRunHosts>
              <Probe />
              {children}
            </WorkspaceRunHosts>
          </WorkspaceDataProvider>
        </RootDAGRunContext.Provider>
      </MemoryRouter>
    </SWRConfig>
  );
  return { ...view, page };
}

/** The stack levels that the modal shows, root first. */
function crumbs(): string[] {
  const nav = screen.queryByRole('navigation');
  if (!nav) return [];
  return Array.from(nav.querySelectorAll('button')).map(
    (b) => b.textContent ?? ''
  );
}

/** The last request target of the modal's run-details hook. */
function lastBoundedTarget() {
  const targets = vi
    .mocked(useBoundedDAGRunDetails)
    .mock.calls.map((call) => call[0].target)
    .filter((t) => t !== null);
  return targets[targets.length - 1];
}

beforeEach(() => {
  h.GET.mockReset();
  h.navigate.mockReset();
  server = {
    dag: dagPage(run('run-1')),
    runs: {
      'run-2': run('run-2', [parallelNode, callNode], Status.Success, {
        rootDAGRunName: 'demo',
        rootDAGRunId: 'run-2',
      }),
      'run-3': run('run-3', [node('a')], Status.Success, {
        name: 'nested',
        rootDAGRunName: 'parent-dag',
        rootDAGRunId: 'parent-run',
      }),
    },
    history: { dagRuns: [], gridData: [] },
    subRuns: [subRunDetail('c-1')],
  };
  h.GET.mockImplementation(serve(server));
  vi.mocked(useBoundedDAGRunDetails).mockImplementation(({ target }) => {
    return {
      data: target
        ? ({
            ...run(target.dagRunId),
            name: target.name,
          } as DAGRunDetails)
        : undefined,
      isValidating: false,
      refresh: vi.fn(),
    } as unknown as ReturnType<typeof useBoundedDAGRunDetails>;
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('WorkspaceRunHosts', () => {
  // parity:subrun-stack-modal
  // parity:common-subrun-stack
  // parity:run-subrun-stack
  it('opens a child run in the stack, pushes nested runs and pops', async () => {
    renderHosts(null, `/dags/${FILE_NAME}?run=run-2`);
    expect(await screen.findByText('run-2')).toBeInTheDocument();
    expect(screen.getByTestId('has-opener')).toHaveTextContent('true');

    fireEvent.click(screen.getByRole('button', { name: 'open child' }));
    expect(await screen.findByTestId('child-content')).toHaveAttribute(
      'data-run',
      'c-1'
    );
    expect(crumbs()).toEqual(['demo', 'child-dag']);
    expect(lastBoundedTarget()).toMatchObject({
      name: 'child-dag',
      dagRunId: 'c-1',
      parentName: 'demo',
      parentDAGRunId: 'run-2',
    });

    fireEvent.click(screen.getByRole('button', { name: 'open grandchild' }));
    expect(await screen.findByTestId('child-content')).toHaveAttribute(
      'data-run',
      'g-1'
    );
    expect(crumbs()).toEqual(['demo', 'child-dag', 'grandchild']);

    // The breadcrumb of the first child pops one level.
    fireEvent.click(screen.getByRole('button', { name: 'child-dag' }));
    expect(screen.getByTestId('child-content')).toHaveAttribute(
      'data-run',
      'c-1'
    );
    expect(crumbs()).toEqual(['demo', 'child-dag']);
    expect(h.navigate).not.toHaveBeenCalled();
  });

  it('takes the root of a nested run from its root fields', async () => {
    renderHosts(null, `/dags/${FILE_NAME}?run=run-3`);
    expect(await screen.findByText('run-3')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'open child' }));
    await screen.findByTestId('child-content');
    // rootLabel is the run's own name; the request goes to its root run.
    expect(crumbs()).toEqual(['nested', 'child-dag']);
    expect(lastBoundedTarget()).toMatchObject({
      parentName: 'parent-dag',
      parentDAGRunId: 'parent-run',
    });
  });

  it('clears the stack when another run is selected', async () => {
    renderHosts(null, `/dags/${FILE_NAME}?run=run-2`);
    expect(await screen.findByText('run-2')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'open child' }));
    expect(await screen.findByTestId('child-content')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'select latest' }));
    expect(await screen.findByText('run-1')).toBeInTheDocument();
    expect(screen.queryByTestId('child-content')).not.toBeInTheDocument();

    // Back on the first run, the old stack does not come back.
    fireEvent.click(screen.getByRole('button', { name: 'select run-2' }));
    expect(await screen.findByText('run-2')).toBeInTheDocument();
    expect(screen.queryByTestId('child-content')).not.toBeInTheDocument();
  });

  // parity:shell-root-dagrun-context
  it('gives RootDAGRunContext the pinned run and ignores setData', async () => {
    const { page } = renderHosts(null, `/dags/${FILE_NAME}?run=run-2`);
    expect(await screen.findByText('run-2')).toBeInTheDocument();
    expect(screen.getByTestId('root-run')).toHaveTextContent('run-2');

    fireEvent.click(screen.getByRole('button', { name: 'set root' }));
    expect(screen.getByTestId('root-run')).toHaveTextContent('run-2');
    expect(page.setData).not.toHaveBeenCalled();
  });

  it('gives RootDAGRunContext the latest run when none is pinned', async () => {
    renderHosts(null);
    expect(await screen.findByText('run-1')).toBeInTheDocument();
    expect(screen.getByTestId('root-run')).toHaveTextContent('run-1');
  });

  // parity:timeline-subdag-rows
  it('opens a timeline sub-DAG row in the stack without navigation', async () => {
    function Timeline() {
      const { details } = useWorkspaceRun();
      return details ? <TimelineChart status={details} /> : null;
    }
    renderHosts(<Timeline />, `/dags/${FILE_NAME}?run=run-2`);
    const bar = await screen.findByTestId('timeline-bar-subdag:fan-out:c-1');

    fireEvent.click(bar);
    expect(await screen.findByTestId('child-content')).toHaveAttribute(
      'data-run',
      'c-1'
    );
    expect(crumbs()).toEqual(['demo', 'child-dag']);
    expect(h.navigate).not.toHaveBeenCalled();
  });

  // The classic step rows reach the hosts only inside the stack, through the
  // lazy modal content, so the test loads them lazily too.
  it('keeps the new tab on a modifier click of a step sub-run link', async () => {
    const { default: NodeStatusTableRow } = await import(
      '@/features/dags/components/dag-details/NodeStatusTableRow'
    );
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null);
    function Row() {
      const { details } = useWorkspaceRun();
      if (!details) return null;
      return (
        <NodeStatusTableRow
          rownum={1}
          node={callNode}
          name={FILE_NAME}
          dagRun={details}
          view="mobile"
        />
      );
    }
    renderHosts(<Row />, `/dags/${FILE_NAME}?run=run-2`);
    const link = (await screen.findByText(/View Sub DAG Run:/)).closest('div')!;

    fireEvent.click(link, { ctrlKey: true });
    expect(openSpy).toHaveBeenCalledTimes(1);
    // The tab opens the child over its root run.
    expect(openSpy.mock.calls[0]?.[0]).toContain('/dags/demo?');
    expect(openSpy.mock.calls[0]?.[0]).toContain('subRun=c-9');
    expect(openSpy.mock.calls[0]?.[1]).toBe('_blank');
    expect(screen.queryByTestId('child-content')).not.toBeInTheDocument();

    fireEvent.click(link, { metaKey: true });
    expect(openSpy).toHaveBeenCalledTimes(2);
    expect(screen.queryByTestId('child-content')).not.toBeInTheDocument();

    fireEvent.click(link);
    expect(await screen.findByTestId('child-content')).toHaveAttribute(
      'data-run',
      'c-9'
    );
    expect(openSpy).toHaveBeenCalledTimes(2);
    expect(h.navigate).not.toHaveBeenCalled();
  });

  // parity:run-agent-timeline
  it('opens an agent timeline child event in the stack', async () => {
    const events: AgentEvent[] = [
      {
        turn: 1,
        kind: 'action' as AgentEvent['kind'],
        name: 'delegate',
        childDagRunId: 'a-1',
        childDagName: 'helper',
      },
    ];
    renderHosts(<AgentProbe events={events} />, `/dags/${FILE_NAME}?run=run-2`);
    expect(await screen.findByText('run-2')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /delegate/ }));
    expect(await screen.findByTestId('child-content')).toHaveAttribute(
      'data-run',
      'a-1'
    );
    expect(crumbs()).toEqual(['demo', 'helper']);
  });

  describe('a sub-run link', () => {
    const SUB_RUN = '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}';

    beforeEach(() => {
      const base = serve(server);
      h.GET.mockImplementation(
        async (
          path: string,
          init?: { params?: { path?: Record<string, string> } }
        ) => {
          if (path !== SUB_RUN) return base(path, init);
          return init?.params?.path?.subDAGRunId === 'c-1'
            ? { data: { dagRunDetails: { ...run('c-1'), name: 'child-dag' } } }
            : {
                error: { code: 'not_found', message: 'not found' },
                response: { status: 404 },
              };
        }
      );
    });

    function lastURL(): string {
      return String(h.navigate.mock.lastCall?.[0] ?? '');
    }

    // ?step next to ?subRun is a step of the sub-run, not of the run.
    it('opens the sub-run on the stack with its step, once', async () => {
      renderHosts(null, `/dags/${FILE_NAME}?run=run-2&subRun=c-1&step=deploy`);

      const content = await screen.findByTestId('child-content');
      expect(content).toHaveAttribute('data-run', 'c-1');
      expect(content).toHaveAttribute('data-step', 'deploy');
      expect(crumbs()).toEqual(['demo', 'child-dag']);
      expect(lastBoundedTarget()).toMatchObject({
        subDAGRunId: 'c-1',
        parentName: 'demo',
        parentDAGRunId: 'run-2',
      });
      // The link leaves the URL, with its step.
      expect(lastURL()).toContain('run=run-2');
      expect(lastURL()).not.toMatch(/subRun|step/);
      expect(h.navigate.mock.lastCall?.[1]).toMatchObject({ replace: true });
    });

    it('drops the link of a sub-run that the run does not have', async () => {
      renderHosts(null, `/dags/${FILE_NAME}?run=run-2&subRun=gone`);

      await vi.waitFor(() => expect(h.navigate).toHaveBeenCalled());
      expect(lastURL()).not.toContain('subRun');
      expect(screen.queryByTestId('child-content')).not.toBeInTheDocument();
    });
  });

  it('renders the children unchanged without WorkspaceDataProvider', () => {
    function Outside() {
      const open = useOpenSubRun();
      const root = useContext(RootDAGRunContext);
      return (
        <output data-testid="outside">
          {`${open === null}:${root.data?.dagRunId ?? 'none'}`}
        </output>
      );
    }
    render(
      <WorkspaceRunHosts>
        <Outside />
      </WorkspaceRunHosts>
    );
    expect(screen.getByTestId('outside')).toHaveTextContent('true:none');
  });
});
