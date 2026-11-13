// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { translateStatic } from '@/i18n/translateStatic';
import {
  buildToolSections,
  flatRows,
  limitFacts,
  matchesWords,
  mergeInputs,
  pickYaml,
  queryWords,
  rowChips,
  type ToolSectionsInput,
} from '../dockModel';
import { readBuiltinTools, readStepTools } from '../parse';
import type { DagCatalogItem } from '../useDagCatalog';

const ts = (text: string, values?: Record<string, string | number>) =>
  translateStatic('en', text, values);

const AGENT = {
  action: 'agent.run',
  with: {
    max_tool_iterations: 20,
    dags: [
      {
        name: 'lookup',
        params: {
          ticket: { from_ai: { description: 'The ticket.' } },
          verbose: true,
          channel: 'ops',
        },
      },
      { name: 'ops.notify' },
    ],
    tools: {
      mcp: [
        { name: 'docs', allow: ['search', 'read', 'list', 'get'] },
        { name: 'inline', url: 'https://example.com/mcp', allow: [] },
      ],
      builtin: {
        read: true,
        shell: { allow: ['go test *'], deny: ['rm *'] },
      },
    },
  },
};

const DAGS: DagCatalogItem[] = [
  { name: 'lookup', description: 'Look up a ticket.', params: [] },
  { name: 'refund', description: 'Refund an order.', params: [] },
  { name: 'notify', description: '', params: [] },
  { name: 'self', description: 'The DAG of the step.', params: [] },
];

function input(over: Partial<ToolSectionsInput> = {}): ToolSectionsInput {
  return {
    ts,
    step: 'agent',
    scope: 'tools',
    query: '',
    entries: readStepTools(AGENT).entries,
    builtin: readBuiltinTools(AGENT),
    dags: DAGS,
    dagsLoading: false,
    servers: ['docs', 'wiki'],
    exclude: 'self',
    fieldIssues: {},
    canAdd: true,
    ...over,
  };
}

const ids = (sections: ReturnType<typeof buildToolSections>) =>
  sections.map((section) => [section.id, section.rows.map((row) => row.id)]);

describe('queryWords and matchesWords', () => {
  it('lower-cases, splits on spaces and needs every word', () => {
    expect(queryWords('  Look  UP ')).toEqual(['look', 'up']);
    expect(matchesWords(['look', 'tick'], ['Look up a ticket'])).toBe(true);
    expect(matchesWords(['look', 'order'], ['Look up a ticket'])).toBe(false);
    expect(matchesWords([], ['anything'])).toBe(true);
  });
});

