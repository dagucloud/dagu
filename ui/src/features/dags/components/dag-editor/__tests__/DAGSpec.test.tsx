// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog';
import { AppBarContext } from '@/contexts/AppBarContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import type { SSEState } from '@/hooks/useSSE';
import { DAGContext } from '../../../contexts/DAGContext';
import { PanelPlacementProvider } from '../../step-details/PanelShell';
import { NO_ID_MESSAGE } from '@/features/visual-editor/doc/issueMessages';
import DAGSpec from '../DAGSpec';

const mocks = vi.hoisted(() => {
  const get = vi.fn();
  const post = vi.fn();
  const put = vi.fn();
  return {
    get,
    post,
    put,
    // The client must be render-stable like the real useClient singleton.
    client: { GET: get, POST: post, PUT: put },
    showError: vi.fn(),
    showToast: vi.fn(),
    useQuery: vi.fn(),
    editorProps: {
      current: {} as {
        markers?: unknown[];
        referenceState?: () => { scope?: unknown; model?: { text: string } };
      },
    },
    editorMounts: 0,
    canWrite: true,
  };
});

vi.mock('@/hooks/api', () => ({
  useClient: () => mocks.client,
  useQuery: mocks.useQuery,
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteForWorkspace: () => mocks.canWrite,
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

// The DAG event stream of the test; useSSECacheSync and sseFallbackOptions
// stay real.
const sse = vi.hoisted(() => ({
  args: [] as unknown[],
  state: null as unknown as SSEState<unknown>,
}));

vi.mock('../../../../../hooks/useDAGSSE', () => ({
  useDAGSSE: (...args: unknown[]) => {
    sse.args = args;
    return sse.state;
  },
}));

vi.mock('@/features/dags/components/step-details', () => ({
  StepDetailsDrawer: ({
    isOpen,
    step,
  }: {
    isOpen: boolean;
    step?: { name: string };
  }) =>
    isOpen && step ? <div data-testid="step-drawer">{step.name}</div> : null,
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

vi.mock('../DAGAttributes', () => ({ default: () => null }));
vi.mock('../AgentSpecOverview', () => ({
  AgentSpecOverview: () => <div>Agent overview</div>,
}));
vi.mock('../ExternalChangeDialog', () => ({
  default: ({
    visible,
    onDiscard,
    onIgnore,
  }: {
    visible: boolean;
    onDiscard(): void;
    onIgnore(): void;
  }) =>
    visible ? (
      <div role="dialog">
        External Changes Detected
        <button type="button" onClick={onDiscard}>
          Discard & Reload
        </button>
        <button type="button" onClick={onIgnore}>
          Ignore
        </button>
      </div>
    ) : null,
}));
vi.mock('../../dag-details', () => ({ DAGStepTable: () => null }));
vi.mock('../../value-reference-notices', () => ({
  ValueReferenceNoticesButton: () => null,
}));

vi.mock('../WorkflowPanel', () => ({
  WorkflowPanel: ({
    dag,
  }: {
    dag: { steps?: { name: string }[]; description?: string };
  }) => (
    <div data-testid="preview-graph">
      {dag.steps?.map((step) => step.name).join(',')}
      {dag.description}
    </div>
  ),
}));

vi.mock('@/features/visual-editor/DetailsCanvas', () => ({
  default: (props: {
    dag: { name: string; steps?: { name: string }[] };
    toolbarEnd?: React.ReactNode;
    toolbarStart?: React.ReactNode;
    toolbarTrail?: React.ReactNode;
    onSelectStep(name: string | null): void;
  }) => (
    <div data-testid="details-canvas">
      {props.toolbarStart}
      {props.toolbarEnd}
      {props.toolbarTrail}
      <span>{props.dag.steps?.map((step) => step.name).join(',')}</span>
      <button type="button" onClick={() => props.onSelectStep('load')}>
        Pick load
      </button>
    </div>
  ),
}));

vi.mock('../DAGEditorWithDocs', () => ({
  default: function MockEditor(props: {
    value: string;
    onChange?: (value?: string) => void;
    readOnly?: boolean;
    markers?: unknown[];
    referenceState?: () => { scope?: unknown; model?: { text: string } };
  }) {
    mocks.editorProps.current = props;
    React.useEffect(() => {
      mocks.editorMounts += 1;
    }, []);
    return (
      <textarea
        aria-label="DAG spec"
        readOnly={props.readOnly}
        value={props.value}
        onChange={(event) => props.onChange?.(event.target.value)}
      />
    );
  },
}));

type VisualProps = {
  text: string;
  onTextChange(text: string): void;
  validation: unknown;
  editable: boolean;
  isDirty: boolean;
  onRevertToSave?(): void;
  yamlVisible?: boolean;
  onYamlVisibleChange?(visible: boolean): void;
  onFormVisibleChange?(visible: boolean): void;
  toolbarStart?: React.ReactNode;
  toolbarTrail?: React.ReactNode;
  workflowPanel?: React.ReactNode;
  canvasOverlay?: React.ReactNode;
  harnesses?: string[];
};
const visual = vi.hoisted(() => ({ props: null as VisualProps | null }));

vi.mock('@/features/visual-editor', () => ({
  default: function MockVisualEditor(props: VisualProps) {
    visual.props = props;
    return (
      <div data-testid="visual-editor-mock">
        {props.toolbarStart}
        {props.toolbarTrail}
        {props.canvasOverlay}
        {props.workflowPanel}
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
        <button type="button" onClick={() => props.onFormVisibleChange?.(true)}>
          Open form
        </button>
        {/* Sends its text on blur, as the fields of the step form do. */}
        <input
          aria-label="Form field"
          onBlur={(event) => {
            if (event.target.value) {
              props.onTextChange(`${props.text}# ${event.target.value}\n`);
            }
          }}
        />
      </div>
    );
  },
}));

vi.mock('@/features/visual-editor/run/SaveDiffDialog', () => ({
  SaveDiffDialog: (props: {
    original: string;
    edited: string;
    onConfirm(): void;
    onCancel(): void;
  }) => (
    // The real Dialog, so that the layer of the host applies to it. Not modal:
    // the page behind it stays reachable for the other tests.
    <Dialog open modal={false}>
      <DialogContent aria-label="Review changes" aria-describedby={undefined}>
        <DialogTitle>Review changes</DialogTitle>
        <pre data-testid="diff-edited">{props.edited}</pre>
        <button type="button" onClick={props.onConfirm}>
          Confirm save
        </button>
        <button type="button" onClick={props.onCancel}>
          Cancel review
        </button>
      </DialogContent>
    </Dialog>
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

function specData(overrides: Record<string, unknown> = {}) {
  return {
    data: {
      dag: { name: 'example', steps: [{ name: 'extract' }] },
      spec: savedSpec,
      errors: [],
      valueReferenceNotices: [],
      ...overrides,
    },
    isLoading: false,
    mutate: vi.fn(),
  };
}

const dagContextValue = {
  refresh: vi.fn(),
  name: 'example',
  fileName: 'example.yaml',
};

function specTree(props: Partial<React.ComponentProps<typeof DAGSpec>> = {}) {
  return (
    <UserPreferencesProvider>
      <AppBarContext.Provider value={appBarValue}>
        <DAGContext.Provider value={dagContextValue}>
          <DAGSpec fileName="example.yaml" {...props} />
        </DAGContext.Provider>
      </AppBarContext.Provider>
    </UserPreferencesProvider>
  );
}

/** Renders the tab and waits for the lazy visual editor to load. */
async function renderSpec(
  props: Partial<React.ComponentProps<typeof DAGSpec>> = {}
) {
  const view = render(specTree(props));
  await settle();
  return view;
}

/** A DAG event stream that is connected, with its last event. */
function connected(data: unknown): SSEState<unknown> {
  return {
    data,
    error: null,
    isConnected: true,
    isConnecting: false,
    shouldUseFallback: false,
  };
}

/** Lets the lazy chunks resolve, with real or fake timers. */
async function settle() {
  await act(async () => {
    for (let i = 0; i < 8; i++) {
      await Promise.resolve();
    }
  });
}

/** Clicks Save, then confirms the diff that the editor shows first. */
async function clickSave(
  button: HTMLElement = screen.getByRole('button', { name: /^save$/i })
) {
  await act(async () => {
    fireEvent.click(button);
  });
  await settle();
  await act(async () => {
    fireEvent.click(screen.getByText('Confirm save'));
  });
}

beforeEach(() => {
  localStorage.clear();
  mocks.editorMounts = 0;
  mocks.canWrite = true;
  sse.args = [];
  sse.state = {
    data: null,
    error: null,
    isConnected: false,
    isConnecting: true,
    shouldUseFallback: false,
  };
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

describe('DAGSpec live validation', () => {
  it('shows saved warnings separately from errors', async () => {
    mocks.useQuery.mockReturnValue(
      specData({
        warnings: ['Harness step review has no explicit working_dir'],
      })
    );
    await renderSpec();

    expect(screen.getByRole('status')).toHaveTextContent('Warnings');
    expect(screen.getByRole('status')).toHaveTextContent('working_dir');
    expect(screen.getByTestId('preview-graph')).toHaveTextContent('extract');
  });

  it('allows saving a warning-only spec and clears corrected warnings', async () => {
    vi.useFakeTimers();
    mocks.post.mockResolvedValueOnce({ data: { valid: true, errors: [] } }); // saved spec
    mocks.post.mockResolvedValueOnce({
      data: {
        valid: true,
        errors: [],
        warnings: ['Harness step review has no explicit working_dir'],
      },
    });
    await renderSpec();
    const editor = screen.getByLabelText('DAG spec');
    fireEvent.change(editor, { target: { value: savedSpec + '# edited' } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });

    expect(screen.getByText('Valid with warnings')).toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('working_dir');
    expect(mocks.editorProps.current.markers).toEqual([]);
    await clickSave();
    expect(mocks.put).toHaveBeenCalledOnce();
    expect(mocks.showError).not.toHaveBeenCalled();
    // The save slot shows the result: no success toast.
    expect(mocks.showToast).not.toHaveBeenCalled();

    mocks.post.mockResolvedValue({
      data: { valid: true, errors: [], warnings: [] },
    });
    fireEvent.change(editor, {
      target: { value: 'working_dir: ./repo\n' + savedSpec },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });
    expect(screen.queryByText('Valid with warnings')).not.toBeInTheDocument();
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  // Swapping the preview back to the saved spec between keystrokes makes the
  // preview flicker while typing.
  it('keeps the last validation result while revalidating', async () => {
    vi.useFakeTimers();
    const warning = 'Harness step review has no explicit working_dir';
    const error = 'step "load" depends on missing step "transform"';
    mocks.useQuery.mockReturnValue(specData({ warnings: [warning] }));
    mocks.post.mockResolvedValueOnce({ data: { valid: true, errors: [] } }); // saved spec
    mocks.post.mockResolvedValueOnce({
      data: {
        valid: false,
        errors: [error],
        warnings: [warning],
        dag: {
          name: 'example',
          steps: [{ name: 'extract' }, { name: 'load' }],
        },
      },
    });
    await renderSpec();
    const editor = screen.getByLabelText('DAG spec');
    expect(screen.getByRole('status')).toHaveTextContent(warning);

    fireEvent.change(editor, { target: { value: savedSpec + '# edited' } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });
    expect(screen.getByRole('status')).toHaveTextContent(warning);
    expect(screen.getByText(error)).toBeInTheDocument();
    expect(screen.getByTestId('preview-graph')).toHaveTextContent(
      'extract,load'
    );

    fireEvent.change(editor, {
      target: { value: savedSpec + '# edited again' },
    });
    expect(screen.getByText('Validating...')).toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent(warning);
    expect(screen.getByText(error)).toBeInTheDocument();
    expect(screen.getByTestId('preview-graph')).toHaveTextContent(
      'extract,load'
    );

    mocks.post.mockResolvedValueOnce({ error: { message: 'unavailable' } });
    fireEvent.change(editor, {
      target: { value: 'working_dir: ./repo\n' + savedSpec },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    expect(screen.queryByText(error)).not.toBeInTheDocument();

    fireEvent.change(editor, { target: { value: savedSpec } });
    expect(screen.getByRole('status')).toHaveTextContent(warning);
  });

  it('prevents overlapping saves without losing newer edits', async () => {
    vi.useFakeTimers();
    let finishSave!: (value: { data: { errors: string[] } }) => void;
    mocks.put.mockReturnValueOnce(
      new Promise((resolve) => {
        finishSave = resolve;
      })
    );
    await renderSpec();
    const editor = screen.getByLabelText('DAG spec');
    const submitted = savedSpec + '# saved';
    const newerEdit = submitted + '\n# still editing';
    fireEvent.change(editor, { target: { value: submitted } });
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }));
    await settle();
    await act(async () => {
      fireEvent.click(screen.getByText('Confirm save'));
    });
    fireEvent.change(editor, { target: { value: submitted + '\n# next' } });
    const saveButton = screen.getByRole('button', { name: /^save$/i });
    expect(saveButton).toBeDisabled();
    fireEvent.click(saveButton);
    fireEvent.keyDown(document, { key: 's', ctrlKey: true });
    expect(mocks.put).toHaveBeenCalledOnce();

    mocks.useQuery.mockReturnValue(specData({ spec: submitted }));
    fireEvent.change(editor, { target: { value: newerEdit } });
    expect(
      screen.queryByText('External Changes Detected')
    ).not.toBeInTheDocument();

    await act(async () => {
      finishSave({ data: { errors: [] } });
    });
    expect(editor).toHaveValue(newerEdit);
    expect(screen.getByRole('button', { name: /^save$/i })).toBeEnabled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it.each([
    ['request failure', { error: { message: 'unavailable' } }],
    ['validation rejection', { data: { errors: ['invalid spec'] } }],
  ])('detects external changes after a save %s', async (_name, response) => {
    vi.useFakeTimers();
    mocks.put.mockResolvedValueOnce(response);
    await renderSpec();
    const editor = screen.getByLabelText('DAG spec');
    const submitted = savedSpec + '# submitted';
    fireEvent.change(editor, { target: { value: submitted } });
    await clickSave();
    expect(mocks.showError).toHaveBeenCalled();
    expect(screen.getByRole('button', { name: /^save$/i })).toBeEnabled();

    mocks.useQuery.mockReturnValue(specData({ spec: submitted }));
    fireEvent.change(editor, {
      target: { value: submitted + '\n# still editing' },
    });
    expect(screen.getByRole('dialog')).toHaveTextContent('External Changes');
  });

  it('shows the validation errors of a refused save without Go formatting', async () => {
    vi.useFakeTimers();
    mocks.put.mockResolvedValueOnce({
      data: {
        errors: [
          'field steps: field with: with.url must be a non-empty string (value: map[method:GET url:])',
        ],
      },
    });
    await renderSpec();
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: savedSpec + '# submitted' },
    });

    await clickSave();

    expect(mocks.showError).toHaveBeenCalledWith(
      'The spec was not saved',
      undefined,
      'Validation errors',
      ['with.url must be a non-empty string']
    );
  });

  it('sends the hash of the saved spec with a save', async () => {
    const sha = 'a'.repeat(64);
    const next = 'b'.repeat(64);
    mocks.useQuery.mockReturnValue(specData({ sha256: sha }));
    mocks.put.mockResolvedValue({ data: { errors: [], sha256: next } });
    await renderSpec();
    const editor = screen.getByLabelText('DAG spec');

    fireEvent.change(editor, { target: { value: savedSpec + '# one' } });
    await clickSave();
    fireEvent.change(editor, { target: { value: savedSpec + '# two' } });
    await clickSave();

    expect(mocks.put.mock.calls.map((call) => call[1].body)).toEqual([
      { spec: savedSpec + '# one', expectedSha256: sha },
      { spec: savedSpec + '# two', expectedSha256: next },
    ]);
  });

  // parity:test-ve-save-draft (conflict)
  it('refuses to overwrite a spec that changed on disk', async () => {
    const mutate = vi.fn();
    mocks.useQuery.mockReturnValue({
      ...specData({ sha256: 'a'.repeat(64) }),
      mutate,
    });
    mocks.put.mockResolvedValueOnce({
      error: { code: 'conflict', message: 'changed' },
      response: { status: 409 },
    });
    await renderSpec();
    const editor = screen.getByLabelText('DAG spec');
    fireEvent.change(editor, { target: { value: savedSpec + '# mine' } });

    await clickSave();

    expect(mocks.showError).toHaveBeenCalledWith(
      'The file changed on disk',
      'A newer version was saved. Review it before you save again.'
    );
    expect(mocks.showToast).not.toHaveBeenCalled();
    expect(mutate).toHaveBeenCalled();

    // The newer version arrives as an external change of the edited buffer.
    mocks.useQuery.mockReturnValue({
      ...specData({ spec: savedSpec + '# theirs', sha256: 'c'.repeat(64) }),
      mutate,
    });
    fireEvent.change(editor, { target: { value: savedSpec + '# mine!' } });
    expect(screen.getByRole('dialog')).toHaveTextContent('External Changes');
  });

  // A live event brings a text without its hash. Until the fresh read
  // returns, a save still names a hash, so the server can refuse it.
  it('keeps the last hash until the text of a live event is read again', async () => {
    const sha = 'a'.repeat(64);
    const mutate = vi.fn();
    mocks.useQuery.mockReturnValue({ ...specData({ sha256: sha }), mutate });
    const view = await renderSpec();
    const next = savedSpec + '# from disk\n';
    mocks.useQuery.mockReturnValue({
      ...specData({ spec: next, sha256: undefined }),
      mutate,
    });
    view.rerender(specTree({ keyboardActive: true }));
    expect(mutate).toHaveBeenCalled();

    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: next + '# mine\n' },
    });
    await clickSave();

    expect(mocks.put.mock.lastCall?.[1].body).toEqual({
      spec: next + '# mine\n',
      expectedSha256: sha,
    });
  });

  it('allows retrying a save after a network exception', async () => {
    vi.useFakeTimers();
    mocks.put.mockRejectedValueOnce(new Error('network unavailable'));
    await renderSpec();
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: savedSpec + '# edited' },
    });
    const saveButton = screen.getByRole('button', { name: /^save$/i });
    await clickSave(saveButton);
    expect(mocks.showError).toHaveBeenCalledWith(
      'Failed to save spec',
      'Please try again.'
    );
    expect(saveButton).toBeEnabled();
    await clickSave(saveButton);
    // The save slot shows the result: no success toast.
    expect(mocks.showToast).not.toHaveBeenCalled();
    expect(saveButton).toBeDisabled();
  });

  // parity:spec-live-validation
  it('validates the edited buffer once per idle window', async () => {
    vi.useFakeTimers();
    await renderSpec();

    const editor = screen.getByLabelText('DAG spec');
    fireEvent.change(editor, { target: { value: 'steps: [' } });
    fireEvent.change(editor, { target: { value: 'steps: [broken' } });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });

    // One call for the saved spec, one for the edited buffer.
    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(mocks.post).toHaveBeenLastCalledWith('/dags/validate', {
      params: { query: { remoteNode: 'local' } },
      body: { spec: 'steps: [broken', name: 'example.yaml' },
    });
  });

  it('renders markers, errors, and the live graph from the validate response', async () => {
    vi.useFakeTimers();
    mocks.post.mockResolvedValue({
      data: {
        valid: false,
        errors: [
          '[3:1] mapping values are not allowed in this context',
          "field 'steps': has invalid keys: nosuchfield",
        ],
        dag: {
          name: 'example',
          steps: [{ name: 'extract' }, { name: 'load' }],
        },
      },
    });

    await renderSpec();
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: 'steps: [broken' },
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });

    expect(
      screen.getByText(/mapping values are not allowed/)
    ).toBeInTheDocument();
    expect(
      screen.getByText(/has invalid keys: nosuchfield/)
    ).toBeInTheDocument();
    expect(screen.getByTestId('preview-graph')).toHaveTextContent(
      'extract,load'
    );
    expect(screen.getByText('2 issues')).toBeInTheDocument();

    const markers = mocks.editorProps.current.markers as Array<{
      startLineNumber: number;
    }>;
    expect(markers).toHaveLength(1);
    expect(markers[0]?.startLineNumber).toBe(3);
  });

  it('marks structured issues on the line of their field', async () => {
    vi.useFakeTimers();
    const edited = savedSpec + '    timeout_sec: soon\n';
    mocks.post.mockResolvedValue({
      data: {
        valid: false,
        errors: ['timeout_sec must be a number'],
        issues: [
          {
            severity: 'error',
            source: 'build',
            message: 'timeout_sec must be a number',
            stepPath: 'steps[0]',
            fieldPath: 'steps[0].timeout_sec',
          },
        ],
        dag: { name: 'example', steps: [{ name: 'extract' }] },
      },
    });
    await renderSpec();
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: edited },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });

    expect(mocks.editorProps.current.markers).toEqual([
      expect.objectContaining({
        startLineNumber: 4,
        message: 'timeout_sec must be a number',
      }),
    ]);
  });

  it('clears previous validation results when a validate request fails', async () => {
    vi.useFakeTimers();
    mocks.post.mockResolvedValueOnce({ data: { valid: true, errors: [] } }); // saved spec
    mocks.post.mockResolvedValueOnce({
      data: {
        valid: false,
        errors: ['[3:1] mapping values are not allowed in this context'],
        dag: undefined,
      },
    });

    await renderSpec();
    const editor = screen.getByLabelText('DAG spec');
    fireEvent.change(editor, { target: { value: 'steps: [broken' } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });
    expect(
      screen.getByText(/mapping values are not allowed/)
    ).toBeInTheDocument();

    // The next validation request fails; the old result no longer describes
    // the buffer and must not linger.
    mocks.post.mockResolvedValueOnce({ error: { message: 'boom' } });
    fireEvent.change(editor, { target: { value: 'steps: [more broken' } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });

    expect(
      screen.queryByText(/mapping values are not allowed/)
    ).not.toBeInTheDocument();
    expect(screen.queryByText(/issue/)).not.toBeInTheDocument();
  });

  it('renders the saved graph alongside saved errors', async () => {
    mocks.useQuery.mockReturnValue(
      specData({ errors: ['something is misconfigured'] })
    );

    await renderSpec();

    expect(screen.getByText('something is misconfigured')).toBeInTheDocument();
    expect(screen.getByTestId('preview-graph')).toHaveTextContent('extract');
  });
});

