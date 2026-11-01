// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { EditResult, StepNameMap } from '../../contracts';
import {
  yamlChain,
  yamlMapSteps,
  yamlNestedParallel,
  yamlMultiDoc,
  yamlShorthandSteps,
} from '../../testing/fixtures';
import { opConvertStepsToExplicitForm } from '../ops/convert';
import { parseDagDoc } from '../parse';

const OP = { kind: 'convertStepsToExplicitForm' } as const;

function convert(text: string, names?: StepNameMap): EditResult {
  return opConvertStepsToExplicitForm(text, OP, names ? { names } : {});
}

/** The text of a successful result; fails the test otherwise. */
function converted(text: string, names?: StepNameMap): string {
  const result = convert(text, names);
  if (!result.ok) throw new Error(`${result.code}: ${result.message}`);
  return result.text;
}

function namesOf(byKey: Record<string, string>): StepNameMap {
  const byName: Record<string, string> = {};
  for (const [key, name] of Object.entries(byKey)) byName[name] = key;
  return { byKey, byName };
}

describe('shorthand steps', () => {
  it('becomes run mappings without names', () => {
    expect(converted(yamlShorthandSteps)).toBe(`name: shorthand
steps:
  - run: echo one
  - run: echo two   # shorthand step
  - name: three
    run: echo three
`);
  });

  it('writes the server name first', () => {
    const names = namesOf({
      'steps[0]': 'cmd_1',
      'steps[1]': 'cmd_2',
      'steps[2]': 'three',
    });
    expect(converted(yamlShorthandSteps, names)).toBe(`name: shorthand
steps:
  - name: cmd_1
    run: echo one
  - name: cmd_2
    run: echo two # shorthand step
  - name: three
    run: echo three
`);
  });

  it('names only the steps that have a server name', () => {
    const text = converted(
      yamlShorthandSteps,
      namesOf({ 'steps[1]': 'cmd_2' })
    );
    expect(text).toBe(`name: shorthand
steps:
  - run: echo one
  - name: cmd_2
    run: echo two # shorthand step
  - name: three
    run: echo three
`);
  });

  it('keeps comments and blank lines around the entries', () => {
    const text = `steps:
  # lead

  - "echo a"   # tail
  # mid
  - echo b
`;
    expect(converted(text)).toBe(`steps:
  # lead

  - run: "echo a"   # tail
  # mid
  - run: echo b
`);
  });

  it('keeps the other lines of the file as they are', () => {
    const text = `# header
name:   keep   # spacing
steps:
    - echo a
    -   name: b
        run:   x
`;
    const result = convert(text);
    expect(result.ok && result.edits).toEqual([
      { from: 41, to: 54, insert: '    - run: echo a\n' },
    ]);
  });

  it('keeps the string style', () => {
    const text = `steps:
  - 'it''s'
  - |
    echo a
    echo b
`;
    expect(converted(text)).toBe(`steps:
  - run: 'it''s'
  - run: |
      echo a
      echo b
`);
  });

  it('keeps the StepKeys and the number of steps', () => {
    const text = converted(yamlShorthandSteps);
    const before = parseDagDoc(yamlShorthandSteps).steps;
    const after = parseDagDoc(text).steps;
    expect(after.map((s) => s.key)).toEqual(before.map((s) => s.key));
    expect(after.map((s) => s.form)).toEqual(['block', 'block', 'block']);
    expect(after.map((s) => s.value.run)).toEqual(
      before.map((s) => s.value.run)
    );
  });

  it('does not set select', () => {
    const result = convert(yamlShorthandSteps);
    expect(result.ok && 'select' in result).toBe(false);
  });
});