describe('buildToolSections', () => {
  it('lists the MCP servers and the built-in tools of the step, then what the catalogue can add, in the Tools dock', () => {
    const sections = buildToolSections(input());
    expect(ids(sections)).toEqual([
      ['step:mcp', ['step:mcp[0]', 'step:mcp[1]']],
      ['step:builtin', ['step:builtin:read', 'step:builtin:shell']],
      // A server that the step has is not offered; the new inline one is.
      ['catalog:mcp', ['catalog:mcp:wiki', 'catalog:mcp:new']],
      [
        'catalog:builtin',
        [
          'catalog:builtin:write',
          'catalog:builtin:edit',
          'catalog:builtin:search',
        ],
      ],
    ]);
    expect(sections.map((section) => section.title)).toEqual([
      'MCP servers',
      'Built-in tools',
      'MCP servers',
      'Built-in tools',
    ]);
  });

  it('lists the DAG tools of the step and the DAGs that it can add, in the DAGs dock', () => {
    const sections = buildToolSections(input({ scope: 'dags' }));
    expect(ids(sections)).toEqual([
      ['step:dag', ['step:dags[0]', 'step:dags[1]']],
      // The DAG of the step and the DAG tools that it has are not offered.
      ['catalog:dag', ['catalog:dag:refund', 'catalog:dag:notify']],
    ]);
    expect(sections.map((section) => section.title)).toEqual([
      'DAG tools',
      'DAG tools',
    ]);
  });

  // Each dock keeps to its kinds: no DAG in the Tools dock, no MCP server or built-in tool in the DAGs dock.
  it('shows no row of the other dock, with or without a search', () => {
    for (const query of ['', 'a', 'lookup', 'docs', 'read', 'ticket']) {
      const tools = flatRows(buildToolSections(input({ query })));
      expect(tools.some((row) => row.kind === 'dag')).toBe(false);
      const dags = flatRows(buildToolSections(input({ query, scope: 'dags' })));
      expect(dags.every((row) => row.kind === 'dag')).toBe(true);
    }
    // The empty reasons of the other dock are gone too.
    const bare = buildToolSections(
      input({ entries: [], builtin: [], dags: [], servers: [] })
    );
    expect(bare.map((s) => s.id)).toEqual(['catalog:mcp', 'catalog:builtin']);
  });

  it('keeps an item that is not a mapping in the list of its category', () => {
    const entries = readStepTools({
      with: { dags: [3], tools: { mcp: [4] } },
    }).entries;
    expect(
      ids(buildToolSections(input({ entries, builtin: [], canAdd: false })))
    ).toEqual([['step:mcp', ['step:mcp[0]']]]);
    expect(
      ids(
        buildToolSections(
          input({ entries, builtin: [], canAdd: false, scope: 'dags' })
        )
      )
    ).toEqual([['step:dag', ['step:dags[0]']]]);
  });

  it('gives each row its description, its tool name and its kind', () => {
    const [dag] = buildToolSections(input({ scope: 'dags' }));
    const [mcp, builtin] = buildToolSections(input());
    expect(dag!.rows[0]).toMatchObject({
      title: 'lookup',
      description: 'Look up a ticket.',
      kind: 'dag',
      tag: '',
    });
    // The model sees `ops_notify` for the DAG `ops.notify` (Spec 090).
    expect(dag!.rows[1]).toMatchObject({
      title: 'ops.notify',
      tag: 'ops_notify',
    });
    expect(mcp!.rows[0]).toMatchObject({
      description: 'Server entry of the AI page',
    });
    expect(mcp!.rows[1]).toMatchObject({
      description: 'https://example.com/mcp',
    });
    expect(builtin!.rows[1]).toMatchObject({
      title: 'Shell commands',
      kind: 'builtin',
    });
  });

  it('marks a known DAG with no description as blank, and an unknown DAG as nothing', () => {
    const sections = buildToolSections(
      input({
        scope: 'dags',
        entries: readStepTools({
          with: { dags: [{ name: 'notify' }, { name: 'ghost' }] },
        }).entries,
      })
    );
    const [first, second] = sections[0]!.rows;
    expect(first!.blank).toBe(true);
    expect(second!.blank).toBeUndefined();
  });

  it('flags a DAG that the complete directory lacks, and no name that a reference gives', () => {
    const entries = readStepTools({
      with: {
        dags: [{ name: 'ghost' }, { name: '${TOOL}' }, { name: 'lookup' }],
      },
    }).entries;
    const flags = (over: Partial<ToolSectionsInput>) =>
      buildToolSections(
        input({ entries, builtin: [], scope: 'dags', ...over })
      )[0]!.rows.map((row) => row.unknown === true);
    expect(flags({ dagsComplete: true })).toEqual([true, false, false]);
    // A list that is not complete, or not loaded, claims nothing.
    expect(flags({})).toEqual([false, false, false]);
    expect(flags({ dagsComplete: true, dagsLoading: true })).toEqual([
      false,
      false,
      false,
    ]);
    const [ghost] = buildToolSections(
      input({ entries, builtin: [], dagsComplete: true, scope: 'dags' })
    )[0]!.rows;
    expect(rowChips(ts, ghost!).map((chip) => [chip.text, chip.tone])).toEqual([
      ['Not in the DAGs directory', 'warning'],
    ]);
  });

  it('keeps the rows that match every word, and drops the empty sections', () => {
    const dags = (query: string) =>
      buildToolSections(input({ query, scope: 'dags' }));
    // Matches the description of lookup and the one of the catalogue DAG lookup.
    expect(ids(dags('tick'))).toEqual([['step:dag', ['step:dags[0]']]]);
    expect(ids(dags('ticket verbose'))).toEqual([
      ['step:dag', ['step:dags[0]']],
    ]);
    // An allowed tool of a server, and a shell pattern, are searched too.
    expect(ids(buildToolSections(input({ query: 'read' }))).flat(2)).toContain(
      'step:mcp[0]'
    );
    expect(
      flatRows(buildToolSections(input({ query: 'go test' }))).map((r) => r.id)
    ).toEqual(['step:builtin:shell']);
    expect(buildToolSections(input({ query: 'nothing like it' }))).toEqual([]);
    expect(dags('nothing like it')).toEqual([]);
  });

  it('ranks an exact name and a prefix first in the catalogue', () => {
    const dags: DagCatalogItem[] = [
      { name: 'my-refund-tool', description: '', params: [] },
      { name: 'refund-order', description: '', params: [] },
      { name: 'refund', description: '', params: [] },
    ];
    const sections = buildToolSections(
      input({ query: 'refund', dags, entries: [], builtin: [], scope: 'dags' })
    );
    expect(sections[0]!.rows.map((row) => row.title)).toEqual([
      'refund',
      'refund-order',
      'my-refund-tool',
    ]);
  });

  it('says why a catalogue section has no row, without a search', () => {
    const none = buildToolSections(
      input({
        servers: [],
        builtin: readBuiltinTools({
          with: {
            tools: {
              builtin: {
                read: true,
                write: true,
                edit: true,
                search: true,
                shell: { allow: ['x'] },
              },
            },
          },
        }),
      })
    );
    expect(
      none.filter((s) => s.group === 'catalog').map((s) => [s.id, s.empty])
    ).toEqual([
      ['catalog:mcp', 'no-servers'],
      ['catalog:builtin', 'all-builtin'],
    ]);
    const noDags = buildToolSections(input({ dags: [], scope: 'dags' }));
    expect(
      noDags.filter((s) => s.group === 'catalog').map((s) => [s.id, s.empty])
    ).toEqual([['catalog:dag', 'no-dags']]);
    const loading = buildToolSections(
      input({ dags: [], dagsLoading: true, scope: 'dags' })
    );
    expect(loading.find((s) => s.id === 'catalog:dag')!.empty).toBe('loading');
    const taken = buildToolSections(
      input({
        dags: DAGS.filter((dag) => dag.name === 'lookup'),
        scope: 'dags',
      })
    );
    expect(taken.find((s) => s.id === 'catalog:dag')!.empty).toBe('all-dags');
    const servers = buildToolSections(input({ servers: ['docs'] }));
    // The new inline server stays a row: the note says why no entry is left.
    expect(servers.find((s) => s.id === 'catalog:mcp')!.empty).toBe(
      'all-servers'
    );
    // A search shows no section that has no match, empty or not.
    expect(buildToolSections(input({ query: 'zzz', dags: [] }))).toEqual([]);
  });

  it('has no built-in tools on a chat step, and no catalogue in a view that only reads', () => {
    const chat = buildToolSections(input({ step: 'chat', builtin: [] }));
    expect(chat.map((s) => s.id)).toEqual(['step:mcp', 'catalog:mcp']);
    const chatDags = buildToolSections(
      input({ step: 'chat', builtin: [], scope: 'dags' })
    );
    expect(chatDags.map((s) => s.id)).toEqual(['step:dag', 'catalog:dag']);
    for (const scope of ['tools', 'dags'] as const) {
      const reading = buildToolSections(input({ canAdd: false, scope }));
      expect(reading.every((s) => s.group === 'step')).toBe(true);
    }
  });

  it('puts the issues of an entry and of its fields on its row', () => {
    const issue = {
      severity: 'error',
      source: 'build',
      message: 'bad',
    } as never;
    const fieldIssues = {
      'with.dags[0].params.verbose': [issue],
      'with.tools.builtin.shell': [issue, issue],
    };
    const rows = [
      ...flatRows(buildToolSections(input({ scope: 'dags', fieldIssues }))),
      ...flatRows(buildToolSections(input({ fieldIssues }))),
    ];
    expect(rows.find((r) => r.id === 'step:dags[0]')!.issues).toHaveLength(1);
    expect(rows.find((r) => r.id === 'step:dags[1]')!.issues).toHaveLength(0);
    expect(
      rows.find((r) => r.id === 'step:builtin:shell')!.issues
    ).toHaveLength(2);
  });
});

