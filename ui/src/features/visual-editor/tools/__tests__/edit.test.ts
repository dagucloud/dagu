// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { EditResult } from '../../contracts';
import { applyEdit, parseDagDoc } from '../../doc';
import { applyTextEdits } from '../../doc/patch';
import { editStepTools, type ToolsChange } from '../edit';
import { readStepTools } from '../parse';

function run(text: string, change: ToolsChange): string {
  const result: EditResult = editStepTools(text, 'steps[0]', change);
  if (!result.ok) throw new Error(`${result.code}: ${result.message}`);
  expect(applyTextEdits(text, result.edits)).toBe(result.text);
  return result.text;
}

function fail(text: string, change: ToolsChange): string {
  const result = editStepTools(text, 'steps[0]', change);
  if (result.ok) throw new Error('The edit did not fail.');
  return result.code;
}

const BASE = `# Support DAG
steps:
  - id: triage
    action: chat.completion
    with:
      prompt: Handle it
      dags:
        # first tool
        - name: lookup
        - name: refund # money
          params:
            amount: 5
      tools:
        mcp:
          - name: docs
            url: http://x/mcp
            allow: [search]
  - id: other
    run: echo done
`;

const BARE = 'steps:\n  - id: a\n    action: chat.completion\n';

function entries(text: string) {
  return readStepTools(parseDagDoc(text).steps[0]!.value).entries;
}

function labels(text: string) {
  return entries(text).map((entry) => `${entry.category}:${entry.label}`);
}

describe('editStepTools add', () => {
  it('appends an entry to its category and keeps the comments', () => {
    const out = run(BASE, {
      kind: 'add',
      category: 'dags',
      entry: { name: 'extra' },
    });
    expect(out).toBe(
      BASE.replace(
        '            amount: 5\n',
        '            amount: 5\n        - name: extra\n'
      )
    );
  });

  it('inserts at a position', () => {
    const out = run(BASE, {
      kind: 'add',
      category: 'dags',
      entry: { name: 'extra' },
      at: 0,
    });
    expect(labels(out)).toEqual([
      'dags:extra',
      'dags:lookup',
      'dags:refund',
      'mcp:docs',
    ]);
    expect(out).toContain('# first tool');
  });

  it('creates the key dags when the step has none', () => {
    const text = `${BARE}    with:
      tools:
        mcp:
          - name: docs
            url: http://x/mcp
            allow: [search]
`;
    const out = run(text, {
      kind: 'add',
      category: 'dags',
      entry: { name: 'lookup' },
    });
    expect(labels(out)).toEqual(['dags:lookup', 'mcp:docs']);
    // The DAG tools are not in the tool map.
    expect(out).toContain('      dags:\n        - name: lookup\n');
  });

  it('creates the tool map with the MCP list when the step has none', () => {
    const out = run(BARE, {
      kind: 'add',
      category: 'mcp',
      entry: { name: 'docs', url: 'http://x/mcp', allow: ['search'] },
    });
    expect(out).toBe(
      `${BARE}    with:\n      tools:\n        mcp:\n          - name: docs\n            url: http://x/mcp\n            allow:\n              - search\n`
    );
  });

  it('creates with and dags when the step has none', () => {
    const out = run(BARE, {
      kind: 'add',
      category: 'dags',
      entry: { name: 'lookup' },
    });
    expect(out).toBe(`${BARE}    with:\n      dags:\n        - name: lookup\n`);
  });

  it('adds under an existing with that has no dags', () => {
    const text = `${BARE}    with:\n      prompt: hi\n`;
    const out = run(text, {
      kind: 'add',
      category: 'dags',
      entry: { name: 'x' },
    });
    expect(entries(out)).toHaveLength(1);
    expect(out).toContain('prompt: hi');
  });
});

