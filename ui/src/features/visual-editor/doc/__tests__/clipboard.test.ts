// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook, act } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it } from 'vitest';
import type { EditResult } from '../../contracts';
import {
  yamlArraySteps,
  yamlChain,
  yamlHandlers,
  yamlMapSteps,
  yamlNestedParallel,
  yamlShorthandSteps,
} from '../../testing/fixtures';
import { useEditSession } from '../../useEditSession';
import {
  copyStepsYaml,
  duplicateStepsOp,
  pasteStepsOp,
} from '../ops/clipboard';
import { parseDagDoc } from '../parse';
import { applyTextEdits } from '../patch';

type Done = Extract<EditResult, { ok: true }>;

/** Checks that the edits give the text in one hunk. */
function done(result: EditResult, input: string): Done {
  if (!result.ok) throw new Error(`${result.code}: ${result.message}`);
  expect(applyTextEdits(input, result.edits)).toBe(result.text);
  expect(result.edits).toHaveLength(1);
  return result;
}

/** A step with an id that another step reads, a comment and a dangling depends. */
const REFS = `name: refs
steps:
  - name: fetch
    id: fetch
    run: curl x
    output: BODY

  # Parse the body.
  - name: parse   # inline
    depends: [fetch, gone]
    run: echo \${steps.fetch.outputs.body} \${fetch.stdout}
`;

// parity:test-ve-doc-model parity:minimal-text-edits
describe('copyStepsYaml', () => {
  it('gives the selected entries as a YAML list, in file order', () => {
    const doc = parseDagDoc(yamlArraySteps);
    expect(copyStepsYaml(doc, ['steps[2]', 'steps[0]'])).toBe(
      `- name: fetch
  run: curl -sSf https://example.com/data.csv -o data.csv
# Last step.
- name: publish
  depends:
    - transform
  run: ./publish.sh sorted.csv
`
    );
    expect(copyStepsYaml(doc, ['steps[1]'])).toBe(
      `- name: transform # keep the header row
  depends: [fetch]
  run: |
    head -n 1 data.csv
    sort data.csv > sorted.csv
`
    );
  });

  it('names a map entry by its key and writes a shorthand as run', () => {
    expect(
      copyStepsYaml(parseDagDoc(yamlMapSteps), ['steps[0]', 'steps[1]'])
    ).toBe(
      `- name: fetch
  run: curl -sSf https://example.com/data.csv -o data.csv
- name: parse
  depends: [fetch]
  run: wc -l data.csv
`
    );
    expect(copyStepsYaml(parseDagDoc(yamlShorthandSteps), ['steps[0]'])).toBe(
      '- run: echo one\n'
    );
  });

  it('keeps the file order of a map whose keys are not sorted', () => {
    const text = 'steps:\n  zeta:\n    run: z\n  alpha:\n    run: a\n';
    // The server sorts map steps by name: alpha is steps[0].
    expect(copyStepsYaml(parseDagDoc(text), ['steps[0]', 'steps[1]'])).toBe(
      '- name: zeta\n  run: z\n- name: alpha\n  run: a\n'
    );
  });

  it('resolves aliases and drops anchors', () => {
    const text = `name: a
steps:
  - name: one
    env: &common
      - A=1
  - name: two
    env: *common
`;
    expect(copyStepsYaml(parseDagDoc(text), ['steps[1]'])).toBe(
      '- name: two\n  env:\n    - A=1\n'
    );
    expect(copyStepsYaml(parseDagDoc(text), ['steps[0]'])).toBe(
      '- name: one\n  env:\n    - A=1\n'
    );
  });

  // The server reads integers exactly, beyond the float precision.
  it('keeps an integer beyond the float precision', () => {
    const text =
      'steps:\n  - name: a\n    env:\n      CHANNEL: 9007199254740993\n';
    expect(copyStepsYaml(parseDagDoc(text), ['steps[0]'])).toBe(
      '- name: a\n  env:\n    CHANNEL: 9007199254740993\n'
    );
  });

  it('gives nothing without a step or with broken YAML', () => {
    expect(copyStepsYaml(parseDagDoc(yamlArraySteps), [])).toBe('');
    expect(copyStepsYaml(parseDagDoc(yamlArraySteps), ['steps[9]'])).toBe('');
    expect(copyStepsYaml(parseDagDoc('steps: [a'), ['steps[0]'])).toBe('');
  });
});

