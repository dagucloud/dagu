// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { DAGContext } from '../../../contexts/DAGContext';
import DAGSpec from '../DAGSpec';
import type {
  EditorNotice,
  VisualEditorHandle,
  VisualEditorState,
  WorkspaceNotice,
} from '../hostTypes';

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
  };
});

vi.mock('@/hooks/api', () => ({
  useClient: () => mocks.client,
  useQuery: mocks.useQuery,
}));
vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteForWorkspace: () => true,
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
vi.mock('@/features/dags/components/step-details', () => ({
  StepDetailsDrawer: () => null,
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
  ValueReferenceNoticesButton: (props: {
    notices: unknown[];
    label?: string;
  }) => (
    <button type="button">
      {props.label ?? 'Notices'} of {props.notices.length}
    </button>
  ),
}));
vi.mock('../WorkflowPanel', () => ({ WorkflowPanel: () => null }));
vi.mock('@/features/visual-editor/DetailsCanvas', () => ({
  default: (props: {
    toolbarEnd?: React.ReactNode;
    toolbarStart?: React.ReactNode;
    toolbarTrail?: React.ReactNode;
  }) => (
    <div data-testid="details-canvas">
      {props.toolbarStart}
      {props.toolbarEnd}
      {props.toolbarTrail}
    </div>
  ),
}));
// The editor object that the panel moves to the line of an issue.
const editor = vi.hoisted(() => ({
  setSelection: vi.fn(),
  revealLineInCenter: vi.fn(),
  focus: vi.fn(),
  baseline: undefined as string | undefined,
}));
vi.mock('../DAGEditorWithDocs', () => ({
  default: function EditorDouble(props: {
    value: string;
    onChange?: (value?: string) => void;
    onEditorMount?: (editor: unknown) => void;
    changeBaseline?: string;
  }) {
    editor.baseline = props.changeBaseline;
    const mount = React.useRef(props.onEditorMount);
    React.useEffect(() => {
      mount.current?.({
        setSelection: editor.setSelection,
        revealLineInCenter: editor.revealLineInCenter,
        focus: editor.focus,
        getModel: () => ({ getLineMaxColumn: () => 40 }),
      });
    }, []);
    return (
      <textarea
        aria-label="DAG spec"
        value={props.value}
        onChange={(event) => props.onChange?.(event.target.value)}
      />
    );
  },
}));

type VisualProps = {
  text: string;
  validation?: {
    dag?: { steps?: { name: string }[] };
    referenceScope?: { steps: { name: string }[] };
  };
  toolbarStart?: React.ReactNode;
  toolbarTrail?: React.ReactNode;
  canvasOverlay?: React.ReactNode;
  onNotice?(notice: EditorNotice | null): void;
  onStateChange?(state: VisualEditorState): void;
  onYamlVisibleChange?(visible: boolean, source?: 'panel'): void;
  onYamlModeChange?(mode: 'file' | 'step'): void;
  handleRef?: React.Ref<VisualEditorHandle>;
};
const visual = vi.hoisted(() => ({
  props: null as VisualProps | null,
  revealStep: null as ((name: string) => boolean) | null,
  toggleYaml: vi.fn(),
}));