describe('editStepTools remove', () => {
  it('removes an entry and leaves the others alone', () => {
    const out = run(BASE, { kind: 'remove', category: 'dags', index: 0 });
    expect(labels(out)).toEqual(['dags:refund', 'mcp:docs']);
    expect(out).toContain('- name: refund # money');
    expect(out).toContain('# Support DAG');
  });

  it('removes a category with its last entry', () => {
    const out = run(BASE, { kind: 'remove', category: 'mcp', index: 0 });
    expect(out).not.toContain('mcp');
    expect(labels(out)).toEqual(['dags:lookup', 'dags:refund']);
  });

  it('removes the dags key with its last entry', () => {
    const text = `${BARE}    with:\n      prompt: hi\n      dags:\n        - name: one\n`;
    const out = run(text, { kind: 'remove', category: 'dags', index: 0 });
    expect(out).toBe(`${BARE}    with:\n      prompt: hi\n`);
  });

  it('refuses an index outside the list, or a category that is absent', () => {
    expect(fail(BASE, { kind: 'remove', category: 'dags', index: 5 })).toBe(
      'invalidYaml'
    );
    const dagsOnly = `${BARE}    with:\n      dags:\n        - name: one\n`;
    expect(fail(dagsOnly, { kind: 'remove', category: 'mcp', index: 0 })).toBe(
      'invalidYaml'
    );
  });
});

describe('editStepTools move', () => {
  it('swaps the entries of a category and keeps their comments', () => {
    const out = run(BASE, {
      kind: 'move',
      category: 'dags',
      from: 1,
      to: 0,
    });
    expect(labels(out)).toEqual(['dags:refund', 'dags:lookup', 'mcp:docs']);
    expect(out).toContain('- name: refund # money');
  });

  it('refuses a target outside the list', () => {
    expect(fail(BASE, { kind: 'move', category: 'dags', from: 0, to: 2 })).toBe(
      'invalidYaml'
    );
  });
});

describe('editStepTools replace', () => {
  it('swaps one entry for the mapping of a text and leaves the others alone', () => {
    const out = run(BASE, {
      kind: 'replace',
      category: 'dags',
      index: 1,
      yaml: 'name: refund-v2\nparams:\n  amount: 9',
    });
    expect(labels(out)).toEqual(['dags:lookup', 'dags:refund-v2', 'mcp:docs']);
    expect(entries(out)[1]!.params[0]).toMatchObject({
      name: 'amount',
      value: 9,
    });
    expect(out).toContain('# first tool');
    expect(out).toContain('- id: other');
  });

  it('keeps the comment above the entry', () => {
    const out = run(BASE, {
      kind: 'replace',
      category: 'dags',
      index: 0,
      yaml: 'name: lookup-v2',
    });
    expect(out).toContain('# first tool\n        - name: lookup-v2');
  });

  it('refuses a text with a syntax error, a text that is not a mapping and an absent entry', () => {
    expect(
      fail(BASE, {
        kind: 'replace',
        category: 'dags',
        index: 0,
        yaml: 'name: [',
      })
    ).toBe('invalidYaml');
    expect(
      fail(BASE, {
        kind: 'replace',
        category: 'dags',
        index: 0,
        yaml: '- a\n- b',
      })
    ).toBe('invalidYaml');
    expect(
      fail(BASE, {
        kind: 'replace',
        category: 'dags',
        index: 9,
        yaml: 'name: x',
      })
    ).toBe('invalidYaml');
  });
});

describe('editStepTools update', () => {
  it('sets a key of an entry', () => {
    const out = run(BASE, {
      kind: 'update',
      category: 'dags',
      index: 1,
      path: ['name'],
      value: 'pay_back',
    });
    expect(labels(out)).toContain('dags:pay_back');
    expect(out).toContain('# money');
  });

  it('sets a from_ai param', () => {
    const out = run(BASE, {
      kind: 'update',
      category: 'dags',
      index: 1,
      path: ['params', 'amount'],
      value: { from_ai: { description: 'Euros' } },
    });
    expect(entries(out)[1]?.params).toEqual([
      { name: 'amount', state: 'from_ai', description: 'Euros' },
    ]);
  });

  it('removes a key and the maps that it leaves empty', () => {
    const out = run(BASE, {
      kind: 'update',
      category: 'dags',
      index: 1,
      path: ['params', 'amount'],
      value: undefined,
    });
    expect(out).not.toContain('params');
    expect(entries(out)[1]?.label).toBe('refund');
  });

  it('edits an entry of another category', () => {
    const out = run(BASE, {
      kind: 'update',
      category: 'mcp',
      index: 0,
      path: ['url'],
      value: 'http://y/mcp',
    });
    expect(entries(out)[2]?.mcp?.url).toBe('http://y/mcp');
  });

  it('reports a field that is absent', () => {
    expect(
      fail(BASE, {
        kind: 'update',
        category: 'dags',
        index: 1,
        path: ['params', 'missing'],
        value: undefined,
      })
    ).toBe('noop');
  });

  it('refuses an entry that is not a mapping', () => {
    const text = `${BARE}    with:\n      dags:\n        - lookup\n`;
    expect(
      fail(text, {
        kind: 'update',
        category: 'dags',
        index: 0,
        path: ['name'],
        value: 'x',
      })
    ).toBe('invalidYaml');
  });
});