describe('DAGSpec visual view', () => {
  it('opens the visual editor and has no view switcher', async () => {
    await renderSpec();

    expect(screen.getByTestId('visual-editor-mock')).toBeInTheDocument();
    expect(
      screen.queryByRole('group', { name: 'View mode' })
    ).not.toBeInTheDocument();
    expect(screen.getByLabelText('DAG spec')).not.toBeVisible();
    expect(visual.props?.text).toBe(savedSpec);
  });

  // Remounting the editor would drop its undo history and cursor.
  it('opens the YAML drawer on request and keeps the same editor', async () => {
    await renderSpec();
    const edited = savedSpec + '# edited';
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: edited },
    });

    fireEvent.click(screen.getByText('Toggle YAML'));
    expect(screen.getByLabelText('DAG spec')).toBeVisible();
    expect(visual.props?.yamlVisible).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));

    expect(screen.getByLabelText('DAG spec')).not.toBeVisible();
    expect(screen.getByLabelText('DAG spec')).toHaveValue(edited);
    expect(mocks.editorMounts).toBe(1);
  });

  it('gives the toolbar the validation status and save, and the YAML panel copy', async () => {
    await renderSpec();

    // Copy is an icon over the YAML editor, not a button of the toolbar.
    const copy = screen.getByRole('button', {
      name: 'Copy YAML',
      hidden: true,
    });
    expect(copy).toHaveTextContent('');
    expect(
      screen.getByLabelText('DAG spec').closest('.relative')
    ).toContainElement(copy);
    expect(
      screen.queryByRole('button', { name: 'Discard' })
    ).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^save$/i })).toBeDisabled();
  });

  // The validation state is in the Save button, which keeps one size.
  // parity:spec-save-button-state
  it('shows the validation state in the Save button and keeps its size', async () => {
    vi.useFakeTimers();
    mocks.post.mockResolvedValueOnce({ data: { valid: true, errors: [] } }); // saved spec
    await renderSpec();
    const save = () => screen.getByRole('button', { name: /^save$/i });
    // Icon only: one fixed size, and the name is for the tooltip and readers.
    const width = save().className.match(/(?:^|\s)(w-7)(?:\s|$)/)?.[1];
    expect(width).toBeDefined();
    expect(save()).toHaveAccessibleName('Save');
    expect(save()).toHaveAttribute('title', expect.stringContaining('Save'));
    expect(save().textContent).toBe('');

    mocks.post.mockResolvedValueOnce({
      data: { valid: true, errors: [], warnings: ['Check working_dir'] },
    });
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: savedSpec + '# one' },
    });
    expect(save()).toHaveTextContent('Validating...');
    // The state is not written in the button, which only says Save: its text
    // is for screen readers, and a spinner shows the validation.
    expect(screen.getByText('Validating...')).toHaveClass('sr-only');
    expect(save().querySelector('svg.animate-spin')).not.toBeNull();
    expect(save().className).toContain(width);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });
    expect(save()).toHaveTextContent('Valid with warnings');
    expect(save().querySelector('svg.animate-spin')).toBeNull();
    // The floppy disk stays: the state never swaps the icon.
    expect(save().querySelector('svg.lucide-save')).not.toBeNull();
    expect(save().querySelectorAll('svg')).toHaveLength(1);
    expect(save().className).toContain(width);

    mocks.post.mockResolvedValueOnce({ data: { valid: true, errors: [] } });
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: savedSpec + '# two' },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });
    expect(save()).toHaveTextContent('Valid');
    expect(save()).not.toHaveTextContent('warnings');
    expect(save().querySelector('svg.lucide-save')).not.toBeNull();
    expect(save().querySelector('svg.lucide-check')).toBeNull();

    mocks.post.mockResolvedValueOnce({
      data: { valid: false, errors: ['a', 'b'] },
    });
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: savedSpec + '# three' },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });
    expect(save()).toHaveTextContent('2 issues');
    expect(save().querySelector('svg.lucide-save')).not.toBeNull();
    expect(save().className).toContain(width);
    // No text of the state sits elsewhere in the toolbar.
    expect(screen.getAllByText('2 issues')).toHaveLength(1);

    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: savedSpec },
    });
    expect(save()).toHaveAccessibleName('Save');
    expect(save().className).toContain(width);
  });

  // The server cannot read a text with a YAML syntax error.
  it('blocks Save while the YAML has a syntax error', async () => {
    await renderSpec();
    const save = () => screen.getByRole('button', { name: /^save$/i });

    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: {
        value: 'steps:\n  - name: b\n   command: [unclosed',
      },
    });
    expect(save()).toBeDisabled();
    expect(save()).toHaveAttribute(
      'title',
      expect.stringContaining('Fix the YAML syntax error')
    );

    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: savedSpec + '# fixed' },
    });
    expect(save()).toBeEnabled();
  });

  // A draft of more than one changed line is not discarded at once.
  it('asks before it reverts more than one changed line', async () => {
    await renderSpec();
    const editor = () => screen.getByLabelText('DAG spec');
    // The visual editor owns the 'Revert to the last save' button.
    const discard = () => act(() => visual.props?.onRevertToSave?.());

    fireEvent.change(editor(), { target: { value: savedSpec + '# one\n' } });
    discard();
    expect(editor()).toHaveValue(savedSpec);

    const edited = savedSpec + '# one\n# two\n';
    fireEvent.change(editor(), { target: { value: edited } });
    discard();
    expect(editor()).toHaveValue(edited);
    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveTextContent('Discard 2 changed lines?');

    fireEvent.click(within(dialog).getByRole('button', { name: /cancel/i }));
    expect(editor()).toHaveValue(edited);

    discard();
    fireEvent.click(
      within(await screen.findByRole('dialog')).getByRole('button', {
        name: 'Discard',
      })
    );
    expect(editor()).toHaveValue(savedSpec);
  });

  it('keeps visual edits in the shared buffer', async () => {
    await renderSpec();

    fireEvent.click(screen.getByText('Visual edit'));

    expect(visual.props?.isDirty).toBe(true);
    expect(screen.getByLabelText('DAG spec')).toHaveValue(
      savedSpec + '# visual\n'
    );
    expect(screen.getByRole('button', { name: /^save$/i })).toBeEnabled();
  });

  it('passes the issues and reference scope of the edited text', async () => {
    vi.useFakeTimers();
    const issue = {
      severity: 'error',
      source: 'build',
      message: 'step "extract" has no command',
      stepPath: 'steps[0]',
    };
    const referenceScope = {
      params: [],
      consts: [],
      env: [],
      secrets: [],
      steps: [],
    };
    mocks.post.mockResolvedValue({
      data: {
        valid: false,
        errors: [issue.message],
        issues: [issue],
        referenceScope,
        dag: { name: 'example', steps: [{ name: 'extract' }] },
      },
    });
    await renderSpec();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });

    fireEvent.click(screen.getByText('Visual edit'));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });

    expect(visual.props?.validation).toEqual({
      dag: { name: 'example', steps: [{ name: 'extract' }] },
      issues: [issue],
      referenceScope,
    });
  });

  // parity:spec-saved-validation
  it('validates the saved spec once for the visual editor', async () => {
    const issue = {
      severity: 'warning',
      source: 'warning',
      message: 'step "extract" has no working_dir',
      stepPath: 'steps[0]',
    };
    mocks.post.mockResolvedValue({
      data: {
        valid: true,
        errors: [],
        issues: [issue],
        dag: { name: 'example', steps: [{ name: 'extract' }] },
      },
    });
    await renderSpec();
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.post).toHaveBeenCalledWith('/dags/validate', {
      params: { query: { remoteNode: 'local' } },
      body: { spec: savedSpec, name: 'example.yaml' },
    });
    expect(visual.props?.validation).toMatchObject({ issues: [issue] });
  });

  // A base configuration that the file inherits changes the result, and
  // the definition of the file, without a change of its text.
  it('validates the saved spec again when its definition changes', async () => {
    const result = (env: string) => ({
      data: {
        valid: true,
        errors: [],
        issues: [],
        referenceScope: { params: [], consts: [], env: [env], secrets: [] },
      },
    });
    mocks.post.mockResolvedValueOnce(result('OLD'));
    const view = await renderSpec();
    await settle();

    mocks.post.mockResolvedValueOnce(result('NEW'));
    mocks.useQuery.mockReturnValue(
      specData({
        dag: { name: 'example', steps: [{ name: 'extract' }], env: ['NEW=1'] },
      })
    );
    view.rerender(specTree({ keyboardActive: true }));
    await settle();

    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(visual.props?.validation).toMatchObject({
      referenceScope: { env: ['NEW'] },
    });
  });

  it('gives the reference scope to the YAML completion', async () => {
    const referenceScope = {
      params: [{ name: 'region', reference: '${params.region}' }],
      consts: [],
      env: [],
      secrets: [],
      steps: [],
    };
    mocks.post.mockResolvedValue({
      data: { valid: true, errors: [], issues: [], referenceScope },
    });
    await renderSpec();
    await settle();

    const state = mocks.editorProps.current.referenceState?.();
    expect(state?.scope).toEqual(referenceScope);
    expect(state?.model?.text).toBe(savedSpec);
  });

  it('gives a read-only editor to readers', async () => {
    mocks.canWrite = false;
    await renderSpec();

    expect(visual.props?.editable).toBe(false);
    expect(screen.getByLabelText('DAG spec')).toHaveAttribute('readonly');
    expect(
      screen.queryByRole('button', { name: /^save$/i })
    ).not.toBeInTheDocument();
  });

  // The YAML drawer marks a read-only file with the lock of the YAML panel.
  it('marks the YAML drawer of a reader with a lock, and not a writer', async () => {
    await renderSpec();
    expect(screen.queryByTestId('readonly-marker')).toBeNull();
    cleanup();

    mocks.canWrite = false;
    await renderSpec();
    expect(screen.getByTestId('readonly-marker')).toHaveAccessibleName(
      'Read-only'
    );
  });

  // parity:test-ve-save-draft (save diff)
  it('shows the YAML diff before it saves', async () => {
    await renderSpec();
    fireEvent.click(screen.getByText('Visual edit'));

    fireEvent.click(screen.getByRole('button', { name: /^save$/i }));
    await settle();
    const review = screen.getByRole('dialog', { name: 'Review changes' });
    expect(within(review).getByTestId('diff-edited')).toHaveTextContent(
      '# visual'
    );
    expect(mocks.put).not.toHaveBeenCalled();

    await act(async () => {
      fireEvent.click(within(review).getByText('Confirm save'));
    });
    expect(mocks.put).toHaveBeenCalledOnce();
    expect(mocks.put.mock.calls[0]?.[1].body.spec).toBe(
      savedSpec + '# visual\n'
    );
    expect(
      screen.queryByRole('dialog', { name: 'Review changes' })
    ).not.toBeInTheDocument();
  });

  // In the graph zone the node view (z-60) is over the page: the diff goes above it.
  it('raises the diff above the node view in the graph zone', async () => {
    render(
      <PanelPlacementProvider value="zone">
        {specTree({})}
      </PanelPlacementProvider>
    );
    await settle();
    fireEvent.click(screen.getByText('Visual edit'));
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }));
    await settle();
    expect(screen.getByRole('dialog', { name: 'Review changes' })).toHaveClass(
      'z-[80]'
    );
  });

  it('keeps the diff on the default layer outside the graph zone', async () => {
    await renderSpec();
    fireEvent.click(screen.getByText('Visual edit'));
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }));
    await settle();
    expect(screen.getByRole('dialog', { name: 'Review changes' })).toHaveClass(
      'z-50'
    );
  });

  it('opens the diff on Ctrl+S and saves nothing on cancel', async () => {
    await renderSpec();
    fireEvent.click(screen.getByText('Visual edit'));

    fireEvent.keyDown(document, { key: 's', ctrlKey: true });
    await settle();
    fireEvent.click(screen.getByText('Cancel review'));

    expect(mocks.put).not.toHaveBeenCalled();
    expect(
      screen.queryByRole('dialog', { name: 'Review changes' })
    ).not.toBeInTheDocument();
  });

  // Ctrl+S in a form field of a clean document saves what the field holds,
  // which reaches the text only on blur.
  it('takes the text of the focused form field on Ctrl+S', async () => {
    await renderSpec();
    const field = screen.getByLabelText('Form field');
    field.focus();
    fireEvent.change(field, { target: { value: 'typed' } });

    fireEvent.keyDown(field, { key: 's', ctrlKey: true });
    await settle();

    expect(screen.getByTestId('diff-edited')).toHaveTextContent('# typed');
  });

  it('gives the visual editor the harnesses of the document', async () => {
    mocks.useQuery.mockReturnValue(
      specData({
        spec: 'harnesses:\n  team-agent:\n    binary: team-agent\nsteps:\n  - action: harness.run\n    with:\n      prompt: Write tests\n',
      })
    );
    await renderSpec();

    expect(visual.props?.harnesses).toEqual(['team-agent']);
  });

  // An agent DAG is edited on the same canvas as every other DAG.
  it('shows an agent DAG in the visual editor, like any other', async () => {
    mocks.useQuery.mockReturnValue(
      specData({
        dag: { name: 'example', type: 'agent', steps: [{ name: 'plan' }] },
      })
    );
    await renderSpec();

    expect(screen.getByTestId('visual-editor-mock')).toBeInTheDocument();
    expect(visual.props?.editable).toBe(true);
    // Its tasks and actions are in the workflow panel.
    expect(screen.getByTestId('preview-graph')).toHaveTextContent('plan');
    fireEvent.click(screen.getByText('Toggle YAML'));
    expect(screen.getByLabelText('DAG spec')).toBeVisible();
  });

  it('validates the saved spec of an agent DAG for the canvas', async () => {
    mocks.useQuery.mockReturnValue(
      specData({ dag: { name: 'example', type: 'agent', steps: [] } })
    );
    await renderSpec();
    await settle();

    expect(mocks.post).toHaveBeenCalledWith('/dags/validate', {
      params: { query: { remoteNode: 'local' } },
      body: { spec: savedSpec, name: 'example.yaml' },
    });
  });

  // parity:spec-document-selector
  describe('files with several documents', () => {
    const localDags = [
      {
        name: 'child',
        dag: { name: 'child', steps: [{ name: 'load' }] },
        errors: [],
      },
    ];

    it('offers the parent and the sub-DAGs as tabs', async () => {
      await renderSpec({ localDags });

      const tabs = within(
        screen.getByRole('tablist', { name: 'Document' })
      ).getAllByRole('tab');
      expect(tabs.map((tab) => tab.textContent)).toEqual(['example', 'child']);
      expect(tabs[0]).toHaveAttribute('aria-selected', 'true');
      expect(screen.getByTestId('visual-editor-mock')).toBeInTheDocument();
    });

    it('shows a sub-DAG as a read-only canvas, with its step details', async () => {
      await renderSpec({ localDags });

      fireEvent.click(screen.getByRole('tab', { name: 'child' }));
      await settle();

      expect(
        screen.queryByTestId('visual-editor-mock')
      ).not.toBeInTheDocument();
      expect(screen.getByTestId('details-canvas')).toHaveTextContent('load');
      // The same tabs and the YAML button stay in the canvas toolbar.
      expect(screen.getByRole('tab', { name: 'child' })).toHaveAttribute(
        'aria-selected',
        'true'
      );
      fireEvent.click(screen.getByText('Pick load'));
      expect(screen.getByTestId('step-drawer')).toHaveTextContent('load');
    });

    // The controls do not jump when a sub-DAG is picked: the tabs first,
    // the actions last, as for the parent.
    it('keeps the toolbar order of the parent in a sub-DAG', async () => {
      await renderSpec({ localDags });
      fireEvent.click(screen.getByRole('tab', { name: 'child' }));
      await settle();

      const toolbar = within(screen.getByTestId('details-canvas'));
      const order = [
        toolbar.getByRole('tablist', { name: 'Document' }),
        toolbar.getByRole('button', { name: 'YAML' }),
        toolbar.getByRole('button', { name: 'Save' }),
      ];
      order.slice(1).forEach((control, index) => {
        expect(
          order[index]!.compareDocumentPosition(control) &
            Node.DOCUMENT_POSITION_FOLLOWING
        ).toBeTruthy();
      });
    });

    // The YAML of the drawer is the whole file: its edits need Save here.
    it('keeps Save in the toolbar of a sub-DAG, and Copy in the YAML panel', async () => {
      await renderSpec({ localDags });
      fireEvent.click(screen.getByRole('tab', { name: 'child' }));
      await settle();
      const toolbar = screen.getByTestId('details-canvas');

      const save = within(toolbar).getByRole('button', { name: 'Save' });
      expect(
        within(toolbar).queryByRole('button', { name: /Copy YAML|Discard/ })
      ).not.toBeInTheDocument();
      expect(
        screen.getByRole('button', { name: 'Copy YAML', hidden: true })
      ).toBeInTheDocument();
      expect(save).toBeDisabled();

      fireEvent.change(screen.getByLabelText('DAG spec'), {
        target: { value: savedSpec + '# edited' },
      });
      expect(save).toBeEnabled();
    });

    // The editor of the parent comes back when the picked sub-DAG is
    // renamed or removed in the file.
    it.each([
      ['renamed', [{ ...localDags[0]!, name: 'renamed' }]],
      ['removed', []],
    ])('falls back to the parent when the sub-DAG is %s', async (_, next) => {
      const view = await renderSpec({ localDags });
      fireEvent.click(screen.getByRole('tab', { name: 'child' }));
      await settle();

      view.rerender(specTree({ localDags: next }));
      await settle();

      expect(screen.getByTestId('visual-editor-mock')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Save' })).toBeInTheDocument();
    });

    // The form of a step leaves with the visual editor of the parent.
    it('shows the YAML of the whole file after a switch from a step form', async () => {
      await renderSpec({ localDags });
      fireEvent.click(screen.getByText('Toggle YAML'));
      fireEvent.click(screen.getByText('Open form'));
      expect(screen.getByLabelText('DAG spec')).not.toBeVisible();

      fireEvent.click(screen.getByRole('tab', { name: 'child' }));
      await settle();

      expect(screen.getByLabelText('DAG spec')).toBeVisible();
      expect(
        screen.getByRole('button', { name: 'Format YAML' })
      ).toBeInTheDocument();
    });

    it('shows no tabs for a file with one document', async () => {
      await renderSpec();

      expect(
        screen.queryByRole('tablist', { name: 'Document' })
      ).not.toBeInTheDocument();
    });
  });
});

