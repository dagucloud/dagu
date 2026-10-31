// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  RightPanelProvider,
  useRightPanel,
} from '@/contexts/RightPanelContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { DAGContext } from '../../../contexts/DAGContext';
import DAGSpec, {
  type DAGSpecHostProps,
  type WorkspaceEditorHandle,
  type WorkspaceEditorState,
} from '../DAGSpec';
import type { VisualEditorHandle, VisualEditorState } from '../hostTypes';

const mocks = vi.hoisted(() => {
  const get = vi.fn();
  const post = vi.fn();
  const put = vi.fn();
  return {
    get,
    post,
    put,
    client: { GET: get, POST: post, PUT: put },
    showError: vi.fn(),
    showToast: vi.fn(),
    useQuery: vi.fn(),
    copyText: vi.fn(),
    refresh: vi.fn(),
  };
});

vi.mock('@/hooks/api', () => ({
  useClient: () => mocks.client,
  useQuery: mocks.useQuery,
}));
vi.mock('@/lib/clipboard', () => ({ copyText: mocks.copyText }));
const auth = vi.hoisted(() => ({ canWrite: true }));
vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteForWorkspace: () => auth.canWrite,
}));
vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: mocks.showError }),
}));
vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: mocks.showToast }),
}));
vi.mock('../../../../../contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('../../../../../contexts/SchemaContext', () => ({
  useSchema: () => ({ schema: null }),
}));
vi.mock('../../../../../contexts/UnsavedChangesContext', () => ({
  useUnsavedChanges: () => ({ setHasUnsavedChanges: vi.fn() }),
}));
vi.mock('../../../../../hooks/useDAGSSE', () => ({ useDAGSSE: () => null }));
vi.mock('../../../../../hooks/useSSECacheSync', () => ({
  sseFallbackOptions: () => ({}),
  useSSECacheSync: vi.fn(),
}));

const drawer = vi.hoisted(() => ({ panelId: undefined as string | undefined }));
vi.mock('@/features/dags/components/step-details', () => ({
  StepDetailsDrawer: (props: { panelId?: string }) => {
    drawer.panelId = props.panelId;
    return null;
  },
}));

// A native select, so that a test can pick a document without a popover.
vi.mock('@/components/ui/select', () => ({
  Select: ({
    value,
    onValueChange,
    children,
  }: {
    value: string;
    onValueChange(value: string): void;
    children: React.ReactNode;
  }) => (
    <select
      aria-label="Document"
      value={value}
      onChange={(event) => onValueChange(event.target.value)}
    >
      {children}
    </select>
  ),
  SelectTrigger: () => null,
  SelectValue: () => null,
  SelectContent: ({ children }: { children: React.ReactNode }) => (
    <>{children}</>
  ),
  SelectItem: ({
    value,
    children,
  }: {
    value: string;
    children: React.ReactNode;
  }) => <option value={value}>{children}</option>,
}));

vi.mock('../ExternalChangeDialog', () => ({ default: () => null }));
vi.mock('../../value-reference-notices', () => ({
  ValueReferenceNoticesButton: () => null,
}));
// The real table of steps, without the rest of the dag-details barrel.
vi.mock('../../dag-details', async () => ({
  DAGStepTable: (await import('../../dag-details/DAGStepTable')).default,
}));
vi.mock('@/features/visual-editor/DetailsCanvas', () => ({
  default: () => <div data-testid="details-canvas" />,
}));
vi.mock('../DAGEditorWithDocs', () => ({
  default: (props: { value: string; onChange?: (value?: string) => void }) => (
    <textarea
      aria-label="DAG spec"
      value={props.value}
      onChange={(event) => props.onChange?.(event.target.value)}
    />
  ),
}));