describe('parallel groups', () => {
  it('flattens the group in place', () => {
    expect(converted(yamlNestedParallel)).toBe(`name: ci
steps:
  - name: prepare
    run: ./prepare.sh
  # Parallel group.
  - name: lint
    run: make lint
  - name: unit
    run: make test
  - name: report
    run: ./report.sh
`);
  });

  it('keeps the StepKeys and the number of steps', () => {
    const before = parseDagDoc(yamlNestedParallel).steps;
    const after = parseDagDoc(converted(yamlNestedParallel)).steps;
    expect(after.map((s) => s.key)).toEqual(before.map((s) => s.key));
    expect(after.map((s) => s.explicitName)).toEqual(
      before.map((s) => s.explicitName)
    );
    expect(after.map((s) => s.form)).toEqual([
      'block',
      'block',
      'block',
      'block',
    ]);
  });

  it('keeps the comments around a group', () => {
    const text = `steps:
  - name: a
    run: x

  # Group comment.
  - - name: b   # b tail
      run: y
    # between
    - name: c
      run: z
  # after group
  - name: d
    run: w
`;
    expect(converted(text)).toBe(`steps:
  - name: a
    run: x

  # Group comment.
  - name: b   # b tail
    run: y
  # between
  - name: c
    run: z
  # after group
  - name: d
    run: w
`);
  });

  it('converts a shorthand inside a group', () => {
    const text =
      'steps:\n  - - echo a\n    - name: b\n      run: y\n  - echo c\n';
    const names = namesOf({ 'steps[0]': 'cmd_1', 'steps[2]': 'cmd_3' });
    expect(converted(text, names)).toBe(`steps:
  - name: cmd_1
    run: echo a
  - name: b
    run: y
  - name: cmd_3
    run: echo c
`);
  });

  it('flattens several groups and drops an empty one', () => {
    const text =
      'steps:\n  - - run: a\n    - run: b\n  - - run: c\n  - []\n  - run: d\n';
    expect(converted(text)).toBe(`steps:
  - run: a
  - run: b
  - run: c
  - run: d
`);
  });

  // The server names an unnamed step from its position, which flattening moves.
  it('pins the server name of unnamed steps from the first group on', () => {
    const text = `steps:
  - run: a
  - - run: b
    - name: c
      run: y
  - run: d
  - id: e
    run: f
`;
    const names = namesOf({
      'steps[0]': 'step_1',
      'steps[1]': 'step_2',
      'steps[2]': 'c',
      'steps[3]': 'step_4',
      'steps[4]': 'e',
    });
    expect(converted(text, names)).toBe(`steps:
  - run: a
  - name: step_2
    run: b
  - name: c
    run: y
  - name: step_4
    run: d
  - id: e
    run: f
`);
  });

  it('does not pin a name in a file without groups', () => {
    const names = namesOf({ 'steps[0]': 'step_1', 'steps[1]': 'cmd_2' });
    expect(converted('steps:\n  - run: a\n  - echo b\n', names)).toBe(
      'steps:\n  - run: a\n  - name: cmd_2\n    run: echo b\n'
    );
  });

  it('changes only the lines of the group', () => {
    const text = 'steps:\n  - run: a\n  - - run: b\n    - run: c\n  - run: d\n';
    const result = convert(text);
    expect(result.ok && result.edits).toEqual([
      { from: 18, to: 44, insert: '  - run: b\n  - run: c\n' },
    ]);
  });
});

