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
import { useEffect, type ReactNode } from 'react';
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigationType,
} from 'react-router-dom';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import { AgentTaskStatus, NodeStatus, Status } from '@/api/v1/schema';
import {
  RightPanelProvider,
  useActiveRightPanel,
  useRightPanel,
} from '@/contexts/RightPanelContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { buildCanvasGraph } from '@/features/visual-editor/canvas';
import type {
  ApiDAGDetails,
  CanvasGraph,
  StepCommandGates,
  VisualCanvasProps,
} from '@/features/visual-editor/contracts';
import {
  groupIssues,
  parseDagDoc,
  resolveStepNames,
} from '@/features/visual-editor/doc';
import { realSchema } from '@/features/visual-editor/inspector/__tests__/realSchema';
import {
  scopeHttpJq,
  yamlArraySteps,
  yamlHttpJq,
  yamlRouter,
  yamlShorthandSteps,
} from '@/features/visual-editor/testing/fixtures';
import type { VisualEditorProps } from '@/features/visual-editor/VisualEditor';
import { node, run as runDetails } from '../../data/__tests__/fixtures';
import { nodesByStep, NO_WAITING } from '../../data/runFacts';
import type { ApiDAGRunDetails, WorkspaceRunValue } from '../../data/types';
import { RunSnapshotCanvas } from '../RunSnapshotCanvas';

const h = vi.hoisted(() => ({
  queries: [] as { path: string; init: unknown }[],
  runSpec: '' as string | null,
  /** The definition of the selected run is still loading. */
  runLoading: false,
  post: vi.fn(),
  runError: null as { code: string; message: string } | null,
  savedSpec: 'name: saved\n',
  run: null as unknown as WorkspaceRunValue,
  editorProps: [] as VisualEditorProps[],
  graphs: [] as CanvasGraph[],
  canvasProps: [] as VisualCanvasProps[],
  yaml: {
    props: null as null | Record<string, unknown>,
    hidden: [] as { startLine: number; endLine: number }[][],
    numbers: null as null | ((line: number) => string),
    bars: [] as string[],
    mounts: 0,
  },
  dock: { open: false, setTab: vi.fn(), yieldToView: vi.fn() },
  actions: {
    can: vi.fn(),
    retryStep: vi.fn(),
    setStatus: vi.fn(),
    runStep: vi.fn(),
    runUpstream: vi.fn(),
    followed: [],
    clearFollowed: vi.fn(),
    openSubRun: vi.fn(),
    openLogs: vi.fn(),
    dialogs: null,
  },
}));

vi.mock(
  '@monaco-editor/react',
  async () =>
    (await import('@/features/visual-editor/inspector/__tests__/monacoMock'))
      .monacoModule
);
vi.mock('@/features/visual-editor/inspector/monacoSetup', () => ({
  monacoTheme: () => 'light',
}));
vi.mock('@/hooks/api', () => ({
  useQuery: (path: string, init: unknown) => {
    h.queries.push({ path, init });
    if (init === null) return { data: undefined, mutate: () => undefined };
    if (path === '/dag-runs/{name}/{dagRunId}/spec') {
      if (h.runError) return { data: undefined, error: h.runError };
      if (h.runLoading) return { data: undefined };
      return h.runSpec === null
        ? { data: undefined, error: { code: 'not_found', message: 'x' } }
        : { data: { spec: h.runSpec } };
    }
    if (path === '/dags/{fileName}/spec') {
      return { data: { spec: h.savedSpec } };
    }
    return { data: undefined, isLoading: false, mutate: () => undefined };
  },
  useClient: () => ({ POST: h.post, GET: vi.fn() }),
}));
vi.mock('@/hooks/useDAGRunSSE', () => ({
  useDAGRunSSE: () => ({
    data: null,
    isConnected: false,
    isConnecting: false,
    shouldUseFallback: true,
  }),
}));
vi.mock('@/contexts/AuthContext', () => ({ useCanExecute: () => true }));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('@/contexts/SchemaContext', () => ({
  useSchema: () => ({ schema: realSchema }),
}));

// The real editor and canvas, with their props watched.
vi.mock('@/features/visual-editor', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('@/features/visual-editor')>();
  function WatchedEditor(props: VisualEditorProps) {
    h.editorProps.push(props);
    return <actual.VisualEditor {...props} />;
  }
  return { ...actual, default: WatchedEditor, VisualEditor: WatchedEditor };
});
vi.mock('@/features/visual-editor/canvas', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('@/features/visual-editor/canvas')>();
  function WatchedCanvas(props: VisualCanvasProps) {
    h.graphs.push(props.graph);
    h.canvasProps.push(props);
    return <actual.VisualCanvas {...props} />;
  }
  return { ...actual, VisualCanvas: WatchedCanvas };
});

vi.mock('../../data', () => ({
  useWorkspaceRun: () => h.run,
  useWorkspacePermissions: () => ({ canExecute: true }),
}));
vi.mock('../../data/context', () => ({
  useWorkspaceData: () => ({
    fileName: 'demo',
    dagData: { dag: { name: 'demo' } },
  }),
}));
// The Monaco editor of the step YAML: a double that records the scope that
// the visual editor gives it (hidden areas, line numbers, change bars).
vi.mock('@/features/dags/components/dag-editor/DAGEditorWithDocs', async () => {
  const { useEffect, useRef } = await import('react');
  return {
    default: (props: {
      value: string;
      readOnly?: boolean;
      onEditorMount?: (editor: unknown) => void;
    }) => {
      h.yaml.props = props;
      const editor = useRef({
        getModel: () => ({
          getValue: () => props.value,
          getLineCount: () => props.value.split('\n').length,
          getLineMaxColumn: () => 1,
        }),
        setHiddenAreas: (
          areas: { startLineNumber: number; endLineNumber: number }[]
        ) => {
          h.yaml.hidden.push(
            areas.map((a) => ({
              startLine: a.startLineNumber,
              endLine: a.endLineNumber,
            }))
          );
        },
        updateOptions: (o: { lineNumbers: (line: number) => string }) => {
          h.yaml.numbers = o.lineNumbers;
        },
        // The bars of changed lines are margin decorations; the light of the
        // step is a line class and is not recorded.
        createDecorationsCollection: (
          list: { options: { linesDecorationsClassName?: string } }[]
        ) => {
          for (const d of list) {
            if (d.options.linesDecorationsClassName) {
              h.yaml.bars.push(d.options.linesDecorationsClassName);
            }
          }
          return { set() {}, clear() {} };
        },
      });
      useEffect(() => {
        h.yaml.mounts += 1;
        props.onEditorMount?.(editor.current);
      }, []);
      return (
        <textarea
          data-testid="run-yaml-editor"
          aria-label="Step YAML"
          readOnly={props.readOnly}
          value={props.value}
          onChange={() => undefined}
        />
      );
    },
  };
});
vi.mock('../../dock/DockContext', () => ({
  useDock: () => h.dock,
  useDockCommands: () => h.dock,
}));
vi.mock('../../step/useStepRunActions', () => ({
  useStepRunActions: () => h.actions,
}));
vi.mock('../../step/StepRunTabs', () => ({
  StepRunTabs: ({ step }: { step: string }) => (
    <p data-testid="step-run-view">{step}</p>
  ),
}));

