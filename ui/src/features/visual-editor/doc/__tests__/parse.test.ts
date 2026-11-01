// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { ApiDAGDetails, StepView } from '../../contracts';
import {
  detailsArraySteps,
  modelArraySteps,
  namesArraySteps,
  yamlArraySteps,
  yamlChain,
  yamlBroken,
  yamlHandlers,
  yamlMapSteps,
  yamlMultiDoc,
  yamlNestedParallel,
  yamlRouter,
  yamlHttpJq,
  yamlShorthandSteps,
  yamlUnindentedQuote,
} from '../../testing/fixtures';
import { parseDagDoc, resolveStepNames } from '../parse';

/** The text of the range of a step view. */
const slice = (text: string, v: StepView) =>
  text.slice(v.range.from, v.range.to);

/** Key, form, name, group and range text of each step. */
const summary = (text: string) =>
  parseDagDoc(text).steps.map((v) => [
    v.key,
    v.form,
    v.explicitName,
    v.parallelGroup,
    slice(text, v),
  ]);

describe('parseDagDoc fixture', () => {
  it('equals the model of the array fixture', () => {
    expect(parseDagDoc(yamlArraySteps)).toEqual(modelArraySteps);
  });
});

describe('forms', () => {
  it('map form lists steps in key order', () => {
    expect(summary(yamlMapSteps)).toEqual([
      [
        'steps[0]',
        'mapEntry',
        'fetch',
        undefined,
        'fetch:\n    run: curl -sSf https://example.com/data.csv -o data.csv',
      ],
      [
        'steps[1]',
        'mapEntry',
        'parse',
        undefined,
        'parse:\n    depends: [fetch]\n    run: wc -l data.csv',
      ],
    ]);
    const model = parseDagDoc(yamlMapSteps);
    expect(model.stepsForm).toBe('map');
    expect(model.steps[1]).toMatchObject({
      depends: ['fetch'],
      kind: 'run',
      runPreview: 'wc -l data.csv',
      value: { depends: ['fetch'], run: 'wc -l data.csv' },
      range: { startLine: 7, startColumn: 3, endLine: 9, endColumn: 24 },
    });
  });

  it('map form with keys out of order', () => {
    const text = 'steps:\n  zeta:\n    run: z\n  alpha:\n    run: a   # tail\n';
    expect(summary(text)).toEqual([
      ['steps[0]', 'mapEntry', 'alpha', undefined, 'alpha:\n    run: a'],
      ['steps[1]', 'mapEntry', 'zeta', undefined, 'zeta:\n    run: z'],
    ]);
  });

  it('shorthand strings', () => {
    expect(summary(yamlShorthandSteps)).toEqual([
      ['steps[0]', 'shorthand', undefined, undefined, 'echo one'],
      ['steps[1]', 'shorthand', undefined, undefined, 'echo two'],
      [
        'steps[2]',
        'block',
        'three',
        undefined,
        'name: three\n    run: echo three',
      ],
    ]);
    const first = parseDagDoc(yamlShorthandSteps).steps[0];
    expect(first).toEqual({
      key: 'steps[0]',
      index: 0,
      form: 'shorthand',
      kind: 'run',
      runPreview: 'echo one',
      depends: [],
      value: { run: 'echo one' },
      range: {
        from: 27,
        to: 35,
        startLine: 3,
        startColumn: 5,
        endLine: 3,
        endColumn: 13,
      },
    });
  });

  it('nested parallel group', () => {
    expect(summary(yamlNestedParallel)).toEqual([
      [
        'steps[0]',
        'block',
        'prepare',
        undefined,
        'name: prepare\n    run: ./prepare.sh',
      ],
      [
        'steps[1]',
        'parallelGroup',
        'lint',
        0,
        'name: lint\n      run: make lint',
      ],
      [
        'steps[2]',
        'parallelGroup',
        'unit',
        0,
        'name: unit\n      run: make test',
      ],
      [
        'steps[3]',
        'block',
        'report',
        undefined,
        'name: report\n    run: ./report.sh',
      ],
    ]);
  });

  it('mixed forms and group numbers', () => {
    const text = [
      'steps:',
      '  - echo start',
      '  - - name: a',
      '      run: x',
      '    - echo b',
      '  - name: mid',
      '    run: y',
      '  - - run: c',
      '',
    ].join('\n');
    expect(summary(text)).toEqual([
      ['steps[0]', 'shorthand', undefined, undefined, 'echo start'],
      ['steps[1]', 'parallelGroup', 'a', 0, 'name: a\n      run: x'],
      ['steps[2]', 'parallelGroup', undefined, 0, 'echo b'],
      ['steps[3]', 'block', 'mid', undefined, 'name: mid\n    run: y'],
      ['steps[4]', 'parallelGroup', undefined, 1, 'run: c'],
    ]);
  });

  it('chain type', () => {
    const model = parseDagDoc(yamlChain);
    expect(model.dagType).toBe('chain');
    expect(model.stepsForm).toBe('array');
    expect(model.steps.map((s) => [s.key, s.explicitName, s.depends])).toEqual([
      ['steps[0]', 'build', []],
      ['steps[1]', 'test', []],
      ['steps[2]', 'deploy', []],
    ]);
  });

  it('handlers', () => {
    const model = parseDagDoc(yamlHandlers);
    expect(model.steps.map((s) => s.key)).toEqual(['steps[0]']);
    expect(model.handlers).toEqual([
      {
        key: 'handler_on.success',
        index: null,
        handler: 'success',
        form: 'block',
        kind: 'run',
        runPreview: 'echo ok',
        depends: [],
        value: { run: 'echo ok' },
        range: {
          from: 34,
          to: 59,
          startLine: 3,
          startColumn: 3,
          endLine: 4,
          endColumn: 17,
        },
      },
      {
        key: 'handler_on.failure',
        index: null,
        handler: 'failure',
        form: 'block',
        kind: 'run',
        runPreview: './notify.sh',
        depends: [],
        value: { run: './notify.sh' },
        range: {
          from: 62,
          to: 116,
          startLine: 5,
          startColumn: 3,
          endLine: 7,
          endColumn: 21,
        },
      },
    ]);
    expect(slice(yamlHandlers, model.handlers[1]!)).toBe(
      'failure:\n    # Notify on failure.\n    run: ./notify.sh'
    );
  });

  it('no steps key', () => {
    const model = parseDagDoc('name: a\n');
    expect(model.stepsForm).toBe('none');
    expect(model.steps).toEqual([]);
    expect(model.handlers).toEqual([]);
  });
});

