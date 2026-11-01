// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { createPortal } from 'react-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  RightPanelProvider,
  useActiveRightPanel,
  useRightPanel,
} from '@/contexts/RightPanelContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import {
  PANEL_DOCK_GEOMETRY,
  PANEL_SHELL_CLASS,
  PanelPlacementProvider,
  panelShellClass,
} from '@/features/dags/components/step-details/PanelShell';
import { realSchema } from '@/features/visual-editor/inspector/__tests__/realSchema';
import { DAGContext } from '../../../contexts/DAGContext';
import DAGSpec, { type DAGSpecHostProps } from '../DAGSpec';
// DAGSpec loads the client issues on mount without waiting for them. Loading
// them here keeps that load from outliving the test file.
import '@/features/visual-editor/doc/clientIssues';
import type { VisualEditorState } from '../hostTypes';
import {
  clampDockedWidth,
  NodeDetailsFrame,
  ndvLayout,
  stepNeighbours,
  type NdvState,
} from '../NodeDetailsFrame';

const mocks = vi.hoisted(() => {
  const post = vi.fn();
  const put = vi.fn();
  return {
    post,
    put,
    client: { GET: vi.fn(), POST: post, PUT: put },
    useQuery: vi.fn(),
  };
});

vi.mock('@/hooks/api', () => ({
  useClient: () => mocks.client,
  useQuery: mocks.useQuery,
}));
const auth = vi.hoisted(() => ({ canWrite: true }));
vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteForWorkspace: () => auth.canWrite,
}));
vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: vi.fn() }),
}));
vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: vi.fn() }),
}));
vi.mock('../../../../../contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
const schemaState = vi.hoisted(() => ({ schema: null as unknown }));
vi.mock('../../../../../contexts/SchemaContext', () => ({
  useSchema: () => ({ schema: schemaState.schema }),
}));
vi.mock('../../../../../contexts/UnsavedChangesContext', () => ({
  useUnsavedChanges: () => ({ setHasUnsavedChanges: vi.fn() }),
}));
vi.mock('../../../../../hooks/useDAGSSE', () => ({ useDAGSSE: () => null }));
vi.mock('../../../../../hooks/useSSECacheSync', () => ({
  sseFallbackOptions: () => ({}),
  useSSECacheSync: vi.fn(),
}));
vi.mock('@/features/dags/components/step-details', () => ({
  StepDetailsDrawer: () => null,
}));
vi.mock('../ExternalChangeDialog', () => ({ default: () => null }));
vi.mock('../../value-reference-notices', () => ({
  ValueReferenceNoticesButton: () => null,
}));
vi.mock('../WorkflowPanel', () => ({ WorkflowPanel: () => null }));
vi.mock('@/features/visual-editor/DetailsCanvas', () => ({
  default: () => null,
}));

/** One Monaco editor of the test double, with its own undo history. */
interface FakeMonaco {
  history: string[];
}
const monaco = vi.hoisted(() => ({
  /** Mounts the real DAGEditorWithDocs, with its schema docs sidebar. */
  real: false,
  mounts: 0,
  editors: [] as unknown[],
  props: {} as {
    readOnly?: boolean;
    showDocsButton?: boolean;
    markers?: { startLineNumber: number }[];
    schema?: unknown;
  },
}));

// The Monaco editor inside the real DAGEditorWithDocs.
vi.mock('../DAGEditor', () => ({
  default: (props: { value: string }) => (
    <textarea aria-label="DAG spec" readOnly value={props.value} />
  ),
}));

type EditorProps = {
  value: string;
  readOnly?: boolean;
  showDocsButton?: boolean;
  onChange?: (value?: string) => void;
  onEditorMount?: (editor: unknown) => void;
};

// The Monaco test double: it gives an editor object to onEditorMount once,
// at mount, and keeps the undo history of the edits in that object.
vi.mock('../DAGEditorWithDocs', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../DAGEditorWithDocs')>();
  const Real = actual.default as unknown as React.ComponentType<EditorProps>;
  return {
    default: (props: EditorProps) =>
      monaco.real ? <Real {...props} /> : <MonacoDouble {...props} />,
  };
});

function MonacoDouble(props: EditorProps) {
  monaco.props = props;
  const editor = React.useRef<FakeMonaco>({ history: [] });
  const onMount = React.useRef(props.onEditorMount);
  React.useEffect(() => {
    monaco.mounts += 1;
    monaco.editors.push(editor.current);
    onMount.current?.(editor.current);
  }, []);
  return (
    <textarea
      aria-label="DAG spec"
      readOnly={props.readOnly}
      value={props.value}
      onChange={(event) => {
        editor.current.history.push(props.value);
        props.onChange?.(event.target.value);
      }}
      onKeyDown={(event) => {
        if (event.ctrlKey && event.key === 'z') {
          const previous = editor.current.history.pop();
          if (previous !== undefined) props.onChange?.(previous);
        }
      }}
    />
  );
}

type VisualProps = {
  editable: boolean;
  customActions: { name: string }[];
  yamlVisible?: boolean;
  onYamlVisibleChange?(visible: boolean, source?: 'panel'): void;
  yamlToolbarRoot?: HTMLElement | null;
  drawerBodyRoot?: HTMLElement | null;
  formVisible?: boolean;
  onFormVisibleChange?(visible: boolean): void;
  selectedStepName?: string | null;
  renderStepRun?(name: string, variant: 'facts'): React.ReactNode;
  inputColumnRoot?: HTMLElement | null;
  outputColumnRoot?: HTMLElement | null;
  onStateChange?(state: VisualEditorState): void;
};
const visual = vi.hoisted(() => ({ props: null as VisualProps | null }));

