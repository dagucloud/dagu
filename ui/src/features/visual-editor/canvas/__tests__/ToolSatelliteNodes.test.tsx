// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, within } from '@testing-library/react';
import type { ReactElement } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { components } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { useQuery } from '@/hooks/api';
import type { CanvasNode } from '../../contracts';
import { readStepTools } from '../../tools/parse';
import { CanvasActionsContext, type CanvasActions } from '../context';
import type { ModelRef } from '../chatModel';
import { CHAT_NODE_WIDTH, chatPlan, nodeSize } from '../layout';
import { FlowProvider, type NodeProps } from '../engine';
import { AddSubNode } from '../nodes/AddSubNode';
import { ModelSubNode } from '../nodes/ModelSubNode';
import { SlotCardNode } from '../nodes/SlotCardNode';
import { StepNode } from '../nodes/StepNode';
import { SLOT_CARD_MAX_ROWS, SUB_TILE_SIZE, slotCardRows } from '../subRow';
import { planToolCard } from '../toolGroups';
import { ToolRunContext, type ToolRunInfo } from '../toolRun';
import type {
  CanvasNodeWithRoutes,
  SlotCardData,
  StepFlowNode,
} from '../types';

vi.mock('@/hooks/api', () => ({ useQuery: vi.fn() }));

type ToolDefinition = components['schemas']['ToolDefinition'];