describe('step fields', () => {
  it('kind table', () => {
    const text = [
      'steps:',
      '  - name: r',
      '    action: router.route',
      '  - name: c1',
      '    call: child',
      '  - name: c2',
      '    action: dag.run',
      '  - name: p',
      '    parallel: [a, b]',
      '    call: child',
      '  - name: p2',
      '    parallel: [a, b]',
      '    run: echo',
      '  - name: a',
      '    action: http.request',
      '  - name: s',
      '    script: echo s',
      '  - name: cm',
      '    command: echo c',
      '  - name: o',
      '    description: nothing',
      '  - echo x',
      '',
    ].join('\n');
    expect(
      parseDagDoc(text).steps.map((s) => [s.explicitName, s.kind])
    ).toEqual([
      ['r', 'router'],
      ['c1', 'call'],
      ['c2', 'call'],
      ['p', 'call'],
      ['p2', 'parallel'],
      ['a', 'action'],
      ['s', 'run'],
      ['cm', 'run'],
      ['o', 'other'],
      [undefined, 'run'],
    ]);
  });

  it('reads router, id, description and action', () => {
    const step = parseDagDoc(yamlRouter).steps[0]!;
    expect(step).toMatchObject({
      key: 'steps[0]',
      explicitName: 'pick',
      kind: 'router',
      action: 'router.route',
    });
    expect(step.runPreview).toBeUndefined();
    const text =
      'steps:\n  - id: i1\n    name: n\n    description: Does it\n    run: x\n';
    expect(parseDagDoc(text).steps[0]).toMatchObject({
      id: 'i1',
      explicitName: 'n',
      description: 'Does it',
    });
  });

  it('depends as written', () => {
    const text = [
      'steps:',
      '  - name: a',
      '    run: x',
      '  - name: b',
      '    depends: a',
      '    run: x',
      '  - name: c',
      '    depends: [a, b]',
      '    run: x',
      '  - name: d',
      '    depends:',
      '      - c',
      '    run: x',
      '  - name: e',
      '    depends: []',
      '    run: x',
      '',
    ].join('\n');
    expect(parseDagDoc(text).steps.map((s) => s.depends)).toEqual([
      [],
      ['a'],
      ['a', 'b'],
      ['c'],
      [],
    ]);
  });

  it('run preview', () => {
    const text = [
      'steps:',
      '  - run: |',
      '      first',
      '      second',
      '  - command: [echo, a, b]',
      '  - script: |',
      '',
      '      printf x',
      '  - command: ls -l',
      '    run: pwd',
      '  - action: http.request',
      '',
    ].join('\n');
    expect(parseDagDoc(text).steps.map((s) => s.runPreview)).toEqual([
      'first',
      'echo a b',
      'printf x',
      'pwd',
      undefined,
    ]);
  });
});