describe('pasteStepsOp', () => {
  it('inserts after the anchor as one hunk with unique names', () => {
    const doc = parseDagDoc(yamlArraySteps);
    const clip = copyStepsYaml(doc, ['steps[0]']);
    const result = done(pasteStepsOp(doc, clip, 'steps[1]'), yamlArraySteps);
    expect(result.text).toBe(`# Nightly import
name: nightly-import
steps:
  # Download the source file.
  - name: fetch
    run: curl -sSf https://example.com/data.csv -o data.csv

  - name: transform   # keep the header row
    depends: [fetch]
    run: |
      head -n 1 data.csv
      sort data.csv > sorted.csv

  - name: fetch_2
    run: curl -sSf https://example.com/data.csv -o data.csv

  # Last step.
  - name: publish
    depends:
      - transform
    run: ./publish.sh sorted.csv
`);
    expect(result.select).toBe('steps[2]');
  });

  it('appends at the end without an anchor', () => {
    const doc = parseDagDoc(yamlChain);
    const result = done(
      pasteStepsOp(doc, '- name: build\n  run: make again\n', null),
      yamlChain
    );
    expect(result.text).toBe(`${yamlChain}  - name: build_2
    run: make again
`);
    expect(result.select).toBe('steps[3]');
  });

  it('drops dangling depends and rewrites the references of renamed steps', () => {
    const doc = parseDagDoc(REFS);
    const clip = copyStepsYaml(doc, ['steps[0]', 'steps[1]']);
    const result = done(pasteStepsOp(doc, clip, 'steps[1]'), REFS);
    expect(result.text).toBe(`${REFS}
  - name: fetch_2
    id: fetch_2
    run: curl x
    output: BODY

  # Parse the body.
  - name: parse_2 # inline
    depends: [fetch_2]
    run: echo \${steps.fetch_2.outputs.body} \${fetch_2.stdout}
`);
  });

  it('keeps depends on a step of the target', () => {
    const doc = parseDagDoc(REFS);
    const result = done(
      pasteStepsOp(
        doc,
        '- name: late\n  depends: fetch\n  run: echo ${steps.fetch.outputs.body}\n',
        null
      ),
      REFS
    );
    expect(result.text).toBe(`${REFS}
  - name: late
    depends: fetch
    run: echo \${steps.fetch.outputs.body}
`);
  });

  it('keeps an integer beyond the float precision', () => {
    const doc = parseDagDoc(yamlChain);
    const result = done(
      pasteStepsOp(
        doc,
        '- name: big\n  env:\n    ID: 9007199254740993\n',
        null
      ),
      yamlChain
    );
    expect(result.text).toContain('    env:\n      ID: 9007199254740993\n');
  });

  // The pasted integers read as bigints and those of the file as numbers:
  // the check of the result reads both as the same values.
  it('inserts a step with an integer without rewriting the file', () => {
    const result = done(
      pasteStepsOp(
        parseDagDoc(yamlArraySteps),
        '- name: small\n  retry_policy:\n    limit: 2\n',
        null
      ),
      yamlArraySteps
    );
    expect(result.edits).toEqual([
      {
        from: yamlArraySteps.length,
        to: yamlArraySteps.length,
        insert: '\n  - name: small\n    retry_policy:\n      limit: 2\n',
      },
    ]);
  });

  // Like the server, a step named by a number is named by its text.
  it('renames a step named by a number and the depends on it', () => {
    const text = 'steps:\n  - name: 7\n    run: x\n';
    const result = done(
      pasteStepsOp(
        parseDagDoc(text),
        '- name: 7\n  run: y\n- name: b\n  depends: [7]\n  run: z\n',
        null
      ),
      text
    );
    expect(result.text).toBe(
      `${text}  - name: 7_2\n    run: y\n  - name: b\n    depends: [7_2]\n    run: z\n`
    );
  });

  it('pastes into map form, keyed by the name', () => {
    const doc = parseDagDoc(yamlMapSteps);
    const result = done(
      pasteStepsOp(
        doc,
        '# Again.\n- name: parse\n  depends: [fetch]\n  run: wc -c data.csv\n',
        'steps[0]'
      ),
      yamlMapSteps
    );
    expect(result.text).toBe(`name: map-form
steps:
  # Entry point.
  fetch:
    run: curl -sSf https://example.com/data.csv -o data.csv

  # Again.
  parse_2:
    depends: [fetch]
    run: wc -c data.csv

  parse:
    depends: [fetch]
    run: wc -l data.csv
`);
    // Map form: the server orders the steps by name.
    expect(result.select).toBe('steps[2]');
  });

  it('takes the steps of a whole DAG and creates the steps list', () => {
    const input = 'name: empty\n';
    const result = done(
      pasteStepsOp(
        parseDagDoc(input),
        'name: other\nsteps:\n  - name: a\n    run: echo a\n',
        null
      ),
      input
    );
    expect(result.text).toBe(
      'name: empty\nsteps:\n  - name: a\n    run: echo a\n'
    );
    const flow = 'name: x\nsteps: []\n';
    expect(
      done(pasteStepsOp(parseDagDoc(flow), 'run: echo b\n', null), flow).text
    ).toBe('name: x\nsteps:\n  - name: step\n    run: echo b\n');
  });

  it('takes an exec step and a typed step with a config', () => {
    const flow = 'name: x\nsteps: []\n';
    expect(
      done(
        pasteStepsOp(parseDagDoc(flow), 'exec:\n  command: ls\n', null),
        flow
      ).text
    ).toBe('name: x\nsteps:\n  - name: step\n    exec:\n      command: ls\n');
    expect(
      pasteStepsOp(
        parseDagDoc(flow),
        '- type: http\n  config:\n    url: https://example.com\n',
        null
      ).ok
    ).toBe(true);
  });

  it('drops depends in a chain DAG', () => {
    const result = done(
      pasteStepsOp(
        parseDagDoc(yamlChain),
        '- name: smoke\n  depends: [test]\n  run: make smoke\n',
        'steps[1]'
      ),
      yamlChain
    );
    expect(result.text).toBe(`# Chain DAG
type: chain
name: release
steps:
  - name: build
    run: make build
  - name: test
    run: make test
  - name: smoke
    run: make smoke
  - name: deploy
    run: make deploy
`);
  });

  it('goes after a parallel group that holds the anchor', () => {
    const result = done(
      pasteStepsOp(
        parseDagDoc(yamlNestedParallel),
        '- name: lint\n  run: make lint\n',
        'steps[1]'
      ),
      yamlNestedParallel
    );
    expect(result.text).toContain(
      '    - name: unit\n      run: make test\n  - name: lint_2\n    run: make lint\n  - name: report\n'
    );
  });

  it.each([
    ['plain text', 'hello world'],
    ['a broken list', '- [a'],
    ['a mapping that is no step', 'foo: bar'],
    ['a list of strings', '- a\n- b\n'],
    ['an empty text', ''],
    ['two documents', '- name: a\n---\n- name: b\n'],
    ['a nested group', '- - name: a\n'],
  ])('refuses %s and changes nothing', (_label, clip) => {
    expect(pasteStepsOp(parseDagDoc(REFS), clip, null)).toEqual({
      ok: false,
      code: 'invalidYaml',
      message: expect.any(String),
    });
  });

  it('refuses an unknown anchor and broken YAML', () => {
    expect(
      pasteStepsOp(parseDagDoc(REFS), '- name: a\n', 'steps[9]')
    ).toMatchObject({ ok: false, code: 'unknownStep' });
    expect(
      pasteStepsOp(parseDagDoc('steps: [a'), '- name: a\n', null)
    ).toMatchObject({ ok: false, code: 'parseError' });
  });
});