vi.mock('@/features/visual-editor', () => ({
  default: function MockVisualEditor(props: VisualProps) {
    visual.props = props;
    React.useImperativeHandle(
      props.handleRef,
      () =>
        ({
          revealStep: (name: string) => visual.revealStep?.(name) ?? false,
          toggleYaml: (open?: boolean) => visual.toggleYaml(open),
          openForm: () => false,
          openYaml: () => false,
        }) as unknown as VisualEditorHandle,
      []
    );
    return (
      <div data-testid="visual-editor-mock">
        {props.toolbarStart}
        {props.toolbarTrail}
        {props.canvasOverlay}
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

const savedSpec = 'steps:\n  - name: extract\n    run: echo hello\n';
const COMPACT = { compactNotices: true };
const FROZEN_TEXT = 'YAML has errors: the canvas shows the last valid version';

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

function visualState(frozen: boolean): VisualEditorState {
  return {
    frozen,
    canUndo: false,
    canRedo: false,
    selectedStepName: null,
    drawer: 'none',
    direction: 'TB',
  };
}

async function settle() {
  await act(async () => {
    for (let i = 0; i < 8; i++) {
      await Promise.resolve();
    }
  });
}

async function renderSpec(
  props: Partial<React.ComponentProps<typeof DAGSpec>> = {}
) {
  const view = render(
    <UserPreferencesProvider>
      <AppBarContext.Provider value={appBarValue}>
        <DAGContext.Provider
          value={{
            refresh: vi.fn(),
            name: 'example',
            fileName: 'example.yaml',
          }}
        >
          <DAGSpec fileName="example.yaml" chrome={COMPACT} {...props} />
        </DAGContext.Provider>
      </AppBarContext.Provider>
    </UserPreferencesProvider>
  );
  await settle();
  return view;
}

/** Opens the Issues popover of the given toolbar. */
function openIssues(toolbar: HTMLElement) {
  fireEvent.click(
    within(toolbar).getByRole('button', { name: /^Issues \(\d+\)$/ })
  );
  return screen.getByRole('dialog', { name: 'Issues' });
}

beforeEach(() => {
  localStorage.clear();
  editor.baseline = undefined;
  visual.props = null;
  visual.revealStep = vi.fn(() => true);
  mocks.useQuery.mockReturnValue(specData());
  mocks.post.mockResolvedValue({
    data: { valid: true, errors: [], dag: undefined },
  });
  mocks.put.mockResolvedValue({ data: { errors: [] } });
});

afterEach(() => {
  vi.useRealTimers();
  vi.clearAllMocks();
});

describe('DAGSpec: a draft that does not build', () => {
  // The backend gives a DAG without steps and an empty scope for a draft
  // with a build error. The views of the step keep the saved ones.
  it('keeps the saved DAG and reference scope for the visual editor', async () => {
    vi.useFakeTimers();
    const saved = {
      valid: true,
      errors: [],
      dag: { name: 'example', steps: [{ name: 'extract' }] },
      issues: [],
      referenceScope: { steps: [{ name: 'extract' }] },
    };
    mocks.post.mockResolvedValueOnce({ data: saved });
    mocks.post.mockResolvedValueOnce({
      data: {
        valid: false,
        errors: ['with.filter is required'],
        dag: { name: 'example', steps: [] },
        issues: [
          {
            severity: ValidationIssueSeverity.error,
            source: ValidationIssueSource.build,
            message: 'with.filter is required',
            stepName: 'extract',
          },
        ],
        referenceScope: { steps: [] },
      },
    });
    await renderSpec();
    expect(visual.props?.validation?.dag?.steps).toHaveLength(1);

    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: savedSpec + '# edited' },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });

    expect(visual.props?.validation?.dag?.steps).toHaveLength(1);
    expect(visual.props?.validation?.referenceScope?.steps).toHaveLength(1);
  });
});

// parity:spec-warnings-errors-banner
// parity:dup-16
describe('DAGSpec compact notices: issues', () => {
  it('puts the saved warnings and errors in the Issues popover', async () => {
    mocks.useQuery.mockReturnValue(
      specData({ warnings: ['Saved warning'], errors: ['Saved error'] })
    );
    await renderSpec();

    // No banner over the canvas.
    for (const status of screen.getAllByRole('status')) {
      expect(status).not.toHaveTextContent('Saved warning');
    }
    expect(screen.getByRole('status')).toHaveTextContent('2 issues');

    const panel = openIssues(screen.getByTestId('visual-editor-mock'));
    expect(
      within(panel).getByRole('region', { name: 'Warnings (1)' })
    ).toHaveTextContent('Saved warning');
    expect(
      within(panel).getByRole('region', { name: 'Errors (1)' })
    ).toHaveTextContent('Saved error');
  });

  it('lists the live warnings and errors while the text is dirty', async () => {
    vi.useFakeTimers();
    mocks.useQuery.mockReturnValue(specData({ warnings: ['Saved warning'] }));
    mocks.post.mockResolvedValueOnce({ data: { valid: true, errors: [] } });
    mocks.post.mockResolvedValueOnce({
      data: {
        valid: false,
        errors: ['Live error'],
        warnings: ['Live warning'],
      },
    });
    await renderSpec();
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: savedSpec + '# edited' },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });

    const panel = openIssues(screen.getByTestId('visual-editor-mock'));
    expect(panel).toHaveTextContent('Live warning');
    expect(panel).toHaveTextContent('Live error');
    expect(panel).not.toHaveTextContent('Saved warning');
  });

  it('reveals the step of a row in the editor', async () => {
    mocks.useQuery.mockReturnValue(specData({ errors: ['Bad field'] }));
    mocks.post.mockResolvedValue({
      data: {
        valid: false,
        errors: ['Bad field'],
        issues: [
          {
            severity: ValidationIssueSeverity.error,
            source: ValidationIssueSource.build,
            message: 'Bad field',
            stepName: 'extract',
          },
        ],
      },
    });
    await renderSpec();
    await settle();

    const panel = openIssues(screen.getByTestId('visual-editor-mock'));
    fireEvent.click(within(panel).getByRole('button', { name: /Bad field/ }));
    expect(visual.revealStep).toHaveBeenCalledWith('extract');
  });

  // parity:spec-value-reference-notices
  it('moves the value-reference notices into the popover', async () => {
    mocks.useQuery.mockReturnValue(
      specData({
        errors: ['An error'],
        valueReferenceNotices: [{ message: 'Unknown step id fetch' }],
      })
    );
    await renderSpec();

    const toolbar = screen.getByTestId('visual-editor-mock');
    expect(
      within(toolbar).queryByRole('button', { name: /Notices of/ })
    ).not.toBeInTheDocument();
    const panel = openIssues(toolbar);
    const group = within(panel).getByRole('region', {
      name: 'Value references (1)',
    });
    expect(group).toHaveTextContent('Unknown step id fetch');
    expect(
      within(group).getByRole('button', { name: 'Details of 1' })
    ).toBeInTheDocument();
  });

  it('shows only the errors of a selected sub-DAG', async () => {
    mocks.useQuery.mockReturnValue(specData({ warnings: ['Parent warning'] }));
    await renderSpec({
      localDags: [
        {
          name: 'child',
          dag: { name: 'child', steps: [{ name: 'load' }] },
          errors: ['Child error'],
        },
      ] as React.ComponentProps<typeof DAGSpec>['localDags'],
    });

    fireEvent.click(screen.getByRole('tab', { name: 'child' }));
    await settle();

    const panel = openIssues(screen.getByTestId('details-canvas'));
    expect(panel).toHaveTextContent('Child error');
    expect(
      within(panel).queryByRole('region', { name: /Warnings/ })
    ).not.toBeInTheDocument();
  });

  it('keeps the errors of a sub-DAG that has no canvas', async () => {
    await renderSpec({
      localDags: [
        { name: 'broken', errors: ['Broken child'] },
      ] as React.ComponentProps<typeof DAGSpec>['localDags'],
    });

    fireEvent.click(screen.getByRole('tab', { name: 'broken' }));
    await settle();

    expect(screen.queryByTestId('details-canvas')).not.toBeInTheDocument();
    expect(screen.getByText('Broken child')).toBeInTheDocument();
  });
});