function makeRun(details: ApiDAGRunDetails | null): WorkspaceRunValue {
  const runId = details?.dagRunId ?? null;
  return {
    selection: runId === null ? 'latest' : { runId },
    ref: runId === null ? null : { name: 'demo', runId },
    details,
    hasRun: runId !== null,
    isLatest: false,
    isActive: false,
    notFound: false,
    latestRef: null,
    overlay: {},
    nodeByStep: nodesByStep(details),
    handlers: [],
    waiting: NO_WAITING,
    failedStep: null,
    select: vi.fn(),
    applyNodeStatus: vi.fn(),
    refresh: vi.fn(async () => {}),
  };
}

function Probe() {
  const location = useLocation();
  return (
    <div
      data-testid="loc"
      data-nav={useNavigationType()}
      data-url={`${location.pathname}${location.search}`}
    />
  );
}

function canvasTree(
  url: string,
  around: (canvas: ReactNode) => ReactNode = (canvas) => canvas
) {
  return (
    <UserPreferencesProvider>
      <MemoryRouter initialEntries={[url]}>
        <Routes>
          <Route
            path="/dags/:fileName/:tab?"
            element={
              <>
                {around(<RunSnapshotCanvas />)}
                <Probe />
              </>
            }
          />
        </Routes>
      </MemoryRouter>
    </UserPreferencesProvider>
  );
}

function renderCanvas(
  url = '/dags/demo?run=run-1&def=run',
  around: (canvas: ReactNode) => ReactNode = (canvas) => canvas
) {
  return render(canvasTree(url, around));
}

// The first render waits for the lazy editor chunk, which is slow under load.
function findNode(key: string): Promise<HTMLElement> {
  return screen.findByTestId(`ve-canvas-node-${key}`, undefined, {
    timeout: 5000,
  });
}

// A step opens on its YAML; the Form button of the node view opens the form.
async function showForm(): Promise<void> {
  const ndv = await screen.findByTestId('ndv');
  expect(screen.queryByTestId('ve-inspector')).toBeNull();
  fireEvent.click(await within(ndv).findByRole('button', { name: 'Form' }));
}

function lastEditor(): VisualEditorProps {
  return h.editorProps[h.editorProps.length - 1]!;
}

function lastGraph(): CanvasGraph {
  return h.graphs[h.graphs.length - 1]!;
}

const arrayRun = () =>
  runDetails(
    'run-1',
    [
      node('fetch'),
      node('transform', NodeStatus.Failed),
      node('publish', NodeStatus.NotStarted),
    ],
    Status.Failed
  );

// The editor is a lazy chunk: load it once, so that no test waits for it.
beforeAll(async () => {
  await import('@/features/visual-editor');
}, 30_000);

beforeEach(() => {
  h.queries = [];
  h.editorProps = [];
  h.graphs = [];
  h.canvasProps = [];
  h.runSpec = yamlArraySteps;
  h.runLoading = false;
  h.runError = null;
  h.savedSpec = 'name: saved\n';
  sessionStorage.clear();
  h.run = makeRun(arrayRun());
  h.post.mockReset();
  h.post.mockResolvedValue({ data: {} });
  h.yaml.props = null;
  h.yaml.hidden = [];
  h.yaml.numbers = null;
  h.yaml.bars = [];
  h.yaml.mounts = 0;
  h.dock.open = false;
  h.dock.setTab.mockReset();
  h.dock.yieldToView.mockReset();
  for (const fn of Object.values(h.actions)) {
    if (typeof fn === 'function') fn.mockReset();
  }
  h.actions.can.mockReturnValue({
    retry: true,
    setStatus: false,
    runStep: true,
    subRuns: false,
  });
});