// parity:test-ve-save-draft (with the save diff and conflict tests above)
describe('DAGSpec draft recovery', () => {
  const draftKey = 'dagu:draft:local:example.yaml';

  it('restores an unsaved draft on request', async () => {
    const draftText = savedSpec + '# draft\n';
    localStorage.setItem(
      draftKey,
      JSON.stringify({ text: draftText, savedAt: Date.UTC(2026, 0, 2) })
    );
    await renderSpec();

    const banner = screen.getByRole('status', { name: 'Unsaved draft' });
    expect(banner).toHaveTextContent('Restore unsaved draft from');
    fireEvent.click(within(banner).getByRole('button', { name: 'Restore' }));

    expect(screen.getByLabelText('DAG spec')).toHaveValue(draftText);
    expect(
      screen.queryByRole('status', { name: 'Unsaved draft' })
    ).not.toBeInTheDocument();
  });

  it('discards an unsaved draft', async () => {
    localStorage.setItem(
      draftKey,
      JSON.stringify({ text: savedSpec + '# draft\n', savedAt: 1 })
    );
    await renderSpec();

    const banner = screen.getByRole('status', { name: 'Unsaved draft' });
    fireEvent.click(within(banner).getByRole('button', { name: 'Discard' }));

    expect(screen.getByLabelText('DAG spec')).toHaveValue(savedSpec);
    expect(localStorage.getItem(draftKey)).toBeNull();
  });

  it('offers no draft that equals the saved spec', async () => {
    localStorage.setItem(
      draftKey,
      JSON.stringify({ text: savedSpec, savedAt: 1 })
    );
    await renderSpec();

    expect(
      screen.queryByRole('status', { name: 'Unsaved draft' })
    ).not.toBeInTheDocument();
  });

  it('stores the hash of the saved spec with the draft', async () => {
    const sha = 'a'.repeat(64);
    mocks.useQuery.mockReturnValue(specData({ sha256: sha }));
    const view = await renderSpec();
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: savedSpec + '# draft\n' },
    });
    view.unmount();

    expect(JSON.parse(localStorage.getItem(draftKey) ?? '{}')).toMatchObject({
      text: savedSpec + '# draft\n',
      baseSha256: sha,
    });
  });

  it('stores no hash while a newer version waits in the conflict dialog', async () => {
    mocks.useQuery.mockReturnValue(specData({ sha256: 'a'.repeat(64) }));
    const view = await renderSpec();
    const editor = screen.getByLabelText('DAG spec');
    fireEvent.change(editor, { target: { value: savedSpec + '# mine\n' } });

    mocks.useQuery.mockReturnValue(
      specData({ spec: savedSpec + '# theirs\n', sha256: 'b'.repeat(64) })
    );
    fireEvent.change(editor, { target: { value: savedSpec + '# mine!\n' } });
    expect(screen.getByRole('dialog')).toHaveTextContent('External Changes');
    view.unmount();

    const stored = JSON.parse(localStorage.getItem(draftKey) ?? '{}');
    expect(stored.text).toBe(savedSpec + '# mine!\n');
    expect(stored).not.toHaveProperty('baseSha256');
  });

  // A draft of an older version would replace the newer one on save: the
  // restore asks first, as for a change on disk during an edit.
  describe('of an older version', () => {
    const draftText = savedSpec + '# draft\n';

    beforeEach(() => {
      localStorage.setItem(
        draftKey,
        JSON.stringify({
          text: draftText,
          savedAt: 1,
          baseSha256: 'a'.repeat(64),
        })
      );
      mocks.useQuery.mockReturnValue(specData({ sha256: 'b'.repeat(64) }));
    });

    const restore = () =>
      fireEvent.click(
        within(screen.getByRole('status', { name: 'Unsaved draft' })).getByRole(
          'button',
          { name: 'Restore' }
        )
      );

    it('restores the draft only when the newer version is ignored', async () => {
      await renderSpec();
      restore();

      expect(screen.getByRole('dialog')).toHaveTextContent('External Changes');
      expect(screen.getByLabelText('DAG spec')).toHaveValue(savedSpec);

      fireEvent.click(screen.getByRole('button', { name: 'Ignore' }));
      expect(screen.getByLabelText('DAG spec')).toHaveValue(draftText);
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    it('keeps the newer version and drops the draft on reload', async () => {
      await renderSpec();
      restore();
      fireEvent.click(screen.getByRole('button', { name: 'Discard & Reload' }));

      expect(screen.getByLabelText('DAG spec')).toHaveValue(savedSpec);
      expect(localStorage.getItem(draftKey)).toBeNull();
      expect(
        screen.queryByRole('status', { name: 'Unsaved draft' })
      ).not.toBeInTheDocument();
    });
  });
});