// Stands in for the editor: it puts its mode and scope buttons in the bar,
// the form in the drawer body and the step data in the host columns.
vi.mock('@/features/visual-editor', () => ({
  default: function MockVisualEditor(props: VisualProps) {
    visual.props = props;
    const selected = props.selectedStepName ?? null;
    const report = React.useRef(props.onStateChange);
    React.useEffect(() => {
      report.current?.({
        frozen: false,
        canUndo: false,
        canRedo: false,
        selectedStepName: selected,
        drawer: 'none',
        direction: 'TB',
      });
    }, [selected]);
    return (
      <div data-testid="visual-editor-mock">
        <button
          type="button"
          onClick={() => props.onYamlVisibleChange?.(!props.yamlVisible)}
        >
          Toggle YAML
        </button>
        {props.yamlToolbarRoot &&
          props.yamlVisible &&
          createPortal(
            <div>
              <button
                type="button"
                onClick={() => props.onFormVisibleChange?.(false)}
              >
                Mode YAML
              </button>
              <button
                type="button"
                onClick={() => props.onFormVisibleChange?.(true)}
              >
                Mode Form
              </button>
            </div>,
            props.yamlToolbarRoot
          )}
        {props.drawerBodyRoot &&
          props.formVisible &&
          createPortal(
            <input aria-label="Step name" disabled={!props.editable} />,
            props.drawerBodyRoot
          )}
        {props.inputColumnRoot &&
          selected &&
          createPortal(
            <p>Upstream data of {selected}</p>,
            props.inputColumnRoot
          )}
        {props.outputColumnRoot &&
          selected &&
          props.renderStepRun &&
          createPortal(
            props.renderStepRun(selected, 'facts'),
            props.outputColumnRoot
          )}
      </div>
    );
  },
}));

vi.mock('@/features/visual-editor/run/SaveDiffDialog', () => ({
  SaveDiffDialog: () => null,
}));

const appBarValue = {
  title: 'DAGs',
  setTitle: vi.fn(),
  remoteNodes: ['local'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'local',
  selectRemoteNode: vi.fn(),
};

const savedSpec =
  'steps:\n  - name: fetch\n    run: curl\n  - name: transform\n    run: sort\n';

const dag = {
  name: 'example',
  steps: [
    { name: 'fetch', executorConfig: { type: 'http' } },
    { name: 'transform', depends: ['fetch'] },
    { name: 'publish', depends: ['transform'] },
  ],
};

function specData() {
  return {
    data: { dag, spec: savedSpec, errors: [], valueReferenceNotices: [] },
    isLoading: false,
    mutate: vi.fn(),
  };
}

const host = vi.hoisted(() => ({
  onStateChange: vi.fn(),
  setState: null as ((state: NdvState) => void) | null,
}));

type HostProps = Partial<DAGSpecHostProps> & { initial?: NdvState };

/** A host page that owns the ndv state. */
function Host({ initial = 'expanded', ...props }: HostProps) {
  const [state, setState] = React.useState<NdvState>(initial);
  host.setState = setState;
  return (
    <DAGSpec
      fileName="example.yaml"
      {...props}
      ndv={{
        state,
        onStateChange: (next) => {
          host.onStateChange(next);
          setState(next);
        },
      }}
    />
  );
}

function tree(props: HostProps = {}, sibling?: React.ReactNode) {
  return (
    <UserPreferencesProvider>
      <AppBarContext.Provider value={appBarValue}>
        <DAGContext.Provider
          value={{
            refresh: vi.fn(),
            name: 'example',
            fileName: 'example.yaml',
          }}
        >
          <Host {...props} />
          {sibling}
        </DAGContext.Provider>
      </AppBarContext.Provider>
    </UserPreferencesProvider>
  );
}

async function settle() {
  await act(async () => {
    for (let i = 0; i < 8; i++) {
      await Promise.resolve();
    }
  });
}

async function renderFrame(props: HostProps = {}, sibling?: React.ReactNode) {
  const view = render(tree(props, sibling));
  await settle();
  return view;
}

const frame = () => screen.getByTestId('ndv');
const header = () => within(frame().querySelector('header')!);
const setNdv = (state: NdvState) => act(() => host.setState?.(state));

/** A matchMedia that answers min-width queries for a viewport width. */
function setViewport(width: number) {
  vi.stubGlobal(
    'matchMedia',
    vi.fn((query: string) => {
      const min = /min-width:\s*(\d+)px/.exec(query);
      return {
        matches: min ? width >= Number(min[1]) : false,
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      };
    })
  );
}

const run = {
  runId: 'run-1',
  details: {
    dagRunId: 'run-1',
    nodes: [
      {
        step: { name: 'transform' },
        status: NodeStatus.Failed,
        statusLabel: 'failed',
        retryCount: 0,
        doneCount: 1,
      },
    ],
  },
} as unknown as NonNullable<DAGSpecHostProps['runSource']>;

beforeEach(() => {
  localStorage.clear();
  schemaState.schema = null;
  auth.canWrite = true;
  monaco.real = false;
  monaco.mounts = 0;
  monaco.editors = [];
  visual.props = null;
  host.setState = null;
  mocks.useQuery.mockReturnValue(specData());
  mocks.post.mockResolvedValue({
    data: { valid: true, errors: [], dag: undefined },
  });
  mocks.put.mockResolvedValue({ data: { errors: [] } });
});

afterEach(() => {
  vi.useRealTimers();
  vi.clearAllMocks();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('NodeDetailsFrame layout rules', () => {
  it('maps the viewport and the state to a layout', () => {
    expect(ndvLayout('wide', 'expanded')).toBe('columns');
    expect(ndvLayout('wide', 'docked')).toBe('tabs');
    expect(ndvLayout('medium', 'expanded')).toBe('columns');
    expect(ndvLayout('medium', 'docked')).toBe('tabs');
    expect(ndvLayout('narrow', 'expanded')).toBe('sheet');
    expect(ndvLayout('narrow', 'docked')).toBe('sheet');
  });

  it('finds the neighbours of a step along depends', () => {
    expect(stepNeighbours(dag, 'transform')).toEqual({
      previous: 'fetch',
      next: 'publish',
    });
    expect(stepNeighbours(dag, 'fetch')).toEqual({
      previous: null,
      next: 'transform',
    });
    expect(stepNeighbours(dag, 'ghost')).toEqual({
      previous: null,
      next: null,
    });
    expect(stepNeighbours(dag, null)).toEqual({ previous: null, next: null });
  });
});

// parity:yaml-drawer
// parity:monaco-yaml-editor
// parity:yaml-scope-step-file
describe('NodeDetailsFrame editor', () => {
  // The NDV Monaco is the only editor of the spec; the dock Spec tab is the
  // read-only DAGSpecReadOnly. parity:dup-15
  it('keeps one Monaco editor and its undo history through every switch', async () => {
    await renderFrame({ selectedStepName: 'transform' });
    const editor = () => screen.getByLabelText('DAG spec');
    expect(monaco.mounts).toBe(1);
    const first = monaco.editors[0];

    fireEvent.change(editor(), { target: { value: savedSpec + '# edit\n' } });
    expect(visual.props?.yamlVisible).toBe(true);

    // Close, then open again.
    fireEvent.click(header().getByRole('button', { name: 'Close' }));
    expect(frame()).not.toBeVisible();
    // The whole file in the YAML panel, then the node view of a step.
    act(() => visual.props?.onYamlVisibleChange?.(true, 'panel'));
    expect(frame()).toBeVisible();
    expect(frame()).toHaveAttribute('data-state', 'docked');
    act(() => visual.props?.onYamlVisibleChange?.(true));
    expect(frame()).toHaveAttribute('data-state', 'expanded');
    // Form <-> YAML.
    fireEvent.click(screen.getByRole('button', { name: 'Mode Form' }));
    expect(editor()).not.toBeVisible();
    expect(screen.getByLabelText('Step name')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Mode YAML' }));
    expect(editor()).toBeVisible();
    // The hidden state.
    setNdv(null);
    expect(frame()).toHaveAttribute('hidden');
    setNdv('expanded');

    expect(monaco.mounts).toBe(1);
    expect(monaco.editors).toEqual([first]);
    expect(editor()).toHaveValue(savedSpec + '# edit\n');
    fireEvent.keyDown(editor(), { key: 'z', ctrlKey: true });
    expect(editor()).toHaveValue(savedSpec);
    expect(visual.props).not.toBeNull();
  });

  it('titles the step with the name of the draft before the server checks it', async () => {
    await renderFrame({ selectedStepName: 'fetch' });
    expect(header().getByRole('heading', { name: 'fetch' })).toBeVisible();

    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: {
        value:
          'steps:\n  - name: fetch2\n    run: curl\n  - name: transform\n    run: sort\n  - name: publish\n    run: put\n',
      },
    });

    expect(header().getByRole('heading', { name: 'fetch2' })).toBeVisible();
  });

  it('opens docked from the YAML panel and expanded from a step', async () => {
    await renderFrame({ initial: null });
    expect(frame()).toHaveAttribute('hidden');

    act(() => visual.props?.onYamlVisibleChange?.(true, 'panel'));
    expect(frame()).toHaveAttribute('data-state', 'docked');

    setNdv(null);
    act(() => visual.props?.onYamlVisibleChange?.(true));
    expect(frame()).toHaveAttribute('data-state', 'expanded');
  });

  // parity:monaco-server-markers
  it('shows the server validation markers in the editor', async () => {
    vi.useFakeTimers();
    mocks.post.mockResolvedValue({
      data: {
        valid: false,
        errors: ['[3:1] mapping values are not allowed in this context'],
      },
    });
    await renderFrame({ selectedStepName: 'transform' });
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: 'steps:\n  - name: a\n  b: [broken' },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });

    expect(monaco.props.markers).toHaveLength(1);
    expect(monaco.props.markers?.[0]?.startLineNumber).toBe(3);
  });

  // parity:custom-action-schema
  it.skipIf(realSchema === null)(
    'gives the custom actions of the file to the editor and the form',
    async () => {
      schemaState.schema = realSchema;
      mocks.useQuery.mockReturnValue({
        ...specData(),
        data: {
          ...specData().data,
          spec:
            savedSpec +
            [
              'actions:',
              '  slack.notify:',
              '    description: Send Slack notification',
              '    input_schema:',
              '      type: object',
              '      properties:',
              '        text:',
              '          type: string',
              '    template:',
              '      action: http.request',
              '',
            ].join('\n'),
        },
      });
      await renderFrame({ selectedStepName: 'transform' });

      expect(visual.props?.customActions.map((a) => a.name)).toContain(
        'slack.notify'
      );
      expect(JSON.stringify(monaco.props.schema)).toContain('slack.notify');
    }
  );

  it('hides the Docs button of the editor in the node view', async () => {
    await renderFrame();
    expect(monaco.props.showDocsButton).toBe(false);
  });
});

