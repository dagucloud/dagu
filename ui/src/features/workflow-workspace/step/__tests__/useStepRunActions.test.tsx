// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import type { ReactNode } from 'react';
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigationType,
} from 'react-router-dom';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
  type components,
} from '@/api/v1/schema';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import { SubRunOpenProvider } from '@/features/dags/components/common/SubRunStackModal';
import type { RunOverlay } from '@/features/visual-editor/contracts';
import { WorkspaceDataContext } from '../../data/context';
import { nodesByStep } from '../../data/runFacts';
import type {
  DAGPageData,
  WorkspacePermissions,
  WorkspaceRunValue,
} from '../../data/types';
import type { ReuseSourceState } from '../useReuseSource';
import { useStepRunActions, type StepRunActions } from '../useStepRunActions';

type Node = components['schemas']['Node'];
type Details = components['schemas']['DAGRunDetails'];

const api = vi.hoisted(() => ({
  get: vi.fn(),
  patch: vi.fn(),
  post: vi.fn(),
}));
const perms = vi.hoisted(() => ({
  value: {} as WorkspacePermissions,
}));
const dock = vi.hoisted(() => ({ openStep: vi.fn() }));
const editor = vi.hoisted(() => ({
  handle: null as null | {
    getBuffer(): string;
    isDirty(): boolean;
    /** The definition that the server built from the buffer. */
    getDag(): unknown;
  },
}));
const editorRun = vi.hoisted(() => ({
  args: null as null | Record<string, unknown>,
  runStep: vi.fn(),
  runSteps: vi.fn(),
  /** True: the real hook runs, so a test can read the request body. */
  real: false,
}));
const reuse = vi.hoisted(() => ({
  value: null as unknown,
  calls: [] as Record<string, unknown>[],
}));
const errors = vi.hoisted(() => ({ showError: vi.fn() }));
const testRunNoted = vi.hoisted(() => vi.fn());

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ GET: api.get, PATCH: api.patch, POST: api.post }),
  useQuery: () => ({ data: undefined, isLoading: false, mutate: vi.fn() }),
}));
vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ apiURL: '/api/v1', permissions: { runDags: true } }),
}));
vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: errors.showError }),
}));
vi.mock('../../data/useRunHistory', () => ({
  noteTestRunStarted: testRunNoted,
}));
vi.mock('../../data/useWorkspacePermissions', () => ({
  useWorkspacePermissions: () => perms.value,
}));
vi.mock('../../dock/DockContext', () => ({
  useDock: () => ({ openStep: dock.openStep }),
  useDockCommands: () => ({ openStep: dock.openStep }),
}));
vi.mock('../../canvas/WorkspaceEditorContext', () => ({
  useWorkspaceEditor: () => ({ handle: editor.handle, state: null }),
}));
vi.mock('@/features/visual-editor/run/useEditorRunActions', async () => {
  const actual = await vi.importActual<
    typeof import('@/features/visual-editor/run/useEditorRunActions')
  >('@/features/visual-editor/run/useEditorRunActions');
  return {
    useEditorRunActions: (
      args: Parameters<typeof actual.useEditorRunActions>[0]
    ) => {
      editorRun.args = args as unknown as Record<string, unknown>;
      if (editorRun.real) return actual.useEditorRunActions(args);
      return {
        runDag: vi.fn(),
        runDraft: vi.fn(),
        runStep: editorRun.runStep,
        runSteps: editorRun.runSteps,
        isStarting: false,
        error: null,
      };
    },
  };
});
// The choice of the source and the stale steps have their own tests
// (useReuseSource.test.tsx); here the dialog gets a given state.
vi.mock('../useReuseSource', () => ({
  useReuseSource: (p: Record<string, unknown>) => {
    reuse.calls.push(p);
    return reuse.value;
  },
}));

/** A fresh source: run-1 holds every output and nothing changed. */
function freshReuse(extra: Partial<ReuseSourceState> = {}): ReuseSourceState {
  const value: ReuseSourceState = {
    source: {
      dagRunId: 'run-1',
      status: Status.Success,
      startedAt: '2026-10-01T00:00:00Z',
    } as ReuseSourceState['source'],
    options: [],
    selectedOptionId: null,
    choose: vi.fn(),
    waitingFor: null,
    stale: [],
    missing: [],
    cached: [],
    cachedRuns: {},
    gone: [],
    stepRuns: {},
    cacheCapturedAt: null,
    cacheTimes: {},
    headChanged: false,
    paramsDiffer: false,
    loading: false,
    resolveRun: () => null,
    ...extra,
  };
  // The picker lists the source run, unless the test gives its own lines.
  if (extra.options === undefined && value.source) {
    value.options = [
      {
        id: `run:${value.source.dagRunId}`,
        kind: 'run',
        runId: value.source.dagRunId,
        status: value.source.status,
        ...(value.source.startedAt
          ? { startedAt: value.source.startedAt }
          : {}),
        auto: true,
      },
    ];
    value.selectedOptionId = `run:${value.source.dagRunId}`;
  }
  return value;
}

/** The buttons of the footer; the run picker and the source link are not. */
function footerButtons(): HTMLElement[] {
  return within(screen.getByTestId('step-run-dialog'))
    .getAllByRole('button')
    .filter(
      (button) =>
        button.dataset.testid !== 'run-source-link' &&
        button.dataset.testid !== 'run-source-picker'
    );
}

function node(name: string, extra: Partial<Node> = {}): Node {
  return {
    step: { name },
    status: NodeStatus.Success,
    statusLabel: NodeStatusLabel.succeeded,
    stdout: '/logs/out',
    stderr: '',
    startedAt: '2026-10-01T00:00:00Z',
    finishedAt: '2026-10-01T00:00:05Z',
    retryCount: 0,
    doneCount: 1,
    ...extra,
  } as Node;
}

const NODES: Node[] = [
  node('build'),
  node('call-one', {
    step: { name: 'call-one', call: 'child' } as Node['step'],
    subRuns: [{ dagRunId: 'c-1', dagName: 'child', params: 'A=1' }],
  }),
  node('fan', {
    step: { name: 'fan', call: 'worker' } as Node['step'],
    subRuns: [
      { dagRunId: 'w-1', dagName: 'worker', params: 'N=1' },
      { dagRunId: 'w-2', dagName: 'worker', params: 'N=2' },
    ],
  }),
  node('task', {
    step: { name: 'task', humanTask: {} } as Node['step'],
  }),
];

function details(status: Status = Status.Success): Details {
  return {
    name: 'example',
    dagRunId: 'run-1',
    rootDAGRunName: 'example',
    rootDAGRunId: 'run-1',
    status,
    statusLabel: StatusLabel.succeeded,
    startedAt: '2026-10-01T00:00:00Z',
    finishedAt: '2026-10-01T00:01:00Z',
    autoRetryCount: 0,
    nodes: NODES,
  } as Details;
}

function runValue(d: Details): WorkspaceRunValue {
  return {
    selection: 'latest',
    ref: { name: d.name, runId: d.dagRunId },
    details: d,
    hasRun: true,
    isLatest: true,
    isActive: false,
    notFound: false,
    latestRef: null,
    overlay: {} as RunOverlay,
    nodeByStep: nodesByStep(d),
    handlers: [],
    waiting: { approvals: 0, humanTasks: 0, agent: 0, firstWaitingStep: null },
    failedStep: null,
    select: vi.fn(),
    applyNodeStatus: vi.fn(),
    refresh: vi.fn().mockResolvedValue(undefined),
  };
}

/** The run of the page when ?run pins it. */
function pinnedRunValue(d: Details): WorkspaceRunValue {
  return {
    ...runValue(d),
    selection: { runId: d.dagRunId },
    isLatest: false,
  };
}

/** The steps of the DAG of the page: the steps of the file, with their depends. */
let dagSteps:
  | {
      name: string;
      id?: string;
      output?: string;
      run?: string;
      depends?: string[];
    }[]
  | undefined;