describe('editStepTools setLimit', () => {
  it('sets and removes a limit', () => {
    const set = run(BASE, { kind: 'setLimit', key: 'max_tools', value: 8 });
    expect(readStepTools(parseDagDoc(set).steps[0]!.value).maxTools).toBe(8);
    const cleared = run(set, {
      kind: 'setLimit',
      key: 'max_tools',
      value: undefined,
    });
    expect(cleared).toBe(BASE);
  });
});

describe('editStepTools targets', () => {
  it('does not touch the other steps', () => {
    const out = run(BASE, { kind: 'remove', category: 'mcp', index: 0 });
    expect(out).toContain('- id: other\n    run: echo done\n');
  });

  it('fails for a step that does not exist', () => {
    const result = editStepTools(BASE, 'steps[9]', {
      kind: 'remove',
      category: 'dags',
      index: 0,
    });
    expect(result.ok).toBe(false);
  });

  it('fails when a category is not a list', () => {
    const text = `${BARE}    with:\n      dags: lookup\n`;
    expect(
      fail(text, { kind: 'add', category: 'dags', entry: { name: 'x' } })
    ).toBe('invalidYaml');
    const mcp = `${BARE}    with:\n      tools:\n        mcp: docs\n`;
    expect(
      fail(mcp, { kind: 'add', category: 'mcp', entry: { name: 'x' } })
    ).toBe('invalidYaml');
  });

  // A tool map that holds a scalar has no list of MCP servers to add to.
  it('fails to add an MCP server when the tools are not a map', () => {
    const text = `${BARE}    with:\n      tools: docs\n`;
    expect(
      fail(text, { kind: 'add', category: 'mcp', entry: { name: 'x' } })
    ).toBe('invalidYaml');
  });
});

describe('editStepTools lists', () => {
  it('removes a map that the last removal leaves empty', () => {
    const text = `${BARE}    with:
      dags:
        - name: s
          params:
            amount: 1
`;
    const out = run(text, {
      kind: 'update',
      category: 'dags',
      index: 0,
      path: ['params', 'amount'],
      value: undefined,
    });
    expect(out).toBe(`${BARE}    with:\n      dags:\n        - name: s\n`);
  });

  it('adds a key under a map that does not exist yet', () => {
    const text = `${BARE}    with:\n      dags:\n        - name: s\n`;
    const out = run(text, {
      kind: 'update',
      category: 'dags',
      index: 0,
      path: ['params', 'amount'],
      value: 'x',
    });
    expect(entries(out)[0]?.params).toEqual([
      { name: 'amount', state: 'fixed', value: 'x' },
    ]);
  });
});

describe('editStepTools as an edit operation', () => {
  it('goes through applyEdit', () => {
    const result = applyEdit(BASE, {
      kind: 'editStepTools',
      step: 'steps[0]',
      change: { kind: 'remove', category: 'dags', index: 0 },
    });
    if (!result.ok) throw new Error(result.message);
    expect(labels(result.text)).toEqual(['dags:refund', 'mcp:docs']);
  });
});

