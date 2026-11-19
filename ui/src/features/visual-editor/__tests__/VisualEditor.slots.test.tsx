// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { useRef, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { CANVAS_DETAILS_KEY } from '../canvas/useCanvasDetails';
import { realSchema } from '../inspector/__tests__/realSchema';
import { VisualEditor, type VisualEditorProps } from '../VisualEditor';
import type { CodeEditorLike } from '../useEditSession';

vi.mock(
  '@monaco-editor/react',
  async () => (await import('../inspector/__tests__/monacoMock')).monacoModule
);
vi.mock('../inspector/monacoSetup', () => ({ monacoTheme: () => 'light' }));
// A test that needs the server sets `served.query`: it answers a request by
// its path, and a request that it does not know is empty.
const served = vi.hoisted(() => ({
  query: null as null | ((path: string, init: unknown) => unknown),
}));
vi.mock('@/hooks/api', () => {
  const empty = { data: undefined, isLoading: false, mutate: () => undefined };
  return {
    useQuery: (path: string, init: unknown) =>
      (served.query ? served.query(path, init) : undefined) ?? empty,
    useClient: () => ({ POST: () => undefined, GET: () => undefined }),
  };
});
vi.mock('@/hooks/useDAGRunSSE', () => ({
  useDAGRunSSE: () => ({
    data: null,
    isConnected: false,
    isConnecting: false,
    shouldUseFallback: true,
  }),
}));
vi.mock('@/contexts/AuthContext', () => ({
  useCanExecute: () => true,
  useCanExecuteForWorkspace: () => true,
  useIsAdminIfSignedIn: () => false,
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('@/contexts/SchemaContext', () => ({
  useSchema: () => ({ schema: realSchema }),
}));

const CHAT = `steps:
  - name: fetch
    run: echo x

  - name: triage
    action: chat.completion
    depends: [fetch]
    with:
      model: gpt-4o
      dags:
        - name: lookup
        - name: refund
`;

const AGENT = `steps:
  - name: code
    action: agent.run
    with:
      provider: claude
      prompt: fix it
      tools:
        builtin:
          read: true
`;

const AGENT_SKILLS = `steps:
  - name: code
    action: agent.run
    with:
      provider: claude
      prompt: fix it
      skills:
        - tone
        - refund-policy
`;

const state = { text: '' };

// The bar of the YAML drawer, where the Form button is: a double click on a
// step opens its YAML, and the form is one click away.
let yamlBar: HTMLElement;
beforeEach(() => {
  // A test that switched to the simplified view must not leave it to the next.
  window.localStorage.removeItem(CANVAS_DETAILS_KEY);
  yamlBar = document.createElement('div');
  document.body.appendChild(yamlBar);
});
afterEach(() => {
  yamlBar.remove();
  served.query = null;
});

/** Opens the YAML of a step with a double click, then its form with the Form button. */
function openForm(stepTestId: string) {
  fireEvent.doubleClick(screen.getByTestId(stepTestId));
  fireEvent.click(within(yamlBar).getByRole('button', { name: 'Form' }));
}

interface FakeEditor extends CodeEditorLike {
  setSelection: ReturnType<typeof vi.fn>;
  revealLineInCenter: ReturnType<typeof vi.fn>;
  focus: ReturnType<typeof vi.fn>;
}

function fakeEditor(): FakeEditor {
  return {
    getModel: () => ({
      getValue: () => state.text,
      getPositionAt: () => ({ lineNumber: 1, column: 1 }),
      getLineCount: () => state.text.split('\n').length,
    }),
    executeEdits: () => true,
    pushUndoStop: () => true,
    trigger: () => undefined,
    setSelection: vi.fn(),
    revealLineInCenter: vi.fn(),
    focus: vi.fn(),
  } as unknown as FakeEditor;
}

function Harness({
  editor,
  initial = CHAT,
  ...overrides
}: Partial<VisualEditorProps> & { editor?: FakeEditor; initial?: string }) {
  const [text, setText] = useState(initial);
  const [yamlVisible, setYamlVisible] = useState(false);
  const ref = useRef<CodeEditorLike | null>(editor ?? null);
  state.text = text;
  return (
    <VisualEditor
      fileName="example.yaml"
      dagName="example"
      text={text}
      onTextChange={setText}
      validation={null}
      editable
      isDirty={false}
      customActions={[]}
      yamlVisible={yamlVisible}
      onYamlVisibleChange={setYamlVisible}
      yamlEditorRef={ref}
      yamlToolbarRoot={yamlBar}
      {...overrides}
    />
  );
}

function renderEditor(
  editor?: FakeEditor,
  overrides: Partial<VisualEditorProps> & { initial?: string } = {}
) {
  return render(
    <UserPreferencesProvider>
      <Harness {...(editor ? { editor } : {})} {...overrides} />
    </UserPreferencesProvider>
  );
}

/** The field box that holds the focus, by its path. */
const focusedPath = () =>
  document.activeElement
    ?.closest('[data-field-path]')
    ?.getAttribute('data-field-path') ?? null;

/** The dock of the whole Tools or Skills list of a step. */
const slotPanel = () => within(screen.getByTestId('ve-slot-panel'));

/** The search box of the Tools dock or the Skills dock: it takes the focus on open. */
const searchBox = (name = 'Search tools') =>
  slotPanel().getByRole('combobox', { name });

/** The rows of the tools that the step has, in the Tools dock. */
const stepRows = () =>
  slotPanel()
    .getAllByTestId('ve-dock-row')
    .filter((row) => row.dataset.group === 'step');

/** The panel that shows at the right for a selected tool entry. */
const panel = () => screen.getByTestId('ve-tool-panel');

describe.skipIf(realSchema === null)('slots of a chat step', () => {
  it('opens the panel of a tool from a click on its row, and leaves the YAML alone', () => {
    const editor = fakeEditor();
    renderEditor(editor);
    fireEvent.click(screen.getByRole('button', { name: 'refund' }));

    // The panel is the editor of the tool, not the YAML of the step.
    expect(within(panel()).getByRole('heading')).toHaveTextContent('refund');
    expect(panel()).toHaveAttribute('data-kind', 'dag');
    expect(editor.setSelection).not.toHaveBeenCalled();
    expect(screen.queryByTestId('ve-inspector')).toBeNull();
    // The row of the tool is lit, and no other.
    expect(screen.getByRole('button', { name: 'refund' })).toHaveAttribute(
      'data-selected',
      'true'
    );
    expect(screen.getByRole('button', { name: 'lookup' })).not.toHaveAttribute(
      'data-selected'
    );
  });

  // The pickers of the Model card edit the model: a double click opens nothing.
  it('opens no form and no YAML from a double click on the Model card', () => {
    const editor = fakeEditor();
    renderEditor(editor);
    fireEvent.doubleClick(
      screen.getByTestId('ve-canvas-node-toolsat:steps[1]:model')
    );
    expect(screen.queryByTestId('ve-inspector')).toBeNull();
    expect(editor.setSelection).not.toHaveBeenCalled();
  });

  // The simplified view has no pickers: its round Model node opens the form.
  it('opens the form on its Model section from a double click on the round Model node of the simplified view', () => {
    const editor = fakeEditor();
    renderEditor(editor);
    fireEvent.click(screen.getByRole('button', { name: 'Simplified view' }));
    fireEvent.doubleClick(
      screen.getByTestId('ve-canvas-node-toolsat:steps[1]:model')
    );

    const form = within(screen.getByTestId('ve-inspector'));
    expect(form.getByRole('button', { name: 'Model' })).toHaveAttribute(
      'aria-expanded',
      'true'
    );
    // The form, not the YAML of the step.
    expect(editor.setSelection).not.toHaveBeenCalled();
    expect(focusedPath()).toBe('with.model');
  });

  // The simplified view draws a tile for each card: it opens the same dock.
  it('opens the dock of the DAGs list from a double click on the DAGs tile of the simplified view', () => {
    const editor = fakeEditor();
    renderEditor(editor);
    fireEvent.click(screen.getByRole('button', { name: 'Simplified view' }));
    const tile = screen.getByTestId('ve-canvas-node-toolsat:steps[1]:dags');
    expect(within(tile).getByTestId('ve-canvas-slot-count')).toHaveTextContent(
      '2'
    );
    fireEvent.doubleClick(tile);

    expect(screen.getByTestId('ve-slot-panel')).toHaveAttribute(
      'data-slot',
      'dags'
    );
    expect(screen.queryByTestId('ve-inspector')).toBeNull();
    expect(editor.setSelection).not.toHaveBeenCalled();
  });

  // The owner asked: a double click on Tools or Skills edits in the dock at
  // the right, not in the step view.
  it('opens the dock of the whole DAGs list from a double click on the DAGs card, and not the form', () => {
    const editor = fakeEditor();
    renderEditor(editor);
    const card = screen.getByTestId('ve-canvas-node-toolsat:steps[1]:dags');
    fireEvent.doubleClick(within(card).getByText('DAGs'));

    expect(screen.getByTestId('ve-slot-panel')).toHaveAttribute(
      'data-slot',
      'dags'
    );
    expect(slotPanel().getAllByRole('heading')[0]).toHaveTextContent('DAGs');
    expect(stepRows()).toHaveLength(2);
    // The DAGs dock lists the DAG tools only.
    expect(stepRows().every((row) => row.dataset.kind === 'dag')).toBe(true);
    // Not the step view: no form, and the YAML does not move.
    expect(screen.queryByTestId('ve-inspector')).toBeNull();
    expect(editor.setSelection).not.toHaveBeenCalled();
    // The search takes the focus, so that the keyboard works at once.
    expect(searchBox('Search DAGs')).toHaveFocus();
  });

  it('opens the same dock from a double click on the body of the card, and from a new double click once it is closed', () => {
    renderEditor(fakeEditor());
    const card = () =>
      screen.getByTestId('ve-canvas-node-toolsat:steps[1]:dags');
    fireEvent.doubleClick(card());
    expect(screen.getByTestId('ve-slot-panel')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.queryByTestId('ve-slot-panel')).toBeNull();
    fireEvent.doubleClick(card());
    expect(screen.getByTestId('ve-slot-panel')).toBeInTheDocument();
  });

  it('opens the dock from Enter on the focused DAGs card', () => {
    renderEditor(fakeEditor());
    const card = screen.getByTestId('ve-canvas-node-toolsat:steps[1]:dags');
    card.focus();
    expect(document.activeElement).toBe(card);
    fireEvent.keyDown(card, { key: 'Enter' });
    expect(screen.getByTestId('ve-slot-panel')).toHaveAttribute(
      'data-slot',
      'dags'
    );
  });

  it('goes from the dock of the list to the panel of a tool, and back to the form of the step', () => {
    renderEditor(fakeEditor());
    fireEvent.doubleClick(
      screen.getByTestId('ve-canvas-node-toolsat:steps[1]:dags')
    );
    fireEvent.click(stepRows()[1]!);
    fireEvent.click(slotPanel().getByTestId('ve-dock-edit'));
    expect(within(panel()).getByRole('heading')).toHaveTextContent('refund');
    expect(screen.queryByTestId('ve-slot-panel')).toBeNull();
    fireEvent.click(
      within(panel()).getByRole('button', { name: /Back to step/ })
    );
    expect(screen.getByTestId('ve-inspector')).toBeInTheDocument();
  });

  it('still opens the Tools section of the form from the form of the step', () => {
    renderEditor(fakeEditor());
    openForm('ve-canvas-node-steps[1]');
    const form = within(screen.getByTestId('ve-inspector'));
    expect(form.getByRole('button', { name: /^Tools/ })).toBeInTheDocument();
  });

  it('keeps the panel of a tool on a double click on its row', () => {
    renderEditor(fakeEditor());
    const row = screen.getByRole('button', { name: 'refund' });
    fireEvent.click(row);
    fireEvent.doubleClick(row);
    expect(within(panel()).getByRole('heading')).toHaveTextContent('refund');
    expect(screen.queryByTestId('ve-inspector')).toBeNull();
  });

  it('moves the panel from one tool to another, with the row that is lit', () => {
    renderEditor(fakeEditor());
    fireEvent.click(screen.getByRole('button', { name: 'lookup' }));
    expect(within(panel()).getByRole('heading')).toHaveTextContent('lookup');
    fireEvent.click(screen.getByRole('button', { name: 'refund' }));
    expect(within(panel()).getByRole('heading')).toHaveTextContent('refund');
    expect(screen.getByRole('button', { name: 'lookup' })).not.toHaveAttribute(
      'data-selected'
    );
  });

  // The miniature of a DAG tool on the DAGs card opens the enlarged view of its
  // DAG in the dock, with a way back to the step and to the panel of the tool.
  describe('the enlarged view of a DAG tool', () => {
    // An earlier test may have switched the canvas to the simplified view,
    // which has no miniature: the choice is kept in the storage.
    beforeEach(() => window.localStorage.removeItem(CANVAS_DETAILS_KEY));

    const serveDags = () => {
      served.query = (path, init) => {
        if (path !== '/dags/{fileName}' || !init) return undefined;
        const name = (init as { params: { path: { fileName: string } } }).params
          .path.fileName;
        return {
          data: {
            dag: {
              name,
              description: `The ${name} DAG.`,
              paramDefs: [],
              steps: [
                { name: 'first', depends: [] },
                { name: 'second', depends: ['first'] },
              ],
            },
          },
        };
      };
    };

    it('opens from the miniature, keeps the row of the tool lit, and goes back to the step from the breadcrumb', () => {
      serveDags();
      renderEditor(fakeEditor());
      const mini = screen.getAllByTestId('ve-dag-mini-open')[1]!;
      expect(mini).toHaveAccessibleName('Show the graph of refund');
      fireEvent.click(mini);

      const view = screen.getByTestId('ve-dag-panel');
      expect(view).toHaveAttribute('data-dag', 'refund');
      expect(within(view).getByTestId('ve-dag-title')).toHaveTextContent(
        'refund'
      );
      expect(within(view).getByTestId('ve-dag-crumb-step')).toHaveTextContent(
        'triage'
      );
      expect(screen.getByRole('button', { name: 'refund' })).toHaveAttribute(
        'data-selected',
        'true'
      );
      // The YAML and the form stay closed.
      expect(screen.queryByTestId('ve-inspector')).toBeNull();
      fireEvent.click(within(view).getByTestId('ve-dag-crumb-step'));
      expect(screen.queryByTestId('ve-dag-panel')).toBeNull();
      expect(screen.getByTestId('ve-inspector')).toBeInTheDocument();
    });

    it('goes to the panel of the tool from "Edit the tool", and closes with the close button', () => {
      serveDags();
      renderEditor(fakeEditor());
      fireEvent.click(screen.getAllByTestId('ve-dag-mini-open')[0]!);
      fireEvent.click(screen.getByTestId('ve-dag-edit-inputs'));
      expect(screen.queryByTestId('ve-dag-panel')).toBeNull();
      expect(within(panel()).getByRole('heading')).toHaveTextContent('lookup');
      fireEvent.click(screen.getAllByTestId('ve-dag-mini-open')[0]!);
      fireEvent.click(
        within(screen.getByTestId('ve-dag-panel')).getByRole('button', {
          name: 'Close',
        })
      );
      expect(screen.queryByTestId('ve-dag-panel')).toBeNull();
    });

    it('opens from the detail of the Tools dock too', () => {
      serveDags();
      renderEditor(fakeEditor());
      fireEvent.doubleClick(
        screen.getByTestId('ve-canvas-node-toolsat:steps[1]:dags')
      );
      fireEvent.click(stepRows()[0]!);
      fireEvent.click(slotPanel().getByTestId('ve-tool-graph-open'));
      expect(screen.queryByTestId('ve-slot-panel')).toBeNull();
      expect(screen.getByTestId('ve-dag-panel')).toHaveAttribute(
        'data-dag',
        'lookup'
      );
    });

    it('opens in a view that only reads', () => {
      serveDags();
      renderEditor(fakeEditor(), { editable: false });
      fireEvent.click(screen.getAllByTestId('ve-dag-mini-open')[0]!);
      expect(screen.getByTestId('ve-dag-panel')).toBeInTheDocument();
    });
  });

  it('opens the dock of the choices from the add row, and shows no panel yet', () => {
    renderEditor(fakeEditor());
    fireEvent.click(screen.getByRole('button', { name: 'Add a tool' }));

    expect(screen.getByTestId('ve-slot-panel')).toHaveAttribute(
      'data-mode',
      'add'
    );
    expect(searchBox()).toHaveFocus();
    expect(screen.queryByTestId('ve-tool-panel')).toBeNull();
    // The dock does not open the YAML or the form.
    expect(screen.queryByTestId('ve-inspector')).toBeNull();
  });

  it('adds the picked MCP server and opens the panel on it, with the focus on its first field', () => {
    renderEditor(fakeEditor());
    fireEvent.click(screen.getByRole('button', { name: 'Add a tool' }));
    // The server list is empty: the new inline server is the item to pick.
    fireEvent.change(searchBox(), { target: { value: 'URL' } });
    fireEvent.keyDown(searchBox(), { key: 'Enter' });

    expect(state.text).toContain('- name: refund\n');
    expect(state.text).toMatch(/mcp:\s+- name: ""\s+url: ""/);
    // The panel took the place of the dock, on the new entry.
    expect(screen.queryByTestId('ve-slot-panel')).toBeNull();
    expect(panel()).toHaveAttribute('data-kind', 'mcp');
    expect(
      within(panel())
        .getAllByRole('button')
        .filter((button) => button.hasAttribute('aria-controls'))
        .map((button) => button.querySelector('span')?.textContent)
    ).toEqual(['General', 'Connection', 'Allowed tools', 'YAML']);
    expect(document.activeElement).toBe(
      within(panel()).getAllByLabelText('Name')[0]
    );
    // The Tools card lists the new entry, and it is the selected one. The two
    // DAG tools stay on the DAGs card.
    const card = within(screen.getByRole('group', { name: 'Tools' }));
    expect(card.getByTestId('ve-canvas-slot-count')).toHaveTextContent('1');
    expect(
      within(screen.getByRole('group', { name: 'DAGs' })).getByTestId(
        've-canvas-slot-count'
      )
    ).toHaveTextContent('2');
    expect(
      card
        .getAllByTestId('ve-canvas-slot-row')
        .filter((row) => row.hasAttribute('data-selected'))
    ).toHaveLength(1);
  });

  it('edits the open tool in the panel, in the YAML of the step', () => {
    renderEditor(fakeEditor());
    fireEvent.click(screen.getByRole('button', { name: 'refund' }));
    const name = within(panel()).getAllByLabelText('Name')[0]!;
    fireEvent.change(name, { target: { value: 'refund-v2' } });
    fireEvent.blur(name);

    expect(state.text).toContain('- name: refund-v2');
    expect(state.text).toContain('- name: lookup');
    // The panel stays on the same tool, under its new name.
    expect(within(panel()).getByRole('heading')).toHaveTextContent('refund-v2');
  });

  it('removes the tool from the panel, closes the panel and clears the selection', () => {
    renderEditor(fakeEditor());
    fireEvent.click(screen.getByRole('button', { name: 'lookup' }));
    fireEvent.click(
      within(panel()).getByRole('button', { name: 'Remove tool lookup' })
    );

    expect(state.text).not.toContain('lookup');
    expect(state.text).toContain('- name: refund');
    expect(screen.queryByTestId('ve-tool-panel')).toBeNull();
    expect(screen.queryByRole('button', { name: 'lookup' })).toBeNull();
    expect(
      screen
        .getAllByTestId('ve-canvas-slot-row')
        .filter((row) => row.hasAttribute('data-selected'))
    ).toHaveLength(0);
  });

  it('closes the panel from its close button, from Escape, and from another step', () => {
    renderEditor(fakeEditor());
    fireEvent.click(screen.getByRole('button', { name: 'refund' }));
    fireEvent.click(within(panel()).getByRole('button', { name: 'Close' }));
    expect(screen.queryByTestId('ve-tool-panel')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'refund' }));
    expect(panel()).toBeInTheDocument();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByTestId('ve-tool-panel')).toBeNull();

    // Another step takes the selection: the tool of the first one goes.
    fireEvent.click(screen.getByRole('button', { name: 'refund' }));
    fireEvent.click(screen.getByTestId('ve-canvas-node-steps[0]'));
    expect(screen.queryByTestId('ve-tool-panel')).toBeNull();
  });

  it('shows the step form from the way back, and the panel goes', () => {
    renderEditor(fakeEditor());
    fireEvent.click(screen.getByRole('button', { name: 'refund' }));
    fireEvent.click(
      within(panel()).getByRole('button', { name: /Back to step/ })
    );
    expect(screen.queryByTestId('ve-tool-panel')).toBeNull();
    expect(screen.getByTestId('ve-inspector')).toBeInTheDocument();
  });

  it('lists the tools of the step as a compact list in the form, and opens the panel from a row', () => {
    renderEditor(fakeEditor());
    fireEvent.click(screen.getByRole('button', { name: 'refund' }));
    fireEvent.click(
      within(panel()).getByRole('button', { name: /Back to step/ })
    );
    const form = within(screen.getByTestId('ve-inspector'));
    expect(form.getAllByTestId('ve-tool-entry')).toHaveLength(2);
    expect(form.queryByTestId('ve-tool-editor')).toBeNull();
    fireEvent.click(form.getByRole('button', { name: 'Edit tool lookup' }));
    expect(screen.queryByTestId('ve-inspector')).toBeNull();
    expect(within(panel()).getByRole('heading')).toHaveTextContent('lookup');
  });

  it('opens the dock from the add button of the form', () => {
    renderEditor(fakeEditor());
    fireEvent.click(screen.getByRole('button', { name: 'refund' }));
    fireEvent.click(
      within(panel()).getByRole('button', { name: /Back to step/ })
    );
    fireEvent.click(
      within(screen.getByTestId('ve-inspector')).getByTestId('ve-tool-add')
    );
    expect(screen.getByTestId('ve-slot-panel')).toHaveAttribute(
      'data-mode',
      'add'
    );
    expect(screen.queryByTestId('ve-inspector')).toBeNull();
  });

  it('opens the dock of the skills from the round "+" of the Skills slot, with a name field and a note, and appends the name', () => {
    renderEditor(fakeEditor());
    // The step has no skill: its Skills slot is a small round "+", no card.
    expect(screen.queryByText('No skills')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Add a skill' }));

    expect(screen.getByTestId('ve-slot-panel')).toHaveAttribute(
      'data-slot',
      'skills'
    );
    expect(screen.getByTestId('ve-add-skill-note')).toHaveTextContent(
      'The list of skills could not be loaded. Type a name instead.'
    );
    const box = () => searchBox('Search skills');
    expect(box()).toHaveFocus();
    fireEvent.change(box(), { target: { value: 'refund-policy' } });
    // The typed name is the item to pick.
    expect(slotPanel().getByTestId('ve-skill-row')).toHaveTextContent(
      'Add "refund-policy"'
    );
    fireEvent.keyDown(box(), { key: 'Enter' });

    expect(state.text).toMatch(/skills:\s+- refund-policy/);
    // The dock stays open, with the new skill in the list.
    expect(box()).toHaveValue('');
    expect(slotPanel().getAllByTestId('ve-skill-row')).toHaveLength(1);
    // With a skill, the slot is a card again, with its own add row.
    expect(screen.getByRole('button', { name: 'Add a skill' })).toBeVisible();
    expect(screen.getByRole('button', { name: 'refund-policy' })).toBeVisible();
    fireEvent.change(box(), { target: { value: 'tone-of-voice' } });
    fireEvent.keyDown(box(), { key: 'Enter' });
    expect(state.text).toMatch(/skills:\s+- refund-policy\s+- tone-of-voice/);
    // A skill that is already listed is not offered again: Enter adds nothing.
    fireEvent.change(box(), { target: { value: 'tone-of-voice' } });
    fireEvent.keyDown(box(), { key: 'Enter' });
    expect(state.text.match(/tone-of-voice/g)).toHaveLength(1);
  });

  it('adds a skill to an agent step from its round "+", and its Tools slot with no tool is a "+" too', () => {
    renderEditor(fakeEditor(), {
      initial: AGENT.replace(/ {6}tools:\n {8}builtin:\n {10}read: true\n/, ''),
    });
    expect(screen.getByRole('button', { name: 'Add a tool' })).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Add a skill' }));
    const box = searchBox('Search skills');
    fireEvent.change(box, { target: { value: 'code-style' } });
    fireEvent.keyDown(box, { key: 'Enter' });
    expect(state.text).toMatch(/skills:\s+- code-style/);
    expect(state.text).toContain('action: agent.run');
  });

  it('closes the dock from its close button', () => {
    renderEditor(fakeEditor());
    fireEvent.click(screen.getByRole('button', { name: 'Add a tool' }));
    fireEvent.click(slotPanel().getByRole('button', { name: 'Close' }));
    expect(screen.queryByTestId('ve-slot-panel')).toBeNull();
  });

  it('moves the dock to the panel of a tool when its row is clicked', () => {
    renderEditor(fakeEditor());
    fireEvent.click(screen.getByRole('button', { name: 'Add a tool' }));
    expect(screen.getByTestId('ve-slot-panel')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'refund' }));
    expect(screen.queryByTestId('ve-slot-panel')).toBeNull();
    expect(within(panel()).getByRole('heading')).toHaveTextContent('refund');
  });
});

