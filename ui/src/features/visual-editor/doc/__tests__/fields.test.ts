// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { DagEditOp, EditResult, StepNameMap } from '../../contracts';
import {
  yamlArraySteps,
  yamlHttpJq,
  yamlHandlers,
  yamlMapSteps,
  namesArraySteps,
  yamlMultiDoc,
  yamlNestedParallel,
  yamlRouter,
  yamlShorthandSteps,
} from '../../testing/fixtures';
import {
  opRenameStep,
  opReplaceStepYaml,
  opSetStepField,
  opSetStepId,
} from '../ops/fields';
import { applyTextEdits } from '../patch';

type SetField = Extract<DagEditOp, { kind: 'setStepField' }>;

/** Returns the new text of a successful result and checks that the edits give it. */
function textOf(input: string, result: EditResult): string {
  if (!result.ok) throw new Error(`${result.code}: ${result.message}`);
  expect(applyTextEdits(input, result.edits)).toBe(result.text);
  return result.text;
}

function setField(
  input: string,
  step: string,
  path: SetField['path'],
  value: unknown
): EditResult {
  return opSetStepField(input, { kind: 'setStepField', step, path, value });
}

function failure(result: EditResult): string {
  if (result.ok) throw new Error('The edit did not fail.');
  return result.code;
}

describe('opSetStepField', () => {
  it('adds a key at the end of the step', () => {
    const out = textOf(
      yamlArraySteps,
      setField(yamlArraySteps, 'steps[0]', ['timeout_sec'], 30)
    );
    expect(out).toBe(
      yamlArraySteps.replace(
        '-o data.csv\n',
        '-o data.csv\n    timeout_sec: 30\n'
      )
    );
  });

  it('adds a key in map form', () => {
    const out = textOf(
      yamlMapSteps,
      setField(yamlMapSteps, 'steps[0]', ['env', 'A'], 'x y')
    );
    expect(out).toBe(
      yamlMapSteps.replace(
        '-o data.csv\n',
        '-o data.csv\n    env:\n      A: x y\n'
      )
    );
  });

  it('creates the maps of a new path', () => {
    const out = textOf(
      yamlArraySteps,
      setField(yamlArraySteps, 'steps[0]', ['retry_policy', 'limit'], 3)
    );
    expect(out).toBe(
      yamlArraySteps.replace(
        '-o data.csv\n',
        '-o data.csv\n    retry_policy:\n      limit: 3\n'
      )
    );
  });

  it('replaces a list and keeps the sibling order', () => {
    const out = textOf(
      yamlArraySteps,
      setField(yamlArraySteps, 'steps[2]', ['depends'], ['transform', 'fetch'])
    );
    expect(out).toBe(
      yamlArraySteps.replace(
        '      - transform\n',
        '      - transform\n      - fetch\n'
      )
    );
  });

  it('replaces a string with a block string', () => {
    const out = textOf(
      yamlArraySteps,
      setField(yamlArraySteps, 'steps[0]', ['run'], 'a\nb\n')
    );
    expect(out).toBe(
      yamlArraySteps.replace(
        '    run: curl -sSf https://example.com/data.csv -o data.csv\n',
        '    run: |\n      a\n      b\n'
      )
    );
  });

  it('removes a key', () => {
    const out = textOf(
      yamlArraySteps,
      setField(yamlArraySteps, 'steps[1]', ['depends'], undefined)
    );
    expect(out).toBe(yamlArraySteps.replace('    depends: [fetch]\n', ''));
  });

  it('removes the maps that the removal leaves empty', () => {
    const input = 'steps:\n  - name: a\n    run: x\n    env:\n      A: "1"\n';
    expect(
      textOf(input, setField(input, 'steps[0]', ['env', 'A'], undefined))
    ).toBe('steps:\n  - name: a\n    run: x\n');
  });

  it('keeps a list that the removal leaves empty', () => {
    const input = 'steps:\n  - name: a\n    with:\n      args: [x]\n';
    expect(
      textOf(input, setField(input, 'steps[0]', ['with', 'args', 0], undefined))
    ).toBe('steps:\n  - name: a\n    with:\n      args: []\n');
  });

  it('edits a handler', () => {
    const out = textOf(
      yamlHandlers,
      setField(yamlHandlers, 'handler_on.success', ['run'], 'echo done')
    );
    expect(out).toBe(yamlHandlers.replace('run: echo ok', 'run: echo done'));
  });

  it('fills an empty entry', () => {
    const input = 'steps:\n  a:\n  b:\n    run: x\n';
    expect(textOf(input, setField(input, 'steps[0]', ['run'], 'echo a'))).toBe(
      'steps:\n  a:\n    run: echo a\n  b:\n    run: x\n'
    );
  });

  it('does not touch a later document', () => {
    const out = textOf(
      yamlMultiDoc,
      setField(yamlMultiDoc, 'steps[0]', ['timeout_sec'], 5)
    );
    const tail = yamlMultiDoc.slice(yamlMultiDoc.indexOf('---'));
    expect(out.endsWith(tail)).toBe(true);
    expect(out).toContain('timeout_sec: 5');
  });

  it('gives noop for the same value', () => {
    expect(
      failure(
        setField(yamlArraySteps, 'steps[2]', ['run'], './publish.sh sorted.csv')
      )
    ).toBe('noop');
  });

  it('gives noop when the key is absent', () => {
    expect(
      failure(setField(yamlArraySteps, 'steps[0]', ['env', 'A'], undefined))
    ).toBe('noop');
    expect(
      failure(setField(yamlArraySteps, 'steps[0]', ['timeout_sec'], undefined))
    ).toBe('noop');
  });

  it('gives unknownStep', () => {
    expect(failure(setField(yamlArraySteps, 'steps[9]', ['run'], 'x'))).toBe(
      'unknownStep'
    );
  });

  it('gives unsupportedForm for a plain string entry', () => {
    const result = setField(yamlShorthandSteps, 'steps[0]', ['run'], 'x');
    expect(failure(result)).toBe('unsupportedForm');
    expect(result.ok ? '' : result.message).toContain(
      'convertStepsToExplicitForm'
    );
  });

  it('gives unsupportedForm for an empty path', () => {
    expect(failure(setField(yamlArraySteps, 'steps[0]', [], 'x'))).toBe(
      'unsupportedForm'
    );
  });

  it('gives invalidYaml when the path crosses a scalar', () => {
    expect(failure(setField(yamlArraySteps, 'steps[0]', ['run', 'x'], 1))).toBe(
      'invalidYaml'
    );
  });

  it('gives parseError for broken YAML', () => {
    expect(failure(setField('steps: [a', 'steps[0]', ['run'], 'x'))).toBe(
      'parseError'
    );
  });
});