describe('ranges', () => {
  it('excludes a trailing comment and keeps a block scalar tail', () => {
    const text =
      'steps:\n  - run: a   # tail\n  - run: |\n      b\n\n  - run: c\n';
    expect(summary(text).map((r) => r[4])).toEqual([
      'run: a',
      'run: |\n      b',
      'run: c',
    ]);
  });

  it('ends a kept block scalar after its last line', () => {
    const text = 'steps:\n  - run: |+\n      keep\n\n\nname: x\n';
    expect(summary(text).map((r) => r[4])).toEqual(['run: |+\n      keep']);
  });

  it('flow mapping entry', () => {
    const text = 'steps:\n  - {name: f, run: x}   # flow\n  - run: y\n';
    expect(summary(text).map((r) => r[4])).toEqual([
      '{name: f, run: x}',
      'run: y',
    ]);
  });

  it('entry that ends with an empty value', () => {
    const text = 'steps:\n  - name: a\n    depends:\n  - run: b\n';
    expect(summary(text).map((r) => r[4])).toEqual([
      'name: a\n    depends:',
      'run: b',
    ]);
  });

  it('map entry without a value', () => {
    expect(
      summary('steps:\n  b:   # none\n  a:\n    run: x\n').map((r) => r[4])
    ).toEqual(['a:\n    run: x', 'b:']);
  });

  it('handler without a value', () => {
    const text = 'handler_on:\n  failure:\nsteps:\n  - run: x\n';
    const model = parseDagDoc(text);
    expect(slice(text, model.handlers[0]!)).toBe('failure:');
    expect(model.handlers[0]!.value).toEqual({});
  });

  it('nested block collection in the last value', () => {
    const text = [
      'steps:',
      '  - name: a',
      '    with:',
      '      items:',
      '        - one   # one',
      '        - two',
      '    # after',
      '',
      '  - run: b',
      '',
    ].join('\n');
    expect(summary(text).map((r) => r[4])).toEqual([
      'name: a\n    with:\n      items:\n        - one   # one\n        - two',
      'run: b',
    ]);
  });

  it('CRLF line ends', () => {
    const text =
      'steps:\r\n  - name: a\r\n    run: |\r\n      x\r\n  - run: b # c\r\n';
    const model = parseDagDoc(text);
    expect(model.steps.map((s) => slice(text, s))).toEqual([
      'name: a\r\n    run: |\r\n      x',
      'run: b',
    ]);
    expect(model.steps[1]!.range).toMatchObject({
      startLine: 5,
      startColumn: 5,
      endLine: 5,
      endColumn: 11,
    });
  });

  it('UTF-16 offsets after non-ASCII text', () => {
    const text = [
      '# \u00e9\u00e8 \u{1F600} \u65e5\u672c',
      'steps:',
      '  # \u2713 first',
      '  - name: caf\u00e9',
      '    run: echo \u{1F600}   # ok',
      '  - name: b',
      '    run: x',
      '',
    ].join('\n');
    const [a, b] = parseDagDoc(text).steps;
    expect(a!.range.from).toBe(text.indexOf('name: caf'));
    expect(slice(text, a!)).toBe('name: caf\u00e9\n    run: echo \u{1F600}');
    // "    run: echo " is 14 units; the emoji is 2 units.
    expect(a!.range).toMatchObject({ startLine: 4, endLine: 5, endColumn: 17 });
    expect(b!.range.from).toBe(text.indexOf('name: b'));
    expect(b!.range).toMatchObject({ startLine: 6, startColumn: 5 });
    expect(a!.explicitName).toBe('caf\u00e9');
  });
});

