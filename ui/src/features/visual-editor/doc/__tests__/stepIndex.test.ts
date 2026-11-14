// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { isMap, isSeq, parseDocument } from 'yaml';
import {
  yamlArraySteps,
  yamlChain,
  yamlHandlers,
  yamlHttpJq,
  yamlMapSteps,
  yamlNestedParallel,
  yamlRouter,
  yamlShorthandSteps,
} from '../../testing/fixtures';
import {
  explicitNameOf,
  findStep,
  getDagType,
  getStepsNode,
  idOf,
  indexSteps,
  isAliasStep,
} from '../stepIndex';

const table = (text: string) =>
  indexSteps(parseDocument(text)).steps.map((s) => [
    s.key,
    s.index,
    s.loc.form,
    explicitNameOf(s),
  ]);

describe('getStepsNode', () => {
  it('returns the steps seq', () => {
    expect(isSeq(getStepsNode(parseDocument(yamlArraySteps)))).toBe(true);
  });

  it.each([
    ['empty text', ''],
    ['no steps key', 'name: a\n'],
    ['scalar steps', 'steps: abc\n'],
    ['top-level seq', '- a\n- b\n'],
  ])('returns null for %s', (_n, text) => {
    expect(getStepsNode(parseDocument(text))).toBeNull();
  });

  it('returns a map for map form', () => {
    expect(
      isMap(getStepsNode(parseDocument('steps:\n  a:\n    run: x\n')))
    ).toBe(true);
  });
});

describe('getDagType', () => {
  it.each([
    ['', 'graph'],
    ['name: a\n', 'graph'],
    ['type: graph\n', 'graph'],
    ['type: chain\n', 'chain'],
    ['type: "  chain  "\n', 'chain'],
    ['type: agent\n', 'agent'],
    ['type: controller\n', 'agent'],
    ['type: build\n', 'build'],
    ['type: other\n', 'graph'],
    ['type: 5\n', 'graph'],
    ['- type: chain\n', 'graph'],
  ])('%j gives %s', (text, expected) => {
    expect(getDagType(parseDocument(text))).toBe(expected);
  });

  it('reads the type of yamlChain', () => {
    expect(getDagType(parseDocument(yamlChain))).toBe('chain');
  });
});

describe('array form', () => {
  it('numbers block entries in file order', () => {
    expect(table(yamlArraySteps)).toEqual([
      ['steps[0]', 0, 'block', 'fetch'],
      ['steps[1]', 1, 'block', 'transform'],
      ['steps[2]', 2, 'block', 'publish'],
    ]);
  });

  it('numbers the chain steps', () => {
    expect(table(yamlChain)).toEqual([
      ['steps[0]', 0, 'block', 'build'],
      ['steps[1]', 1, 'block', 'test'],
      ['steps[2]', 2, 'block', 'deploy'],
    ]);
  });

  it('keeps the locator of an entry', () => {
    const doc = parseDocument(yamlRouter);
    const step = findStep(doc, 'steps[2]');
    expect(step?.loc).toMatchObject({ form: 'block', seqIndex: 2 });
    const steps = getStepsNode(doc);
    expect(step?.node).toBe(isSeq(steps) ? steps.items[2] : null);
  });

  it('gives no step for a missing steps key', () => {
    expect(indexSteps(parseDocument('name: a\n'))).toEqual({
      steps: [],
      handlers: [],
    });
  });

  it('returns null for an unknown key', () => {
    expect(findStep(parseDocument(yamlArraySteps), 'steps[3]')).toBeNull();
    expect(findStep(parseDocument(yamlArraySteps), 'nope')).toBeNull();
  });
});

