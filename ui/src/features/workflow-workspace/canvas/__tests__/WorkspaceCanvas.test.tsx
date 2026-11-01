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
import React, { useImperativeHandle } from 'react';
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigationType,
} from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, Status } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import type DAGSpec from '@/features/dags/components/dag-editor/DAGSpec';
import { usePanelPlacement } from '@/features/dags/components/step-details/PanelShell';
import type {
  ApiDAGRunDetails,
  DAGPageData,
  WorkspaceEditorHandle,
  WorkspaceEditorState,
  WorkspacePermissions,
  WorkspaceRunValue,
} from '../../contracts';
import type { DockController } from '../../dock/DockContext';
import type { StepPanelProps } from '../../step/StepPanel';
import type { StepRunActions } from '../../step/useStepRunActions';
import { useWorkspaceUrl } from '../../state/workspaceUrl';
import { WorkspaceCanvas } from '../WorkspaceCanvas';
import {
  useWorkspaceEditor,
  WorkspaceEditorProvider,
} from '../WorkspaceEditorContext';

type SpecProps = React.ComponentProps<typeof DAGSpec>;

const host = vi.hoisted(() => ({
  /** Props of the last DAGSpec render. */
  props: null as SpecProps | null,
  /** Renders the real DAGSpec in place of the stand-in. */
  real: false,
  /** What the stand-in gives to editorHandleRef; null gives nothing. */
  handle: null as unknown,
}));

// Captures the props; the stand-in gives the handle as DAGSpec does once loaded.
vi.mock(
  '@/features/dags/components/dag-editor/DAGSpec',
  async (importOriginal) => {
    const actual =
      await importOriginal<
        typeof import('@/features/dags/components/dag-editor/DAGSpec')
      >();
    function StandIn(props: SpecProps) {
      useImperativeHandle(
        host.handle ? props.editorHandleRef : undefined,
        () => host.handle as WorkspaceEditorHandle,
        []
      );
      return (
        <div
          data-testid="dag-spec-stand-in"
          data-placement={usePanelPlacement()}
        />
      );
    }
    function CapturingDAGSpec(props: SpecProps) {
      host.props = props;
      return host.real ? <actual.default {...props} /> : <StandIn {...props} />;
    }
    return { default: CapturingDAGSpec };
  }
);

const useStepRunActionsOptions = vi.hoisted(() => vi.fn());
const ws = vi.hoisted(() => ({
  run: null as unknown,
  permissions: null as unknown,
  actions: null as unknown,
  dock: null as unknown,
}));
vi.mock('../../data', () => ({
  useWorkspaceRun: () => ws.run,
  useWorkspacePermissions: () => ws.permissions,
}));
vi.mock('../../step/useStepRunActions', () => ({
  useStepRunActions: (opts?: unknown) => {
    useStepRunActionsOptions(opts);
    return ws.actions;
  },
}));
// The reuse source of the step run dialog; the INPUT run is the same run.
const reuse = vi.hoisted(() => ({
  state: null as unknown,
  args: vi.fn(),
}));
vi.mock('../../step/useReuseSource', () => ({
  useReuseSource: (p: unknown) => {
    reuse.args(p);
    return reuse.state;
  },
}));
vi.mock('../../dock/DockContext', () => ({
  useDock: () => ws.dock,
  useDockCommands: () => ws.dock,
}));
vi.mock('../../notices/useWorkspaceNotices', () => ({
  useWorkspaceNotices: () => [],
}));
// The panel has its own tests: a stand-in that gives its props.
const panel = vi.hoisted(() => ({ props: null as StepPanelProps | null }));
vi.mock('../../step/StepPanel', () => ({
  StepPanel: function StandInPanel(p: StepPanelProps) {
    panel.props = p;
    return (
      <aside data-testid="step-panel" data-step={p.step} data-step-panel="">
        <button type="button" onClick={p.onClose}>
          close panel
        </button>
      </aside>
    );
  },
}));
// The snapshot has its own tests: a stand-in with the Back button.
vi.mock('../../executions/RunSnapshotCanvas', () => ({
  RunSnapshotCanvas: function Snapshot() {
    const [, setUrl] = useWorkspaceUrl();
    return (
      <button type="button" onClick={() => setUrl({ def: null })}>
        snapshot back
      </button>
    );
  },
}));

// The API and the shared contexts, for the test with the real editor.
const SPEC_ONE = 'steps:\n  - name: extract\n    run: echo hello\n';
const api = vi.hoisted(() => ({
  queries: [] as { path: string; init: unknown }[],
  specLoading: false,
  saved: 0,
  spec: 'steps:\n  - name: extract\n    run: echo hello\n',
  steps: [{ name: 'extract' }] as { name: string }[],
}));
// The responses keep their identity between renders, as SWR gives them: a
// new object on every call would make the editor load again on every render.
const respond = vi.hoisted(() => {
  const mutate = () => undefined;
  let key = '';
  let spec: unknown = null;
  return {
    mutate,
    specOf(text: string, steps: unknown[]) {
      const next = JSON.stringify([text, steps]);
      if (next !== key) {
        key = next;
        spec = {
          data: {
            dag: { name: 'example', steps },
            spec: text,
            sha256: 'sha',
            errors: [],
            warnings: [],
            valueReferenceNotices: [],
          },
          isLoading: false,
          mutate,
        };
      }
      return spec;
    },
    loading: { data: undefined, isLoading: true, mutate },
    none: { data: undefined, isLoading: false, mutate },
    client: {
      GET: async () => ({ data: undefined }),
      POST: async () => ({ data: undefined }),
      // A save puts the text on disk: the next read of the spec returns it.
      PUT: async (_path: string, init: { body: { spec: string } }) => {
        api.spec = init.body.spec;
        api.saved += 1;
        return { data: undefined };
      },
    },
  };
});
vi.mock('@/hooks/api', () => ({
  useQuery: (path: string, init: unknown) => {
    api.queries.push({ path, init });
    if (path === '/dags/{fileName}/spec' && init !== null) {
      return api.specLoading
        ? respond.loading
        : respond.specOf(api.spec, api.steps);
    }
    return respond.none;
  },
  useClient: () => respond.client,
}));
vi.mock('@/hooks/useDAGSSE', () => ({ useDAGSSE: () => null }));
vi.mock('@/hooks/useDAGRunSSE', () => ({
  useDAGRunSSE: () => ({
    data: null,
    isConnected: false,
    isConnecting: false,
    shouldUseFallback: true,
  }),
}));
vi.mock('@/hooks/useSSECacheSync', () => ({
  sseFallbackOptions: () => ({}),
  useSSECacheSync: vi.fn(),
}));
vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteForWorkspace: () => true,
  useCanExecute: () => true,
  useCanManageWebhooks: () => false,
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('@/contexts/SchemaContext', () => ({
  useSchema: () => ({ schema: null }),
}));
vi.mock('@/contexts/UnsavedChangesContext', () => ({
  useUnsavedChanges: () => ({ setHasUnsavedChanges: vi.fn() }),
}));
vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: vi.fn() }),
}));
vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: vi.fn() }),
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
vi.mock('@/features/dags/components/dag-editor/DAGEditorWithDocs', () => ({
  default: () => null,
}));
vi.mock('@/features/dags/components/dag-editor/ExternalChangeDialog', () => ({
  default: () => null,
}));
vi.mock('@/features/dags/components/step-details', () => ({
  StepDetailsDrawer: () => null,
}));
vi.mock('@/features/dags/components/value-reference-notices', () => ({
  ValueReferenceNoticesButton: () => null,
}));