describe('errors and documents', () => {
  it('reports the error of yamlBroken', () => {
    expect(parseDagDoc(yamlBroken)).toEqual({
      text: yamlBroken,
      ok: false,
      parseErrors: [
        {
          message:
            'Flow sequence in block collection must be sufficiently indented and end with a ]',
          line: 5,
          column: 5,
        },
      ],
      documentCount: 1,
      dagType: 'graph',
      stepsForm: 'none',
      steps: [],
      handlers: [],
    });
  });

  it('reports every error with a 1-based position', () => {
    const model = parseDagDoc('a: [b, c\n  d: 1');
    expect(model.ok).toBe(false);
    expect(model.parseErrors).toEqual([
      {
        message:
          'Implicit keys of flow sequence pairs need to be on a single line',
        line: 1,
        column: 8,
      },
      {
        message:
          'Flow sequence in block collection must be sufficiently indented and end with a ]',
        line: 2,
        column: 7,
      },
    ]);
  });

  it('keeps the type of a broken document', () => {
    const model = parseDagDoc('type: chain\nsteps:\n  - run: [a\n');
    expect(model.ok).toBe(false);
    expect(model.dagType).toBe('chain');
    expect(model.steps).toEqual([]);
  });

  it('counts the documents and reads only document 0', () => {
    const model = parseDagDoc(yamlMultiDoc);
    expect(model.ok).toBe(true);
    expect(model.documentCount).toBe(2);
    expect(
      model.steps.map((s) => [s.key, s.explicitName, s.kind, s.action])
    ).toEqual([['steps[0]', 'run-child', 'call', 'dag.run']]);
    expect(model.handlers).toEqual([]);
  });

  it('ignores an error in a later document', () => {
    const model = parseDagDoc('steps:\n  - run: a\n---\nsteps: [x\n');
    expect(model.ok).toBe(true);
    expect(model.documentCount).toBe(2);
    expect(model.steps).toHaveLength(1);
  });

  it('gives no document for an empty text', () => {
    expect(parseDagDoc('')).toEqual({
      text: '',
      ok: true,
      parseErrors: [],
      documentCount: 0,
      dagType: 'graph',
      stepsForm: 'none',
      steps: [],
      handlers: [],
    });
    expect(parseDagDoc('# only a comment\n').documentCount).toBe(0);
  });

  it('accepts a document separator without content', () => {
    const model = parseDagDoc('---\n');
    expect(model).toMatchObject({
      ok: true,
      documentCount: 1,
      stepsForm: 'none',
    });
  });

  it.each([
    ['a scalar', 'just text\n'],
    ['a sequence', '- a\n- b\n'],
    ['null', 'null\n'],
    ['steps as scalar', 'steps: abc\n'],
  ])('text that is %s has no steps', (_n, text) => {
    expect(parseDagDoc(text)).toMatchObject({
      text,
      ok: true,
      parseErrors: [],
      documentCount: 1,
      dagType: 'graph',
      stepsForm: 'none',
      steps: [],
      handlers: [],
    });
  });

  it.each([
    ['NUL', '\u0000'],
    ['BOM', '\uFEFFname: a\nsteps:\n  - run: x\n'],
    ['open braces', '{{{{'],
    ['unknown alias', 'steps:\n  - *nope\n'],
    ['tab indent', 'steps:\n\t- run: x\n'],
    ['unterminated quote', 'steps:\n  - "abc\n'],
    ['deep nesting', `${'['.repeat(5000)}${']'.repeat(5000)}`],
    ['duplicate keys', 'steps: []\nsteps: []\n'],
    ['not a string', null as unknown as string],
  ])('does not throw on %s', (_n, text) => {
    const model = parseDagDoc(text);
    expect(typeof model.ok).toBe('boolean');
    expect(model.steps).toBeInstanceOf(Array);
  });

  it('marks duplicate keys as an error', () => {
    const model = parseDagDoc('steps: []\nsteps: []\n');
    expect(model.ok).toBe(false);
    expect(model.parseErrors).toEqual([
      { message: 'Map keys must be unique', line: 2, column: 1 },
    ]);
  });
});