describe('opSetStepField quote style', () => {
  const styled = `steps:
  - name: a
    run: 'echo one' # single
    env:
      DQ: "two"
      PLAIN: three
    with:
      script: |
        line1
        line2
      folded: >-
        text here
`;

  it('keeps single quotes', () => {
    expect(
      textOf(styled, setField(styled, 'steps[0]', ['run'], 'echo uno'))
    ).toBe(styled.replace("'echo one'", "'echo uno'"));
  });

  it('keeps double quotes', () => {
    expect(
      textOf(styled, setField(styled, 'steps[0]', ['env', 'DQ'], 'zwei'))
    ).toBe(styled.replace('"two"', '"zwei"'));
  });

  it('keeps plain style', () => {
    expect(
      textOf(styled, setField(styled, 'steps[0]', ['env', 'PLAIN'], 'drei'))
    ).toBe(styled.replace('three', 'drei'));
  });

  it('keeps a literal block', () => {
    expect(
      textOf(
        styled,
        setField(styled, 'steps[0]', ['with', 'script'], 'a\nb\nc\n')
      )
    ).toBe(
      styled.replace(
        '        line1\n        line2\n',
        '        a\n        b\n        c\n'
      )
    );
  });

  it('keeps a folded block', () => {
    expect(
      textOf(styled, setField(styled, 'steps[0]', ['with', 'folded'], 'other'))
    ).toBe(styled.replace('text here', 'other'));
  });

  it('keeps single quotes when the text has a quote mark', () => {
    expect(
      textOf(styled, setField(styled, 'steps[0]', ['run'], "echo 'x'"))
    ).toBe(styled.replace("'echo one'", "'echo ''x'''"));
  });

  it('keeps the quotes of a jq filter', () => {
    expect(
      textOf(
        yamlHttpJq,
        setField(yamlHttpJq, 'steps[1]', ['with', 'filter'], '[.[].id]')
      )
    ).toBe(yamlHttpJq.replace("'[.[].name]'", "'[.[].id]'"));
  });

  it('replaces a number by a string', () => {
    const input = 'steps:\n  - name: a\n    run: x\n    timeout_sec: 30\n';
    expect(
      textOf(input, setField(input, 'steps[0]', ['timeout_sec'], '${T}'))
    ).toBe('steps:\n  - name: a\n    run: x\n    timeout_sec: ${T}\n');
  });

  it('keeps a trailing comment', () => {
    const input = 'steps:\n  - name: a\n    run: old # why\n';
    expect(textOf(input, setField(input, 'steps[0]', ['run'], 'new'))).toBe(
      'steps:\n  - name: a\n    run: new # why\n'
    );
  });
});