describe.skipIf(realSchema === null)('RunSnapshotCanvas', () => {
  // parity:history-graph parity:api-run-spec
  it('shows the stored definition of the run, read-only and striped', async () => {
    renderCanvas();
    await findNode('steps[0]');

    const props = lastEditor();
    expect(props.text).toBe(yamlArraySteps);
    expect(props.editable).toBe(false);
    expect(props.isDirty).toBe(false);
    expect(props.chrome).toEqual({
      hideRunBar: true,
      striped: true,
      hideReadOnlyChip: true,
    });
    expect(props.dagName).toBe('demo');
    expect(props.runSource).toEqual({ runId: 'run-1', details: h.run.details });
    expect(props.validation?.issues).toEqual([]);
    expect(props.validation?.dag?.steps?.map((s) => s.name)).toEqual([
      'fetch',
      'transform',
      'publish',
    ]);
    expect(h.canvasProps[h.canvasProps.length - 1]!.exportName).toBe('demo');
    // VisualEditor draws the stripes of chrome.striped behind the canvas.
    expect(
      within(screen.getByTestId('ww-run-definition')).getByTestId(
        've-canvas-frame'
      )
    ).toHaveAttribute('data-striped', 'true');
  });

  it('colours the steps from the selected run without reading it again', async () => {
    renderCanvas();
    const card = await findNode('steps[1]');

    expect(
      card.querySelector('[data-testid="ve-canvas-node-status"]')
    ).toHaveAttribute('data-status', String(NodeStatus.Failed));
    expect(
      h.queries.filter(
        (q) => q.path === '/dag-runs/{name}/{dagRunId}' && q.init !== null
      )
    ).toEqual([]);
  });

  it('gives unnamed steps the names of the run', async () => {
    h.runSpec = yamlShorthandSteps;
    h.run = makeRun(
      runDetails('run-1', [node('cmd_1'), node('cmd_2'), node('three')])
    );
    renderCanvas('/dags/demo?run=run-1&def=run&step=cmd_2');
    await findNode('steps[0]');

    expect(lastGraph().nodes.map((n) => n.serverName)).toEqual([
      'cmd_1',
      'cmd_2',
      'three',
    ]);
    expect(lastEditor().selectedStepName).toBe('cmd_2');
  });

  it('takes the handlers from the run', async () => {
    h.run = makeRun(
      runDetails('run-1', [node('fetch')], Status.Success, {
        onSuccess: node('onSuccess'),
        onFailure: node('onFailure'),
      })
    );
    renderCanvas();
    await findNode('steps[0]');

    const handlerOn = lastEditor().validation?.dag?.handlerOn;
    expect(handlerOn?.success?.name).toBe('onSuccess');
    expect(handlerOn?.failure?.name).toBe('onFailure');
  });

  it('binds the selection to ?step with replace', async () => {
    renderCanvas();
    fireEvent.click(await findNode('steps[2]'));

    const loc = screen.getByTestId('loc');
    expect(loc.dataset.url).toBe('/dags/demo?run=run-1&def=run&step=publish');
    expect(loc.dataset.nav).toBe('REPLACE');
  });

  // A step click selects ?step only: it never opens the Run panel.
  it('leaves a closed Run panel closed when a step is picked', async () => {
    h.dock.open = false;
    renderCanvas();
    fireEvent.click(await findNode('steps[2]'));

    expect(h.dock.setTab).not.toHaveBeenCalled();
  });

  it('leaves an open dock as it is when a step is picked', async () => {
    h.dock.open = true;
    renderCanvas();
    fireEvent.click(await findNode('steps[2]'));

    expect(h.dock.setTab).not.toHaveBeenCalled();
  });

  it('sends the step commands to the step actions of the workspace', async () => {
    renderCanvas();
    await findNode('steps[0]');
    const { onStepCommand, stepCommandGates } = lastEditor();

    // The host accepts a command that its gate allows.
    h.actions.can.mockReturnValue({
      retry: true,
      setStatus: true,
      runStep: true,
      subRuns: true,
    });
    onStepCommand!({ kind: 'retryStep', step: 'transform' });
    onStepCommand!({ kind: 'setStatus', step: 'transform' });
    onStepCommand!({ kind: 'runStep', step: 'fetch' });
    onStepCommand!({ kind: 'runUpstream', step: 'fetch' });
    onStepCommand!({ kind: 'openSubRun', step: 'fetch', newTab: true });
    onStepCommand!({ kind: 'viewLogs', step: 'fetch', stream: 'stderr' });

    expect(h.actions.retryStep).toHaveBeenCalledWith('transform');
    expect(h.actions.setStatus).toHaveBeenCalledWith('transform');
    expect(h.actions.runStep).toHaveBeenCalledWith('fetch');
    expect(h.actions.runUpstream).toHaveBeenCalledWith('fetch');
    expect(h.actions.openSubRun).toHaveBeenCalledWith('fetch', {
      newTab: true,
    });
    expect(h.actions.openLogs).toHaveBeenCalledWith({
      step: 'fetch',
      stream: 'stderr',
    });
    h.actions.can.mockReturnValue({
      retry: true,
      setStatus: false,
      runStep: true,
      subRuns: false,
    });
    const gates = stepCommandGates as StepCommandGates;
    expect(gates.canRunStep).toBe(true);
    expect(gates.canRetry('transform')).toBe(true);
    expect(gates.canSetStatus('transform')).toBe(false);
    expect(gates.hasSubRuns('transform')).toBe(false);
    expect(h.actions.can).toHaveBeenCalledWith('transform');
  });

  it('shows the saved definition with a notice when the run has none', async () => {
    h.runSpec = null;
    h.savedSpec = yamlArraySteps;
    renderCanvas();
    await findNode('steps[0]');

    expect(screen.getByRole('status')).toHaveTextContent(
      'This run has no stored definition. The current definition is shown.'
    );
    expect(lastEditor().text).toBe(yamlArraySteps);
  });

  it('says why when the definition of the run cannot be read', () => {
    h.runError = { code: 'internal', message: 'disk full' };
    renderCanvas();

    expect(screen.getByRole('alert')).toHaveTextContent(
      'Failed to load DAG spec: disk full'
    );
    expect(h.editorProps).toEqual([]);
  });

  it('shows no notice when the run has its definition', async () => {
    renderCanvas();
    await findNode('steps[0]');

    expect(
      screen.queryByText(
        'This run has no stored definition. The current definition is shown.'
      )
    ).toBeNull();
  });

  it('shows no canvas and reads nothing before the first run', () => {
    h.run = makeRun(null);
    renderCanvas('/dags/demo?def=run');

    expect(screen.getByTestId('ww-run-definition')).toHaveTextContent(
      'No runs yet'
    );
    expect(h.editorProps).toEqual([]);
    expect(h.queries.every((q) => q.init === null)).toBe(true);
  });

  it('renders nothing without ?def=run', () => {
    renderCanvas('/dags/demo?run=run-1');
    expect(screen.queryByTestId('ww-run-definition')).toBeNull();
    expect(h.editorProps).toEqual([]);
  });

  it('has a lock marker and a Back button in its toolbar', async () => {
    renderCanvas();
    await findNode('steps[0]');
    const bar = screen.getByTestId('ww-run-snapshot-bar');
    expect(within(bar).getByTestId('readonly-marker')).toHaveAccessibleName(
      'Read-only: definition of run run-1'
    );
    expect(within(bar).queryByText('Read-only')).toBeNull();
    expect(
      within(bar).getByRole('button', { name: 'Back to current definition' })
    ).toBeInTheDocument();
  });

  it('clears def with the Back button and keeps the other keys', async () => {
    renderCanvas('/dags/demo?run=run-1&def=run&step=transform');
    await findNode('steps[0]');
    fireEvent.click(
      screen.getByRole('button', { name: 'Back to current definition' })
    );
    await waitFor(() =>
      expect(screen.getByTestId('loc').dataset.url).toBe(
        '/dags/demo?run=run-1&step=transform'
      )
    );
    expect(screen.getByTestId('loc').dataset.nav).toBe('PUSH');
  });

  it('clears def on Escape when no layer is open', async () => {
    renderCanvas();
    await findNode('steps[0]');
    fireEvent.keyDown(document.body, { key: 'Escape' });
    await waitFor(() =>
      expect(screen.getByTestId('loc').dataset.url).toBe('/dags/demo?run=run-1')
    );
  });

  // The toolbar of the canvas takes Escape for itself when it has the focus.
  it('clears def on Escape with the focus in the canvas toolbar', async () => {
    renderCanvas();
    await findNode('steps[0]');
    const yaml = within(screen.getByTestId('ve-toolbar')).getByRole('button', {
      name: 'YAML',
    });
    yaml.focus();
    fireEvent.keyDown(yaml, { key: 'Escape' });
    await waitFor(() =>
      expect(screen.getByTestId('loc').dataset.url).toBe('/dags/demo?run=run-1')
    );
  });

  it('keeps def on Escape while the step view is open', async () => {
    renderCanvas('/dags/demo?run=run-1&def=run&step=transform&ndv=expanded');
    await screen.findByTestId('ndv');
    fireEvent.keyDown(document.body, { key: 'Escape' });
    await waitFor(() =>
      expect(screen.getByTestId('loc').dataset.url).not.toContain('ndv=')
    );
    expect(screen.getByTestId('loc').dataset.url).toContain('def=run');
  });

  it('keeps def on Escape while a dialog is open', async () => {
    renderCanvas();
    await findNode('steps[0]');
    render(<div role="dialog" data-state="open" />);
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(screen.getByTestId('loc').dataset.url).toContain('def=run');
  });

  it('shows the OUTPUT of the step view with the tabs of the Run panel', async () => {
    renderCanvas('/dags/demo?run=run-1&def=run&step=transform&ndv=expanded');
    await screen.findByTestId('ndv');
    expect(await screen.findByTestId('step-run-view')).toHaveTextContent(
      'transform'
    );
    expect(lastEditor().renderStepOutput).toBeTypeOf('function');
    expect(lastEditor().renderStepRun).toBeUndefined();
  });

  it('keeps its step view out of the right panels of the editor', async () => {
    const editorEvicted = vi.fn();
    function EditorNdv() {
      const { claim } = useRightPanel('ndv', editorEvicted);
      const active = useActiveRightPanel();
      useEffect(() => claim(), [claim]);
      return <p data-testid="outer-panel">{active ?? 'none'}</p>;
    }
    renderCanvas(undefined, (canvas) => (
      <RightPanelProvider>
        <EditorNdv />
        {canvas}
      </RightPanelProvider>
    ));
    expect(screen.getByTestId('outer-panel')).toHaveTextContent('ndv');

    // Enter opens the step view of the canvas, as a panel of its own.
    const stepNode = await findNode('steps[1]');
    fireEvent.click(stepNode);
    fireEvent.keyDown(stepNode, { key: 'Enter' });
    await showForm();
    expect(await screen.findByTestId('ve-inspector')).toBeInTheDocument();
    expect(screen.getByTestId('step-run-view')).toHaveTextContent('transform');
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));

    expect(screen.getByTestId('ndv')).toHaveAttribute('data-state', 'closed');
    expect(screen.getByTestId('outer-panel')).toHaveTextContent('ndv');
    expect(editorEvicted).not.toHaveBeenCalled();
  });

  // A click selects the step (the dock shows its facts and logs); a double
  // click and Enter open the step view in the graph zone, the node view of
  // the editor with the run data: INPUT | parameters | OUTPUT.
  it('opens the step view in the graph zone on a double click', async () => {
    renderCanvas();
    const stepNode = await findNode('steps[1]');
    fireEvent.click(stepNode);
    fireEvent.doubleClick(stepNode);

    const ndv = await screen.findByTestId('ndv');
    expect(ndv).toHaveAttribute('data-state', 'expanded');
    expect(ndv).toHaveAttribute('data-placement', 'zone');
    expect(screen.getByTestId('ww-run-zone')).toContainElement(ndv);
    expect(screen.getByTestId('loc').dataset.url).toBe(
      '/dags/demo?run=run-1&def=run&step=transform&ndv=expanded'
    );
    // No fixed drawer: nothing leaves the zone.
    expect(screen.queryByTestId('step-side-drawer')).toBeNull();
    expect(
      within(ndv).getByRole('heading', { name: 'transform' })
    ).toBeVisible();
    // The facts of the run are the OUTPUT column; the step opens on its YAML,
    // and the Form button shows the form in the center.
    expect(
      within(within(ndv).getByRole('region', { name: 'Output' })).getByTestId(
        'step-run-view'
      )
    ).toHaveTextContent('transform');
    await showForm();
    expect(within(ndv).getByTestId('ve-inspector')).toBeInTheDocument();
    // Previous, next, Run step and Close, as in the editor. The node view
    // has no Docs button and no button that docks it.
    for (const name of ['Previous step', 'Next step', 'Run step', 'Close']) {
      expect(within(ndv).getByRole('button', { name })).toBeInTheDocument();
    }
    expect(within(ndv).queryByRole('button', { name: /Docs/ })).toBeNull();
    expect(
      within(ndv).queryByRole('button', { name: 'Dock to the side' })
    ).toBeNull();
  });

  // A browser sends the clicks of a double click before the router renders
  // the URL that the first one wrote: act holds the renders back the same
  // way. The ?step of the click must survive the ?ndv of the double click.
  it('keeps ?step when a double click lands before the render', async () => {
    renderCanvas();
    const stepNode = await findNode('steps[1]');
    act(() => {
      fireEvent.click(stepNode);
      fireEvent.click(stepNode);
      fireEvent.doubleClick(stepNode);
    });

    const ndv = await screen.findByTestId('ndv');
    await waitFor(() => expect(ndv).toHaveAttribute('data-state', 'expanded'));
    expect(screen.getByTestId('loc').dataset.url).toBe(
      '/dags/demo?run=run-1&def=run&step=transform&ndv=expanded'
    );
    await showForm();
    expect(await screen.findByTestId('ve-inspector')).toBeInTheDocument();
  });

  it('selects the step of a click and of a lone double click', async () => {
    renderCanvas();
    const stepNode = await findNode('steps[1]');
    act(() => {
      fireEvent.doubleClick(stepNode);
    });

    expect(await screen.findByTestId('ndv')).toHaveAttribute(
      'data-state',
      'expanded'
    );
    expect(screen.getByTestId('loc').dataset.url).toBe(
      '/dags/demo?run=run-1&def=run&step=transform&ndv=expanded'
    );
    expect(screen.getByTestId('loc').dataset.nav).toBe('REPLACE');
  });

  it('opens the step view on Enter and closes it on Escape', async () => {
    renderCanvas();
    const stepNode = await findNode('steps[1]');
    fireEvent.click(stepNode);
    fireEvent.keyDown(stepNode, { key: 'Enter' });
    await showForm();
    expect(await screen.findByTestId('ve-inspector')).toBeInTheDocument();
    expect(screen.getByTestId('ndv')).toHaveAttribute('data-state', 'expanded');

    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(screen.getByTestId('ndv')).toHaveAttribute('data-state', 'closed');
    expect(screen.getByTestId('loc').dataset.url).toBe(
      '/dags/demo?run=run-1&def=run&step=transform'
    );
  });

  it('clears the selected step on a second Escape', async () => {
    renderCanvas();
    const stepNode = await findNode('steps[1]');
    fireEvent.click(stepNode);
    fireEvent.keyDown(stepNode, { key: 'Enter' });
    await showForm();
    await screen.findByTestId('ve-inspector');

    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(screen.getByTestId('loc').dataset.url).toBe(
      '/dags/demo?run=run-1&def=run&step=transform'
    );
    await waitFor(() =>
      expect(screen.getByTestId('ndv')).toHaveAttribute('data-state', 'closed')
    );
    fireEvent.keyDown(document.body, { key: 'Escape' });
    await waitFor(() =>
      expect(screen.getByTestId('loc').dataset.url).toBe(
        '/dags/demo?run=run-1&def=run'
      )
    );
  });

  // ?ndv=docked is the YAML panel of the whole file, as in the editor: a
  // step in the link only selects it.
  it('opens the YAML panel of a ?step&ndv=docked link, docked', async () => {
    renderCanvas('/dags/demo?run=run-1&def=run&step=transform&ndv=docked');

    const ndv = await screen.findByTestId('ndv');
    await waitFor(() => expect(ndv).toHaveAttribute('data-state', 'docked'));
    expect(screen.queryByTestId('ve-inspector')).toBeNull();
    expect(lastEditor().selectedStepName).toBe('transform');
  });

  it('shows no step view for ?ndv without a step', async () => {
    renderCanvas('/dags/demo?run=run-1&def=run&ndv=expanded');
    await findNode('steps[0]');

    expect(screen.getByTestId('ndv')).toHaveAttribute('data-state', 'closed');
  });

  // The node view needs the room: the Run panel collapses while it is open.
  describe('Run panel', () => {
    it('collapses when the step view opens and comes back when it closes', async () => {
      h.dock.open = true;
      renderCanvas();
      const stepNode = await findNode('steps[1]');
      fireEvent.click(stepNode);
      expect(h.dock.yieldToView).not.toHaveBeenCalled();
      fireEvent.doubleClick(stepNode);
      await screen.findByTestId('ndv');

      expect(h.dock.yieldToView).toHaveBeenLastCalledWith(true, {
        restore: true,
      });
      fireEvent.keyDown(document.body, { key: 'Escape' });
      await waitFor(() =>
        expect(h.dock.yieldToView).toHaveBeenLastCalledWith(false)
      );
    });

    it('collapses on Enter too', async () => {
      h.dock.open = true;
      renderCanvas();
      const stepNode = await findNode('steps[1]');
      fireEvent.click(stepNode);
      fireEvent.keyDown(stepNode, { key: 'Enter' });
      await screen.findByTestId('ndv');
      expect(h.dock.yieldToView).toHaveBeenLastCalledWith(true, {
        restore: true,
      });
    });

    it('collapses for a ?step&ndv link', async () => {
      h.dock.open = true;
      renderCanvas('/dags/demo?run=run-1&def=run&step=transform&ndv=expanded');
      await screen.findByTestId('ndv');
      expect(h.dock.yieldToView).toHaveBeenCalledWith(true, { restore: true });
    });

    it('does not change the panel while no step view is open', async () => {
      h.dock.open = true;
      renderCanvas();
      fireEvent.click(await findNode('steps[1]'));
      expect(h.dock.yieldToView).not.toHaveBeenCalled();
    });

    // The click of the double click does not open a collapsed panel: the view
    // gives the panel back as it was, collapsed.
    it('gives a collapsed panel back as it was', async () => {
      h.dock.open = false;
      renderCanvas();
      const stepNode = await findNode('steps[1]');
      fireEvent.click(stepNode);
      expect(h.dock.setTab).not.toHaveBeenCalled();
      fireEvent.doubleClick(stepNode);
      await screen.findByTestId('ndv');
      expect(h.dock.yieldToView).toHaveBeenLastCalledWith(true, {
        restore: true,
      });
    });

    it('does not open the panel for a step picked in the open view', async () => {
      renderCanvas('/dags/demo?run=run-1&def=run&step=fetch&ndv=expanded');
      const ndv = await screen.findByTestId('ndv');
      fireEvent.click(within(ndv).getByRole('button', { name: 'Next step' }));
      await waitFor(() =>
        expect(screen.getByTestId('loc').dataset.url).toContain(
          'step=transform'
        )
      );
      expect(h.dock.setTab).not.toHaveBeenCalled();
    });
  });

  // The YAML of the step view is the read-only editor of the edit mode, on
  // the one step of the definition that the run used.
  describe('YAML of the step view', () => {
    // A double click opens the step on its YAML; a link opens its form.
    async function openStepYaml(): Promise<void> {
      renderCanvas();
      const stepNode = await findNode('steps[1]');
      fireEvent.click(stepNode);
      fireEvent.doubleClick(stepNode);
    }
    // The step block in yamlArraySteps: the lines of '- name: transform'.
    const lines = yamlArraySteps.split('\n');
    const first = lines.findIndex((l) => l.includes('- name: transform')) + 1;

    it('shows the definition of the run in a read-only editor, no plain text', async () => {
      await openStepYaml();
      const ndv = await screen.findByTestId('ndv');
      const editor = await within(ndv).findByTestId('run-yaml-editor');

      expect(editor).toHaveAttribute('readonly');
      expect(editor).toHaveValue(yamlArraySteps);
      expect(h.yaml.props).toMatchObject({
        readOnly: true,
        showDocsButton: false,
      });
      // One model for the run, not the one of the edit mode.
      expect(String(h.yaml.props?.modelUri)).toContain('/dag-runs/demo/run-1');
      expect(ndv.querySelector('pre')).toBeNull();
    });

    it('shows the step alone, from line 1, with no change bars', async () => {
      await openStepYaml();
      await screen.findByTestId('run-yaml-editor');

      await waitFor(() => expect(h.yaml.numbers).not.toBeNull());
      // The first line of the step reads 1, the next one 2.
      expect(h.yaml.numbers!(first)).toBe('1');
      expect(h.yaml.numbers!(first + 1)).toBe('2');
      // Everything above and below the step is hidden.
      const areas = h.yaml.hidden[h.yaml.hidden.length - 1]!;
      expect(areas[0]).toMatchObject({ startLine: 1, endLine: first - 1 });
      expect(areas[areas.length - 1]!.endLine).toBe(lines.length);
      // No baseline: no bar for a line that differs from a saved text.
      expect(lastEditor().savedText).toBeUndefined();
      expect(lastEditor().editable).toBe(false);
      expect(h.yaml.bars).toEqual([]);
    });

    it('follows the step that the view shows', async () => {
      await openStepYaml();
      const ndv = await screen.findByTestId('ndv');
      await waitFor(() => expect(h.yaml.numbers).not.toBeNull());
      fireEvent.click(
        within(ndv).getByRole('button', { name: 'Previous step' })
      );
      const fetchFirst =
        lines.findIndex((l) => l.includes('- name: fetch')) + 1;
      await waitFor(() => expect(h.yaml.numbers!(fetchFirst)).toBe('1'));
    });

    it('keeps the editor mounted when the form shows', async () => {
      await openStepYaml();
      await screen.findByTestId('run-yaml-editor');
      await showForm();
      expect(h.yaml.mounts).toBe(1);
      expect(screen.getByTestId('run-yaml-editor')).toBeInTheDocument();
    });

    it('has a Copy YAML button that copies the definition', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined);
      Object.defineProperty(navigator, 'clipboard', {
        value: { writeText },
        configurable: true,
      });
      await openStepYaml();
      const ndv = await screen.findByTestId('ndv');
      fireEvent.click(
        await within(ndv).findByRole('button', { name: 'Copy YAML' })
      );
      await waitFor(() =>
        expect(writeText).toHaveBeenCalledWith(yamlArraySteps)
      );
    });
  });

  // The YAML button of the toolbar opens the whole definition of the run in
  // the right panel of the editor: the same component, read-only.
  describe('YAML panel of the whole file', () => {
    const clipboard = vi.fn().mockResolvedValue(undefined);
    async function openPanel(
      url = '/dags/demo?run=run-1&def=run'
    ): Promise<HTMLElement> {
      Object.defineProperty(navigator, 'clipboard', {
        value: { writeText: clipboard },
        configurable: true,
      });
      renderCanvas(url);
      await findNode('steps[0]');
      fireEvent.click(screen.getByRole('button', { name: 'YAML' }));
      const ndv = await screen.findByTestId('ndv');
      await waitFor(() => expect(ndv).toHaveAttribute('data-state', 'docked'));
      return ndv;
    }

    it('opens from the YAML button with the ?ndv=docked flag of the editor', async () => {
      renderCanvas();
      await findNode('steps[0]');
      const button = screen.getByRole('button', { name: 'YAML' });
      expect(button).toHaveAttribute('aria-pressed', 'false');
      fireEvent.click(button);

      const ndv = await screen.findByTestId('ndv');
      await waitFor(() => expect(ndv).toHaveAttribute('data-state', 'docked'));
      expect(screen.getByTestId('loc').dataset.url).toBe(
        '/dags/demo?run=run-1&def=run&ndv=docked'
      );
      expect(button).toHaveAttribute('aria-pressed', 'true');
      // The panel of the editor: right shell in the zone, 48 px header.
      expect(ndv).toHaveAttribute('data-panel-shell', 'right');
      expect(ndv).toHaveAttribute('data-placement', 'zone');
      expect(ndv.querySelector('[data-panel-header]')).not.toBeNull();
      expect(
        within(ndv).getByRole('heading', { name: 'YAML' })
      ).toBeInTheDocument();
    });

    it('opens from a link with ?ndv=docked and no step', async () => {
      renderCanvas('/dags/demo?run=run-1&def=run&ndv=docked');
      const ndv = await screen.findByTestId('ndv');
      await waitFor(() => expect(ndv).toHaveAttribute('data-state', 'docked'));
      expect(await screen.findByTestId('run-yaml-editor')).toBeVisible();
    });

    it('shows the snapshot of the run, whole and read-only', async () => {
      h.runSpec = yamlArraySteps;
      h.savedSpec = 'name: saved\n';
      const ndv = await openPanel();
      const editor = await within(ndv).findByTestId('run-yaml-editor');

      expect(editor).toHaveAttribute('readonly');
      expect(editor).toHaveValue(yamlArraySteps);
      expect(h.yaml.props).toMatchObject({
        readOnly: true,
        showDocsButton: false,
      });
      expect(h.yaml.props?.onChange).toBeUndefined();
      expect(String(h.yaml.props?.modelUri)).toContain('/dag-runs/demo/run-1');
      // The whole file: no hidden lines, no change bars.
      expect(h.yaml.hidden.filter((areas) => areas.length > 0)).toEqual([]);
      expect(h.yaml.bars).toEqual([]);
      expect(h.yaml.props?.scopePath ?? null).toBeNull();
    });

    it('names the run in the header and marks it read-only with a lock', async () => {
      const ndv = await openPanel();
      const lock = within(ndv).getByTestId('readonly-marker');
      expect(lock).toHaveAccessibleName('Read-only: definition of run run-1');
      // A lock, not a chip, and no "Run X definition" text.
      expect(within(ndv).queryByTestId('yaml-readonly-chip')).toBeNull();
      expect(within(ndv).queryByText(/definition$/)).toBeNull();
      expect(within(ndv).getByTestId('yaml-run-ref')).toHaveTextContent(
        'run-1'
      );
    });

    it('has no text tools', async () => {
      const ndv = await openPanel();
      expect(within(ndv).queryByTestId('yaml-fix-all')).toBeNull();
      expect(within(ndv).queryByTestId('yaml-format')).toBeNull();
      expect(lastEditor().editable).toBe(false);
    });

    it('has the Copy YAML overlay that copies the run definition', async () => {
      clipboard.mockClear();
      const ndv = await openPanel();
      fireEvent.click(within(ndv).getByRole('button', { name: 'Copy YAML' }));
      await waitFor(() =>
        expect(clipboard).toHaveBeenCalledWith(yamlArraySteps)
      );
    });

    it('closes with the close button', async () => {
      const ndv = await openPanel();
      fireEvent.click(within(ndv).getByRole('button', { name: 'Close' }));
      await waitFor(() => expect(ndv).toHaveAttribute('data-state', 'closed'));
      expect(screen.getByTestId('loc').dataset.url).toBe(
        '/dags/demo?run=run-1&def=run'
      );
    });

    it('closes on Escape and keeps the selected step', async () => {
      renderCanvas('/dags/demo?run=run-1&def=run&step=transform');
      await findNode('steps[0]');
      const button = screen.getByRole('button', { name: 'YAML' });
      button.focus();
      fireEvent.click(button);
      const ndv = await screen.findByTestId('ndv');
      await waitFor(() => expect(ndv).toHaveAttribute('data-state', 'docked'));

      fireEvent.keyDown(document.body, { key: 'Escape' });
      await waitFor(() => expect(ndv).toHaveAttribute('data-state', 'closed'));
      expect(screen.getByTestId('loc').dataset.url).toBe(
        '/dags/demo?run=run-1&def=run&step=transform'
      );
      expect(document.activeElement).toBe(button);
    });

    it('gives its room to the other right panels', async () => {
      const ndv = await openPanel();
      expect(ndv).toHaveAttribute('data-state', 'docked');
      // The Run panel keeps its height: only the step view takes its room.
      expect(h.dock.yieldToView).not.toHaveBeenCalledWith(
        true,
        expect.anything()
      );
    });
  });

  it('runs the step of the view only with the right to execute', async () => {
    renderCanvas();
    const stepNode = await findNode('steps[1]');
    fireEvent.click(stepNode);
    fireEvent.doubleClick(stepNode);
    const ndv = await screen.findByTestId('ndv');
    fireEvent.click(within(ndv).getByRole('button', { name: 'Run step' }));

    // The Run step button of the view asks the host to run the step.
    expect(h.actions.runStep).toHaveBeenCalledWith('transform');
  });

  // The INPUT column reads the references of the definition of the run, as
  // the editor does for its saved text.
  it('lists the upstream data of the step in the INPUT column', async () => {
    h.post.mockResolvedValue({ data: { referenceScope: scopeHttpJq } });
    h.runSpec = yamlHttpJq;
    h.run = makeRun(
      runDetails(
        'run-1',
        [
          node('fetch'),
          node('extract'),
          node('publish', NodeStatus.NotStarted),
        ],
        Status.Success
      )
    );
    renderCanvas('/dags/demo?run=run-1&def=run&step=extract&ndv=expanded');

    const input = await screen.findByRole('region', { name: 'Input' });
    await waitFor(() =>
      expect(within(input).getByText('Direct upstream')).toBeInTheDocument()
    );
    expect(within(input).queryByText('No reference data yet')).toBeNull();
    expect(h.post).toHaveBeenCalledWith(
      '/dags/validate',
      expect.objectContaining({
        body: { spec: yamlHttpJq, name: 'demo' },
      })
    );
  });

  it('reads no reference data while no step view is open', async () => {
    renderCanvas();
    await findNode('steps[0]');

    expect(h.post).not.toHaveBeenCalled();
  });
});