describe.skipIf(realSchema === null)('slots in a view that only reads', () => {
  it('opens the dock of the Skills list, read-only, with no remove button', () => {
    renderEditor(fakeEditor(), { editable: false, initial: AGENT_SKILLS });
    fireEvent.doubleClick(
      screen.getByTestId('ve-canvas-node-toolsat:steps[0]:skills')
    );
    const dock = within(screen.getByTestId('ve-slot-panel'));
    expect(dock.getAllByTestId('ve-skill-row')).toHaveLength(2);
    expect(dock.queryByRole('button', { name: /^Remove/ })).toBeNull();
    expect(state.text).toBe(AGENT_SKILLS);
  });

  it('opens the dock of the prompt, read-only', () => {
    renderEditor(fakeEditor(), { editable: false, initial: AGENT });
    fireEvent.click(
      screen.getByTestId('ve-canvas-node-toolsat:steps[0]:prompt')
    );
    expect(
      within(screen.getByTestId('ve-prompt-panel')).getByLabelText('Prompt')
    ).toBeDisabled();
    expect(state.text).toBe(AGENT);
  });

  it('opens the dock of the Tools list, read-only', () => {
    renderEditor(fakeEditor(), { editable: false });
    fireEvent.doubleClick(
      screen.getByTestId('ve-canvas-node-toolsat:steps[1]:dags')
    );
    expect(stepRows()).toHaveLength(2);
    // No catalogue and no add: the dock only reads.
    expect(slotPanel().queryByTestId('ve-dock-add')).toBeNull();
    expect(
      slotPanel()
        .getAllByTestId('ve-dock-group')
        .every((group) => group.dataset.section?.startsWith('step:'))
    ).toBe(true);
    expect(slotPanel().queryByRole('button', { name: /^Remove/ })).toBeNull();
  });

  it('opens the panel of a tool, read-only, and has no add row', () => {
    renderEditor(fakeEditor(), { editable: false });
    expect(screen.queryByRole('button', { name: 'Add a tool' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'refund' }));

    expect(within(panel()).getByRole('heading')).toHaveTextContent('refund');
    expect(within(panel()).getAllByLabelText('Name')[0]).toBeDisabled();
    expect(
      within(panel()).queryByRole('button', { name: /Remove tool/ })
    ).toBeNull();
    // Nothing is written to the file.
    expect(state.text).toBe(CHAT);
  });
});

