// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useQuery } from '@/hooks/api';
import type { DagEditOp } from '../../contracts';
import { parseDagDoc } from '../../doc';
import { ToolsDock, type ToolsDockProps } from '../ToolsDock';

vi.mock('@/hooks/api', () => ({ useQuery: vi.fn() }));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

const DAGS = [
  { name: 'lookup', description: 'Look up a ticket.', params: ['ticket'] },
  { name: 'refund', description: 'Refund an order.', params: [] },
  { name: 'notify', description: '', params: [] },
];

const DETAILS: Record<string, unknown> = {
  lookup: {
    description: 'Look up a ticket by its number.',
    paramDefs: [
      {
        name: 'ticket',
        type: 'string',
        required: true,
        description: 'The ticket number.',
      },
      { name: 'verbose', type: 'boolean', required: false },
    ],
  },
};

function serve(over: { servers?: string[]; dags?: typeof DAGS } = {}) {
  const dags = over.dags ?? DAGS;
  const servers = over.servers ?? ['docs', 'wiki'];
  (useQuery as unknown as ReturnType<typeof vi.fn>).mockImplementation(
    (
      path: string,
      init: { params?: { path?: { fileName?: string } } } | null
    ) => {
      if (init === null) return {};
      switch (path) {
        case '/dags':
          return {
            data: {
              dags: dags.map((dag) => ({
                dag: {
                  name: dag.name,
                  description: dag.description,
                  params: dag.params,
                },
              })),
            },
          };
        case '/dags/{fileName}': {
          const name = init.params?.path?.fileName ?? '';
          return name in DETAILS
            ? { data: { dag: DETAILS[name] } }
            : { error: new Error('not found') };
        }
        case '/ai/mcp-server-choices':
          return {
            data: {
              choices: servers.map((name) => ({ name, scope: 'global' })),
            },
          };
        default:
          return {};
      }
    }
  );
}

afterEach(() => vi.clearAllMocks());

const AGENT = `steps:
  - name: code
    action: agent.run
    with:
      provider: claude
      prompt: fix it
      max_tool_iterations: 12
      dags:
        - name: lookup
          params:
            ticket:
              from_ai:
                description: The ticket.
            verbose: true
        - name: ghost
      tools:
        mcp:
          - name: docs
            allow: [search, read]
        builtin:
          read: true
          shell:
            allow: ["go test *"]
            deny: ["rm *"]
`;

const AGENT_DAGS = `steps:
  - name: code
    action: agent.run
    with:
      provider: claude
      prompt: fix it
      max_tool_calls: 20
      dags:
        - name: lookup
`;

const CHAT = `steps:
  - name: triage
    action: chat.completion
    with:
      model: gpt-4o
      dags:
        - name: lookup
`;

const CHAT_MCP = `steps:
  - name: triage
    action: chat.completion
    with:
      model: gpt-4o
      dags:
        - name: lookup
      tools:
        mcp:
          - name: docs
            allow: [search]
`;

const EMPTY = `steps:
  - name: code
    action: agent.run
    with:
      provider: claude
      prompt: fix it
`;

type DockScope = ToolsDockProps['scope'];

// The accessible name of the search box depends on the dock.
let current: DockScope = 'tools';

function dock(
  text: string,
  over: Partial<ToolsDockProps> & {
    servers?: string[];
    dags?: typeof DAGS;
  } = {}
) {
  serve(over as { servers?: string[]; dags?: typeof DAGS });
  current = over.scope ?? 'tools';
  const model = parseDagDoc(text);
  const step = model.steps[0]!;
  const onEdit = vi.fn<(op: DagEditOp) => void>();
  const onOpenTool = vi.fn();
  const onPick = vi.fn();
  const onClose = vi.fn();
  const props: ToolsDockProps = {
    model,
    step,
    stepLabel: 'code',
    stepKind: text.includes('chat.completion') ? 'chat' : 'agent',
    scope: 'tools',
    fieldIssues: {},
    locked: false,
    dag: { name: 'self' },
    adding: false,
    onEdit,
    onOpenTool,
    onPick,
    onClose,
    ...over,
  };
  render(<ToolsDock {...props} />);
  return { onEdit, onOpenTool, onPick, onClose, step };
}

const search = () =>
  screen.getByRole('combobox', {
    name: current === 'dags' ? 'Search DAGs' : 'Search tools',
  });
const rowIds = () =>
  screen.queryAllByTestId('ve-dock-row').map((row) => row.dataset.row);
const sectionIds = () =>
  screen.queryAllByTestId('ve-dock-group').map((s) => s.dataset.section);
const selectedRow = () =>
  screen
    .getAllByTestId('ve-dock-row')
    .find((row) => row.getAttribute('aria-selected') === 'true');
const detail = () => within(screen.getByTestId('ve-dock-detail'));
const key = (name: string, init: Record<string, unknown> = {}) =>
  fireEvent.keyDown(search(), { key: name, ...init });