// parity:drawer-shell
describe('NodeDetailsFrame states', () => {
  it('docks with the stored width, expands centred and hides on null', async () => {
    localStorage.setItem('dagu.stepDetailsDrawer.width', '700');
    await renderFrame({ initial: 'docked' });
    const section = frame();
    const backdrop = screen.getByTestId('ndv-backdrop');

    expect(section).toHaveAttribute('data-state', 'docked');
    expect(section.style.width).toBe('700px');
    expect(section).toHaveClass('max-w-full');
    // A host sets --ndv-top below its own bar; without it the top is 0.
    expect(section).toHaveClass(
      'top-[var(--ndv-top,0px)]',
      'bottom-0',
      'right-0'
    );
    expect(section).not.toHaveClass('inset-y-0');
    expect(section).toHaveAttribute('data-panel-shell', 'right');
    // The same placement as the shell of the other right-side panels.
    for (const token of PANEL_DOCK_GEOMETRY.split(' ')) {
      expect(section).toHaveClass(token);
    }
    expect(backdrop).toHaveAttribute('hidden');

    // A step opens the node view, which fills the zone.
    act(() => visual.props?.onYamlVisibleChange?.(true));
    expect(frame()).toBe(section);
    // Full screen with a small margin, over the sidebar and the bar.
    expect(section).toHaveClass('inset-3', 'rounded-lg', 'z-[60]');
    expect(section).not.toHaveClass('top-[var(--ndv-top,0px)]');
    expect(section.style.width).toBe('');
    expect(backdrop).not.toHaveAttribute('hidden');

    // A click on the dimmed backdrop closes the view.
    fireEvent.click(backdrop);
    expect(host.onStateChange).toHaveBeenLastCalledWith(null);
    setNdv('expanded');

    setNdv(null);
    expect(frame()).toBe(section);
    expect(section).toHaveAttribute('hidden');
    expect(backdrop).toHaveAttribute('hidden');
  });

  it('keeps the classic YAML drawer without the ndv prop', async () => {
    render(
      <UserPreferencesProvider>
        <AppBarContext.Provider value={appBarValue}>
          <DAGContext.Provider
            value={{
              refresh: vi.fn(),
              name: 'example',
              fileName: 'example.yaml',
            }}
          >
            <DAGSpec fileName="example.yaml" />
          </DAGContext.Provider>
        </AppBarContext.Provider>
      </UserPreferencesProvider>
    );
    await settle();
    expect(screen.queryByTestId('ndv')).toBeNull();
    fireEvent.click(screen.getByText('Toggle YAML'));
    const drawer = screen.getByRole('region', { name: 'YAML' });
    expect(drawer).toBeVisible();
    expect(screen.getByRole('button', { name: 'Close' })).toBeInTheDocument();
    // The YAML drawer has the shell of the other right-side panels.
    for (const token of PANEL_SHELL_CLASS.split(' ')) {
      expect(drawer).toHaveClass(token);
    }
    expect(drawer.querySelector('[data-panel-header]')).toHaveClass('h-12');
    expect(monaco.props.showDocsButton).toBe(true);
  });
});