// A blank line, a trailing space or a line end style is no change of the
// document: Save, Revert and the unsaved state follow the document.
describe('DAGSpec semantic dirty state', () => {
  const save = () => screen.getByRole('button', { name: /^save$/i });

  it('keeps Save off after a blank line, a trailing space or CRLF', async () => {
    await renderSpec();
    const editor = screen.getByLabelText('DAG spec');

    for (const value of [
      savedSpec + '\n',
      savedSpec + '\n\n\n',
      savedSpec.replace('extract', 'extract   '),
      savedSpec.replace('steps:', '\nsteps:'),
      savedSpec.replace(/\n/g, '\r\n'),
    ]) {
      fireEvent.change(editor, { target: { value } });
      expect(save()).toBeDisabled();
      expect(visual.props?.isDirty).toBe(false);
    }
  });

  it('turns Save on for a comment or a value, and off again when it is undone', async () => {
    await renderSpec();
    const editor = screen.getByLabelText('DAG spec');

    fireEvent.change(editor, { target: { value: savedSpec + '# note\n' } });
    expect(save()).toBeEnabled();
    expect(visual.props?.isDirty).toBe(true);

    fireEvent.change(editor, { target: { value: savedSpec + '\n' } });
    expect(save()).toBeDisabled();

    fireEvent.change(editor, {
      target: { value: savedSpec.replace('echo hello', 'echo bye') },
    });
    expect(save()).toBeEnabled();
  });

  it('does not tell the host that the file changed', async () => {
    const states: boolean[] = [];
    await renderSpec({
      onEditorStateChange: (state) => states.push(state.dirty),
    });
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: savedSpec + '\n\n' },
    });
    expect(states.every((dirty) => !dirty)).toBe(true);
  });

  it('does not offer a draft that only differs by blank lines', async () => {
    localStorage.setItem(
      'dagu:draft:local:example.yaml',
      JSON.stringify({ text: savedSpec + '\n\n', savedAt: 1 })
    );
    await renderSpec();
    expect(
      screen.queryByRole('status', { name: 'Unsaved draft' })
    ).not.toBeInTheDocument();
  });
});