let actions: StepRunActions;
let location: ReturnType<typeof useLocation>;
/** Every navigation type that the router saw, oldest first. */
let navigations: string[] = [];

function LocationProbe() {
  location = useLocation();
  const type = useNavigationType();
  if (navigations[navigations.length - 1] !== `${type}:${location.key}`) {
    navigations.push(`${type}:${location.key}`);
  }
  return null;
}

let probeStay = false;

function Probe() {
  actions = useStepRunActions({ stay: probeStay });
  return <>{actions.dialogs}</>;
}

function renderActions(
  run: WorkspaceRunValue,
  openOnStack = vi.fn(),
  url = '/'
): { openOnStack: ReturnType<typeof vi.fn>; container: HTMLElement } {
  const wrap = (children: ReactNode) => (
    <MemoryRouter initialEntries={[url]}>
      <LocationProbe />
      <RemoteNodeProvider remoteNode="edge">
        <WorkspaceDataContext.Provider
          value={{
            fileName: 'example.yaml',
            dagData: {
              dag: { name: 'example', steps: dagSteps },
            } as DAGPageData,
            workspaceName: null,
            remoteNode: 'edge',
            run,
            refresh: vi.fn(),
            registerHistory: () => () => undefined,
          }}
        >
          <SubRunOpenProvider value={openOnStack}>
            {children}
          </SubRunOpenProvider>
        </WorkspaceDataContext.Provider>
      </RemoteNodeProvider>
    </MemoryRouter>
  );
  const { container } = render(
    wrap(
      <Routes>
        <Route path="/dags/:fileName/:tab" element={<Probe />} />
        <Route path="/dags/:fileName" element={<Probe />} />
        <Route path="*" element={<Probe />} />
      </Routes>
    )
  );
  return { openOnStack, container };
}

beforeEach(() => {
  api.get.mockReset().mockResolvedValue({ data: undefined });
  api.patch.mockReset().mockResolvedValue({ error: undefined });
  api.post.mockReset().mockResolvedValue({ error: undefined });
  perms.value = {
    canWrite: true,
    canRunDags: true,
    canExecute: true,
    canExecuteForWorkspace: true,
    isAdmin: false,
    canManageProfiles: false,
    canManageWebhooks: false,
    canManageNotifications: false,
  };
  dock.openStep.mockReset();
  editor.handle = null;
  editorRun.args = null;
  editorRun.real = false;
  reuse.value = freshReuse();
  reuse.calls = [];
  editorRun.runStep.mockReset().mockResolvedValue('run-2');
  editorRun.runSteps.mockReset().mockResolvedValue('run-2');
  errors.showError.mockReset();
  testRunNoted.mockClear();
  dagSteps = undefined;
  probeStay = false;
  navigations = [];
});

describe('useStepRunActions gates', () => {
  it('opens retry and set status on a finished run when runDags allows them', () => {
    renderActions(runValue(details()));

    expect(actions.can('build')).toEqual({
      retry: true,
      setStatus: true,
      runStep: true,
      subRuns: false,
    });
    expect(actions.can('call-one').subRuns).toBe(true);
    expect(actions.can('fan').subRuns).toBe(true);
  });

  it('closes retry and set status without runDags', () => {
    perms.value = { ...perms.value, canRunDags: false };
    renderActions(runValue(details()));

    expect(actions.can('build')).toMatchObject({
      retry: false,
      setStatus: false,
      runStep: true,
      reason: 'You do not have permission to run workflows.',
    });
  });

  // canRetryStep offers the retry of a running run, but its dialog cannot start it.
  it('keeps set status closed while the run is active', () => {
    renderActions(runValue(details(Status.Running)));

    expect(actions.can('build')).toMatchObject({
      retry: true,
      setStatus: false,
      reason: 'Retry unavailable while the DAG run is running.',
    });
  });

  it('closes set status on a human task step', () => {
    renderActions(runValue(details()));

    expect(actions.can('task')).toMatchObject({
      retry: false,
      setStatus: false,
    });
  });

  it('gives run step from canExecute only', () => {
    perms.value = { ...perms.value, canExecute: false };
    renderActions(runValue(details()));

    expect(actions.can('build').runStep).toBe(false);
    expect(actions.can('missing')).toEqual({
      retry: false,
      setStatus: false,
      runStep: false,
      subRuns: false,
    });
  });
});