const yamlInferred = `name: inferred
steps:
  - name: produce
    run: echo hi > out.txt
  - name: consume
    run: cat out.txt
`;

const yamlAgent = `type: agent
name: helper
steps:
  - name: search
    run: echo search
  - name: answer
    run: echo answer
`;

const tasks = [
  { name: 'find', status: AgentTaskStatus.open },
  { name: 'reply', status: AgentTaskStatus.open },
];

/** The fields of the graph that the YAML and the DAG decide, not the run. */
function shape(graph: CanvasGraph) {
  return {
    nodes: graph.nodes.map((node) => {
      const rest = { ...node };
      delete rest.run;
      return rest;
    }),
    edges: graph.edges,
    editableEdges: graph.editableEdges,
  };
}

function editorGraph(yaml: string, dag: ApiDAGDetails): CanvasGraph {
  const model = parseDagDoc(yaml);
  const names = resolveStepNames(model, dag);
  const issues = groupIssues(model, [], names);
  return buildCanvasGraph({ model, names, details: dag, issues });
}

describe.skipIf(realSchema === null)('RunSnapshotCanvas graph', () => {
  // parity:history-graph parity:crit-history-graph-timeline-subtabs
  it.each([
    {
      label: 'router',
      yaml: yamlRouter,
      steps: [{ name: 'pick' }, { name: 'server_error' }, { name: 'other' }],
      extra: {},
      saved: {},
    },
    {
      label: 'inferred edges',
      yaml: yamlInferred,
      steps: [
        { name: 'produce' },
        { name: 'consume', inferredDepends: ['produce'] },
      ],
      extra: {},
      saved: {},
    },
    {
      label: 'agent',
      yaml: yamlAgent,
      steps: [{ name: 'search' }, { name: 'answer' }],
      extra: { agentTasks: tasks },
      saved: { tasks },
    },
  ])('draws the $label graph as the editor does', async (fixture) => {
    h.runSpec = fixture.yaml;
    const nodes = fixture.steps.map((step) =>
      node(step.name, NodeStatus.Success, { step })
    );
    if (fixture.label === 'agent') nodes.push(node('__agent__'));
    h.run = makeRun(runDetails('run-1', nodes, Status.Success, fixture.extra));
    renderCanvas();
    await findNode('steps[0]');

    const saved = {
      name: 'demo',
      steps: fixture.steps,
      ...fixture.saved,
    } as unknown as ApiDAGDetails;
    const expected = editorGraph(fixture.yaml, saved);
    expect(shape(lastGraph())).toEqual(shape(expected));
    expect(expected.edges.length).toBeGreaterThan(0);
  });
});