const row = (id: string) =>
  screen.getAllByTestId('ve-dock-row').find((item) => item.dataset.row === id)!;

describe('the DAGs dock of an agent step', () => {
  const dags = { scope: 'dags' } as const;

  it('lists the DAG tools of the step and the DAGs to add, with no heading of a kind', () => {
    dock(AGENT, dags);
    expect(screen.getByTestId('ve-tools-dock')).toHaveAttribute(
      'data-scope',
      'dags'
    );
    expect(sectionIds()).toEqual(['step:dag', 'catalog:dag']);
    // A dock of one kind has nothing to fold: the two lists are open.
    expect(rowIds()).toEqual([
      'step:dags[0]',
      'step:dags[1]',
      'catalog:dag:refund',
      'catalog:dag:notify',
    ]);
    const groups = screen.getAllByTestId('ve-dock-group');
    expect(groups.map((g) => g.getAttribute('aria-expanded'))).toEqual([
      'true',
      'true',
    ]);
    expect(screen.queryByTestId('ve-dock-section')).toBeNull();
    expect(screen.getByRole('tree')).toHaveAccessibleName('DAGs');
    // The first row is selected and its detail shows.
    expect(selectedRow()).toHaveAttribute('data-row', 'step:dags[0]');
    expect(screen.getByTestId('ve-dock-detail-title')).toHaveTextContent(
      'lookup'
    );
  });

  // The MCP servers and the built-in tools are in the Tools dock.
  it('shows no MCP server and no built-in tool, in the list, the counts and the search', () => {
    dock(AGENT, dags);
    expect(screen.queryByText('docs')).toBeNull();
    expect(screen.queryByText('Read files')).toBeNull();
    expect(screen.getByText('In this step').parentElement).toHaveTextContent(
      'In this step2'
    );
    fireEvent.change(search(), { target: { value: 'docs' } });
    expect(rowIds()).toEqual([]);
    fireEvent.change(search(), { target: { value: 'read' } });
    expect(rowIds()).toEqual([]);
    expect(screen.getByTestId('ve-dock-no-match')).toHaveTextContent(
      'No DAG matches the search.'
    );
  });

  it('gives a row its icon, name, description and facts, and no kind badge', () => {
    dock(AGENT, dags);
    const [lookup] = screen.getAllByTestId('ve-dock-row');
    expect(lookup).toHaveTextContent('lookup');
    expect(lookup).toHaveTextContent('Look up a ticket.');
    expect(within(lookup!).queryByTestId('ve-tool-kind')).toBeNull();
    expect(lookup!.querySelector('[data-kind-icon="dag"]')).not.toBeNull();
    expect(lookup).toHaveTextContent('1 fixed');
    expect(lookup).toHaveTextContent('1 from the model');
  });

  it('starts on the first DAG to add when the dock opens to add', () => {
    dock(AGENT, { ...dags, adding: true });
    expect(screen.getByTestId('ve-tools-dock')).toHaveAttribute(
      'data-mode',
      'add'
    );
    expect(search()).toHaveFocus();
    // The DAGs of the step are not offered again.
    expect(rowIds()).toEqual([
      'step:dags[0]',
      'step:dags[1]',
      'catalog:dag:refund',
      'catalog:dag:notify',
    ]);
    expect(selectedRow()).toHaveAttribute('data-row', 'catalog:dag:refund');
    expect(sectionIds()).toEqual(['step:dag', 'catalog:dag']);
  });

  it('filters with the search, opens the folded sections, and clears with Escape', () => {
    const { onClose } = dock(AGENT, dags);
    fireEvent.change(search(), { target: { value: 'refund' } });
    // Matches nothing of the step, and the catalogue DAG: its section opens.
    expect(rowIds()).toEqual(['catalog:dag:refund']);
    fireEvent.change(search(), { target: { value: 'zzzz' } });
    expect(rowIds()).toEqual([]);
    key('Escape');
    expect(search()).toHaveValue('');
    expect(onClose).not.toHaveBeenCalled();
    key('Escape');
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('moves with the arrows, home and end over the rows, with no stop on a heading', () => {
    dock(AGENT, dags);
    key('ArrowDown');
    expect(selectedRow()).toHaveAttribute('data-row', 'step:dags[1]');
    expect(screen.getByTestId('ve-dock-detail-title')).toHaveTextContent(
      'ghost'
    );
    key('ArrowDown');
    expect(selectedRow()).toHaveAttribute('data-row', 'catalog:dag:refund');
    // The step has a Tools card: the limits are in the Tools dock.
    key('End');
    expect(selectedRow()).toHaveAttribute('data-row', 'catalog:dag:notify');
    expect(screen.queryByTestId('ve-dock-limits-row')).toBeNull();
    key('Home');
    expect(selectedRow()).toHaveAttribute('data-row', 'step:dags[0]');
  });

  it('keeps the two lists open: there is no heading to fold', () => {
    dock(AGENT, dags);
    key('ArrowLeft');
    key('ArrowRight');
    expect(rowIds()).toEqual([
      'step:dags[0]',
      'step:dags[1]',
      'catalog:dag:refund',
      'catalog:dag:notify',
    ]);
  });

  it('opens the panel of a DAG tool from Enter, from the Edit button and from a double click', () => {
    const { onOpenTool, step } = dock(AGENT, dags);
    key('Enter');
    expect(onOpenTool).toHaveBeenLastCalledWith({
      step: step.key,
      category: 'dags',
      index: 0,
    });
    fireEvent.click(detail().getByRole('button', { name: 'Edit' }));
    expect(onOpenTool).toHaveBeenCalledTimes(2);
    fireEvent.click(row('step:dags[1]'), { detail: 1 });
    fireEvent.click(row('step:dags[1]'), { detail: 2 });
    expect(onOpenTool).toHaveBeenLastCalledWith({
      step: step.key,
      category: 'dags',
      index: 1,
    });
  });

  it('removes the DAG tool from Delete and from its detail, with the same operation', () => {
    const { onEdit, step } = dock(AGENT, dags);
    key('Delete');
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'editStepTools',
      step: step.key,
      change: { kind: 'remove', category: 'dags', index: 0 },
    });
    fireEvent.click(
      detail().getByRole('button', { name: 'Remove tool lookup' })
    );
    expect(onEdit).toHaveBeenCalledTimes(2);
  });

  it('does not remove on Delete while the search has text', () => {
    const { onEdit } = dock(AGENT, dags);
    fireEvent.change(search(), { target: { value: 'look' } });
    key('Delete');
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('adds a catalogue DAG from Enter, with the pick that the editor opens a panel for', () => {
    const { onPick, onEdit } = dock(AGENT, { ...dags, adding: true });
    key('Enter');
    expect(onPick).toHaveBeenCalledWith({
      category: 'dags',
      entry: { name: 'refund' },
    });
    expect(onEdit).not.toHaveBeenCalled();
    fireEvent.click(detail().getByTestId('ve-dock-add'));
    expect(onPick).toHaveBeenCalledTimes(2);
  });

  it('adds and stays on the list from Shift+Enter, with the operations of the editor', () => {
    const { onPick, onEdit, step } = dock(AGENT, { ...dags, adding: true });
    key('Enter', { shiftKey: true });
    expect(onPick).not.toHaveBeenCalled();
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'editStepTools',
      step: step.key,
      change: { kind: 'add', category: 'dags', entry: { name: 'refund' } },
    });
  });

  it('shows what the add writes: the key dags, not the tool map', () => {
    dock(AGENT, { ...dags, adding: true });
    expect(screen.getByTestId('ve-dock-add-yaml').textContent).toBe(
      'dags:\n  - name: refund'
    );
  });

  it('shows the inputs of a DAG tool: from the model, fixed, and open', () => {
    dock(AGENT, dags);
    const inputs = within(screen.getByTestId('ve-dock-inputs'));
    const lines = inputs.getAllByTestId('ve-dock-input');
    expect(lines.map((line) => line.dataset.state)).toEqual([
      'from_ai',
      'fixed',
    ]);
    expect(lines[0]).toHaveTextContent('ticket');
    expect(lines[0]).toHaveTextContent('string');
    expect(lines[0]).toHaveTextContent('required');
    expect(lines[0]).toHaveTextContent('The model reads: The ticket.');
    expect(lines[1]).toHaveTextContent('Fixed');
    expect(lines[1]).toHaveTextContent('true');
    expect(screen.getByText('Look up a ticket by its number.')).toBeVisible();
    // The YAML of the entry shows the entry: no `tools` and no `dags` key.
    expect(screen.getByTestId('ve-dock-yaml').textContent).toContain(
      'name: lookup'
    );
    // The slot of the miniature graph is there, empty and labelled.
    const slot = screen.getByTestId('ve-tool-graph-slot');
    expect(slot).toBeEmptyDOMElement();
    expect(slot).toHaveClass('empty:hidden');
    expect(slot).toHaveAttribute('aria-label', 'Workflow graph');
  });

  // The DAG of a tool has its miniature in the slot of the detail. A tool of the
  // step opens the enlarged view from it; a DAG of the catalogue has no such tool.
  it('draws the miniature of the DAG in the detail, and opens the enlarged view of a tool of the step', () => {
    DETAILS.lookup = {
      ...(DETAILS.lookup as object),
      steps: [
        { name: 'fetch', depends: [] },
        { name: 'reply', depends: ['fetch'] },
      ],
    };
    const onOpenDag = vi.fn();
    try {
      dock(AGENT, { ...dags, onOpenDag });
      const open = screen.getByTestId('ve-tool-graph-open');
      expect(open).toHaveAccessibleName('Show the graph of lookup');
      expect(
        within(open).getByTestId('ve-dag-mini-svg').querySelectorAll('rect')
      ).toHaveLength(2);
      expect(open).toHaveTextContent('2 steps');
      fireEvent.click(open);
      expect(onOpenDag).toHaveBeenCalledWith({
        step: 'steps[0]',
        category: 'dags',
        index: 0,
      });
    } finally {
      delete (DETAILS.lookup as { steps?: unknown }).steps;
    }
  });

  it('shows the miniature without the enlarge button when the dock cannot open the view', () => {
    DETAILS.lookup = {
      ...(DETAILS.lookup as object),
      steps: [{ name: 'only', depends: [] }],
    };
    try {
      dock(AGENT, dags);
      expect(screen.queryByTestId('ve-tool-graph-open')).toBeNull();
      expect(
        within(screen.getByTestId('ve-tool-graph-slot')).getByTestId(
          've-dag-mini-svg'
        )
      ).toBeInTheDocument();
    } finally {
      delete (DETAILS.lookup as { steps?: unknown }).steps;
    }
  });

  it('warns about a DAG that the server does not know', () => {
    dock(AGENT, dags);
    key('ArrowDown');
    expect(screen.getByRole('alert')).toHaveTextContent(
      'The server does not know the DAG "ghost".'
    );
  });

  // The limits sit on the Tools card, and on the DAGs card of a step with no Tools card.
  it('shows the limits only when the step has no Tools card', () => {
    dock(AGENT, dags);
    expect(screen.queryByTestId('ve-dock-limits-row')).toBeNull();
    cleanup();
    const { onEdit, step } = dock(AGENT_DAGS, dags);
    fireEvent.click(screen.getByTestId('ve-dock-limits-row'));
    const calls = detail().getByLabelText('Maximum number of tool calls');
    expect(calls).toHaveValue('20');
    fireEvent.change(calls, { target: { value: '7' } });
    fireEvent.blur(calls);
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'setStepField',
      step: step.key,
      path: ['with', 'max_tool_calls'],
      value: 7,
    });
  });

  it('is read-only in a view that only reads: no catalogue, no remove', () => {
    const { onEdit } = dock(AGENT, { ...dags, locked: true });
    expect(sectionIds()).toEqual(['step:dag']);
    expect(screen.queryByTestId('ve-dock-remove')).toBeNull();
    expect(screen.queryByTestId('ve-dock-row-remove')).toBeNull();
    expect(screen.getByTestId('ve-dock-edit')).toBeEnabled();
    key('Delete');
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('puts the issues of an entry on its row and at the top of its detail', () => {
    dock(AGENT, {
      ...dags,
      fieldIssues: {
        'with.dags[0].params.verbose': [
          {
            severity: 'error',
            source: 'build',
            message: 'Must be a string',
          } as never,
        ],
      },
    });
    const [lookup] = screen.getAllByTestId('ve-dock-row');
    expect(within(lookup!).getByTestId('ve-dock-issues')).toHaveAttribute(
      'data-severity',
      'error'
    );
    expect(screen.getByTestId('ve-dock-detail-issues')).toHaveTextContent(
      'Must be a string'
    );
  });

  it('moves the focus to the search from the slash key', () => {
    dock(AGENT, dags);
    screen.getByTestId('ve-dock-edit').focus();
    fireEvent.keyDown(screen.getByTestId('ve-dock-edit'), { key: '/' });
    expect(search()).toHaveFocus();
  });

  it('folds the detail of a narrow dock to its title', () => {
    dock(AGENT, dags);
    const toggle = screen.getByTestId('ve-dock-detail-toggle');
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByTestId('ve-dock-detail')).toHaveClass('hidden');
  });
});