describe('duplicateStepsOp', () => {
  it('copies and pastes after the last selected step', () => {
    const doc = parseDagDoc(yamlArraySteps);
    const result = done(
      duplicateStepsOp(doc, ['steps[1]', 'steps[0]']),
      yamlArraySteps
    );
    expect(result.text).toBe(`# Nightly import
name: nightly-import
steps:
  # Download the source file.
  - name: fetch
    run: curl -sSf https://example.com/data.csv -o data.csv

  - name: transform   # keep the header row
    depends: [fetch]
    run: |
      head -n 1 data.csv
      sort data.csv > sorted.csv

  - name: fetch_2
    run: curl -sSf https://example.com/data.csv -o data.csv

  - name: transform_2 # keep the header row
    depends: [fetch_2]
    run: |
      head -n 1 data.csv
      sort data.csv > sorted.csv

  # Last step.
  - name: publish
    depends:
      - transform
    run: ./publish.sh sorted.csv
`);
    expect(result.select).toBe('steps[2]');
  });

  it('keeps the upstream step of a single copy', () => {
    const result = done(
      duplicateStepsOp(parseDagDoc(REFS), ['steps[1]']),
      REFS
    );
    expect(result.text).toBe(`${REFS}
  # Parse the body.
  - name: parse_2 # inline
    depends: [fetch]
    run: echo \${steps.fetch.outputs.body} \${fetch.stdout}
`);
  });

  // The server trims names and ids: "task " and "task" are one step.
  it('renames a copy whose name or id differs by outer spaces only', () => {
    const text = 'steps:\n  - name: "task "\n    id: " job"\n    run: x\n';
    const result = done(
      duplicateStepsOp(parseDagDoc(text), ['steps[0]']),
      text
    );
    expect(result.text).toBe(
      `${text}  - name: "task_2"\n    id: "job_2"\n    run: x\n`
    );
  });

  it('refuses a handler', () => {
    // A copy of a handler would run as a step on every run.
    expect(
      duplicateStepsOp(parseDagDoc(yamlHandlers), ['handler_on.failure'])
    ).toMatchObject({ ok: false, code: 'noop' });
  });

  it('refuses no step, an unknown step and broken YAML', () => {
    const doc = parseDagDoc(yamlArraySteps);
    expect(duplicateStepsOp(doc, [])).toMatchObject({
      ok: false,
      code: 'noop',
    });
    expect(duplicateStepsOp(doc, ['steps[7]'])).toMatchObject({
      ok: false,
      code: 'unknownStep',
    });
    expect(
      duplicateStepsOp(parseDagDoc('steps: [a'), ['steps[0]'])
    ).toMatchObject({ ok: false, code: 'parseError' });
  });
});