// The text with errors cannot be saved: the Issues button takes the place of
// Save, in the same slot. Warnings alone do not block.
describe('DAGSpec compact notices: Save and Issues', () => {
  const save = () => document.getElementById('save-config');

  async function dirtySpec(errors: string[], warnings: string[] = []) {
    vi.useFakeTimers();
    mocks.post.mockResolvedValue({
      data: { valid: errors.length === 0, errors, warnings },
    });
    await renderSpec();
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: savedSpec + '# edited' },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(700);
    });
  }

  it('shows Save and no slot while the text has no issue', async () => {
    await renderSpec();
    expect(save()).toBeInTheDocument();
    expect(screen.queryByTestId('issues-slot')).toBeNull();
  });

  it('replaces Save with the Issues button, with its count, when the text has errors', async () => {
    await dirtySpec(['First error', 'Second error']);

    expect(save()).toBeNull();
    const slot = screen.getByTestId('issues-slot');
    expect(slot).toHaveAccessibleName('Issues (2)');
    expect(slot).toHaveTextContent(/^2$/);
    expect(slot).toHaveClass('h-7');
    const toolbar = screen.getByTestId('visual-editor-mock');
    expect(Array.from(toolbar.querySelectorAll('button')).pop()).toBe(slot);
    const panel = openIssues(toolbar);
    expect(panel).toHaveTextContent('First error');
    expect(panel).toHaveTextContent('Second error');
  });

  // The owner asked that the two buttons take the same place: one box of a
  // fixed size, with the button aligned to its left.
  it('gives Save and the Issues button the same box', async () => {
    const box = ['h-7', 'w-14', 'justify-start'];
    await renderSpec();
    expect(screen.getByTestId('save-slot')).toHaveClass(...box);
    expect(screen.getByTestId('save-slot')).toContainElement(save());
    cleanup();

    await dirtySpec(['First error', 'Second error']);
    const blocked = screen.getByTestId('save-slot');
    expect(blocked).toHaveClass(...box);
    expect(blocked).toContainElement(screen.getByTestId('issues-slot'));
    expect(save()).toBeNull();
  });

  // The owner removed the separate Issues pill: the triangle that takes the
  // place of Save is the only trigger. Warnings alone count on Save.
  it('keeps Save with a warning count, and no Issues button, for warnings only', async () => {
    await dirtySpec([], ['A warning', 'Another warning']);

    expect(save()).toBeInTheDocument();
    expect(screen.queryByTestId('issues-slot')).toBeNull();
    expect(
      screen.queryByRole('button', { name: /^Issues \(/ })
    ).not.toBeInTheDocument();
    const badge = screen.getByTestId('save-warning-count');
    expect(badge).toHaveTextContent('2');
    expect(badge).toHaveClass('bg-warning', '-right-2', '-top-2');
    expect(save()).toContainElement(badge);
    expect(save()).toHaveAttribute('title', expect.stringContaining('2'));
  });

  it('shows no count on Save without a warning', async () => {
    await renderSpec();
    expect(screen.queryByTestId('save-warning-count')).toBeNull();
  });

  it('has one issues button only, in the place of Save, when there are errors', async () => {
    await dirtySpec(['An error'], ['A warning']);

    const toolbar = screen.getByTestId('visual-editor-mock');
    const triggers = within(toolbar).getAllByRole('button', {
      name: /^Issues \(\d+\)$/,
    });
    expect(triggers).toHaveLength(1);
    expect(screen.getByTestId('save-slot')).toContainElement(triggers[0]!);
    expect(screen.queryByTestId('save-warning-count')).toBeNull();
  });

  it('brings Save back when the errors are gone', async () => {
    await dirtySpec(['An error']);
    expect(save()).toBeNull();

    mocks.post.mockResolvedValue({
      data: { valid: true, errors: [], warnings: [] },
    });
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: savedSpec + '# fixed' },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(700);
    });
    expect(save()).toBeInTheDocument();
    expect(screen.queryByTestId('issues-slot')).toBeNull();
  });

  it('opens the Issues popover on Ctrl+S, and saves nothing', async () => {
    await dirtySpec(['An error']);
    expect(screen.queryByRole('dialog', { name: 'Issues' })).toBeNull();

    fireEvent.keyDown(document, { key: 's', ctrlKey: true });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10);
    });

    expect(screen.getByRole('dialog', { name: 'Issues' })).toBeVisible();
    expect(mocks.put).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog', { name: 'Review changes' })).toBeNull();
  });

  it('keeps Save on a page without the compact toolbar', async () => {
    mocks.useQuery.mockReturnValue(specData({ errors: ['Saved error'] }));
    await renderSpec({ chrome: undefined });
    expect(save()).toBeInTheDocument();
    expect(screen.queryByTestId('issues-slot')).toBeNull();
  });
});

