// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { readStepTools } from '../../tools/parse';
import type { BuiltinEntryModel } from '../../tools/model';
import { planToolCard, splitDagEntries } from '../toolGroups';

function entries(tools: { dags?: unknown; mcp?: unknown }) {
  // The DAG tools are in `with.dags`, the MCP servers in `with.tools.mcp`.
  return readStepTools({
    with: { dags: tools.dags, tools: { mcp: tools.mcp } },
  }).entries;
}

const READ: BuiltinEntryModel = { name: 'read' };

const dags = (n: number) =>
  Array.from({ length: n }, (_, i) => ({ name: `d${i}` }));
const mcps = (n: number) =>
  Array.from({ length: n }, (_, i) => ({
    name: `m${i}`,
    url: `https://x${i}`,
    allow: [],
  }));
const builtins = (n: number): BuiltinEntryModel[] =>
  (['read', 'search', 'shell', 'write', 'edit'] as const)
    .slice(0, n)
    .map((name) => ({ name }));
const kindsOf = (plan: ReturnType<typeof planToolCard>) =>
  plan.columns.map((column) => column.map((group) => group.kind));

describe('planToolCard', () => {
  it('has no group and no heading without a tool', () => {
    expect(planToolCard([], [])).toEqual({
      groups: [],
      headed: false,
      hidden: [],
      headings: 0,
      columns: [],
      rows: 0,
    });
  });

  it('keeps one group without a heading when the step has one kind', () => {
    const plan = planToolCard(
      entries({ dags: [{ name: 'a' }, { name: 'b' }] }),
      []
    );
    expect(plan.groups.map((group) => group.kind)).toEqual(['dag']);
    expect(plan.groups[0]!.total).toBe(2);
    expect(plan.headed).toBe(false);
    expect(plan.headings).toBe(0);
  });

  it('groups the tools by kind in the order DAG, MCP, built-in, with a heading each', () => {
    const plan = planToolCard(
      entries({
        mcp: [{ name: 'docs', url: 'https://x', allow: [] }],
        dags: [{ name: 'a' }, { name: 'b' }],
      }),
      [READ]
    );
    expect(plan.groups.map((group) => group.kind)).toEqual([
      'dag',
      'mcp',
      'builtin',
    ]);
    expect(plan.groups.map((group) => group.items.length)).toEqual([2, 1, 1]);
    expect(plan.headed).toBe(true);
    expect(plan.headings).toBe(3);
  });

  it('lists the tools of a group in the order of the YAML', () => {
    const plan = planToolCard(
      entries({ dags: [{ name: 'b' }, { name: 'a' }] }),
      []
    );
    expect(
      plan.groups[0]!.items.map(
        (item) => (item.entry as { label: string }).label
      )
    ).toEqual(['b', 'a']);
  });

  it('lists at most `max` tools of one kind and hides the others', () => {
    const plan = planToolCard(entries({ dags: dags(5) }), [], 4);
    expect(plan.groups.map((group) => group.items.length)).toEqual([4]);
    // The count of a heading is the size of the group, not what shows.
    expect(plan.groups.map((group) => group.total)).toEqual([5]);
    expect(plan.hidden).toHaveLength(1);
  });
});

