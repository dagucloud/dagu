// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { builtinToolsYaml, toolEntryYaml } from '../entryYaml';

const STEP = `id: triage
action: chat.completion
with:
  dags:
    # first tool
    - name: lookup
    - name: refund # money
      params:
        amount: 5
  tools:
    mcp:
      - {name: docs, url: "https://x"}
`;

describe('toolEntryYaml', () => {
  it('gives the text of an entry at column 0, with its comments', () => {
    expect(toolEntryYaml(STEP, 'dags', 1)).toBe(
      'name: refund # money\nparams:\n  amount: 5'
    );
    expect(toolEntryYaml(STEP, 'dags', 0)).toBe('name: lookup');
  });

  it('keeps the flow style of an entry', () => {
    expect(toolEntryYaml(STEP, 'mcp', 0)).toBe(
      '{name: docs, url: "https://x"}'
    );
  });

  it('gives an empty text for an entry that is not there', () => {
    expect(toolEntryYaml(STEP, 'dags', 5)).toBe('');
    expect(toolEntryYaml(STEP, 'mcp', 5)).toBe('');
    expect(toolEntryYaml('id: a\n', 'dags', 0)).toBe('');
  });

  it('shows a name of the list form as the mapping that the first edit writes', () => {
    const list =
      'id: a\naction: chat.completion\nwith:\n  tools:\n    - lookup\n    - refund\n';
    expect(toolEntryYaml(list, 'dags', 1, true)).toBe('name: refund');
  });
});

describe('builtinToolsYaml', () => {
  it('gives the built-in map of an agent step at column 0', () => {
    const step = `id: code
action: agent.run
with:
  tools:
    builtin:
      read: true
      shell:
        allow: [make test]
`;
    expect(builtinToolsYaml(step)).toBe(
      'read: true\nshell:\n  allow: [make test]'
    );
    expect(builtinToolsYaml('id: a\n')).toBe('');
  });
});