// The server stops at its first error. The second pass reads the text, and
// every issue that it finds shows with the ones of the server.
describe('DAGSpec compact notices: all the issues', () => {
  const twoProblems =
    'steps:\n  - id: a\n    name: a\n    action: ""\n  - id: b\n    name: b\n    run: echo\n    depends: [ghost]\n';

  it('lists the issues that the server did not say, with their lines', async () => {
    mocks.useQuery.mockReturnValue(specData({ spec: twoProblems }));
    await renderSpec();

    const slot = await screen.findByTestId('issues-slot');
    expect(slot).toHaveAccessibleName('Issues (2)');
    const panel = openIssues(screen.getByTestId('visual-editor-mock'));
    const rows = within(panel).getAllByRole('button');
    // By line.
    expect(rows[0]).toHaveTextContent('Action is empty.');
    expect(rows[0]).toHaveTextContent('line 4');
    expect(rows[1]).toHaveTextContent('Depends on the unknown step "ghost".');
    expect(rows[1]).toHaveTextContent('line 8');
  });

  it('gives the merged issues to the visual editor, for the badges of its nodes', async () => {
    mocks.useQuery.mockReturnValue(specData({ spec: twoProblems }));
    await renderSpec();
    await screen.findByTestId('issues-slot');

    const issues = (
      visual.props?.validation as { issues?: { message: string }[] }
    ).issues;
    expect(issues?.map((issue) => issue.message)).toEqual([
      'Action is empty.',
      'Depends on the unknown step "ghost".',
    ]);
  });

  it('says an issue of the server once', async () => {
    mocks.useQuery.mockReturnValue(
      specData({
        spec: twoProblems,
        errors: ['step command is empty'],
      })
    );
    mocks.post.mockResolvedValue({
      data: {
        valid: false,
        errors: ['step command is empty'],
        issues: [
          {
            severity: ValidationIssueSeverity.error,
            source: ValidationIssueSource.build,
            message: 'step command is empty',
            stepName: 'a',
            stepPath: 'steps[0]',
          },
        ],
      },
    });
    await renderSpec();
    await screen.findByTestId('issues-slot');
    await settle();

    // The empty command of the server and the empty action are one issue.
    expect(screen.getByTestId('issues-slot')).toHaveAccessibleName(
      'Issues (2)'
    );
  });
});