type VisualProps = Record<string, unknown> & {
  text: string;
  onTextChange(text: string): void;
  yamlVisible?: boolean;
  onYamlVisibleChange?(visible: boolean): void;
  toolbarLead?: React.ReactNode;
  toolbarTrail?: React.ReactNode;
  onStateChange?(state: VisualEditorState): void;
  handleRef?: React.Ref<VisualEditorHandle>;
};
const visual = vi.hoisted(() => ({
  props: null as VisualProps | null,
  handle: null as VisualEditorHandle | null,
}));

// Stands in for the editor with host seams: it gives its handle to handleRef.
vi.mock('@/features/visual-editor', () => ({
  default: function MockVisualEditor(props: VisualProps) {
    visual.props = props;
    React.useImperativeHandle(
      visual.handle ? props.handleRef : undefined,
      () => visual.handle!,
      []
    );
    return (
      <div data-testid="visual-editor-mock">
        {props.toolbarLead}
        {props.toolbarTrail}
        <button
          type="button"
          onClick={() => props.onTextChange(props.text + '# visual\n')}
        >
          Visual edit
        </button>
        <button
          type="button"
          onClick={() => props.onYamlVisibleChange?.(!props.yamlVisible)}
        >
          Toggle YAML
        </button>
      </div>
    );
  },
}));

vi.mock('@/features/visual-editor/run/SaveDiffDialog', () => ({
  SaveDiffDialog: (props: { onConfirm(): void; onCancel(): void }) => (
    <div role="dialog" aria-label="Review changes">
      <button type="button" onClick={props.onConfirm}>
        Confirm save
      </button>
      <button type="button" onClick={props.onCancel}>
        Cancel review
      </button>
    </div>
  ),
}));