describe('useStepRunActions dialogs', () => {
  // parity:run-step-change-status
  it('sets a status, patches the run node and then refreshes', async () => {
    const run = runValue(details());
    renderActions(run);

    act(() => actions.setStatus('build'));
    expect(screen.getByText('Update status')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: /Success/ }));

    await waitFor(() => expect(run.refresh).toHaveBeenCalledTimes(1));
    expect(api.patch).toHaveBeenCalledWith(
      '/dag-runs/{name}/{dagRunId}/steps/{stepName}/status',
      {
        params: {
          path: { name: 'example', dagRunId: 'run-1', stepName: 'build' },
          query: { remoteNode: 'edge' },
        },
        body: { status: NodeStatus.Success },
      }
    );
    expect(run.applyNodeStatus).toHaveBeenCalledWith(
      'build',
      NodeStatus.Success
    );
    const applyOrder = vi.mocked(run.applyNodeStatus).mock
      .invocationCallOrder[0]!;
    const refreshOrder = vi.mocked(run.refresh).mock.invocationCallOrder[0]!;
    expect(applyOrder).toBeLessThan(refreshOrder);
    await waitFor(() =>
      expect(screen.queryByText('Update status')).not.toBeInTheDocument()
    );
  });

  it('keeps the node unchanged when the status request fails', async () => {
    api.patch.mockResolvedValue({ error: { message: 'boom' } });
    const run = runValue(details());
    renderActions(run);

    act(() => actions.setStatus('build'));
    fireEvent.click(screen.getByRole('button', { name: /Failed/ }));

    await waitFor(() =>
      expect(errors.showError).toHaveBeenCalledWith(
        'boom',
        'Please try again or check the server connection.'
      )
    );
    expect(run.applyNodeStatus).not.toHaveBeenCalled();
    expect(run.refresh).not.toHaveBeenCalled();
  });

  it('opens no status dialog when the gate is closed', () => {
    renderActions(runValue(details(Status.Running)));

    act(() => actions.setStatus('build'));

    expect(screen.queryByText('Update status')).not.toBeInTheDocument();
  });

  it('retries a step through StepRetryDialog and refreshes the run', async () => {
    const run = runValue(details());
    renderActions(run);

    act(() => actions.retryStep('build'));
    expect(screen.getByText('Retry this step?')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));

    await waitFor(() => expect(run.refresh).toHaveBeenCalled());
    expect(api.post).toHaveBeenCalledWith(
      '/dag-runs/{name}/{dagRunId}/retry',
      expect.objectContaining({
        body: { dagRunId: 'run-1', stepName: 'build' },
      })
    );
  });

  // A step with no upstream step runs alone, on no earlier output.
  // parity:run-single-step
  it('runs one step from the editor buffer after a confirmation', async () => {
    editor.handle = {
      getBuffer: () => 'steps: [draft]',
      isDirty: () => true,
      getDag: () => undefined,
    };
    dagSteps = [{ name: 'build' }];
    const run = runValue(details());
    renderActions(run, vi.fn(), '/dags/example.yaml');

    act(() => actions.runStep('build'));

    expect(
      screen.getByText(
        'Run only the step build now? Its command runs for real.'
      )
    ).toBeVisible();
    // The dialog does not repeat that a new run starts.
    expect(screen.queryByText(/This creates a new run/)).toBeNull();
    expect(
      screen.getByText('The step runs from the unsaved editor text.')
    ).toBeVisible();
    expect(editorRun.args).toMatchObject({
      fileName: 'example.yaml',
      dagName: 'example',
      spec: 'steps: [draft]',
      isDirty: true,
    });
    expect(editorRun.runSteps).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Run' }));

    await waitFor(() => expect(editorRun.runSteps).toHaveBeenCalled());
    // The unsaved text makes a test run: it is selectable while it exists.
    await waitFor(() => expect(location.search).toContain('run=run-2'));
    expect(editorRun.runSteps).toHaveBeenCalledWith(['build'], undefined);
  });

  // Run runs the steps that the step needs, in dependency order, then the
  // step, and it reuses no output of an earlier run.
  it('runs the previous steps and then the step', async () => {
    dagSteps = [
      { name: 'build' },
      { name: 'unrelated' },
      { name: 'test', depends: ['build'] },
      { name: 'lint' },
      { name: 'ship', depends: ['test', 'lint'] },
    ];
    renderActions(runValue(details()), vi.fn(), '/dags/example.yaml');

    act(() => actions.runStep('ship'));

    expect(
      screen.getAllByTestId('run-step-item').map((item) => item.textContent)
    ).toEqual(['buildreused', 'testreused', 'lintreused']);
    expect(editorRun.runSteps).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Run' }));

    await waitFor(() => expect(editorRun.runSteps).toHaveBeenCalled());
    // A start selects the new run in the URL.
    await waitFor(() => expect(location.search).toContain('run=run-2'));
    expect(editorRun.runSteps).toHaveBeenCalledWith(
      ['build', 'test', 'lint', 'ship'],
      undefined
    );
    expect(editorRun.runStep).not.toHaveBeenCalled();
  });

  // Step only runs the step alone, on the outputs of the reuse source. With
  // nothing changed it has three buttons and Step only is the default.
  it('runs one step on the outputs of the source with Step only', async () => {
    dagSteps = [{ name: 'build' }, { name: 'test', depends: ['build'] }];
    renderActions(runValue(details()), vi.fn(), '/dags/example.yaml');

    act(() => actions.runStep('test'));

    expect(screen.getByRole('dialog', { name: 'Run step' })).toBeVisible();
    expect(footerButtons().map((button) => button.textContent)).toEqual([
      'Cancel',
      'Step only',
      'Run',
    ]);
    expect(screen.getByRole('button', { name: 'Step only' })).toHaveFocus();
    fireEvent.click(screen.getByRole('button', { name: 'Step only' }));

    await waitFor(() => expect(editorRun.runSteps).toHaveBeenCalled());
    // A start selects the new run in the URL.
    await waitFor(() => expect(location.search).toContain('run=run-2'));
    expect(editorRun.runSteps).toHaveBeenCalledWith(
      ['test'],
      expect.objectContaining({ reuseOutputsFrom: 'run-1' })
    );
  });

  // The steps that the step needs, not the step: the request lists them
  // after the steps that they need, and no output is reused.
  it('runs only the upstream steps of a step after a confirmation', async () => {
    dagSteps = [
      { name: 'ship', depends: ['test', 'lint'] },
      { name: 'test', depends: ['build'] },
      { name: 'build' },
      { name: 'lint' },
    ];
    renderActions(runValue(details()), vi.fn(), '/dags/example.yaml');

    act(() => actions.runUpstream('ship'));

    // Three steps are upstream: build and lint directly or through test.
    expect(
      screen.getByText(
        'Run the 3 upstream steps of ship now? Their commands run for real.'
      )
    ).toBeVisible();
    expect(editorRun.runSteps).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Run' }));

    await waitFor(() => expect(editorRun.runSteps).toHaveBeenCalled());
    // A start selects the new run in the URL.
    await waitFor(() => expect(location.search).toContain('run=run-2'));
    expect(editorRun.runSteps).toHaveBeenCalledWith(
      ['build', 'test', 'lint'],
      undefined
    );
    expect(editorRun.runStep).not.toHaveBeenCalled();
  });

  it('asks nothing when the step has no upstream step', () => {
    dagSteps = [{ name: 'build' }, { name: 'test', depends: ['build'] }];
    renderActions(runValue(details()), vi.fn(), '/dags/example.yaml');

    act(() => actions.runUpstream('build'));

    expect(screen.queryByRole('button', { name: 'Run' })).toBeNull();
  });

  it('forgets the upstream steps when the next run is for the step alone', async () => {
    dagSteps = [{ name: 'build' }, { name: 'test', depends: ['build'] }];
    renderActions(runValue(details()), vi.fn(), '/dags/example.yaml');

    act(() => actions.runUpstream('test'));
    act(() => actions.runStep('test'));

    expect(screen.getByRole('button', { name: 'Step only' })).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Step only' }));
    await waitFor(() => expect(editorRun.runSteps).toHaveBeenCalledTimes(1));
    expect(editorRun.runSteps).toHaveBeenCalledWith(
      ['test'],
      expect.objectContaining({ reuseOutputsFrom: 'run-1' })
    );
  });

  describe('in the editor (stay)', () => {
    beforeEach(() => {
      probeStay = true;
      dagSteps = [{ name: 'build' }, { name: 'test', depends: ['build'] }];
    });

    // The user stays on the step: the URL keeps its path, its step and its
    // node view, and the hook gives the run to follow.
    it('follows an upstream run without leaving the editor', async () => {
      renderActions(
        runValue(details()),
        vi.fn(),
        '/dags/example.yaml?step=test&ndv=expanded'
      );

      act(() => actions.runUpstream('test'));
      fireEvent.click(screen.getByRole('button', { name: 'Run' }));

      await waitFor(() =>
        expect(actions.followed).toEqual([
          expect.objectContaining({
            kind: 'upstream',
            step: 'test',
            runId: 'run-2',
            steps: ['build'],
          }),
        ])
      );
      expect(editorRun.runSteps).toHaveBeenCalledWith(['build'], undefined);
      expect(location.pathname).toBe('/dags/example.yaml');
      await waitFor(() =>
        expect(location.search).toBe('?run=run-2&step=test&ndv=expanded')
      );
    });

    it('follows the run of Step only without leaving the editor', async () => {
      renderActions(
        runValue(details()),
        vi.fn(),
        '/dags/example.yaml?step=test&ndv=expanded'
      );

      act(() => actions.runStep('test'));
      fireEvent.click(screen.getByRole('button', { name: 'Step only' }));

      await waitFor(() =>
        expect(actions.followed).toEqual([
          expect.objectContaining({
            kind: 'execute',
            step: 'test',
            runId: 'run-2',
            steps: ['test'],
          }),
        ])
      );
      expect(editorRun.runSteps).toHaveBeenCalledWith(
        ['test'],
        expect.objectContaining({ reuseOutputsFrom: 'run-1' })
      );
      expect(location.pathname).toBe('/dags/example.yaml');
      await waitFor(() =>
        expect(location.search).toBe('?run=run-2&step=test&ndv=expanded')
      );
    });

    // Run follows the run as the button of the upstream steps does: the
    // INPUT column follows the previous steps, the OUTPUT column the step.
    it('follows the run of Run from the node view', async () => {
      renderActions(
        runValue(details()),
        vi.fn(),
        '/dags/example.yaml?step=test&ndv=expanded'
      );

      act(() => actions.runStep('test'));
      fireEvent.click(screen.getByRole('button', { name: 'Run' }));

      await waitFor(() =>
        expect(
          actions.followed.map((run) => [run.kind, run.step, run.steps])
        ).toEqual([
          ['upstream', 'test', ['build']],
          ['execute', 'test', ['test']],
        ])
      );
      expect(editorRun.runSteps).toHaveBeenCalledWith(
        ['build', 'test'],
        undefined
      );
      expect(location.pathname).toBe('/dags/example.yaml');
      await waitFor(() =>
        expect(location.search).toBe('?run=run-2&step=test&ndv=expanded')
      );
    });

    it('follows the run of the graph Run step when its node view is open', async () => {
      renderActions(
        runValue(details()),
        vi.fn(),
        '/dags/example.yaml?step=test&ndv=docked'
      );

      act(() => actions.runStep('test'));
      fireEvent.click(screen.getByRole('button', { name: 'Run' }));

      await waitFor(() => expect(actions.followed).toHaveLength(2));
      expect(location.pathname).toBe('/dags/example.yaml');
    });

    // The node view is open on another step: its columns follow nothing, the
    // page stays where it is.
    it('follows no run when the node view shows another step', async () => {
      renderActions(
        runValue(details()),
        vi.fn(),
        '/dags/example.yaml?step=build&ndv=expanded'
      );

      act(() => actions.runStep('test'));
      fireEvent.click(screen.getByRole('button', { name: 'Run' }));

      await waitFor(() => expect(editorRun.runSteps).toHaveBeenCalled());
      expect(location.pathname).toBe('/dags/example.yaml');
      await waitFor(() =>
        expect(location.search).toBe('?run=run-2&step=build&ndv=expanded')
      );
      expect(actions.followed).toEqual([]);
    });

    it('follows no run for the play button of a node', async () => {
      renderActions(
        runValue(details()),
        vi.fn(),
        '/dags/example.yaml?step=test'
      );

      act(() => actions.runStep('test'));
      fireEvent.click(screen.getByRole('button', { name: 'Run' }));

      await waitFor(() =>
        expect(editorRun.runSteps).toHaveBeenCalledWith(
          ['build', 'test'],
          undefined
        )
      );
      expect(location.pathname).toBe('/dags/example.yaml');
      await waitFor(() => expect(location.search).toBe('?run=run-2&step=test'));
      expect(actions.followed).toEqual([]);
    });

    // The run of the page is pinned: the new run replaces it, the node view
    // stays open on its step and its columns follow the new run.
    it('pins the new run with replace and keeps the followed run', async () => {
      renderActions(
        pinnedRunValue(details()),
        vi.fn(),
        '/dags/example.yaml?run=run-1&step=test&ndv=expanded'
      );
      const before = navigations.length;

      act(() => actions.runStep('test'));
      fireEvent.click(screen.getByRole('button', { name: 'Step only' }));

      await waitFor(() => expect(location.search).toContain('run=run-2'));
      expect(location.search).toBe('?run=run-2&step=test&ndv=expanded');
      expect(navigations.slice(before).map((n) => n.split(':')[0])).toEqual([
        'REPLACE',
      ]);
      expect(actions.followed).toEqual([
        expect.objectContaining({ kind: 'execute', runId: 'run-2' }),
      ]);
    });

    // The data of the INPUT column stays when Step only runs, and the
    // other way round: the list keeps the last run of each kind.
    it('keeps the last run of each kind', async () => {
      editorRun.runSteps
        .mockResolvedValueOnce('run-2')
        .mockResolvedValueOnce('run-3')
        .mockResolvedValueOnce('run-4');
      renderActions(
        runValue(details()),
        vi.fn(),
        '/dags/example.yaml?step=test&ndv=expanded'
      );
      for (const [start, button] of [
        [() => actions.runUpstream('test'), 'Run'],
        [() => actions.runStep('test'), 'Step only'],
        [() => actions.runUpstream('test'), 'Run'],
      ] as const) {
        act(start);
        fireEvent.click(screen.getByRole('button', { name: button }));
        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
      }
      await waitFor(() =>
        expect(actions.followed.map((run) => [run.kind, run.runId])).toEqual([
          ['execute', 'run-3'],
          ['upstream', 'run-4'],
        ])
      );
    });

    it('forgets the followed run on clearFollowed', async () => {
      renderActions(
        runValue(details()),
        vi.fn(),
        '/dags/example.yaml?step=test&ndv=expanded'
      );
      act(() => actions.runStep('test'));
      fireEvent.click(screen.getByRole('button', { name: 'Step only' }));
      await waitFor(() => expect(actions.followed).toHaveLength(1));

      act(() => actions.clearFollowed());

      expect(actions.followed).toEqual([]);
    });

    it('follows no run when the start fails', async () => {
      editorRun.runSteps.mockResolvedValue(null);
      renderActions(
        runValue(details()),
        vi.fn(),
        '/dags/example.yaml?step=test&ndv=expanded'
      );

      act(() => actions.runStep('test'));
      fireEvent.click(screen.getByRole('button', { name: 'Run' }));

      await waitFor(() => expect(editorRun.runSteps).toHaveBeenCalled());
      await act(() => editorRun.runSteps.mock.results[0]!.value);
      expect(actions.followed).toEqual([]);
    });
  });

  // The node view is on z-[60]: the question and its overlay sit above it, so
  // that the Run button can be clicked while the node view is open.
  it('puts the run confirmation above the node view', () => {
    renderActions(runValue(details()));

    act(() => actions.runStep('build'));

    expect(screen.getByTestId('step-run-dialog')).toHaveClass('z-[80]');
    expect(screen.getByTestId('step-run-overlay')).toHaveClass('z-[80]');
    expect(screen.getByRole('button', { name: 'Run' })).toBeVisible();
  });

  // D11: a pinned run gives way to the new run with replace, and the node
  // view and the step stay. No mode is ever written.
  it('replaces a pinned ?run and writes no mode', async () => {
    renderActions(
      pinnedRunValue(details()),
      vi.fn(),
      '/dags/example.yaml?run=run-1&step=build&ndv=expanded&remoteNode=edge'
    );
    const before = navigations.length;

    act(() => actions.runStep('build'));
    fireEvent.click(screen.getByRole('button', { name: 'Run' }));

    await waitFor(() => expect(location.search).toContain('run=run-2'));
    expect(location.pathname).toBe('/dags/example.yaml');
    const params = new URLSearchParams(location.search);
    expect(params.get('run')).toBe('run-2');
    expect(params.get('step')).toBe('build');
    expect(params.get('ndv')).toBe('expanded');
    expect(params.get('remoteNode')).toBe('edge');
    expect(navigations.slice(before).map((n) => n.split(':')[0])).toEqual([
      'REPLACE',
    ]);
  });

  // The URL always names the shown run, also when it is the latest one: the
  // new run replaces it, because the page does not follow a newer run by itself.
  it('replaces the run of the URL while the shown run is the latest', async () => {
    renderActions(
      runValue(details()),
      vi.fn(),
      '/dags/example.yaml?step=build&ndv=expanded&remoteNode=edge'
    );
    const before = navigations.length;

    act(() => actions.runStep('build'));
    fireEvent.click(screen.getByRole('button', { name: 'Run' }));

    await waitFor(() => expect(editorRun.runSteps).toHaveBeenCalled());
    expect(location.pathname).toBe('/dags/example.yaml');
    await waitFor(() => expect(location.search).toContain('run=run-2'));
    expect(location.search).toBe(
      '?run=run-2&step=build&ndv=expanded&remoteNode=edge'
    );
    expect(navigations.slice(before).map((n) => n.split(':')[0])).toEqual([
      'REPLACE',
    ]);
  });

  it('asks nothing when the user cannot execute', () => {
    perms.value = { ...perms.value, canExecute: false };
    renderActions(runValue(details()));

    act(() => actions.runStep('build'));

    expect(screen.queryByText('Run step')).not.toBeInTheDocument();
  });

  it('renders no dialog until an action opens one', () => {
    const { container } = renderActions(runValue(details()));

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(container).toBeEmptyDOMElement();
  });
});