describe('the Tools dock of an agent step', () => {
  it('lists the MCP servers and the built-in tools by kind, with a count, and folds the catalogue', () => {
    dock(AGENT);
    expect(screen.getByTestId('ve-tools-dock')).toHaveAttribute(
      'data-scope',
      'tools'
    );
    expect(sectionIds()).toEqual([
      'step:mcp',
      'step:builtin',
      'catalog:mcp',
      'catalog:builtin',
    ]);
    // The catalogue is folded when the dock opens on the tools.
    expect(rowIds()).toEqual([
      'step:mcp[0]',
      'step:builtin:read',
      'step:builtin:shell',
    ]);
    const groups = screen.getAllByTestId('ve-dock-group');
    expect(groups.map((g) => g.getAttribute('aria-expanded'))).toEqual([
      'true',
      'true',
      'false',
      'false',
    ]);
    expect(groups[0]).toHaveAccessibleName('MCP servers 1');
    expect(screen.getByRole('tree')).toHaveAccessibleName('Tools');
    // The first row is selected and its detail shows.
    expect(selectedRow()).toHaveAttribute('data-row', 'step:mcp[0]');
    expect(screen.getByTestId('ve-dock-detail-title')).toHaveTextContent(
      'docs'
    );
  });

  // The DAG tools are in the DAGs dock.
  it('shows no DAG, in the list, the catalogue, the counts and the search', () => {
    dock(AGENT);
    expect(screen.queryByText('lookup')).toBeNull();
    expect(screen.getByText('In this step').parentElement).toHaveTextContent(
      'In this step3'
    );
    fireEvent.change(search(), { target: { value: 'refund' } });
    expect(rowIds()).toEqual([]);
    expect(screen.getByTestId('ve-dock-no-match')).toHaveTextContent(
      'No tool matches the search.'
    );
    fireEvent.change(search(), { target: { value: 'lookup' } });
    expect(rowIds()).toEqual([]);
  });

  it('gives a row its icon, name, description, kind badge and facts', () => {
    dock(AGENT);
    const docs = row('step:mcp[0]');
    expect(docs).toHaveTextContent('docs');
    expect(within(docs).getByTestId('ve-tool-kind')).toHaveTextContent('MCP');
    expect(docs.querySelector('[data-kind-icon="mcp"]')).not.toBeNull();
    expect(docs).toHaveTextContent('Allow: search, read');
    const shell = row('step:builtin:shell');
    expect(shell).toHaveTextContent('Allow: go test *');
    expect(shell).toHaveTextContent('Deny: rm *');
  });

  it('opens on the catalogue, and folds the tools of the step, when the dock opens to add', () => {
    dock(AGENT, { adding: true });
    expect(screen.getByTestId('ve-tools-dock')).toHaveAttribute(
      'data-mode',
      'add'
    );
    expect(search()).toHaveFocus();
    expect(rowIds()).toEqual([
      // A server that the step has is not offered again.
      'catalog:mcp:wiki',
      'catalog:mcp:new',
      'catalog:builtin:write',
      'catalog:builtin:edit',
      'catalog:builtin:search',
    ]);
  });

  it('filters every kind of the dock with the search, opens the folded sections, and clears with Escape', () => {
    const { onClose } = dock(AGENT);
    fireEvent.change(search(), { target: { value: 'wiki' } });
    // Matches nothing of the step, and the catalogue server: its section opens.
    expect(rowIds()).toEqual(['catalog:mcp:wiki']);
    fireEvent.change(search(), { target: { value: 'search' } });
    // The allowed tool of a server and the built-in tool: both kinds at once.
    expect(rowIds()).toEqual(['step:mcp[0]', 'catalog:builtin:search']);
    fireEvent.change(search(), { target: { value: 'zzzz' } });
    expect(rowIds()).toEqual([]);
    expect(screen.getByTestId('ve-dock-no-match')).toHaveTextContent(
      'No tool matches the search.'
    );
    key('Escape');
    expect(search()).toHaveValue('');
    expect(onClose).not.toHaveBeenCalled();
    key('Escape');
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('moves with the arrows, home and end, and follows with the detail', () => {
    dock(AGENT);
    key('ArrowDown');
    // The heading of the next section is an item: the keyboard stops on it.
    expect(search()).toHaveAttribute(
      'aria-activedescendant',
      screen.getAllByTestId('ve-dock-group')[1]!.id
    );
    key('ArrowDown');
    expect(selectedRow()).toHaveAttribute('data-row', 'step:builtin:read');
    expect(screen.getByTestId('ve-dock-detail-title')).toHaveTextContent(
      'Read files'
    );
    // The limits come first: they hold for the whole step.
    key('Home');
    expect(screen.getByTestId('ve-dock-limits-row')).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(screen.getByTestId('ve-dock-limits')).toBeInTheDocument();
    key('ArrowDown');
    expect(search()).toHaveAttribute(
      'aria-activedescendant',
      screen.getAllByTestId('ve-dock-group')[0]!.id
    );
  });

  it('folds and unfolds a section from the keyboard and from a click', () => {
    dock(AGENT);
    key('Home');
    key('ArrowDown');
    key('ArrowLeft');
    expect(rowIds()).not.toContain('step:mcp[0]');
    key('ArrowRight');
    expect(rowIds()).toContain('step:mcp[0]');
    fireEvent.click(
      within(screen.getAllByTestId('ve-dock-group')[2]!).getByTestId(
        've-dock-section'
      )
    );
    expect(rowIds()).toContain('catalog:mcp:wiki');
    // Left on a row goes to its heading.
    key('ArrowDown');
    key('ArrowLeft');
    expect(search()).toHaveAttribute(
      'aria-activedescendant',
      screen.getAllByTestId('ve-dock-group')[2]!.id
    );
  });

  it('opens the panel of a tool from Enter, from the Edit button and from a double click', () => {
    const { onOpenTool, step } = dock(AGENT);
    key('Enter');
    expect(onOpenTool).toHaveBeenLastCalledWith({
      step: step.key,
      category: 'mcp',
      index: 0,
    });
    fireEvent.click(detail().getByRole('button', { name: 'Edit' }));
    expect(onOpenTool).toHaveBeenCalledTimes(2);
    const shell = row('step:builtin:shell');
    fireEvent.click(shell, { detail: 1 });
    fireEvent.click(shell, { detail: 2 });
    // The built-in tools are one entry: place 0.
    expect(onOpenTool).toHaveBeenLastCalledWith({
      step: step.key,
      category: 'builtin',
      index: 0,
    });
  });

  it('removes the tool from Delete, from its detail and from the row, with the same operations', () => {
    const { onEdit, step } = dock(AGENT);
    key('Delete');
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'editStepTools',
      step: step.key,
      change: { kind: 'remove', category: 'mcp', index: 0 },
    });
    fireEvent.click(detail().getByRole('button', { name: 'Remove tool docs' }));
    expect(onEdit).toHaveBeenCalledTimes(2);
    // A built-in tool is a key of with.tools.builtin: the bin removes it, as for the others.
    fireEvent.click(
      within(row('step:builtin:read')).getByTestId('ve-dock-row-remove')
    );
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'setStepField',
      step: step.key,
      path: ['with', 'tools', 'builtin', 'read'],
      value: undefined,
    });
  });

  it('does not remove on Delete while the search has text', () => {
    const { onEdit } = dock(AGENT);
    fireEvent.change(search(), { target: { value: 'doc' } });
    key('Delete');
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('adds a catalogue tool from Enter, with the pick that the editor opens a panel for', () => {
    const { onPick, onEdit } = dock(AGENT, { adding: true });
    key('Enter');
    expect(onPick).toHaveBeenCalledWith({
      category: 'mcp',
      entry: { name: 'wiki', allow: [] },
    });
    expect(onEdit).not.toHaveBeenCalled();
    fireEvent.click(detail().getByTestId('ve-dock-add'));
    expect(onPick).toHaveBeenCalledTimes(2);
  });

  it('adds and stays on the list from Shift+Enter, with the operations of the editor', () => {
    const { onPick, onEdit, step } = dock(AGENT, { adding: true });
    key('Enter', { shiftKey: true });
    expect(onPick).not.toHaveBeenCalled();
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'editStepTools',
      step: step.key,
      change: {
        category: 'mcp',
        kind: 'add',
        entry: { name: 'wiki', allow: [] },
      },
    });
    // A built-in tool is a key; the shell tool starts with an empty allow list.
    fireEvent.change(search(), { target: { value: 'shell' } });
    expect(rowIds()).toEqual(['step:builtin:shell']);
    fireEvent.change(search(), { target: { value: 'write' } });
    key('Enter', { shiftKey: true });
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'setStepField',
      step: step.key,
      path: ['with', 'tools', 'builtin', 'write'],
      value: true,
    });
  });

  it('shows what the add writes for a catalogue tool', () => {
    dock(AGENT, { adding: true });
    // The first row is the server wiki.
    expect(screen.getByTestId('ve-dock-detail-title')).toHaveTextContent(
      'wiki'
    );
    expect(screen.getByTestId('ve-dock-add-yaml').textContent).toBe(
      'tools:\n  mcp:\n    - name: wiki\n      allow: []'
    );
  });

  it('adds a named server and a new inline server', () => {
    const { onPick } = dock(AGENT, { adding: true });
    fireEvent.change(search(), { target: { value: 'wiki' } });
    key('Enter');
    expect(onPick).toHaveBeenLastCalledWith({
      category: 'mcp',
      entry: { name: 'wiki', allow: [] },
    });
    fireEvent.change(search(), { target: { value: 'url' } });
    key('Enter');
    expect(onPick).toHaveBeenLastCalledWith({
      category: 'mcp',
      entry: { name: '', url: '', allow: [] },
    });
  });

  it('shows the allowed tools of an MCP server, and the rules of the shell tool', () => {
    dock(AGENT);
    fireEvent.click(row('step:mcp[0]'));
    expect(
      within(screen.getByTestId('ve-dock-allow'))
        .getAllByRole('listitem')
        .map((item) => item.textContent)
    ).toEqual(['search', 'read']);
    expect(
      detail().getByText('The server entry docs of the AI page.')
    ).toBeVisible();
    expect(
      detail().getByRole('link', { name: /Open the MCP servers/ })
    ).toBeInTheDocument();
    // The YAML of the entry is the entry of the tool map.
    expect(screen.getByTestId('ve-dock-yaml').textContent).toContain(
      'name: docs'
    );
  });

  it('edits the shell rules and the switch of a built-in tool in the detail', () => {
    const { onEdit, step } = dock(AGENT);
    fireEvent.click(row('step:builtin:shell'));
    const shell = within(screen.getByTestId('ve-dock-shell'));
    fireEvent.change(shell.getAllByLabelText('New item')[0]!, {
      target: { value: 'make *' },
    });
    fireEvent.click(shell.getAllByRole('button', { name: 'Add item' })[0]!);
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'setStepField',
      step: step.key,
      path: ['with', 'tools', 'builtin', 'shell', 'allow'],
      value: ['go test *', 'make *'],
    });
    fireEvent.click(shell.getByRole('button', { name: 'Remove rm *' }));
    // The last deny pattern goes: the key goes too.
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'setStepField',
      step: step.key,
      path: ['with', 'tools', 'builtin', 'shell', 'deny'],
      value: undefined,
    });
    expect(detail().queryByRole('switch')).toBeNull();
    fireEvent.click(
      detail().getByRole('button', { name: 'Remove tool Shell commands' })
    );
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'setStepField',
      step: step.key,
      path: ['with', 'tools', 'builtin', 'shell'],
      value: undefined,
    });
  });

  it('shows the limits as a block, and edits them with the operations of the step form', () => {
    const { onEdit, step } = dock(AGENT);
    fireEvent.click(screen.getByTestId('ve-dock-limits-row'));
    const figures = screen
      .getAllByTestId('ve-dock-limit-figures')
      .map((node) => node.textContent);
    expect(figures[0]).toBe('Turns12Calls100 (default)Timeoutnone');
    const turns = detail().getByLabelText('Maximum number of tool iterations');
    expect(turns).toHaveValue('12');
    const calls = detail().getByLabelText('Maximum number of tool calls');
    expect(calls).toHaveAttribute('placeholder', 'Default: 100');
    fireEvent.change(calls, { target: { value: '7' } });
    fireEvent.blur(calls);
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'setStepField',
      step: step.key,
      path: ['with', 'max_tool_calls'],
      value: 7,
    });
  });

  it('is read-only in a view that only reads: no catalogue, no remove, no switch', () => {
    const { onEdit } = dock(AGENT, { locked: true });
    expect(sectionIds()).toEqual(['step:mcp', 'step:builtin']);
    expect(screen.queryByTestId('ve-dock-remove')).toBeNull();
    expect(screen.queryByTestId('ve-dock-row-remove')).toBeNull();
    expect(screen.queryAllByTestId('ve-dock-row-switch')).toEqual([]);
    expect(screen.getByTestId('ve-dock-edit')).toBeEnabled();
    key('Delete');
    expect(onEdit).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('ve-dock-limits-row'));
    expect(
      screen.getByLabelText('Maximum number of tool iterations')
    ).toBeDisabled();
  });

  it('puts the issues of an entry on its row and at the top of its detail', () => {
    dock(AGENT, {
      fieldIssues: {
        'with.tools.mcp[0].allow': [
          {
            severity: 'error',
            source: 'build',
            message: 'Must not be empty',
          } as never,
        ],
      },
    });
    const [docs] = screen.getAllByTestId('ve-dock-row');
    expect(within(docs!).getByTestId('ve-dock-issues')).toHaveAttribute(
      'data-severity',
      'error'
    );
    expect(screen.getByTestId('ve-dock-detail-issues')).toHaveTextContent(
      'Must not be empty'
    );
  });

  it('moves the focus to the search from the slash key', () => {
    dock(AGENT);
    screen.getByTestId('ve-dock-edit').focus();
    fireEvent.keyDown(screen.getByTestId('ve-dock-edit'), { key: '/' });
    expect(search()).toHaveFocus();
  });
});