// Go: `buildStepsFromArray` and `stepGroupFromRaw` (internal/spec/dag.go) add
// a nested array at its position, item by item; `normalizeStepData`
// (internal/spec/builder.go) makes a step of each string.
describe('shorthand and parallel groups', () => {
  it('numbers shorthand strings', () => {
    expect(table(yamlShorthandSteps)).toEqual([
      ['steps[0]', 0, 'shorthand', undefined],
      ['steps[1]', 1, 'shorthand', undefined],
      ['steps[2]', 2, 'block', 'three'],
    ]);
  });

  it('flattens a nested group in place', () => {
    expect(table(yamlNestedParallel)).toEqual([
      ['steps[0]', 0, 'block', 'prepare'],
      ['steps[1]', 1, 'parallelGroup', 'lint'],
      ['steps[2]', 2, 'parallelGroup', 'unit'],
      ['steps[3]', 3, 'block', 'report'],
    ]);
  });

  it('gives the group and entry position in the locator', () => {
    const doc = parseDocument(yamlNestedParallel);
    expect(findStep(doc, 'steps[2]')?.loc).toMatchObject({
      form: 'parallelGroup',
      seqIndex: 1,
      groupIndex: 1,
      groupNumber: 0,
    });
    expect(findStep(doc, 'steps[3]')?.loc).toMatchObject({
      form: 'block',
      seqIndex: 2,
    });
  });

  it('numbers groups among the groups of the file', () => {
    const text = [
      'steps:',
      '  - - run: a',
      '    - run: b',
      '  - run: c',
      '  - - run: d',
      '  - - run: e',
      '    - run: f',
      '',
    ].join('\n');
    const groups = indexSteps(parseDocument(text)).steps.map((s) =>
      s.loc.form === 'parallelGroup' ? s.loc.groupNumber : null
    );
    expect(groups).toEqual([0, 0, null, 1, 2, 2]);
  });

  it('accepts a string inside a group', () => {
    const text =
      'steps:\n  - - echo a\n    - name: b\n      run: x\n  - echo c\n';
    expect(table(text)).toEqual([
      ['steps[0]', 0, 'parallelGroup', undefined],
      ['steps[1]', 1, 'parallelGroup', 'b'],
      ['steps[2]', 2, 'shorthand', undefined],
    ]);
  });

  it('adds no step for an empty group, but numbers it', () => {
    const text = 'steps:\n  - []\n  - - run: a\n  - run: b\n';
    const steps = indexSteps(parseDocument(text)).steps;
    expect(steps.map((s) => s.key)).toEqual(['steps[0]', 'steps[1]']);
    expect(steps[0]?.loc).toMatchObject({ groupNumber: 1, seqIndex: 1 });
  });

  it('skips entries that are not steps', () => {
    const text = 'steps:\n  - 5\n  - run: a\n  - null\n  - - 7\n    - run: b\n';
    expect(table(text)).toEqual([
      ['steps[0]', 0, 'block', undefined],
      ['steps[1]', 1, 'parallelGroup', undefined],
    ]);
  });

  it('mixes forms in one file', () => {
    const text = [
      'steps:',
      '  - echo start',
      '  - - name: a',
      '      run: x',
      '    - echo b',
      '  - name: end',
      '    run: y',
      '',
    ].join('\n');
    expect(table(text)).toEqual([
      ['steps[0]', 0, 'shorthand', undefined],
      ['steps[1]', 1, 'parallelGroup', 'a'],
      ['steps[2]', 2, 'parallelGroup', undefined],
      ['steps[3]', 3, 'block', 'end'],
    ]);
  });
});

