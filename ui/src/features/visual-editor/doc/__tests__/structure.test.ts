// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { ApplyEditOptions, DagEditOp, EditResult } from '../../contracts';
import {
  yamlArraySteps,
  yamlChain,
  yamlHandlers,
  yamlMapSteps,
  yamlMultiDoc,
  yamlNestedParallel,
  yamlRouter,
  yamlShorthandSteps,
} from '../../testing/fixtures';
import {
  opAddDependency,
  opAddStep,
  opDeleteStep,
  opRemoveDependency,
  opSetRouteTargets,
} from '../ops/structure';
import { applyTextEdits } from '../patch';

type Op<K extends DagEditOp['kind']> = Extract<DagEditOp, { kind: K }>;
type Done = Extract<EditResult, { ok: true }>;

const AGENT_DAG = `type: agent
name: helper
steps:
  - name: ask
    run: echo ask
`;

/** Text with comments above, after and between entries, and a tail comment. */
const COMMENTED = `# top
name: c
steps:
  # first
  - name: a   # trailing
    run: echo a

  # second
  - name: b
    depends: [a]
    run: echo b
  # tail comment
`;

/** Checks that the edits give the text, and that they are small. */
function done(result: EditResult, input: string, maxEdits = 3): Done {
  if (!result.ok) throw new Error(`${result.code}: ${result.message}`);
  expect(result.text).toBe(applyTextEdits(input, result.edits));
  expect(result.edits.length).toBeLessThanOrEqual(maxEdits);
  const removed = result.edits.reduce((n, e) => n + (e.to - e.from), 0);
  expect(removed).toBeLessThanOrEqual(200);
  return result;
}

function failed(result: EditResult): { code: string; message: string } {
  if (result.ok) throw new Error('expected a failure');
  return result;
}

const NAMED: ApplyEditOptions = {
  names: { byKey: { 'steps[1]': 'cmd_2' }, byName: { cmd_2: 'steps[1]' } },
};