describe.skipIf(realSchema === null)('RunSnapshotCanvas agent run', () => {
  const agentTasks = [
    { name: 'find', status: AgentTaskStatus.completed },
    { name: 'reply', status: AgentTaskStatus.open },
  ];
  const agentEvents = [
    { turn: 1, kind: 'action', name: 'search', status: 'succeeded' },
    { turn: 2, kind: 'action', name: 'search', status: 'succeeded' },
    { turn: 3, kind: 'task_status', name: 'find', status: 'completed' },
  ];

  function agentRunDetails(extra: Partial<ApiDAGRunDetails> = {}) {
    return runDetails(
      'run-1',
      [
        node('search', NodeStatus.Success, { step: { name: 'search' } }),
        node('answer', NodeStatus.Success, { step: { name: 'answer' } }),
        node('__agent__'),
      ],
      Status.Success,
      extra
    );
  }

  // The tool cards, the hub and the checklist read the run of the selection.
  it('shows the calls, the hub state and the tasks of the selected run', async () => {
    h.runSpec = yamlAgent;
    h.run = makeRun(
      agentRunDetails({ agentTasks, agentEvents } as Partial<ApiDAGRunDetails>)
    );
    renderCanvas();
    const called = await findNode('steps[0]');
    const never = await findNode('steps[1]');

    expect(
      within(called).getByTestId('ve-canvas-tool-calls')
    ).toHaveTextContent('2');
    expect(called).not.toHaveAttribute('data-dimmed');
    expect(never).toHaveAttribute('data-dimmed', 'true');
    expect(within(never).queryByTestId('ve-canvas-tool-calls')).toBeNull();

    expect(screen.getByTestId('ve-canvas-hub-iterations')).toHaveTextContent(
      'Iterations: 3'
    );
    expect(screen.getByTestId('ve-canvas-hub-settled')).toHaveTextContent(
      '1 of 2 tasks settled'
    );
    expect(
      screen
        .getAllByTestId('ve-canvas-hub-task')
        .map((row) => row.getAttribute('data-state'))
    ).toEqual(['completed', 'open']);
  });

  it('dims every tool of an agent run that has made no call yet', async () => {
    h.runSpec = yamlAgent;
    h.run = makeRun(agentRunDetails());
    renderCanvas();
    const card = await findNode('steps[0]');

    expect(card).toHaveAttribute('data-dimmed', 'true');
    expect(screen.getByTestId('ve-canvas-hub-iterations')).toHaveTextContent(
      'Iterations: 0'
    );
  });

  it('shows no agent facts for the run of a DAG that is not an agent', async () => {
    renderCanvas();
    await findNode('steps[0]');

    expect(screen.queryByTestId('ve-canvas-tool-calls')).toBeNull();
    expect(screen.queryByTestId('ve-canvas-hub-iterations')).toBeNull();
  });

  // The base key 'open' is a verb in zh-CN and ja.
  it('labels a pending task with its own key', async () => {
    h.runSpec = yamlAgent;
    h.run = makeRun(
      agentRunDetails({ agentTasks, agentEvents } as Partial<ApiDAGRunDetails>)
    );
    renderCanvas();
    await findNode('steps[0]');

    expect(screen.getByLabelText('Task pending')).toBeInTheDocument();
    expect(screen.queryByLabelText('open')).toBeNull();
  });
});