describe('DAGSpec text tools', () => {
  const messy =
    'steps:\n  - run: echo hello\n    name: extract\nname: example\n';
  const tool = (name: string) =>
    screen.getByRole('button', { name, hidden: true });

  it('formats the file with the canonical order, and the Save button follows', async () => {
    mocks.useQuery.mockReturnValue(specData({ spec: messy }));
    await renderSpec();

    fireEvent.click(tool('Format YAML'));

    expect(screen.getByLabelText('DAG spec')).toHaveValue(
      'name: example\n\nsteps:\n  - name: extract\n    run: echo hello\n'
    );
    expect(screen.getByRole('button', { name: /^save$/i })).toBeEnabled();
  });

  // Formatting would change nothing: the button is off, with the reason in
  // its title, and formats again after an edit.
  it('turns the format button off when the file is already formatted', async () => {
    mocks.useQuery.mockReturnValue(
      specData({ spec: 'name: example\n\nsteps:\n  - name: a\n    run: x\n' })
    );
    await renderSpec();

    expect(tool('Format YAML')).toBeDisabled();
    expect(tool('Format YAML')).toHaveAttribute(
      'title',
      'The YAML is already formatted'
    );
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: {
        value: 'name:   example\n\nsteps:\n  - name: a\n    run: x\n',
      },
    });
    await waitFor(() => expect(tool('Format YAML')).toBeEnabled());
  });

  it('does not format a text with a syntax error', async () => {
    await renderSpec();
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: 'steps:\n  - name: b\n   command: [unclosed' },
    });
    expect(tool('Format YAML')).toBeDisabled();
  });

  it('has the quick fixes of every step in one button', async () => {
    const issue = (index: number, name: string) => ({
      severity: 'error',
      source: 'build',
      message: NO_ID_MESSAGE,
      stepPath: `steps[${index}]`,
      stepName: name,
    });
    const spec =
      'steps:\n  - name: one\n    run: echo 1\n  - name: two\n    run: echo 2\n';
    mocks.useQuery.mockReturnValue(specData({ spec }));
    mocks.post.mockResolvedValue({
      data: {
        valid: false,
        errors: [NO_ID_MESSAGE],
        issues: [issue(0, 'one'), issue(1, 'two')],
        dag: { name: 'example', steps: [{ name: 'one' }, { name: 'two' }] },
      },
    });
    await renderSpec();
    await waitFor(() => expect(tool('Apply all quick fixes')).toBeEnabled());

    fireEvent.click(tool('Apply all quick fixes'));

    expect(screen.getByLabelText('DAG spec')).toHaveValue(
      'steps:\n  - name: one\n    id: one\n    run: echo 1\n  - name: two\n    id: two\n    run: echo 2\n'
    );
  });

  it('turns the wand off when no quick fix exists', async () => {
    await renderSpec();
    expect(tool('Apply all quick fixes')).toBeDisabled();
    expect(tool('Apply all quick fixes')).toHaveAttribute(
      'title',
      'No quick fix to apply'
    );
  });

  it('has no text tool for a reader', async () => {
    mocks.canWrite = false;
    await renderSpec();
    expect(
      screen.queryByRole('button', { name: 'Format YAML', hidden: true })
    ).not.toBeInTheDocument();
  });
});