describe('opAddStep', () => {
  const add = (
    text: string,
    op: Partial<Op<'addStep'>> & Pick<Op<'addStep'>, 'step'>,
    options: ApplyEditOptions = {}
  ) => opAddStep(text, { kind: 'addStep', ...op }, options);

  it('appends to the array form', () => {
    const result = done(
      add(yamlArraySteps, {
        step: { action: 'http.request', with: { method: 'GET', url: '' } },
        dependsOn: ['steps[2]'],
      }),
      yamlArraySteps
    );
    expect(result.text).toBe(`${yamlArraySteps}
  - name: http_request
    depends: [publish]
    action: http.request
    with:
      method: GET
      url: ""
`);
    expect(result.select).toBe('steps[3]');
  });

  // A command step has no action: it is named like the server names it.
  it('names a command step cmd_<position>', () => {
    const result = done(
      add(yamlArraySteps, { step: { run: '' } }),
      yamlArraySteps
    );
    expect(result.text).toContain('  - name: cmd_4\n    run: ""');

    const again = done(add(result.text, { step: { run: '' } }), result.text);
    expect(again.text).toContain('  - name: cmd_5\n    run: ""');
  });

  it('skips a cmd name that is in use', () => {
    const text = 'steps:\n  - name: cmd_2\n    run: a\n';
    const result = done(add(text, { step: { run: '' } }), text);
    expect(result.text).toContain('name: cmd_3');
  });

  it('inserts after a step of the array form', () => {
    const result = done(
      add(yamlArraySteps, {
        step: { name: 'check', run: 'echo ok' },
        after: 'steps[0]',
      }),
      yamlArraySteps
    );
    expect(result.text).toBe(
      yamlArraySteps.replace(
        '\n  - name: transform',
        '\n  - name: check\n    run: echo ok\n\n  - name: transform'
      )
    );
    expect(result.select).toBe('steps[1]');
  });

  it('adds a key to the map form after the given entry', () => {
    const result = done(
      add(yamlMapSteps, {
        step: { name: 'next', run: 'echo hi\necho 2' },
        after: 'steps[0]',
        dependsOn: ['steps[0]'],
      }),
      yamlMapSteps
    );
    expect(result.text).toBe(`name: map-form
steps:
  # Entry point.
  fetch:
    run: curl -sSf https://example.com/data.csv -o data.csv

  next:
    depends: [fetch]
    run: |-
      echo hi
      echo 2

  parse:
    depends: [fetch]
    run: wc -l data.csv
`);
  });

  it('inserts before the first step with atStart', () => {
    const result = done(
      add(yamlArraySteps, {
        step: { name: 'prepare', run: 'echo prepare' },
        atStart: true,
      }),
      yamlArraySteps
    );
    expect(result.text).toBe(
      yamlArraySteps.replace(
        '\n  # Download the source file.\n',
        '\n  - name: prepare\n    run: echo prepare\n\n  # Download the source file.\n'
      )
    );
    expect(result.select).toBe('steps[0]');
  });

  it('inserts before the first entry of the map form with atStart', () => {
    const result = done(
      add(yamlMapSteps, {
        step: { name: 'prepare', run: 'echo prepare' },
        atStart: true,
      }),
      yamlMapSteps
    );
    expect(result.text).toBe(`name: map-form
steps:
  prepare:
    run: echo prepare

  # Entry point.
  fetch:
    run: curl -sSf https://example.com/data.csv -o data.csv

  parse:
    depends: [fetch]
    run: wc -l data.csv
`);
  });

  it('makes the new step the first of a chain DAG with atStart', () => {
    const result = done(
      add(yamlChain, {
        step: { name: 'lint', run: 'make lint' },
        atStart: true,
      }),
      yamlChain
    );
    expect(result.text).toBe(
      yamlChain.replace(
        'steps:\n',
        'steps:\n  - name: lint\n    run: make lint\n'
      )
    );
  });

  it('selects the key in server order for the map form', () => {
    const result = done(
      add(yamlMapSteps, { step: { name: 'zeta', run: 'echo z' } }),
      yamlMapSteps
    );
    expect(result.select).toBe('steps[2]');
    const first = done(
      add(yamlMapSteps, { step: { name: 'alpha', run: 'echo a' } }),
      yamlMapSteps
    );
    expect(first.select).toBe('steps[0]');
  });

  it('keeps the comments of a commented file', () => {
    const result = done(
      add(COMMENTED, { step: { run: 'echo c' }, dependsOn: ['steps[1]'] }),
      COMMENTED
    );
    expect(result.text).toBe(`# top
name: c
steps:
  # first
  - name: a   # trailing
    run: echo a

  # second
  - name: b
    depends: [a]
    run: echo b

  - name: cmd_3
    depends: [b]
    run: echo c
  # tail comment
`);
  });

  it('puts id after the name and before depends', () => {
    const result = done(
      add(yamlArraySteps, {
        step: { name: 'n', id: 'nid', run: 'x' },
        dependsOn: ['steps[0]', 'steps[1]'],
      }),
      yamlArraySteps
    );
    expect(
      result.text.endsWith(`
  - name: n
    id: nid
    depends: [fetch, transform]
    run: x
`)
    ).toBe(true);
  });

  it('writes depends in the style of the file', () => {
    const text = `steps:
  - name: a
    run: x
  - name: b
    depends:
      - a
      - a2
    run: y
`;
    const result = done(
      add(text, {
        step: { name: 'c', run: 'z' },
        dependsOn: ['steps[0]', 'steps[1]'],
      }),
      text
    );
    expect(
      result.text.endsWith(`
  - name: c
    depends:
      - a
      - b
    run: z
`)
    ).toBe(true);
  });

  it('writes a scalar depends when the file uses scalars', () => {
    const text =
      'steps:\n  - name: a\n    run: x\n  - name: b\n    depends: a\n    run: y\n';
    const result = done(
      add(text, { step: { name: 'c', run: 'z' }, dependsOn: ['steps[1]'] }),
      text
    );
    expect(result.text).toBe(
      `${text}  - name: c\n    depends: b\n    run: z\n`
    );
  });

  it('creates the steps key as a block array', () => {
    const text = 'env:\n  - A: b\nname: x\n';
    const result = done(add(text, { step: { run: 'echo c' } }), text);
    expect(result.text).toBe(
      'env:\n  - A: b\nname: x\nsteps:\n  - name: cmd_1\n    run: echo c\n'
    );
    expect(result.select).toBe('steps[0]');
  });

  it('turns an empty flow list into a block array', () => {
    const text = 'env:\n  - A: b\nsteps: []\nname: x\n';
    const result = done(add(text, { step: { run: 'echo c' } }), text);
    expect(result.text).toBe(
      'env:\n  - A: b\nsteps:\n  - name: cmd_1\n    run: echo c\nname: x\n'
    );
  });

  it('fills an empty steps key', () => {
    const text = 'env:\n  - A: b\nsteps:\nname: x\n';
    const result = done(add(text, { step: { run: 'echo c' } }), text);
    expect(result.text).toBe(
      'env:\n  - A: b\nsteps:\n  - name: cmd_1\n    run: echo c\nname: x\n'
    );
  });

  it('leaves the bytes after the separator unchanged', () => {
    const result = done(
      add(yamlMultiDoc, { step: { name: 'two', run: 'x' } }),
      yamlMultiDoc
    );
    const tail = yamlMultiDoc.slice(yamlMultiDoc.indexOf('---'));
    expect(result.text.endsWith(tail)).toBe(true);
    expect(
      result.text.startsWith(
        yamlMultiDoc.slice(0, yamlMultiDoc.indexOf('---') - 1)
      )
    ).toBe(true);
    expect(result.text).toBe(
      yamlMultiDoc.replace('\n\n---\n', '\n  - name: two\n    run: x\n\n---\n')
    );
  });

  it('names the step from the action when it has no name', () => {
    const text = 'steps:\n  - name: http_request\n    run: a\n';
    const result = done(add(text, { step: { action: 'http.request' } }), text);
    expect(result.text).toBe(
      `${text}  - name: http_request_2\n    action: http.request\n`
    );
  });

  it('avoids the id of another step when it names the step', () => {
    const text = 'steps:\n  - name: a\n    id: cmd_2\n    run: a\n';
    const result = done(add(text, { step: { run: 'b' } }), text);
    expect(result.text).toBe(`${text}  - name: cmd_3\n    run: b\n`);
  });

  it('avoids the server name of an unnamed step', () => {
    const text = 'steps:\n  - run: a\n';
    const options: ApplyEditOptions = {
      names: { byKey: { 'steps[0]': 'cmd_2' }, byName: { cmd_2: 'steps[0]' } },
    };
    const result = done(add(text, { step: { run: 'b' } }, options), text);
    expect(result.text).toBe(`${text}  - name: cmd_3\n    run: b\n`);
  });

  it('quotes a name that is not a string in YAML', () => {
    const text = 'steps:\n  - name: a\n    run: a\n';
    const result = done(add(text, { step: { name: '123', run: 'b' } }), text);
    expect(result.text).toBe(`${text}  - name: "123"\n    run: b\n`);
  });

  it('adds a step to an empty text', () => {
    const result = done(add('', { step: { run: 'echo c' } }), '');
    expect(result.text).toContain('steps:');
    expect(result.select).toBe('steps[0]');
  });

  it('refuses a duplicate name', () => {
    expect(failed(add(yamlArraySteps, { step: { name: 'fetch' } })).code).toBe(
      'duplicateName'
    );
    expect(failed(add(yamlMapSteps, { step: { name: 'parse' } })).code).toBe(
      'duplicateName'
    );
  });

  it('refuses a name that is the id of another step', () => {
    const text = 'steps:\n  - name: a\n    id: ida\n    run: a\n';
    expect(failed(add(text, { step: { name: 'ida' } })).code).toBe(
      'duplicateName'
    );
    expect(failed(add(text, { step: { id: 'a' } })).code).toBe('duplicateName');
    expect(failed(add(text, { step: { id: 'ida' } })).code).toBe(
      'duplicateName'
    );
  });

  it('refuses an invalid name or id', () => {
    expect(failed(add(yamlArraySteps, { step: { name: ' x' } })).code).toBe(
      'invalidName'
    );
    expect(failed(add(yamlArraySteps, { step: { name: '' } })).code).toBe(
      'invalidName'
    );
    expect(
      failed(add(yamlArraySteps, { step: { name: 'ask_user' } })).code
    ).toBe('invalidName');
    expect(failed(add(yamlArraySteps, { step: { id: '1a' } })).code).toBe(
      'invalidName'
    );
  });

  it('gives unknownStep for a missing target', () => {
    expect(
      failed(add(yamlArraySteps, { step: { run: 'x' }, after: 'steps[9]' }))
        .code
    ).toBe('unknownStep');
    expect(
      failed(
        add(yamlArraySteps, { step: { run: 'x' }, dependsOn: ['steps[9]'] })
      ).code
    ).toBe('unknownStep');
  });

  it('gives unsupportedForm for a shorthand or grouped target', () => {
    expect(
      failed(add(yamlShorthandSteps, { step: { run: 'x' }, after: 'steps[0]' }))
        .code
    ).toBe('unsupportedForm');
    expect(
      failed(
        add(yamlShorthandSteps, { step: { run: 'x' }, dependsOn: ['steps[1]'] })
      ).code
    ).toBe('unsupportedForm');
    expect(
      failed(add(yamlNestedParallel, { step: { run: 'x' }, after: 'steps[1]' }))
        .code
    ).toBe('unsupportedForm');
    expect(
      failed(
        add(yamlNestedParallel, { step: { run: 'x' }, dependsOn: ['steps[2]'] })
      ).code
    ).toBe('unsupportedForm');
  });

  it('appends after the last of a file with shorthand steps', () => {
    const result = done(
      add(yamlShorthandSteps, { step: { name: 'four', run: 'x' } }),
      yamlShorthandSteps
    );
    expect(result.select).toBe('steps[3]');
    expect(result.text).toBe(
      `${yamlShorthandSteps}  - name: four\n    run: x\n`
    );
  });

  it('names an unnamed upstream step with the server name', () => {
    const text = 'steps:\n  - name: a\n    run: a\n  - run: b\n';
    const result = done(
      add(
        text,
        { step: { name: 'c', run: 'c' }, dependsOn: ['steps[1]'] },
        NAMED
      ),
      text
    );
    expect(result.text).toBe(
      'steps:\n  - name: a\n    run: a\n  - name: cmd_2\n    run: b\n  - name: c\n    depends: [cmd_2]\n    run: c\n'
    );
  });

  it('refuses an unnamed upstream step without a server name', () => {
    const text = 'steps:\n  - name: a\n    run: a\n  - run: b\n';
    expect(
      failed(add(text, { step: { run: 'c' }, dependsOn: ['steps[1]'] })).code
    ).toBe('invalidName');
    expect(
      failed(
        add(
          text,
          { step: { run: 'c' }, dependsOn: ['steps[1]'] },
          { names: { byKey: {}, byName: {} } }
        )
      ).code
    ).toBe('invalidName');
  });

  it('uses the id of an upstream step that has no name', () => {
    const text = 'steps:\n  - id: first\n    run: a\n';
    const result = done(
      add(text, { step: { name: 'c', run: 'c' }, dependsOn: ['steps[0]'] }),
      text
    );
    expect(result.text).toBe(
      `${text}  - name: c\n    depends: [first]\n    run: c\n`
    );
  });

  it('refuses depends in a chain DAG and adds without it', () => {
    expect(
      failed(add(yamlChain, { step: { run: 'x' }, dependsOn: ['steps[0]'] }))
        .code
    ).toBe('chainType');
    const result = done(
      add(yamlChain, { step: { name: 'verify', run: 'x' }, after: 'steps[1]' }),
      yamlChain
    );
    expect(result.text).toBe(
      yamlChain.replace(
        '  - name: deploy',
        '  - name: verify\n    run: x\n  - name: deploy'
      )
    );
  });

  // The steps of an agent DAG are the actions that the agent chooses from.
  it('adds a step to an agent DAG at the end', () => {
    const result = done(add(AGENT_DAG, { step: { run: 'x' } }), AGENT_DAG);
    expect(result.text.startsWith(AGENT_DAG.trimEnd())).toBe(true);
    expect(result.text).toMatch(/- name: \w+\n\s+run: x\n$/);
    expect(result.select).toBe('steps[1]');
  });

  it('refuses an add in an agent DAG that depends on a step', () => {
    expect(
      failed(add(AGENT_DAG, { step: { run: 'x' }, dependsOn: ['steps[0]'] }))
        .code
    ).toBe('agentType');
    expect(
      failed(
        add(AGENT_DAG, {
          step: { run: 'x' },
          insertBetween: { from: 'steps[0]', to: 'steps[0]' },
        })
      ).code
    ).toBe('agentType');
  });

  it('gives parseError for broken YAML', () => {
    expect(failed(add('steps: [a\n', { step: { run: 'x' } })).code).toBe(
      'parseError'
    );
  });
});