describe('rowChips', () => {
  const rows = [
    ...flatRows(buildToolSections(input({ scope: 'dags' }))),
    ...flatRows(buildToolSections(input())),
  ];
  const chips = (id: string) =>
    rowChips(ts, rows.find((row) => row.id === id)!).map((c) => [
      c.id,
      c.text,
      c.tone,
    ]);

  it('counts the fixed inputs and the inputs of the model of a DAG tool', () => {
    expect(chips('step:dags[0]')).toEqual([
      ['fixed', '2 fixed', 'default'],
      ['model', '1 from the model', 'default'],
    ]);
    expect(chips('step:dags[1]')).toEqual([]);
  });

  it('shows the allow list of an MCP server, and warns about an empty one', () => {
    expect(chips('step:mcp[0]')).toEqual([
      ['allow', 'Allow: search, read, list and 1 more', 'default'],
    ]);
    expect(chips('step:mcp[1]')).toEqual([
      ['allow', 'No allowed tools', 'warning'],
    ]);
  });

  it('shows the allow and deny rules of the shell tool', () => {
    expect(chips('step:builtin:shell')).toEqual([
      ['allow', 'Allow: go test *', 'default'],
      ['deny', 'Deny: rm *', 'default'],
    ]);
    expect(chips('step:builtin:read')).toEqual([]);
  });
});