describe('the docks: empty states', () => {
  it('says that the step has no DAG, and offers the catalogue', () => {
    dock(EMPTY, { scope: 'dags' });
    expect(screen.getByTestId('ve-dock-no-tools')).toHaveTextContent(
      'No DAGs yet.'
    );
    // Nothing to fold: the catalogue is open.
    expect(rowIds()).toContain('catalog:dag:lookup');
    expect(sectionIds()).toEqual(['catalog:dag']);
    expect(screen.getAllByTestId('ve-dock-group')[0]).toHaveAttribute(
      'aria-expanded',
      'true'
    );
  });

  it('says that the step has no tool, and offers the catalogue of the Tools dock', () => {
    dock(EMPTY);
    expect(screen.getByTestId('ve-dock-no-tools')).toHaveTextContent(
      'No tools yet.'
    );
    expect(sectionIds()).toEqual(['catalog:mcp', 'catalog:builtin']);
    // No DAG is offered here.
    expect(rowIds().some((id) => id?.startsWith('catalog:dag'))).toBe(false);
  });

  it('says why the DAGs of the catalogue are not offered', () => {
    dock(EMPTY, { scope: 'dags', dags: [] });
    const notes = screen.getAllByTestId('ve-dock-empty');
    expect(notes.map((note) => note.dataset.reason)).toEqual(['no-dags']);
    expect(notes[0]).toHaveTextContent('No other DAG found.');
    // No note about the servers: they are in the Tools dock.
    expect(screen.queryByText(/MCP server entry/)).toBeNull();
  });

  it('says why a section of the catalogue has no row, with a link to the AI page for servers', () => {
    dock(EMPTY, { servers: [] });
    const notes = screen.getAllByTestId('ve-dock-empty');
    expect(notes.map((note) => note.dataset.reason)).toEqual(['no-servers']);
    expect(
      within(notes[0]!).getByRole('link', { name: /Open the MCP servers/ })
    ).toHaveAttribute('href', '/ai/mcp');
    // The new inline server is still offered.
    expect(rowIds()).toContain('catalog:mcp:new');
  });

  it('says that every built-in tool is on', () => {
    dock(
      EMPTY.replace(
        'prompt: fix it',
        'prompt: fix it\n      tools:\n        builtin:\n          read: true\n          write: true\n          edit: true\n          search: true\n          shell:\n            allow: [x]'
      )
    );
    key('Escape');
    fireEvent.click(
      within(
        screen
          .getAllByTestId('ve-dock-group')
          .find((g) => g.dataset.section === 'catalog:builtin')!
      ).getByTestId('ve-dock-section')
    );
    expect(
      screen
        .getAllByTestId('ve-dock-empty')
        .find((n) => n.dataset.reason === 'all-builtin')
    ).toHaveTextContent('All built-in tools are enabled.');
  });

  it('shows what the DAG tools are when the search of the DAGs dock lists nothing', () => {
    dock(EMPTY, { scope: 'dags' });
    fireEvent.change(search(), { target: { value: 'zzzz' } });
    const empty = screen.getByTestId('ve-dock-detail-empty');
    expect(empty).toHaveTextContent('Choose a DAG');
    expect(empty).toHaveTextContent('DAG tool');
    expect(empty).not.toHaveTextContent('MCP server');
    expect(empty).not.toHaveTextContent('Built-in tool');
  });

  it('shows what the other kinds are when the search of the Tools dock lists nothing', () => {
    dock(EMPTY);
    fireEvent.change(search(), { target: { value: 'zzzz' } });
    const empty = screen.getByTestId('ve-dock-detail-empty');
    expect(empty).toHaveTextContent('Choose a tool');
    expect(empty).not.toHaveTextContent('DAG tool');
    expect(empty).toHaveTextContent('MCP server');
    expect(empty).toHaveTextContent('Built-in tool');
  });
});

