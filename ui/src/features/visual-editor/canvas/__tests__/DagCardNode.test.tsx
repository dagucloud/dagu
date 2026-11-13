// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, within } from '@testing-library/react';
import type { ReactElement } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Status } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import type { CanvasNode } from '../../contracts';
import { readStepTools } from '../../tools/parse';
import { CanvasActionsContext, type CanvasActions } from '../context';
import { FlowProvider, type NodeProps } from '../engine';
import { DagCardNode } from '../nodes/DagCardNode';
import {
  DAG_CARD_MAX_ENTRIES,
  DAG_ENTRY_HEIGHT,
  SUB_TILE_SIZE,
} from '../subRow';
import { ToolRunContext, type ToolRunInfo } from '../toolRun';
import type {
  CanvasNodeWithRoutes,
  SlotCardData,
  StepFlowNode,
} from '../types';

vi.mock('@/hooks/api', () => ({ useQuery: vi.fn() }));

const appBar = {
  title: 'DAGs',
  setTitle: vi.fn(),
  remoteNodes: ['local'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'local',
  selectRemoteNode: vi.fn(),
};

interface Served {
  dags?: Record<string, unknown>;
  sub?: unknown;
}

const requests: string[] = [];

// The server: the DAGs by name (a name that is not in the list is a 404) and
// the details of one sub-run.
function serve({ dags = {}, sub }: Served = {}) {
  requests.length = 0;
  (useQuery as unknown as ReturnType<typeof vi.fn>).mockImplementation(
    (
      path: string,
      init: { params?: { path?: { fileName?: string } } } | null
    ) => {
      if (init === null) return { data: undefined };
      if (path === '/dags/{fileName}') {
        const name = init.params?.path?.fileName ?? '';
        requests.push(name);
        const dag = dags[name];
        return dag
          ? { data: { dag } }
          : { data: undefined, error: new Error('404') };
      }
      if (path.includes('/sub-dag-runs/')) {
        return { data: sub ? { dagRunDetails: sub } : undefined };
      }
      return { data: undefined };
    }
  );
}

afterEach(() => vi.clearAllMocks());

const step = (name: string, depends: string[] = [], over = {}) => ({
  name,
  depends,
  ...over,
});

const TRIAGE = {
  name: 'triage-ticket',
  description: 'Read a ticket and route it.',
  paramDefs: [
    {
      name: 'ticket_id',
      type: 'string',
      required: true,
      description: 'The id.',
    },
    { name: 'lang', type: 'string', required: false, default: 'en' },
    { name: 'verbose', type: 'boolean', required: false },
    { name: 'extra', type: 'string', required: false },
  ],
  steps: [
    step('fetch'),
    step('classify', ['fetch']),
    step('route', ['classify'], { preconditions: [{ condition: '${X}' }] }),
  ],
};

const DAGS = {
  'triage-ticket': TRIAGE,
  lookup: {
    name: 'lookup',
    description: '',
    paramDefs: [],
    steps: [step('a')],
  },
  empty: { name: 'empty', description: 'Nothing.', paramDefs: [], steps: [] },
  big: {
    name: 'big',
    description: 'A batch.',
    paramDefs: [],
    steps: Array.from({ length: 100 }, (_, i) =>
      step(`s${i}`, i < 10 ? [] : [`s${i - 10}`])
    ),
  },
};

function actions(over: Partial<CanvasActions> = {}): CanvasActions {
  return {
    direction: 'TB',
    editable: true,
    onCommand: () => {},
    onDeleteStep: () => {},
    onInsertOnEdge: () => {},
    ...over,
  };
}

function dagsCard(
  dags: unknown[],
  over: Partial<SlotCardData> = {}
): CanvasNodeWithRoutes {
  const entries = readStepTools({ with: { dags } }).entries;
  return {
    key: 'toolsat:steps[1]:dags',
    label: 'DAGs',
    kind: 'other',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
    slotCard: {
      owner: 'steps[1]',
      slot: 'dags',
      stepName: 'support',
      entries,
      skills: [],
      editable: true,
      rows: 1,
      ...over,
    },
  };
}

function props(node: CanvasNode, selected = false): NodeProps<StepFlowNode> {
  return { id: node.key, data: { node }, selected, isConnectable: false };
}

function view(
  ui: ReactElement,
  opts: {
    run?: ToolRunInfo | null;
    compact?: boolean;
    actions?: Partial<CanvasActions>;
  } = {}
) {
  return render(
    <AppBarContext.Provider value={appBar}>
      <FlowProvider>
        <CanvasActionsContext.Provider
          value={actions({ compact: opts.compact ?? false, ...opts.actions })}
        >
          <ToolRunContext.Provider value={opts.run ?? null}>
            {ui}
          </ToolRunContext.Provider>
        </CanvasActionsContext.Provider>
      </FlowProvider>
    </AppBarContext.Provider>
  );
}

const entry = (root: HTMLElement, name: string) =>
  within(root)
    .getAllByTestId('ve-dag-entry')
    .find((item) => item.dataset.dag === name)!;

describe('the DAGs card', () => {
  it('has a header with the count, an entry for each DAG with its name and description, and an add row', () => {
    serve({ dags: DAGS });
    const { getByTestId, getAllByTestId, getByRole } = view(
      <DagCardNode
        {...props(dagsCard([{ name: 'triage-ticket' }, { name: 'lookup' }]))}
      />
    );
    const card = getByTestId('ve-canvas-node-toolsat:steps[1]:dags');
    expect(card).toHaveAttribute('data-slot', 'dags');
    expect(card).toHaveTextContent('DAGs');
    expect(getByTestId('ve-canvas-slot-count')).toHaveTextContent('2');
    expect(getAllByTestId('ve-dag-entry')).toHaveLength(2);
    const first = entry(card, 'triage-ticket');
    expect(first).toHaveTextContent('Read a ticket and route it.');
    expect(first.dataset.state).toBe('graph');
    // A DAG with no description says so, once it is known.
    expect(entry(card, 'lookup')).toHaveTextContent('No description');
    expect(getByRole('button', { name: 'Add a DAG' })).toBe(
      getByTestId('ve-canvas-sub-add')
    );
  });

  it('draws the miniature of the DAG with its number of steps, and opens the enlarged view from it', () => {
    serve({ dags: DAGS });
    const onCommand = vi.fn();
    const { getByTestId } = view(
      <DagCardNode {...props(dagsCard([{ name: 'triage-ticket' }]))} />,
      { actions: { onCommand } }
    );
    const mini = getByTestId('ve-dag-mini-open');
    expect(mini).toHaveAccessibleName('Show the graph of triage-ticket');
    expect(within(mini).getByTestId('ve-dag-mini-svg')).toHaveAttribute(
      'data-kind',
      'graph'
    );
    expect(getByTestId('ve-dag-steps')).toHaveTextContent('3 steps');
    // A step with a precondition has a dashed line.
    expect(mini.querySelectorAll('rect[stroke-dasharray]')).toHaveLength(1);
    fireEvent.click(mini);
    expect(onCommand).toHaveBeenCalledWith({
      kind: 'openDagView',
      tool: { step: 'steps[1]', category: 'dags', index: 0 },
    });
  });

  it('opens the panel of the tool from its name, and never the list from a double click on a row', () => {
    serve({ dags: DAGS });
    const onCommand = vi.fn();
    const { getByRole, getAllByTestId } = view(
      <DagCardNode {...props(dagsCard([{ name: 'lookup' }]))} />,
      { actions: { onCommand } }
    );
    fireEvent.click(getByRole('button', { name: 'lookup' }));
    expect(onCommand).toHaveBeenCalledWith({
      kind: 'openTool',
      tool: { step: 'steps[1]', category: 'dags', index: 0 },
    });
    onCommand.mockClear();
    fireEvent.doubleClick(getAllByTestId('ve-canvas-slot-row')[0]!);
    expect(onCommand).not.toHaveBeenCalled();
  });

  it('lights the entry of the selected tool', () => {
    serve({ dags: DAGS });
    const { getByRole } = view(
      <DagCardNode {...props(dagsCard([{ name: 'lookup' }]))} />,
      {
        actions: {
          selectedTool: { step: 'steps[1]', category: 'dags', index: 0 },
        },
      }
    );
    expect(getByRole('button', { name: 'lookup' })).toHaveAttribute(
      'data-selected',
      'true'
    );
  });

  it('shows the tool name beside the DAG name when they differ', () => {
    serve({
      dags: { 'ops.notify': { name: 'ops.notify', steps: [step('a')] } },
    });
    const { getByTestId } = view(
      <DagCardNode {...props(dagsCard([{ name: 'ops.notify' }]))} />
    );
    expect(getByTestId('ve-dag-entry')).toHaveTextContent('ops_notify');
  });

  describe('the states of an entry', () => {
    it('marks a DAG that the server does not know in red, with the reason in place of the graph', () => {
      serve({ dags: DAGS });
      const { getByTestId, getByRole, queryByTestId } = view(
        <DagCardNode {...props(dagsCard([{ name: 'no-such-dag' }]))} />
      );
      const item = getByTestId('ve-dag-entry');
      expect(item.dataset.state).toBe('missing');
      expect(getByRole('alert')).toHaveTextContent('DAG not found');
      expect(getByRole('button', { name: 'no-such-dag' }).textContent).toBe(
        'no-such-dag'
      );
      expect(within(item).getByText('no-such-dag')).toHaveClass(
        'text-destructive'
      );
      // Nothing to enlarge, and no claim about its inputs.
      expect(queryByTestId('ve-dag-mini-open')).toBeNull();
      expect(item).not.toHaveTextContent('No input');
    });

    it('does not ask for a DAG whose name is a reference, and says that it is chosen when the step runs', () => {
      serve({ dags: DAGS });
      const { getByTestId } = view(
        <DagCardNode {...props(dagsCard([{ name: '${TOOL_DAG}' }]))} />
      );
      expect(getByTestId('ve-dag-entry').dataset.state).toBe('dynamic');
      expect(getByTestId('ve-dag-mini-note')).toHaveTextContent(
        'Chosen when the step runs'
      );
      expect(requests).toEqual([]);
    });

    it('shows an item that is not a mapping as an invalid entry with its reason', () => {
      serve({ dags: DAGS });
      const { getByTestId } = view(
        <DagCardNode {...props(dagsCard(['lookup']))} />
      );
      expect(getByTestId('ve-dag-entry').dataset.state).toBe('invalid');
      expect(getByTestId('ve-dag-mini-note')).toHaveTextContent(
        'A tool entry is a mapping with a name.'
      );
    });

    it('waits for the server with a quiet placeholder', () => {
      (useQuery as unknown as ReturnType<typeof vi.fn>).mockImplementation(
        () => ({ data: undefined })
      );
      const { getByTestId } = view(
        <DagCardNode {...props(dagsCard([{ name: 'lookup' }]))} />
      );
      expect(getByTestId('ve-dag-entry').dataset.state).toBe('loading');
      expect(getByTestId('ve-dag-entry')).not.toHaveTextContent('No input');
    });

    it('says that a DAG with no step has none', () => {
      serve({ dags: DAGS });
      const { getByTestId, queryByTestId } = view(
        <DagCardNode {...props(dagsCard([{ name: 'empty' }]))} />
      );
      expect(getByTestId('ve-dag-entry').dataset.state).toBe('empty');
      expect(getByTestId('ve-dag-mini-note')).toHaveTextContent('No steps');
      expect(queryByTestId('ve-dag-mini-open')).toBeNull();
    });

    it('shows the miniature of a DAG with one step as one card', () => {
      serve({ dags: DAGS });
      const { getByTestId } = view(
        <DagCardNode {...props(dagsCard([{ name: 'lookup' }]))} />
      );
      expect(getByTestId('ve-dag-steps')).toHaveTextContent('1 step');
      expect(
        getByTestId('ve-dag-mini-svg').querySelectorAll('rect')
      ).toHaveLength(1);
    });

    it('collapses a DAG of a hundred steps to a summary of its ranks', () => {
      serve({ dags: DAGS });
      const { getByTestId } = view(
        <DagCardNode {...props(dagsCard([{ name: 'big' }]))} />
      );
      expect(getByTestId('ve-dag-entry').dataset.state).toBe('summary');
      expect(getByTestId('ve-dag-mini-svg')).toHaveAttribute(
        'data-kind',
        'summary'
      );
      expect(getByTestId('ve-dag-steps')).toHaveTextContent(
        '100 steps in 10 stages'
      );
    });

    it('marks the DAG that holds the step as a call to itself', () => {
      serve({ dags: DAGS });
      const { getByTestId, queryByTestId } = view(
        <DagCardNode
          {...props(dagsCard([{ name: 'lookup' }, { name: 'triage-ticket' }]))}
        />,
        { actions: { dag: { name: 'lookup' } } }
      );
      const marks = within(getByTestId('ve-canvas-node-toolsat:steps[1]:dags'))
        .getAllByTestId('ve-dag-entry')
        .map((item) => !!within(item).queryByTestId('ve-dag-recursive'));
      expect(marks).toEqual([true, false]);
      expect(
        queryByTestId('ve-dag-recursive')?.getAttribute('aria-label')
      ).toBe('This DAG calls itself.');
    });
  });

  describe('the inputs line', () => {
    it('tells who decides each input: the model, the entry, or nobody yet', () => {
      serve({ dags: DAGS });
      const { getAllByTestId, getByTestId, getByText } = view(
        <DagCardNode
          {...props(
            dagsCard([
              {
                name: 'triage-ticket',
                params: {
                  ticket_id: { from_ai: true },
                  lang: 'fr',
                  nope: 'x',
                },
              },
            ])
          )}
        />
      );
      const chips = getAllByTestId('ve-dag-input');
      // Three chips, then the others are counted.
      expect(
        chips.map((chip) => [chip.textContent, chip.dataset.state])
      ).toEqual([
        ['ticket_id*', 'from_ai'],
        ['lang', 'fixed'],
        ['verbose', 'open'],
      ]);
      expect(chips[1]!.querySelector('svg')).not.toBeNull();
      expect(chips[1]!.getAttribute('title')).toContain('Fixed: fr');
      expect(getByTestId('ve-dag-inputs')).toHaveTextContent('+2 more');
      // The tooltip of "+2 more" names the inputs that the line does not show.
      expect(getByText('+2 more').getAttribute('title')).toBe('extra\nnope');
    });

    it('flags an input that the DAG does not declare', () => {
      serve({ dags: { lookup: DAGS.lookup } });
      const { getByTestId } = view(
        <DagCardNode
          {...props(dagsCard([{ name: 'lookup', params: { nope: 'x' } }]))}
        />
      );
      const chip = getByTestId('ve-dag-input');
      expect(chip).toHaveClass('text-warning');
      expect(chip.getAttribute('title')).toContain('Not declared by the DAG');
    });

    it('says that a known DAG takes no input', () => {
      serve({ dags: DAGS });
      const { getByTestId } = view(
        <DagCardNode {...props(dagsCard([{ name: 'lookup' }]))} />
      );
      expect(getByTestId('ve-dag-inputs')).toHaveTextContent('No input');
    });
  });

  describe('the rows under the entries', () => {
    const five = Array.from({ length: 5 }, () => ({ name: 'lookup' }));

    it('lists three DAGs and a "+N more" row that opens the list', () => {
      serve({ dags: DAGS });
      const onCommand = vi.fn();
      const { getAllByTestId, getByTestId } = view(
        <DagCardNode {...props(dagsCard(five))} />,
        { actions: { onCommand } }
      );
      expect(getAllByTestId('ve-dag-entry')).toHaveLength(DAG_CARD_MAX_ENTRIES);
      expect(getByTestId('ve-canvas-slot-count')).toHaveTextContent('5');
      const more = getByTestId('ve-canvas-slot-more');
      expect(more).toHaveTextContent('+2 more');
      fireEvent.click(more);
      expect(onCommand).toHaveBeenCalledWith({
        kind: 'openSlotPanel',
        step: 'steps[1]',
        slot: 'dags',
      });
    });

    it('opens the add dock from the add row, and has none on a read-only canvas', () => {
      serve({ dags: DAGS });
      const onCommand = vi.fn();
      const first = view(
        <DagCardNode {...props(dagsCard([{ name: 'lookup' }]))} />,
        {
          actions: { onCommand },
        }
      );
      fireEvent.click(first.getByTestId('ve-canvas-sub-add'));
      expect(onCommand).toHaveBeenCalledWith({
        kind: 'openSlotDock',
        step: 'steps[1]',
        slot: 'dags',
      });
      first.unmount();
      const second = view(
        <DagCardNode
          {...props(dagsCard([{ name: 'lookup' }], { editable: false }))}
        />
      );
      expect(second.queryByTestId('ve-canvas-sub-add')).toBeNull();
    });

    it('shows the limits of the tool loop under the header when the card holds them', () => {
      serve({ dags: DAGS });
      const { getByTestId } = view(
        <DagCardNode
          {...props(
            dagsCard([{ name: 'lookup' }], {
              limits: { turns: 20, calls: 3 },
              rows: 2,
            })
          )}
        />
      );
      expect(getByTestId('ve-canvas-limit-turns')).toHaveTextContent(
        'max 20 turns'
      );
      expect(getByTestId('ve-canvas-limit-calls')).toHaveTextContent(
        'max 3 calls'
      );
    });
  });

  it('draws a tile with the count of the DAGs in the compact canvas, and opens the list from it', () => {
    serve({ dags: DAGS });
    const onCommand = vi.fn();
    const { getByTestId, queryByTestId } = view(
      <DagCardNode
        {...props(dagsCard([{ name: 'triage-ticket' }, { name: 'lookup' }]))}
      />,
      { compact: true, actions: { onCommand } }
    );
    const tile = getByTestId('ve-canvas-node-toolsat:steps[1]:dags');
    expect(tile).toHaveAttribute('data-slot', 'dags');
    expect(tile).toHaveStyle({
      width: `${SUB_TILE_SIZE}px`,
      height: `${SUB_TILE_SIZE}px`,
    });
    expect(getByTestId('ve-canvas-slot-count')).toHaveTextContent('2');
    // The tooltip lists the DAGs; the card has no entry and no graph.
    expect(tile).toHaveAttribute('title', 'DAGs\ntriage-ticket\nlookup');
    expect(queryByTestId('ve-dag-entry')).toBeNull();
    expect(queryByTestId('ve-dag-mini')).toBeNull();
    fireEvent.doubleClick(tile);
    expect(onCommand).toHaveBeenCalledWith({
      kind: 'openSlotPanel',
      step: 'steps[1]',
      slot: 'dags',
    });
  });

  it('is as tall as the layout reserves, so that its entries never overflow the card', () => {
    serve({ dags: DAGS });
    const card = dagsCard([{ name: 'lookup' }, { name: 'triage-ticket' }]);
    const { getByTestId } = view(<DagCardNode {...props(card)} />);
    const entries = within(
      getByTestId('ve-canvas-node-toolsat:steps[1]:dags')
    ).getAllByTestId('ve-dag-entry');
    // The parts of an entry have the heights of the layout: name, description,
    // miniature, inputs and pad.
    const total = (item: HTMLElement) =>
      [...item.children].reduce(
        (sum, child) => sum + parseFloat((child as HTMLElement).style.height),
        0
      );
    expect(entries.map(total)).toEqual([DAG_ENTRY_HEIGHT, DAG_ENTRY_HEIGHT]);
  });

  describe('in a run view', () => {
    const RUN: ToolRunInfo = {
      dagName: 'main',
      runId: 'run-1',
      calls: { support: { 'triage-ticket': 2 } },
      subRuns: {
        support: {
          'triage-ticket': [{ dagRunId: 'sub-1' }, { dagRunId: 'sub-2' }],
        },
      },
      root: { name: 'main', id: 'run-1', fileName: 'main' },
    };
    const SUB = {
      status: Status.Failed,
      nodes: [
        { step: { name: 'fetch' }, status: 4 },
        { step: { name: 'classify' }, status: 2 },
      ],
    };

    it('counts the calls, shows the status of the latest sub-run, and links to it', () => {
      serve({ dags: DAGS, sub: SUB });
      const { getByTestId } = view(
        <DagCardNode
          {...props(dagsCard([{ name: 'triage-ticket' }, { name: 'lookup' }]))}
        />,
        { run: RUN }
      );
      const called = entry(
        getByTestId('ve-canvas-node-toolsat:steps[1]:dags'),
        'triage-ticket'
      );
      expect(
        within(called).getByTestId('ve-canvas-tool-calls')
      ).toHaveTextContent('2');
      expect(within(called).getByTestId('status-dot')).toHaveAttribute(
        'data-status',
        String(Status.Failed)
      );
      const link = within(called)
        .getAllByTestId('ve-open-link')
        .find((a) => a.getAttribute('href')?.includes('subRun=sub-2'));
      expect(link).toBeDefined();
      expect(link?.getAttribute('href')).toContain('run=run-1');
      // The steps of the sub-run colour the miniature.
      expect(
        called.querySelector(
          '[data-testid="ve-dag-mini-svg"] .stroke-\\[var\\(--graph-success\\)\\]'
        )
      ).not.toBeNull();
      expect(
        called.querySelector(
          '[data-testid="ve-dag-mini-svg"] .stroke-\\[var\\(--graph-error\\)\\]'
        )
      ).not.toBeNull();
      // A DAG that the model never called has no status and no run link.
      const never = entry(
        getByTestId('ve-canvas-node-toolsat:steps[1]:dags'),
        'lookup'
      );
      expect(within(never).queryByTestId('status-dot')).toBeNull();
      expect(
        within(never)
          .getAllByTestId('ve-open-link')
          .every((a) => !a.getAttribute('href')?.includes('subRun='))
      ).toBe(true);
    });

    it('shows no status while the sub-run loads, and nothing for a canvas with no run', () => {
      serve({ dags: DAGS });
      const loading = view(
        <DagCardNode {...props(dagsCard([{ name: 'triage-ticket' }]))} />,
        { run: RUN }
      );
      expect(loading.queryByTestId('status-dot')).toBeNull();
      expect(loading.getByTestId('ve-canvas-tool-calls')).toBeInTheDocument();
      loading.unmount();
      const plain = view(
        <DagCardNode {...props(dagsCard([{ name: 'triage-ticket' }]))} />
      );
      expect(plain.queryByTestId('ve-canvas-tool-calls')).toBeNull();
    });
  });
});