describe('useStepRunActions sub-runs and logs', () => {
  it('opens a single child run on the sub-run stack', () => {
    const { openOnStack } = renderActions(runValue(details()));

    act(() => actions.openSubRun('call-one'));

    expect(openOnStack).toHaveBeenCalledWith({
      name: 'child',
      dagRunId: 'c-1',
    });
  });

  it('opens a single child run page in a new tab', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null);
    const { openOnStack } = renderActions(runValue(details()));

    act(() => actions.openSubRun('call-one', { newTab: true }));

    expect(openOnStack).not.toHaveBeenCalled();
    expect(open).toHaveBeenCalledWith(
      '/dags/example?remoteNode=edge&run=run-1&subRun=c-1',
      '_blank'
    );
    open.mockRestore();
  });

  it('picks one of several child runs in ParallelExecutionModal', () => {
    const { openOnStack } = renderActions(runValue(details()));

    act(() => actions.openSubRun('fan'));
    expect(screen.getByText('2 sub DAG-runs')).toBeVisible();
    fireEvent.click(screen.getByText('N=2'));

    expect(openOnStack).toHaveBeenCalledWith({
      name: 'worker',
      dagRunId: 'w-2',
    });
    expect(screen.queryByText('2 sub DAG-runs')).not.toBeInTheDocument();
  });

  it('opens a step log in the dock', () => {
    renderActions(runValue(details()));

    act(() => actions.openLogs({ step: 'build', stream: 'stderr' }));

    expect(dock.openStep).toHaveBeenCalledWith({
      step: 'build',
      stream: 'stderr',
    });
  });
});

