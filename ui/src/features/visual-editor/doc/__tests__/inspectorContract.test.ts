// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// The step inspector relies on these three behaviors of applyEdit.

import { describe, expect, it } from 'vitest';
import type { DagEditOp, EditResult } from '../../contracts';
import { yamlRouter } from '../../testing/fixtures';
import { applyEdit, parseDagDoc } from '../index';
import { applyTextEdits } from '../patch';

/** Applies the op, checks that the edits give the text, and returns the text. */
function run(input: string, op: DagEditOp): string {
  const result: EditResult = applyEdit(input, op);
  if (!result.ok) throw new Error(`${result.code}: ${result.message}`);
  expect(applyTextEdits(input, result.edits)).toBe(result.text);
  return result.text;
}

describe('replaceStepYaml with dedented text', () => {
  // The text sits at column 0; the op moves it to the column of the entry.
  it('re-indents a step that is not the first', () => {
    const input = `name: t
steps:
  - name: a
    run: echo a
  # About b.
  - name: b
    run: echo old

  - name: c
    run: echo c
`;
    const out = run(input, {
      kind: 'replaceStepYaml',
      step: 'steps[1]',
      yaml: 'name: b\nrun: echo hi\n',
    });
    expect(out).toBe(
      input.replace('    run: echo old\n', '    run: echo hi\n')
    );
    const model = parseDagDoc(out);
    expect(model.ok).toBe(true);
    expect(model.steps.map((s) => s.explicitName)).toEqual(['a', 'b', 'c']);
    expect(model.steps[1]?.value).toEqual({ name: 'b', run: 'echo hi' });
  });

  it('re-indents a nested map and a block scalar', () => {
    const input = `name: t
steps:
  - name: a
    run: echo a
  - name: b
    run: echo old
  - name: c
    run: echo c
`;
    const out = run(input, {
      kind: 'replaceStepYaml',
      step: 'steps[1]',
      yaml: 'name: b\nwith:\n  url: http://x\n  method: GET\nrun: |\n  line one\n  line two\n',
    });
    expect(out).toBe(`name: t
steps:
  - name: a
    run: echo a
  - name: b
    with:
      url: http://x
      method: GET
    run: |
      line one
      line two
  - name: c
    run: echo c
`);
    const model = parseDagDoc(out);
    expect(model.ok).toBe(true);
    expect(model.steps.map((s) => s.explicitName)).toEqual(['a', 'b', 'c']);
    expect(model.steps[1]?.value).toEqual({
      name: 'b',
      with: { url: 'http://x', method: 'GET' },
      run: 'line one\nline two\n',
    });
  });
});

describe('setRouteTargets with no target', () => {
  it('removes the pattern and keeps the rest', () => {
    const out = run(yamlRouter, {
      kind: 'setRouteTargets',
      step: 'steps[0]',
      pattern: 're:^5\\d\\d$',
      targets: [],
    });
    expect(out).toBe(
      yamlRouter.replace('        "re:^5\\\\d\\\\d$": [server_error]\n', '')
    );
    expect(out).toContain('"re:.*": [other]');
    expect(out).toContain('  # Fallback branch.\n');
  });
});

describe('setStepField with a list index and no value', () => {
  const remove = (input: string, step: string, path: (string | number)[]) =>
    run(input, { kind: 'setStepField', step, path, value: undefined });

  it('removes an item of a block depends list', () => {
    const input = `steps:
  - name: a
    run: x
  - name: b
    run: y
  # Join.
  - name: c
    depends:
      - a
      - b
    run: z
`;
    expect(remove(input, 'steps[2]', ['depends', 1])).toBe(
      input.replace('      - b\n', '')
    );
  });

  it('removes an item of a flow depends list', () => {
    const input = `steps:
  - name: a
    run: x
  - name: b
    run: y
  - name: c
    depends: [a, b]   # both
    run: z
`;
    const out = remove(input, 'steps[2]', ['depends', 1]);
    expect(out).toBe(input.replace('[a, b]', '[a]'));
    expect(parseDagDoc(out).steps[2]?.depends).toEqual(['a']);
  });

  it('removes an item of an env list', () => {
    const input = `steps:
  - name: a
    env:
      - A=1
      - B=2
    run: x
  - name: b
    run: y
`;
    expect(remove(input, 'steps[0]', ['env', 0])).toBe(
      input.replace('      - A=1\n', '')
    );
  });
});