describe('opReplaceStepYaml', () => {
  type Replace = Extract<DagEditOp, { kind: 'replaceStepYaml' }>;
  const replace = (input: string, step: string, yaml: string): EditResult =>
    opReplaceStepYaml(input, {
      kind: 'replaceStepYaml',
      step,
      yaml,
    } as Replace);
  const two =
    'name: fetch\nrun: curl -s https://x\nretry_policy:\n  limit: 2\n';

  it('replaces the first step and keeps the comment above', () => {
    expect(
      textOf(yamlArraySteps, replace(yamlArraySteps, 'steps[0]', two))
    ).toBe(
      yamlArraySteps.replace(
        '    run: curl -sSf https://example.com/data.csv -o data.csv\n',
        '    run: curl -s https://x\n    retry_policy:\n      limit: 2\n'
      )
    );
  });

  it('keeps the comment and the blank line above a later step', () => {
    expect(
      textOf(yamlArraySteps, replace(yamlArraySteps, 'steps[2]', two))
    ).toBe(
      yamlArraySteps.replace(
        '  - name: publish\n    depends:\n      - transform\n    run: ./publish.sh sorted.csv\n',
        '  - name: fetch\n    run: curl -s https://x\n    retry_policy:\n      limit: 2\n'
      )
    );
  });

  it('replaces the value of a map entry and keeps the key', () => {
    expect(
      textOf(yamlMapSteps, replace(yamlMapSteps, 'steps[1]', 'run: x'))
    ).toBe(
      yamlMapSteps.replace(
        '    depends: [fetch]\n    run: wc -l data.csv\n',
        '    run: x\n'
      )
    );
  });

  it('replaces a plain string entry by a mapping', () => {
    expect(
      textOf(
        yamlShorthandSteps,
        replace(yamlShorthandSteps, 'steps[1]', 'name: two\nrun: echo 2')
      )
    ).toBe(
      yamlShorthandSteps.replace(
        '  - echo two   # shorthand step\n',
        '  - name: two\n    run: echo 2\n'
      )
    );
  });

  it('replaces a handler', () => {
    expect(
      textOf(
        yamlHandlers,
        replace(yamlHandlers, 'handler_on.failure', 'run: echo f')
      )
    ).toBe(
      yamlHandlers.replace(
        '    # Notify on failure.\n    run: ./notify.sh\n',
        '    run: echo f\n'
      )
    );
  });

  it('replaces an entry of a parallel group', () => {
    expect(
      textOf(
        yamlNestedParallel,
        replace(yamlNestedParallel, 'steps[2]', 'name: unit\nrun: x')
      )
    ).toBe(yamlNestedParallel.replace('run: make test', 'run: x'));
  });

  it('does not touch a later document', () => {
    const out = textOf(
      yamlMultiDoc,
      replace(yamlMultiDoc, 'steps[0]', 'name: run-child\nrun: echo x')
    );
    expect(out.endsWith(yamlMultiDoc.slice(yamlMultiDoc.indexOf('---')))).toBe(
      true
    );
    expect(out).toContain('run: echo x');
  });

  it('gives noop for the same step', () => {
    const same =
      'name: publish\ndepends:\n  - transform\nrun: ./publish.sh sorted.csv\n';
    expect(failure(replace(yamlArraySteps, 'steps[2]', same))).toBe('noop');
  });

  it('gives invalidYaml for a syntax error', () => {
    const result = replace(yamlArraySteps, 'steps[0]', 'a: [');
    expect(failure(result)).toBe('invalidYaml');
    expect(result.ok ? '' : result.message).toBe(
      'Flow sequence in block collection must be sufficiently indented and end with a ] at line 1, column 5'
    );
  });

  it.each(['just text', '', '- a\n- b\n'])(
    'rejects %j as no mapping',
    (yaml) => {
      const result = replace(yamlArraySteps, 'steps[0]', yaml);
      expect(failure(result)).toBe('invalidYaml');
      expect(result.ok ? '' : result.message).toBe(
        'The step YAML must be one mapping.'
      );
    }
  );

  it('rejects two documents', () => {
    expect(
      failure(replace(yamlArraySteps, 'steps[0]', 'a: 1\n---\nb: 2\n'))
    ).toBe('invalidYaml');
  });

  it('gives unknownStep', () => {
    expect(failure(replace(yamlArraySteps, 'steps[7]', 'run: x'))).toBe(
      'unknownStep'
    );
  });
});