describe('opAddStep between two steps', () => {
  const between = (
    text: string,
    from: string,
    to: string,
    step: Op<'addStep'>['step'] = { name: 'mid', run: 'echo mid' },
    options: ApplyEditOptions = {},
    extra: Partial<Op<'addStep'>> = {}
  ) =>
    opAddStep(
      text,
      { kind: 'addStep', step, insertBetween: { from, to }, ...extra },
      options
    );

  it('rewires the array form', () => {
    const result = done(
      between(yamlArraySteps, 'steps[0]', 'steps[1]'),
      yamlArraySteps,
      2
    );
    expect(result.text).toBe(
      yamlArraySteps
        .replace(
          '\n  - name: transform',
          '\n  - name: mid\n    depends: [fetch]\n    run: echo mid\n\n  - name: transform'
        )
        // The patch engine re-emits the line next to the new entry: the
        // spaces before its inline comment become one space.
        .replace(
          'name: transform   # keep the header row\n    depends: [fetch]',
          'name: transform # keep the header row\n    depends: [mid]'
        )
    );
    expect(result.select).toBe('steps[1]');
  });

  it('rewires a block list', () => {
    const result = done(
      between(yamlArraySteps, 'steps[1]', 'steps[2]'),
      yamlArraySteps,
      2
    );
    expect(result.text).toBe(
      `${yamlArraySteps.replace('    depends:\n      - transform\n', '    depends:\n      - mid\n')}`.replace(
        '\n  # Last step.',
        '\n  - name: mid\n    depends: [transform]\n    run: echo mid\n\n  # Last step.'
      )
    );
    expect(result.select).toBe('steps[2]');
  });

  it('rewires the map form', () => {
    const result = done(
      between(yamlMapSteps, 'steps[0]', 'steps[1]'),
      yamlMapSteps,
      2
    );
    expect(result.text).toBe(`name: map-form
steps:
  # Entry point.
  fetch:
    run: curl -sSf https://example.com/data.csv -o data.csv

  mid:
    depends: [fetch]
    run: echo mid

  parse:
    depends: [mid]
    run: wc -l data.csv
`);
    expect(result.select).toBe('steps[1]');
  });

  it('keeps the other entries of the depends of the target', () => {
    const text = `steps:
  - name: a
    run: a
  - name: b
    run: b
  - name: c
    depends: [b, a, x]
    run: c
`;
    const result = done(between(text, 'steps[0]', 'steps[2]'), text, 2);
    expect(result.text).toBe(`steps:
  - name: a
    run: a
  - name: mid
    depends: [a]
    run: echo mid
  - name: b
    run: b
  - name: c
    depends: [b, mid, x]
    run: c
`);
  });

  it('keeps the comments of a commented file', () => {
    const result = done(
      between(COMMENTED, 'steps[0]', 'steps[1]'),
      COMMENTED,
      2
    );
    expect(result.text).toBe(`# top
name: c
steps:
  # first
  - name: a   # trailing
    run: echo a

  - name: mid
    depends: [a]
    run: echo mid

  # second
  - name: b
    depends: [mid]
    run: echo b
  # tail comment
`);
  });

  it('matches the target by the id of the source', () => {
    const text = `steps:
  - name: a
    id: aid
    run: a
  - name: b
    depends: aid
    run: b
`;
    const result = done(between(text, 'steps[0]', 'steps[1]'), text, 2);
    expect(result.text).toBe(`steps:
  - name: a
    id: aid
    run: a
  - name: mid
    depends: a
    run: echo mid
  - name: b
    depends: mid
    run: b
`);
  });

  // The edge comes from a reference, as b reading ${steps.a.outputs.x}.
  it('makes a target that does not list the source wait for the step', () => {
    const text = 'steps:\n  - name: a\n    run: a\n  - name: b\n    run: b\n';
    const result = done(between(text, 'steps[0]', 'steps[1]'), text);
    expect(result.text).toBe(
      'steps:\n  - name: a\n    run: a\n  - name: mid\n    depends: [a]\n    run: echo mid\n  - name: b\n    depends: [mid]\n    run: b\n'
    );
  });

  it('adds the extra dependencies after the source', () => {
    const result = done(
      between(
        yamlArraySteps,
        'steps[1]',
        'steps[2]',
        { name: 'mid', run: 'x' },
        {},
        { dependsOn: ['steps[0]', 'steps[1]'] }
      ),
      yamlArraySteps,
      2
    );
    expect(result.text).toContain(
      '  - name: mid\n    depends: [transform, fetch]\n    run: x\n'
    );
  });

  it('only positions the step in a chain DAG', () => {
    const result = done(between(yamlChain, 'steps[0]', 'steps[1]'), yamlChain);
    expect(result.text).toBe(
      yamlChain.replace(
        '  - name: test',
        '  - name: mid\n    run: echo mid\n  - name: test'
      )
    );
    expect(result.select).toBe('steps[1]');
  });

  it('names an unnamed source with the server name', () => {
    const text =
      'steps:\n  - name: a\n    run: a\n  - run: b\n  - name: c\n    depends: [cmd_2]\n    run: c\n';
    const result = done(
      between(text, 'steps[1]', 'steps[2]', { name: 'mid', run: 'x' }, NAMED),
      text,
      3
    );
    expect(result.text).toBe(
      'steps:\n  - name: a\n    run: a\n  - name: cmd_2\n    run: b\n  - name: mid\n    depends: [cmd_2]\n    run: x\n  - name: c\n    depends: [mid]\n    run: c\n'
    );
  });

  it('refuses an unnamed source without a server name', () => {
    const text = 'steps:\n  - run: b\n  - name: c\n    run: c\n';
    expect(failed(between(text, 'steps[0]', 'steps[1]')).code).toBe(
      'invalidName'
    );
  });

  it('gives unknownStep and unsupportedForm for bad targets', () => {
    expect(failed(between(yamlArraySteps, 'steps[0]', 'steps[7]')).code).toBe(
      'unknownStep'
    );
    expect(failed(between(yamlArraySteps, 'steps[7]', 'steps[0]')).code).toBe(
      'unknownStep'
    );
    expect(
      failed(between(yamlShorthandSteps, 'steps[2]', 'steps[0]')).code
    ).toBe('unsupportedForm');
    expect(
      failed(between(yamlNestedParallel, 'steps[0]', 'steps[1]')).code
    ).toBe('unsupportedForm');
  });

  it('refuses a duplicate name before any change', () => {
    expect(
      failed(between(yamlArraySteps, 'steps[0]', 'steps[1]', { name: 'fetch' }))
        .code
    ).toBe('duplicateName');
  });

  it('leaves the bytes after the separator unchanged', () => {
    const text = `${yamlMultiDoc.slice(0, yamlMultiDoc.indexOf('\n---'))}
  - name: after
    depends: [run-child]
    run: x
${yamlMultiDoc.slice(yamlMultiDoc.indexOf('\n---'))}`;
    const result = done(between(text, 'steps[0]', 'steps[1]'), text, 2);
    expect(result.text.endsWith(text.slice(text.indexOf('---')))).toBe(true);
    expect(result.text).toContain('  - name: after\n    depends: [mid]\n');
  });
});