// Go: `buildStepsFromMap` (internal/spec/dag.go) sorts the steps of a map by
// name with `strings.Compare`, so the keys are numbered in UTF-8 byte order.
describe('map form', () => {
  it('numbers entries of the fixture', () => {
    expect(table(yamlMapSteps)).toEqual([
      ['steps[0]', 0, 'mapEntry', 'fetch'],
      ['steps[1]', 1, 'mapEntry', 'parse'],
    ]);
  });

  it('sorts keys that are not sorted in the file', () => {
    const text =
      'steps:\n  zeta:\n    run: z\n  alpha:\n    run: a\n  mid:\n    run: m\n';
    expect(table(text)).toEqual([
      ['steps[0]', 0, 'mapEntry', 'alpha'],
      ['steps[1]', 1, 'mapEntry', 'mid'],
      ['steps[2]', 2, 'mapEntry', 'zeta'],
    ]);
  });

  it('sorts upper case before lower case', () => {
    const text =
      'steps:\n  b:\n    run: x\n  B:\n    run: x\n  a:\n    run: x\n';
    expect(table(text).map((r) => r[3])).toEqual(['B', 'a', 'b']);
  });

  // UTF-16 order would put the emoji (D83D) before the full-width z (FF5A).
  it('sorts by UTF-8 bytes, not UTF-16 units', () => {
    const text =
      'steps:\n  "\u{1F600}":\n    run: x\n  "\uFF5A":\n    run: x\n  \u00E9:\n    run: x\n';
    expect(table(text).map((r) => r[3])).toEqual([
      '\u00E9',
      '\uFF5A',
      '\u{1F600}',
    ]);
  });

  it('puts a prefix before its longer key', () => {
    const text = 'steps:\n  ab:\n    run: x\n  a:\n    run: x\n';
    expect(table(text).map((r) => r[3])).toEqual(['a', 'ab']);
  });

  it('uses the key, not a name value', () => {
    const text = 'steps:\n  b:\n    name: zzz\n    run: x\n  a:\n    run: x\n';
    expect(table(text).map((r) => r[3])).toEqual(['a', 'b']);
  });

  it('keeps an entry without a value', () => {
    const doc = parseDocument('steps:\n  b:\n  a:\n    run: x\n');
    const steps = indexSteps(doc).steps;
    expect(steps.map((s) => [s.key, s.node === null])).toEqual([
      ['steps[0]', false],
      ['steps[1]', true],
    ]);
  });

  it('keeps pair and map in the locator', () => {
    const doc = parseDocument(yamlMapSteps);
    const step = findStep(doc, 'steps[1]')!;
    expect(step.loc.form).toBe('mapEntry');
    expect(step.loc.form === 'mapEntry' && step.loc.pair.value).toBe(step.node);
    expect(step.loc.form === 'mapEntry' && step.loc.map).toBe(
      getStepsNode(doc)
    );
  });

  it('reads the id of an entry', () => {
    const doc = parseDocument('steps:\n  a:\n    id: x1\n    run: x\n');
    expect(idOf(findStep(doc, 'steps[0]')!)).toBe('x1');
  });
});

describe('handlers', () => {
  const handlers = (text: string) =>
    indexSteps(parseDocument(text)).handlers.map((h) => [
      h.key,
      h.index,
      h.handler,
      h.node === null,
    ]);

  it('lists handlers of the fixture in file order', () => {
    expect(handlers(yamlHandlers)).toEqual([
      ['handler_on.success', null, 'success', false],
      ['handler_on.failure', null, 'failure', false],
    ]);
  });

  it('keeps the steps apart from the handlers', () => {
    const { steps, handlers } = indexSteps(parseDocument(yamlHandlers));
    expect(steps.map((s) => s.key)).toEqual(['steps[0]']);
    expect(handlers[1]?.loc).toMatchObject({
      form: 'handler',
      event: 'failure',
    });
  });

  it('knows the six events and ignores other keys', () => {
    const text = [
      'handler_on:',
      '  wait:',
      '    run: a',
      '  other:',
      '    run: b',
      '  exit:',
      '    run: c',
      '  init:',
      '    run: d',
      '  abort:',
      '    run: e',
      '',
    ].join('\n');
    expect(handlers(text).map((h) => h[0])).toEqual([
      'handler_on.wait',
      'handler_on.exit',
      'handler_on.init',
      'handler_on.abort',
    ]);
  });

  it('keeps a handler without a value', () => {
    expect(handlers('handler_on:\n  failure:\n')).toEqual([
      ['handler_on.failure', null, 'failure', true],
    ]);
  });

  it('reads name and id of a handler', () => {
    const doc = parseDocument(
      'handler_on:\n  exit:\n    name: bye\n    id: b1\n    run: x\n'
    );
    const h = findStep(doc, 'handler_on.exit')!;
    expect(explicitNameOf(h)).toBe('bye');
    expect(idOf(h)).toBe('b1');
  });

  it('gives no handler when handler_on is not a map', () => {
    expect(handlers('handler_on: abc\n')).toEqual([]);
    expect(handlers('')).toEqual([]);
  });

  it('does not number handlers as steps', () => {
    expect(findStep(parseDocument(yamlHandlers), 'steps[1]')).toBeNull();
  });
});