describe('opRenameStep', () => {
  type Rename = Extract<DagEditOp, { kind: 'renameStep' }>;
  const rename = (
    input: string,
    step: string,
    newName: string,
    names?: StepNameMap
  ): EditResult =>
    opRenameStep(
      input,
      { kind: 'renameStep', step, newName } as Rename,
      names ? { names } : {}
    );
  const code = (r: EditResult): string => failure(r);

  it('renames in array form and updates a flow depends list', () => {
    expect(
      textOf(yamlArraySteps, rename(yamlArraySteps, 'steps[0]', 'download'))
    ).toBe(
      yamlArraySteps
        .replace('- name: fetch', '- name: download')
        .replace('depends: [fetch]', 'depends: [download]')
    );
  });

  it('updates a block depends list', () => {
    expect(
      textOf(yamlArraySteps, rename(yamlArraySteps, 'steps[1]', 'convert'))
    ).toBe(
      yamlArraySteps
        .replace('- name: transform', '- name: convert')
        .replace('      - transform\n', '      - convert\n')
    );
  });

  it('renames the key in map form and selects the new key', () => {
    const result = rename(yamlMapSteps, 'steps[0]', 'zeta');
    expect(textOf(yamlMapSteps, result)).toBe(
      yamlMapSteps
        .replace('  fetch:\n', '  zeta:\n')
        .replace('depends: [fetch]', 'depends: [zeta]')
    );
    expect(result.ok && result.select).toBe('steps[1]');
  });

  it('updates the targets of a router', () => {
    expect(textOf(yamlRouter, rename(yamlRouter, 'steps[1]', 'srv_err'))).toBe(
      yamlRouter
        .replace('[server_error]', '[srv_err]')
        .replace('- name: server_error', '- name: srv_err')
    );
  });

  it('updates the targets of a legacy routes field', () => {
    const input =
      'steps:\n  - name: pick\n    routes:\n      x: [a, b]\n      y: a\n  - name: a\n    run: x\n  - name: b\n    run: y\n';
    expect(textOf(input, rename(input, 'steps[1]', 'c'))).toBe(
      input
        .replace('[a, b]', '[c, b]')
        .replace('y: a', 'y: c')
        .replace('- name: a', '- name: c')
    );
  });

  // The server reads a number in a list of names as its text.
  it('updates a number in a depends list', () => {
    const input =
      'steps:\n  - name: "1"\n    run: x\n  - name: b\n    depends: [1]\n    run: y\n';
    const out = textOf(input, rename(input, 'steps[0]', 'first'));
    expect(out).toContain('depends: [first]');
  });

  // The server trims a name, so `depends` names it without the blanks.
  it('updates the depends of a name with blanks around it', () => {
    const input =
      'steps:\n  - name: " build "\n    run: x\n  - name: b\n    depends: build\n    run: y\n';
    const out = textOf(input, rename(input, 'steps[0]', 'compile'));
    expect(out).toContain('depends: compile');
  });

  it('keeps the quote style of a scalar depends', () => {
    const input =
      'steps:\n  - name: a\n    run: x\n  - name: b\n    depends: "a"\n    run: y\n  - name: c\n    depends: \'a\'\n    run: z\n';
    expect(textOf(input, rename(input, 'steps[0]', 'd'))).toBe(
      input
        .replace('- name: a', '- name: d')
        .replace('depends: "a"', 'depends: "d"')
        .replace("depends: 'a'", "depends: 'd'")
    );
  });

  it('updates the rewind targets', () => {
    const input = `steps:
  - name: a
    run: x
  - name: gate
    depends: a
    approval:
      prompt: ok
      rewind_to: a
  - name: task
    depends: [a]
    action: human.task
    with:
      push_back:
        rewind_to: a
`;
    expect(textOf(input, rename(input, 'steps[0]', 'b'))).toBe(
      input
        .replace('- name: a', '- name: b')
        .replace('depends: a', 'depends: b')
        .replace('rewind_to: a\n  - name: task', 'rewind_to: b\n  - name: task')
        .replace('depends: [a]', 'depends: [b]')
        .replace('        rewind_to: a\n', '        rewind_to: b\n')
    );
  });

  it('keeps references that resolve through the id', () => {
    expect(textOf(yamlHttpJq, rename(yamlHttpJq, 'steps[0]', 'load'))).toBe(
      yamlHttpJq.replace('    name: fetch\n', '    name: load\n')
    );
  });

  it('keeps tokens, text and other namespaces', () => {
    const input =
      'steps:\n  - name: a\n    run: echo a ${steps.a.outputs.x} ${a.stdout} ${params.a}\n  - name: b\n    depends: [a]\n    run: echo a\n';
    expect(textOf(input, rename(input, 'steps[0]', 'z'))).toBe(
      input
        .replace('- name: a', '- name: z')
        .replace('depends: [a]', 'depends: [z]')
    );
  });

  it('inserts name first for an unnamed step and keeps the comment', () => {
    const input =
      'steps:\n  # Say hi.\n  - run: echo hi\n\n  - name: b\n    depends: [cmd_1]\n    run: x\n';
    const names: StepNameMap = {
      byKey: { 'steps[0]': 'cmd_1', 'steps[1]': 'b' },
      byName: { cmd_1: 'steps[0]', b: 'steps[1]' },
    };
    expect(textOf(input, rename(input, 'steps[0]', 'hi', names))).toBe(
      'steps:\n  # Say hi.\n  - name: hi\n    run: echo hi\n\n  - name: b\n    depends: [hi]\n    run: x\n'
    );
  });

  it('keeps depends that name the id of an unnamed step', () => {
    const input =
      'steps:\n  - id: one\n    run: x\n  - name: b\n    depends: [one]\n    run: y\n';
    expect(textOf(input, rename(input, 'steps[0]', 'first'))).toBe(
      input.replace('  - id: one\n', '  - name: first\n    id: one\n')
    );
  });

  it('renames a handler without touching steps', () => {
    expect(
      textOf(yamlHandlers, rename(yamlHandlers, 'handler_on.success', 'ok'))
    ).toBe(yamlHandlers.replace('  success:\n', '  success:\n    name: ok\n'));
  });

  it('works on the names that the server gives', () => {
    expect(
      textOf(
        yamlArraySteps,
        rename(yamlArraySteps, 'steps[2]', 'ship', namesArraySteps)
      )
    ).toBe(yamlArraySteps.replace('- name: publish', '- name: ship'));
  });

  it('does not touch a later document', () => {
    const out = textOf(yamlMultiDoc, rename(yamlMultiDoc, 'steps[0]', 'go'));
    expect(out.endsWith(yamlMultiDoc.slice(yamlMultiDoc.indexOf('---')))).toBe(
      true
    );
    expect(out).toContain('- name: go');
  });

  it('gives invalidName', () => {
    expect(code(rename(yamlArraySteps, 'steps[0]', ' x'))).toBe('invalidName');
    expect(code(rename(yamlArraySteps, 'steps[0]', ''))).toBe('invalidName');
  });

  it('gives duplicateName for a name and for an id', () => {
    expect(code(rename(yamlArraySteps, 'steps[0]', 'publish'))).toBe(
      'duplicateName'
    );
    expect(code(rename(yamlHttpJq, 'steps[0]', 'names'))).toBe('duplicateName');
    expect(code(rename(yamlMapSteps, 'steps[0]', 'parse'))).toBe(
      'duplicateName'
    );
  });

  it('gives noop for the same name', () => {
    expect(code(rename(yamlArraySteps, 'steps[0]', 'fetch'))).toBe('noop');
  });

  it('gives unsupportedForm for a plain string and a group entry', () => {
    const plain = rename(yamlShorthandSteps, 'steps[0]', 'x');
    expect(code(plain)).toBe('unsupportedForm');
    expect(plain.ok ? '' : plain.message).toContain(
      'convertStepsToExplicitForm'
    );
    expect(code(rename(yamlNestedParallel, 'steps[1]', 'x'))).toBe(
      'unsupportedForm'
    );
  });

  it('gives unknownStep', () => {
    expect(code(rename(yamlArraySteps, 'steps[8]', 'x'))).toBe('unknownStep');
  });
});