describe('resolveStepNames', () => {
  const server = (
    names: string[],
    extra: Partial<ApiDAGDetails> = {}
  ): ApiDAGDetails => ({
    name: 'd',
    steps: names.map((name) => ({ name })),
    ...extra,
  });

  it('maps the array fixture', () => {
    expect(resolveStepNames(modelArraySteps, detailsArraySteps)).toEqual(
      namesArraySteps
    );
  });

  it('uses the server name by index when the counts agree', () => {
    const model = parseDagDoc('steps:\n  - echo a\n  - run: b\n  - - run: c\n');
    expect(
      resolveStepNames(model, server(['cmd_1', 'cmd_2', 'step_3']))
    ).toEqual({
      byKey: { 'steps[0]': 'cmd_1', 'steps[1]': 'cmd_2', 'steps[2]': 'step_3' },
      byName: { cmd_1: 'steps[0]', cmd_2: 'steps[1]', step_3: 'steps[2]' },
    });
  });

  it('maps a map-form file in key order', () => {
    const model = parseDagDoc(
      'steps:\n  zeta:\n    run: z\n  alpha:\n    run: a\n'
    );
    expect(resolveStepNames(model, server(['alpha', 'zeta'])).byKey).toEqual({
      'steps[0]': 'alpha',
      'steps[1]': 'zeta',
    });
  });

  it('maps handlers by event', () => {
    const model = parseDagDoc(yamlHandlers);
    const names = resolveStepNames(
      model,
      server(['main'], {
        handlerOn: {
          success: { name: 'onSuccess' },
          failure: { name: 'onFailure' },
        },
      })
    );
    expect(names).toEqual({
      byKey: {
        'steps[0]': 'main',
        'handler_on.success': 'onSuccess',
        'handler_on.failure': 'onFailure',
      },
      byName: {
        main: 'steps[0]',
        onSuccess: 'handler_on.success',
        onFailure: 'handler_on.failure',
      },
    });
  });

  it('leaves a handler absent when the server has none', () => {
    const names = resolveStepNames(parseDagDoc(yamlHandlers), server(['main']));
    expect(names.byKey).toEqual({ 'steps[0]': 'main' });
  });

  it('matches by explicit name or id when the counts differ', () => {
    const model = parseDagDoc(yamlHttpJq);
    // The server lists one more step (for example an old definition).
    const details: ApiDAGDetails = {
      name: 'd',
      steps: [
        { name: 'fetch', id: 'fetch' },
        { name: 'other' },
        { name: 'renamed', id: 'names' },
      ],
    };
    expect(resolveStepNames(model, details)).toEqual({
      byKey: { 'steps[0]': 'fetch', 'steps[1]': 'renamed' },
      byName: { fetch: 'steps[0]', renamed: 'steps[1]' },
    });
  });

  it('leaves unmatched keys absent', () => {
    const model = parseDagDoc(
      'steps:\n  - name: a\n    run: x\n  - echo y\n  - name: c\n    run: z\n'
    );
    expect(resolveStepNames(model, server(['c', 'q'])).byKey).toEqual({
      'steps[2]': 'c',
    });
  });

  it('uses the explicit names without details', () => {
    const model = parseDagDoc(
      'steps:\n  - name: a\n    run: x\n  - echo y\nhandler_on:\n  exit:\n    name: bye\n    run: z\n'
    );
    expect(resolveStepNames(model)).toEqual({
      byKey: { 'steps[0]': 'a', 'handler_on.exit': 'bye' },
      byName: { a: 'steps[0]', bye: 'handler_on.exit' },
    });
  });

  it('gives an empty map for an empty model', () => {
    expect(resolveStepNames(parseDagDoc(''))).toEqual({
      byKey: {},
      byName: {},
    });
    expect(resolveStepNames(parseDagDoc(''), server([]))).toEqual({
      byKey: {},
      byName: {},
    });
  });

  it('handles a server name that is an object property', () => {
    const model = parseDagDoc('steps:\n  - name: constructor\n    run: x\n');
    expect(resolveStepNames(model, server(['constructor'])).byName).toEqual({
      constructor: 'steps[0]',
    });
  });
});