const appBarValue = {
  title: 'DAGs',
  setTitle: vi.fn(),
  remoteNodes: ['local'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'local',
  selectRemoteNode: vi.fn(),
};

const savedSpec = 'steps:\n  - name: extract\n    run: echo hello\n';

function specData(dag: Record<string, unknown> = {}) {
  return {
    data: {
      dag: { name: 'example', steps: [{ name: 'extract' }], ...dag },
      spec: savedSpec,
      errors: [],
      valueReferenceNotices: [],
    },
    isLoading: false,
    mutate: vi.fn(),
  };
}

function fakeVisualHandle(): VisualEditorHandle {
  return {
    fitView: vi.fn(),
    revealStep: vi.fn(() => true),
    addStep: vi.fn(),
    openWorkflowPanel: vi.fn(),
    openLauncher: vi.fn(),
    toggleYaml: vi.fn(),
    openForm: vi.fn(() => true),
    openYaml: vi.fn(() => true),
    copySelection: vi.fn(() => 'steps: []\n'),
    paste: vi.fn(() => ({ ok: true as const, text: 'pasted', edits: [] })),
    duplicateSelection: vi.fn(() => ({
      ok: true as const,
      text: 'duplicated',
      edits: [],
    })),
    undo: vi.fn(),
    redo: vi.fn(),
  };
}

type Extra = Partial<DAGSpecHostProps> & {
  localDags?: React.ComponentProps<typeof DAGSpec>['localDags'];
  keyboardActive?: boolean;
};

function tree(props: Extra = {}, sibling?: React.ReactNode) {
  return (
    <UserPreferencesProvider>
      <AppBarContext.Provider value={appBarValue}>
        <DAGContext.Provider
          value={{
            refresh: mocks.refresh,
            name: 'example',
            fileName: 'example.yaml',
          }}
        >
          <DAGSpec fileName="example.yaml" {...props} />
          {sibling}
        </DAGContext.Provider>
      </AppBarContext.Provider>
    </UserPreferencesProvider>
  );
}

/** Lets the lazy chunks resolve. */
async function settle() {
  await act(async () => {
    for (let i = 0; i < 8; i++) {
      await Promise.resolve();
    }
  });
}

async function renderSpec(props: Extra = {}, sibling?: React.ReactNode) {
  const view = render(tree(props, sibling));
  await settle();
  return view;
}

/** Renders with an editor handle ref, and returns the handle. */
async function renderWithHandle(props: Extra = {}) {
  const ref = React.createRef<WorkspaceEditorHandle>();
  const view = await renderSpec({ ...props, editorHandleRef: ref });
  const handle = () => {
    if (!ref.current) throw new Error('no editor handle');
    return ref.current;
  };
  return { ...view, ref, handle };
}

beforeEach(() => {
  localStorage.clear();
  auth.canWrite = true;
  visual.props = null;
  visual.handle = fakeVisualHandle();
  drawer.panelId = undefined;
  mocks.useQuery.mockReturnValue(specData());
  mocks.post.mockResolvedValue({
    data: { valid: true, errors: [], dag: undefined },
  });
  mocks.put.mockResolvedValue({ data: { errors: [] } });
  mocks.copyText.mockResolvedValue(true);
});

afterEach(() => {
  vi.clearAllMocks();
});

// parity:test-dag-spec
describe('DAGSpec host props', () => {
  it('gives the host props to the visual editor unchanged', async () => {
    const host = {
      runSource: { runId: 'r1', details: null },
      onRunStarted: vi.fn(),
      selectedStepName: 'extract',
      onSelectedStepNameChange: vi.fn(),
      openNdvOn: 'open' as const,
      renderStepRun: vi.fn(() => null),
      inputColumnRoot: document.createElement('div'),
      outputColumnRoot: document.createElement('div'),
      onStepCommand: vi.fn(),
      stepCommandGates: {
        canRunStep: true,
        canRetry: () => true,
        canSetStatus: () => false,
        hasSubRuns: () => false,
      },
      chrome: { hideRunBar: true, compactNotices: true, striped: true },
      onNotice: vi.fn(),
    };
    await renderSpec(host);

    for (const [key, value] of Object.entries(host)) {
      expect(visual.props?.[key], key).toBe(value);
    }
  });

  it('passes the visual editor handle and state on to the host', async () => {
    const handleRef = React.createRef<VisualEditorHandle>();
    const onStateChange = vi.fn();
    await renderSpec({ handleRef, onStateChange });

    expect(handleRef.current).toBe(visual.handle);
    const state: VisualEditorState = {
      frozen: true,
      canUndo: true,
      canRedo: false,
      selectedStepName: 'extract',
      drawer: 'yaml',
      direction: 'LR',
    };
    act(() => visual.props?.onStateChange?.(state));
    expect(onStateChange).toHaveBeenCalledWith(state);
  });

  it('fills the height of its parent with fillHeight', async () => {
    const filled = await renderSpec({ fillHeight: true });
    expect(filled.container.firstElementChild).toHaveClass('h-full');
    filled.unmount();

    const classic = await renderSpec();
    expect(classic.container.firstElementChild).not.toHaveClass('h-full');
  });

  it('accepts notices and does not render them yet', async () => {
    await renderSpec({
      notices: [
        { id: 'draft', tone: 'info', message: 'A notice', priority: 1 },
      ],
    });
    expect(screen.queryByText('A notice')).not.toBeInTheDocument();
  });

  // parity:spec-subdag-readonly-canvas
  it('gives the sub-DAG step details their panel id', async () => {
    await renderSpec({
      localDags: [
        {
          name: 'child',
          dag: { name: 'child', steps: [{ name: 'load' }] },
          errors: [],
        },
      ] as Extra['localDags'],
    });
    expect(drawer.panelId).toBeUndefined();

    fireEvent.change(screen.getByRole('combobox', { name: 'Document' }), {
      target: { value: 'child' },
    });
    await settle();
    expect(drawer.panelId).toBe('subdag-step');
  });
});

/** Renders the workflow panel that DAGSpec gave to the visual editor. */
function renderWorkflowPanel() {
  const panel = visual.props?.workflowPanel;
  if (!React.isValidElement(panel)) throw new Error('no workflow panel');
  return render(panel);
}

describe('DAGSpec workflow panel in a host', () => {
  // parity:crit-spec-definition-step-row
  it('gives the visual editor the rows of the handlers, not of the steps', async () => {
    mocks.useQuery.mockReturnValue(
      specData({
        steps: [
          { name: 'extract', id: 'ext', description: 'Reads the sales.' },
          { name: 'load', call: 'warehouse-load' },
        ],
        handlerOn: { failure: { name: 'notify' } },
      })
    );
    await renderSpec({ chrome: { compactNotices: true } });
    const panel = renderWorkflowPanel();

    expect(panel.queryByRole('region', { name: 'Steps' })).toBeNull();
    expect(panel.queryByText('extract')).toBeNull();
    const handlers = within(panel.getByRole('region', { name: 'Handlers' }));
    expect(handlers.getAllByRole('row').slice(1)).toHaveLength(1);
    expect(handlers.getByText('notify')).toBeInTheDocument();
  });

  // parity:agent-spec-overview
  it('shows the tasks and actions of an agent DAG in the panel', async () => {
    mocks.useQuery.mockReturnValue(
      specData({
        type: 'agent',
        steps: [{ name: 'plan', id: 'plan', script: 'make plan' }],
        tasks: [{ name: 'planned', description: 'A plan exists.' }],
      })
    );
    await renderSpec({ chrome: { compactNotices: true } });
    expect(screen.getByTestId('visual-editor-mock')).toBeInTheDocument();
    const panel = renderWorkflowPanel();

    expect(panel.getByRole('heading', { name: 'Tasks' })).toBeInTheDocument();
    expect(panel.getByText('A plan exists.')).toBeInTheDocument();
    expect(panel.getByRole('button', { name: /plan/ })).toBeInTheDocument();
    expect(panel.getByText('make plan')).toBeInTheDocument();
    expect(
      panel.queryByRole('region', { name: 'Steps' })
    ).not.toBeInTheDocument();
  });
});

describe('DAGSpec editor handle', () => {
  it('stays null until the spec has loaded', async () => {
    mocks.useQuery.mockReturnValue({
      data: undefined,
      isLoading: true,
      mutate: vi.fn(),
    });
    const ref = React.createRef<WorkspaceEditorHandle>();
    const view = await renderSpec({ editorHandleRef: ref });
    expect(ref.current).toBeNull();

    // A changed prop renders the memoized DAGSpec with the new query result.
    mocks.useQuery.mockReturnValue(specData());
    view.rerender(tree({ editorHandleRef: ref, keyboardActive: false }));
    await settle();
    expect(ref.current).not.toBeNull();
  });

  // parity:api-dag-spec-validate
  it('opens the diff on save only while the buffer is dirty', async () => {
    const { handle } = await renderWithHandle();

    act(() => handle().save());
    await settle();
    expect(
      screen.queryByRole('dialog', { name: 'Review changes' })
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByText('Visual edit'));
    act(() => handle().save());
    await settle();
    expect(
      screen.getByRole('dialog', { name: 'Review changes' })
    ).toBeInTheDocument();

    await act(async () => {
      fireEvent.click(screen.getByText('Confirm save'));
    });
    await settle();
    expect(mocks.put).toHaveBeenCalledOnce();
    expect(mocks.refresh).toHaveBeenCalled();
  });

  // The server cannot read a YAML syntax error: save() and Ctrl+S refuse it.
  it('refuses to save a buffer with a YAML syntax error', async () => {
    const { handle } = await renderWithHandle();

    act(() =>
      handle().replaceBuffer('steps:\n  - name: b\n   command: [unclosed')
    );
    act(() => handle().save());
    fireEvent.keyDown(document, { key: 's', ctrlKey: true });
    await settle();

    expect(
      screen.queryByRole('dialog', { name: 'Review changes' })
    ).not.toBeInTheDocument();
    expect(mocks.showToast).toHaveBeenCalledWith(
      expect.stringContaining('Fix the YAML syntax error'),
      { variant: 'error' }
    );
  });

  it('does not open the diff while a save runs', async () => {
    let finish: (value: unknown) => void = () => {};
    mocks.put.mockReturnValue(new Promise((resolve) => (finish = resolve)));
    const { handle } = await renderWithHandle();

    fireEvent.click(screen.getByText('Visual edit'));
    act(() => handle().save());
    await settle();
    await act(async () => {
      fireEvent.click(screen.getByText('Confirm save'));
    });
    fireEvent.click(screen.getByText('Cancel review'));

    act(() => handle().save());
    await settle();
    expect(
      screen.queryByRole('dialog', { name: 'Review changes' })
    ).not.toBeInTheDocument();

    await act(async () => {
      finish({ data: { errors: [] } });
    });
  });

  // parity:spec-discard
  it('discards, replaces and reads the buffer', async () => {
    const { handle } = await renderWithHandle();
    expect(handle().isDirty()).toBe(false);
    expect(handle().getBuffer()).toBe(savedSpec);

    act(() => handle().replaceBuffer('steps: []\n'));
    expect(visual.props?.text).toBe('steps: []\n');
    expect(handle().isDirty()).toBe(true);
    expect(handle().getBuffer()).toBe('steps: []\n');
    expect(screen.getByRole('button', { name: /^save$/i })).toBeEnabled();

    act(() => handle().discard());
    expect(visual.props?.text).toBe(savedSpec);
    expect(handle().isDirty()).toBe(false);
    expect(screen.getByRole('button', { name: /^save$/i })).toBeDisabled();
  });

  it('does not change the buffer without write permission', async () => {
    auth.canWrite = false;
    const { handle } = await renderWithHandle();

    act(() => handle().replaceBuffer('steps: []\n'));
    expect(handle().isDirty()).toBe(false);
    expect(handle().getBuffer()).toBe(savedSpec);
    act(() => handle().discard());
    expect(visual.props?.text).toBe(savedSpec);
    expect(
      screen.queryByRole('button', { name: /^save$/i })
    ).not.toBeInTheDocument();
  });

  // parity:spec-copy-yaml
  it('copies the YAML like the Copy button', async () => {
    const { handle } = await renderWithHandle();
    fireEvent.click(screen.getByText('Visual edit'));

    await act(async () => {
      await handle().copyYaml();
    });
    expect(mocks.copyText).toHaveBeenCalledWith(savedSpec + '# visual\n');
  });

  // Without a visual editor handle (a sub-DAG document, or an editor that
  // gives none), DAGSpec drives the section itself.
  it('opens and closes the YAML section', async () => {
    visual.handle = null;
    const { handle } = await renderWithHandle();
    const editor = () => screen.getByLabelText('DAG spec');
    expect(editor()).not.toBeVisible();

    act(() => handle().toggleYaml());
    expect(editor()).toBeVisible();
    expect(visual.props?.yamlVisible).toBe(true);
    act(() => handle().toggleYaml(true));
    expect(editor()).toBeVisible();
    act(() => handle().toggleYaml(false));
    expect(editor()).not.toBeVisible();
    expect(visual.props?.yamlVisible).toBe(false);
  });

  it('passes the canvas commands to the visual editor', async () => {
    const { handle } = await renderWithHandle();
    const ve = visual.handle!;

    handle().fitView();
    expect(handle().revealStep('extract')).toBe(true);
    handle().addStep({ mode: 'first' });
    handle().openWorkflowPanel(true);
    handle().openLauncher('cron');
    expect(handle().openForm('extract')).toBe(true);
    expect(handle().openYaml('extract')).toBe(true);
    expect(handle().copySelection()).toBe('steps: []\n');
    expect(handle().paste('x')).toMatchObject({ ok: true, text: 'pasted' });
    expect(handle().duplicateSelection()).toMatchObject({ ok: true });
    handle().undo();
    handle().redo();
    handle().toggleYaml(true);

    expect(ve.fitView).toHaveBeenCalledOnce();
    expect(ve.toggleYaml).toHaveBeenCalledWith(true);
    expect(ve.revealStep).toHaveBeenCalledWith('extract');
    expect(ve.addStep).toHaveBeenCalledWith({ mode: 'first' });
    expect(ve.openWorkflowPanel).toHaveBeenCalledWith(true);
    expect(ve.openLauncher).toHaveBeenCalledWith('cron');
    expect(ve.openForm).toHaveBeenCalledWith('extract');
    expect(ve.openYaml).toHaveBeenCalledWith('extract');
    expect(ve.paste).toHaveBeenCalledWith('x');
    expect(ve.duplicateSelection).toHaveBeenCalledOnce();
    expect(ve.undo).toHaveBeenCalledOnce();
    expect(ve.redo).toHaveBeenCalledOnce();
  });

  it('returns empty results without a visual editor handle', async () => {
    visual.handle = null;
    const { handle } = await renderWithHandle();

    expect(() => handle().fitView()).not.toThrow();
    expect(handle().revealStep('extract')).toBe(false);
    expect(handle().openForm('extract')).toBe(false);
    expect(handle().openYaml('extract')).toBe(false);
    expect(handle().copySelection()).toBeNull();
    expect(handle().paste('x')).toMatchObject({ ok: false });
    expect(handle().duplicateSelection()).toMatchObject({ ok: false });
    expect(() => {
      handle().addStep();
      handle().openWorkflowPanel();
      handle().undo();
      handle().redo();
    }).not.toThrow();
  });
});

// The workspace keeps DAGSpec mounted in a hidden pane: keyboardActive
// turns its document Ctrl/Cmd+S handler off there.
describe('DAGSpec keyboardActive', () => {
  it('saves on Ctrl+S only while keyboardActive is not false', async () => {
    const view = await renderSpec({ keyboardActive: false });
    fireEvent.click(screen.getByText('Visual edit'));

    fireEvent.keyDown(document, { key: 's', ctrlKey: true });
    fireEvent.keyDown(document, { key: 's', metaKey: true });
    await settle();
    expect(
      screen.queryByRole('dialog', { name: 'Review changes' })
    ).not.toBeInTheDocument();

    view.rerender(tree({ keyboardActive: true }));
    await settle();
    fireEvent.keyDown(document, { key: 's', ctrlKey: true });
    await settle();
    expect(
      screen.getByRole('dialog', { name: 'Review changes' })
    ).toBeInTheDocument();
  });

  it('lets the browser have Ctrl+S while keyboardActive is false', async () => {
    await renderSpec({ keyboardActive: false });
    fireEvent.click(screen.getByText('Visual edit'));

    expect(fireEvent.keyDown(document, { key: 's', ctrlKey: true })).toBe(true);
  });
});

describe('DAGSpec editor state', () => {
  const defaults = {
    frozen: false,
    canUndo: false,
    canRedo: false,
    selectedStepName: null,
    drawer: 'none',
    direction: 'TB',
  };

  it('reports its state merged with the visual editor state', async () => {
    const onEditorStateChange = vi.fn<(s: WorkspaceEditorState) => void>();
    const view = await renderSpec({ onEditorStateChange });

    expect(onEditorStateChange).toHaveBeenCalledTimes(1);
    expect(onEditorStateChange).toHaveBeenLastCalledWith({
      ...defaults,
      loaded: true,
      editable: true,
      dirty: false,
      saving: false,
      saveState: 'clean',
      hasDraft: false,
      conflict: false,
    });

    // A render that changes no field reports nothing.
    view.rerender(tree({ onEditorStateChange }));
    await settle();
    expect(onEditorStateChange).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByText('Visual edit'));
    expect(onEditorStateChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ dirty: true, saveState: 'validating' })
    );
    const calls = onEditorStateChange.mock.calls.length;

    const state: VisualEditorState = {
      ...defaults,
      selectedStepName: 'extract',
      drawer: 'form',
    } as VisualEditorState;
    act(() => visual.props?.onStateChange?.(state));
    expect(onEditorStateChange).toHaveBeenCalledTimes(calls + 1);
    expect(onEditorStateChange).toHaveBeenLastCalledWith(
      expect.objectContaining({
        selectedStepName: 'extract',
        drawer: 'form',
        dirty: true,
      })
    );

    act(() => visual.props?.onStateChange?.({ ...state }));
    expect(onEditorStateChange).toHaveBeenCalledTimes(calls + 1);
  });

  it('reports a stored draft', async () => {
    localStorage.setItem(
      'dagu:draft:local:example.yaml',
      JSON.stringify({ text: savedSpec + '# draft\n', savedAt: 1 })
    );
    const onEditorStateChange = vi.fn();
    await renderSpec({ onEditorStateChange });
    expect(onEditorStateChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ hasDraft: true })
    );
  });
});