// The editor applies a clipboard result as a whole new text; the edit
// session records it as one undo step (VisualEditor applyResult).
describe('clipboard edits in the edit session', () => {
  function useHarness(initial: string) {
    const [text, setText] = useState(initial);
    const session = useEditSession({
      text,
      setText,
      names: { byKey: {}, byName: {} },
    });
    return { text, session };
  }

  it('reverts a duplicate and then a paste with one undo each', () => {
    const { result } = renderHook(() => useHarness(yamlArraySteps));
    const duplicated = duplicateStepsOp(parseDagDoc(yamlArraySteps), [
      'steps[0]',
    ]);
    if (!duplicated.ok) throw new Error(duplicated.message);
    act(() => {
      result.current.session.applyWith(() => duplicated);
    });
    act(() => result.current.session.undo());
    expect(result.current.text).toBe(yamlArraySteps);
    act(() => result.current.session.redo());
    expect(result.current.text).toBe(duplicated.text);

    const pasted = pasteStepsOp(
      parseDagDoc(duplicated.text),
      '- name: extra\n  run: echo\n',
      null
    );
    if (!pasted.ok) throw new Error(pasted.message);
    act(() => {
      result.current.session.applyWith(() => pasted);
    });
    act(() => result.current.session.undo());
    expect(result.current.text).toBe(duplicated.text);
    act(() => result.current.session.undo());
    expect(result.current.text).toBe(yamlArraySteps);
  });
});
