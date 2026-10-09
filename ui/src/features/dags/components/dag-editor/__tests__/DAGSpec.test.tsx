// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { DAGContext } from '../../../contexts/DAGContext';
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
    editorProps: { current: {} as { markers?: unknown[] } },
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

vi.mock('react-cookie', () => ({
  useCookies: () => [{}, vi.fn()],
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

vi.mock('../../../../../hooks/useDAGSSE', () => ({
  useDAGSSE: () => null,
}));

vi.mock('../../../../../hooks/useSSECacheSync', () => ({
  sseFallbackOptions: () => ({}),
  useSSECacheSync: vi.fn(),
}));

vi.mock('@/features/dags/components/step-details', () => ({
  StepDetailsDrawer: () => null,
}));

vi.mock('../DAGAttributes', () => ({ default: () => null }));
vi.mock('../AgentSpecOverview', () => ({
  AgentSpecOverview: () => <div>Agent overview</div>,
}));
vi.mock('../ExternalChangeDialog', () => ({
  default: ({ visible }: { visible: boolean }) =>
    visible ? <div role="dialog">External Changes Detected</div> : null,
}));
vi.mock('../../dag-details', () => ({ DAGStepTable: () => null }));
vi.mock('../../value-reference-notices', () => ({
  ValueReferenceNoticesButton: () => null,
}));

vi.mock('../../visualization', () => ({
  FlowchartType: {},
  Graph: ({
    steps,
    height,
  }: {
    steps?: { name: string }[];
    height?: string | number;
  }) => (
    <div data-testid="preview-graph" data-height={height ?? 'auto'}>
      {steps?.map((step) => step.name).join(',')}
    </div>
  ),
}));

vi.mock('../DAGEditorWithDocs', () => ({
  default: function MockEditor(props: {
    value: string;
    onChange?: (value?: string) => void;
    readOnly?: boolean;
    markers?: unknown[];
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

// jsdom has no layout, so the spec tab reports `specWidth` and resizes only
// when a test calls resizeSpec.
let specWidth = 1600;
const resizeListeners = new Set<() => void>();

class WidthObserver {
  private readonly notify: () => void;

  constructor(callback: ResizeObserverCallback) {
    this.notify = () =>
      callback(
        [{ contentRect: { width: specWidth } } as ResizeObserverEntry],
        this as unknown as ResizeObserver
      );
  }

  observe() {
    resizeListeners.add(this.notify);
  }

  unobserve() {}

  disconnect() {
    resizeListeners.delete(this.notify);
  }
}

function resizeSpec(width: number) {
  specWidth = width;
  act(() => resizeListeners.forEach((notify) => notify()));
}

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

function renderSpec(props: Partial<React.ComponentProps<typeof DAGSpec>> = {}) {
  return render(
    <UserPreferencesProvider>
      <AppBarContext.Provider value={appBarValue}>
        <DAGContext.Provider
          value={{
            refresh: vi.fn(),
            name: 'example',
            fileName: 'example.yaml',
          }}
        >
          <DAGSpec fileName="example.yaml" {...props} />
        </DAGContext.Provider>
      </AppBarContext.Provider>
    </UserPreferencesProvider>
  );
}

function viewButton(name: string) {
  return within(screen.getByRole('group', { name: 'View mode' })).getByRole(
    'button',
    { name }
  );
}

function storedSpecView(): unknown {
  return JSON.parse(localStorage.getItem('user_preferences') ?? '{}')
    .specViewMode;
}

beforeEach(() => {
  localStorage.clear();
  specWidth = 1600;
  mocks.editorMounts = 0;
  mocks.canWrite = true;
  vi.stubGlobal('ResizeObserver', WidthObserver);
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(
    () => ({ width: specWidth }) as DOMRect
  );
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
  it('shows saved warnings separately from errors', () => {
    mocks.useQuery.mockReturnValue(
      specData({
        warnings: ['Harness step review has no explicit working_dir'],
      })
    );
    renderSpec();

    expect(screen.getByRole('status')).toHaveTextContent('Warnings');
    expect(screen.getByRole('status')).toHaveTextContent('working_dir');
    expect(screen.getByTestId('preview-graph')).toHaveTextContent('extract');
  });

  it('allows saving a warning-only spec and clears corrected warnings', async () => {
    vi.useFakeTimers();
    mocks.post.mockResolvedValueOnce({
      data: {
        valid: true,
        errors: [],
        warnings: ['Harness step review has no explicit working_dir'],
      },
    });
    renderSpec();
    const editor = screen.getByLabelText('DAG spec');
    fireEvent.change(editor, { target: { value: savedSpec + '# edited' } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });

    expect(screen.getByText('Valid with warnings')).toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('working_dir');
    expect(mocks.editorProps.current.markers).toEqual([]);
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /save/i }));
    });
    expect(mocks.put).toHaveBeenCalledOnce();
    expect(mocks.showError).not.toHaveBeenCalled();
    expect(mocks.showToast).toHaveBeenCalledWith('Changes saved successfully');

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
    renderSpec();
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
    renderSpec();
    const editor = screen.getByLabelText('DAG spec');
    const submitted = savedSpec + '# saved';
    const newerEdit = submitted + '\n# still editing';
    fireEvent.change(editor, { target: { value: submitted } });
    fireEvent.click(screen.getByRole('button', { name: /save/i }));
    fireEvent.change(editor, { target: { value: submitted + '\n# next' } });
    const saveButton = screen.getByRole('button', { name: /save/i });
    expect(saveButton).toBeDisabled();
    fireEvent.click(saveButton);
    fireEvent.keyDown(document, { key: 's', ctrlKey: true });
    expect(mocks.put).toHaveBeenCalledOnce();

    mocks.useQuery.mockReturnValue(specData({ spec: submitted }));
    fireEvent.change(editor, { target: { value: newerEdit } });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

    await act(async () => {
      finishSave({ data: { errors: [] } });
    });
    expect(editor).toHaveValue(newerEdit);
    expect(screen.getByRole('button', { name: /save/i })).toBeEnabled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it.each([
    ['request failure', { error: { message: 'unavailable' } }],
    ['validation rejection', { data: { errors: ['invalid spec'] } }],
  ])('detects external changes after a save %s', async (_name, response) => {
    vi.useFakeTimers();
    mocks.put.mockResolvedValueOnce(response);
    renderSpec();
    const editor = screen.getByLabelText('DAG spec');
    const submitted = savedSpec + '# submitted';
    fireEvent.change(editor, { target: { value: submitted } });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /save/i }));
    });
    expect(mocks.showError).toHaveBeenCalled();
    expect(screen.getByRole('button', { name: /save/i })).toBeEnabled();

    mocks.useQuery.mockReturnValue(specData({ spec: submitted }));
    fireEvent.change(editor, {
      target: { value: submitted + '\n# still editing' },
    });
    expect(screen.getByRole('dialog')).toHaveTextContent('External Changes');
  });

  it('allows retrying a save after a network exception', async () => {
    vi.useFakeTimers();
    mocks.put.mockRejectedValueOnce(new Error('network unavailable'));
    renderSpec();
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: savedSpec + '# edited' },
    });
    const saveButton = screen.getByRole('button', { name: /save/i });
    await act(async () => {
      fireEvent.click(saveButton);
    });
    expect(mocks.showError).toHaveBeenCalledWith(
      'Failed to save spec',
      'Please try again.'
    );
    expect(saveButton).toBeEnabled();
    await act(async () => {
      fireEvent.click(saveButton);
    });
    expect(mocks.showToast).toHaveBeenCalledWith('Changes saved successfully');
    expect(saveButton).toBeDisabled();
  });

  it('validates the edited buffer once per idle window', async () => {
    vi.useFakeTimers();
    renderSpec();

    const editor = screen.getByLabelText('DAG spec');
    fireEvent.change(editor, { target: { value: 'steps: [' } });
    fireEvent.change(editor, { target: { value: 'steps: [broken' } });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.post).toHaveBeenCalledWith('/dags/validate', {
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
        dag: { name: 'example', steps: [{ name: 'extract' }, { name: 'load' }] },
      },
    });

    renderSpec();
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: 'steps: [broken' },
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });

    expect(
      screen.getByText(/mapping values are not allowed/)
    ).toBeInTheDocument();
    expect(screen.getByText(/has invalid keys: nosuchfield/)).toBeInTheDocument();
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

  it('clears previous validation results when a validate request fails', async () => {
    vi.useFakeTimers();
    mocks.post.mockResolvedValueOnce({
      data: {
        valid: false,
        errors: ['[3:1] mapping values are not allowed in this context'],
        dag: undefined,
      },
    });

    renderSpec();
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

  it('renders the saved graph alongside saved errors', () => {
    mocks.useQuery.mockReturnValue(
      specData({ errors: ['something is misconfigured'] })
    );

    renderSpec();

    expect(screen.getByText('something is misconfigured')).toBeInTheDocument();
    expect(screen.getByTestId('preview-graph')).toHaveTextContent('extract');
  });
});