// The workspace puts every right panel inside its graph zone: absolute, never
// fixed to the window, with the same edges for all of them.
describe('NodeDetailsFrame in a graph zone', () => {
  const inZone = (props: HostProps = {}) => (
    <div data-testid="zone" className="relative">
      <PanelPlacementProvider value="zone">
        {tree(props)}
      </PanelPlacementProvider>
    </div>
  );

  it('fills the zone when expanded, with no fixed overlay and no backdrop', async () => {
    render(inZone({ initial: 'expanded' }));
    await settle();
    const section = frame();
    expect(section).toHaveClass('absolute', 'inset-0');
    expect(section).not.toHaveClass('fixed');
    expect(section).not.toHaveClass('inset-3');
    expect(section).toHaveAttribute('data-placement', 'zone');
    expect(screen.getByTestId('zone')).toContainElement(section);
    expect(screen.queryByTestId('ndv-backdrop')).toBeNull();
    // Header, columns and close stay inside the frame.
    expect(header().getByRole('button', { name: 'Close' })).toBeInTheDocument();
    expect(section.querySelector('#ndv-input')).not.toBeNull();
    expect(section.querySelector('#ndv-output')).not.toBeNull();
  });

  it('docks on the right edge of the zone and shares the shell of the panels', async () => {
    render(inZone({ initial: 'docked' }));
    await settle();
    const section = frame();
    // One box from the top of the zone, with the row of toolbars (--ww-toolbar-row,
    // 0 without one) over its top padding.
    expect(section).toHaveClass(
      'absolute',
      'bottom-0',
      'right-0',
      'top-0!',
      'pt-(--ww-toolbar-row,0px)'
    );
    expect(section).not.toHaveClass('fixed');
    expect(section).not.toHaveClass('top-[var(--ndv-top,0px)]');
    for (const token of panelShellClass('zone').split(' ')) {
      if (token === 'flex-col' || token === 'flex') continue;
      expect(section).toHaveClass(token);
    }
  });

  it('closes the expanded view with Escape on the body', async () => {
    render(inZone({ initial: 'expanded' }));
    await settle();
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(host.onStateChange).toHaveBeenLastCalledWith(null);
  });

  it('gives the YAML drawer the shell of the zone', async () => {
    render(
      <PanelPlacementProvider value="zone">
        <UserPreferencesProvider>
          <AppBarContext.Provider value={appBarValue}>
            <DAGContext.Provider
              value={{
                refresh: vi.fn(),
                name: 'example',
                fileName: 'example.yaml',
              }}
            >
              <DAGSpec fileName="example.yaml" />
            </DAGContext.Provider>
          </AppBarContext.Provider>
        </UserPreferencesProvider>
      </PanelPlacementProvider>
    );
    await settle();
    fireEvent.click(screen.getByText('Toggle YAML'));
    const drawer = screen.getByRole('region', { name: 'YAML' });
    for (const token of panelShellClass('zone').split(' ')) {
      expect(drawer).toHaveClass(token);
    }
    expect(drawer).not.toHaveClass('fixed');
  });
});