describe('alias entries', () => {
  const defs = 'defs: &s\n  name: a\n  run: echo hi\n';

  it('reads the value through the alias and ranges the token', () => {
    const text = `${defs}steps:\n  - *s   # same\n  - name: b\n    run: y\n`;
    const [first, second] = parseDagDoc(text).steps;
    expect(first).toEqual({
      key: 'steps[0]',
      index: 0,
      form: 'block',
      explicitName: 'a',
      kind: 'run',
      runPreview: 'echo hi',
      depends: [],
      value: { name: 'a', run: 'echo hi' },
      range: {
        from: 45,
        to: 47,
        startLine: 5,
        startColumn: 5,
        endLine: 5,
        endColumn: 7,
      },
    });
    expect(slice(text, first!)).toBe('*s');
    expect([second!.key, second!.explicitName]).toEqual(['steps[1]', 'b']);
  });

  it('numbers the steps after an alias in a group', () => {
    const text = `${defs}steps:\n  - - *s\n    - name: b\n      run: y\n  - name: c\n    run: z\n`;
    expect(summary(text)).toEqual([
      ['steps[0]', 'parallelGroup', 'a', 0, '*s'],
      ['steps[1]', 'parallelGroup', 'b', 0, 'name: b\n      run: y'],
      ['steps[2]', 'block', 'c', undefined, 'name: c\n    run: z'],
    ]);
  });

  it('ranges an alias value in map form', () => {
    const text = `${defs}steps:\n  b: *s   # c\n  a:\n    run: y\n`;
    const model = parseDagDoc(text);
    expect(
      model.steps.map((s) => [s.key, s.explicitName, slice(text, s)])
    ).toEqual([
      ['steps[0]', 'a', 'a:\n    run: y'],
      ['steps[1]', 'b', 'b: *s'],
    ]);
    expect(model.steps[1]!.value).toEqual({ name: 'a', run: 'echo hi' });
  });

  it('ranges an alias under handler_on', () => {
    const text = `${defs}handler_on:\n  failure: *s\nsteps:\n  - run: y\n`;
    const [handler] = parseDagDoc(text).handlers;
    expect(slice(text, handler!)).toBe('failure: *s');
    expect(handler!.value).toEqual({ name: 'a', run: 'echo hi' });
  });

  it('turns an alias to a string into a run value', () => {
    const text = 'cmd: &c echo hi\nsteps:\n  - *c\n';
    expect(parseDagDoc(text).steps[0]).toMatchObject({
      key: 'steps[0]',
      form: 'block',
      kind: 'run',
      value: { run: 'echo hi' },
    });
  });
});

// The server reads a quoted scalar up to its closing quote, whatever the
// indent of its later lines, so a file in that shape runs and must parse.
describe('quoted scalar with unindented lines', () => {
  it('reads the steps as the server does', () => {
    const model = parseDagDoc(yamlUnindentedQuote);
    expect(model.ok).toBe(true);
    expect(model.parseErrors).toEqual([]);
    expect(model.steps.map((s) => s.value.run)).toEqual([
      `echo "\${REPO}" | cut -d'/' -f1\n`,
      'echo done',
    ]);
  });

  it('ranges the steps in the text', () => {
    const model = parseDagDoc(yamlUnindentedQuote);
    expect(
      model.steps.map((s) => [
        slice(yamlUnindentedQuote, s),
        s.range.startLine,
        s.range.endLine,
      ])
    ).toEqual([
      [`id: owner\n  run: 'echo "\${REPO}" | cut -d''/'' -f1\n\n'`, 2, 5],
      ['id: notes\n  depends: [owner]\n  run: echo done', 6, 8],
    ]);
  });
});