// A row of the list moves the cursor of the YAML to its line, and shows the
// step of the issue on the canvas.
describe('DAGSpec compact notices: a row of the issues', () => {
  it('opens the YAML file at the line of an issue about the file', async () => {
    mocks.useQuery.mockReturnValue(
      specData({
        // The empty action is an error: with errors only, the button shows.
        spec: 'schedule: every day\nsteps:\n  - name: a\n    action: ""\n',
      })
    );
    await renderSpec();
    await screen.findByRole('button', { name: /^Issues \(\d+\)$/ });
    const panel = openIssues(screen.getByTestId('visual-editor-mock'));
    fireEvent.click(within(panel).getByRole('button', { name: /every day/ }));
    expect(visual.toggleYaml).toHaveBeenCalledWith(true);
  });

  it('only shows the step of a row about a step, with the YAML closed', async () => {
    mocks.useQuery.mockReturnValue(
      specData({
        spec: 'steps:\n  - name: a\n    action: ""\n',
      })
    );
    await renderSpec();
    const slot = await screen.findByTestId('issues-slot');
    fireEvent.click(slot);
    fireEvent.click(
      within(screen.getByRole('dialog', { name: 'Issues' })).getByRole(
        'button',
        { name: /Action is empty/ }
      )
    );
    expect(visual.revealStep).toHaveBeenCalledWith('a');
    expect(visual.toggleYaml).not.toHaveBeenCalled();
  });

  const twoProblems =
    'steps:\n  - id: a\n    name: a\n    action: ""\n  - id: b\n    name: b\n    run: echo\n    depends: [ghost]\n';

  it('puts the cursor of the YAML file on the line of the issue', async () => {
    mocks.useQuery.mockReturnValue(specData({ spec: twoProblems }));
    await renderSpec();
    await screen.findByTestId('issues-slot');
    act(() => visual.props?.onYamlVisibleChange?.(true, 'panel'));

    const panel = openIssues(screen.getByTestId('visual-editor-mock'));
    fireEvent.click(
      within(panel).getByRole('button', { name: /Depends on the unknown/ })
    );

    expect(editor.setSelection).toHaveBeenLastCalledWith(
      expect.objectContaining({ startLineNumber: 8, endLineNumber: 8 })
    );
    expect(editor.revealLineInCenter).toHaveBeenLastCalledWith(8);
    expect(editor.focus).toHaveBeenCalled();
    expect(visual.revealStep).toHaveBeenCalledWith('b');
    expect(screen.queryByRole('dialog', { name: 'Issues' })).toBeNull();
  });
});

// The whole file shows a bar for each line that differs from the last save;
// the YAML of a step in the node view shows none.
describe('DAGSpec: change bars of the YAML file', () => {
  it('gives the saved text to the editor of the whole file only', async () => {
    await renderSpec();
    expect(editor.baseline).toBe(savedSpec);

    act(() => visual.props?.onYamlModeChange?.('step'));
    expect(editor.baseline).toBeUndefined();

    act(() => visual.props?.onYamlModeChange?.('file'));
    expect(editor.baseline).toBe(savedSpec);
  });
});