// parity:drawer-header
describe('NodeDetailsFrame header', () => {
  it('shows the icon, the name and the run status of the step', async () => {
    await renderFrame({ selectedStepName: 'transform', runSource: run });
    expect(header().getByRole('heading', { name: 'transform' })).toBeVisible();
    expect(header().getByText('Failed')).toBeInTheDocument();
    expect(header().getByTestId('ndv-step-icon')).toHaveClass(
      'lucide-terminal'
    );
  });

  it('shows the action icon of an action step and no status without a run', async () => {
    await renderFrame({ selectedStepName: 'fetch' });
    expect(header().getByTestId('ndv-step-icon')).toHaveClass('lucide-globe');
    expect(header().queryByText('Failed')).toBeNull();
  });

  it('shows the repeat badge of a repeating step, read-only, hidden below 520 px', async () => {
    const base = specData();
    mocks.useQuery.mockReturnValue({
      ...base,
      data: {
        ...base.data,
        dag: {
          ...dag,
          steps: dag.steps.map((step) =>
            step.name === 'transform'
              ? { ...step, repeatPolicy: { repeat: 'while', interval: 10 } }
              : step
          ),
        },
      },
    });
    await renderFrame({ selectedStepName: 'transform', runSource: run });
    const badge = header().getByTestId('repeat-badge');
    expect(badge).toHaveTextContent('×1');
    expect(badge.getAttribute('title')).toContain(
      'repeat while exit 0 · every 10s'
    );
    // The container query of the header hides it when the header is narrow.
    expect(header().getByTestId('ndv-repeat')).toHaveClass(
      'hidden',
      '@[520px]:inline-flex'
    );
    expect(frame().querySelector('header')).toHaveClass('@container');
  });

  it('shows no repeat badge for a step that does not repeat', async () => {
    await renderFrame({ selectedStepName: 'transform', runSource: run });
    expect(header().queryByTestId('repeat-badge')).toBeNull();
  });

  it('moves to the previous and next step along depends', async () => {
    const onSelectedStepNameChange = vi.fn();
    await renderFrame({
      selectedStepName: 'transform',
      onSelectedStepNameChange,
    });

    fireEvent.click(header().getByRole('button', { name: 'Previous step' }));
    expect(onSelectedStepNameChange).toHaveBeenLastCalledWith('fetch');
    fireEvent.click(header().getByRole('button', { name: 'Next step' }));
    expect(onSelectedStepNameChange).toHaveBeenLastCalledWith('publish');
  });

  // parity:handlers-editing
  it('shows a handler step and opens its form', async () => {
    mocks.useQuery.mockReturnValue({
      ...specData(),
      data: {
        ...specData().data,
        dag: {
          ...dag,
          handlerOn: {
            failure: { name: 'notify', executorConfig: { type: 'mail' } },
          },
        },
      },
    });
    await renderFrame({ selectedStepName: 'notify' });

    expect(header().getByRole('heading', { name: 'notify' })).toBeVisible();
    expect(header().getByTestId('ndv-step-icon')).toHaveClass('lucide-mail');
    // The handlers follow the steps in the order of the moves.
    expect(
      header().getByRole('button', { name: 'Previous step' })
    ).toBeEnabled();
    expect(header().getByRole('button', { name: 'Next step' })).toBeDisabled();
    // A lifecycle handler is not run alone.
    expect(
      header().queryByRole('button', { name: 'Run step' })
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Mode Form' }));
    expect(screen.getByLabelText('Step name')).toBeVisible();
    expect(screen.getByLabelText('DAG spec')).not.toBeVisible();
  });

  it('disables the moves at the ends of the flow', async () => {
    await renderFrame({ selectedStepName: 'fetch' });
    expect(
      header().getByRole('button', { name: 'Previous step' })
    ).toBeDisabled();
    expect(header().getByRole('button', { name: 'Next step' })).toBeEnabled();
  });

  it('tells why a move is disabled', async () => {
    await renderFrame({ selectedStepName: 'fetch' });
    const previous = header().getByRole('button', { name: 'Previous step' });
    // The wrapper takes the pointer event that the disabled button cannot.
    await userEvent.hover(previous.parentElement as HTMLElement);
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      'This is the first step'
    );
  });

  it('closes with a button named Close', async () => {
    await renderFrame({ selectedStepName: 'fetch' });
    const close = header().getByRole('button', { name: 'Close' });
    expect(close).toHaveClass('size-7');
  });

  // parity:run-step-drawer
  it('runs the step through the host only when the gate allows it', async () => {
    const onStepCommand = vi.fn();
    const gates = {
      canRunStep: true,
      canRetry: () => false,
      canSetStatus: () => false,
      hasSubRuns: () => false,
    };
    const view = await renderFrame({
      selectedStepName: 'transform',
      onStepCommand,
      stepCommandGates: gates,
    });
    fireEvent.click(header().getByRole('button', { name: 'Run step' }));
    expect(onStepCommand).toHaveBeenCalledWith({
      kind: 'runStep',
      step: 'transform',
    });
    view.unmount();

    const closed = await renderFrame({
      selectedStepName: 'transform',
      onStepCommand,
      stepCommandGates: { ...gates, canRunStep: false },
    });
    expect(header().queryByRole('button', { name: 'Run step' })).toBeNull();
    closed.unmount();

    await renderFrame({ selectedStepName: 'transform' });
    expect(header().queryByRole('button', { name: 'Run step' })).toBeNull();
  });

  // parity:schema-docs-shortcut
  it('closes the expanded frame with Escape, and keeps a handled Escape', async () => {
    await renderFrame({ initial: 'expanded', selectedStepName: 'transform' });
    const prevented = new KeyboardEvent('keydown', {
      key: 'Escape',
      bubbles: true,
      cancelable: true,
    });
    prevented.preventDefault();
    act(() => {
      frame().dispatchEvent(prevented);
    });
    expect(host.onStateChange).not.toHaveBeenCalled();

    fireEvent.keyDown(frame(), { key: 'Escape' });
    expect(host.onStateChange).toHaveBeenLastCalledWith(null);
  });

  it('closes the expanded frame with Escape from the page body or an input', async () => {
    await renderFrame({ initial: 'expanded', selectedStepName: 'transform' });
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(host.onStateChange).toHaveBeenLastCalledWith(null);

    setNdv('expanded');
    host.onStateChange.mockClear();
    const input = document.createElement('input');
    frame().appendChild(input);
    input.focus();
    fireEvent.keyDown(input, { key: 'Escape' });
    expect(host.onStateChange).toHaveBeenLastCalledWith(null);
  });

  it('leaves Escape to an open menu, dialog or editor widget', async () => {
    await renderFrame({ initial: 'expanded', selectedStepName: 'transform' });
    const owners = [
      '<div role="menu"></div>',
      '<div role="listbox"></div>',
      '<div role="dialog" data-state="open"></div>',
      '<div class="monaco-editor"><div class="suggest-widget visible"></div></div>',
    ];
    for (const html of owners) {
      const layer = document.createElement('div');
      layer.innerHTML = html;
      document.body.appendChild(layer);
      fireEvent.keyDown(document.body, { key: 'Escape' });
      layer.remove();
    }
    expect(host.onStateChange).not.toHaveBeenCalled();
  });

  // A listbox of a hidden pane, such as the list of runs, is not a layer
  // that the user sees.
  it('ignores a hidden or inert layer when Escape closes the frame', async () => {
    await renderFrame({ initial: 'expanded', selectedStepName: 'transform' });
    const layer = document.createElement('div');
    layer.setAttribute('inert', '');
    layer.innerHTML = '<ul role="listbox"></ul>';
    document.body.appendChild(layer);
    fireEvent.keyDown(document.body, { key: 'Escape' });
    layer.remove();
    expect(host.onStateChange).toHaveBeenLastCalledWith(null);
  });

  it('keeps the frame open on Escape while its pane is inert', async () => {
    await renderFrame({ initial: 'expanded', selectedStepName: 'transform' });
    const pane = document.createElement('div');
    pane.setAttribute('inert', '');
    frame().parentElement?.appendChild(pane);
    pane.appendChild(frame());
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(host.onStateChange).not.toHaveBeenCalled();
  });

  it('does not close the docked frame with Escape on the body', async () => {
    await renderFrame({ initial: 'docked', selectedStepName: 'transform' });
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(host.onStateChange).not.toHaveBeenCalled();
  });

  it('has a Run step button, previous and next arrows and a close button', async () => {
    await renderFrame({
      initial: 'expanded',
      selectedStepName: 'transform',
      onStepCommand: vi.fn(),
      stepCommandGates: {
        canRunStep: true,
        canRetry: () => false,
        canSetStatus: () => false,
        hasSubRuns: () => false,
      },
    });
    for (const name of ['Previous step', 'Next step', 'Run step', 'Close']) {
      expect(header().getByRole('button', { name })).toBeInTheDocument();
    }
  });

  // The header has no Docs button: the shortcut of the editor stays.
  it('has no Docs button in the header', async () => {
    await renderFrame();
    expect(
      header().queryByRole('button', { name: /Docs/ })
    ).not.toBeInTheDocument();
  });

  function pressSchemaDocsShortcut() {
    fireEvent.keyDown(document, { key: 'D', ctrlKey: true, shiftKey: true });
  }

  // parity:schema-docs-sidebar
  it('opens the schema docs sidebar on the field of the cursor', async () => {
    monaco.real = true;
    schemaState.schema = {
      type: 'object',
      properties: {
        name: { type: 'string', description: 'The name of the workflow.' },
        steps: {
          type: 'array',
          description: 'The steps that the workflow runs.',
        },
      },
    };
    await renderFrame({ selectedStepName: 'transform' });
    expect(screen.queryByText('Schema Docs')).not.toBeInTheDocument();

    pressSchemaDocsShortcut();

    expect(screen.getByText('Schema Docs')).toBeInTheDocument();
    // The cursor is on 'steps:' (line 1), so the sidebar shows that field
    // and not the list of every top-level field.
    expect(
      screen.getByText('The steps that the workflow runs.')
    ).toBeInTheDocument();
    expect(
      screen.queryByText('The name of the workflow.')
    ).not.toBeInTheDocument();
    expect(localStorage.getItem('schema-sidebar-open')).toBe('true');

    pressSchemaDocsShortcut();
    expect(screen.queryByText('Schema Docs')).not.toBeInTheDocument();
  });

  // The node view has no button that docks it, and the YAML panel none
  // that expands it: each closes through the host state.
  it('closes the node view and the YAML panel through the host state', async () => {
    await renderFrame();
    expect(header().queryByRole('button', { name: 'Expand' })).toBeNull();
    expect(
      header().queryByRole('button', { name: 'Dock to the side' })
    ).toBeNull();
    fireEvent.click(header().getByRole('button', { name: 'Close' }));
    expect(host.onStateChange).toHaveBeenLastCalledWith(null);
    expect(frame()).toHaveAttribute('hidden');

    act(() => visual.props?.onYamlVisibleChange?.(true, 'panel'));
    expect(frame()).toHaveAttribute('data-state', 'docked');
    expect(header().queryByRole('button', { name: 'Expand' })).toBeNull();
    fireEvent.click(header().getByRole('button', { name: 'Close' }));
    expect(host.onStateChange).toHaveBeenLastCalledWith(null);
  });

  // A link says ?ndv=docked for a step: the state follows what shows.
  it('sets a docked state of a step right', async () => {
    await renderFrame({ initial: 'docked' });
    act(() => visual.props?.onYamlVisibleChange?.(true));
    expect(frame()).toHaveAttribute('data-state', 'expanded');
    setNdv('docked');
    expect(host.onStateChange).toHaveBeenLastCalledWith('expanded');
    expect(frame()).toHaveAttribute('data-state', 'expanded');
  });
});