describe.skipIf(realSchema === null)('the Skills slot', () => {
  const card = () =>
    screen.getByTestId('ve-canvas-node-toolsat:steps[0]:skills');
  const dock = () => within(screen.getByTestId('ve-slot-panel'));

  it('opens the dock of the whole Skills list from a double click on the card, and not the form', () => {
    const editor = fakeEditor();
    renderEditor(editor, { initial: AGENT_SKILLS });
    fireEvent.doubleClick(within(card()).getByText('Skills'));

    expect(screen.getByTestId('ve-slot-panel')).toHaveAttribute(
      'data-slot',
      'skills'
    );
    expect(dock().getAllByRole('heading')[0]).toHaveTextContent('Skills');
    expect(dock().getAllByTestId('ve-skill-row')).toHaveLength(2);
    expect(screen.queryByTestId('ve-inspector')).toBeNull();
    expect(editor.setSelection).not.toHaveBeenCalled();
    // The focus is on the search, not on a button that deletes.
    expect(
      dock().getByRole('combobox', { name: 'Search skills' })
    ).toHaveFocus();
  });

  it('opens it from a click or a double click on a skill row, and from Enter on the card', () => {
    renderEditor(fakeEditor(), { initial: AGENT_SKILLS });
    const row = screen.getByRole('button', { name: 'refund-policy' });
    // A double click is two clicks, and the first one must not cover the
    // canvas: the second one lands on the row again.
    fireEvent.click(row);
    expect(screen.getByTestId('ve-slot-panel')).toHaveAttribute(
      'data-slot',
      'skills'
    );
    expect(screen.queryByTestId('ve-inspector')).toBeNull();
    fireEvent.doubleClick(row);
    expect(screen.getAllByTestId('ve-slot-panel')).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.queryByTestId('ve-slot-panel')).toBeNull();
    fireEvent.keyDown(card(), { key: 'Enter' });
    expect(screen.getByTestId('ve-slot-panel')).toBeInTheDocument();
  });

  it('removes a skill from the dock and the card follows the file', () => {
    renderEditor(fakeEditor(), { initial: AGENT_SKILLS });
    fireEvent.doubleClick(card());
    fireEvent.click(dock().getByRole('button', { name: 'Remove skill tone' }));
    expect(state.text).not.toContain('- tone');
    expect(state.text).toContain('- refund-policy');
    expect(
      within(card()).getByTestId('ve-canvas-slot-count')
    ).toHaveTextContent('1');
  });
});