// The list form `tools: [name]` is the released form: the first edit of an
// entry rewrites it to the key `dags`. A read never changes the text.
describe('editStepTools on the list form', () => {
  const LIST = `${BARE}    with:
      prompt: hi
      tools:
        # first tool
        - lookup
        - "refund" # money
        - audit
`;

  it('reads without a change: no edit is made until an entry changes', () => {
    expect(labels(LIST)).toEqual(['dags:lookup', 'dags:refund', 'dags:audit']);
    expect(parseDagDoc(LIST).text).toBe(LIST);
  });

  it('rewrites the list to dags on an add, and appends the entry', () => {
    const out = run(LIST, {
      kind: 'add',
      category: 'dags',
      entry: { name: 'ops.notify' },
    });
    expect(out).toBe(`${BARE}    with:
      prompt: hi
      dags:
        # first tool
        - name: lookup
        - name: "refund" # money
        - name: audit
        - name: ops.notify
`);
    expect(
      readStepTools(parseDagDoc(out).steps[0]!.value).listForm
    ).toBeUndefined();
  });

  it('rewrites the list on a remove, and keeps the other names in order', () => {
    const out = run(LIST, { kind: 'remove', category: 'dags', index: 1 });
    expect(labels(out)).toEqual(['dags:lookup', 'dags:audit']);
    expect(out).toContain('      dags:');
    expect(out).not.toContain('tools');
    expect(out).toContain('- name: lookup');
    expect(out).not.toMatch(/^\s+- lookup$/m);
  });

  it('removes the key with the last name', () => {
    const text = `${BARE}    with:\n      prompt: hi\n      tools: [lookup]\n`;
    const out = run(text, { kind: 'remove', category: 'dags', index: 0 });
    expect(out).toBe(`${BARE}    with:\n      prompt: hi\n`);
  });

  it('rewrites the list on a move', () => {
    const out = run(LIST, { kind: 'move', category: 'dags', from: 2, to: 0 });
    expect(labels(out)).toEqual(['dags:audit', 'dags:lookup', 'dags:refund']);
    expect(out).toContain('- name: audit');
  });

  it('rewrites the list on an update of one entry', () => {
    const out = run(LIST, {
      kind: 'update',
      category: 'dags',
      index: 0,
      path: ['params', 'id'],
      value: { from_ai: true },
    });
    expect(entries(out)[0]).toMatchObject({
      label: 'lookup',
      params: [{ name: 'id', state: 'from_ai' }],
    });
    expect(labels(out)).toEqual(['dags:lookup', 'dags:refund', 'dags:audit']);
  });

  it('rewrites the flow list and keeps the other keys of with', () => {
    const text = `${BARE}    with:\n      prompt: hi\n      tools: [lookup, refund]\n      max_tools: 4\n`;
    const out = run(text, {
      kind: 'update',
      category: 'dags',
      index: 1,
      path: ['name'],
      value: 'pay_back',
    });
    expect(out).toBe(
      `${BARE}    with:\n      prompt: hi\n      dags:\n        - name: lookup\n        - name: pay_back\n      max_tools: 4\n`
    );
  });

  it('keeps an item that is not a name, in dags, for the validation to report', () => {
    const text = `${BARE}    with:\n      tools: [lookup, 3]\n`;
    const out = run(text, {
      kind: 'add',
      category: 'dags',
      entry: { name: 'x' },
    });
    expect(entries(out).map((entry) => entry.kind)).toEqual([
      'dag',
      'invalid',
      'dag',
    ]);
  });

  it('puts the names of the list in front of the entries of dags that exist', () => {
    const text = `${BARE}    with:\n      tools: [lookup]\n      dags:\n        - name: refund\n`;
    const out = run(text, {
      kind: 'update',
      category: 'dags',
      index: 1,
      path: ['name'],
      value: 'pay_back',
    });
    expect(out).toBe(
      `${BARE}    with:\n      dags:\n        - name: lookup\n        - name: pay_back\n`
    );
  });

  it('leaves the MCP servers of a step with the list form in the tool map', () => {
    const text = `${BARE}    with:\n      tools: [lookup]\n`;
    const out = run(text, {
      kind: 'add',
      category: 'mcp',
      entry: { name: 'docs', allow: ['search'] },
    });
    expect(labels(out)).toEqual(['dags:lookup', 'mcp:docs']);
    expect(out).toBe(
      `${BARE}    with:\n      dags:\n        - name: lookup\n      tools:\n        mcp:\n          - name: docs\n            allow:\n              - search\n`
    );
  });

  it('does not rewrite the list for a limit', () => {
    const text = `${BARE}    with:\n      tools: [lookup]\n`;
    const out = run(text, { kind: 'setLimit', key: 'max_tools', value: 5 });
    expect(out).toBe(`${text}      max_tools: 5\n`);
  });

  it('leaves the text as it is when the edit fails', () => {
    expect(fail(LIST, { kind: 'remove', category: 'dags', index: 9 })).toBe(
      'invalidYaml'
    );
  });
});