describe('mergeInputs', () => {
  const entry = readStepTools(AGENT).entries[0]!;
  const declared = [
    {
      name: 'ticket',
      type: 'string',
      required: true,
      description: 'Id',
      fallback: '',
      choices: [],
    },
    {
      name: 'verbose',
      type: 'boolean',
      required: false,
      description: '',
      fallback: 'false',
      choices: [],
    },
    {
      name: 'extra',
      type: 'string',
      required: false,
      description: '',
      fallback: '',
      choices: [],
    },
  ];

  it('gives each declared input its state: from the model, fixed, or open', () => {
    const rows = mergeInputs(entry.params, declared);
    expect(rows.map((r) => [r.name, r.state, r.undeclared])).toEqual([
      ['ticket', 'from_ai', false],
      ['verbose', 'fixed', false],
      ['extra', 'open', false],
      // A key of params that the DAG does not declare.
      ['channel', 'fixed', true],
    ]);
    expect(rows[0]).toMatchObject({ modelText: 'The ticket.', required: true });
    expect(rows[1]).toMatchObject({ value: 'true', type: 'boolean' });
  });

  it('shows only the params, and flags none, while the DAG is not known', () => {
    const rows = mergeInputs(entry.params, null);
    expect(rows.map((r) => [r.name, r.undeclared])).toEqual([
      ['ticket', false],
      ['verbose', false],
      ['channel', false],
    ]);
  });
});

describe('limitFacts', () => {
  it('shows the limits of an agent step with the defaults', () => {
    expect(
      limitFacts(ts, 'agent', { with: { max_tool_iterations: 20 } }).map(
        (f) => [f.id, f.value, f.isDefault]
      )
    ).toEqual([
      ['turns', '20', false],
      ['calls', '100 (default)', true],
      ['timeout', 'none', true],
    ]);
    expect(
      limitFacts(ts, 'agent', {
        timeout_sec: 90,
        with: { max_tool_calls: 5 },
      }).map((f) => f.value)
    ).toEqual(['no limit', '5', '90 s']);
  });

  it('shows the limits of a chat step', () => {
    expect(limitFacts(ts, 'chat', {}).map((f) => f.value)).toEqual([
      '64 (default)',
      '512 KiB (default)',
    ]);
    expect(
      limitFacts(ts, 'chat', {
        with: { max_tools: 3, tool_result_max_bytes: 0 },
      }).map((f) => f.value)
    ).toEqual(['3', 'no limit']);
  });
});

describe('pickYaml', () => {
  it('writes what each kind of pick adds under with', () => {
    // The DAG tools have the key `dags` of `with`; the others are in `tools`.
    expect(pickYaml({ category: 'dags', entry: { name: 'lookup' } })).toBe(
      'dags:\n  - name: lookup'
    );
    expect(
      pickYaml({ category: 'mcp', entry: { name: 'docs', allow: [] } })
    ).toBe('tools:\n  mcp:\n    - name: docs\n      allow: []');
    expect(pickYaml({ category: 'builtin', tool: 'read' })).toBe(
      'tools:\n  builtin:\n    read: true'
    );
    expect(pickYaml({ category: 'builtin', tool: 'shell' })).toBe(
      'tools:\n  builtin:\n    shell:\n      allow: []'
    );
  });
});