describe.skipIf(realSchema === null)('slots of an agent step', () => {
  it('opens the one panel of the built-in tools from any built-in row', () => {
    renderEditor(fakeEditor(), { initial: AGENT });
    fireEvent.click(screen.getByRole('button', { name: 'Read files' }));
    expect(panel()).toHaveAttribute('data-kind', 'builtin');
    expect(screen.getByTestId('ve-builtin-tools')).toBeInTheDocument();
  });

  it('picks a built-in tool to add, turns it on, and opens the panel', () => {
    renderEditor(fakeEditor(), { initial: AGENT });
    fireEvent.click(screen.getByRole('button', { name: 'Add a tool' }));
    // The built-in tool that is on is not offered again.
    const catalog = slotPanel()
      .getAllByTestId('ve-dock-row')
      .filter((row) => row.dataset.group === 'catalog');
    expect(catalog.map((row) => row.dataset.row)).not.toContain(
      'catalog:builtin:read'
    );
    fireEvent.change(searchBox(), { target: { value: 'Search files' } });
    fireEvent.keyDown(searchBox(), { key: 'Enter' });

    expect(state.text).toMatch(/read: true\s+search: true/);
    expect(panel()).toHaveAttribute('data-kind', 'builtin');
    expect(within(panel()).getByLabelText('Search files')).toBeChecked();
  });

  // The owner clicked "Add a tool" on a step that has every built-in tool on
  // and saw an empty panel.
  it('says that every built-in tool is on, and still offers the other kinds', () => {
    renderEditor(fakeEditor(), {
      initial: AGENT.replace(
        'read: true',
        'read: true\n          write: true\n          edit: true\n          search: true\n          shell:\n            allow: ["./test.sh"]'
      ),
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add a tool' }));
    const sections = slotPanel()
      .getAllByTestId('ve-dock-group')
      .filter((group) => group.dataset.section?.startsWith('catalog:'));
    // The DAGs have their own dock: this one offers the other kinds only.
    expect(sections.map((group) => group.dataset.section)).toEqual([
      'catalog:mcp',
      'catalog:builtin',
    ]);
    expect(within(sections[1]!).getByTestId('ve-dock-empty')).toHaveTextContent(
      'All built-in tools are enabled.'
    );
    // The new inline MCP server is always an item.
    expect(within(sections[0]!).getByTestId('ve-dock-row')).toHaveAttribute(
      'data-row',
      'catalog:mcp:new'
    );
  });

  it('adds an MCP server to an agent step from the add panel', () => {
    renderEditor(fakeEditor(), { initial: AGENT });
    fireEvent.click(screen.getByRole('button', { name: 'Add a tool' }));
    fireEvent.change(searchBox(), { target: { value: 'URL' } });
    fireEvent.keyDown(searchBox(), { key: 'Enter' });
    expect(state.text).toMatch(/mcp:\s+- name: ""/);
    expect(state.text).toContain('builtin:');
    expect(panel()).toHaveAttribute('data-kind', 'mcp');
    // The Tools card lists both kinds.
    const card = within(screen.getByRole('group', { name: 'Tools' }));
    expect(card.getAllByTestId('ve-canvas-slot-row')).toHaveLength(2);
  });

  it('shows the first characters of the prompt in a card, and opens a dock that edits the prompt and the system message', () => {
    renderEditor(fakeEditor(), { initial: AGENT });
    const card = screen.getByTestId('ve-canvas-node-toolsat:steps[0]:prompt');
    expect(card).toHaveTextContent('fix it');
    // The system message is not set: the card says so.
    expect(within(card).getByTestId('ve-canvas-prompt-system')).toHaveAttribute(
      'data-empty'
    );
    fireEvent.click(card);

    const dock = within(screen.getByTestId('ve-prompt-panel'));
    // The dock does not open the form or the YAML.
    expect(screen.queryByTestId('ve-inspector')).toBeNull();
    const system = dock.getByLabelText('System message');
    fireEvent.change(system, { target: { value: 'Be brief.' } });
    fireEvent.blur(system);
    expect(state.text).toMatch(/system: Be brief\./);
    const prompt = dock.getByLabelText('Prompt');
    fireEvent.change(prompt, { target: { value: 'fix **it** now' } });
    fireEvent.blur(prompt);
    expect(state.text).toMatch(/prompt: fix \*\*it\*\* now/);
    // The card follows the file.
    expect(
      within(screen.getByTestId('ve-canvas-node-toolsat:steps[0]:prompt'))
        .getByTestId('ve-canvas-prompt-text')
        .querySelector('strong')
    ).toHaveTextContent('it');
  });

  it('lists the built-in tools and the limits in the dock of the Tools list', () => {
    renderEditor(fakeEditor(), { initial: AGENT });
    fireEvent.doubleClick(
      screen.getByTestId('ve-canvas-node-toolsat:steps[0]:tools')
    );
    expect(stepRows().map((row) => row.dataset.row)).toEqual([
      'step:builtin:read',
    ]);
    // The limits of the tool loop are in the dock too, as in the card header.
    fireEvent.click(slotPanel().getByTestId('ve-dock-limits-row'));
    expect(slotPanel().getByTestId('ve-dock-limits')).toHaveTextContent(
      'Maximum number of tool iterations'
    );
    // The same dock offers what the step can add.
    expect(
      slotPanel()
        .getAllByTestId('ve-dock-group')
        .map((group) => group.dataset.section)
    ).toEqual(['step:builtin', 'catalog:mcp', 'catalog:builtin']);
  });

  it('opens the add panel from the Tools section of the form too', () => {
    renderEditor(fakeEditor(), { initial: AGENT });
    openForm('ve-canvas-node-steps[0]');
    const form = within(screen.getByTestId('ve-inspector'));
    fireEvent.click(form.getByTestId('ve-tool-add'));
    expect(screen.getByTestId('ve-slot-panel')).toHaveAttribute(
      'data-mode',
      'add'
    );
  });
});