// parity:upstream-data-panel
// parity:embed-step-drawer-other
describe('NodeDetailsFrame columns', () => {
  const renderStepRun = (name: string) => <p>Run of {name}</p>;
  const input = () => screen.getByRole('region', { name: 'Input' });

  it('shows INPUT, CENTER and OUTPUT side by side from 1280px', async () => {
    setViewport(1440);
    await renderFrame({
      initial: 'expanded',
      selectedStepName: 'transform',
      renderStepRun,
    });

    expect(frame()).toHaveAttribute('data-layout', 'columns');
    expect(screen.getByText('Upstream data of transform')).toBeVisible();
    expect(screen.getByTestId('ndv-center')).toBeVisible();
    expect(screen.getByLabelText('DAG spec')).toBeVisible();
    expect(screen.getByText('Run of transform')).toBeVisible();
    expect(screen.queryByRole('tablist')).toBeNull();
    expect(input()).toBe(visual.props?.inputColumnRoot);
  });

  // The YAML panel is the whole file: it has no INPUT and no OUTPUT.
  it('has no tabs and no columns in the YAML panel', async () => {
    setViewport(1440);
    await renderFrame({
      initial: 'docked',
      selectedStepName: 'transform',
      renderStepRun,
    });
    expect(frame()).toHaveAttribute('data-layout', 'tabs');
    expect(header().getByRole('heading', { name: 'YAML' })).toBeVisible();
    expect(screen.getByRole('tablist', { hidden: true })).not.toBeVisible();
    expect(screen.getByLabelText('Input')).not.toBeVisible();
    expect(screen.getByLabelText('Output')).not.toBeVisible();
    expect(screen.getByLabelText('DAG spec')).toBeVisible();
  });

  it('shows the three columns of the expanded frame from 768px', async () => {
    setViewport(800);
    await renderFrame({
      initial: 'expanded',
      selectedStepName: 'transform',
      renderStepRun,
    });
    expect(frame()).toHaveAttribute('data-layout', 'columns');
    expect(screen.getByText('Upstream data of transform')).toBeVisible();
    expect(screen.getByLabelText('DAG spec')).toBeVisible();
    expect(screen.getByText('Run of transform')).toBeVisible();
    expect(screen.queryByRole('tablist')).toBeNull();
  });

  it('shows CENTER and an INPUT | OUTPUT tablist in a narrow node view', async () => {
    setViewport(1000);
    const width = vi
      .spyOn(HTMLElement.prototype, 'getBoundingClientRect')
      .mockImplementation(() => ({ width: 700, height: 800 }) as DOMRect);
    await renderFrame({
      selectedStepName: 'transform',
      renderStepRun,
    });
    expect(frame()).toHaveAttribute('data-layout', 'tabs');
    const tablist = screen.getByRole('tablist', { name: 'Step data' });
    expect(
      within(tablist)
        .getAllByRole('tab')
        .map((tab) => tab.textContent)
    ).toEqual(['Input', 'Output']);
    expect(screen.getByLabelText('DAG spec')).toBeVisible();
    // The host gives run facts: the tabs open on OUTPUT.
    expect(
      within(tablist).getByRole('tab', { name: 'Output' })
    ).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByText('Run of transform')).toBeVisible();
    expect(screen.getByText('Upstream data of transform')).not.toBeVisible();

    fireEvent.click(within(tablist).getByRole('tab', { name: 'Input' }));
    expect(screen.getByText('Upstream data of transform')).toBeVisible();
    expect(screen.getByText('Run of transform')).not.toBeVisible();
    expect(screen.getByLabelText('DAG spec')).toBeVisible();
    width.mockRestore();
  });

  it('opens the tabs on INPUT when the host gives no run facts', async () => {
    setViewport(1000);
    const width = vi
      .spyOn(HTMLElement.prototype, 'getBoundingClientRect')
      .mockImplementation(() => ({ width: 700, height: 800 }) as DOMRect);
    await renderFrame({ selectedStepName: 'transform' });
    expect(frame()).toHaveAttribute('data-layout', 'tabs');
    const tablist = screen.getByRole('tablist', { name: 'Step data' });
    expect(within(tablist).getByRole('tab', { name: 'Input' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(screen.getByText('Upstream data of transform')).toBeVisible();
    width.mockRestore();
  });

  it('shows a full-screen sheet of tabs below 768px', async () => {
    setViewport(600);
    await renderFrame({ selectedStepName: 'transform', renderStepRun });
    expect(frame()).toHaveAttribute('data-layout', 'sheet');
    expect(frame()).toHaveClass('inset-0');
    expect(frame().style.width).toBe('');
    expect(header().queryByRole('button', { name: 'Expand' })).toBeNull();
    const tabs = within(screen.getByRole('tablist', { name: 'Step data' }));
    expect(tabs.getAllByRole('tab').map((tab) => tab.textContent)).toEqual([
      'Input',
      'Parameters',
      'YAML',
      'Output',
    ]);
    expect(tabs.getByRole('tab', { name: 'YAML' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(screen.getByLabelText('DAG spec')).toBeVisible();

    fireEvent.click(tabs.getByRole('tab', { name: 'Input' }));
    expect(screen.getByText('Upstream data of transform')).toBeVisible();
    expect(screen.getByTestId('ndv-center')).not.toBeVisible();

    fireEvent.click(tabs.getByRole('tab', { name: 'Parameters' }));
    expect(screen.getByLabelText('Step name')).toBeVisible();
    expect(screen.getByLabelText('DAG spec')).not.toBeVisible();

    fireEvent.click(tabs.getByRole('tab', { name: 'Output' }));
    expect(screen.getByText('Run of transform')).toBeVisible();

    fireEvent.click(tabs.getByRole('tab', { name: 'YAML' }));
    expect(screen.getByLabelText('DAG spec')).toBeVisible();
    expect(monaco.mounts).toBe(1);
  });

  it('moves between the tabs with the arrow keys', async () => {
    setViewport(600);
    await renderFrame({ selectedStepName: 'transform', renderStepRun });
    const tabs = within(screen.getByRole('tablist', { name: 'Step data' }));
    const yaml = tabs.getByRole('tab', { name: 'YAML' });
    expect(
      tabs.getAllByRole('tab').map((tab) => tab.getAttribute('tabindex'))
    ).toEqual(['-1', '-1', '0', '-1']);

    fireEvent.keyDown(yaml, { key: 'ArrowRight' });
    const output = tabs.getByRole('tab', { name: 'Output' });
    expect(output).toHaveFocus();
    expect(output).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByText('Run of transform')).toBeVisible();

    fireEvent.keyDown(output, { key: 'ArrowRight' });
    expect(tabs.getByRole('tab', { name: 'Input' })).toHaveFocus();
    expect(screen.getByText('Upstream data of transform')).toBeVisible();

    fireEvent.keyDown(tabs.getByRole('tab', { name: 'Input' }), {
      key: 'End',
    });
    expect(tabs.getByRole('tab', { name: 'Output' })).toHaveFocus();
    expect(monaco.mounts).toBe(1);
  });

  // The tab row has the height of the dock and of the Workflow panel: 32 px.
  it('has a 32 px tab row', async () => {
    setViewport(600);
    await renderFrame({ selectedStepName: 'transform', renderStepRun });
    expect(screen.getByRole('tablist', { name: 'Step data' })).toHaveClass(
      'h-8'
    );
  });

  it('gives the host columns to the editor before its own', async () => {
    const own = document.createElement('div');
    await renderFrame({ inputColumnRoot: own });
    expect(visual.props?.inputColumnRoot).toBe(own);
    expect(visual.props?.outputColumnRoot).toBe(
      document.getElementById('ndv-output')
    );
  });
});

function ActivePanel() {
  return <output aria-label="Active panel">{useActiveRightPanel()}</output>;
}

function AddStepClaimer() {
  const { claim } = useRightPanel('add-step', () => {});
  return (
    <button type="button" onClick={claim}>
      Claim add-step
    </button>
  );
}

// parity:drawer-shell
// parity:dup-4
describe('NodeDetailsFrame right-side panel', () => {
  it('claims ndv while open and closes when the list of steps opens', async () => {
    render(
      <RightPanelProvider>
        {tree(
          {},
          <>
            <ActivePanel />
            <AddStepClaimer />
          </>
        )}
      </RightPanelProvider>
    );
    await settle();
    const active = () => screen.getByRole('status', { name: 'Active panel' });
    expect(active()).toHaveTextContent('ndv');

    fireEvent.click(screen.getByText('Claim add-step'));
    expect(host.onStateChange).toHaveBeenLastCalledWith(null);
    expect(frame()).toHaveAttribute('hidden');
    expect(active()).toHaveTextContent('add-step');

    setNdv('docked');
    expect(active()).toHaveTextContent('ndv');
  });
});

describe('NodeDetailsFrame for a reader', () => {
  it('locks the editor and the form and keeps OUTPUT', async () => {
    auth.canWrite = false;
    await renderFrame({
      selectedStepName: 'transform',
      renderStepRun: (name) => <p>Run of {name}</p>,
    });

    expect(monaco.props.readOnly).toBe(true);
    expect(screen.getByLabelText('DAG spec')).toHaveAttribute('readonly');
    expect(visual.props?.editable).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Mode Form' }));
    expect(screen.getByLabelText('Step name')).toBeDisabled();
    expect(screen.getByText('Run of transform')).toBeInTheDocument();
    expect(
      within(document.getElementById('ndv-output')!).getByText(
        'Run of transform'
      )
    ).toBeInTheDocument();
  });
});

/** The frame alone, opened from a button that stands for a canvas node. */
function FocusHarness() {
  const [state, setState] = React.useState<NdvState>(null);
  host.setState = setState;
  return (
    <>
      <button type="button" onClick={() => setState('docked')}>
        Node transform
      </button>
      <NodeDetailsFrame
        state={state}
        scope={state === 'docked' ? 'file' : 'step'}
        onClose={() => setState(null)}
        stepName="transform"
        dag={dag as React.ComponentProps<typeof NodeDetailsFrame>['dag']}
        onSelectStep={vi.fn()}
        formMode
        canForm
        onFormModeChange={vi.fn()}
        toolbarRef={() => {}}
        inputRef={() => {}}
        outputRef={() => {}}
      >
        <input aria-label="Step field" />
      </NodeDetailsFrame>
      <button type="button">After the frame</button>
    </>
  );
}

/** Opens the frame from its button, as a click on the canvas node does. */
function openFromNode() {
  const node = screen.getByRole('button', { name: 'Node transform' });
  act(() => node.focus());
  fireEvent.click(node);
  return node;
}

// parity:run-step-drawer-resize
describe('NodeDetailsFrame focus and width', () => {
  it('stays beside the page when docked', () => {
    render(<FocusHarness />);
    const node = openFromNode();

    const section = frame();
    expect(section).toHaveAttribute('data-state', 'docked');
    expect(section).not.toHaveAttribute('role');
    expect(section).not.toHaveAttribute('aria-modal');
    expect(node).toHaveFocus();

    // Tab leaves the frame.
    const close = header().getByRole('button', { name: 'Close' });
    act(() => close.focus());
    expect(fireEvent.keyDown(close, { key: 'Tab' })).toBe(true);
  });

  it('traps the focus while expanded', () => {
    render(<FocusHarness />);
    openFromNode();
    setNdv('expanded');

    const section = screen.getByRole('dialog', { name: 'Step details' });
    expect(section).toHaveAttribute('aria-modal', 'true');
    expect(section).toHaveFocus();

    const stops = Array.from(
      section.querySelectorAll<HTMLElement>('button:not([disabled]), input')
    ).filter((element) => !element.closest('[hidden]'));
    const first = stops[0]!;
    const last = stops[stops.length - 1]!;
    expect(last).toHaveAccessibleName('Step field');

    act(() => last.focus());
    expect(fireEvent.keyDown(last, { key: 'Tab' })).toBe(false);
    expect(first).toHaveFocus();
    expect(fireEvent.keyDown(first, { key: 'Tab', shiftKey: true })).toBe(
      false
    );
    expect(last).toHaveFocus();
  });

  it('gives the focus back to the element that opened it', () => {
    render(<FocusHarness />);
    const node = openFromNode();
    act(() => screen.getByLabelText('Step field').focus());

    fireEvent.click(header().getByRole('button', { name: 'Close' }));
    expect(frame()).toHaveAttribute('hidden');
    expect(node).toHaveFocus();

    // From the expanded frame too.
    openFromNode();
    setNdv('expanded');
    expect(frame()).toHaveFocus();
    setNdv(null);
    expect(node).toHaveFocus();
  });

  it('does not take the focus back from an element outside the frame', () => {
    render(<FocusHarness />);
    openFromNode();
    const after = screen.getByRole('button', { name: 'After the frame' });
    act(() => after.focus());
    setNdv(null);
    expect(after).toHaveFocus();
  });

  it('resizes the docked frame from its edge and keeps the width', () => {
    render(<FocusHarness />);
    openFromNode();
    const edge = screen.getByRole('separator', {
      name: 'Resize step details',
    });
    expect(edge).toHaveAttribute('aria-valuenow', '560');

    fireEvent.keyDown(edge, { key: 'ArrowLeft' });
    expect(frame().style.width).toBe('600px');
    expect(localStorage.getItem('dagu.stepDetailsDrawer.width')).toBe('600');
    fireEvent.keyDown(edge, { key: 'Home' });
    expect(frame().style.width).toBe('420px');

    fireEvent.mouseDown(edge, { clientX: 500 });
    fireEvent.mouseMove(document, { clientX: 400 });
    expect(frame().style.width).toBe('520px');
    expect(localStorage.getItem('dagu.stepDetailsDrawer.width')).toBe('420');
    fireEvent.mouseUp(document);
    expect(localStorage.getItem('dagu.stepDetailsDrawer.width')).toBe('520');

    // The expanded frame has no edge.
    setNdv('expanded');
    expect(screen.queryByRole('separator')).toBeNull();
  });

  it('keeps the width inside its bounds and the viewport', () => {
    expect(clampDockedWidth(100, 1600)).toBe(420);
    expect(clampDockedWidth(2000, 1600)).toBe(960);
    expect(clampDockedWidth(900, 800)).toBe(800);
    expect(clampDockedWidth(900, 300)).toBe(420);
  });
});