// The step reads the outputs of other steps: Run includes them with the
// steps that they need; Step only and Changed steps reuse the source run.
describe('useStepRunActions run with references', () => {
  const merge =
    'echo ${steps.extract_orders.outputs.orders} ${steps.extract_users.outputs.users}';
  const etl = () => [
    { name: 'seed' },
    { name: 'extract orders', id: 'extract_orders', depends: ['seed'] },
    { name: 'extract users', id: 'extract_users' },
    {
      name: 'merge',
      run: merge,
      depends: ['extract orders', 'extract users'],
    },
  ];
  const open = async (step = 'merge', run = runValue(details())) => {
    renderActions(run, vi.fn(), '/dags/example.yaml');
    act(() => actions.runStep(step));
    return screen.findByTestId('step-run-dialog');
  };
  const buttons = () => footerButtons().map((button) => button.textContent);
  const items = () =>
    screen.getAllByTestId('run-step-item').map((item) => item.textContent);

  it('has a single Run when the step has no previous step and reads none', async () => {
    dagSteps = [{ name: 'a' }, { name: 'b', run: 'echo hi' }];
    await open('b');
    expect(screen.queryByRole('button', { name: 'Step only' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Run' })).toBeVisible();
    expect(screen.getByRole('button', { name: 'Run' })).toHaveFocus();
    expect(screen.queryByTestId('run-steps-list')).toBeNull();
    expect(reuse.calls).toEqual([]);
  });

  it('gives the hook the previous steps, the reads and the editor text', async () => {
    dagSteps = etl();
    editor.handle = {
      getBuffer: () => 'steps: [live]',
      isDirty: () => false,
      getDag: () => undefined,
    };
    await open();
    expect(reuse.calls[reuse.calls.length - 1]).toEqual({
      fileName: 'example.yaml',
      step: 'merge',
      previous: ['seed', 'extract orders', 'extract users'],
      reads: ['extract orders', 'extract users'],
      followedUpstreamRunId: null,
      liveText: 'steps: [live]',
    });
  });

  it('lists the previous steps, the steps that they need first', async () => {
    dagSteps = etl();
    await open();
    expect(items()).toEqual([
      'seedreused',
      'extract ordersreused',
      'extract usersreused',
    ]);
    expect(screen.queryByRole('checkbox')).toBeNull();
    // A long list scrolls; no name is cut away.
    expect(screen.getByTestId('run-steps-list')).toHaveClass('overflow-y-auto');
  });

  it('names the source in one line with a run picker and no stale text', async () => {
    dagSteps = etl();
    await open();
    const line = screen.getByTestId('run-source');
    expect(line).toHaveTextContent(/^Inputs from/);
    expect(within(line).getByTestId('run-source-picker')).toHaveTextContent(
      'run-1'
    );
    expect(screen.queryByText(/This creates a new run/)).toBeNull();
    expect(screen.queryByText(/does not hold every output/)).toBeNull();
  });

  // The bar, the dock and the dialog show a run ID in one short form: the
  // first 8 characters, the whole ID as title.
  it('shows the source run ID in its short form with the whole ID as title', async () => {
    dagSteps = etl();
    reuse.value = freshReuse({
      source: {
        dagRunId: '0123456789abcdef',
        status: Status.Success,
        startedAt: '2026-10-01T00:00:00Z',
      } as ReuseSourceState['source'],
    });
    await open();
    const picker = screen.getByTestId('run-source-picker');
    expect(picker).toHaveTextContent('01234567');
    expect(picker).not.toHaveTextContent('0123456789');
    expect(within(picker).getByTitle('0123456789abcdef')).toBeVisible();
    expect(screen.getByTestId('run-source-link')).toHaveAttribute(
      'title',
      '0123456789abcdef'
    );
  });

  // The user chooses the run that the inputs come from; the hook keeps it.
  it('gives the choice of the picker to the reuse source', async () => {
    dagSteps = etl();
    const choose = vi.fn();
    reuse.value = freshReuse({
      choose,
      options: [
        {
          id: 'run:run-1',
          kind: 'run',
          runId: 'run-1',
          auto: true,
          status: Status.Success,
        },
        {
          id: 'run:run-0',
          kind: 'run',
          runId: 'run-0',
          status: Status.Success,
        },
        {
          id: 'run:run--1',
          kind: 'run',
          runId: 'run--1',
          status: Status.Success,
          blocked: { reason: 'missing', steps: ['seed'] },
        },
      ],
      selectedOptionId: 'run:run-1',
    });
    await open();
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    await user.click(screen.getByTestId('run-source-picker'));
    expect(
      await screen.findByTestId('run-source-option-run:run--1')
    ).toHaveAttribute('data-disabled');
    await user.click(screen.getByTestId('run-source-option-run:run-0'));
    expect(choose).toHaveBeenCalledWith(
      expect.objectContaining({ runId: 'run-0' })
    );
  });

  // The link selects the run with a pushed ?run and closes the question.
  it('selects the source run from its link', async () => {
    dagSteps = etl();
    renderActions(
      runValue(details()),
      vi.fn(),
      '/dags/example.yaml?step=merge'
    );
    act(() => actions.runStep('merge'));
    const before = navigations.length;

    fireEvent.click(await screen.findByTestId('run-source-link'));

    await waitFor(() => expect(location.search).toContain('run=run-1'));
    expect(location.search).toBe('?run=run-1&step=merge');
    expect(navigations.slice(before).map((n) => n.split(':')[0])).toEqual([
      'PUSH',
    ]);
    expect(screen.queryByTestId('step-run-dialog')).toBeNull();
    expect(editorRun.runSteps).not.toHaveBeenCalled();
  });

  it('marks the changed and the missing previous steps', async () => {
    dagSteps = etl();
    reuse.value = freshReuse({
      stale: ['extract orders'],
      missing: ['extract users'],
    });
    await open();
    expect(items()).toEqual([
      'seedreused',
      'extract orderschanged since run run-1',
      'extract usersno output in run run-1',
    ]);
  });

  it('warns when the source run used other parameters', async () => {
    dagSteps = etl();
    reuse.value = freshReuse({ paramsDiffer: true });
    await open();
    expect(
      screen.getByText(
        'Run run-1 used other parameters. The step runs with the defaults.'
      )
    ).toBeVisible();
  });

  it('says that no run holds the outputs when there is no source', async () => {
    dagSteps = etl();
    reuse.value = freshReuse({ source: null });
    await open();
    expect(
      screen.getByText('No earlier run holds the outputs that this step reads.')
    ).toBeVisible();
    expect(buttons()).toEqual(['Cancel', 'Run']);
    expect(screen.queryByTestId('run-source')).toBeNull();
    expect(screen.queryByTestId('run-source-picker')).toBeNull();
    expect(items()).toEqual(['seed', 'extract orders', 'extract users']);
  });

  it('offers Step only without a source when the step reads nothing', async () => {
    dagSteps = [{ name: 'a' }, { name: 'b', run: 'echo hi', depends: ['a'] }];
    reuse.value = freshReuse({ source: null });
    await open('b');
    expect(buttons()).toEqual(['Cancel', 'Step only', 'Run']);
    // No source: Run is the default, and Step only sends no reuse.
    expect(screen.getByRole('button', { name: 'Run' })).toHaveFocus();
    fireEvent.click(screen.getByRole('button', { name: 'Step only' }));
    await waitFor(() => expect(editorRun.runSteps).toHaveBeenCalledTimes(1));
    expect(editorRun.runSteps).toHaveBeenCalledWith(['b'], undefined);
  });

  it('runs the steps that it reads from with Run', async () => {
    dagSteps = etl();
    await open();
    fireEvent.click(screen.getByRole('button', { name: 'Run' }));
    await waitFor(() => expect(editorRun.runSteps).toHaveBeenCalledTimes(1));
    expect(editorRun.runSteps).toHaveBeenCalledWith(
      ['seed', 'extract orders', 'extract users', 'merge'],
      undefined
    );
  });

  it('runs the step alone on the outputs of the run with Step only', async () => {
    dagSteps = etl();
    await open();
    fireEvent.click(screen.getByRole('button', { name: 'Step only' }));
    await waitFor(() => expect(editorRun.runSteps).toHaveBeenCalledTimes(1));
    expect(editorRun.runSteps).toHaveBeenCalledWith(
      ['merge'],
      expect.objectContaining({ reuseOutputsFrom: 'run-1' })
    );
  });

  it('reads the references in the unsaved editor text', async () => {
    dagSteps = [
      { name: 'a', id: 'a' },
      { name: 'b', run: 'echo saved' },
    ];
    const buffer =
      'steps:\n  - id: a\n    run: echo 1\n  - name: b\n    run: echo ${steps.a.outputs.v}\n';
    editor.handle = {
      getBuffer: () => buffer,
      isDirty: () => true,
      getDag: () => undefined,
    };
    await open('b');
    expect(reuse.calls[reuse.calls.length - 1]).toMatchObject({
      reads: ['a'],
      previous: ['a'],
      liveText: buffer,
    });
    expect(items()).toEqual(['areused']);
    expect(
      screen.getByText('The step runs from the unsaved editor text.')
    ).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Run' }));
    await waitFor(() => expect(editorRun.runSteps).toHaveBeenCalledTimes(1));
    expect(editorRun.runSteps).toHaveBeenCalledWith(['a', 'b'], undefined);
  });

  // The run executes the draft: a dependency that only the draft has makes
  // a previous step, and Run starts it before the step.
  it('takes the previous steps from the unsaved draft', async () => {
    dagSteps = [{ name: 'a' }, { name: 'b', run: 'echo 2' }];
    editor.handle = {
      getBuffer: () => 'steps: [draft]',
      isDirty: () => true,
      getDag: () => ({
        name: 'example',
        steps: [{ name: 'a' }, { name: 'b', depends: ['a'], run: 'echo 2' }],
      }),
    };
    await open('b');
    expect(reuse.calls[reuse.calls.length - 1]).toMatchObject({
      previous: ['a'],
    });
    fireEvent.click(screen.getByRole('button', { name: 'Run' }));
    await waitFor(() => expect(editorRun.runSteps).toHaveBeenCalledTimes(1));
    expect(editorRun.runSteps).toHaveBeenCalledWith(['a', 'b'], undefined);
  });

  it('runs the upstream steps of the unsaved draft', () => {
    dagSteps = [{ name: 'a' }, { name: 'b' }];
    editor.handle = {
      getBuffer: () => 'steps: [draft]',
      isDirty: () => true,
      getDag: () => ({
        name: 'example',
        steps: [{ name: 'a' }, { name: 'b', depends: ['a'] }],
      }),
    };
    renderActions(runValue(details()), vi.fn(), '/dags/example.yaml');

    act(() => actions.runUpstream('b'));

    expect(
      screen.getByText(
        'Run the 1 upstream steps of b now? Their commands run for real.'
      )
    ).toBeVisible();
  });

  it('keeps the request of the followed run for the steps that ran', async () => {
    probeStay = true;
    dagSteps = etl();
    renderActions(
      runValue(details()),
      vi.fn(),
      '/dags/example.yaml?step=merge&ndv=expanded'
    );
    act(() => actions.runStep('merge'));
    await screen.findByTestId('step-run-dialog');
    fireEvent.click(screen.getByRole('button', { name: 'Run' }));
    await waitFor(() => expect(actions.followed).toHaveLength(2));
    expect(actions.followed[0]).toMatchObject({
      kind: 'upstream',
      step: 'merge',
      steps: ['seed', 'extract orders', 'extract users'],
    });
    expect(actions.followed[1]).toMatchObject({
      kind: 'execute',
      step: 'merge',
      steps: ['merge'],
    });
  });

  it('gives the followed upstream run to the hook as the source candidate', async () => {
    probeStay = true;
    dagSteps = etl();
    renderActions(
      runValue(details()),
      vi.fn(),
      '/dags/example.yaml?step=merge&ndv=expanded'
    );
    act(() => actions.runUpstream('merge'));
    fireEvent.click(screen.getByRole('button', { name: 'Run' }));
    await waitFor(() => expect(actions.followed).toHaveLength(1));

    act(() => actions.runStep('merge'));
    await screen.findByTestId('step-run-dialog');
    expect(reuse.calls[reuse.calls.length - 1]).toMatchObject({
      followedUpstreamRunId: 'run-2',
    });
  });
});

// D13: the default button, the buttons that show, and the request of each.
describe('useStepRunActions step run choices', () => {
  const etl = () => [
    { name: 'seed' },
    { name: 'extract orders', id: 'extract_orders', depends: ['seed'] },
    { name: 'extract users', id: 'extract_users' },
    {
      name: 'merge',
      run: 'echo ${steps.extract_orders.outputs.orders}',
      depends: ['extract orders', 'extract users'],
    },
  ];
  const open = async () => {
    renderActions(runValue(details()), vi.fn(), '/dags/example.yaml');
    act(() => actions.runStep('merge'));
    await screen.findByTestId('step-run-dialog');
  };
  const buttons = () => footerButtons().map((button) => button.textContent);
  const primary = () =>
    footerButtons()
      .filter((button) => button.hasAttribute('data-confirm'))
      .map((button) => button.textContent);

  beforeEach(() => {
    dagSteps = etl();
  });

  it('makes Step only the default when the source is fresh', async () => {
    await open();
    expect(buttons()).toEqual(['Cancel', 'Step only', 'Run']);
    expect(primary()).toEqual(['Step only']);
    expect(screen.getByRole('button', { name: 'Step only' })).toHaveFocus();
  });

  it('makes Changed steps the default when a previous step changed', async () => {
    reuse.value = freshReuse({ stale: ['extract users'] });
    await open();
    expect(buttons()).toEqual(['Cancel', 'Step only', 'Changed steps', 'Run']);
    expect(primary()).toEqual(['Changed steps']);
    expect(screen.getByRole('button', { name: 'Changed steps' })).toHaveFocus();
  });

  it('makes Changed steps the default when a previous step has no output', async () => {
    reuse.value = freshReuse({ missing: ['seed'] });
    await open();
    expect(primary()).toEqual(['Changed steps']);
  });

  it('makes Run the default when there is no source', async () => {
    reuse.value = freshReuse({ source: null });
    await open();
    expect(buttons()).toEqual(['Cancel', 'Run']);
    expect(primary()).toEqual(['Run']);
    expect(screen.getByRole('button', { name: 'Run' })).toHaveFocus();
  });

  it('shows no Changed steps when nothing changed', async () => {
    await open();
    expect(screen.queryByRole('button', { name: 'Changed steps' })).toBeNull();
  });

  it('shows no Changed steps without a source', async () => {
    reuse.value = freshReuse({ source: null, stale: ['seed'] });
    await open();
    expect(screen.queryByRole('button', { name: 'Changed steps' })).toBeNull();
  });

  // Enter picks the focused default button.
  it('disables Step only and focuses no button while the search runs', async () => {
    reuse.value = freshReuse({ loading: true });
    await open();
    expect(screen.getByRole('button', { name: 'Step only' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Run' })).not.toHaveFocus();
  });

  // The request lists the changed and missing steps in dependency order,
  // then the step; the body carries the source as outputsFromRunId.
  it('sends the changed steps in order and the source with Changed steps', async () => {
    editorRun.real = true;
    api.post.mockResolvedValue({
      data: { dagRunId: 'run-2' },
      response: { status: 200 },
    });
    // The stale set comes in a different order than the dependency order.
    reuse.value = freshReuse({
      stale: ['extract users'],
      missing: ['extract orders'],
    });
    await open();

    fireEvent.click(screen.getByRole('button', { name: 'Changed steps' }));

    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1));
    expect(api.post).toHaveBeenCalledWith('/dags/{fileName}/start', {
      params: {
        path: { fileName: 'example.yaml' },
        query: { remoteNode: 'edge' },
      },
      body: {
        steps: ['extract orders', 'extract users', 'merge'],
        outputsFromRunId: 'run-1',
        labels: ['dagu.source=editor', 'dagu.outputs-from=run-1'],
      },
    });
  });

  it('sends the step and the source with Step only', async () => {
    editorRun.real = true;
    api.post.mockResolvedValue({
      data: { dagRunId: 'run-2' },
      response: { status: 200 },
    });
    await open();

    fireEvent.click(screen.getByRole('button', { name: 'Step only' }));

    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1));
    expect(api.post).toHaveBeenCalledWith('/dags/{fileName}/start', {
      params: {
        path: { fileName: 'example.yaml' },
        query: { remoteNode: 'edge' },
      },
      body: {
        steps: ['merge'],
        outputsFromRunId: 'run-1',
        labels: ['dagu.source=editor', 'dagu.outputs-from=run-1'],
      },
    });
  });

  it('sends the previous steps and no source with Run', async () => {
    editorRun.real = true;
    api.post.mockResolvedValue({
      data: { dagRunId: 'run-2' },
      response: { status: 200 },
    });
    reuse.value = freshReuse({ stale: ['seed'] });
    await open();

    fireEvent.click(screen.getByRole('button', { name: 'Run' }));

    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1));
    expect(api.post).toHaveBeenCalledWith('/dags/{fileName}/start', {
      params: {
        path: { fileName: 'example.yaml' },
        query: { remoteNode: 'edge' },
      },
      body: {
        steps: ['seed', 'extract orders', 'extract users', 'merge'],
        labels: ['dagu.source=editor'],
      },
    });
  });

  it('follows the steps that Changed steps ran before the step', async () => {
    probeStay = true;
    reuse.value = freshReuse({ stale: ['extract users'] });
    renderActions(
      runValue(details()),
      vi.fn(),
      '/dags/example.yaml?step=merge&ndv=expanded'
    );
    act(() => actions.runStep('merge'));
    await screen.findByTestId('step-run-dialog');

    fireEvent.click(screen.getByRole('button', { name: 'Changed steps' }));

    await waitFor(() => expect(actions.followed).toHaveLength(2));
    expect(editorRun.runSteps).toHaveBeenCalledWith(
      ['extract users', 'merge'],
      expect.objectContaining({ reuseOutputsFrom: 'run-1' })
    );
    expect(actions.followed[0]).toMatchObject({
      kind: 'upstream',
      steps: ['extract users'],
    });
  });

  describe('while the source run is active', () => {
    const waiting = () => freshReuse({ source: null, waitingFor: 'run-9' });

    it('disables Step only with a spinner and the reason', async () => {
      reuse.value = waiting();
      await open();
      const button = screen.getByRole('button', { name: 'Step only' });
      expect(button).toBeDisabled();
      expect(button.querySelector('.animate-spin')).not.toBeNull();
      expect(button.parentElement).toHaveAttribute(
        'title',
        'Waits for run run-9 to finish'
      );
      expect(primary()).toEqual(['Run']);
      fireEvent.click(button);
      expect(editorRun.runSteps).not.toHaveBeenCalled();
    });

    it('enables Step only when the run ends', async () => {
      reuse.value = waiting();
      await open();
      expect(screen.getByRole('button', { name: 'Step only' })).toBeDisabled();

      reuse.value = freshReuse({
        source: {
          dagRunId: 'run-9',
          status: Status.Success,
        } as ReuseSourceState['source'],
      });
      // A new render of the open dialog reads the new state.
      act(() => actions.runStep('merge'));

      await waitFor(() =>
        expect(screen.getByRole('button', { name: 'Step only' })).toBeEnabled()
      );
      expect(
        screen
          .getByRole('button', { name: 'Step only' })
          .querySelector('.animate-spin')
      ).toBeNull();
    });
  });
});