describe('chain DAG', () => {
  it('writes depends and sets the graph type', () => {
    expect(converted(yamlChain)).toBe(`# Chain DAG
type: graph
name: release
steps:
  - name: build
    run: make build
  - name: test
    depends: [build]
    run: make test
  - name: deploy
    depends: [test]
    run: make deploy
`);
  });

  it('makes a group one position', () => {
    const text = `type: chain
steps:
  - name: a
    run: x
  # The two run after a.
  - - name: b
      run: y
    - name: c
      run: z
  - name: d
    run: w
`;
    expect(converted(text)).toBe(`type: graph
steps:
  - name: a
    run: x
  # The two run after a.
  - name: b
    depends: [a]
    run: y
  - name: c
    depends: [a]
    run: z
  - name: d
    depends: [b, c]
    run: w
`);
  });

  it('keeps the StepKeys and the number of steps', () => {
    const text =
      'type: chain\nsteps:\n  - name: a\n    run: x\n  - - name: b\n      run: y\n    - name: c\n      run: z\n  - name: d\n    run: w\n';
    const before = parseDagDoc(text).steps;
    const after = parseDagDoc(converted(text)).steps;
    expect(after.map((s) => s.key)).toEqual(before.map((s) => s.key));
    expect(after.map((s) => s.explicitName)).toEqual(['a', 'b', 'c', 'd']);
    expect(after.map((s) => s.depends)).toEqual([[], ['a'], ['a'], ['b', 'c']]);
  });

  it('uses the server names of shorthand steps', () => {
    const text =
      'type: chain\nsteps:\n  - echo a\n  - echo b\n  - name: c\n    run: x\n';
    const names = namesOf({
      'steps[0]': 'cmd_1',
      'steps[1]': 'cmd_2',
      'steps[2]': 'c',
    });
    expect(converted(text, names)).toBe(`type: graph
steps:
  - name: cmd_1
    run: echo a
  - name: cmd_2
    depends: [cmd_1]
    run: echo b
  - name: c
    depends: [cmd_2]
    run: x
`);
  });

  it('names an unnamed step from the server names when others depend on it', () => {
    const text = 'type: chain\nsteps:\n  - run: a\n  - name: b\n    run: x\n';
    const names = namesOf({ 'steps[0]': 'step_1', 'steps[1]': 'b' });
    expect(converted(text, names)).toBe(`type: graph
steps:
  - name: step_1
    run: a
  - name: b
    depends: [step_1]
    run: x
`);
  });

  it('returns invalidName for an unnamed step that others depend on', () => {
    const text = 'type: chain\nsteps:\n  - run: a\n  - name: b\n    run: x\n';
    expect(convert(text)).toMatchObject({ ok: false, code: 'invalidName' });
    expect(
      convert('type: chain\nsteps:\n  - echo a\n  - echo b\n')
    ).toMatchObject({
      ok: false,
      code: 'invalidName',
    });
  });

  it('does not need a name for the last step', () => {
    const text = 'type: chain\nsteps:\n  - name: a\n    run: x\n  - run: y\n';
    expect(converted(text)).toBe(
      'type: graph\nsteps:\n  - name: a\n    run: x\n  - depends: [a]\n    run: y\n'
    );
  });

  it('refers to a step by its id when it has no name', () => {
    const text =
      'type: chain\nsteps:\n  - id: first\n    run: x\n  - id: second\n    run: y\n';
    expect(converted(text)).toBe(
      'type: graph\nsteps:\n  - id: first\n    run: x\n  - id: second\n    depends: [first]\n    run: y\n'
    );
  });

  it('keeps quotes and the comment of the type', () => {
    const text =
      'type: "chain"   # sequential\nsteps:\n  - name: a\n    run: x\n';
    expect(converted(text)).toBe(
      'type: "graph"   # sequential\nsteps:\n  - name: a\n    run: x\n'
    );
  });

  it('gives the step after an empty group no dependency', () => {
    const text =
      'type: chain\nsteps:\n  - name: a\n    run: x\n  - []\n  - name: b\n    run: y\n';
    expect(converted(text)).toBe(
      'type: graph\nsteps:\n  - name: a\n    run: x\n  - name: b\n    run: y\n'
    );
  });

  it('only sets the type for map form', () => {
    const text = 'type: chain\nsteps:\n  b:\n    run: y\n  a:\n    run: x\n';
    expect(converted(text)).toBe(
      'type: graph\nsteps:\n  b:\n    run: y\n  a:\n    run: x\n'
    );
  });

  it('sets the type when there are no steps', () => {
    expect(converted('type: chain\nname: a\n')).toBe('type: graph\nname: a\n');
  });

  it('pins the server names of unnamed steps after a group', () => {
    const text =
      'type: chain\nsteps:\n  - name: a\n    run: x\n  - - name: b\n      run: y\n    - name: c\n      run: z\n  - run: w\n';
    const names = namesOf({
      'steps[0]': 'a',
      'steps[1]': 'b',
      'steps[2]': 'c',
      'steps[3]': 'step_3',
    });
    expect(converted(text, names)).toBe(`type: graph
steps:
  - name: a
    run: x
  - name: b
    depends: [a]
    run: y
  - name: c
    depends: [a]
    run: z
  - name: step_3
    depends: [b, c]
    run: w
`);
  });

  it('keeps the bytes of document 1', () => {
    const text =
      'type: chain\nsteps:\n  - name: a\n    run: x\n  - name: b\n    run: y\n---\ntype: chain\nsteps:\n  - echo z\n';
    const out = converted(text);
    expect(out.slice(out.indexOf('---'))).toBe(
      '---\ntype: chain\nsteps:\n  - echo z\n'
    );
    expect(out.startsWith('type: graph\n')).toBe(true);
  });
});