describe('DAGSpec compact notices: notice line', () => {
  // parity:canvas-frozen-state
  it('keeps the draft offer on the canvas, out of the notice line', async () => {
    localStorage.setItem(
      'dagu:draft:local:example.yaml',
      JSON.stringify({ text: savedSpec + '# draft\n', savedAt: 1 })
    );
    await renderSpec();
    // The card is the canvas overlay of the visual editor, not a line of the
    // host: the only notice line on the page is the one inside the card.
    const card = within(screen.getByTestId('visual-editor-mock')).getByTestId(
      'draft-recovery'
    );
    expect(screen.getAllByTestId('notice-line')).toEqual([
      within(card).getByTestId('notice-line'),
    ]);
    expect(
      within(card).getByRole('status', { name: 'Unsaved draft' })
    ).toBeInTheDocument();

    act(() => visual.props?.onStateChange?.(visualState(true)));
    const line = screen
      .getAllByTestId('notice-line')
      .find((element) => !card.contains(element))!;
    expect(line).toHaveTextContent(FROZEN_TEXT);
    expect(
      screen.queryByRole('button', { name: '1 more notice' })
    ).not.toBeInTheDocument();
    expect(
      within(line).queryByRole('status', { name: 'Unsaved draft' })
    ).not.toBeInTheDocument();

    act(() => visual.props?.onStateChange?.(visualState(false)));
    expect(screen.queryByText(FROZEN_TEXT)).not.toBeInTheDocument();
  });

  it('names the line that the editor cannot read', async () => {
    await renderSpec();
    act(() =>
      visual.props?.onStateChange?.({ ...visualState(true), unparsedLine: 40 })
    );

    expect(screen.getByTestId('notice-line')).toHaveTextContent(
      'The editor cannot read the YAML at line 40: the canvas shows the steps the server read'
    );
  });

  // parity:edit-notice-bar
  it('shows a refused edit as an alert with its actions', async () => {
    await renderSpec();
    const convert = vi.fn();
    const dismiss = vi.fn();
    act(() =>
      visual.props?.onNotice?.({
        kind: 'edit-refused',
        message: 'This step cannot be edited here',
        convertible: true,
        convert,
        dismiss,
      })
    );

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent('This step cannot be edited here');
    fireEvent.click(
      within(alert).getByRole('button', { name: 'Convert steps' })
    );
    expect(convert).toHaveBeenCalledOnce();
    fireEvent.click(within(alert).getByRole('button', { name: 'Dismiss' }));
    expect(dismiss).toHaveBeenCalledOnce();

    act(() => visual.props?.onNotice?.(null));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('leaves the refusal to a host that listens to onNotice', async () => {
    const onNotice = vi.fn();
    await renderSpec({ onNotice });
    expect(visual.props?.onNotice).toBe(onNotice);
  });

  it('merges the notices of the host by priority', async () => {
    const run = vi.fn();
    const notices: WorkspaceNotice[] = [
      {
        id: 'run-not-found',
        tone: 'info',
        message: 'The run was not found',
        action: { label: 'Show latest', run },
        priority: 1,
      },
    ];
    await renderSpec({ notices });

    const line = screen.getByTestId('notice-line');
    expect(line).toHaveTextContent('The run was not found');
    fireEvent.click(within(line).getByRole('button', { name: 'Show latest' }));
    expect(run).toHaveBeenCalledOnce();

    act(() => visual.props?.onStateChange?.(visualState(true)));
    expect(line).toHaveTextContent(FROZEN_TEXT);
    expect(line).not.toHaveTextContent('The run was not found');
    expect(
      screen.getByRole('button', { name: '1 more notice' })
    ).toBeInTheDocument();
  });

  it('keeps the classic page without compactNotices', async () => {
    mocks.useQuery.mockReturnValue(specData({ warnings: ['Saved warning'] }));
    await renderSpec({
      chrome: undefined,
      notices: [{ id: 'x', tone: 'info', message: 'Host notice', priority: 1 }],
    });

    expect(visual.props?.onNotice).toBeUndefined();
    expect(screen.queryByTestId('notice-line')).not.toBeInTheDocument();
    expect(screen.queryByText('Host notice')).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: /^Issues/ })
    ).not.toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('Saved warning');
  });
});

// The draft tests of DAGSpec.test.tsx, with compact notices.
// parity:spec-draft-recovery
describe('DAGSpec compact notices: draft recovery', () => {
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
});