describe('the docks of a chat step', () => {
  it('has no built-in tools in the Tools dock, says why, and has the two limits of the catalogue', () => {
    const { onEdit, step } = dock(CHAT_MCP);
    expect(sectionIds()).toEqual(['step:mcp', 'catalog:mcp']);
    expect(screen.getByTestId('ve-dock-builtin-note')).toHaveTextContent(
      'Valid on agent.run steps only.'
    );
    fireEvent.click(screen.getByTestId('ve-dock-limits-row'));
    const max = detail().getByLabelText('Maximum number of tools');
    expect(max).toHaveAttribute('placeholder', 'Default: 64');
    fireEvent.change(max, { target: { value: '5' } });
    fireEvent.blur(max);
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'editStepTools',
      step: step.key,
      change: { kind: 'setLimit', key: 'max_tools', value: 5 },
    });
  });

  it('lists the DAG tools in the DAGs dock, with the limits when no Tools card holds them', () => {
    dock(CHAT, { scope: 'dags' });
    expect(sectionIds()).toEqual(['step:dag', 'catalog:dag']);
    expect(screen.queryByTestId('ve-dock-builtin-note')).toBeNull();
    expect(screen.getByTestId('ve-dock-limits-row')).toBeInTheDocument();
    // The Tools dock of that step has none: they are on the DAGs card.
    cleanup();
    dock(CHAT);
    expect(sectionIds()).toEqual(['catalog:mcp']);
    expect(screen.queryByTestId('ve-dock-limits-row')).toBeNull();
  });

  it('leaves the limits to the Tools dock when the step has an MCP server', () => {
    dock(CHAT_MCP, { scope: 'dags' });
    expect(screen.queryByTestId('ve-dock-limits-row')).toBeNull();
    cleanup();
    dock(CHAT_MCP);
    expect(sectionIds()).toEqual(['step:mcp', 'catalog:mcp']);
    expect(screen.getByTestId('ve-dock-limits-row')).toBeInTheDocument();
  });

  // A step with neither card has no home for the limits on the canvas: the Tools dock shows them.
  it('shows the limits in the Tools dock of a step with no tool at all, and not in the DAGs dock', () => {
    dock(EMPTY);
    expect(screen.getByTestId('ve-dock-limits-row')).toBeInTheDocument();
    cleanup();
    dock(EMPTY, { scope: 'dags' });
    expect(screen.queryByTestId('ve-dock-limits-row')).toBeNull();
  });
});