// The tool cards of a long agent run wrap into rows of the compact grid of
// the editor, so that 20, 50 or 100 tools stay readable.
describe.skipIf(realSchema === null)('RunSnapshotCanvas agent grid', () => {
  function position(card: HTMLElement): { x: number; y: number } {
    let el: HTMLElement | null = card;
    while (el) {
      const match = /translate\((-?[\d.]+)px, (-?[\d.]+)px\)/.exec(
        el.style.transform
      );
      if (match) return { x: Number(match[1]), y: Number(match[2]) };
      el = el.parentElement;
    }
    throw new Error('no positioned ancestor');
  }

  it.each([20, 50, 100])('wraps the %i tools of a run into rows', async (n) => {
    const names = Array.from({ length: n }, (_, i) => `tool${i}`);
    h.runSpec = `type: agent\nname: helper\nsteps:\n${names
      .map((name) => `  - name: ${name}\n    run: echo ${name}\n`)
      .join('')}`;
    h.run = makeRun(
      runDetails(
        'run-1',
        [
          ...names.map((name) =>
            node(name, NodeStatus.Success, { step: { name } })
          ),
          node('__agent__'),
        ],
        Status.Success
      )
    );
    renderCanvas();
    const first = position(await findNode('steps[0]'));
    const last = position(await findNode(`steps[${n - 1}]`));
    const sixth = position(await findNode('steps[5]'));
    const seventh = position(await findNode('steps[6]'));

    // Six columns in one row; the seventh tool starts the next row.
    expect(sixth.y).toBe(first.y);
    expect(seventh.y).toBeGreaterThan(first.y);
    expect(seventh.x).toBe(first.x);
    // Rows, not a line: the grid is never taller than wide by n cards.
    expect(last.y - first.y).toBeGreaterThan(0);
    expect(last.x - first.x).toBeLessThan(6 * 200);
  });
  // A run that starts from the step view becomes the selected run. The view
  // does not unmount while the definition of that run loads, and it keeps
  // its mode and its editor.
  it('keeps the step view, its YAML and its editor when the run changes', async () => {
    const url = '/dags/demo?run=run-1&def=run&step=transform&ndv=expanded';
    const view = render(canvasTree(url));
    const ndv = await screen.findByTestId('ndv');
    const yamlButton = () =>
      within(screen.getByTestId('ndv')).getByRole('button', {
        name: 'YAML',
        description: '',
      });
    await waitFor(() =>
      expect(
        within(ndv).getByRole('group', { name: 'Editor' })
      ).toBeInTheDocument()
    );
    const editors = h.yaml.mounts;
    expect(editors).toBe(1);
    expect(
      within(ndv).getByRole('button', { name: 'YAML', pressed: true })
    ).toBeInTheDocument();

    h.run = makeRun(
      runDetails('run-2', [node('fetch'), node('transform')], Status.Success)
    );
    h.runLoading = true;
    view.rerender(canvasTree(url));
    // The step view of the first render is still there, in the same mode.
    expect(screen.getByTestId('ndv')).toBe(ndv);
    expect(yamlButton()).toHaveAttribute('aria-pressed', 'true');

    h.runLoading = false;
    view.rerender(canvasTree(url));
    await waitFor(() =>
      expect(screen.getByTestId('ww-run-definition')).toHaveAttribute(
        'data-run-id',
        'run-2'
      )
    );
    expect(screen.getByTestId('ndv')).toBe(ndv);
    expect(yamlButton()).toHaveAttribute('aria-pressed', 'true');
    expect(h.yaml.mounts).toBe(editors);
  });

  // The mode of the last step view is the one of the next view: after the
  // form, a link to a step opens the form.
  it('opens a linked step in the last mode of the session', async () => {
    sessionStorage.setItem('dagu.stepView.mode', 'form');
    renderCanvas('/dags/demo?run=run-1&def=run&step=transform&ndv=expanded');
    const ndv = await screen.findByTestId('ndv');
    expect(
      await within(ndv).findByRole('button', { name: 'Form', pressed: true })
    ).toBeInTheDocument();
  });
});