describe('explicitNameOf and idOf', () => {
  it('reads name and id', () => {
    const doc = parseDocument(yamlHttpJq);
    const second = findStep(doc, 'steps[1]')!;
    expect(explicitNameOf(second)).toBe('extract');
    expect(idOf(second)).toBe('names');
  });

  it('gives undefined when the key is absent', () => {
    const doc = parseDocument('steps:\n  - run: echo a\n');
    const step = findStep(doc, 'steps[0]')!;
    expect(explicitNameOf(step)).toBeUndefined();
    expect(idOf(step)).toBeUndefined();
  });

  it('reads a numeric name as text', () => {
    const doc = parseDocument('steps:\n  - name: 12\n    id: x1\n');
    const step = findStep(doc, 'steps[0]')!;
    expect(explicitNameOf(step)).toBe('12');
    expect(idOf(step)).toBe('x1');
  });
});

// The server decodes an alias to the value that it refers to, so an alias
// entry is one step and later steps keep the server numbering.
describe('alias entries', () => {
  const rows = (text: string) =>
    indexSteps(parseDocument(text)).steps.map((s) => [
      s.key,
      s.loc.form,
      isAliasStep(s),
      explicitNameOf(s),
    ]);

  it('counts an alias in the top-level list', () => {
    const text = `defs: &s
  name: a
  run: x
steps:
  - *s
  - name: b
    run: y
`;
    expect(rows(text)).toEqual([
      ['steps[0]', 'block', true, 'a'],
      ['steps[1]', 'block', false, 'b'],
    ]);
    const step = findStep(parseDocument(text), 'steps[0]')!;
    expect(step.node?.constructor.name).toBe('Alias');
    expect(isMap(step.target)).toBe(true);
  });

  it('counts an alias to a string as a shorthand step', () => {
    const text = 'cmd: &c echo hi\nsteps:\n  - *c\n  - run: y\n';
    expect(rows(text)).toEqual([
      ['steps[0]', 'block', true, undefined],
      ['steps[1]', 'block', false, undefined],
    ]);
  });

  it('counts an alias inside a group', () => {
    const text = `defs: &s
  name: a
  run: x
steps:
  - - *s
    - name: b
      run: y
  - name: c
    run: z
`;
    expect(rows(text)).toEqual([
      ['steps[0]', 'parallelGroup', true, 'a'],
      ['steps[1]', 'parallelGroup', false, 'b'],
      ['steps[2]', 'block', false, 'c'],
    ]);
  });

  it('counts an alias value in map form', () => {
    const text = `defs: &s
  run: x
steps:
  b: *s
  a:
    run: y
`;
    expect(rows(text)).toEqual([
      ['steps[0]', 'mapEntry', false, 'a'],
      ['steps[1]', 'mapEntry', true, 'b'],
    ]);
  });

  it('counts an alias under handler_on', () => {
    const text = 'defs: &s\n  run: x\nhandler_on:\n  failure: *s\n';
    const { handlers } = indexSteps(parseDocument(text));
    expect(handlers.map((h) => [h.key, isAliasStep(h)])).toEqual([
      ['handler_on.failure', true],
    ]);
  });

  it('reads name and id through the alias', () => {
    const doc = parseDocument('d: &s\n  id: i1\n  name: n\nsteps:\n  - *s\n');
    const step = findStep(doc, 'steps[0]')!;
    expect(explicitNameOf(step)).toBe('n');
    expect(idOf(step)).toBe('i1');
  });

  it('does not index an alias to a sequence', () => {
    const text = 'g: &g\n  - run: a\nsteps:\n  - *g\n  - run: b\n';
    expect(rows(text)).toEqual([['steps[0]', 'block', false, undefined]]);
  });

  it('gives false for an ordinary entry', () => {
    const doc = parseDocument(yamlArraySteps);
    expect(isAliasStep(findStep(doc, 'steps[0]')!)).toBe(false);
  });
});
