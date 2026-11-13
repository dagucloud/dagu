// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { parseDagDoc } from '../../doc';
import { entryKey, entryPath } from '../model';
import { readBuiltinTools, readStepTools } from '../parse';

function tools(yaml: string) {
  const model = parseDagDoc(yaml);
  return readStepTools(model.steps[0]!.value);
}

const HEAD = `steps:
  - id: triage
    action: chat.completion
    with:
      prompt: Handle it
`;

describe('readStepTools', () => {
  it('returns nothing for a step without tools', () => {
    expect(tools(HEAD)).toEqual({ entries: [] });
    expect(readStepTools({ run: 'echo' })).toEqual({ entries: [] });
  });

  it('reads dags entries with their params', () => {
    const model = tools(`${HEAD}      dags:
        - name: refund
          params:
            customer_id: \${CUSTOMER_ID}
            amount:
              from_ai:
                description: Amount in euros
            note:
              from_ai: true
`);
    const [refund] = model.entries;
    expect(refund).toMatchObject({
      category: 'dags',
      index: 0,
      kind: 'dag',
      label: 'refund',
    });
    expect(refund?.params).toEqual([
      { name: 'customer_id', state: 'fixed', value: '${CUSTOMER_ID}' },
      { name: 'amount', state: 'from_ai', description: 'Amount in euros' },
      { name: 'note', state: 'from_ai' },
    ]);
  });

  it('lists the categories as dags, mcp whatever their order in the file', () => {
    const model = tools(`${HEAD}      tools:
        mcp:
          - name: docs
            url: http://x/mcp
            allow: [search]
      dags:
        - name: d2
        - name: d1
`);
    expect(model.entries.map(entryKey)).toEqual([
      'dags[0]',
      'dags[1]',
      'mcp[0]',
    ]);
    expect(model.entries.map((entry) => entry.label)).toEqual([
      'd2',
      'd1',
      'docs',
    ]);
    expect(entryPath(model.entries[2]!)).toBe('with.tools.mcp[0]');
  });

  it('reads an entry that still has an if key without a condition model', () => {
    const model = tools(`${HEAD}      dags:
        - name: refund
          if:
            - condition: \${TIER}
              expected: gold
          params:
            CUSTOMER_ID: c-1
`);
    const entry = model.entries[0]!;
    expect(entry.kind).toBe('dag');
    expect(entry.label).toBe('refund');
    expect(entry.params).toEqual([
      { name: 'CUSTOMER_ID', state: 'fixed', value: 'c-1' },
    ]);
    expect(entry).not.toHaveProperty('conditions');
  });

  it('reads an mcp entry with its name, url, headers and allow list', () => {
    const model = tools(`${HEAD}      tools:
        mcp:
          - name: docs
            url: https://mcp.example.com/v1
            headers:
              Authorization: Bearer x
            allow: [search]
`);
    const mcp = model.entries[0]!;
    expect(mcp.kind).toBe('mcp');
    expect(mcp.label).toBe('docs');
    expect(mcp.mcp).toEqual({
      named: false,
      url: 'https://mcp.example.com/v1',
      host: 'mcp.example.com',
      headers: { Authorization: 'Bearer x' },
      allow: ['search'],
    });
  });

  it('keeps a templated MCP url as the host', () => {
    const model = tools(`${HEAD}      tools:
        mcp:
          - name: docs
            url: \${MCP_URL}
            allow: [search]
`);
    expect(model.entries[0]?.mcp?.host).toBe('${MCP_URL}');
  });

  it('flags an item that is not a mapping and keeps its place', () => {
    const model = tools(`${HEAD}      dags:
        - lookup
        - name: refund
      tools:
        mcp:
          - 3
`);
    expect(model.entries.map((entry) => entry.kind)).toEqual([
      'invalid',
      'dag',
      'invalid',
    ]);
    expect(model.entries[0]).toMatchObject({
      category: 'dags',
      index: 0,
      problem: expect.any(String),
    });
    expect(model.entries[1]).toMatchObject({ index: 1, label: 'refund' });
    expect(model.entries[2]).toMatchObject({ category: 'mcp', index: 0 });
  });

  it('gives no entry for an unknown category or builtin', () => {
    expect(
      tools(
        `${HEAD}      tools:\n        toolsets: [a]\n        actions: [{ name: a }]\n        builtin: { read: true }\n`
      ).entries
    ).toEqual([]);
  });

  // The DAG tools have the key `dags` of `with`: `with.tools.dags` is a key like any other that is unknown.
  it('does not read dags in the tool map', () => {
    expect(
      tools(`${HEAD}      tools:\n        dags: [{ name: a }]\n`).entries
    ).toEqual([]);
  });

  // The list form `tools: [name]` is the released form; it equals `dags` entries with no params.
  it('reads the list form as dags entries with no params', () => {
    const list = tools(`${HEAD}      tools: [lookup, refund]\n`);
    const map = tools(
      `${HEAD}      dags: [{ name: lookup }, { name: refund }]\n`
    );
    expect(list.listForm).toBe(true);
    expect(map.listForm).toBeUndefined();
    expect(list.entries).toHaveLength(2);
    expect(list.entries[0]).toMatchObject({
      category: 'dags',
      kind: 'dag',
      index: 0,
      label: 'lookup',
      params: [],
      listForm: true,
    });
    expect(list.entries[1]).toMatchObject({ index: 1, label: 'refund' });
    // The same tools as the map form: only the path of an entry differs.
    const view = (model: typeof list) =>
      model.entries.map((entry) => [
        entry.category,
        entry.index,
        entry.kind,
        entry.label,
        entry.params,
      ]);
    expect(view(list)).toEqual(view(map));
    expect(entryPath(list.entries[1]!)).toBe('with.tools[1]');
    expect(entryPath(map.entries[1]!)).toBe('with.dags[1]');
  });

  it('reads a block list, and keeps the limits next to the list form', () => {
    const model = tools(`${HEAD}      max_tools: 3
      tools:
        - lookup
        - refund
`);
    expect(model.entries.map((entry) => entry.label)).toEqual([
      'lookup',
      'refund',
    ]);
    expect(model.maxTools).toBe(3);
    expect(model.listForm).toBe(true);
  });

  it('flags a list item that is not a name, and keeps its place', () => {
    const model = tools(`${HEAD}      tools: [lookup, 3, { name: refund }]\n`);
    expect(model.entries.map((entry) => entry.kind)).toEqual([
      'dag',
      'invalid',
      'invalid',
    ]);
    expect(model.entries[1]).toMatchObject({
      index: 1,
      problem: expect.any(String),
      listForm: true,
    });
  });

  // `with.dags` can hold an entry at the same place as the list form: the keys differ.
  it('gives an entry of the list form a key of its own', () => {
    const model = tools(
      `${HEAD}      tools: [lookup]\n      dags: [{ name: refund }]\n`
    );
    expect(model.entries.map(entryKey)).toEqual(['tools[0]', 'dags[0]']);
    expect(model.entries.map(entryPath)).toEqual([
      'with.tools[0]',
      'with.dags[0]',
    ]);
  });

  it('keeps an empty list as the list form with no entry', () => {
    expect(tools(`${HEAD}      tools: []\n`)).toEqual({
      entries: [],
      listForm: true,
    });
  });

  it('reads the limits only when they are numbers', () => {
    const model = tools(`${HEAD}      max_tools: 8
      tool_result_max_bytes: 0
      dags: [{ name: a }]
`);
    expect(model.maxTools).toBe(8);
    expect(model.resultMaxBytes).toBe(0);
    const bad = tools(`${HEAD}      max_tools: many\n`);
    expect(bad.maxTools).toBeUndefined();
  });
});

describe('readBuiltinTools', () => {
  it('reads the built-in tools that the step turns on, in the order of the spec', () => {
    expect(
      readBuiltinTools({
        with: {
          tools: {
            builtin: {
              shell: { allow: ['git diff *'], deny: ['git push *'] },
              search: true,
              read: true,
              edit: false,
              unknown: true,
            },
          },
        },
      })
    ).toEqual([
      { name: 'read' },
      { name: 'search' },
      { name: 'shell', allow: ['git diff *'], deny: ['git push *'] },
    ]);
  });

  it('gives no entry for a value of the wrong type', () => {
    expect(readBuiltinTools({})).toEqual([]);
    expect(readBuiltinTools({ with: { tools: [] } })).toEqual([]);
    expect(readBuiltinTools({ with: { tools: { builtin: true } } })).toEqual(
      []
    );
    expect(
      readBuiltinTools({ with: { tools: { builtin: { shell: true } } } })
    ).toEqual([]);
  });
});