describe('alias entries', () => {
  const defs = 'defs: &s\n  name: a\n  run: x\n';

  it('converts the other steps and leaves the alias', () => {
    const text = `${defs}steps:\n  - *s\n  - echo b\n`;
    expect(converted(text)).toBe(`${defs}steps:\n  - *s\n  - run: echo b\n`);
  });

  it('refuses a chain whose alias would need depends', () => {
    const text = `${defs}type: chain\nsteps:\n  - name: b\n    run: y\n  - *s\n`;
    expect(convert(text)).toMatchObject({
      ok: false,
      code: 'unsupportedForm',
    });
  });

  it('refuses a chain in which an alias must be referenced', () => {
    const text = `${defs}type: chain\nsteps:\n  - *s\n  - name: b\n    run: y\n`;
    expect(convert(text)).toMatchObject({
      ok: false,
      code: 'unsupportedForm',
    });
  });

  it('keeps the StepKeys when it flattens a group with an alias', () => {
    const text = `${defs}steps:\n  - - *s\n    - name: b\n      run: y\n  - name: c\n    run: z\n`;
    const after = parseDagDoc(converted(text)).steps;
    expect(after.map((s) => [s.key, s.explicitName])).toEqual([
      ['steps[0]', 'a'],
      ['steps[1]', 'b'],
      ['steps[2]', 'c'],
    ]);
  });
});

describe('nothing to convert', () => {
  it('returns noop for block steps', () => {
    const result = convert('steps:\n  - name: a\n    run: x\n');
    expect(result).toMatchObject({ ok: false, code: 'noop' });
  });

  it('returns noop for map form', () => {
    expect(convert(yamlMapSteps)).toMatchObject({ ok: false, code: 'noop' });
  });

  it('returns noop without steps', () => {
    expect(convert('name: a\n')).toMatchObject({ ok: false, code: 'noop' });
  });

  it('returns parseError for broken YAML', () => {
    expect(convert('steps: [a\n')).toMatchObject({
      ok: false,
      code: 'parseError',
    });
  });
});

describe('agent DAG', () => {
  it('returns agentType', () => {
    const text = 'type: agent\nsteps:\n  - echo a\n';
    expect(convert(text)).toMatchObject({ ok: false, code: 'agentType' });
  });
});

describe('later documents', () => {
  it('keeps the bytes of document 1', () => {
    const text = `${yamlMultiDoc.replace('- name: run-child', '- echo first\n  - name: run-child')}`;
    const out = converted(text);
    const tail = text.slice(text.indexOf('\n---\n'));
    expect(out.endsWith(tail)).toBe(true);
    expect(out.startsWith('name: parent\nsteps:\n  - run: echo first\n')).toBe(
      true
    );
  });

  it('is not changed when only document 1 has shorthand', () => {
    const text = 'steps:\n  - name: a\n    run: x\n---\nsteps:\n  - echo b\n';
    expect(convert(text)).toMatchObject({ ok: false, code: 'noop' });
  });
});
