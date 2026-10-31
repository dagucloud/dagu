// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  type components,
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import { Dialog, DialogContent } from '@/components/ui/dialog';
import { type Config, ConfigContext } from '@/contexts/ConfigContext';
import { buildDAGPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import type { PeekTarget } from '../peekParams';
import { WorkflowPeek } from '../WorkflowPeek';

type DAGRunDetails = components['schemas']['DAGRunDetails'];

const h = vi.hoisted(() => ({
  workflow: {} as { data?: unknown; error?: unknown },
  history: undefined as { dagRuns: unknown[] } | undefined,
  run: { data: null, error: null } as { data: unknown; error: unknown },
  runTargets: [] as unknown[],
  queries: [] as Array<{ path: string; init: unknown }>,
  canExecute: true,
  askedWorkspace: undefined as string | null | undefined,
  mutate: vi.fn(),
  refreshRun: vi.fn(),
  sse: {
    data: null,
    error: null,
    isConnected: false,
    isConnecting: false,
    shouldUseFallback: false,
  },
}));

vi.mock('@/hooks/api', () => ({
  useQuery: (path: string, init: unknown) => {
    h.queries.push({ path, init });
    if (path === '/dags/{fileName}') return { ...h.workflow, mutate: h.mutate };
    if (path === '/dags/{fileName}/dag-runs') {
      return { data: h.history, mutate: vi.fn() };
    }
    return { data: undefined, mutate: vi.fn() };
  },
  useClient: () => ({ GET: vi.fn() }),
}));

vi.mock('@/hooks/useDAGSSE', () => ({ useDAGSSE: () => h.sse }));
vi.mock('@/hooks/useDAGHistorySSE', () => ({
  useDAGHistorySSE: () => h.sse,
}));

vi.mock('@/features/dag-runs/hooks/useBoundedDAGRunDetails', () => ({
  useBoundedDAGRunDetails: ({ target }: { target: unknown }) => {
    h.runTargets.push(target);
    return {
      data: h.run.data,
      error: h.run.error,
      isLoading: false,
      isValidating: false,
      refresh: h.refreshRun,
    };
  },
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanExecuteForWorkspace: (workspace?: string | null) => {
    h.askedWorkspace = workspace;
    return h.canExecute;
  },
  useCanManageWebhooks: () => false,
}));

vi.mock('@/features/dags/components/common/DAGActions', () => ({
  default: () => <div data-testid="dag-actions" />,
}));

// One button for each step, with the state that the run overlay gives it.
vi.mock('@/features/visual-editor/DetailsCanvas', () => ({
  default: ({
    dag,
    overlay,
    onSelectStep,
    onOpenStep,
  }: {
    dag: { steps?: Array<{ name: string }> };
    overlay?: Record<string, { statusLabel?: string }>;
    onSelectStep(step: string | null): void;
    onOpenStep?(step: string): void;
  }) => (
    <div data-testid="canvas">
      {dag.steps?.map((step) => (
        <button
          key={step.name}
          type="button"
          onClick={() => onSelectStep(step.name)}
          onDoubleClick={() => onOpenStep?.(step.name)}
        >
          {`${step.name}: ${overlay?.[step.name]?.statusLabel ?? 'no run'}`}
        </button>
      ))}
    </div>
  ),
}));

function run(id: string, overrides: Partial<DAGRunDetails> = {}) {
  return {
    name: 'etl',
    dagRunId: id,
    rootDAGRunName: 'etl',
    rootDAGRunId: id,
    status: Status.Failed,
    statusLabel: StatusLabel.failed,
    startedAt: '2026-10-31T10:00:00Z',
    finishedAt: '2026-10-31T10:01:00Z',
    queuedAt: '',
    autoRetryCount: 0,
    artifactsAvailable: false,
    sourceFileName: 'etl.yaml',
    error: 'load failed',
    nodes: [
      {
        step: { name: 'extract' },
        status: NodeStatus.Success,
        statusLabel: NodeStatusLabel.succeeded,
      },
      {
        step: { name: 'load' },
        status: NodeStatus.Failed,
        statusLabel: NodeStatusLabel.failed,
      },
    ],
    ...overrides,
  } as unknown as DAGRunDetails;
}

function workflowData(latest: DAGRunDetails) {
  return {
    dag: {
      name: 'etl',
      description: 'Nightly load',
      labels: ['team=data', 'workspace=ops'],
      schedule: [],
      steps: [{ name: 'extract' }, { name: 'load' }],
    },
    latestDAGRun: latest,
    suspended: false,
    pinned: false,
    errors: [],
    localDags: [],
  };
}

function LocationProbe() {
  const location = useLocation();
  return (
    <output data-testid="location">{`${location.pathname}${location.search}`}</output>
  );
}

function renderPeek(target: PeekTarget, onSelectRun = vi.fn()) {
  render(
    <MemoryRouter initialEntries={['/dags?peek=etl.yaml']}>
      <ConfigContext.Provider
        value={{ basePath: '', tz: 'UTC', permissions: {} } as Config}
      >
        <Dialog open>
          <DialogContent hideCloseButton aria-describedby={undefined}>
            <WorkflowPeek
              target={target}
              remoteNode="local"
              onClose={vi.fn()}
              onSelectRun={onSelectRun}
            />
          </DialogContent>
        </Dialog>
        <LocationProbe />
      </ConfigContext.Provider>
    </MemoryRouter>
  );
  return { onSelectRun };
}

const openLink = (name = 'Open workflow') => screen.getByRole('link', { name });

describe('WorkflowPeek', () => {
  beforeEach(() => {
    h.workflow = { data: workflowData(run('run-1')) };
    h.history = undefined;
    h.run = { data: null, error: null };
    h.runTargets.length = 0;
    h.queries.length = 0;
    h.canExecute = true;
    h.askedWorkspace = undefined;
    h.mutate.mockReset();
    h.refreshRun.mockReset();
  });

  it('shows the workflow and its latest run on the graph', async () => {
    renderPeek({ dag: 'etl.yaml' });

    expect(screen.getByRole('heading', { name: 'etl' })).toBeInTheDocument();
    expect(screen.getByText('Nightly load')).toBeInTheDocument();
    expect(screen.getByText('team=data')).toBeInTheDocument();
    expect(screen.queryByText('workspace=ops')).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Latest run' })).toBeVisible();
    expect(screen.getByTestId('dag-run-error')).toHaveTextContent(
      'load failed'
    );
    expect(
      await screen.findByRole('button', { name: 'load: failed' })
    ).toBeInTheDocument();
  });

  // The peek warms the cache of the page that it opens.
  it('reads the workflow with the key of the workflow page', () => {
    renderPeek({ dag: 'etl.yaml' });

    expect(h.queries).toContainEqual({
      path: '/dags/{fileName}',
      init: {
        params: {
          query: { remoteNode: 'local' },
          path: { fileName: 'etl.yaml' },
        },
      },
    });
    expect(h.runTargets).toContainEqual(null);
  });

  it('links Open workflow to the page that the name link opens', () => {
    renderPeek({ dag: 'etl.yaml' });

    expect(openLink()).toHaveAttribute(
      'href',
      buildDAGPageURL({ fileName: 'etl.yaml', remoteNode: 'local' })
    );
  });

  it('opens the full page at a step clicked on the graph', async () => {
    renderPeek({ dag: 'etl.yaml' });

    const step = await screen.findByRole('button', { name: 'load: failed' });
    fireEvent.pointerDown(step);
    fireEvent.click(step);

    expect(screen.getByTestId('location')).toHaveTextContent(
      '/dags/etl.yaml?remoteNode=local&step=load'
    );
  });

  // Arrow keys move the selection of the graph without leaving the peek.
  it('stays on the peek when a step is selected with the keyboard', async () => {
    renderPeek({ dag: 'etl.yaml' });
    const before = screen.getByTestId('location').textContent;

    const step = await screen.findByRole('button', { name: 'load: failed' });
    fireEvent.keyDown(step, { key: 'ArrowRight' });
    fireEvent.click(step);

    expect(screen.getByTestId('location')).toHaveTextContent(before ?? '');
  });

  it('opens the full page at a step opened on the graph', async () => {
    renderPeek({ dag: 'etl.yaml' });

    fireEvent.doubleClick(
      await screen.findByRole('button', { name: 'load: failed' })
    );

    expect(screen.getByTestId('location')).toHaveTextContent(
      '/dags/etl.yaml?remoteNode=local&step=load'
    );
  });

  it('says when the workflow has not run yet', () => {
    h.workflow = { data: workflowData(run('', { dagRunId: '' })) };

    renderPeek({ dag: 'etl.yaml' });

    expect(screen.getByText('No runs yet')).toBeInTheDocument();
  });

  it('reports a workflow that does not exist', () => {
    h.workflow = { error: { status: 404, message: 'not found' } };

    renderPeek({ dag: 'gone.yaml' });

    expect(screen.getByText('Workflow not found')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'gone.yaml' })).toBeVisible();
  });

  it('reports a failed read and reads again on retry', () => {
    h.workflow = { error: { status: 500, message: 'disk is full' } };

    renderPeek({ dag: 'etl.yaml' });

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent("Couldn't load this workflow");
    expect(alert).toHaveTextContent('disk is full');
    fireEvent.click(within(alert).getByRole('button', { name: 'Try again' }));
    expect(h.mutate).toHaveBeenCalled();
  });

  it('offers the run actions only with the execute permission', () => {
    h.canExecute = false;
    renderPeek({ dag: 'etl.yaml' });

    expect(screen.queryByTestId('dag-actions')).not.toBeInTheDocument();
    expect(h.askedWorkspace).toBe('ops');
  });

  it('offers the run actions to a user who can run the workflow', () => {
    renderPeek({ dag: 'etl.yaml' });

    expect(screen.getByTestId('dag-actions')).toBeInTheDocument();
  });

  describe('of a run', () => {
    beforeEach(() => {
      h.run = {
        data: run('run-0', { status: Status.Success, error: '' }),
        error: null,
      };
      h.history = { dagRuns: [run('run-1'), run('run-0')] };
    });

    it('shows the run, and the recent runs with the shown one current', () => {
      const { onSelectRun } = renderPeek({ dag: 'etl', run: 'run-0' });

      expect(h.runTargets).toContainEqual({
        remoteNode: 'local',
        name: 'etl',
        dagRunId: 'run-0',
      });
      expect(screen.getByRole('heading', { name: 'Run' })).toBeVisible();
      const recent = screen
        .getByRole('heading', { name: 'Recent runs' })
        .closest('section')!;
      const [newer, shown] = within(recent).getAllByRole('button');
      expect(shown).toHaveAttribute('aria-current', 'true');
      expect(newer).not.toHaveAttribute('aria-current');
      fireEvent.click(newer!);
      expect(onSelectRun).toHaveBeenCalledWith(
        expect.objectContaining({ dagRunId: 'run-1' })
      );
    });

    // A run names its workflow by its DAG name; the run gives the file.
    it('reads the workflow from the source file of the run', () => {
      renderPeek({ dag: 'etl', run: 'run-0' });

      expect(h.queries).toContainEqual({
        path: '/dags/{fileName}',
        init: {
          params: {
            query: { remoteNode: 'local' },
            path: { fileName: 'etl.yaml' },
          },
        },
      });
      expect(openLink('Open run')).toHaveAttribute(
        'href',
        '/dags/etl?remoteNode=local&run=run-0'
      );
    });

    it('shows a run of the history while its own read loads', () => {
      h.run = { data: null, error: null };

      renderPeek({ dag: 'etl', run: 'run-1' });

      expect(screen.getByTestId('dag-run-error')).toHaveTextContent(
        'load failed'
      );
    });

    it('says that the run loads', () => {
      h.run = { data: null, error: null };
      h.history = undefined;

      renderPeek({ dag: 'etl', run: 'run-9' });

      expect(screen.getByRole('status')).toHaveTextContent('Loading run...');
    });

    it('reports a run that does not exist', () => {
      h.run = { data: null, error: { status: 404, message: 'not found' } };
      h.history = undefined;

      renderPeek({ dag: 'etl', run: 'run-9' });

      expect(screen.getByText('Run not found')).toBeInTheDocument();
    });

    it('keeps Open for a run whose workflow file is gone', () => {
      h.workflow = { error: { status: 404, message: 'not found' } };
      h.history = undefined;

      renderPeek({ dag: 'etl', run: 'run-0' });

      expect(screen.getByRole('heading', { name: 'Run' })).toBeVisible();
      expect(screen.queryByTestId('canvas')).not.toBeInTheDocument();
      expect(openLink('Open run')).toHaveAttribute(
        'href',
        '/dags/etl?remoteNode=local&run=run-0'
      );
    });
  });
});