describe('useStepRunActions with the cache and the draft', () => {
  const etl = () => [
    { name: 'seed' },
    { name: 'extract orders', id: 'extract_orders', depends: ['seed'] },
    { name: 'extract users', id: 'extract_users' },
    {
      name: 'merge',
      run: 'echo ${steps.extract_orders.outputs.orders}',
      depends: ['extract orders', 'extract users'],
    },
  ];
  const open = async (url = '/dags/example.yaml') => {
    renderActions(runValue(details()), vi.fn(), url);
    act(() => actions.runStep('merge'));
    await screen.findByTestId('step-run-dialog');
  };
  const primary = () =>
    footerButtons()
      .filter((button) => button.hasAttribute('data-confirm'))
      .map((button) => button.textContent);
  const dirtyEditor = () => {
    editor.handle = {
      getBuffer: () => 'draft: text',
      isDirty: () => true,
      getDag: () => undefined,
    };
  };
  const started = () => {
    editorRun.real = true;
    api.post.mockResolvedValue({
      data: { dagRunId: 'run-2' },
      response: { status: 200 },
    });
  };
  const body = (call = 0) => api.post.mock.calls[call]?.[1]?.body;
  const READ_AT = Date.parse('2026-10-01T00:00:00Z');
  // The cache names the run of each cached step; the browser has no value to send.
  const cached = (names: string[], extra: Partial<ReuseSourceState> = {}) =>
    freshReuse({
      cached: names,
      cachedRuns: Object.fromEntries(
        names.map((name) => [
          name,
          { runId: `Run-${name}`, capturedAt: READ_AT },
        ])
      ),
      stepRuns: Object.fromEntries(
        names.map((name) => [
          name,
          {
            runId: `Run-${name}`,
            startedAt: '2026-10-01T00:00:00Z',
            finishedAt: '2026-10-01T00:00:20Z',
          },
        ])
      ),
      cacheCapturedAt: READ_AT,
      cacheTimes: Object.fromEntries(names.map((name) => [name, READ_AT])),
      ...extra,
    });

  beforeEach(() => {
    dagSteps = etl();
  });

  describe('the request of a step run', () => {
    it('sends no ephemeral flag when the editor holds a clean definition', async () => {
      started();
      await open();
      fireEvent.click(screen.getByRole('button', { name: 'Step only' }));
      await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1));
      expect(api.post.mock.calls[0]?.[0]).toBe('/dags/{fileName}/start');
      expect('ephemeral' in body()).toBe(false);
    });

    it('sends ephemeral true when the editor holds unsaved text', async () => {
      started();
      dirtyEditor();
      await open();
      fireEvent.click(screen.getByRole('button', { name: 'Step only' }));
      await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1));
      expect(api.post.mock.calls[0]?.[0]).toBe('/dag-runs');
      expect(body()).toMatchObject({
        spec: 'draft: text',
        steps: ['merge'],
        outputsFromRunId: 'run-1',
        ephemeral: true,
      });
    });

    it('sends ephemeral true for the upstream steps of an unsaved draft', async () => {
      started();
      dirtyEditor();
      renderActions(runValue(details()), vi.fn(), '/dags/example.yaml');
      act(() => actions.runUpstream('merge'));
      fireEvent.click(await screen.findByRole('button', { name: 'Run' }));
      await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1));
      expect(body()).toMatchObject({ ephemeral: true });
      expect(body().steps).toEqual(['seed', 'extract orders', 'extract users']);
    });

    // The browser names runs and never sends a value: the server reads the
    // outputs of each cached step from its run.
    it('names the run of each cached step next to the source run, and sends no value', async () => {
      started();
      reuse.value = cached(['seed', 'extract orders'], {
        stale: ['extract users'],
      });
      await open();
      fireEvent.click(screen.getByRole('button', { name: 'Changed steps' }));
      await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1));
      expect(body()).toMatchObject({
        steps: ['extract users', 'merge'],
        outputsFromRunId: 'run-1',
        outputsFromRuns: {
          seed: 'Run-seed',
          'extract orders': 'Run-extract orders',
        },
      });
      expect('outputs' in body()).toBe(false);
      expect('outputsSources' in body()).toBe(false);
    });

    it('sends only the runs of the cache with no source run', async () => {
      started();
      dirtyEditor();
      reuse.value = cached(['seed', 'extract orders', 'extract users'], {
        source: null,
      });
      await open();
      expect(primary()).toEqual(['Step only']);
      fireEvent.click(screen.getByRole('button', { name: 'Step only' }));
      await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1));
      const sent = body();
      expect('outputsFromRunId' in sent).toBe(false);
      expect(Object.keys(sent.outputsFromRuns)).toEqual([
        'seed',
        'extract orders',
        'extract users',
      ]);
      expect('outputs' in sent).toBe(false);
      expect(sent.ephemeral).toBe(true);
    });

    it('does not name a run for a step that the request runs', async () => {
      started();
      reuse.value = cached(['seed', 'extract orders'], {
        missing: ['extract users'],
        stale: ['extract orders'],
      });
      await open();
      fireEvent.click(screen.getByRole('button', { name: 'Changed steps' }));
      await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1));
      expect(body().steps).toContain('extract orders');
      expect(Object.keys(body().outputsFromRuns)).toEqual(['seed']);
    });

    // The server keeps the source of each reused step. The labels hold what
    // it lacks: a step that changed since the run, which the user ran anyway.
    it('writes a label only for a step that changed since its run', async () => {
      started();
      reuse.value = freshReuse({ stale: ['extract users'] });
      await open();
      fireEvent.click(screen.getByRole('button', { name: 'Step only' }));
      await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1));
      expect(body().labels).toEqual([
        'dagu.source=editor',
        'dagu.outputs-from=run-1',
        'dagu.input=run-stale/1790812800/extract.20users/run-1',
      ]);
    });

    it('writes no label for the cached steps: the request names their run', async () => {
      started();
      dirtyEditor();
      reuse.value = cached(['seed', 'extract orders', 'extract users'], {
        source: null,
      });
      await open();
      fireEvent.click(screen.getByRole('button', { name: 'Step only' }));
      await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1));
      expect(body().labels).toEqual(['dagu.source=editor']);
    });

    it('sends no outputs with Run: it runs every previous step', async () => {
      started();
      reuse.value = cached(['seed']);
      await open();
      fireEvent.click(screen.getByRole('button', { name: 'Run' }));
      await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1));
      expect('outputs' in body()).toBe(false);
      expect('outputsFromRuns' in body()).toBe(false);
      expect('outputsFromRunId' in body()).toBe(false);
    });
  });

  describe('the dialog', () => {
    it('lists each cached step with its run, the time that it took and when it ended', async () => {
      reuse.value = cached(['seed']);
      await open();
      const items = screen.getAllByTestId('run-step-item');
      const seed = items.find((item) => item.textContent?.includes('seed'));
      expect(
        within(seed as HTMLElement).getByTestId('run-step-mark-cached')
      ).toHaveTextContent('cached');
      const run = within(seed as HTMLElement).getByTestId('run-step-run');
      expect(run).toHaveTextContent(/^run Run-seed, 20s, \d\d:\d\d$/);
      expect(run).toHaveAttribute('title', 'Run-seed');
    });

    it('lists the step that the source run gives with that run', async () => {
      reuse.value = freshReuse({
        stepRuns: {
          seed: {
            runId: 'run-1',
            startedAt: '2026-10-01T00:00:00Z',
            finishedAt: '2026-10-01T00:00:03Z',
          },
        },
      });
      await open();
      const seed = screen
        .getAllByTestId('run-step-item')
        .find((item) => item.textContent?.includes('seed'));
      expect(
        within(seed as HTMLElement).getByTestId('run-step-run')
      ).toHaveTextContent(/^run run-1, 3s, /);
    });

    it('says that the source run of a cached step is no longer available', async () => {
      reuse.value = freshReuse({
        source: null,
        gone: ['seed'],
        options: [],
      });
      await open();
      const seed = screen
        .getAllByTestId('run-step-item')
        .find((item) => item.textContent?.includes('seed'));
      expect(
        within(seed as HTMLElement).getByTestId('run-step-gone')
      ).toHaveTextContent('source run no longer available');
      // Nothing covers the previous steps: Run is the default.
      expect(primary()).toEqual(['Run']);
    });

    it('shows the picker with the run in use, for a run with no cache', async () => {
      await open();
      const line = screen.getByTestId('run-source');
      expect(line).toHaveTextContent(/^Inputs from/);
      expect(within(line).getByTestId('run-source-picker')).toHaveTextContent(
        'run-1'
      );
    });

    it('names the cached values and their run in the picker when the cache is the source', async () => {
      reuse.value = cached(['seed', 'extract orders', 'extract users'], {
        source: null,
        options: [
          {
            id: 'cache',
            kind: 'cache',
            cache: { runIds: ['Run-seed'], capturedAt: READ_AT },
          },
        ],
        selectedOptionId: 'cache',
      });
      await open();
      const line = screen.getByTestId('run-source');
      expect(within(line).getByTestId('run-source-picker')).toHaveTextContent(
        'Cached values'
      );
      await userEvent
        .setup({ pointerEventsCheck: 0 })
        .click(within(line).getByTestId('run-source-picker'));
      expect(
        await screen.findByTestId('run-source-option-cache')
      ).toHaveTextContent('Cached values (from run Run-see');
      expect(screen.queryByTestId('run-source-link')).toBeNull();
    });

    it('makes Changed steps the default when the cache covers only some steps', async () => {
      reuse.value = cached(['seed'], {
        source: null,
        missing: ['extract orders', 'extract users'],
      });
      await open();
      expect(primary()).toEqual(['Changed steps']);
    });
  });

  describe('in the editor (stay) with an unsaved draft', () => {
    beforeEach(() => {
      probeStay = true;
      dirtyEditor();
    });

    // A test run is selectable while the server keeps it: the URL names it.
    it('follows the test run and selects it with replace', async () => {
      renderActions(
        pinnedRunValue(details()),
        vi.fn(),
        '/dags/example.yaml?run=run-1&step=merge&ndv=expanded'
      );
      act(() => actions.runStep('merge'));
      fireEvent.click(await screen.findByRole('button', { name: 'Step only' }));
      await waitFor(() =>
        expect(actions.followed).toEqual([
          expect.objectContaining({ kind: 'execute', runId: 'run-2' }),
        ])
      );
      await waitFor(() =>
        expect(location.search).toBe('?run=run-2&step=merge&ndv=expanded')
      );
      expect(location.pathname).toBe('/dags/example.yaml');
    });

    it('opens the node view of the step to follow the test run', async () => {
      renderActions(runValue(details()), vi.fn(), '/dags/example.yaml');
      act(() => actions.runStep('merge'));
      fireEvent.click(await screen.findByRole('button', { name: 'Step only' }));
      await waitFor(() => expect(actions.followed).toHaveLength(1));
      await waitFor(() => {
        expect(location.search).toContain('step=merge');
        expect(location.search).toContain('ndv=expanded');
        expect(location.search).toContain('run=run-2');
      });
    });

    it('tells the timeline that this tab started a test run', async () => {
      renderActions(runValue(details()), vi.fn(), '/dags/example.yaml');
      act(() => actions.runStep('merge'));
      fireEvent.click(await screen.findByRole('button', { name: 'Step only' }));
      await waitFor(() => expect(actions.followed).toHaveLength(1));
      expect(testRunNoted).toHaveBeenCalledWith('example.yaml');
    });
  });
});