describe('DAGSpec ndv prop', () => {
  it('opens the section exactly when the state is not null', async () => {
    const onStateChange = vi.fn();
    const view = await renderSpec({ ndv: { state: null, onStateChange } });
    const editor = () => screen.getByLabelText('DAG spec');
    expect(editor()).not.toBeVisible();
    expect(visual.props?.yamlVisible).toBe(false);

    // The node details view opens full screen; docking is a choice.
    fireEvent.click(screen.getByText('Toggle YAML'));
    expect(onStateChange).toHaveBeenLastCalledWith('expanded');
    expect(editor()).not.toBeVisible();

    view.rerender(tree({ ndv: { state: 'expanded', onStateChange } }));
    expect(editor()).toBeVisible();
    expect(visual.props?.yamlVisible).toBe(true);

    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onStateChange).toHaveBeenLastCalledWith(null);
    expect(editor()).toBeVisible();

    view.rerender(tree({ ndv: { state: null, onStateChange } }));
    expect(editor()).not.toBeVisible();
  });

  // A hidden pane shares the URL state with the view that is shown.
  it('corrects the state of the host only when it is active', async () => {
    const onStateChange = vi.fn();
    const view = await renderSpec({
      ndv: { state: null, onStateChange, active: false },
    });
    view.rerender(
      tree({ ndv: { state: 'expanded', onStateChange, active: false } })
    );
    expect(onStateChange).not.toHaveBeenCalled();

    // The same state on an active pane is the node view of a step.
    view.rerender(
      tree({ ndv: { state: 'expanded', onStateChange, active: true } })
    );
    expect(onStateChange).not.toHaveBeenCalled();
  });
});

function AddStepClaimer() {
  const { claim } = useRightPanel('add-step', () => {});
  return (
    <button type="button" onClick={claim}>
      Claim add-step
    </button>
  );
}

// parity:drawer-mode-rules
describe('DAGSpec YAML section claim', () => {
  it('claims yaml while open and closes when evicted', async () => {
    render(
      <RightPanelProvider>{tree({}, <AddStepClaimer />)}</RightPanelProvider>
    );
    await settle();
    const editor = () => screen.getByLabelText('DAG spec');

    fireEvent.click(screen.getByText('Toggle YAML'));
    expect(editor()).toBeVisible();

    act(() => {
      fireEvent.click(screen.getByText('Claim add-step'));
    });
    expect(editor()).not.toBeVisible();
    expect(visual.props?.yamlVisible).toBe(false);
  });

  it('asks the host to close the ndv when evicted', async () => {
    const onStateChange = vi.fn();
    render(
      <RightPanelProvider>
        {tree({ ndv: { state: 'docked', onStateChange } }, <AddStepClaimer />)}
      </RightPanelProvider>
    );
    await settle();

    act(() => {
      fireEvent.click(screen.getByText('Claim add-step'));
    });
    expect(onStateChange).toHaveBeenCalledWith(null);
  });
});