const dagData = {
  dag: { name: 'example' },
  localDags: [{ name: 'child', errors: [] }],
  editorHints: { customActions: [] },
  latestDAGRun: {},
  suspended: false,
  pinned: false,
  errors: [],
} as unknown as DAGPageData;

function runDetails(): ApiDAGRunDetails {
  return {
    name: 'example',
    dagRunId: 'run-7',
    status: Status.Failed,
    nodes: [
      {
        step: { name: 'extract' },
        status: NodeStatus.Failed,
        statusLabel: 'failed',
        retryCount: 0,
        doneCount: 1,
      },
    ],
  } as unknown as ApiDAGRunDetails;
}

function makeRun(over: Partial<WorkspaceRunValue> = {}): WorkspaceRunValue {
  return {
    selection: 'latest',
    ref: { name: 'example', runId: 'run-7' },
    details: runDetails(),
    hasRun: true,
    isLatest: true,
    isActive: false,
    notFound: false,
    latestRef: { runId: 'run-7', status: Status.Failed },
    overlay: {},
    nodeByStep: new Map(),
    handlers: [],
    waiting: { approvals: 0, humanTasks: 0, agent: 0, firstWaitingStep: null },
    failedStep: 'extract',
    select: vi.fn(),
    applyNodeStatus: vi.fn(),
    refresh: vi.fn(async () => undefined),
    ...over,
  };
}

function makePermissions(canExecute: boolean): WorkspacePermissions {
  return {
    canWrite: true,
    canRunDags: true,
    canExecute,
    canExecuteForWorkspace: true,
    isAdmin: false,
    canManageProfiles: false,
    canManageWebhooks: false,
    canManageNotifications: false,
  };
}

function makeActions(): StepRunActions {
  return {
    can: vi.fn((step: string) => ({
      retry: step === 'a',
      setStatus: step === 'b',
      runStep: true,
      subRuns: step === 'c',
    })),
    retryStep: vi.fn(),
    setStatus: vi.fn(),
    runStep: vi.fn(),
    runUpstream: vi.fn(),
    followed: [],
    clearFollowed: vi.fn(),
    openSubRun: vi.fn(),
    openLogs: vi.fn(),
    dialogs: <div data-testid="step-dialogs" />,
  };
}

function makeDock(): DockController {
  return {
    open: false,
    tab: 'log',
    height: 0,
    maximized: false,
    setTab: vi.fn(),
    toggle: vi.fn(),
    maximize: vi.fn(),
    openStep: vi.fn(),
    openRunLog: vi.fn(),
  };
}

function LocationProbe() {
  const location = useLocation();
  const navigationType = useNavigationType();
  return (
    <output data-testid="location" data-navigation={navigationType}>
      {location.pathname + location.search}
    </output>
  );
}

// The handle that the bound editor gave, for the tests that edit.
let boundHandle: WorkspaceEditorHandle | null = null;

function EditorProbe() {
  const { handle, state } = useWorkspaceEditor();
  boundHandle = handle;
  return (
    <output
      data-testid="editor"
      data-handle={handle ? 'bound' : 'none'}
      data-dirty={state ? String(state.dirty) : 'none'}
    />
  );
}

function renderCanvas(url = '/dags/example.yaml') {
  return render(
    <UserPreferencesProvider>
      <AppBarContext.Provider
        value={{
          title: 'DAGs',
          setTitle: vi.fn(),
          remoteNodes: ['local'],
          setRemoteNodes: vi.fn(),
          selectedRemoteNode: 'local',
          selectRemoteNode: vi.fn(),
        }}
      >
        <MemoryRouter initialEntries={[url]}>
          <Routes>
            {['/dags/:fileName', '/dags/:fileName/:tab'].map((path) => (
              <Route
                key={path}
                path={path}
                element={
                  <WorkspaceEditorProvider>
                    <LocationProbe />
                    <EditorProbe />
                    <WorkspaceCanvas
                      fileName="example.yaml"
                      dagData={dagData}
                    />
                  </WorkspaceEditorProvider>
                }
              />
            ))}
          </Routes>
        </MemoryRouter>
      </AppBarContext.Provider>
    </UserPreferencesProvider>
  );
}

function props(): SpecProps {
  if (!host.props) throw new Error('DAGSpec did not render');
  return host.props;
}

function location() {
  return screen.getByTestId('location');
}

let run: WorkspaceRunValue;
let actions: StepRunActions;
let dock: DockController;