describe('DAGSpec draft offer on the canvas', () => {
  it('gives the card to the visual editor, and puts nothing above it', async () => {
    localStorage.setItem(
      'dagu:draft:local:example.yaml',
      JSON.stringify({ text: savedSpec + '# draft\n', savedAt: 1 })
    );
    await renderSpec();

    expect(visual.props?.canvasOverlay).toBeTruthy();
    const card = screen.getByRole('status', { name: 'Unsaved draft' });
    expect(screen.getByTestId('visual-editor-mock')).toContainElement(card);
  });

  it('gives no card without a draft, or to a reader', async () => {
    await renderSpec();
    expect(visual.props?.canvasOverlay).toBeUndefined();
    cleanup();

    localStorage.setItem(
      'dagu:draft:local:example.yaml',
      JSON.stringify({ text: savedSpec + '# draft\n', savedAt: 1 })
    );
    mocks.canWrite = false;
    await renderSpec();
    expect(visual.props?.canvasOverlay).toBeUndefined();
  });
});

// parity:spec-live-sse
describe('DAGSpec live spec', () => {
  it('takes the spec from the DAG events and polls only without them', async () => {
    // DAGSpec is memoized and the stream state is a mock: a changed prop
    // renders it again, as an event of the real stream renders SpecLiveSync.
    let turn = 0;
    const eventTree = () => specTree({ keyboardActive: turn++ % 2 === 1 });
    const sha = 'a'.repeat(64);
    const mutate = vi.fn();
    mocks.useQuery.mockReturnValue({ ...specData({ sha256: sha }), mutate });
    sse.state = connected(null);
    const view = await renderSpec();
    // The spec query, among the other queries of the page.
    const lastQuery = () => {
      const calls = mocks.useQuery.mock.calls.filter(
        (call: unknown[]) => call[0] === '/dags/{fileName}/spec'
      );
      return calls[calls.length - 1] ?? [];
    };

    expect(sse.args).toEqual(['example.yaml', true, 'local']);
    expect(lastQuery()[0]).toBe('/dags/{fileName}/spec');
    expect(lastQuery()[2]).toMatchObject({
      refreshInterval: 0,
      revalidateIfStale: false,
      revalidateOnFocus: false,
    });

    // An event goes into the cache without a request. The hash stays only
    // while the text is the same.
    const dag = { name: 'example', steps: [{ name: 'extract' }] };
    sse.state = connected({ dag, errors: [], spec: savedSpec });
    view.rerender(eventTree());
    expect(mutate).toHaveBeenLastCalledWith(
      {
        dag,
        errors: [],
        warnings: [],
        valueReferenceNotices: [],
        spec: savedSpec,
        sha256: sha,
      },
      { revalidate: false }
    );
    const next = savedSpec + '# from disk\n';
    sse.state = connected({ dag, errors: ['e'], warnings: ['w'], spec: next });
    view.rerender(eventTree());
    expect(mutate).toHaveBeenLastCalledWith(
      {
        dag,
        errors: ['e'],
        warnings: ['w'],
        valueReferenceNotices: [],
        spec: next,
        sha256: undefined,
      },
      { revalidate: false }
    );

    // An event without the spec leaves the cache as it is.
    mutate.mockClear();
    sse.state = connected({ dag, errors: [] });
    view.rerender(eventTree());
    expect(mutate).not.toHaveBeenCalled();

    // Without the stream, the spec is polled every 2 s.
    sse.state = {
      ...connected(null),
      isConnected: false,
      shouldUseFallback: true,
    };
    view.rerender(eventTree());
    expect(lastQuery()[2]).toMatchObject({
      refreshInterval: 2000,
      revalidateIfStale: true,
      revalidateOnFocus: true,
    });
    sse.state = { ...connected(null), isConnected: false };
    view.rerender(eventTree());
    expect(lastQuery()[2]).toMatchObject({ refreshInterval: 2000 });
    expect(mutate).not.toHaveBeenCalled();
  });
});