// Above eight tools of more than one kind, each kind has its own cap, and the
// kinds share columns.
describe('planToolCard in columns', () => {
  it('keeps one column with the global cap for one kind', () => {
    const plan = planToolCard(entries({ dags: dags(12) }), []);
    expect(kindsOf(plan)).toEqual([['dag']]);
    expect(plan.groups[0]!.items).toHaveLength(8);
    expect(plan.hidden).toHaveLength(4);
    expect(plan.groups[0]!.hidden).toEqual([]);
    // No heading: eight tools and the "+N more" row.
    expect(plan.rows).toBe(9);
  });

  it('keeps one column while it has at most ten rows', () => {
    const plan = planToolCard(
      entries({ dags: dags(3), mcp: mcps(1) }),
      builtins(3)
    );
    expect(kindsOf(plan)).toEqual([['dag', 'mcp', 'builtin']]);
    // Seven tools and three headings.
    expect(plan.rows).toBe(10);
  });

  it('opens columns when the headings make the column pass ten rows, even with eight tools', () => {
    // The Tools card of the demo: 2 DAG tools, 1 MCP server and 5 built-in tools.
    const plan = planToolCard(
      entries({ dags: dags(2), mcp: mcps(1) }),
      builtins(5)
    );
    expect(kindsOf(plan)).toEqual([['dag', 'mcp'], ['builtin']]);
    expect(plan.rows).toBe(6);
  });

  it('puts DAG tools in one column, and MCP servers with built-in tools in another', () => {
    const plan = planToolCard(
      entries({ dags: dags(12), mcp: mcps(2) }),
      builtins(5)
    );
    expect(kindsOf(plan)).toEqual([['dag'], ['mcp', 'builtin']]);
    // First column: heading, eight tools and "+N more". Second: 2 + 5 tools and two headings.
    expect(plan.rows).toBe(10);
    expect(plan.headed).toBe(true);
    expect(plan.headings).toBe(3);
  });

  it('caps each kind at `max` tools with its own hidden tools', () => {
    const plan = planToolCard(
      entries({ dags: dags(12), mcp: mcps(9) }),
      builtins(3)
    );
    expect(plan.groups.map((g) => g.items.length)).toEqual([8, 8, 3]);
    expect(plan.groups.map((g) => g.total)).toEqual([12, 9, 3]);
    expect(plan.groups.map((g) => g.hidden.length)).toEqual([4, 1, 0]);
    expect(plan.hidden).toHaveLength(5);
    // The tools of a group keep the order of the YAML.
    expect(plan.groups[0]!.hidden[0]).toBe(plan.hidden[0]);
  });

  it('uses the fewest columns whose tallest column is short, else the shortest tallest column', () => {
    // Heights 3, 3 and 6: two columns reach 6, and a third does not help.
    const two = planToolCard(
      entries({ dags: dags(2), mcp: mcps(2) }),
      builtins(5)
    );
    expect(kindsOf(two)).toEqual([['dag', 'mcp'], ['builtin']]);
    expect(two.rows).toBe(6);
    // Heights 9, 9 and 6: one column each.
    const three = planToolCard(
      entries({ dags: dags(8), mcp: mcps(8) }),
      builtins(5)
    );
    expect(kindsOf(three)).toEqual([['dag'], ['mcp'], ['builtin']]);
    expect(three.rows).toBe(9);
  });

  it('keeps two columns when the tallest has at most 10 rows, even if a third would be shorter', () => {
    // Heights 8, 3 and 6: three columns would give 8, two give 9.
    const plan = planToolCard(
      entries({ dags: dags(7), mcp: mcps(2) }),
      builtins(5)
    );
    expect(kindsOf(plan)).toEqual([['dag'], ['mcp', 'builtin']]);
    expect(plan.rows).toBe(9);
  });

  it('keeps the order of the kinds in the columns', () => {
    const plan = planToolCard(
      entries({ mcp: mcps(9), dags: dags(1) }),
      builtins(5)
    );
    expect(kindsOf(plan).flat()).toEqual(['dag', 'mcp', 'builtin']);
  });
});

describe('splitDagEntries', () => {
  it('gives the DAG tools to the DAGs card and the MCP servers to the Tools card, each in order', () => {
    const { dags: dagEntries, others } = splitDagEntries(
      entries({ dags: [{ name: 'a' }, { name: 'b' }], mcp: mcps(2) })
    );
    expect(dagEntries.map((entry) => entry.label)).toEqual(['a', 'b']);
    expect(others.map((entry) => entry.label)).toEqual(['m0', 'm1']);
  });

  it('keeps an item of `dags` that is not a mapping on the DAGs card, as an invalid entry', () => {
    const { dags: dagEntries, others } = splitDagEntries(
      entries({ dags: ['lookup'] })
    );
    expect(dagEntries.map((entry) => entry.kind)).toEqual(['invalid']);
    expect(others).toEqual([]);
  });

  it('has two empty lists without a tool', () => {
    expect(splitDagEntries([])).toEqual({ dags: [], others: [] });
  });
});