const appBar = {
  title: 'DAGs',
  setTitle: vi.fn(),
  remoteNodes: ['local'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'local',
  selectRemoteNode: vi.fn(),
};

function serve(defs: ToolDefinition[] | undefined) {
  (useQuery as unknown as ReturnType<typeof vi.fn>).mockImplementation(() => ({
    data: defs ? { messages: [], toolDefinitions: defs } : undefined,
  }));
}

afterEach(() => {
  vi.clearAllMocks();
});

const RUN: ToolRunInfo = {
  dagName: 'main',
  runId: 'run-1',
  calls: { triage: { lookup: 2, refund: 1 } },
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

/** The tools of a step that has these DAG names (`with.dags`). */
function dagTools(...names: string[]) {
  return { dags: names.map((name) => ({ name })) };
}

function toolsCard(
  tools: { dags?: unknown; mcp?: unknown },
  over: Partial<SlotCardData> = {}
): CanvasNodeWithRoutes {
  // The DAG tools are in `with.dags`, the MCP servers in `with.tools.mcp`.
  const entries = readStepTools({
    with: { dags: tools.dags, tools: { mcp: tools.mcp } },
  }).entries;
  return {
    key: 'toolsat:steps[1]:tools',
    label: 'Tools',
    kind: 'other',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
    slotCard: {
      owner: 'steps[1]',
      slot: 'tools',
      stepName: 'triage',
      entries,
      skills: [],
      editable: true,
      rows: slotCardRows(entries.length, true),
      ...over,
    },
  };
}

function skillsCard(
  names: string[],
  over: Partial<SlotCardData> = {}
): CanvasNodeWithRoutes {
  return {
    key: 'toolsat:steps[1]:skills',
    label: 'Skills',
    kind: 'other',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
    slotCard: {
      owner: 'steps[1]',
      slot: 'skills',
      entries: [],
      skills: names.map((name, index) => ({ name, index })),
      editable: true,
      rows: slotCardRows(names.length, true),
      ...over,
    },
  };
}

function props(node: CanvasNode, selected = false): NodeProps<StepFlowNode> {
  return {
    id: node.key,
    data: { node },
    selected,
    isConnectable: false,
  };
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

describe('the Tools card', () => {
  it('has a header with the slot name and the count, a row for each entry and an add row', () => {
    serve(undefined);
    const { getByTestId, getAllByTestId } = view(
      <SlotCardNode
        {...props(
          toolsCard({
            ...dagTools('lookup', 'refund'),
            mcp: [{ name: 'docs', url: 'https://mcp.example.com', allow: [] }],
          })
        )}
      />
    );
    const card = getByTestId('ve-canvas-node-toolsat:steps[1]:tools');
    expect(card).toHaveAttribute('data-slot', 'tools');
    expect(card).toHaveTextContent('Tools');
    expect(getByTestId('ve-canvas-slot-count')).toHaveTextContent('3');
    expect(getAllByTestId('ve-canvas-slot-row')).toHaveLength(3);
    expect(within(card).getByRole('button', { name: 'Add a tool' })).toBe(
      getByTestId('ve-canvas-sub-add')
    );
  });

  it('shows only the add row when it is empty', () => {
    serve(undefined);
    const { queryAllByTestId, getByRole, getByTestId } = view(
      <SlotCardNode {...props(toolsCard({}))} />
    );
    expect(queryAllByTestId('ve-canvas-slot-row')).toHaveLength(0);
    expect(getByRole('button', { name: 'Add a tool' })).toBeInTheDocument();
    expect(getByTestId('ve-canvas-slot-count')).toHaveTextContent('0');
  });

  it('has no add row on a read-only canvas, and says so when empty', () => {
    serve(undefined);
    const { queryByTestId, container } = view(
      <SlotCardNode {...props(toolsCard({}, { editable: false }))} />
    );
    expect(queryByTestId('ve-canvas-sub-add')).toBeNull();
    expect(container).toHaveTextContent('No tools');
  });

  it('gives a row the icon of its kind, its name and a muted cue', () => {
    serve(undefined);
    const { getAllByTestId } = view(
      <SlotCardNode
        {...props(
          toolsCard({
            dags: [{ name: 'refund' }, { name: 'ops.notify' }],
            mcp: [
              {
                name: 'docs',
                url: 'https://mcp.example.com/v1',
                allow: ['a', 'b'],
              },
            ],
          })
        )}
      />
    );
    const rows = getAllByTestId('ve-canvas-slot-row');
    expect(rows.map((row) => row.getAttribute('data-kind'))).toEqual([
      'dag',
      'dag',
      'mcp',
    ]);
    expect(rows[0]).toHaveTextContent('refund');
    expect(rows[1]).toHaveTextContent('ops.notify');
    expect(rows[2]).toHaveTextContent('docs');
    expect(rows[2]).toHaveTextContent('2 tools');
  });

  it('shows an item that is not a mapping as an invalid entry, with the reason', () => {
    serve(undefined);
    const { getByTestId } = view(
      <SlotCardNode {...props(toolsCard({ dags: ['lookup'] }))} />
    );
    const row = getByTestId('ve-canvas-slot-row');
    expect(row).toHaveAttribute('data-kind', 'invalid');
    expect(row).toHaveTextContent('Invalid tool entry');
    expect(row).toHaveTextContent('A tool entry is a mapping with a name.');
  });

  it('names an MCP entry that has no name yet, by its host or by its kind', () => {
    serve(undefined);
    const bare = view(
      <SlotCardNode
        {...props(toolsCard({ mcp: [{ name: '', url: '', allow: [] }] }))}
      />
    );
    expect(bare.getByTestId('ve-canvas-slot-row')).toHaveAttribute(
      'aria-label',
      'MCP server'
    );
    bare.unmount();
    const host = view(
      <SlotCardNode
        {...props(
          toolsCard({
            mcp: [{ name: '', url: 'https://mcp.example.com/v1', allow: [] }],
          })
        )}
      />
    );
    expect(host.getByTestId('ve-canvas-slot-row')).toHaveAttribute(
      'aria-label',
      'mcp.example.com'
    );
  });

  it('lists the fixed inputs of an entry in a lock, and shows no if chip', () => {
    serve(undefined);
    const { getByTestId, queryByTestId } = view(
      <SlotCardNode
        {...props(
          toolsCard({
            dags: [
              {
                name: 'refund',
                // An old file can still have this key: the canvas ignores it.
                if: [{ condition: '${TIER}', expected: 'gold' }],
                params: {
                  customer_id: '${CUSTOMER_ID}',
                  amount: { from_ai: true },
                },
              },
            ],
          })
        )}
      />
    );
    expect(queryByTestId('ve-canvas-tool-if')).toBeNull();
    const lock = getByTestId('ve-canvas-tool-fixed');
    expect(lock.getAttribute('title')).toContain('customer_id: ${CUSTOMER_ID}');
    expect(lock.getAttribute('title')).not.toContain('amount');
  });

  it('counts the calls of the run in the row', () => {
    serve([{ name: 'lookup', source: 'dag:lookup' }]);
    const { getByTestId } = view(
      <SlotCardNode {...props(toolsCard(dagTools('lookup')))} />,
      { run: RUN }
    );
    expect(getByTestId('ve-canvas-tool-calls')).toHaveTextContent('2');
    expect(getByTestId('ve-canvas-slot-row')).toHaveAttribute(
      'data-offered',
      'true'
    );
  });

  it('shows the limits in a row under the header, and the calls made in a run', () => {
    serve(undefined);
    const limits = { turns: 20, calls: 3 };
    const card = toolsCard(dagTools('lookup'), {
      limits,
      rows: slotCardRows(1, true, 0, true),
    });
    const first = view(<SlotCardNode {...props(card)} />);
    expect(first.getByTestId('ve-canvas-limit-turns')).toHaveTextContent(
      'max 20 turns'
    );
    expect(first.getByTestId('ve-canvas-limit-calls')).toHaveTextContent(
      'max 3 calls'
    );
    first.unmount();
    // The run made 3 calls of the 3 that the step allows.
    const second = view(<SlotCardNode {...props(card)} />, { run: RUN });
    expect(second.getByTestId('ve-canvas-limit-calls')).toHaveTextContent(
      '3 of 3 calls'
    );
    expect(second.getByTestId('ve-canvas-limit-calls').className).toContain(
      'warning'
    );
  });

  it('lets a long limit shrink in the card and end with an ellipsis, with its text in the tooltip', () => {
    serve(undefined);
    const reference = '${THE_LIMIT_OF_THE_TOOL_CALLS_OF_THIS_STEP}';
    const card = toolsCard(dagTools('lookup'), {
      limits: { turns: 20, calls: reference },
      rows: slotCardRows(1, true, 0, true),
    });
    const { getByTestId } = view(<SlotCardNode {...props(card)} />);
    const chip = getByTestId('ve-canvas-limit-calls');
    // The chip may shrink below its text, and the text is cut with an ellipsis.
    expect(chip.className).toContain('min-w-0');
    expect(chip.className).not.toContain('shrink-0');
    // The turns chip keeps its text, up to a share of the row.
    expect(getByTestId('ve-canvas-limit-turns').className).toContain(
      'max-w-[55%]'
    );
    expect(chip.firstElementChild?.className).toContain('truncate');
    expect(chip).toHaveTextContent(`max ${reference} calls`);
  });

  it('shows no limits row when the step has no limits', () => {
    serve(undefined);
    const { queryByTestId } = view(
      <SlotCardNode {...props(toolsCard(dagTools('lookup')))} />
    );
    expect(queryByTestId('ve-canvas-slot-limits')).toBeNull();
  });

  it('lists eight rows and a "+N more" row that shows the first hidden entry', () => {
    serve(undefined);
    const names = Array.from({ length: 11 }, (_, i) => `t${i}`);
    const onCommand = vi.fn();
    const { getAllByTestId, getByTestId } = view(
      <SlotCardNode {...props(toolsCard(dagTools(...names)))} />,
      { actions: { onCommand } }
    );
    expect(getAllByTestId('ve-canvas-slot-row')).toHaveLength(
      SLOT_CARD_MAX_ROWS
    );
    expect(getByTestId('ve-canvas-slot-count')).toHaveTextContent('11');
    const more = getByTestId('ve-canvas-slot-more');
    expect(more).toHaveTextContent('+3 more');
    fireEvent.click(more);
    expect(onCommand).toHaveBeenCalledWith({
      kind: 'openTool',
      tool: { step: 'steps[1]', category: 'dags', index: SLOT_CARD_MAX_ROWS },
    });
  });

  it('draws a tile in the compact canvas: the icon and the count, with the names and the calls in its tooltip', () => {
    serve(undefined);
    const onCommand = vi.fn();
    const tools = view(
      <SlotCardNode {...props(toolsCard(dagTools('lookup', 'refund')))} />,
      { compact: true, run: RUN, actions: { onCommand } }
    );
    const tile = tools.getByTestId('ve-canvas-node-toolsat:steps[1]:tools');
    expect(tile).toHaveStyle({
      width: `${SUB_TILE_SIZE}px`,
      height: `${SUB_TILE_SIZE}px`,
    });
    expect(tools.getByTestId('ve-canvas-slot-count')).toHaveTextContent('2');
    expect(tile).toHaveAttribute('title', 'Tools\nlookup\nrefund\nCalls: 3');
    expect(tools.queryByTestId('ve-canvas-slot-line')).toBeNull();
    expect(tools.queryByTestId('ve-canvas-sub-add')).toBeNull();
    fireEvent.doubleClick(tile);
    fireEvent.keyDown(tile, { key: 'Enter' });
    expect(onCommand).toHaveBeenCalledTimes(2);
    expect(onCommand).toHaveBeenLastCalledWith({
      kind: 'openSlotPanel',
      step: 'steps[1]',
      slot: 'tools',
    });
  });

  it('rings the tile of the Tools slot while the panel shows a tool of the step', () => {
    serve(undefined);
    const card = toolsCard(dagTools('a'));
    const idle = view(<SlotCardNode {...props(card)} />, { compact: true });
    expect(
      idle.getByTestId('ve-canvas-node-toolsat:steps[1]:tools')
    ).not.toHaveAttribute('data-selected');
    idle.unmount();
    const chosen = view(<SlotCardNode {...props(card)} />, {
      compact: true,
      actions: {
        selectedTool: { step: 'steps[1]', category: 'mcp', index: 0 },
      },
    });
    expect(
      chosen.getByTestId('ve-canvas-node-toolsat:steps[1]:tools')
    ).toHaveAttribute('data-selected', 'true');
  });

  it('draws a tile for the Skills card in the compact canvas', () => {
    serve(undefined);
    const { getByTestId } = view(
      <SlotCardNode {...props(skillsCard(['a', 'b']))} />,
      { compact: true }
    );
    const tile = getByTestId('ve-canvas-node-toolsat:steps[1]:skills');
    expect(tile).toHaveAttribute('data-slot', 'skills');
    expect(getByTestId('ve-canvas-slot-count')).toHaveTextContent('2');
    expect(tile).toHaveAttribute('title', 'Skills\na\nb');
  });
});

describe('the kinds on the Tools card', () => {
  const mixed = {
    dags: [{ name: 'refund' }, { name: 'lookup' }],
    mcp: [{ name: 'docs', url: 'https://mcp.example.com:8443/v1', allow: [] }],
  };

  it('draws the badge and the coloured icon of its kind on each row', () => {
    serve(undefined);
    const { getAllByTestId } = view(
      <SlotCardNode {...props(toolsCard(mixed))} />
    );
    const rows = getAllByTestId('ve-canvas-slot-row');
    expect(
      rows.map((row) => within(row).getByTestId('ve-tool-kind').textContent)
    ).toEqual(['DAG', 'DAG', 'MCP']);
    expect(
      rows.map((row) =>
        row.querySelector('[data-kind-icon]')?.getAttribute('data-kind-icon')
      )
    ).toEqual(['dag', 'dag', 'mcp']);
    expect(rows[2]!.querySelector('[data-kind-icon]')).toHaveClass(
      'text-tool-mcp'
    );
  });

  it('shows the badge when the step has one kind only, and no group heading', () => {
    serve(undefined);
    const { getAllByTestId, queryAllByTestId } = view(
      <SlotCardNode {...props(toolsCard(dagTools('a', 'b')))} />
    );
    expect(getAllByTestId('ve-tool-kind')).toHaveLength(2);
    expect(queryAllByTestId('ve-canvas-slot-heading')).toHaveLength(0);
  });

  it('puts the rows in a group with a heading for each kind, in the order of the kinds', () => {
    serve(undefined);
    const { getAllByTestId, getByRole } = view(
      <SlotCardNode {...props(toolsCard(mixed))} />
    );
    const headings = getAllByTestId('ve-canvas-slot-heading');
    expect(headings.map((heading) => heading.textContent)).toEqual([
      'DAG tools2',
      'MCP servers1',
    ]);
    expect(
      headings.map((heading) => heading.getAttribute('data-kind'))
    ).toEqual(['dag', 'mcp']);
    const dags = getByRole('group', { name: 'DAG tools' });
    expect(
      within(dags)
        .getAllByTestId('ve-canvas-slot-row')
        .map((row) => row.getAttribute('aria-label'))
    ).toEqual(['refund', 'lookup']);
  });

  it('shows the heading of the built-in tools with the other kinds', () => {
    serve(undefined);
    const { getAllByTestId } = view(
      <SlotCardNode
        {...props(toolsCard(dagTools('a'), { builtin: [{ name: 'read' }] }))}
      />
    );
    expect(
      getAllByTestId('ve-canvas-slot-heading').map((h) =>
        h.getAttribute('data-kind')
      )
    ).toEqual(['dag', 'builtin']);
  });

  it('names the kind and the source in the tooltip of a row', () => {
    serve(undefined);
    const { getAllByTestId } = view(
      <SlotCardNode {...props(toolsCard(mixed))} />
    );
    const titles = getAllByTestId('ve-canvas-slot-row').map(
      (row) => row.getAttribute('title') ?? ''
    );
    expect(titles[0]).toBe('DAG tool\nDAG: refund');
    expect(titles[2]).toBe('MCP server\nServer: mcp.example.com:8443');
  });

  // Above eight tools of several kinds the kinds share columns, each with its own cap.
  it('draws a column for the DAG tools and a column for the MCP servers and the built-in tools above eight tools', () => {
    serve(undefined);
    const tools = {
      dags: Array.from({ length: 12 }, (_, i) => ({ name: `d${i}` })),
      mcp: [
        { name: 'a', url: 'https://a.example.com', allow: [] },
        { name: 'b', url: 'https://b.example.com', allow: [] },
      ],
    };
    const builtin: SlotCardData['builtin'] = [
      { name: 'read' },
      { name: 'search' },
      { name: 'shell' },
      { name: 'write' },
      { name: 'edit' },
    ];
    const entries = readStepTools({ with: { tools } }).entries;
    const plan = planToolCard(entries, builtin);
    const { getAllByTestId, getByTestId } = view(
      <SlotCardNode
        {...props(
          toolsCard(tools, {
            builtin,
            rows: plan.rows + 1,
            cols: plan.columns.length,
          })
        )}
      />
    );
    const columns = getAllByTestId('ve-tools-column');
    expect(columns).toHaveLength(2);
    expect(
      within(columns[0]!).getAllByTestId('ve-canvas-slot-row')
    ).toHaveLength(8);
    expect(
      within(columns[0]!).getByTestId('ve-canvas-slot-more')
    ).toHaveTextContent('+4 more');
    expect(
      within(columns[1]!)
        .getAllByTestId('ve-canvas-slot-heading')
        .map((heading) => heading.getAttribute('data-kind'))
    ).toEqual(['mcp', 'builtin']);
    expect(
      within(columns[1]!).getAllByTestId('ve-canvas-slot-row')
    ).toHaveLength(7);
    // The header counts every tool; the add row is outside the columns.
    expect(getByTestId('ve-canvas-slot-count')).toHaveTextContent('19');
    const add = getByTestId('ve-canvas-sub-add');
    expect(columns[0]!.contains(add)).toBe(false);
    expect(columns[1]!.contains(add)).toBe(false);
  });

  it('counts the headings in the rows of the layout', () => {
    const entries = readStepTools({
      with: { dags: mixed.dags, tools: { mcp: mixed.mcp } },
    }).entries;
    const plan = planToolCard(entries, []);
    expect(plan.headings).toBe(2);
    expect(slotCardRows(entries.length, true, plan.headings)).toBe(6);
    expect(planToolCard(entries.slice(0, 2), []).headings).toBe(0);
  });
});

describe('the Tools card of an agent step', () => {
  const builtin: SlotCardData['builtin'] = [
    { name: 'read' },
    { name: 'search' },
    {
      name: 'shell',
      allow: ['./scripts/test.sh', 'git diff *'],
      deny: ['git push *'],
    },
  ];

  it('lists the built-in tools, with the shell patterns in the tooltip', () => {
    serve(undefined);
    const { getAllByTestId } = view(
      <SlotCardNode {...props(toolsCard({}, { builtin, rows: 3 }))} />
    );
    const rows = getAllByTestId('ve-canvas-slot-row');
    expect(rows.map((row) => row.getAttribute('data-kind'))).toEqual([
      'builtin',
      'builtin',
      'builtin',
    ]);
    expect(rows[0]).toHaveTextContent('Read files');
    expect(rows[2]).toHaveTextContent('Shell commands');
    expect(rows[2]).toHaveTextContent('2 allowed');
    const title = rows[2]?.getAttribute('title') ?? '';
    expect(title).toContain('Allow: git diff *');
    expect(title).toContain('Deny: git push *');
    expect(rows[2]).not.toHaveAttribute('data-dimmed');
  });

  it('counts the built-in tools in the header', () => {
    serve(undefined);
    const { getByTestId } = view(
      <SlotCardNode
        {...props(toolsCard({}, { builtin, rows: slotCardRows(3, true) }))}
      />
    );
    expect(getByTestId('ve-canvas-slot-count')).toHaveTextContent('3');
  });

  it('selects the one entry of the built-in tools from a click on any built-in row', () => {
    serve(undefined);
    const onCommand = vi.fn();
    const { getAllByTestId } = view(
      <SlotCardNode {...props(toolsCard({}, { builtin, rows: 4 }))} />,
      { actions: { onCommand } }
    );
    fireEvent.click(getAllByTestId('ve-canvas-slot-row')[1]!);
    expect(onCommand).toHaveBeenCalledWith({
      kind: 'openTool',
      tool: { step: 'steps[1]', category: 'builtin', index: 0 },
    });
  });
});

describe('the Skills card', () => {
  it('lists the names with a book icon and an add row', () => {
    const { getAllByTestId, getByTestId, getByRole } = view(
      <SlotCardNode
        {...props(skillsCard(['refund-policy', 'tone-of-voice']))}
      />
    );
    const card = getByTestId('ve-canvas-node-toolsat:steps[1]:skills');
    expect(card).toHaveTextContent('Skills');
    expect(getByTestId('ve-canvas-slot-count')).toHaveTextContent('2');
    const rows = getAllByTestId('ve-canvas-slot-row');
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent('refund-policy');
    expect(getByRole('button', { name: 'Add a skill' })).toBeInTheDocument();
  });

  it('shows at most eight skills, then a "+N more" row that opens the dock of the list', () => {
    const onCommand = vi.fn();
    const names = Array.from(
      { length: SLOT_CARD_MAX_ROWS + 3 },
      (_, i) => `s${i}`
    );
    const { getAllByTestId, getByTestId } = view(
      <SlotCardNode {...props(skillsCard(names))} />,
      { actions: { onCommand } }
    );
    expect(getAllByTestId('ve-canvas-slot-row')).toHaveLength(
      SLOT_CARD_MAX_ROWS
    );
    const more = getByTestId('ve-canvas-slot-more');
    expect(more).toHaveTextContent('+3 more');
    fireEvent.click(more);
    expect(onCommand).toHaveBeenCalledWith({
      kind: 'openSlotPanel',
      step: 'steps[1]',
      slot: 'skills',
    });
  });

  it('has no add row on a read-only canvas', () => {
    const { queryByRole } = view(
      <SlotCardNode {...props(skillsCard(['a'], { editable: false }))} />
    );
    expect(queryByRole('button', { name: 'Add a skill' })).toBeNull();
  });
});

describe('opening from a slot card', () => {
  it('selects the tool from a click, Enter and Space on its row', () => {
    serve(undefined);
    const onCommand = vi.fn();
    const { getByRole } = view(
      <SlotCardNode {...props(toolsCard(dagTools('lookup', 'refund')))} />,
      { actions: { onCommand } }
    );
    const row = getByRole('button', { name: 'refund' });
    expect(row).toHaveAttribute('tabindex', '0');
    fireEvent.click(row);
    fireEvent.keyDown(row, { key: 'Enter' });
    fireEvent.keyDown(row, { key: ' ' });
    expect(onCommand).toHaveBeenCalledTimes(3);
    for (const call of onCommand.mock.calls) {
      expect(call[0]).toEqual({
        kind: 'openTool',
        tool: { step: 'steps[1]', category: 'dags', index: 1 },
      });
    }
  });

  it('does not let the click on a row select the step again', () => {
    serve(undefined);
    const onClick = vi.fn();
    const { getByRole } = view(
      <div onClick={onClick}>
        <SlotCardNode {...props(toolsCard(dagTools('lookup')))} />
      </div>
    );
    fireEvent.click(getByRole('button', { name: 'lookup' }));
    expect(onClick).not.toHaveBeenCalled();
  });

  it('selects the tool by its category and its place in it', () => {
    serve(undefined);
    const onCommand = vi.fn();
    const { getByRole } = view(
      <SlotCardNode
        {...props(
          toolsCard({
            ...dagTools('lookup'),
            mcp: [
              { name: 'docs', url: 'https://mcp.example.com/v1', allow: [] },
            ],
          })
        )}
      />,
      { actions: { onCommand } }
    );
    fireEvent.click(getByRole('button', { name: 'docs' }));
    expect(onCommand).toHaveBeenCalledWith({
      kind: 'openTool',
      tool: { step: 'steps[1]', category: 'mcp', index: 0 },
    });
  });

  it('lights the row of the selected tool only', () => {
    serve(undefined);
    const { getAllByTestId } = view(
      <SlotCardNode {...props(toolsCard(dagTools('lookup', 'refund')))} />,
      {
        actions: {
          selectedTool: { step: 'steps[1]', category: 'dags', index: 1 },
        },
      }
    );
    const rows = getAllByTestId('ve-canvas-slot-row');
    expect(rows[0]).not.toHaveAttribute('data-selected');
    expect(rows[1]).toHaveAttribute('data-selected', 'true');
    expect(rows[1]).toHaveAttribute('aria-current', 'true');
  });

  it('does not light a row of another step', () => {
    serve(undefined);
    const { getByTestId } = view(
      <SlotCardNode {...props(toolsCard(dagTools('lookup')))} />,
      {
        actions: {
          selectedTool: { step: 'steps[2]', category: 'dags', index: 0 },
        },
      }
    );
    expect(getByTestId('ve-canvas-slot-row')).not.toHaveAttribute(
      'data-selected'
    );
  });

  it('opens the dock of the Skills list on a click on a skill', () => {
    const onCommand = vi.fn();
    const { getByRole } = view(
      <SlotCardNode {...props(skillsCard(['a', 'b']))} />,
      { actions: { onCommand } }
    );
    fireEvent.click(getByRole('button', { name: 'b' }));
    expect(onCommand).toHaveBeenCalledWith({
      kind: 'openSlotPanel',
      step: 'steps[1]',
      slot: 'skills',
    });
  });

  it('opens the dock of the whole list from a double click and Enter and Space on the card, and not twice from a skill row', () => {
    serve(undefined);
    const onCommand = vi.fn();
    const skills = view(<SlotCardNode {...props(skillsCard(['a', 'b']))} />, {
      actions: { onCommand },
    });
    const slotPanel = {
      kind: 'openSlotPanel',
      step: 'steps[1]',
      slot: 'skills',
    };
    // A double click on a row is two clicks: the card does not open it again.
    fireEvent.click(skills.getByRole('button', { name: 'b' }));
    fireEvent.doubleClick(skills.getByRole('button', { name: 'b' }));
    expect(onCommand).toHaveBeenCalledTimes(1);
    expect(onCommand).toHaveBeenLastCalledWith(slotPanel);
    const card = skills.getByRole('group', { name: 'Skills' });
    fireEvent.doubleClick(card);
    expect(onCommand).toHaveBeenLastCalledWith(slotPanel);
    card.focus();
    expect(document.activeElement).toBe(card);
    fireEvent.keyDown(card, { key: 'Enter' });
    fireEvent.keyDown(card, { key: ' ' });
    fireEvent.keyDown(card, { key: 'a' });
    expect(
      onCommand.mock.calls.filter(([c]) => c.kind === 'openSlotPanel')
    ).toHaveLength(4);
    skills.unmount();
    // The Tools card says the same with its own slot.
    const tools = view(<SlotCardNode {...props(toolsCard(dagTools('a')))} />, {
      actions: { onCommand },
    });
    fireEvent.keyDown(tools.getByRole('group', { name: 'Tools' }), {
      key: 'Enter',
    });
    expect(onCommand).toHaveBeenLastCalledWith({
      kind: 'openSlotPanel',
      step: 'steps[1]',
      slot: 'tools',
    });
  });

  it('lets a key on a row act on the row only, not on the card', () => {
    serve(undefined);
    const onCommand = vi.fn();
    const { getByRole } = view(
      <SlotCardNode {...props(toolsCard(dagTools('a')))} />,
      { actions: { onCommand } }
    );
    fireEvent.keyDown(getByRole('button', { name: 'a' }), { key: 'Enter' });
    expect(onCommand).toHaveBeenCalledTimes(1);
    expect(onCommand).toHaveBeenCalledWith({
      kind: 'openTool',
      tool: { step: 'steps[1]', category: 'dags', index: 0 },
    });
  });

  it('opens the dock from the add row of each card, and does not open the YAML', () => {
    serve(undefined);
    const onCommand = vi.fn();
    const tools = view(<SlotCardNode {...props(toolsCard(dagTools('a')))} />, {
      actions: { onCommand },
    });
    fireEvent.click(tools.getByRole('button', { name: 'Add a tool' }));
    expect(onCommand).toHaveBeenLastCalledWith({
      kind: 'openSlotDock',
      step: 'steps[1]',
      slot: 'tools',
    });
    tools.unmount();
    const skills = view(<SlotCardNode {...props(skillsCard([]))} />, {
      actions: { onCommand },
    });
    fireEvent.click(skills.getByRole('button', { name: 'Add a skill' }));
    expect(onCommand).toHaveBeenLastCalledWith({
      kind: 'openSlotDock',
      step: 'steps[1]',
      slot: 'skills',
    });
    expect(onCommand).toHaveBeenCalledTimes(2);
  });

  it('has a ring while selected', () => {
    serve(undefined);
    const plain = view(<SlotCardNode {...props(toolsCard(dagTools('a')))} />);
    expect(plain.getByRole('group', { name: 'Tools' })).not.toHaveAttribute(
      'data-selected'
    );
    plain.unmount();
    const picked = view(
      <SlotCardNode {...props(toolsCard(dagTools('a')), true)} />
    );
    const group = picked.getByRole('group', { name: 'Tools' });
    expect(group).toHaveAttribute('data-selected', 'true');
    expect(group.className).toContain('ring-primary');
  });
});

function chatStep(over: Partial<CanvasNodeWithRoutes> = {}) {
  const node: CanvasNodeWithRoutes = {
    key: 'steps[1]',
    label: 'triage',
    kind: 'action',
    action: 'chat.completion',
    subtitle: 'chat.completion',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
    chat: { models: [{ name: 'gpt-4o', provider: 'openai' }], skills: [] },
    subRow: {
      items: [
        { key: 'toolsat:steps[1]:model', slot: 'model' },
        { key: 'toolsat:steps[1]:tools', slot: 'tools', rows: 2 },
        { key: 'toolsat:steps[1]:skills', slot: 'skills', rows: 1 },
      ],
    },
    ...over,
  };
  return node;
}

describe('StepNode with a row of sub-nodes', () => {
  it('has a large card, and a box as tall as the layout says, with the three slots labelled', () => {
    const node = chatStep();
    const { getByTestId, container } = view(<StepNode {...props(node)} />);
    const frame = getByTestId('ve-canvas-sub-frame');
    const size = nodeSize(node, false, 'TB');
    expect(frame.style.width).toBe(`${size.width}px`);
    expect(frame.style.height).toBe(`${size.height}px`);
    expect(getByTestId('ve-canvas-slot-model')).toHaveTextContent('Model');
    expect(getByTestId('ve-canvas-slot-tools')).toHaveTextContent('Tools');
    expect(getByTestId('ve-canvas-slot-skills')).toHaveTextContent('Skills');
    for (const id of ['slot:model', 'slot:tools', 'slot:skills']) {
      const handle = container.querySelector(`[data-handle-id="${id}"]`);
      expect(handle, id).not.toBeNull();
      expect(handle).toHaveAttribute('data-handle-type', 'source');
    }
    const card = getByTestId('ve-canvas-node-steps[1]');
    expect(card.style.width).toBe(`${CHAT_NODE_WIDTH}px`);
    expect(card).toHaveTextContent('triage');
    expect(card).toHaveTextContent('chat.completion');
  });

  it('puts the entry and the exit on the top and the bottom of the card, in the middle of the box', () => {
    const node = chatStep();
    const { container } = view(<StepNode {...props(node)} />);
    const size = nodeSize(node, false, 'TB');
    const plan = chatPlan(node, false, 'TB')!;
    const target = container.querySelector<HTMLElement>(
      '[data-handle-id="target"]'
    )!;
    const source = container.querySelector<HTMLElement>(
      '[data-handle-id="source"]'
    )!;
    // The box is wider than the card, and taller when a column is: the
    // handles are on the card, on the middle of the box.
    expect(size.width).toBeGreaterThan(CHAT_NODE_WIDTH);
    expect(target.style.top).toBe(`${plan.card.y}px`);
    expect(source.style.top).toBe(`${plan.base.y + plan.base.height}px`);
    expect(target.style.left).toBe(`${size.width / 2}px`);
    expect(source.style.left).toBe(`${size.width / 2}px`);
    expect(
      container.querySelector(
        '[data-testid="ve-canvas-node-steps[1]"] [data-handle-id]'
      )
    ).toBeNull();
  });

  it('puts the entry and the exit on the sides of the card left to right', () => {
    const node = chatStep();
    const { container } = view(<StepNode {...props(node)} />, {
      actions: { direction: 'LR' },
    });
    const size = nodeSize(node, false, 'LR');
    const plan = chatPlan(node, false, 'LR')!;
    const target = container.querySelector<HTMLElement>(
      '[data-handle-id="target"]'
    )!;
    const source = container.querySelector<HTMLElement>(
      '[data-handle-id="source"]'
    )!;
    // The box is wider than the card: the handles are not on its sides.
    expect(size.width).toBeGreaterThan(CHAT_NODE_WIDTH);
    expect(target.style.left).toBe(`${plan.base.x}px`);
    expect(source.style.left).toBe(`${plan.base.x + CHAT_NODE_WIDTH}px`);
    expect(target.style.top).toBe(`${size.height / 2}px`);
    expect(source.style.top).toBe(`${size.height / 2}px`);
  });

  it('keeps the frame and the caption in the compact canvas, with a larger tile', () => {
    const { getByTestId } = view(<StepNode {...props(chatStep())} />, {
      compact: true,
    });
    expect(getByTestId('ve-canvas-sub-frame')).toBeInTheDocument();
    expect(getByTestId('ve-canvas-node-caption')).toHaveTextContent('triage');
    expect(getByTestId('ve-canvas-slot-skills')).toBeInTheDocument();
    expect(getByTestId('ve-canvas-node-steps[1]').style.width).toBe('80px');
  });
});

function modelNode(models: ModelRef[]) {
  const node: CanvasNodeWithRoutes = {
    key: 'toolsat:steps[1]:model',
    label: models[0]?.name ?? '',
    kind: 'other',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
    modelSat: { owner: 'steps[1]', models },
  };
  return node;
}

describe('ModelSubNode', () => {
  it('shows the model and its provider', () => {
    const { getByTestId, queryByTestId } = view(
      <ModelSubNode
        {...props(modelNode([{ name: 'gpt-4o', provider: 'openai' }]))}
      />
    );
    const card = getByTestId('ve-canvas-node-toolsat:steps[1]:model');
    expect(card).toHaveTextContent('gpt-4o');
    expect(card).toHaveTextContent('openai');
    expect(queryByTestId('ve-canvas-model-more')).toBeNull();
  });

  it('names the first model of a list and counts the others', () => {
    const { getByTestId } = view(
      <ModelSubNode
        {...props(
          modelNode([
            { name: 'a', provider: 'p1' },
            { name: 'b' },
            { name: 'c', provider: 'p3' },
          ])
        )}
      />
    );
    expect(getByTestId('ve-canvas-model-more')).toHaveTextContent('+2');
    const title = getByTestId(
      've-canvas-node-toolsat:steps[1]:model'
    ).getAttribute('title');
    expect(title).toContain('b');
    expect(title).toContain('p3');
  });

  it('names the provider of an agent step under the model', () => {
    const { getByTestId } = view(
      <ModelSubNode
        {...props(
          modelNode([{ name: 'claude-haiku-4-5', provider: 'claude' }])
        )}
      />
    );
    const card = getByTestId('ve-canvas-node-toolsat:steps[1]:model');
    expect(card).toHaveTextContent('claude-haiku-4-5');
    expect(card).toHaveTextContent('claude');
    expect(card).not.toHaveTextContent('cli');
  });

  it('shows the endpoint under the model, in the place of the provider', () => {
    const { getByTestId } = view(
      <ModelSubNode
        {...props(
          modelNode([{ name: 'sonnet', provider: 'claude', endpoint: 'cle1' }])
        )}
      />
    );
    const card = getByTestId('ve-canvas-node-toolsat:steps[1]:model');
    expect(card).toHaveTextContent('sonnet');
    expect(card).toHaveTextContent('cle1');
    expect(card).not.toHaveTextContent('claude');
    // The tooltip keeps the three names.
    expect(card.getAttribute('title')).toBe('sonnet\ncle1\nclaude');
  });

  it('shows the endpoint of a chat model under its name, and the provider of the old method', () => {
    const { getByTestId } = view(
      <ModelSubNode
        {...props(
          modelNode([
            { name: 'claude-haiku-4-5', endpoint: 'cle1' },
            { name: 'gpt-4o', provider: 'openai' },
          ])
        )}
      />
    );
    const card = getByTestId('ve-canvas-node-toolsat:steps[1]:model');
    expect(card).toHaveTextContent('claude-haiku-4-5');
    expect(card).toHaveTextContent('cle1');
    expect(card).toHaveTextContent('+1');
    // The tooltip lists each model with its endpoint or its provider.
    expect(card.getAttribute('title')).toBe(
      'claude-haiku-4-5\ncle1\ngpt-4o\nopenai'
    );
  });

  it('shows the model of an agent step, and no Agent file label', () => {
    const { getByTestId, queryByText } = view(
      <ModelSubNode {...props(modelNode([{ name: 'reviewer' }]))} />
    );
    const card = getByTestId('ve-canvas-node-toolsat:steps[1]:model');
    expect(card).toHaveTextContent('reviewer');
    expect(queryByText(/Agent file/)).toBeNull();
  });

  it('opens the Model section of the form from a double click and Enter, not from a click', () => {
    const onCommand = vi.fn();
    const { getByRole } = view(
      <ModelSubNode {...props(modelNode([{ name: 'gpt-4o' }]))} />,
      { actions: { onCommand } }
    );
    const node = getByRole('button', { name: 'gpt-4o' });
    // A click only selects: the canvas handles it. Enter and a double click open.
    fireEvent.click(node);
    expect(onCommand).not.toHaveBeenCalled();
    fireEvent.doubleClick(node);
    fireEvent.keyDown(node, { key: 'Enter' });
    expect(onCommand).toHaveBeenCalledTimes(2);
    expect(onCommand).toHaveBeenCalledWith({
      kind: 'openForm',
      step: 'steps[1]',
      section: 'model',
    });
  });

  it('has a ring while selected, and a tab stop with the name', () => {
    const node = modelNode([{ name: 'gpt-4o' }]);
    const plain = view(<ModelSubNode {...props(node)} />);
    const leaf = plain.getByRole('button', { name: 'gpt-4o' });
    expect(leaf).toHaveAttribute('tabindex', '0');
    expect(leaf).not.toHaveAttribute('data-selected');
    expect(plain.getByTestId('ve-canvas-sub-icon').className).not.toContain(
      'ring-primary'
    );
    plain.unmount();
    const picked = view(<ModelSubNode {...props(node, true)} />);
    expect(picked.getByRole('button', { name: 'gpt-4o' })).toHaveAttribute(
      'data-selected',
      'true'
    );
    expect(picked.getByTestId('ve-canvas-sub-icon').className).toContain(
      'ring-primary'
    );
  });
});

function addNode() {
  const node: CanvasNodeWithRoutes = {
    key: 'toolsat:steps[1]:add-model',
    label: '',
    kind: 'other',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
    addSat: { owner: 'steps[1]', slot: 'model' },
  };
  return node;
}

function slotAddNode(slot: 'tools' | 'skills' | 'prompt') {
  const node: CanvasNodeWithRoutes = {
    key: `toolsat:steps[1]:add-${slot}`,
    label: '',
    kind: 'other',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
    addSat: { owner: 'steps[1]', slot },
  };
  return node;
}

// The small round "+" of a Tools or Skills slot with no entry is the round
// "+" of the links of the canvas, not a card and not the dashed Model node.
describe('AddSubNode of the Tools and Skills slots', () => {
  it.each([
    ['tools', 'Add a tool'],
    ['skills', 'Add a skill'],
  ] as const)(
    'opens the dock of the %s slot from a round "+"',
    (slot, name) => {
      const onCommand = vi.fn();
      const { getByRole, getByTestId, queryByText } = view(
        <AddSubNode {...props(slotAddNode(slot))} />,
        { actions: { onCommand } }
      );
      const button = getByRole('button', { name });
      expect(button).toHaveAttribute('title', name);
      // The look of the "+" of a link.
      expect(button.className).toContain('rounded-full');
      expect(button.className).toContain('h-6 w-6');
      expect(button.className).not.toContain('border-dashed');
      fireEvent.click(button);
      expect(onCommand).toHaveBeenCalledWith({
        kind: 'openSlotDock',
        step: 'steps[1]',
        slot,
      });
      expect(
        getByTestId(`ve-canvas-node-toolsat:steps[1]:add-${slot}`)
      ).toHaveAttribute('data-slot', slot);
      expect(queryByText('No tools')).toBeNull();
    }
  );

  it('does not open the form on a double click', () => {
    const onCommand = vi.fn();
    const { getByRole } = view(
      <AddSubNode {...props(slotAddNode('tools'))} />,
      {
        actions: { onCommand },
      }
    );
    fireEvent.doubleClick(getByRole('button', { name: 'Add a tool' }));
    expect(onCommand).not.toHaveBeenCalled();
  });

  it('has no button on a read-only canvas, but keeps a dashed round gap', () => {
    const { queryByRole, getByRole } = view(
      <AddSubNode {...props(slotAddNode('skills'))} />,
      { actions: { editable: false } }
    );
    expect(queryByRole('button')).toBeNull();
    expect(getByRole('img', { name: 'No skills' }).className).toContain(
      'border-dashed'
    );
  });

  it('has its entry on the side that faces the step', () => {
    // Top to bottom, the Skills and the Tools sit right of their step: the
    // entry is on the left. Left to right they sit under it: the entry is on top.
    const entry = (
      slot: 'tools' | 'skills' | 'prompt',
      direction: 'TB' | 'LR'
    ) => {
      const node = view(<AddSubNode {...props(slotAddNode(slot))} />, {
        actions: { direction },
      });
      const position = node.container
        .querySelector('[data-handle-id="target"]')
        ?.getAttribute('data-handle-position');
      node.unmount();
      return position;
    };
    expect(entry('tools', 'TB')).toBe('left');
    expect(entry('skills', 'TB')).toBe('left');
    expect(entry('tools', 'LR')).toBe('top');
    expect(entry('skills', 'LR')).toBe('top');
    // The Prompt sits left of the step, top to bottom, and above it, left to
    // right: the entry is on the right, or on the bottom.
    expect(entry('prompt', 'TB')).toBe('right');
    expect(entry('prompt', 'LR')).toBe('bottom');
  });
});

describe('AddSubNode', () => {
  it('asks for a model on the empty Model slot, and opens the Model section', () => {
    const onCommand = vi.fn();
    const { getByRole, getByTestId } = view(
      <AddSubNode {...props(addNode())} />,
      {
        actions: { onCommand },
      }
    );
    const button = getByRole('button', { name: 'Add a model' });
    expect(button).toHaveAttribute('title', 'Add a model');
    expect(button.className).toContain('border-dashed');
    fireEvent.click(button);
    expect(onCommand).toHaveBeenCalledWith({
      kind: 'openForm',
      step: 'steps[1]',
      section: 'model',
    });
    expect(
      getByTestId('ve-canvas-node-toolsat:steps[1]:add-model')
    ).toHaveAttribute('data-slot', 'model');
  });

  it('has no button on a read-only canvas, but keeps the dashed gap', () => {
    const { queryByRole, getByRole } = view(
      <AddSubNode {...props(addNode())} />,
      {
        actions: { editable: false },
      }
    );
    expect(queryByRole('button')).toBeNull();
    expect(getByRole('img', { name: 'No model set' }).className).toContain(
      'border-dashed'
    );
  });
});

/** Serves the skill list of `/skills`, and the tool definitions of a run. */
function serveSkills(
  skills: { name: string; description: string }[],
  defs?: ToolDefinition[]
) {
  (useQuery as unknown as ReturnType<typeof vi.fn>).mockImplementation(
    (key: string) =>
      key === '/skills'
        ? { data: { skills } }
        : { data: defs ? { messages: [], toolDefinitions: defs } : undefined }
  );
}

function withBase(ui: ReactElement, basePath = '') {
  return (
    <ConfigContext.Provider value={{ basePath } as Config}>
      {ui}
    </ConfigContext.Provider>
  );
}

describe('a skill that the directory does not have', () => {
  it('shows a name that the skills list lacks in red, with the reason as tooltip', () => {
    serveSkills([{ name: 'known', description: 'A skill.' }]);
    const { getAllByTestId } = view(
      <SlotCardNode {...props(skillsCard(['known', 'missing']))} />
    );
    const [known, missing] = getAllByTestId('ve-canvas-slot-row');
    expect(known!.querySelector('.text-destructive')).toBeNull();
    expect(missing!.querySelector('svg')).toHaveClass('text-destructive');
    expect(missing!.querySelector('span')).toHaveClass('text-destructive');
    expect(missing).toHaveAttribute(
      'title',
      'missing\nSkill not found in the skills directory'
    );
  });

  it('marks nothing before the list has arrived', () => {
    (useQuery as unknown as ReturnType<typeof vi.fn>).mockImplementation(
      () => ({ data: undefined })
    );
    const { getAllByTestId } = view(
      <SlotCardNode {...props(skillsCard(['missing']))} />
    );
    expect(
      getAllByTestId('ve-canvas-slot-row')[0]!.querySelector(
        '.text-destructive'
      )
    ).toBeNull();
  });
});

describe('links from the cards', () => {
  it('gives a DAG row an open icon that opens a new tab', () => {
    serveSkills([]);
    const { getByRole, queryByRole } = view(
      withBase(
        <SlotCardNode
          {...props(
            toolsCard({
              ...dagTools('refund'),
              mcp: [
                { name: 'docs', url: 'https://mcp.example.com/v1', allow: [] },
              ],
            })
          )}
        />,
        '/dagu'
      )
    );
    const link = getByRole('link', { name: 'Open refund' });
    expect(link).toHaveAttribute('href', '/dagu/dags/refund');
    expect(link).toHaveAttribute('target', '_blank');
    expect(link.getAttribute('rel')).toContain('noopener');
    expect(link).toHaveAttribute('title', 'Opens in a new tab');
    expect(queryByRole('link', { name: 'Open docs' })).toBeNull();
  });

  it('gives an MCP and an invalid row no icon', () => {
    serveSkills([]);
    const { queryAllByRole } = view(
      <SlotCardNode
        {...props(
          toolsCard({
            dags: [42],
            mcp: [
              { name: 'docs', url: 'https://mcp.example.com/v1', allow: [] },
            ],
          })
        )}
      />
    );
    expect(queryAllByRole('link')).toHaveLength(0);
  });

  it('keeps the open icon in the tab order and does not open the YAML', () => {
    serveSkills([]);
    const onCommand = vi.fn();
    const { getByRole } = view(
      <SlotCardNode {...props(toolsCard(dagTools('refund')))} />,
      { actions: { onCommand } }
    );
    const link = getByRole('link', { name: 'Open refund' });
    expect(link).not.toHaveAttribute('tabindex', '-1');
    fireEvent.click(link);
    expect(onCommand).not.toHaveBeenCalled();
  });

  it('gives a skill row an open icon and its description as a tooltip', () => {
    serveSkills([{ name: 'refund-policy', description: 'How to refund' }]);
    const { getByRole } = view(
      <SlotCardNode {...props(skillsCard(['refund-policy']))} />
    );
    expect(getByRole('link', { name: 'Open refund-policy' })).toHaveAttribute(
      'href',
      '/ai/skills/refund-policy'
    );
    expect(
      getByRole('button', { name: 'refund-policy' }).getAttribute('title')
    ).toContain('How to refund');
  });
});