describe('opDeleteStep', () => {
  const del = (
    text: string,
    step: string,
    rewire = false,
    options: ApplyEditOptions = {}
  ) => opDeleteStep(text, { kind: 'deleteStep', step, rewire }, options);

  it('removes an array entry and the reference to it', () => {
    const result = done(del(yamlArraySteps, 'steps[1]'), yamlArraySteps, 2);
    expect(result.text).toBe(`# Nightly import
name: nightly-import
steps:
  # Download the source file.
  - name: fetch
    run: curl -sSf https://example.com/data.csv -o data.csv

  # Last step.
  - name: publish
    run: ./publish.sh sorted.csv
`);
  });

  it('rewires the children to the parents with rewire', () => {
    const result = done(
      del(yamlArraySteps, 'steps[1]', true),
      yamlArraySteps,
      2
    );
    expect(result.text).toBe(`# Nightly import
name: nightly-import
steps:
  # Download the source file.
  - name: fetch
    run: curl -sSf https://example.com/data.csv -o data.csv

  # Last step.
  - name: publish
    depends:
      - fetch
    run: ./publish.sh sorted.csv
`);
  });

  it('removes the first array entry with its comment', () => {
    const result = done(del(yamlArraySteps, 'steps[0]'), yamlArraySteps, 2);
    expect(result.text).toBe(`# Nightly import
name: nightly-import
steps:
  - name: transform   # keep the header row
    run: |
      head -n 1 data.csv
      sort data.csv > sorted.csv

  # Last step.
  - name: publish
    depends:
      - transform
    run: ./publish.sh sorted.csv
`);
  });

  it('removes the last array entry with its comment', () => {
    const result = done(del(yamlArraySteps, 'steps[2]'), yamlArraySteps, 2);
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
`);
  });

  it('removes a map entry and the reference to it', () => {
    const result = done(del(yamlMapSteps, 'steps[0]'), yamlMapSteps, 2);
    expect(result.text).toBe(`name: map-form
steps:
  parse:
    run: wc -l data.csv
`);
  });

  it('removes the last map entry', () => {
    const result = done(del(yamlMapSteps, 'steps[1]'), yamlMapSteps, 2);
    expect(result.text).toBe(`name: map-form
steps:
  # Entry point.
  fetch:
    run: curl -sSf https://example.com/data.csv -o data.csv
`);
  });

  it('keeps the other comments of a commented file', () => {
    const result = done(del(COMMENTED, 'steps[0]', true), COMMENTED, 2);
    expect(result.text).toBe(`# top
name: c
steps:
  # second
  - name: b
    run: echo b
  # tail comment
`);
  });

  it('keeps the tail comment when the last entry goes', () => {
    const result = done(del(COMMENTED, 'steps[1]'), COMMENTED, 2);
    expect(result.text).toBe(`# top
name: c
steps:
  # first
  - name: a   # trailing
    run: echo a
  # tail comment
`);
  });

  it('writes the parents once and never the step itself', () => {
    const text = `steps:
  - name: x
    run: x
  - name: b
    depends: [x]
    run: b
  - name: c
    depends: [b, x]
    run: c
  - name: d
    depends: [c, b]
    run: d
`;
    const result = done(del(text, 'steps[1]', true), text, 3);
    expect(result.text).toBe(`steps:
  - name: x
    run: x
  - name: c
    depends: [x]
    run: c
  - name: d
    depends: [c, x]
    run: d
`);
  });

  it('matches a reference by id', () => {
    const text = `steps:
  - name: a
    id: aid
    run: a
  - name: b
    depends: aid
    run: b
`;
    const result = done(del(text, 'steps[0]'), text, 2);
    expect(result.text).toBe('steps:\n  - name: b\n    run: b\n');
  });

  it('leaves an empty list when the only step goes', () => {
    const text = 'name: x\nsteps:\n  - name: a\n    run: a\n';
    const result = done(del(text, 'steps[0]'), text, 2);
    expect(result.text).toBe('name: x\nsteps: []\n');
  });

  it('removes a handler and then the handler_on key', () => {
    const text =
      'handler_on:\n  failure:\n    run: a\n  success:\n    run: b\nsteps:\n  - name: m\n    run: m\n';
    const first = done(del(text, 'handler_on.failure'), text, 2);
    expect(first.text).toBe(
      'handler_on:\n  success:\n    run: b\nsteps:\n  - name: m\n    run: m\n'
    );
    const second = done(del(first.text, 'handler_on.success'), first.text, 2);
    expect(second.text).toBe('steps:\n  - name: m\n    run: m\n');
  });

  it('removes the handler with its comment from the fixture', () => {
    const result = done(
      del(yamlHandlers, 'handler_on.failure'),
      yamlHandlers,
      2
    );
    expect(result.text).toBe(`name: with-handlers
handler_on:
  success:
    run: echo ok
steps:
  - name: main
    run: ./job.sh
`);
  });

  it('removes a step of a chain DAG', () => {
    const result = done(del(yamlChain, 'steps[1]'), yamlChain, 2);
    expect(result.text).toBe(
      yamlChain.replace('  - name: test\n    run: make test\n', '')
    );
  });

  it('leaves the bytes after the separator unchanged', () => {
    const result = done(del(yamlMultiDoc, 'steps[0]'), yamlMultiDoc, 2);
    expect(
      result.text.endsWith(yamlMultiDoc.slice(yamlMultiDoc.indexOf('---')))
    ).toBe(true);
    expect(result.text.startsWith('name: parent\nsteps: []\n')).toBe(true);
  });

  it('gives unknownStep for a missing key', () => {
    expect(failed(del(yamlArraySteps, 'steps[8]')).code).toBe('unknownStep');
    expect(failed(del(yamlHandlers, 'handler_on.exit')).code).toBe(
      'unknownStep'
    );
  });

  it('gives unsupportedForm for a shorthand or grouped entry', () => {
    expect(failed(del(yamlShorthandSteps, 'steps[1]')).code).toBe(
      'unsupportedForm'
    );
    expect(failed(del(yamlNestedParallel, 'steps[2]')).code).toBe(
      'unsupportedForm'
    );
  });

  it('deletes a step of an agent DAG', () => {
    const result = done(del(AGENT_DAG, 'steps[0]'), AGENT_DAG);
    expect(result.text).not.toContain('name: ask');
  });
});

describe('opDeleteStep router targets', () => {
  const del = (
    text: string,
    step: string,
    rewire = false,
    options: ApplyEditOptions = {}
  ) => opDeleteStep(text, { kind: 'deleteStep', step, rewire }, options);

  it('removes a pattern whose only target goes', () => {
    const result = done(del(yamlRouter, 'steps[1]'), yamlRouter, 3);
    expect(result.text).toBe(`name: route-by-code
params:
  - CODE: "200"
steps:
  - name: pick
    action: router.route
    with:
      value: "\${params.CODE}"
      routes:
        "re:.*": [other]
  # Fallback branch.
  - name: other
    run: echo other
`);
  });

  it('removes the last pattern with its target', () => {
    const result = done(del(yamlRouter, 'steps[2]'), yamlRouter, 3);
    expect(result.text).toBe(
      yamlRouter
        .replace('        "re:.*": [other]\n', '')
        .replace(
          '  # Fallback branch.\n  - name: other\n    run: echo other\n',
          ''
        )
    );
  });

  it('drops one name from a list of targets', () => {
    const text = `steps:
  - name: pick
    action: router.route
    with:
      value: x
      routes:
        a: [one, two]
        b:
          - two
          - three
        c: two
  - name: one
    run: 1
  - name: two
    run: 2
  - name: three
    run: 3
`;
    const result = done(del(text, 'steps[2]'), text, 4);
    expect(result.text).toBe(`steps:
  - name: pick
    action: router.route
    with:
      value: x
      routes:
        a: [one]
        b:
          - three
  - name: one
    run: 1
  - name: three
    run: 3
`);
  });

  it('drops the target and rewires the depends in one edit', () => {
    const text = `steps:
  - name: pick
    action: router.route
    with:
      value: x
      routes:
        a: [one]
        b: [two]
  - name: one
    depends: [pick]
    run: 1
  - name: two
    depends: [pick, one]
    run: 2
`;
    const result = done(del(text, 'steps[1]', true), text, 4);
    expect(result.text).toBe(`steps:
  - name: pick
    action: router.route
    with:
      value: x
      routes:
        b: [two]
  - name: two
    depends: [pick]
    run: 2
`);
  });

  it('deletes the router step itself', () => {
    const result = done(del(yamlRouter, 'steps[0]'), yamlRouter, 2);
    expect(result.text).toBe(`name: route-by-code
params:
  - CODE: "200"
steps:
  - name: server_error
    run: echo 5xx
  # Fallback branch.
  - name: other
    run: echo other
`);
  });
});

/** Four steps, one with a block list, one with a scalar, one with no depends. */
const GRAPH = `steps:
  - name: a
    run: a
  - name: b
    depends: a
    run: b
  - name: c
    depends:
      - a
      - b
    run: c
  - name: d
    id: did
    run: d
`;

describe('opAddDependency', () => {
  const add = (
    text: string,
    step: string,
    dependsOn: string,
    options: ApplyEditOptions = {}
  ) =>
    opAddDependency(text, { kind: 'addDependency', step, dependsOn }, options);

  // GRAPH has a scalar for one name and a block list for several.
  it('creates a scalar after the name', () => {
    const result = done(add(GRAPH, 'steps[0]', 'steps[3]'), GRAPH, 1);
    expect(result.text).toBe(
      GRAPH.replace('  - name: a\n', '  - name: a\n    depends: d\n')
    );
  });

  it('turns a scalar into a block list', () => {
    const result = done(add(GRAPH, 'steps[1]', 'steps[3]'), GRAPH, 1);
    expect(result.text).toBe(
      GRAPH.replace('depends: a\n', 'depends:\n      - a\n      - d\n')
    );
  });

  it('creates a flow list when the file uses flow lists', () => {
    const text = `${yamlArraySteps}\n  - name: lone\n    run: x\n`;
    const result = done(add(text, 'steps[3]', 'steps[2]'), text, 1);
    expect(result.text).toBe(
      text.replace(
        '  - name: lone\n',
        '  - name: lone\n    depends: [publish]\n'
      )
    );
  });

  it('adds a key to the map form', () => {
    const text = `${yamlMapSteps}\n  zeta:\n    run: z\n`;
    const result = done(add(text, 'steps[2]', 'steps[1]'), text, 1);
    expect(result.text).toBe(
      text.replace('  zeta:\n', '  zeta:\n    depends: [parse]\n')
    );
  });

  it('keeps the comments of a commented file', () => {
    const text = COMMENTED.replace(
      '  # tail comment\n',
      '\n  # third\n  - name: c\n    run: echo c\n  # tail comment\n'
    );
    const result = done(add(text, 'steps[2]', 'steps[0]'), text, 1);
    expect(result.text).toBe(
      text.replace('  - name: c\n', '  - name: c\n    depends: [a]\n')
    );
  });

  it('writes the id of an upstream step that has no name', () => {
    const text = 'steps:\n  - id: first\n    run: a\n  - name: b\n    run: b\n';
    const result = done(add(text, 'steps[1]', 'steps[0]'), text, 1);
    expect(result.text).toBe(
      'steps:\n  - id: first\n    run: a\n  - name: b\n    depends: [first]\n    run: b\n'
    );
  });

  it('names an unnamed upstream step with the server name', () => {
    const text = 'steps:\n  - name: a\n    run: a\n  - run: b\n';
    const result = done(add(text, 'steps[0]', 'steps[1]', NAMED), text, 2);
    expect(result.text).toBe(
      'steps:\n  - name: a\n    depends: [cmd_2]\n    run: a\n  - name: cmd_2\n    run: b\n'
    );
  });

  it('refuses an unnamed upstream step without a server name', () => {
    const text = 'steps:\n  - name: a\n    run: a\n  - run: b\n';
    expect(failed(add(text, 'steps[0]', 'steps[1]')).code).toBe('invalidName');
  });

  it('gives noop when the dependency exists by name or by id', () => {
    expect(failed(add(GRAPH, 'steps[1]', 'steps[0]')).code).toBe('noop');
    const text =
      'steps:\n  - name: a\n    id: aid\n    run: a\n  - name: b\n    depends: aid\n    run: b\n';
    expect(failed(add(text, 'steps[1]', 'steps[0]')).code).toBe('noop');
  });

  it('gives unknownStep for a missing step', () => {
    expect(failed(add(GRAPH, 'steps[9]', 'steps[0]')).code).toBe('unknownStep');
    expect(failed(add(GRAPH, 'steps[0]', 'steps[9]')).code).toBe('unknownStep');
  });

  it('gives unsupportedForm for a shorthand, grouped or handler step', () => {
    expect(failed(add(yamlShorthandSteps, 'steps[2]', 'steps[0]')).code).toBe(
      'unsupportedForm'
    );
    expect(failed(add(yamlShorthandSteps, 'steps[0]', 'steps[2]')).code).toBe(
      'unsupportedForm'
    );
    expect(failed(add(yamlNestedParallel, 'steps[3]', 'steps[1]')).code).toBe(
      'unsupportedForm'
    );
    expect(
      failed(add(yamlHandlers, 'handler_on.failure', 'steps[0]')).code
    ).toBe('unsupportedForm');
  });

  it('gives chainType for a chain DAG', () => {
    expect(failed(add(yamlChain, 'steps[2]', 'steps[0]')).code).toBe(
      'chainType'
    );
  });

  it('gives agentType for an agent DAG', () => {
    expect(failed(add(AGENT_DAG, 'steps[0]', 'steps[0]')).code).toBe(
      'agentType'
    );
  });

  it('leaves the bytes after the separator unchanged', () => {
    const text = yamlMultiDoc.replace(
      '\n---\n',
      '  - name: second\n    run: x\n---\n'
    );
    const result = done(add(text, 'steps[1]', 'steps[0]'), text, 1);
    expect(result.text.endsWith(text.slice(text.indexOf('---')))).toBe(true);
    expect(result.text).toContain(
      '  - name: second\n    depends: [run-child]\n'
    );
  });
});

describe('opRemoveDependency', () => {
  const remove = (
    text: string,
    step: string,
    dependsOn: string,
    options: ApplyEditOptions = {}
  ) =>
    opRemoveDependency(
      text,
      { kind: 'removeDependency', step, dependsOn },
      options
    );

  it('removes a scalar depends key', () => {
    const result = done(remove(GRAPH, 'steps[1]', 'steps[0]'), GRAPH, 1);
    expect(result.text).toBe(GRAPH.replace('    depends: a\n', ''));
  });

  it('removes an item of a block list', () => {
    const result = done(remove(GRAPH, 'steps[2]', 'steps[0]'), GRAPH, 1);
    expect(result.text).toBe(
      GRAPH.replace('      - a\n      - b\n', '      - b\n')
    );
  });

  it('removes the key with the last item of a block list', () => {
    const text = remove(GRAPH, 'steps[2]', 'steps[0]');
    if (!text.ok) throw new Error('failed');
    const result = done(
      remove(text.text, 'steps[2]', 'steps[1]'),
      text.text,
      1
    );
    expect(result.text).toBe(
      GRAPH.replace('    depends:\n      - a\n      - b\n', '')
    );
  });

  it('removes an item of a flow list', () => {
    const result = done(
      remove(yamlArraySteps, 'steps[1]', 'steps[0]'),
      yamlArraySteps,
      1
    );
    expect(result.text).toBe(
      yamlArraySteps.replace('    depends: [fetch]\n', '')
    );
  });

  it('removes a key of the map form', () => {
    const result = done(
      remove(yamlMapSteps, 'steps[1]', 'steps[0]'),
      yamlMapSteps,
      1
    );
    expect(result.text).toBe(
      yamlMapSteps.replace('    depends: [fetch]\n', '')
    );
  });

  it('keeps the comments of a commented file', () => {
    const result = done(
      remove(COMMENTED, 'steps[1]', 'steps[0]'),
      COMMENTED,
      1
    );
    expect(result.text).toBe(COMMENTED.replace('    depends: [a]\n', ''));
  });

  it('matches the reference by id', () => {
    const text =
      'steps:\n  - name: a\n    id: aid\n    run: a\n  - name: b\n    depends: [aid, x]\n    run: b\n';
    const result = done(remove(text, 'steps[1]', 'steps[0]'), text, 1);
    expect(result.text).toBe(
      'steps:\n  - name: a\n    id: aid\n    run: a\n  - name: b\n    depends: [x]\n    run: b\n'
    );
  });

  it('matches the reference by server name', () => {
    const text =
      'steps:\n  - name: a\n    depends: [cmd_2]\n    run: a\n  - run: b\n';
    const result = done(remove(text, 'steps[0]', 'steps[1]', NAMED), text, 1);
    expect(result.text).toBe('steps:\n  - name: a\n    run: a\n  - run: b\n');
  });

  it('gives noop when the dependency is absent', () => {
    expect(failed(remove(GRAPH, 'steps[0]', 'steps[1]')).code).toBe('noop');
    expect(failed(remove(GRAPH, 'steps[3]', 'steps[0]')).code).toBe('noop');
  });

  it('gives unknownStep for a missing step', () => {
    expect(failed(remove(GRAPH, 'steps[9]', 'steps[0]')).code).toBe(
      'unknownStep'
    );
    expect(failed(remove(GRAPH, 'steps[1]', 'steps[9]')).code).toBe(
      'unknownStep'
    );
  });

  it('gives unsupportedForm for a shorthand step', () => {
    expect(
      failed(remove(yamlShorthandSteps, 'steps[2]', 'steps[0]')).code
    ).toBe('unsupportedForm');
  });

  it('gives chainType and agentType', () => {
    expect(failed(remove(yamlChain, 'steps[2]', 'steps[0]')).code).toBe(
      'chainType'
    );
    expect(failed(remove(AGENT_DAG, 'steps[0]', 'steps[0]')).code).toBe(
      'agentType'
    );
  });

  it('leaves the bytes after the separator unchanged', () => {
    const text = yamlMultiDoc.replace(
      '\n---\n',
      '  - name: second\n    depends: [run-child]\n    run: x\n---\n'
    );
    const result = done(remove(text, 'steps[1]', 'steps[0]'), text, 1);
    expect(result.text.endsWith(text.slice(text.indexOf('---')))).toBe(true);
    expect(result.text).not.toContain('depends: [run-child]');
  });
});

describe('dependency cycles', () => {
  const add = (text: string, step: string, dependsOn: string) =>
    opAddDependency(text, { kind: 'addDependency', step, dependsOn }, {});

  const CHAIN3 = `steps:
  - name: a
    run: a
  - name: b
    depends: [a]
    run: b
  - name: c
    depends: [b]
    run: c
`;

  it('refuses a step that depends on itself', () => {
    expect(failed(add(GRAPH, 'steps[3]', 'steps[3]')).code).toBe('cycle');
  });

  it('refuses a direct loop', () => {
    expect(failed(add(CHAIN3, 'steps[0]', 'steps[1]')).code).toBe('cycle');
  });

  it('refuses a loop through several steps', () => {
    expect(failed(add(CHAIN3, 'steps[0]', 'steps[2]')).code).toBe('cycle');
  });

  it('follows references by id', () => {
    const text =
      'steps:\n  - name: a\n    id: aid\n    run: a\n  - name: b\n    depends: aid\n    run: b\n';
    expect(failed(add(text, 'steps[0]', 'steps[1]')).code).toBe('cycle');
  });

  // The server orders a step after each step whose outputs it reads by id.
  it('follows a step-output reference', () => {
    const text =
      'steps:\n  - name: a\n    id: a\n    run: a\n  - name: b\n    run: echo ${steps.a.outputs.x}\n';
    expect(failed(add(text, 'steps[0]', 'steps[1]')).code).toBe('cycle');
    const described = text.replace(
      'run: echo ${steps.a.outputs.x}',
      'description: reads ${steps.a.outputs.x}\n    run: b'
    );
    expect(
      done(add(described, 'steps[0]', 'steps[1]'), described, 1).text
    ).toContain('depends: [b]');
  });

  it('follows the targets of a router', () => {
    const text = `steps:
  - name: pick
    action: router.route
    with:
      value: x
      routes:
        a: [one]
  - name: one
    run: 1
  - name: two
    depends: [one]
    run: 2
`;
    expect(failed(add(text, 'steps[0]', 'steps[1]')).code).toBe('cycle');
    expect(failed(add(text, 'steps[0]', 'steps[2]')).code).toBe('cycle');
  });

  it('allows a second parent and ignores unknown references', () => {
    const text =
      'steps:\n  - name: a\n    depends: [ghost]\n    run: a\n  - name: b\n    run: b\n';
    expect(done(add(text, 'steps[1]', 'steps[0]'), text, 1).text).toContain(
      'depends: [a]'
    );
    expect(done(add(CHAIN3, 'steps[2]', 'steps[0]'), CHAIN3, 1).text).toContain(
      'depends: [b, a]'
    );
  });

  it('refuses to insert a step between a step and itself', () => {
    const result = opAddStep(
      CHAIN3,
      {
        kind: 'addStep',
        step: { run: 'x' },
        insertBetween: { from: 'steps[1]', to: 'steps[1]' },
      },
      {}
    );
    expect(failed(result).code).toBe('cycle');
  });

  it('refuses an extra parent that is downstream of the target', () => {
    const result = opAddStep(
      CHAIN3,
      {
        kind: 'addStep',
        step: { name: 'mid', run: 'x' },
        insertBetween: { from: 'steps[0]', to: 'steps[1]' },
        dependsOn: ['steps[2]'],
      },
      {}
    );
    expect(failed(result).code).toBe('cycle');
  });

  it('allows an extra parent that is upstream of the target', () => {
    const text = `${CHAIN3}  - name: d
    depends: [c]
    run: d
`;
    const result = opAddStep(
      text,
      {
        kind: 'addStep',
        step: { name: 'mid', run: 'x' },
        insertBetween: { from: 'steps[2]', to: 'steps[3]' },
        dependsOn: ['steps[0]'],
      },
      {}
    );
    expect(done(result, text, 2).text).toContain(
      '  - name: mid\n    depends: [c, a]\n'
    );
  });
});

describe('opSetRouteTargets', () => {
  const set = (
    text: string,
    step: string,
    pattern: string,
    targets: string[],
    options: ApplyEditOptions = {}
  ) =>
    opSetRouteTargets(
      text,
      { kind: 'setRouteTargets', step, pattern, targets },
      options
    );

  const FIVE = '"re:^5\\\\d\\\\d$"';

  it('sets the targets of a quoted pattern in place', () => {
    const result = done(
      set(yamlRouter, 'steps[0]', 're:^5\\d\\d$', ['server_error', 'other']),
      yamlRouter,
      1
    );
    expect(result.text).toBe(
      yamlRouter.replace(
        `${FIVE}: [server_error]`,
        `${FIVE}: [server_error, other]`
      )
    );
  });

  it('replaces the targets of a pattern', () => {
    const result = done(
      set(yamlRouter, 'steps[0]', 're:.*', ['server_error']),
      yamlRouter,
      1
    );
    expect(result.text).toBe(
      yamlRouter.replace('"re:.*": [other]', '"re:.*": [server_error]')
    );
  });

  it('appends a new pattern in the style of the others', () => {
    const result = done(
      set(yamlRouter, 'steps[0]', 'ok', ['other', 'server_error']),
      yamlRouter,
      1
    );
    expect(result.text).toBe(
      yamlRouter.replace(
        '        "re:.*": [other]\n',
        '        "re:.*": [other]\n        ok: [other, server_error]\n'
      )
    );
  });

  it('removes a pattern for an empty list', () => {
    const result = done(
      set(yamlRouter, 'steps[0]', 're:.*', []),
      yamlRouter,
      1
    );
    expect(result.text).toBe(
      yamlRouter.replace('        "re:.*": [other]\n', '')
    );
  });

  it('keeps the block style of the lists', () => {
    const text = `steps:
  - name: pick
    action: router.route
    with:
      value: x
      routes:
        a:
          - one
        b:
          - two
  - name: one
    run: 1
  - name: two
    run: 2
`;
    const added = done(set(text, 'steps[0]', 'c', ['one', 'two']), text, 1);
    expect(added.text).toBe(
      text.replace(
        '          - two\n  - name: one',
        '          - two\n        c:\n          - one\n          - two\n  - name: one'
      )
    );
    const changed = done(set(text, 'steps[0]', 'a', ['two', 'one']), text, 1);
    expect(changed.text).toBe(
      text.replace(
        '        a:\n          - one\n',
        '        a:\n          - two\n          - one\n'
      )
    );
  });

  it('keeps a scalar target in place', () => {
    const text = `steps:
  - name: pick
    action: router.route
    with:
      value: x
      routes:
        a: one   # first
  - name: one
    run: 1
  - name: two
    run: 2
`;
    const one = done(set(text, 'steps[0]', 'a', ['two']), text, 1);
    expect(one.text).toBe(text.replace('a: one', 'a: two'));
    const many = done(set(text, 'steps[0]', 'a', ['one', 'two']), text, 1);
    expect(many.text).toBe(text.replace('a: one', 'a: [one, two]'));
  });

  it('works in the map form and by id', () => {
    const text = `steps:
  pick:
    action: router.route
    with:
      value: x
      routes:
        a: [one]
  one:
    run: 1
  two:
    id: twoid
    run: 2
`;
    const result = done(set(text, 'steps[1]', 'b', ['twoid']), text, 1);
    expect(result.text).toBe(
      text.replace('        a: [one]\n', '        a: [one]\n        b: [two]\n')
    );
  });

  it('keeps the comments of a commented file', () => {
    const text = `# Router
steps:
  # Choose.
  - name: pick
    action: router.route
    with:
      value: x
      routes:
        # Main route.
        a: [one]   # one

        # Other route.
        b: [two]
  - name: one
    run: 1
  - name: two
    run: 2
`;
    const result = done(set(text, 'steps[0]', 'b', ['one', 'two']), text, 2);
    expect(result.text).toBe(text.replace('b: [two]', 'b: [one, two]'));
  });

  it('creates the routes of a router that has none', () => {
    const text =
      'steps:\n  - name: pick\n    action: router.route\n    with:\n      value: x\n  - name: one\n    run: 1\n';
    const result = done(set(text, 'steps[0]', 'a', ['one']), text, 1);
    expect(result.text).toBe(
      'steps:\n  - name: pick\n    action: router.route\n    with:\n      value: x\n      routes:\n        a: [one]\n  - name: one\n    run: 1\n'
    );
  });

  it('gives noop for the same targets and for a missing pattern', () => {
    expect(failed(set(yamlRouter, 'steps[0]', 're:.*', ['other'])).code).toBe(
      'noop'
    );
    expect(failed(set(yamlRouter, 'steps[0]', 'none', [])).code).toBe('noop');
  });

  it('refuses a target that is not a step', () => {
    expect(failed(set(yamlRouter, 'steps[0]', 'ok', ['ghost'])).code).toBe(
      'unknownStep'
    );
  });

  it('refuses a step that is not a router', () => {
    expect(failed(set(yamlRouter, 'steps[1]', 'ok', ['other'])).code).toBe(
      'unsupportedForm'
    );
    expect(
      failed(set(yamlShorthandSteps, 'steps[0]', 'ok', ['three'])).code
    ).toBe('unsupportedForm');
    expect(failed(set(yamlRouter, 'steps[9]', 'ok', ['other'])).code).toBe(
      'unknownStep'
    );
  });

  it('refuses a target that makes a cycle', () => {
    expect(failed(set(yamlRouter, 'steps[0]', 'ok', ['pick'])).code).toBe(
      'cycle'
    );
    const text = `${yamlRouter}  - name: after
    depends: [other]
    run: x
`;
    const back = `steps:
  - name: first
    run: a
  - name: pick
    depends: [first]
    action: router.route
    with:
      value: x
      routes:
        a: [one]
  - name: one
    run: 1
`;
    expect(failed(set(back, 'steps[1]', 'b', ['first'])).code).toBe('cycle');
    expect(
      done(set(text, 'steps[0]', 'ok', ['after']), text, 1).text
    ).toContain('ok: [after]');
  });

  it('gives agentType for an agent DAG', () => {
    expect(failed(set(AGENT_DAG, 'steps[0]', 'a', ['ask'])).code).toBe(
      'agentType'
    );
  });

  it('leaves the bytes after the separator unchanged', () => {
    const text = `${yamlRouter}---
name: child
steps:
  - name: work
    run: echo work
`;
    const result = done(set(text, 'steps[0]', 'ok', ['other']), text, 1);
    expect(result.text.endsWith(text.slice(text.indexOf('---')))).toBe(true);
    expect(result.text).toContain('ok: [other]');
  });
});