describe('opSetStepId', () => {
  type SetId = Extract<DagEditOp, { kind: 'setStepId' }>;
  const setId = (
    input: string,
    step: string,
    id: string | null,
    names?: StepNameMap
  ): EditResult =>
    opSetStepId(
      input,
      { kind: 'setStepId', step, id } as SetId,
      names ? { names } : {}
    );
  const message = (r: EditResult): string => (r.ok ? '' : r.message);

  it('rewrites the tokens of other steps', () => {
    expect(textOf(yamlHttpJq, setId(yamlHttpJq, 'steps[0]', 'load'))).toBe(
      yamlHttpJq
        .replace('  - id: fetch\n', '  - id: load\n')
        .replace('${steps.fetch.outputs.body}', '${steps.load.outputs.body}')
    );
  });

  it('leaves similar text unchanged', () => {
    const input = `steps:
  - id: fetch
    name: get
    run: echo fetch \${stepsx} \${steps.fetch2.outputs.a} \${fetch2.stdout}
  - name: use
    run: |
      echo "\${steps.fetch.outputs.a}" \${fetch.stdout} \${params.fetch}
      echo fetch \\\${steps.fetch.outputs.a}
    env:
      A: '\${steps.fetch.outputs.b}'
      B: "\${steps.fetch.outputs.c}"
`;
    expect(textOf(input, setId(input, 'steps[0]', 'load'))).toBe(
      input
        .replace('  - id: fetch\n', '  - id: load\n')
        .replace(
          'echo "${steps.fetch.outputs.a}" ${fetch.stdout}',
          'echo "${steps.load.outputs.a}" ${load.stdout}'
        )
        .replace("'${steps.fetch.outputs.b}'", "'${steps.load.outputs.b}'")
        .replace('"${steps.fetch.outputs.c}"', '"${steps.load.outputs.c}"')
    );
  });

  it('rewrites tokens in handlers', () => {
    const input =
      'handler_on:\n  failure:\n    run: echo ${fetch.exit_code}\nsteps:\n  - id: fetch\n    run: x\n';
    expect(textOf(input, setId(input, 'steps[0]', 'get'))).toBe(
      input
        .replace('${fetch.exit_code}', '${get.exit_code}')
        .replace('- id: fetch', '- id: get')
    );
  });

  it('keeps depends that name the step by its name', () => {
    expect(textOf(yamlHttpJq, setId(yamlHttpJq, 'steps[0]', 'load'))).toContain(
      'depends: [fetch]'
    );
  });

  it('rewrites depends and routes of a step that takes its name from the id', () => {
    const input = `steps:
  - id: one
    run: echo
  - name: b
    depends: [one]
    run: x \${steps.one.outputs.a}
  - name: pick
    action: router.route
    with:
      value: v
      routes:
        x: [one]
`;
    expect(textOf(input, setId(input, 'steps[0]', 'uno'))).toBe(
      input
        .replace('- id: one', '- id: uno')
        .replace('depends: [one]', 'depends: [uno]')
        .replace('steps.one.', 'steps.uno.')
        .replace('x: [one]', 'x: [uno]')
    );
  });

  it('adds an id after name', () => {
    expect(
      textOf(yamlArraySteps, setId(yamlArraySteps, 'steps[1]', 'conv'))
    ).toBe(
      yamlArraySteps.replace(
        '  - name: transform   # keep the header row\n',
        '  - name: transform   # keep the header row\n    id: conv\n'
      )
    );
  });

  it('adds an id first when the name is unknown', () => {
    const input = 'steps:\n  - run: x\n    env:\n      A: b\n';
    expect(textOf(input, setId(input, 'steps[0]', 'first'))).toBe(
      'steps:\n  - id: first\n    run: x\n    env:\n      A: b\n'
    );
  });

  it('adds an id in map form', () => {
    expect(textOf(yamlMapSteps, setId(yamlMapSteps, 'steps[0]', 'f'))).toBe(
      yamlMapSteps.replace(
        '  fetch:\n    run:',
        '  fetch:\n    id: f\n    run:'
      )
    );
  });

  it('keeps the name of an unnamed step when it gets an id', () => {
    const input =
      'steps:\n  # Say hi.\n  - run: echo hi\n\n  - name: b\n    depends: [cmd_1]\n    run: x\n';
    const names: StepNameMap = {
      byKey: { 'steps[0]': 'cmd_1', 'steps[1]': 'b' },
      byName: { cmd_1: 'steps[0]', b: 'steps[1]' },
    };
    expect(textOf(input, setId(input, 'steps[0]', 'hi', names))).toBe(
      'steps:\n  # Say hi.\n  - name: cmd_1\n    id: hi\n    run: echo hi\n\n  - name: b\n    depends: [cmd_1]\n    run: x\n'
    );
  });

  it('removes an id', () => {
    expect(textOf(yamlHttpJq, setId(yamlHttpJq, 'steps[1]', null))).toBe(
      yamlHttpJq.replace(
        '  - id: names\n    name: extract\n',
        '  - name: extract\n'
      )
    );
  });

  it('writes the name when it removes the id of an unnamed step', () => {
    const input =
      'steps:\n  - id: one\n    run: echo\n  - name: b\n    depends: [one]\n    run: x\n';
    expect(textOf(input, setId(input, 'steps[0]', null))).toBe(
      input.replace('  - id: one\n', '  - name: one\n')
    );
  });

  it('rewrites depends of the id to the name when it removes the id', () => {
    const input = `steps:
  - id: one
    name: First
    run: echo
  - name: b
    depends: [one]
    run: x
`;
    expect(textOf(input, setId(input, 'steps[0]', null))).toBe(
      input
        .replace('  - id: one\n    name: First\n', '  - name: First\n')
        .replace('depends: [one]', 'depends: [First]')
    );
  });

  it('refuses to remove an id that a token still uses', () => {
    const result = setId(yamlHttpJq, 'steps[0]', null);
    expect(failure(result)).toBe('invalidName');
    expect(message(result)).toBe(
      'Step "extract" still uses references to the ID "fetch". Change them first.'
    );
  });

  it('does not touch a later document', () => {
    const out = textOf(yamlMultiDoc, setId(yamlMultiDoc, 'steps[0]', 'rc'));
    expect(out.endsWith(yamlMultiDoc.slice(yamlMultiDoc.indexOf('---')))).toBe(
      true
    );
    expect(out).toContain('    id: rc\n');
  });

  it('gives invalidName for a bad id', () => {
    expect(failure(setId(yamlHttpJq, 'steps[0]', 'a-b'))).toBe('invalidName');
    expect(failure(setId(yamlHttpJq, 'steps[0]', 'Env'))).toBe('invalidName');
    expect(failure(setId(yamlHttpJq, 'steps[0]', ''))).toBe('invalidName');
  });

  it('gives duplicateName for an id and for a name of another step', () => {
    expect(failure(setId(yamlHttpJq, 'steps[0]', 'names'))).toBe(
      'duplicateName'
    );
    expect(failure(setId(yamlHttpJq, 'steps[0]', 'extract'))).toBe(
      'duplicateName'
    );
  });

  it('accepts the name of the same step as id', () => {
    const input = 'steps:\n  - name: build\n    run: x\n';
    expect(textOf(input, setId(input, 'steps[0]', 'build'))).toBe(
      'steps:\n  - name: build\n    id: build\n    run: x\n'
    );
  });

  it('gives noop for the same id and for no id', () => {
    expect(failure(setId(yamlHttpJq, 'steps[0]', 'fetch'))).toBe('noop');
    expect(failure(setId(yamlArraySteps, 'steps[0]', null))).toBe('noop');
  });

  it('gives unsupportedForm for a plain string and a group entry', () => {
    expect(failure(setId(yamlShorthandSteps, 'steps[0]', 'x'))).toBe(
      'unsupportedForm'
    );
    expect(failure(setId(yamlNestedParallel, 'steps[1]', 'x'))).toBe(
      'unsupportedForm'
    );
  });

  it('gives unknownStep', () => {
    expect(failure(setId(yamlArraySteps, 'steps[8]', 'x'))).toBe('unknownStep');
  });
});