beforeEach(() => {
  host.props = null;
  panel.props = null;
  host.real = false;
  host.handle = null;
  api.queries = [];
  api.specLoading = false;
  api.saved = 0;
  api.spec = SPEC_ONE;
  api.steps = [{ name: 'extract' }];
  run = makeRun();
  actions = makeActions();
  dock = makeDock();
  ws.run = run;
  ws.permissions = makePermissions(true);
  ws.actions = actions;
  ws.dock = dock;
  reuse.state = {
    source: null,
    waitingFor: null,
    stale: [],
    missing: [],
    headChanged: false,
    paramsDiffer: false,
    loading: false,
    options: [],
    selectedOptionId: null,
    choose: () => undefined,
    cached: [],
    cachedRuns: {},
    gone: [],
    stepRuns: {},
    cacheCapturedAt: null,
    cacheTimes: {},
  };
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('WorkspaceCanvas', () => {
  // The graph zone: the right panels and the expanded node view are
  // absolute inside it and never exceed it.
  it('is the positioned graph zone that holds the panels', () => {
    renderCanvas();
    const zone = screen.getByTestId('ww-canvas');
    expect(zone).toHaveClass('relative', 'overflow-hidden');
    expect(screen.getByTestId('dag-spec-stand-in')).toHaveAttribute(
      'data-placement',
      'zone'
    );
    expect(zone).toContainElement(screen.getByTestId('dag-spec-stand-in'));
  });

  it('gives DAGSpec the page data and the host chrome', () => {
    renderCanvas();
    const p = props();
    expect(p.fileName).toBe('example.yaml');
    expect(p.localDags).toBe(dagData.localDags);
    expect(p.editorHints).toBe(dagData.editorHints);
    expect(p.fillHeight).toBe(true);
    expect(p.chrome).toEqual({ hideRunBar: true, compactNotices: true });
    expect(p.openNdvOn).toBe('open');
    expect(screen.getByTestId('step-dialogs')).toBeInTheDocument();
  });

  // No ?run is the latest run, ?run=X is run X: both come from the selected
  // run of the workspace (canvasRunSource).
  describe('run source', () => {
    it('is the latest run when no run is pinned', () => {
      renderCanvas();
      expect(props().runSource).toEqual({
        runId: 'run-7',
        details: run.details,
      });
    });

    it('is the pinned run', () => {
      const pinned = makeRun({
        selection: { runId: 'run-3' },
        ref: { name: 'example', runId: 'run-3' },
        details: { ...runDetails(), dagRunId: 'run-3' },
        isLatest: false,
      });
      ws.run = pinned;
      renderCanvas('/dags/example.yaml?run=run-3');
      expect(props().runSource).toEqual({
        runId: 'run-3',
        details: pinned.details,
      });
    });

    it('has no run before the workflow ran', () => {
      ws.run = makeRun({ ref: null, details: null, hasRun: false });
      renderCanvas();
      expect(props().runSource).toEqual({ runId: null, details: null });
    });

    // The step view shows the run tabs only: no run facts renderer.
    it('gives the OUTPUT column the run tabs and no run facts', () => {
      renderCanvas();
      expect(props().renderStepOutput).toBeTypeOf('function');
      expect(props().renderStepRun).toBeUndefined();
    });

    // One rebuild of the graph for each interval, not for each event.
    it('passes a live run at most once for each second', () => {
      vi.useFakeTimers();
      try {
        renderCanvas();
        // The first push comes at once; the next one waits for the interval.
        act(() => {
          vi.advanceTimersByTime(10);
        });
        const first = props().runSource?.details;
        expect(first).toBe(run.details);
        const next = { ...runDetails(), status: Status.Running };
        ws.run = makeRun({ details: next });
        // A re-render with the new details, as an event of the run causes.
        act(() => props().onSelectedStepNameChange?.('extract'));
        act(() => {
          vi.advanceTimersByTime(10);
        });
        expect(props().runSource?.details).toBe(first);
        act(() => {
          vi.advanceTimersByTime(1100);
        });
        expect(props().runSource?.details).toBe(next);
      } finally {
        vi.useRealTimers();
      }
    });

    // A run that is not the one shown never reaches the canvas as details.
    it('shows the new run at once', () => {
      vi.useFakeTimers();
      try {
        renderCanvas();
        const other = makeRun({
          selection: { runId: 'run-8' },
          ref: { name: 'example', runId: 'run-8' },
          details: { ...runDetails(), dagRunId: 'run-8' },
        });
        ws.run = other;
        act(() => props().onSelectedStepNameChange?.('extract'));
        act(() => {
          vi.advanceTimersByTime(10);
        });
        expect(props().runSource).toEqual({
          runId: 'run-8',
          details: other.details,
        });
      } finally {
        vi.useRealTimers();
      }
    });
  });

  describe('draft edit with a run shown', () => {
    const named = {
      step: { name: 'extract', id: 'ex' },
      status: NodeStatus.Success,
      statusLabel: 'succeeded',
      retryCount: 0,
      doneCount: 1,
    };
    const unnamed = (n: number) => ({
      ...named,
      step: { name: `cmd_${n}` },
    });

    function withNodes() {
      ws.run = makeRun({
        details: {
          ...runDetails(),
          nodes: [named, unnamed(2), unnamed(3)],
        } as unknown as ApiDAGRunDetails,
      });
    }
    const nodeNames = () =>
      (props().runSource?.details?.nodes ?? []).map((n) => n.step.name);

    it('keeps every status while the draft is clean', () => {
      withNodes();
      renderCanvas();
      expect(nodeNames()).toEqual(['extract', 'cmd_2', 'cmd_3']);
    });

    // The server names a step with no name by its position: an edit of the
    // draft shifts those names, so a status could land on another card.
    it('drops the statuses of unnamed steps once the draft changed', () => {
      withNodes();
      host.handle = {
        getBuffer: () =>
          'steps:\n  - run: echo new\n  - name: extract\n    run: a\n  - run: b\n  - run: c\n',
      } as unknown as WorkspaceEditorHandle;
      renderCanvas();
      act(() =>
        props().onEditorStateChange?.({ dirty: true } as WorkspaceEditorState)
      );
      expect(nodeNames()).toEqual(['extract']);
      // The run itself stays selected.
      expect(props().runSource?.runId).toBe('run-7');
    });

    it('keeps the status of a step that the draft names', () => {
      withNodes();
      host.handle = {
        getBuffer: () => 'steps:\n  - name: cmd_2\n    run: a\n',
      } as unknown as WorkspaceEditorHandle;
      renderCanvas();
      act(() =>
        props().onEditorStateChange?.({ dirty: true } as WorkspaceEditorState)
      );
      expect(nodeNames()).toEqual(['cmd_2']);
    });

    it('gives the statuses back when the draft is saved or discarded', () => {
      withNodes();
      host.handle = {
        getBuffer: () => 'steps:\n  - run: a\n',
      } as unknown as WorkspaceEditorHandle;
      renderCanvas();
      act(() =>
        props().onEditorStateChange?.({ dirty: true } as WorkspaceEditorState)
      );
      expect(nodeNames()).toEqual([]);
      act(() =>
        props().onEditorStateChange?.({ dirty: false } as WorkspaceEditorState)
      );
      expect(nodeNames()).toEqual(['extract', 'cmd_2', 'cmd_3']);
    });
  });

  describe('selection in the URL', () => {
    it('reads ?step and ?ndv', () => {
      renderCanvas('/dags/example.yaml?step=extract&ndv=expanded');
      expect(props().selectedStepName).toBe('extract');
      expect(props().ndv?.state).toBe('expanded');
    });

    it('gives null without ?step and ?ndv', () => {
      renderCanvas();
      expect(props().selectedStepName).toBeNull();
      expect(props().ndv?.state).toBeNull();
    });

    it('writes ?step with replace and keeps the other keys', () => {
      renderCanvas('/dags/example.yaml?run=r1&ndv=docked&dock=log&foo=1');
      act(() => props().onSelectedStepNameChange?.('load'));
      expect(location()).toHaveTextContent(
        '/dags/example.yaml?run=r1&step=load&ndv=docked&dock=log&foo=1'
      );
      expect(location()).toHaveAttribute('data-navigation', 'REPLACE');
      expect(props().selectedStepName).toBe('load');
    });

    it('removes ?step and ?ndv when the selection clears', () => {
      renderCanvas(
        '/dags/example.yaml?run=r1&step=extract&ndv=docked&dock=log'
      );
      act(() => props().onSelectedStepNameChange?.(null));
      expect(location()).toHaveTextContent(
        '/dags/example.yaml?run=r1&dock=log'
      );
      expect(location()).toHaveAttribute('data-navigation', 'REPLACE');
    });

    describe('Esc on the canvas', () => {
      it('clears ?step when no panel or node view is open', () => {
        renderCanvas('/dags/example.yaml?run=r1&step=extract&dock=log');
        fireEvent.keyDown(document.body, { key: 'Escape' });
        expect(location()).toHaveTextContent(
          '/dags/example.yaml?run=r1&dock=log'
        );
        expect(location()).toHaveAttribute('data-navigation', 'REPLACE');
      });

      // The link opens the step panel: the first Esc closes it.
      it('clears it when the focus is in the canvas zone', () => {
        renderCanvas('/dags/example.yaml?step=extract');
        const canvas = screen.getByTestId('dag-spec-stand-in');
        fireEvent.keyDown(canvas, { key: 'Escape' });
        expect(screen.queryByTestId('step-panel')).toBeNull();
        expect(location()).toHaveTextContent('step=extract');
        fireEvent.keyDown(canvas, { key: 'Escape' });
        expect(location()).toHaveTextContent('/dags/example.yaml');
        expect(location()).not.toHaveTextContent('step=');
      });

      it('first closes the node view, then clears the step', () => {
        renderCanvas('/dags/example.yaml?step=extract&ndv=expanded');
        fireEvent.keyDown(document.body, { key: 'Escape' });
        expect(location()).toHaveTextContent('/dags/example.yaml?step=extract');
        expect(location()).not.toHaveTextContent('ndv=');
        fireEvent.keyDown(document.body, { key: 'Escape' });
        expect(location()).not.toHaveTextContent('step=');
      });

      it('leaves the key to an open dialog and to text entry', () => {
        renderCanvas('/dags/example.yaml?step=extract');
        const dialog = document.createElement('div');
        dialog.setAttribute('role', 'dialog');
        dialog.setAttribute('data-state', 'open');
        document.body.appendChild(dialog);
        fireEvent.keyDown(document.body, { key: 'Escape' });
        dialog.remove();
        expect(location()).toHaveTextContent('step=extract');

        const input = document.createElement('input');
        document.body.appendChild(input);
        fireEvent.keyDown(input, { key: 'Escape' });
        input.remove();
        expect(location()).toHaveTextContent('step=extract');
      });

      // The text editor of the step view takes text, so the other layers
      // leave its Esc alone. The step view still closes on it, unless a
      // widget of the editor is open.
      describe('in the YAML of the node view', () => {
        function yamlField() {
          const frame = document.createElement('div');
          frame.setAttribute('data-testid', 'ndv');
          frame.innerHTML =
            '<div class="monaco-editor"><textarea></textarea></div>';
          document.body.appendChild(frame);
          return {
            field: frame.querySelector('textarea') as HTMLTextAreaElement,
            remove: () => frame.remove(),
          };
        }

        it('closes the node view', () => {
          renderCanvas('/dags/example.yaml?step=extract&ndv=expanded');
          const { field, remove } = yamlField();
          fireEvent.keyDown(field, { key: 'Escape' });
          remove();
          expect(location()).toHaveTextContent(
            '/dags/example.yaml?step=extract'
          );
          expect(location()).not.toHaveTextContent('ndv=');
        });

        // The editor marks every Esc as handled: it is the widget that counts.
        it('closes the view although the editor marks the key as handled', () => {
          renderCanvas('/dags/example.yaml?step=extract&ndv=expanded');
          const { field, remove } = yamlField();
          field.addEventListener('keydown', (event) => event.preventDefault());
          fireEvent.keyDown(field, { key: 'Escape' });
          remove();
          expect(location()).not.toHaveTextContent('ndv=');
        });

        it('leaves the view open while a widget of the editor is open', () => {
          renderCanvas('/dags/example.yaml?step=extract&ndv=expanded');
          const { field, remove } = yamlField();
          const widget = document.createElement('div');
          widget.className = 'suggest-widget visible';
          document.body.appendChild(widget);
          fireEvent.keyDown(field, { key: 'Escape' });
          widget.remove();
          remove();
          expect(location()).toHaveTextContent('ndv=expanded');
        });

        it('does nothing for a text field outside the node view', () => {
          renderCanvas('/dags/example.yaml?step=extract&ndv=expanded');
          const input = document.createElement('input');
          document.body.appendChild(input);
          fireEvent.keyDown(input, { key: 'Escape' });
          input.remove();
          expect(location()).toHaveTextContent('ndv=expanded');
        });
      });

      it('does nothing without a selected step', () => {
        renderCanvas('/dags/example.yaml?run=r1');
        fireEvent.keyDown(document.body, { key: 'Escape' });
        expect(location()).toHaveTextContent('/dags/example.yaml?run=r1');
      });
    });

    it('writes ?ndv with replace and removes it on null', () => {
      renderCanvas('/dags/example.yaml?step=extract');
      act(() => props().ndv?.onStateChange('docked'));
      expect(location()).toHaveTextContent(
        '/dags/example.yaml?step=extract&ndv=docked'
      );
      expect(location()).toHaveAttribute('data-navigation', 'REPLACE');
      expect(props().ndv?.state).toBe('docked');

      act(() => props().ndv?.onStateChange('expanded'));
      expect(props().ndv?.state).toBe('expanded');

      act(() => props().ndv?.onStateChange(null));
      expect(location()).toHaveTextContent('/dags/example.yaml?step=extract');
      expect(props().ndv?.state).toBeNull();
    });
  });

  describe('step panel', () => {
    const AT = { clientX: 700, clientY: 120 };
    function clickStep(name: string) {
      act(() => {
        props().onSelectedStepNameChange?.(name);
        props().onStepClick?.(name, AT);
      });
    }
    const shownPanel = () => screen.queryByTestId('step-panel');

    it('opens on a click on a step and follows the selection, with no dock', () => {
      renderCanvas();
      clickStep('extract');
      expect(shownPanel()).toHaveAttribute('data-step', 'extract');
      expect(screen.getByTestId('ww-canvas')).toContainElement(shownPanel());
      // The panel knows where the click was, to let a double click through.
      expect(panel.props?.openedAt).toEqual(AT);

      // Another step, by a click or a selection from elsewhere.
      act(() => props().onSelectedStepNameChange?.('load'));
      expect(shownPanel()).toHaveAttribute('data-step', 'load');
      expect(dock.openStep).not.toHaveBeenCalled();
      expect(dock.toggle).not.toHaveBeenCalled();
    });

    it('does not open on a selection without a click', () => {
      renderCanvas();
      act(() => props().onSelectedStepNameChange?.('extract'));
      expect(location()).toHaveTextContent('step=extract');
      expect(shownPanel()).toBeNull();
    });

    // Closing leaves the step selected; Esc then clears it, as before.
    it('closes on its button and on Esc, and keeps the step', () => {
      renderCanvas();
      clickStep('extract');
      expect(props().chrome?.escapeToHost).toBe(true);
      fireEvent.click(screen.getByRole('button', { name: 'close panel' }));
      expect(shownPanel()).toBeNull();
      expect(location()).toHaveTextContent('step=extract');
      expect(props().chrome?.escapeToHost).toBeFalsy();

      clickStep('extract');
      fireEvent.keyDown(document.body, { key: 'Escape' });
      expect(shownPanel()).toBeNull();
      expect(location()).toHaveTextContent('step=extract');
      fireEvent.keyDown(document.body, { key: 'Escape' });
      expect(location()).not.toHaveTextContent('step=');
    });

    it('leaves Esc to a text field', () => {
      renderCanvas();
      clickStep('extract');
      const input = document.createElement('input');
      screen.getByTestId('ww-canvas').appendChild(input);
      fireEvent.keyDown(input, { key: 'Escape' });
      input.remove();
      expect(shownPanel()).toHaveAttribute('data-step', 'extract');
    });

    it('closes for good when the step clears or the node view opens', () => {
      renderCanvas();
      clickStep('extract');
      act(() => props().onSelectedStepNameChange?.(null));
      expect(shownPanel()).toBeNull();
      act(() => props().onSelectedStepNameChange?.('extract'));
      expect(shownPanel()).toBeNull();

      clickStep('extract');
      act(() => props().ndv?.onStateChange('expanded'));
      expect(shownPanel()).toBeNull();
      act(() => props().ndv?.onStateChange(null));
      expect(location()).toHaveTextContent('step=extract');
      expect(shownPanel()).toBeNull();
    });

    it('opens from a link to a step, not from a link to the Run panel or the node view', () => {
      const first = renderCanvas('/dags/example.yaml?step=extract');
      expect(shownPanel()).toHaveAttribute('data-step', 'extract');
      first.unmount();

      const second = renderCanvas('/dags/example.yaml?step=extract&dock=log');
      expect(shownPanel()).toBeNull();
      second.unmount();

      renderCanvas('/dags/example.yaml?step=extract&ndv=expanded');
      expect(shownPanel()).toBeNull();
    });

    it('stays shut for a link to a step that another view covers', () => {
      renderCanvas('/dags/example.yaml?run=run-7&step=extract&def=run');
      expect(shownPanel()).toBeNull();
      fireEvent.click(screen.getByText('snapshot back'));
      expect(location()).toHaveTextContent('step=extract');
      expect(shownPanel()).toBeNull();
    });

    // The panel takes the path of the node menu: the gates and the dialogs
    // of the step actions.
    it('sends its commands through the step actions and opens the node view', () => {
      renderCanvas();
      clickStep('a');
      const given = panel.props!;
      expect(given.gates.canRetry('a')).toBe(true);
      expect(given.gates.canSetStatus('a')).toBe(false);

      act(() => given.onStepCommand({ kind: 'retryStep', step: 'a' }));
      expect(actions.retryStep).toHaveBeenCalledWith('a');
      act(() => given.onStepCommand({ kind: 'setStatus', step: 'a' }));
      expect(actions.setStatus).not.toHaveBeenCalled();

      act(() => given.onOpenStep());
      expect(location()).toHaveTextContent('step=a&ndv=expanded');
      expect(shownPanel()).toBeNull();
    });
  });

  describe('step commands', () => {
    it('maps runStep to the step run actions', () => {
      renderCanvas();
      props().onStepCommand?.({ kind: 'runStep', step: 'a' });
      expect(actions.runStep).toHaveBeenCalledWith('a');
      expect(actions.runUpstream).not.toHaveBeenCalled();
    });

    it('starts the runs of the step view without leaving the editor', () => {
      renderCanvas();
      expect(useStepRunActionsOptions).toHaveBeenCalledWith({ stay: true });
    });

    it('gives the followed run to the editor', () => {
      const followed = {
        kind: 'upstream',
        step: 'b',
        runId: 'r9',
        steps: ['a'],
        startedAt: 1,
      } as const;
      actions.followed = [followed];
      renderCanvas('/dags/example.yaml?step=b&ndv=expanded');
      expect(props().followedRuns).toEqual([followed]);
    });

    // E test 9: the INPUT column and the step run dialog name one run.
    describe('input run', () => {
      const source = {
        dagRunId: 'run-5',
        status: Status.Success,
        startedAt: '2026-10-25T10:00:00Z',
      };

      it('is the reuse source of the open step', () => {
        reuse.state = {
          ...(reuse.state as object),
          source,
          stale: ['slow'],
          missing: ['gone'],
        };
        renderCanvas('/dags/example.yaml?step=use');
        expect(reuse.args).toHaveBeenCalledWith(
          expect.objectContaining({ step: 'use' })
        );
        const input = props().inputRun;
        expect(input?.runId).toBe('run-5');
        expect(input?.status).toBe(Status.Success);
        expect(input?.startedAt).toBe(source.startedAt);
        expect([...(input?.stale ?? [])]).toEqual(['slow']);
        expect([...(input?.missing ?? [])]).toEqual(['gone']);
      });

      it('tells which active run the step waits for', () => {
        reuse.state = { ...(reuse.state as object), waitingFor: 'run-8' };
        renderCanvas('/dags/example.yaml?step=use');
        expect(props().inputRun?.runId).toBeNull();
        expect(props().inputRun?.waitingFor).toBe('run-8');
      });

      it('gives the followed upstream run of the step to the source', () => {
        actions.followed = [
          {
            kind: 'upstream',
            step: 'use',
            runId: 'r9',
            steps: ['a'],
            startedAt: 1,
          },
        ];
        renderCanvas('/dags/example.yaml?step=use');
        expect(reuse.args).toHaveBeenCalledWith(
          expect.objectContaining({ followedUpstreamRunId: 'r9' })
        );
      });

      // A reload on the run of a step run: the labels of the run say which
      // previous steps it reused, so the column shows that run and the source
      // of each input, as it did while the run was followed.
      it('is the selected run when it reused the previous steps of the step', () => {
        const dag = dagData.dag as unknown as { steps?: unknown[] };
        dag.steps = [{ name: 'slow' }, { name: 'use', depends: ['slow'] }];
        try {
          ws.run = makeRun({
            details: {
              ...runDetails(),
              dagRunId: 'run-7',
              status: Status.Success,
              startedAt: '2026-10-26T10:00:00Z',
              labels: ['dagu.input=cache/5/slow'],
              nodes: [],
            } as unknown as ApiDAGRunDetails,
          });
          reuse.state = { ...(reuse.state as object), source, stale: ['slow'] };
          renderCanvas('/dags/example.yaml?step=use');
          const input = props().inputRun;
          expect(input?.runId).toBe('run-7');
          expect(input?.startedAt).toBe('2026-10-26T10:00:00Z');
          expect([...(input?.provenance?.keys() ?? [])]).toEqual(['slow']);
          // The reuse source and the cache are about the next run.
          expect(input?.stale.size).toBe(0);
          expect(input?.cache).toBeUndefined();
        } finally {
          delete dag.steps;
        }
      });

      it('keeps the reuse source when the selected run reused nothing', () => {
        const dag = dagData.dag as unknown as { steps?: unknown[] };
        dag.steps = [{ name: 'slow' }, { name: 'use', depends: ['slow'] }];
        try {
          reuse.state = { ...(reuse.state as object), source };
          renderCanvas('/dags/example.yaml?step=use');
          expect(props().inputRun?.runId).toBe('run-5');
          expect(props().inputRun?.provenance?.size).toBe(0);
        } finally {
          delete dag.steps;
        }
      });

      it('is absent when no step is open', () => {
        renderCanvas();
        expect(props().inputRun).toBeUndefined();
      });

      it('selects the run with a new history entry', () => {
        reuse.state = { ...(reuse.state as object), source };
        renderCanvas('/dags/example.yaml?step=use');
        act(() => props().inputRun?.onSelectRun?.('run-5'));
        expect(location()).toHaveTextContent('run=run-5');
        expect(location()).toHaveAttribute('data-navigation', 'PUSH');
      });
    });

    // The followed run lives while the node view is open.
    it('forgets the followed run when the node view is closed', () => {
      renderCanvas('/dags/example.yaml?step=b&ndv=expanded');
      expect(actions.clearFollowed).not.toHaveBeenCalled();
      act(() => props().ndv?.onStateChange(null));
      expect(actions.clearFollowed).toHaveBeenCalled();
    });

    it('maps runUpstream to the upstream run action', () => {
      renderCanvas();
      props().onStepCommand?.({ kind: 'runUpstream', step: 'a' });
      expect(actions.runUpstream).toHaveBeenCalledWith('a');
      expect(actions.runStep).not.toHaveBeenCalled();
    });

    // The run items act on a step that the selected run has (the gates of
    // the step actions say which).
    it('maps the run items of a step of the run to the step actions', () => {
      renderCanvas();
      props().onStepCommand?.({ kind: 'openSubRun', step: 'c' });
      props().onStepCommand?.({ kind: 'setStatus', step: 'b' });
      props().onStepCommand?.({ kind: 'retryStep', step: 'a' });
      expect(actions.openSubRun).toHaveBeenCalledWith('c', {
        newTab: undefined,
      });
      expect(actions.setStatus).toHaveBeenCalledWith('b');
      expect(actions.retryStep).toHaveBeenCalledWith('a');
    });

    // A step of the live file that the run does not have.
    it.each([
      { kind: 'openSubRun', step: 'live' },
      { kind: 'setStatus', step: 'live' },
      { kind: 'retryStep', step: 'live' },
      // The gate of another step does not open this one.
      { kind: 'retryStep', step: 'b' },
    ] as const)('ignores %o', (cmd) => {
      renderCanvas();
      props().onStepCommand?.(cmd);
      expect(actions.openSubRun).not.toHaveBeenCalled();
      expect(actions.setStatus).not.toHaveBeenCalled();
      expect(actions.retryStep).not.toHaveBeenCalled();
    });

    it('opens the log of a step in the dock', () => {
      renderCanvas();
      props().onStepCommand?.({
        kind: 'viewLogs',
        step: 'a',
        stream: 'stderr',
      });
      expect(actions.openLogs).toHaveBeenCalledWith({
        step: 'a',
        stream: 'stderr',
      });
    });

    it('gates retry, set status and sub-runs on the steps of the run', () => {
      renderCanvas();
      const gates = props().stepCommandGates!;
      expect(gates.canRunStep).toBe(true);
      expect(gates.canRetry('a')).toBe(true);
      expect(gates.canRetry('live')).toBe(false);
      expect(gates.canSetStatus('b')).toBe(true);
      expect(gates.canSetStatus('live')).toBe(false);
      expect(gates.hasSubRuns('c')).toBe(true);
      expect(gates.hasSubRuns('live')).toBe(false);
    });

    // The gates follow the actions of the latest event, with no new object.
    it('keeps the gates object while the run changes', () => {
      renderCanvas();
      const before = props().stepCommandGates;
      ws.actions = {
        ...actions,
        can: vi.fn((step: string) => ({
          retry: step === 'z',
          setStatus: false,
          runStep: true,
          subRuns: false,
        })),
      };
      act(() => props().onSelectedStepNameChange?.('extract'));
      expect(props().stepCommandGates).toBe(before);
      expect(props().stepCommandGates?.canRetry('z')).toBe(true);
    });

    it('gives canRunStep false without the execute permission', () => {
      ws.permissions = makePermissions(false);
      renderCanvas();
      expect(props().stepCommandGates?.canRunStep).toBe(false);
    });
  });

  describe('editor binding', () => {
    it('gives the handle and the state to useWorkspaceEditor', () => {
      const handle = {
        save: vi.fn(),
        getBuffer: () => '',
      } as unknown as WorkspaceEditorHandle;
      host.handle = handle;
      renderCanvas();
      expect(screen.getByTestId('editor')).toHaveAttribute(
        'data-handle',
        'bound'
      );

      act(() =>
        props().onEditorStateChange?.({
          dirty: true,
        } as WorkspaceEditorState)
      );
      expect(screen.getByTestId('editor')).toHaveAttribute(
        'data-dirty',
        'true'
      );
    });

    it('has no handle while DAGSpec gives none', () => {
      renderCanvas();
      expect(screen.getByTestId('editor')).toHaveAttribute(
        'data-handle',
        'none'
      );
    });
  });

  describe('legacy spec link', () => {
    // The old Spec tab was the YAML: /spec redirects here with ?yaml=1.
    // The editor writes ?ndv=docked as it opens the YAML; the URL keeps it
    // when ?yaml leaves.
    it('opens the YAML drawer once and swaps ?yaml for ?ndv=docked', () => {
      const toggleYaml = vi.fn((open?: boolean) => {
        if (open) host.props?.ndv?.onStateChange('docked');
      });
      host.handle = { toggleYaml } as unknown as WorkspaceEditorHandle;
      renderCanvas('/dags/example.yaml?yaml=1&remoteNode=local');
      expect(toggleYaml).toHaveBeenCalledWith(true);
      expect(toggleYaml).toHaveBeenCalledTimes(1);
      expect(location()).toHaveTextContent(
        '/dags/example.yaml?ndv=docked&remoteNode=local'
      );
      expect(props().ndv?.state).toBe('docked');
    });

    it('sets ?ndv=docked when the editor does not write it', () => {
      host.handle = {
        toggleYaml: vi.fn(),
      } as unknown as WorkspaceEditorHandle;
      renderCanvas('/dags/example.yaml?yaml=1&remoteNode=local');
      expect(location()).toHaveTextContent(
        '/dags/example.yaml?ndv=docked&remoteNode=local'
      );
    });

    it('waits for the editor to load', () => {
      renderCanvas('/dags/example.yaml?yaml=1');
      expect(location()).toHaveTextContent('/dags/example.yaml?yaml=1');
    });
  });

  describe('panel link', () => {
    // The old /wiki, /incidents, /notifications and /webhook routes redirect
    // here: the Workflow panel on a tab, or a launcher panel.
    it('opens the Workflow panel once and keeps the tab', () => {
      const openWorkflowPanel = vi.fn();
      host.handle = { openWorkflowPanel } as unknown as WorkspaceEditorHandle;
      renderCanvas('/dags/example.yaml?panel=workflow&wtab=wiki');
      expect(openWorkflowPanel).toHaveBeenCalledWith(true);
      expect(openWorkflowPanel).toHaveBeenCalledTimes(1);
      expect(location()).toHaveTextContent('/dags/example.yaml?wtab=wiki');
      expect(location()).toHaveAttribute('data-navigation', 'REPLACE');
    });

    it('opens a launcher panel once and drops ?panel', () => {
      const openLauncher = vi.fn();
      host.handle = { openLauncher } as unknown as WorkspaceEditorHandle;
      renderCanvas('/dags/example.yaml?panel=webhook&remoteNode=local');
      expect(openLauncher).toHaveBeenCalledWith('webhook');
      expect(location()).toHaveTextContent(
        '/dags/example.yaml?remoteNode=local'
      );
    });

    it('waits for the editor to load', () => {
      renderCanvas('/dags/example.yaml?panel=cron');
      expect(location()).toHaveTextContent('/dags/example.yaml?panel=cron');
    });

    it('gives the editor the launcher host and the tabbed panel', () => {
      renderCanvas('/dags/example.yaml');
      expect(typeof props().launcherHost?.guard).toBe('function');
      expect(typeof props().renderWorkflowPanel).toBe('function');
    });
  });

  // parity:dup-1
  describe('run started from the editor', () => {
    // The new run becomes the selected run. The editor stays the editor, and
    // the node view and the step stay as they are.
    it('selects the new run and writes no mode', () => {
      renderCanvas('/dags/example.yaml?step=extract&ndv=docked');
      act(() => props().onRunStarted?.('run-8'));
      expect(location()).toHaveTextContent(
        '/dags/example.yaml?run=run-8&step=extract&ndv=docked'
      );
      expect(location()).toHaveAttribute('data-navigation', 'PUSH');
    });
  });

  describe('dock', () => {
    it('yields to the expanded node view and comes back with it', () => {
      const yieldToView = vi.fn();
      ws.dock = { ...dock, yieldToView };
      renderCanvas('/dags/example.yaml?step=extract&ndv=expanded');
      expect(yieldToView).toHaveBeenCalledWith(true, { restore: true });
      act(() => props().ndv?.onStateChange(null));
      expect(yieldToView).toHaveBeenLastCalledWith(false);
    });

    it('does not yield for the docked YAML panel', () => {
      const yieldToView = vi.fn();
      ws.dock = { ...dock, yieldToView };
      renderCanvas('/dags/example.yaml?step=extract&ndv=docked');
      expect(yieldToView).not.toHaveBeenCalled();
    });

    // D10: a click on the canvas selects; it never opens the dock.
    it('is not opened by the selection of a step', () => {
      renderCanvas();
      act(() => props().onSelectedStepNameChange?.('extract'));
      expect(dock.toggle).not.toHaveBeenCalled();
      expect(dock.openStep).not.toHaveBeenCalled();
      expect(dock.setTab).not.toHaveBeenCalled();
    });
  });

  // ?def=run puts the snapshot of the run in place of the editor. The editor
  // stays mounted, hidden and inert, with its draft.
  describe('run snapshot (?def=run)', () => {
    it('shows no snapshot and an active editor without ?def', () => {
      renderCanvas('/dags/example.yaml?run=run-7');
      expect(screen.queryByText('snapshot back')).toBeNull();
      const graph = screen.getByTestId('ww-canvas-graph');
      expect(graph).not.toHaveAttribute('hidden');
      expect(graph).not.toHaveAttribute('inert');
    });

    it('shows the snapshot and hides the editor, which stays mounted', () => {
      renderCanvas('/dags/example.yaml?run=run-7&def=run');
      expect(screen.getByText('snapshot back')).toBeInTheDocument();
      const graph = screen.getByTestId('ww-canvas-graph');
      expect(graph).toHaveAttribute('hidden');
      expect(graph).toHaveAttribute('inert');
      expect(screen.getByTestId('dag-spec-stand-in')).toBeInTheDocument();
      // The hidden editor takes no key.
      expect(props().keyboardActive).toBe(false);
    });

    it('gives the editor back with Back, in a push', () => {
      renderCanvas('/dags/example.yaml?run=run-7&def=run');
      fireEvent.click(screen.getByText('snapshot back'));
      expect(location()).toHaveTextContent('/dags/example.yaml?run=run-7');
      expect(location()).toHaveAttribute('data-navigation', 'PUSH');
      expect(screen.queryByText('snapshot back')).toBeNull();
      expect(screen.getByTestId('ww-canvas-graph')).not.toHaveAttribute(
        'hidden'
      );
      expect(props().keyboardActive).toBe(true);
    });

    it('keeps the draft text through the snapshot and Back', async () => {
      host.real = true;
      renderCanvas('/dags/example.yaml?run=run-7&def=run');
      await waitFor(
        () =>
          expect(screen.getByTestId('editor')).toHaveAttribute(
            'data-handle',
            'bound'
          ),
        { timeout: 10000 }
      );
      const draft = `# draft\n${api.spec}`;
      act(() => boundHandle?.replaceBuffer(draft));
      await waitFor(() =>
        expect(screen.getByTestId('editor')).toHaveAttribute(
          'data-dirty',
          'true'
        )
      );

      fireEvent.click(screen.getByText('snapshot back'));
      expect(screen.getByTestId('ww-canvas-graph')).not.toHaveAttribute(
        'hidden'
      );
      expect(boundHandle?.getBuffer()).toBe(draft);
      expect(screen.getByTestId('editor')).toHaveAttribute(
        'data-dirty',
        'true'
      );
    });
  });

  // parity:dup-1 parity:canvas-run-overlay parity:spec-load-state
  // The real editor mounts slowly when many test files run at once.
  describe('with the real DAGSpec and VisualEditor', { timeout: 15000 }, () => {
    // The selected run is failed. The live file has a step that the run did
    // not have: it shows no status. The editor reads no run itself.
    it('overlays the selected run on the live file', async () => {
      host.real = true;
      api.spec =
        'steps:\n  - name: extract\n    run: echo hello\n  - name: load\n    run: echo load\n';
      api.steps = [{ name: 'extract' }, { name: 'load' }];
      renderCanvas();

      const node = await screen.findByTestId(
        've-canvas-node-steps[0]',
        {},
        { timeout: 10000 }
      );
      const status = node.querySelector(
        '[data-testid="ve-canvas-node-status"]'
      );
      expect(status).toHaveAttribute('data-status', String(NodeStatus.Failed));
      const live = screen.getByTestId('ve-canvas-node-steps[1]');
      expect(
        live.querySelector('[data-testid="ve-canvas-node-status"]')
      ).toBeNull();
      expect(
        api.queries.filter(
          (q) => q.path === '/dag-runs/{name}/{dagRunId}' && q.init !== null
        )
      ).toEqual([]);
      expect(screen.getByTestId('editor')).toHaveAttribute(
        'data-handle',
        'bound'
      );
    });

    // A draft edit rebuilds the graph: the statuses stay on their steps, and
    // Save leaves ?run alone.
    it('keeps the overlay and ?run through a draft edit and a save', async () => {
      host.real = true;
      renderCanvas('/dags/example.yaml?run=run-7&step=extract');
      await screen.findByTestId(
        've-canvas-node-steps[0]',
        {},
        { timeout: 10000 }
      );
      const status = () =>
        screen
          .getByTestId('ve-canvas-node-steps[0]')
          .querySelector('[data-testid="ve-canvas-node-status"]');

      act(() => boundHandle?.replaceBuffer(`# draft\n${api.spec}`));
      await waitFor(() =>
        expect(screen.getByTestId('editor')).toHaveAttribute(
          'data-dirty',
          'true'
        )
      );
      expect(status()).toHaveAttribute(
        'data-status',
        String(NodeStatus.Failed)
      );

      act(() => boundHandle?.save());
      const review = await screen.findByRole('dialog');
      fireEvent.click(within(review).getByRole('button', { name: 'Save' }));
      await waitFor(() =>
        expect(screen.getByTestId('editor')).toHaveAttribute(
          'data-dirty',
          'false'
        )
      );
      expect(status()).toHaveAttribute(
        'data-status',
        String(NodeStatus.Failed)
      );
      expect(location()).toHaveTextContent(
        '/dags/example.yaml?run=run-7&step=extract'
      );
    });

    it('binds no handle while the spec loads', async () => {
      host.real = true;
      api.specLoading = true;
      renderCanvas();
      await act(async () => {
        await Promise.resolve();
      });
      expect(screen.getByTestId('editor')).toHaveAttribute(
        'data-handle',
        'none'
      );
      expect(screen.queryByTestId('ve-canvas-node-steps[0]')).toBeNull();
    });
  });
});
