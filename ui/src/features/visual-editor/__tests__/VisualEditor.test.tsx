// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus } from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { parseDagDoc } from '../doc/parse';
import { realSchema } from '../inspector/__tests__/realSchema';
import {
  scopeHttpJq,
  yamlArraySteps,
  yamlBroken,
  yamlHttpJq,
} from '../testing/fixtures';
import { DOC_MODEL_DELAY_MS } from '../useDocModel';
import { VisualEditor, type VisualEditorProps } from '../VisualEditor';

vi.mock(
  '@monaco-editor/react',
  async () => (await import('../inspector/__tests__/monacoMock')).monacoModule
);
vi.mock('../inspector/monacoSetup', () => ({ monacoTheme: () => 'light' }));
const api = vi.hoisted(() => {
  const empty = { data: undefined, isLoading: false, mutate: () => undefined };
  return {
    empty,
    runs: { data: { dagRuns: [] }, isLoading: false, mutate: () => undefined },
    run: empty as unknown,
    post: (() => undefined) as (...args: unknown[]) => unknown,
  };
});

vi.mock('@/hooks/api', () => ({
  useQuery: (path: string, init: unknown) => {
    if (init === null) return api.empty;
    if (path === '/dag-runs/{name}/{dagRunId}') return api.run;
    if (path === '/dags/{fileName}/dag-runs') return api.runs;
    return api.empty;
  },
  useClient: () => ({ POST: (...args: unknown[]) => api.post(...args) }),
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

type HarnessProps = Partial<VisualEditorProps> & { initial: string };

const state = { text: '', changes: 0 };

function Harness({ initial, ...overrides }: HarnessProps) {
  const [text, setText] = useState(initial);
  state.text = text;
  return (
    <VisualEditor
      fileName="example.yaml"
      dagName="example"
      text={text}
      onTextChange={(next) => {
        state.changes += 1;
        setText(next);
      }}
      validation={null}
      editable
      isDirty={text !== initial}
      customActions={[]}
      {...overrides}
    />
  );
}

// The bar of the YAML drawer, where the Form button is: a double click on a
// step opens its YAML, and the form is one click away.
let yamlBar: HTMLElement;
beforeEach(() => {
  yamlBar = document.createElement('div');
  document.body.appendChild(yamlBar);
});
afterEach(() => yamlBar.remove());

function BarHarness(props: HarnessProps) {
  const [yamlVisible, setYamlVisible] = useState(false);
  return (
    <Harness
      yamlVisible={yamlVisible}
      onYamlVisibleChange={(visible) => setYamlVisible(visible)}
      yamlToolbarRoot={yamlBar}
      {...props}
    />
  );
}

/** Renders the editor with a YAML drawer bar, so that a test can open the form. */
function renderEditorWithBar(props: HarnessProps) {
  state.text = props.initial;
  state.changes = 0;
  return render(
    <UserPreferencesProvider>
      <BarHarness {...props} />
    </UserPreferencesProvider>
  );
}

/** Opens the YAML of a step with a double click, then its form with the Form button. */
function openForm(stepTestId: string) {
  fireEvent.doubleClick(screen.getByTestId(stepTestId));
  fireEvent.click(within(yamlBar).getByRole('button', { name: 'Form' }));
}

function renderEditor(props: HarnessProps) {
  state.text = props.initial;
  state.changes = 0;
  return render(
    <UserPreferencesProvider>
      <Harness {...props} />
    </UserPreferencesProvider>
  );
}

/** The search field of the list of steps, which opens in the side drawer. */
function paletteSearch() {
  return screen.getByRole('combobox', { name: 'Search actions' });
}

describe.skipIf(realSchema === null)('VisualEditor', () => {
  it('opens without changing the text', () => {
    renderEditor({ initial: yamlArraySteps });

    expect(screen.getByTestId('ve-canvas')).toBeInTheDocument();
    expect(screen.getByText('fetch')).toBeInTheDocument();
    expect(state.changes).toBe(0);
    expect(state.text).toBe(yamlArraySteps);
  });

  it('freezes the canvas while the YAML does not parse', () => {
    vi.useFakeTimers();
    try {
      const { rerender } = renderEditor({ initial: yamlArraySteps });
      rerender(
        <UserPreferencesProvider>
          <Harness
            initial={yamlArraySteps}
            text={`\tname: x\n${yamlArraySteps}`}
          />
        </UserPreferencesProvider>
      );
      act(() => {
        vi.advanceTimersByTime(DOC_MODEL_DELAY_MS);
      });

      expect(
        screen.getByText(
          'YAML has errors: the canvas shows the last valid version'
        )
      ).toBeInTheDocument();
      // The n key does not open the list of steps on a frozen canvas.
      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
      expect(screen.queryByTestId('ve-add-step')).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it('keeps the other steps when one step does not parse', () => {
    vi.useFakeTimers();
    try {
      const { rerender } = renderEditor({ initial: yamlArraySteps });
      rerender(
        <UserPreferencesProvider>
          <Harness initial={yamlArraySteps} text={yamlBroken} />
        </UserPreferencesProvider>
      );
      act(() => {
        vi.advanceTimersByTime(DOC_MODEL_DELAY_MS);
      });

      // The step that does not parse is a node of its own: the canvas is
      // not frozen and no step disappears.
      expect(
        screen.queryByText(
          'YAML has errors: the canvas shows the last valid version'
        )
      ).toBeNull();
      expect(screen.getByTestId('ve-canvas-node-steps[0]')).toHaveTextContent(
        'a'
      );
      expect(screen.getByTestId('ve-canvas-node-steps[0]')).toHaveTextContent(
        'YAML error'
      );
    } finally {
      vi.useRealTimers();
    }
  });

  // The browser parser rejects a closing quote that is less indented than
  // its step, while the server reads the file and runs it.
  it('shows the steps that the server read while no text has parsed', () => {
    renderEditor({
      initial: [
        'type: graph',
        'steps:',
        '- id: a',
        "  run: 'echo x",
        '',
        "'",
        '- id: b',
        '  depends: [a]',
        '  run: echo y',
        '',
      ].join('\n'),
      validation: {
        issues: [],
        dag: {
          name: 'example',
          steps: [{ name: 'a' }, { name: 'b', depends: ['a'] }],
        },
      } as never,
    });

    expect(screen.getByTestId('ve-canvas-node-a')).toBeInTheDocument();
    expect(screen.getByTestId('ve-canvas-node-b')).toBeInTheDocument();
    expect(
      screen.getByText(
        'The editor cannot read the YAML at line 5: the canvas shows the steps the server read'
      )
    ).toBeInTheDocument();
    // The canvas is frozen: the n key does not open the list of steps.
    fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
    expect(screen.queryByTestId('ve-add-step')).toBeNull();
  });

  // "At the end of the workflow" chains the step after the last leaf.
  it('adds a step from the palette', () => {
    renderEditor({ initial: yamlArraySteps });

    fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
    const search = paletteSearch();
    fireEvent.change(search, { target: { value: 'http.request' } });
    fireEvent.keyDown(search, { key: 'Enter' });

    expect(state.text).toBe(
      yamlArraySteps +
        `
  - name: http_request
    depends: [publish]
    action: http.request
    with:
      method: GET
      url: https://example.com
`
    );
  });

  it('adds a step at the end of a chain workflow with no depends', () => {
    renderEditor({
      initial: 'type: chain\nsteps:\n  - name: a\n    run: echo a\n',
    });

    fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
    const search = paletteSearch();
    fireEvent.change(search, { target: { value: 'http.request' } });
    fireEvent.keyDown(search, { key: 'Enter' });

    expect(state.text).toContain('- name: http_request');
    expect(state.text).not.toContain('depends');
  });

  // The flow of the definition of done: add a step after a selected step,
  // then set its URL in the inspector. Only the new lines change.
  it('adds a step after the selected step and sets its URL', () => {
    vi.useFakeTimers();
    try {
      renderEditorWithBar({ initial: yamlArraySteps });

      openForm('ve-canvas-node-steps[0]');
      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
      const search = paletteSearch();
      fireEvent.change(search, { target: { value: 'http.request' } });
      fireEvent.keyDown(search, { key: 'Enter' });

      const inspector = within(screen.getByTestId('ve-inspector'));
      expect(
        inspector.getByRole('heading', { name: 'http_request' })
      ).toBeInTheDocument();
      fireEvent.change(inspector.getByLabelText('url'), {
        target: { value: 'https://example.com/${steps.fetch.outputs.body}' },
      });
      act(() => {
        vi.advanceTimersByTime(1000);
      });

      expect(state.text).toBe(
        yamlArraySteps.replace(
          '\n  - name: transform',
          `
  - name: http_request
    depends: [fetch]
    action: http.request
    with:
      method: GET
      url: https://example.com/\${steps.fetch.outputs.body}

  - name: transform`
        )
      );
    } finally {
      vi.useRealTimers();
    }
  });

  it('opens the inspector of a clicked step and closes it', () => {
    renderEditorWithBar({ initial: yamlArraySteps });

    openForm('ve-canvas-node-steps[1]');
    const inspector = within(screen.getByTestId('ve-inspector'));
    expect(
      inspector.getByRole('heading', { name: 'transform' })
    ).toBeInTheDocument();

    fireEvent.click(inspector.getByRole('button', { name: 'Close' }));
    expect(screen.queryByTestId('ve-inspector')).not.toBeInTheDocument();
    expect(state.changes).toBe(0);
  });

  // The inspector is a drawer over the page: the editor leaves its width free.
  it('leaves the width of the inspector drawer free while it is open', () => {
    renderEditorWithBar({ initial: yamlArraySteps });
    const editor = screen.getByTestId('visual-editor');
    expect(editor.style.paddingRight).toBe('');

    openForm('ve-canvas-node-steps[1]');
    expect(editor.style.paddingRight).toMatch(/^\d+px$/);

    fireEvent.click(
      within(screen.getByTestId('ve-inspector')).getByRole('button', {
        name: 'Close',
      })
    );
    expect(editor.style.paddingRight).toBe('');
  });

  it('shows the status of the latest run on the steps', () => {
    api.run = {
      data: {
        dagRunDetails: {
          dagRunId: 'run-1',
          nodes: [
            {
              step: { name: 'fetch' },
              status: NodeStatus.Success,
              statusLabel: 'succeeded',
              retryCount: 0,
              doneCount: 1,
            },
          ],
        },
      },
      isLoading: false,
      mutate: () => undefined,
    };
    try {
      renderEditor({ initial: yamlArraySteps });

      const node = within(screen.getByTestId('ve-canvas-node-steps[0]'));
      expect(node.getByTestId('ve-canvas-node-status')).toHaveAttribute(
        'data-status',
        String(NodeStatus.Success)
      );
      // The newest run is the one shown: there is no selector.
      expect(screen.queryByLabelText('Run to show')).not.toBeInTheDocument();
    } finally {
      api.run = api.empty;
    }
  });

  it('runs one step after a confirmation that names it', async () => {
    const calls: unknown[][] = [];
    api.post = (...args: unknown[]) => {
      calls.push(args);
      return Promise.resolve({
        data: { dagRunId: 'run-2' },
        response: new Response(null, { status: 200 }),
      });
    };
    try {
      renderEditor({ initial: yamlArraySteps });
      const node = screen
        .getByTestId('ve-canvas-node-steps[1]')
        .closest<HTMLElement>('[data-flow-node]')!;
      // The toolbar mounts its buttons for a hovered node.
      fireEvent.pointerEnter(node);
      fireEvent.click(within(node).getByLabelText('Run step'));

      const dialog = screen.getByRole('dialog');
      expect(dialog).toHaveTextContent('Run only the step transform now?');
      expect(calls).toHaveLength(0);
      await act(async () => {
        fireEvent.click(within(dialog).getByRole('button', { name: 'Run' }));
      });

      expect(calls).toEqual([
        [
          '/dags/{fileName}/start',
          {
            params: {
              path: { fileName: 'example.yaml' },
              query: { remoteNode: 'local' },
            },
            body: { steps: ['transform'], labels: ['dagu.source=editor'] },
          },
        ],
      ]);
    } finally {
      api.post = () => undefined;
    }
  });

  // Run step runs the steps that the step needs first, on no earlier output.
  it('runs the upstream steps and the step, with no reuse', async () => {
    const calls: unknown[][] = [];
    api.post = (...args: unknown[]) => {
      calls.push(args);
      return Promise.resolve({
        data: { dagRunId: 'run-2' },
        response: new Response(null, { status: 200 }),
      });
    };
    try {
      renderEditor({
        initial: yamlArraySteps,
        validation: {
          issues: [],
          dag: {
            name: 'nightly-import',
            steps: [
              { name: 'fetch' },
              { name: 'transform', depends: ['fetch'] },
              { name: 'publish', depends: ['transform'] },
            ],
          },
        } as never,
      });
      const node = screen
        .getByTestId('ve-canvas-node-steps[2]')
        .closest<HTMLElement>('[data-flow-node]')!;
      // The toolbar mounts its buttons for a hovered node.
      fireEvent.pointerEnter(node);
      fireEvent.click(within(node).getByLabelText('Run step'));

      const dialog = screen.getByRole('dialog');
      expect(dialog).toHaveTextContent(
        'Run the step publish with its 2 upstream steps?'
      );
      await act(async () => {
        fireEvent.click(within(dialog).getByRole('button', { name: 'Run' }));
      });

      expect(calls).toHaveLength(1);
      expect(calls[0]![1]).toMatchObject({
        body: {
          steps: ['fetch', 'transform', 'publish'],
          labels: ['dagu.source=editor'],
        },
      });
      expect(
        'outputsFromRunId' in (calls[0]![1] as { body: object }).body
      ).toBe(false);
    } finally {
      api.post = () => undefined;
    }
  });

  it('suggests the harnesses of the document as providers', () => {
    renderEditorWithBar({
      initial: `harnesses:
  team-agent:
    binary: team-agent
steps:
  - name: agent
    action: harness.run
    with:
      prompt: Write tests
`,
      harnesses: ['team-agent'],
    });
    openForm('ve-canvas-node-steps[0]');
    const inspector = within(screen.getByTestId('ve-inspector'));
    fireEvent.focus(inspector.getByRole('combobox', { name: 'provider' }));
    expect(
      inspector.getByRole('option', { name: 'team-agent' })
    ).toBeInTheDocument();
  });

  it('inserts an upstream output into the focused field', () => {
    vi.useFakeTimers();
    try {
      renderEditorWithBar({
        initial: yamlHttpJq,
        validation: { issues: [], referenceScope: scopeHttpJq },
      });
      openForm('ve-canvas-node-steps[1]');
      const inspector = within(screen.getByTestId('ve-inspector'));
      const filter = inspector.getByLabelText('filter') as HTMLInputElement;
      fireEvent.focusIn(filter);
      filter.setSelectionRange(filter.value.length, filter.value.length);

      const insert = inspector
        .getAllByRole('button')
        .find((button) => button.title === '${steps.fetch.outputs.body}');
      fireEvent.click(insert!);
      act(() => {
        vi.advanceTimersByTime(1000);
      });

      expect(filter.value).toBe('[.[].name]${steps.fetch.outputs.body}');
      expect(state.text).toContain('[.[].name]${steps.fetch.outputs.body}');
    } finally {
      vi.useRealTimers();
    }
  });

  it('copies the value when no field was focused', async () => {
    const writeText = vi.fn(() => Promise.resolve());
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    });
    renderEditorWithBar({
      initial: yamlHttpJq,
      validation: { issues: [], referenceScope: scopeHttpJq },
    });
    openForm('ve-canvas-node-steps[1]');
    fireEvent.click(
      within(screen.getByTestId('ve-inspector'))
        .getAllByRole('button')
        .find((button) => button.title === '${steps.fetch.outputs.body}')!
    );

    expect(writeText).toHaveBeenCalledTimes(1);
    // The name shows a Check, with the copied label.
    expect(
      await within(screen.getByTestId('ve-inspector')).findByRole('button', {
        name: 'Copied',
      })
    ).toBeInTheDocument();
    expect(state.changes).toBe(0);
  });

  it('undoes and redoes a visual edit', () => {
    renderEditor({ initial: yamlArraySteps });

    fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
    fireEvent.keyDown(paletteSearch(), { key: 'Enter' });
    const edited = state.text;
    expect(edited).not.toBe(yamlArraySteps);

    fireEvent.click(screen.getByLabelText('Undo'));
    expect(state.text).toBe(yamlArraySteps);
    fireEvent.click(screen.getByLabelText('Redo'));
    expect(state.text).toBe(edited);
  });

  it('undoes with the keyboard on the canvas but not in a field', () => {
    renderEditorWithBar({ initial: yamlArraySteps });
    fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
    fireEvent.keyDown(paletteSearch(), { key: 'Enter' });
    const edited = state.text;

    fireEvent.keyDown(screen.getByTestId('ve-canvas'), {
      key: 'z',
      ctrlKey: true,
    });
    expect(state.text).toBe(yamlArraySteps);
    fireEvent.keyDown(screen.getByTestId('ve-canvas'), {
      key: 'Z',
      metaKey: true,
      shiftKey: true,
    });
    expect(state.text).toBe(edited);

    openForm('ve-canvas-node-steps[0]');
    const field = within(screen.getByTestId('ve-inspector')).getAllByRole(
      'textbox'
    )[0]!;
    fireEvent.keyDown(field, { key: 'z', ctrlKey: true });
    expect(state.text).toBe(edited);
  });

  it('shows no edit controls to a reader', () => {
    renderEditor({ initial: yamlArraySteps, editable: false });

    expect(screen.queryByText('Add step')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Undo')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Redo')).not.toBeInTheDocument();
    // The direction and the export stay.
    expect(screen.getByLabelText('Top to bottom')).toBeInTheDocument();
  });

  // There is no bar above the canvas: the controls sit on the canvas.
  it('puts the run, history and workflow controls on the canvas', () => {
    renderEditor({
      initial: yamlArraySteps,
      toolbarLead: <button type="button">Lead</button>,
      toolbarTrail: <button type="button">Trail</button>,
      workflowPanel: <p>About the workflow</p>,
      onYamlVisibleChange: () => {},
    });

    const toolbar = within(screen.getByTestId('ve-toolbar'));
    for (const name of ['Lead', 'Trail', 'Undo', 'Redo', 'Run', 'Workflow']) {
      expect(toolbar.getByRole('button', { name })).toBeInTheDocument();
    }
    expect(toolbar.getByRole('button', { name: 'YAML' })).toBeInTheDocument();
    expect(screen.getByTestId('ve-canvas')).toContainElement(
      screen.getByTestId('ve-toolbar')
    );
  });

  // The toolbar has no Add step button: the n key, the + buttons and the
  // empty tile add a step.
  it('opens the list of steps with the n key and has no Add step button', () => {
    renderEditor({ initial: yamlArraySteps });
    expect(
      within(screen.getByTestId('ve-toolbar')).queryByRole('button', {
        name: 'Add step',
      })
    ).toBeNull();

    fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
    expect(screen.getByTestId('ve-add-step')).toBeInTheDocument();

    fireEvent.keyDown(paletteSearch(), { key: 'Escape' });
    expect(screen.queryByTestId('ve-add-step')).toBeNull();
  });

  it('reverts to the last save from a button next to Undo', () => {
    const onRevertToSave = vi.fn();
    const { unmount } = renderEditor({
      initial: yamlArraySteps,
      onRevertToSave,
      isDirty: false,
    });
    const revert = () =>
      screen.getByRole('button', { name: 'Revert to the last save' });
    expect(revert()).toBeDisabled();
    unmount();

    renderEditor({ initial: yamlArraySteps, onRevertToSave, isDirty: true });
    expect(revert()).toBeEnabled();
    expect(
      revert().compareDocumentPosition(screen.getByLabelText('Undo')) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
    fireEvent.click(revert());
    expect(onRevertToSave).toHaveBeenCalledTimes(1);
  });

  // A disabled button shows its reason, and Undo shows the key of the table.
  it('explains a disabled Undo in its tooltip, with the key', async () => {
    renderEditor({ initial: yamlArraySteps });
    const undo = screen.getByRole('button', { name: 'Undo' });
    expect(undo).toBeDisabled();
    act(() => undo.parentElement?.focus());
    const tip = await screen.findByRole('tooltip');
    expect(tip).toHaveTextContent('Nothing to undo');
    expect(tip).toHaveTextContent('Ctrl/Cmd+Z');
  });

  it('has no revert button without a handler or for a reader', () => {
    const onRevertToSave = vi.fn();
    const { unmount } = renderEditor({ initial: yamlArraySteps });
    expect(
      screen.queryByRole('button', { name: 'Revert to the last save' })
    ).toBeNull();
    unmount();
    renderEditor({ initial: yamlArraySteps, editable: false, onRevertToSave });
    expect(
      screen.queryByRole('button', { name: 'Revert to the last save' })
    ).toBeNull();
  });

  // The toolbar button shows the whole file: no step choice, no form, and a
  // click on a step does not scope the YAML. A step opened with a double
  // click has the scoped view and its YAML | Form bar, with no choice of
  // the part of the file: the file has its own panel.
  it('opens the whole file from the toolbar YAML button, with no bar', () => {
    const onYamlStepPathChange = vi.fn();
    renderEditorWithBar({ initial: yamlArraySteps, onYamlStepPathChange });

    fireEvent.click(
      within(screen.getByTestId('ve-toolbar')).getByRole('button', {
        name: 'YAML',
      })
    );
    expect(yamlBar.querySelectorAll('button')).toHaveLength(0);

    fireEvent.click(screen.getByTestId('ve-canvas-node-steps[1]'));
    expect(yamlBar.querySelectorAll('button')).toHaveLength(0);
    expect(onYamlStepPathChange).not.toHaveBeenCalledWith(['steps', '1']);

    fireEvent.doubleClick(screen.getByTestId('ve-canvas-node-steps[1]'));
    const bar = within(yamlBar);
    expect(bar.getByRole('button', { name: 'Form' })).toBeEnabled();
    expect(bar.getByRole('button', { name: 'YAML' })).toBeEnabled();
    expect(bar.queryByRole('button', { name: 'Step' })).toBeNull();
    expect(bar.queryByRole('button', { name: 'File' })).toBeNull();
  });

  it('offers an export of the graph', () => {
    renderEditor({ initial: yamlArraySteps });

    // The export button sits in the top-left toolbar, beside the zoom buttons.
    expect(
      within(screen.getByTestId('ve-canvas')).getByRole('button', {
        name: 'Export graph',
      })
    ).toBeInTheDocument();
    expect(
      within(screen.getByTestId('ve-toolbar')).queryByRole('button', {
        name: 'Export graph',
      })
    ).toBeNull();
  });

  it('has no workflow button without a panel', () => {
    renderEditor({ initial: yamlArraySteps });

    expect(
      screen.queryByRole('button', { name: 'Workflow' })
    ).not.toBeInTheDocument();
  });

  // The workflow panel, the step inspector and the YAML share one drawer.
  it('shows the workflow panel in the drawer, and a step replaces it', () => {
    const onYamlVisibleChange = vi.fn();
    renderEditor({
      initial: yamlArraySteps,
      workflowPanel: <p>About the workflow</p>,
      onYamlVisibleChange,
    });

    const button = screen.getByRole('button', { name: 'Workflow' });
    fireEvent.click(button);
    expect(screen.getByText('About the workflow')).toBeInTheDocument();
    expect(button).toHaveAttribute('aria-pressed', 'true');
    // The drawer leaves its width free, so that the toolbar stays in reach.
    expect(screen.getByTestId('visual-editor').style.paddingRight).toMatch(
      /^\d+px$/
    );

    // A click on a step shows its YAML, and the workflow panel makes room.
    fireEvent.click(screen.getByTestId('ve-canvas-node-steps[1]'));
    expect(onYamlVisibleChange).toHaveBeenLastCalledWith(true);
    expect(screen.queryByTestId('ve-inspector')).not.toBeInTheDocument();
    expect(button).toHaveAttribute('aria-pressed', 'false');
  });

  it('closes the inspector when the workflow panel opens', () => {
    renderEditorWithBar({
      initial: yamlArraySteps,
      workflowPanel: <p>About the workflow</p>,
    });
    openForm('ve-canvas-node-steps[1]');
    expect(screen.getByTestId('ve-inspector')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Workflow' }));

    expect(screen.queryByTestId('ve-inspector')).not.toBeInTheDocument();
    expect(screen.getByText('About the workflow')).toBeInTheDocument();
  });

  it('closes the workflow panel when the YAML opens', () => {
    const onYamlVisibleChange = vi.fn();
    renderEditor({
      initial: yamlArraySteps,
      workflowPanel: <p>About the workflow</p>,
      onYamlVisibleChange,
    });
    fireEvent.click(screen.getByRole('button', { name: 'Workflow' }));

    fireEvent.click(screen.getByRole('button', { name: 'YAML' }));

    expect(onYamlVisibleChange).toHaveBeenCalledWith(true, 'panel');
    expect(screen.getByRole('button', { name: 'Workflow' })).toHaveAttribute(
      'aria-pressed',
      'false'
    );
  });

  describe('the YAML of a step', () => {
    const yamlProps = (root: HTMLElement) => ({
      initial: yamlArraySteps,
      yamlVisible: true,
      onYamlVisibleChange: vi.fn(),
      yamlToolbarRoot: root,
    });

    it('shows the YAML of a clicked step, not its form', () => {
      const onYamlVisibleChange = vi.fn();
      renderEditor({ initial: yamlArraySteps, onYamlVisibleChange });

      fireEvent.click(screen.getByTestId('ve-canvas-node-steps[1]'));

      expect(onYamlVisibleChange).toHaveBeenLastCalledWith(true);
      expect(screen.queryByTestId('ve-inspector')).not.toBeInTheDocument();
    });

    // Opening a step shows its YAML first; the Form button opens the form.
    it('opens the YAML with the Open button of a step, then the form', () => {
      const onYamlVisibleChange = vi.fn();
      renderEditor({
        initial: yamlArraySteps,
        yamlVisible: true,
        onYamlVisibleChange,
        yamlToolbarRoot: yamlBar,
      });

      fireEvent.doubleClick(screen.getByTestId('ve-canvas-node-steps[1]'));

      expect(screen.queryByTestId('ve-inspector')).not.toBeInTheDocument();
      expect(onYamlVisibleChange).toHaveBeenLastCalledWith(true);

      fireEvent.click(within(yamlBar).getByRole('button', { name: 'Form' }));

      expect(screen.getByTestId('ve-inspector')).toBeInTheDocument();
      expect(onYamlVisibleChange).toHaveBeenLastCalledWith(false);
    });

    it('puts the YAML | Form choice in the bar, with no Step | File choice', () => {
      const root = document.createElement('div');
      document.body.appendChild(root);
      renderEditor(yamlProps(root));
      const bar = within(root);
      // With no step picked, there is nothing to open as a form.
      const form = bar.getByRole('button', { name: 'Form' });
      expect(form).toBeDisabled();
      expect(form).toHaveAttribute('title', 'Select a step first');
      // The YAML panel is the whole file: no choice of a part of it.
      for (const name of ['Step', 'File']) {
        expect(bar.queryByRole('button', { name })).toBeNull();
      }

      fireEvent.click(screen.getByTestId('ve-canvas-node-steps[1]'));
      expect(bar.getByRole('button', { name: 'Form' })).not.toHaveAttribute(
        'title'
      );
      // The bar does not name the step.
      expect(bar.queryByText('transform')).not.toBeInTheDocument();
      expect(bar.getByRole('button', { name: 'YAML' })).toHaveAttribute(
        'aria-pressed',
        'true'
      );
      root.remove();
    });

    it('opens the form from the bar of the YAML drawer, and comes back', () => {
      const root = document.createElement('div');
      document.body.appendChild(root);
      const props = yamlProps(root);
      renderEditor(props);
      fireEvent.click(screen.getByTestId('ve-canvas-node-steps[1]'));

      fireEvent.click(within(root).getByRole('button', { name: 'Form' }));
      expect(props.onYamlVisibleChange).toHaveBeenLastCalledWith(false);
      const inspector = within(screen.getByTestId('ve-inspector'));

      fireEvent.click(inspector.getByTitle('Show the YAML of this step'));
      expect(props.onYamlVisibleChange).toHaveBeenLastCalledWith(true);
      expect(screen.queryByTestId('ve-inspector')).not.toBeInTheDocument();
      root.remove();
    });

    describe('in one drawer for the YAML and the form', () => {
      function setup() {
        const bar = document.createElement('div');
        const body = document.createElement('div');
        document.body.append(bar, body);
        const onYamlVisibleChange = vi.fn();
        const onFormVisibleChange = vi.fn();
        const view = renderEditor({
          initial: yamlArraySteps,
          yamlVisible: true,
          onYamlVisibleChange,
          yamlToolbarRoot: bar,
          drawerBodyRoot: body,
          onFormVisibleChange,
        });
        return {
          bar: within(bar),
          body: within(body),
          barRoot: bar,
          bodyRoot: body,
          onYamlVisibleChange,
          onFormVisibleChange,
          ...view,
          clean: () => {
            bar.remove();
            body.remove();
          },
        };
      }

      it('puts the form in the body of the drawer, with no header of its own', () => {
        const t = setup();
        fireEvent.click(screen.getByTestId('ve-canvas-node-steps[1]'));

        fireEvent.click(t.bar.getByRole('button', { name: 'Form' }));

        expect(t.onFormVisibleChange).toHaveBeenLastCalledWith(true);
        // The drawer stays open: the YAML and the form are two modes of it.
        expect(t.onYamlVisibleChange).toHaveBeenLastCalledWith(true);
        const inspector = t.body.getByTestId('ve-inspector');
        expect(inspector).toBeInTheDocument();
        expect(
          t.body.queryByRole('heading', { name: 'transform' })
        ).not.toBeInTheDocument();
        expect(
          t.body.queryByTitle('Show the YAML of this step')
        ).not.toBeInTheDocument();
        t.clean();
      });

      it('keeps the same bar in both modes', () => {
        const t = setup();
        fireEvent.click(screen.getByTestId('ve-canvas-node-steps[1]'));
        const before = [...t.barRoot.querySelectorAll('button')].map(
          (b) => b.textContent
        );
        expect(before).toEqual(['YAML', 'Form']);

        fireEvent.click(t.bar.getByRole('button', { name: 'Form' }));

        const after = [...t.barRoot.querySelectorAll('button')].map(
          (b) => b.textContent
        );
        expect(after).toEqual(before);
        expect(t.bar.getByRole('button', { name: 'Form' })).toHaveAttribute(
          'aria-pressed',
          'true'
        );
        t.clean();
      });

      it('goes back to the YAML from the bar', () => {
        const t = setup();
        fireEvent.click(screen.getByTestId('ve-canvas-node-steps[1]'));
        fireEvent.click(t.bar.getByRole('button', { name: 'Form' }));

        fireEvent.click(t.bar.getByRole('button', { name: 'YAML' }));

        expect(t.onFormVisibleChange).toHaveBeenLastCalledWith(false);
        t.clean();
      });

      // The form shows in place of the text: the toolbar button is not
      // pressed then, and a click on it shows the text and keeps the panel open.
      it('shows the toolbar YAML button as not pressed while the form shows', () => {
        const t = setup();
        const toolbarYaml = () =>
          screen
            .getAllByRole('button', { name: 'YAML' })
            .find(
              (button) =>
                !t.barRoot.contains(button) && !t.bodyRoot.contains(button)
            )!;
        fireEvent.click(screen.getByTestId('ve-canvas-node-steps[1]'));
        expect(toolbarYaml()).toHaveAttribute('aria-pressed', 'true');

        fireEvent.click(t.bar.getByRole('button', { name: 'Form' }));
        expect(toolbarYaml()).toHaveAttribute('aria-pressed', 'false');

        t.onYamlVisibleChange.mockClear();
        fireEvent.click(toolbarYaml());

        expect(t.onYamlVisibleChange).toHaveBeenLastCalledWith(true, 'panel');
        expect(t.onFormVisibleChange).toHaveBeenLastCalledWith(false);
        t.clean();
      });

      it('stays in the form while the user clicks other steps', () => {
        const t = setup();
        fireEvent.click(screen.getByTestId('ve-canvas-node-steps[0]'));
        fireEvent.click(t.bar.getByRole('button', { name: 'Form' }));

        fireEvent.click(screen.getByTestId('ve-canvas-node-steps[2]'));

        expect(t.body.getByTestId('ve-inspector')).toBeInTheDocument();
        expect(t.onFormVisibleChange).not.toHaveBeenLastCalledWith(false);
        t.clean();
      });
    });

    it('stays on the form while the user clicks other steps', () => {
      renderEditorWithBar({ initial: yamlArraySteps });
      openForm('ve-canvas-node-steps[0]');

      fireEvent.click(screen.getByTestId('ve-canvas-node-steps[2]'));

      expect(
        within(screen.getByTestId('ve-inspector')).getByRole('heading', {
          name: 'publish',
        })
      ).toBeInTheDocument();
    });

    it('lights the step that the cursor of the YAML is in', () => {
      let moveCursor: (line: number) => void = () => undefined;
      const editor = {
        getModel: () => ({
          getValue: () => yamlArraySteps,
          getPositionAt: () => ({ lineNumber: 1, column: 1 }),
          getLineCount: () => 20,
        }),
        executeEdits: () => true,
        pushUndoStop: () => true,
        trigger: () => undefined,
        onDidChangeCursorPosition: (
          listener: (event: { position: { lineNumber: number } }) => void
        ) => {
          moveCursor = (lineNumber) => listener({ position: { lineNumber } });
          return { dispose: () => undefined };
        },
      };
      renderEditor({
        initial: yamlArraySteps,
        yamlVisible: true,
        onYamlVisibleChange: vi.fn(),
        yamlEditorRef: { current: editor as never },
      });
      // The light is a class of its own; the spotlight of a host is a
      // variant class that only data-spotlit turns on.
      const lit = (key: string) =>
        screen
          .getByTestId(`ve-canvas-node-${key}`)
          .classList.contains('shadow-primary/30');
      expect(lit('steps[1]')).toBe(false);

      const transformLine =
        parseDagDoc(yamlArraySteps).steps[1]!.range.startLine;
      act(() => moveCursor(transformLine));
      expect(lit('steps[1]')).toBe(true);
      expect(lit('steps[0]')).toBe(false);

      act(() => moveCursor(1));
      expect(lit('steps[1]')).toBe(false);
    });
    // In the whole file, the cursor in the block of a step is a selection of
    // that step, with the callbacks of the host; the panel stays the file.
    describe('the whole file in the YAML panel', () => {
      function cursorEditor() {
        const state = {
          move: ((): void => undefined) as (
            line: number,
            source?: string
          ) => void,
          positionLine: undefined as number | undefined,
        };
        const editor = {
          getModel: () => ({
            getValue: () => yamlArraySteps,
            getPositionAt: () => ({ lineNumber: 1, column: 1 }),
            getLineCount: () => 20,
          }),
          executeEdits: () => true,
          pushUndoStop: () => true,
          trigger: () => undefined,
          getPosition: () =>
            state.positionLine === undefined
              ? null
              : { lineNumber: state.positionLine },
          revealLineInCenterIfOutsideViewport: vi.fn(),
          onDidChangeCursorPosition: (
            listener: (event: {
              position: { lineNumber: number };
              source: string;
            }) => void
          ) => {
            state.move = (lineNumber, source = 'mouse') => {
              state.positionLine = lineNumber;
              listener({ position: { lineNumber }, source });
            };
            return { dispose: () => undefined };
          },
        };
        return { editor, state };
      }

      // The light of the cursor stands on the selected step: a click on
      // another node in the graph moves the selection and ends the light.
      it('drops the light of the cursor when a click selects another step', () => {
        const { editor, state } = cursorEditor();
        renderEditor({
          initial: yamlArraySteps,
          yamlVisible: true,
          initialYamlMode: 'file',
          onYamlVisibleChange: vi.fn(),
          yamlEditorRef: { current: editor as never },
        });
        const steps = parseDagDoc(yamlArraySteps).steps;
        // The spotlight of a host is a variant that only data-spotlit turns on.
        const ring = (key: string) =>
          screen
            .getByTestId(`ve-canvas-node-${key}`)
            .className.split(/\s+/)
            .filter((t) => !t.startsWith('group-data-[spotlit]/node:'))
            .join(' ');

        act(() => state.move(steps[1]!.range.startLine + 1));
        expect(ring('steps[1]')).toContain('ring-primary');

        fireEvent.click(screen.getByTestId('ve-canvas-node-steps[2]'));
        expect(ring('steps[2]')).toContain('ring-primary');
        expect(ring('steps[1]')).not.toContain('ring-primary');
      });

      it('selects the step that the cursor is in, and nothing outside a step', () => {
        const { editor, state } = cursorEditor();
        const onSelectedStepNameChange = vi.fn();
        const onYamlVisibleChange = vi.fn();
        renderEditor({
          initial: yamlArraySteps,
          yamlVisible: true,
          initialYamlMode: 'file',
          onYamlVisibleChange,
          onSelectedStepNameChange,
          yamlEditorRef: { current: editor as never },
        });
        const steps = parseDagDoc(yamlArraySteps).steps;

        act(() => state.move(steps[1]!.range.startLine + 1));
        expect(onSelectedStepNameChange).toHaveBeenLastCalledWith('transform');

        // The same step again: no new call.
        act(() => state.move(steps[1]!.range.startLine));
        expect(onSelectedStepNameChange).toHaveBeenCalledTimes(1);

        act(() => state.move(steps[2]!.range.startLine));
        expect(onSelectedStepNameChange).toHaveBeenLastCalledWith('publish');

        // The comment on the first line is in no step: nothing is cleared.
        act(() => state.move(1));
        expect(onSelectedStepNameChange).toHaveBeenCalledTimes(2);
        expect(onSelectedStepNameChange).not.toHaveBeenCalledWith(null);
        // It selects; it never opens the node view of the step.
        expect(onYamlVisibleChange).not.toHaveBeenCalled();
      });

      // The canvas edits the text, and Monaco moves the cursor under it.
      it('does not select a step for a move that the editor made itself', () => {
        const { editor, state } = cursorEditor();
        const onSelectedStepNameChange = vi.fn();
        renderEditor({
          initial: yamlArraySteps,
          yamlVisible: true,
          initialYamlMode: 'file',
          onYamlVisibleChange: vi.fn(),
          onSelectedStepNameChange,
          yamlEditorRef: { current: editor as never },
        });
        const second = parseDagDoc(yamlArraySteps).steps[1]!;
        act(() => state.move(second.range.startLine + 1, 'modelChange'));
        act(() => state.move(second.range.startLine + 1, 'api'));
        expect(onSelectedStepNameChange).not.toHaveBeenCalled();
      });

      it('does not scroll the editor to the step that the cursor selected', () => {
        const { editor, state } = cursorEditor();
        renderEditor({
          initial: yamlArraySteps,
          yamlVisible: true,
          initialYamlMode: 'file',
          onYamlVisibleChange: vi.fn(),
          onSelectedStepNameChange: vi.fn(),
          yamlEditorRef: { current: editor as never },
        });
        const second = parseDagDoc(yamlArraySteps).steps[1]!;
        act(() => state.move(second.range.endLine));
        expect(
          editor.revealLineInCenterIfOutsideViewport
        ).not.toHaveBeenCalled();
      });

      // In the node view of a step the cursor only lights the block.
      it('only lights the block of a step in the node view', () => {
        const { editor, state } = cursorEditor();
        const onSelectedStepNameChange = vi.fn();
        renderEditor({
          initial: yamlArraySteps,
          yamlVisible: true,
          onYamlVisibleChange: vi.fn(),
          onSelectedStepNameChange,
          yamlEditorRef: { current: editor as never },
        });
        act(() =>
          state.move(parseDagDoc(yamlArraySteps).steps[1]!.range.startLine)
        );
        expect(onSelectedStepNameChange).not.toHaveBeenCalled();
      });

      it('reports the mode of the YAML to the host', () => {
        const onYamlModeChange = vi.fn();
        renderEditor({
          initial: yamlArraySteps,
          yamlVisible: false,
          onYamlVisibleChange: vi.fn(),
          onYamlModeChange,
        });
        expect(onYamlModeChange).toHaveBeenLastCalledWith('step');
        fireEvent.click(
          within(screen.getByTestId('ve-toolbar')).getByRole('button', {
            name: 'YAML',
          })
        );
        expect(onYamlModeChange).toHaveBeenLastCalledWith('file');
        fireEvent.doubleClick(screen.getByTestId('ve-canvas-node-steps[1]'));
        expect(onYamlModeChange).toHaveBeenLastCalledWith('step');
      });
    });
  });

  describe('an agent DAG', () => {
    const yamlAgent = `type: agent
llm:
  provider: anthropic
  model: claude-opus-5
steps:
  - name: plan
    run: echo plan
  - name: build
    run: echo build
tasks:
  - name: shipped
    description: Finished when the release is out.
`;

    it('draws the agent over its steps, which hang from it', () => {
      renderEditor({ initial: yamlAgent });

      expect(screen.getByTestId('ve-canvas-node-agent')).toBeInTheDocument();
      expect(screen.getByTestId('ve-canvas-node-steps[0]')).toBeInTheDocument();
      expect(screen.getByTestId('ve-canvas-node-steps[1]')).toBeInTheDocument();
    });

    it('shows the YAML of the agent node, and has no form for it', () => {
      const onYamlVisibleChange = vi.fn();
      renderEditor({ initial: yamlAgent, onYamlVisibleChange });

      fireEvent.click(screen.getByTestId('ve-canvas-node-agent'));
      expect(onYamlVisibleChange).toHaveBeenLastCalledWith(true);

      onYamlVisibleChange.mockClear();
      fireEvent.doubleClick(screen.getByTestId('ve-canvas-node-agent'));
      expect(screen.queryByTestId('ve-inspector')).not.toBeInTheDocument();
      expect(onYamlVisibleChange).toHaveBeenLastCalledWith(true);
    });

    it('does not delete the agent node, which is no step', () => {
      renderEditor({ initial: yamlAgent });
      fireEvent.click(screen.getByTestId('ve-canvas-node-agent'));

      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'Delete' });

      expect(state.changes).toBe(0);
      expect(state.text).toBe(yamlAgent);
    });

    it('adds a step at the end when the agent node is selected', () => {
      renderEditor({ initial: yamlAgent });
      fireEvent.click(screen.getByTestId('ve-canvas-node-agent'));

      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
      const search = paletteSearch();
      fireEvent.change(search, { target: { value: 'http.request' } });
      fireEvent.keyDown(search, { key: 'Enter' });

      expect(state.changes).toBe(1);
      expect(state.text).toMatch(/- name: build[\s\S]*- name: http_request/);
      expect(state.text).toContain('tasks:');
    });
  });

  describe('a long agent DAG', () => {
    /** An agent DAG of `count` tools and `tasks` tasks. */
    function longAgent(count: number, tasks = 3): string {
      const steps = Array.from({ length: count }, (_, i) => {
        const n = String(i + 1).padStart(3, '0');
        return `  - name: tool ${n}\n    description: Do the work of step ${i + 1}.\n    run: echo ${i + 1}`;
      }).join('\n');
      const goals = Array.from(
        { length: tasks },
        (_, i) =>
          `  - name: goal ${i + 1}\n    description: Goal ${i + 1} is met.`
      ).join('\n');
      return `type: agent
llm:
  provider: anthropic
  model: claude-opus-5
  max_tool_iterations: 200
  system: |
    You run the tools one by one.
    Report each result.
steps:
${steps}
tasks:
${goals}
`;
    }

    const cards = () =>
      document.querySelectorAll(
        '[data-shape="tool"]'
      ) as NodeListOf<HTMLElement>;

    it('draws one hub and 100 tool cards, with no line between them', () => {
      renderEditor({ initial: longAgent(100, 10) });

      expect(screen.getAllByTestId('ve-canvas-node-agent')).toHaveLength(1);
      expect(cards()).toHaveLength(100);
      expect(document.querySelectorAll('[data-flow-edge]')).toHaveLength(0);
      const hub = within(screen.getByTestId('ve-canvas-node-agent'));
      expect(hub.getByTestId('ve-canvas-hub-model')).toHaveTextContent(
        'anthropic / claude-opus-5'
      );
      expect(hub.getByTestId('ve-canvas-hub-limit')).toHaveTextContent(
        'Up to 200 tool calls'
      );
      expect(hub.getByTestId('ve-canvas-hub-system')).toHaveTextContent(
        'You run the tools one by one.'
      );
      expect(hub.getAllByTestId('ve-canvas-hub-task')).toHaveLength(10);
    });

    it('shows the name, the description and the action of a tool card', () => {
      renderEditor({ initial: longAgent(5) });

      const card = within(screen.getByTestId('ve-canvas-node-steps[2]'));
      expect(card.getByText('tool 003')).toBeInTheDocument();
      expect(card.getByText('Do the work of step 3.')).toBeInTheDocument();
    });

    it('filters the tool cards by name', () => {
      renderEditor({ initial: longAgent(30) });
      const box = screen.getByRole('searchbox', {
        name: 'Filter tools by name',
      });

      fireEvent.change(box, { target: { value: 'TOOL 01' } });

      // tool 010 to tool 019
      const lit = Array.from(cards()).filter(
        (card) => card.dataset.filterMatch === 'true'
      );
      expect(lit).toHaveLength(10);
      expect(Array.from(cards()).filter((c) => c.dataset.dimmed)).toHaveLength(
        20
      );
      expect(screen.getByTestId('ve-tool-filter-count')).toHaveTextContent(
        '10 of 30 tools'
      );

      fireEvent.click(screen.getByRole('button', { name: 'Clear the filter' }));
      expect(Array.from(cards()).some((c) => c.dataset.dimmed)).toBe(false);
    });

    it('has no filter box on a graph workflow', () => {
      renderEditor({ initial: yamlArraySteps });
      expect(screen.queryByTestId('ve-tool-filter')).not.toBeInTheDocument();
    });

    it('opens the inspector of a tool card', () => {
      renderEditorWithBar({ initial: longAgent(8) });

      openForm('ve-canvas-node-steps[4]');

      expect(
        within(screen.getByTestId('ve-inspector')).getByRole('heading', {
          name: 'tool 005',
        })
      ).toBeInTheDocument();
    });

    it('deletes a tool card with its Delete button', () => {
      renderEditor({ initial: longAgent(6) });
      const cardNode = screen.getByTestId('ve-canvas-node-steps[1]');
      fireEvent.pointerEnter(cardNode.closest('[data-flow-node]')!);
      const card = within(cardNode);

      fireEvent.click(card.getByRole('button', { name: 'Delete' }));

      expect(state.changes).toBe(1);
      expect(state.text).not.toContain('tool 002');
      expect(state.text).toContain('tool 001');
      expect(state.text).toContain('tool 003');
      expect(cards()).toHaveLength(5);
    });

    it('renames a tool card', () => {
      renderEditor({ initial: longAgent(4) });
      fireEvent.click(screen.getByTestId('ve-canvas-node-steps[2]'));

      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'F2' });
      const field = screen.getByDisplayValue('tool 003');
      fireEvent.change(field, { target: { value: 'deploy' } });
      fireEvent.keyDown(field, { key: 'Enter' });

      expect(state.changes).toBe(1);
      expect(state.text).toContain('name: deploy');
      expect(state.text).not.toContain('tool 003');
    });

    it('adds a tool after the selected card', () => {
      renderEditor({ initial: longAgent(3) });
      fireEvent.click(screen.getByTestId('ve-canvas-node-steps[0]'));

      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
      const search = paletteSearch();
      fireEvent.change(search, { target: { value: 'http.request' } });
      fireEvent.keyDown(search, { key: 'Enter' });

      expect(state.changes).toBe(1);
      expect(cards()).toHaveLength(4);
    });

    it('moves the selection by a row with the arrow keys', () => {
      renderEditor({ initial: longAgent(20) });
      const canvas = screen.getByTestId('ve-canvas');
      fireEvent.click(screen.getByTestId('ve-canvas-node-agent'));

      fireEvent.keyDown(canvas, { key: 'ArrowDown' });
      fireEvent.keyDown(canvas, { key: 'ArrowRight' });
      fireEvent.keyDown(canvas, { key: 'ArrowRight' });
      // No width in jsdom: the grid has the default 6 columns.
      fireEvent.keyDown(canvas, { key: 'ArrowDown' });

      expect(screen.getByTestId('ve-canvas-node-steps[8]')).toHaveClass(
        'ring-primary'
      );
    });

    describe('with a run', () => {
      const runSource = {
        runId: 'run-1',
        details: {
          dagRunId: 'run-1',
          name: 'example',
          nodes: [
            {
              step: { name: 'tool 001' },
              status: NodeStatus.Success,
              statusLabel: 'succeeded',
              retryCount: 0,
              doneCount: 0,
            },
            {
              step: { name: 'tool 002' },
              status: NodeStatus.Failed,
              statusLabel: 'failed',
              retryCount: 0,
              doneCount: 0,
            },
            {
              step: { name: '__agent__' },
              status: NodeStatus.Running,
              statusLabel: 'running',
              retryCount: 0,
              doneCount: 0,
            },
          ],
          agentEvents: [
            { turn: 1, kind: 'action', name: 'tool 001', status: 'succeeded' },
            { turn: 2, kind: 'action', name: 'tool 002', status: 'failed' },
            { turn: 3, kind: 'action', name: 'tool 002', status: 'failed' },
            {
              turn: 3,
              kind: 'task_status',
              name: 'goal 1',
              status: 'completed',
            },
          ],
          agentTasks: [
            { name: 'goal 1', status: 'completed' },
            { name: 'goal 2', status: 'open' },
            { name: 'goal 3', status: 'failed' },
          ],
        },
      } as unknown as NonNullable<VisualEditorProps['runSource']>;

      it('shows the calls of each tool and dims the tools never called', () => {
        renderEditor({ initial: longAgent(4), runSource });

        const badge = (key: string) =>
          within(screen.getByTestId(`ve-canvas-node-${key}`)).queryByTestId(
            've-canvas-tool-calls'
          );
        expect(badge('steps[0]')).toHaveTextContent('1');
        expect(badge('steps[1]')).toHaveTextContent('2');
        expect(badge('steps[2]')).toBeNull();
        expect(screen.getByTestId('ve-canvas-node-steps[2]')).toHaveAttribute(
          'data-dimmed',
          'true'
        );
        expect(
          screen.getByTestId('ve-canvas-node-steps[0]')
        ).not.toHaveAttribute('data-dimmed');
        // The last status of a tool colours its card.
        expect(
          screen.getByTestId('ve-canvas-node-steps[1]').className
        ).toContain('border-destructive');
      });

      it('shows the calls of an agent run that has no task', () => {
        const noTasks = {
          runId: 'run-2',
          details: {
            dagRunId: 'run-2',
            name: 'example',
            nodes: [],
            agentEvents: [
              {
                turn: 1,
                kind: 'action',
                name: 'tool 001',
                status: 'succeeded',
              },
            ],
          },
        } as unknown as NonNullable<VisualEditorProps['runSource']>;
        renderEditor({ initial: longAgent(3, 0), runSource: noTasks });

        expect(
          within(screen.getByTestId('ve-canvas-node-steps[0]')).getByTestId(
            've-canvas-tool-calls'
          )
        ).toHaveTextContent('1');
        expect(screen.getByTestId('ve-canvas-node-steps[1]')).toHaveAttribute(
          'data-dimmed',
          'true'
        );
        expect(
          within(screen.getByTestId('ve-canvas-node-agent')).getByTestId(
            've-canvas-hub-iterations'
          )
        ).toHaveTextContent('Iterations: 1');
      });

      it('shows the run state and the turns on the hub, and the tasks', () => {
        renderEditor({ initial: longAgent(4), runSource });

        const hub = within(screen.getByTestId('ve-canvas-node-agent'));
        expect(hub.getByTestId('ve-canvas-node-status')).toHaveAttribute(
          'data-status',
          String(NodeStatus.Running)
        );
        expect(hub.getByTestId('ve-canvas-hub-iterations')).toHaveTextContent(
          'Iterations: 3'
        );
        expect(
          hub
            .getAllByTestId('ve-canvas-hub-task')
            .map((row) => row.dataset.state)
        ).toEqual(['completed', 'open', 'failed']);
        expect(hub.getByTestId('ve-canvas-hub-settled')).toHaveTextContent(
          '2 of 3 tasks settled'
        );
      });
    });
  });

  describe('adding a step in the side drawer', () => {
    it('opens the list of steps in the drawer, not in the canvas', () => {
      renderEditor({ initial: yamlArraySteps });

      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });

      const panel = screen.getByTestId('ve-add-step');
      expect(panel).toBeInTheDocument();
      expect(screen.getByTestId('ve-canvas')).not.toContainElement(panel);
      expect(
        screen.getByRole('heading', { name: 'Add a step' })
      ).toBeInTheDocument();
    });

    it('says where the step goes', () => {
      renderEditor({ initial: yamlArraySteps });
      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
      expect(
        screen.getByText('At the end of the workflow')
      ).toBeInTheDocument();
      fireEvent.keyDown(paletteSearch(), { key: 'Escape' });

      fireEvent.click(screen.getByTestId('ve-canvas-node-steps[0]'));
      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
      expect(screen.getByText('After fetch')).toBeInTheDocument();
    });

    it('closes the YAML while it lists the steps, and shows the new step', () => {
      const onYamlVisibleChange = vi.fn();
      renderEditor({
        initial: yamlArraySteps,
        yamlVisible: true,
        onYamlVisibleChange,
      });

      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
      expect(onYamlVisibleChange).toHaveBeenLastCalledWith(false);

      const search = paletteSearch();
      fireEvent.change(search, { target: { value: 'http.request' } });
      fireEvent.keyDown(search, { key: 'Enter' });
      // The panel gives way to the YAML of the step that it added.
      expect(onYamlVisibleChange).toHaveBeenLastCalledWith(true);
      expect(screen.queryByTestId('ve-add-step')).not.toBeInTheDocument();
      expect(state.changes).toBe(1);
    });

    it('remembers what the user added, for the next time', () => {
      localStorage.clear();
      renderEditor({ initial: yamlArraySteps });
      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
      const search = paletteSearch();
      fireEvent.change(search, { target: { value: 'http.request' } });
      fireEvent.keyDown(search, { key: 'Enter' });

      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });

      expect(screen.getByText('Recently used')).toBeInTheDocument();
      expect(
        JSON.parse(
          localStorage.getItem('dagu.visualEditor.recentActions') ?? '[]'
        )
      ).toEqual(['http.request']);
    });

    it('closes with Escape and changes nothing', () => {
      renderEditor({ initial: yamlArraySteps });
      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });

      fireEvent.keyDown(paletteSearch(), { key: 'Escape' });

      expect(screen.queryByTestId('ve-add-step')).not.toBeInTheDocument();
      expect(state.changes).toBe(0);
    });

    it('does not open for a reader', () => {
      renderEditor({ initial: yamlArraySteps, editable: false });

      // A reader has no Add step button at all.
      expect(screen.queryByText('Add step')).not.toBeInTheDocument();
      expect(screen.queryByTestId('ve-add-step')).not.toBeInTheDocument();
    });
  });
});

describe('the canvas overlay', () => {
  it('floats over the canvas, in its frame, with its own accessible content', () => {
    renderEditor({
      initial: yamlArraySteps,
      canvasOverlay: (
        <div role="status" aria-label="Unsaved draft">
          card
        </div>
      ),
    });

    const overlay = screen.getByTestId('ve-canvas-overlay');
    expect(screen.getByTestId('ve-canvas-frame')).toContainElement(overlay);
    expect(overlay.className).toContain('absolute');
    expect(
      within(overlay).getByRole('status', { name: 'Unsaved draft' })
    ).toBeInTheDocument();
  });

  it('adds nothing without an overlay', () => {
    renderEditor({ initial: yamlArraySteps });
    expect(screen.queryByTestId('ve-canvas-overlay')).toBeNull();
  });
});