describe('DAGSpec views', () => {
  it('opens with the graph beside the editor on wide screens', () => {
    renderSpec();

    expect(viewButton('Split')).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByTestId('preview-graph')).toHaveAttribute(
      'data-height',
      '100%'
    );
    expect(screen.getByLabelText('DAG spec')).toBeVisible();
  });

  it('remembers the chosen view', () => {
    const { unmount } = renderSpec();

    fireEvent.click(viewButton('YAML'));

    expect(screen.queryByTestId('preview-graph')).not.toBeInTheDocument();
    expect(screen.getByLabelText('DAG spec')).toBeVisible();
    expect(storedSpecView()).toBe('yaml');
    unmount();
    renderSpec();
    expect(viewButton('YAML')).toHaveAttribute('aria-pressed', 'true');
  });

  // Remounting the editor would drop its undo history and cursor.
  it('keeps unsaved edits in the same editor across views', () => {
    renderSpec();
    const edited = savedSpec + '# edited';
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: edited },
    });

    fireEvent.click(viewButton('Graph'));
    expect(screen.getByLabelText('DAG spec')).not.toBeVisible();
    fireEvent.click(viewButton('YAML'));
    fireEvent.click(viewButton('Split'));

    expect(screen.getByLabelText('DAG spec')).toHaveValue(edited);
    expect(mocks.editorMounts).toBe(1);
  });

  it('saves from the graph view', async () => {
    renderSpec();
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: savedSpec + '# edited' },
    });
    fireEvent.click(viewButton('Graph'));

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /save/i }));
    });

    expect(mocks.put).toHaveBeenCalledOnce();
  });

  it('shows the graph view where the editor does not fit beside it', () => {
    specWidth = 800;
    renderSpec();

    expect(
      within(screen.getByRole('group', { name: 'View mode' })).queryByRole(
        'button',
        { name: 'Split' }
      )
    ).not.toBeInTheDocument();
    expect(viewButton('Graph')).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByLabelText('DAG spec')).not.toBeVisible();

    resizeSpec(1600);
    expect(viewButton('Split')).toHaveAttribute('aria-pressed', 'true');
  });

  // Messages above the editor would push it down while typing.
  it('shows validation messages below the editor in the yaml view', () => {
    mocks.useQuery.mockReturnValue(
      specData({
        warnings: ['Harness step review has no explicit working_dir'],
        errors: ['something is misconfigured'],
      })
    );
    renderSpec();

    fireEvent.click(viewButton('YAML'));

    const editor = screen.getByLabelText('DAG spec');
    for (const message of [
      screen.getByRole('status'),
      screen.getByText('something is misconfigured'),
    ]) {
      expect(
        editor.compareDocumentPosition(message) &
          Node.DOCUMENT_POSITION_FOLLOWING
      ).toBeTruthy();
    }
  });

  it('previews agent DAGs as an overview without a split view', () => {
    mocks.useQuery.mockReturnValue(
      specData({ dag: { name: 'example', type: 'agent', steps: [] } })
    );
    renderSpec();

    expect(viewButton('Overview')).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByText('Agent overview')).toBeInTheDocument();
    expect(
      within(screen.getByRole('group', { name: 'View mode' })).queryByRole(
        'button',
        { name: 'Split' }
      )
    ).not.toBeInTheDocument();
  });

  it('lets read-only users switch views without editing', () => {
    mocks.canWrite = false;
    renderSpec();

    fireEvent.click(viewButton('YAML'));

    expect(screen.getByLabelText('DAG spec')).toHaveAttribute('readonly');
    expect(
      screen.queryByRole('button', { name: /save/i })
    ).not.toBeInTheDocument();
  });

  // The page header shows the parent's description but not a local DAG's.
  it('describes a local DAG in its graph preview', () => {
    mocks.useQuery.mockReturnValue(
      specData({
        dag: {
          name: 'example',
          description: 'Parent summary',
          steps: [{ name: 'extract' }],
        },
      })
    );
    renderSpec({
      localDags: [
        {
          name: 'child',
          dag: {
            name: 'child',
            description: 'Child summary',
            steps: [{ name: 'load' }],
          },
          errors: [],
        },
      ],
    });
    fireEvent.click(viewButton('Graph'));

    expect(screen.queryByText('Parent summary')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'child' }));
    expect(screen.getByText('Child summary')).toBeInTheDocument();
  });
});
