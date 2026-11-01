// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { isMap, parseDocument } from 'yaml';
import type { ApplyEditOptions, EditResult } from '../../contracts';
import {
  yamlArraySteps,
  yamlMapSteps,
  yamlShorthandSteps,
} from '../../testing/fixtures';
import {
  detectDependsStyle,
  readDepends,
  referenceNameFor,
  writeDepends,
} from '../ops/depends';
import type { DependsStyle } from '../ops/depends';
import { applyTextEdits, editDocument } from '../patch';
import { findStep } from '../stepIndex';

const FLOW: DependsStyle = { multi: 'flow', singleScalar: false };
const BLOCK: DependsStyle = { multi: 'block', singleScalar: false };
const SCALAR: DependsStyle = { multi: 'flow', singleScalar: true };

/** Writes `names` as the depends of the step `key` and returns the new text. */
function write(
  text: string,
  key: string,
  names: string[],
  style: DependsStyle
): string {
  const result = editDocument(text, (doc) => {
    const step = findStep(doc, key);
    if (!step || !isMap(step.node)) throw new Error('no step');
    writeDepends(doc, step.node, names, style);
  });
  if (!result.ok) throw new Error(`${result.code}: ${result.message}`);
  expect(result.text).toBe(applyTextEdits(text, result.edits));
  return result.text;
}

function reference(
  text: string,
  key: string,
  options: ApplyEditOptions = {}
): { result: EditResult; name?: string } {
  let name: string | undefined;
  const result = editDocument(text, (doc) => {
    const step = findStep(doc, key);
    if (!step) throw new Error('no step');
    const ref = referenceNameFor(doc, step, options);
    if (!ref.ok) return ref;
    name = ref.name;
  });
  return { result, name };
}

describe('readDepends', () => {
  it('reads a scalar', () => {
    expect(
      readDepends(parseDocument('depends: a').get('depends', true))
    ).toEqual(['a']);
  });

  it('reads a sequence of scalars', () => {
    const doc = parseDocument('depends: [a, "b", 3]');
    expect(readDepends(doc.get('depends', true))).toEqual(['a', 'b', '3']);
  });

  it('reads plain values', () => {
    expect(readDepends('a')).toEqual(['a']);
    expect(readDepends(['a', 'b'])).toEqual(['a', 'b']);
  });

  it('gives an empty list for anything else', () => {
    expect(readDepends(undefined)).toEqual([]);
    expect(readDepends(null)).toEqual([]);
    expect(readDepends(parseDocument('depends:').get('depends', true))).toEqual(
      []
    );
    expect(
      readDepends(parseDocument('depends: {a: 1}').get('depends', true))
    ).toEqual([]);
  });
});

describe('detectDependsStyle', () => {
  const detect = (text: string) => detectDependsStyle(parseDocument(text));

  it('defaults to a flow list', () => {
    expect(detect('steps:\n  - run: a\n')).toEqual(FLOW);
    expect(detect('')).toEqual(FLOW);
  });

  it('reads a flow list in the array form', () => {
    expect(detect(yamlArraySteps)).toEqual(FLOW);
  });

  it('reads a flow list in the map form', () => {
    expect(detect(yamlMapSteps)).toEqual(FLOW);
  });

  it('prefers the first list of two entries', () => {
    const text = `steps:
  - name: a
    depends: [x]
  - name: b
    depends:
      - x
      - y
`;
    expect(detect(text)).toEqual(BLOCK);
  });

  it('reads a scalar', () => {
    const text =
      'steps:\n  - name: a\n    run: x\n  - name: b\n    depends: a\n';
    expect(detect(text)).toEqual(SCALAR);
  });

  it('reads no scalar when a list has one entry', () => {
    const text = 'steps:\n  - name: b\n    depends:\n      - a\n';
    expect(detect(text)).toEqual({ multi: 'block', singleScalar: false });
  });
});

describe('writeDepends', () => {
  it('adds a flow list after the name', () => {
    expect(write(yamlArraySteps, 'steps[0]', ['x', 'y'], FLOW)).toBe(
      yamlArraySteps.replace(
        '  - name: fetch\n',
        '  - name: fetch\n    depends: [x, y]\n'
      )
    );
  });

  it('adds a block list', () => {
    expect(write(yamlArraySteps, 'steps[0]', ['x', 'y'], BLOCK)).toBe(
      yamlArraySteps.replace(
        '  - name: fetch\n',
        '  - name: fetch\n    depends:\n      - x\n      - y\n'
      )
    );
  });

  it('adds a scalar for one name', () => {
    expect(write(yamlArraySteps, 'steps[0]', ['x'], SCALAR)).toBe(
      yamlArraySteps.replace(
        '  - name: fetch\n',
        '  - name: fetch\n    depends: x\n'
      )
    );
  });

  it('adds a one-entry list when scalars are not used', () => {
    expect(write(yamlMapSteps, 'steps[0]', ['x'], FLOW)).toBe(
      yamlMapSteps.replace('  fetch:\n', '  fetch:\n    depends: [x]\n')
    );
  });

  it('puts the key after id and description', () => {
    const text =
      'steps:\n  - run: a\n    description: d\n    name: n\n    id: i\n    with: {}\n';
    expect(write(text, 'steps[0]', ['x'], FLOW)).toBe(
      'steps:\n  - run: a\n    description: d\n    name: n\n    id: i\n    depends: [x]\n    with: {}\n'
    );
  });

  it('puts the key first without a name', () => {
    expect(write('steps:\n  - run: a\n', 'steps[0]', ['x'], FLOW)).toBe(
      'steps:\n  - depends: [x]\n    run: a\n'
    );
  });

  it('keeps a flow list when it grows', () => {
    expect(write(yamlArraySteps, 'steps[1]', ['fetch', 'extra'], BLOCK)).toBe(
      yamlArraySteps.replace('depends: [fetch]', 'depends: [fetch, extra]')
    );
  });

  it('keeps a block list when it grows', () => {
    expect(
      write(yamlArraySteps, 'steps[2]', ['transform', 'fetch'], FLOW)
    ).toBe(
      yamlArraySteps.replace(
        '      - transform\n',
        '      - transform\n      - fetch\n'
      )
    );
  });

  it('keeps the quotes of a name that stays', () => {
    const text = 'steps:\n  - name: a\n    depends: ["x", y]\n';
    expect(write(text, 'steps[0]', ['y', 'x', 'z'], FLOW)).toBe(
      'steps:\n  - name: a\n    depends: [y, "x", z]\n'
    );
  });

  it('shrinks a list to one entry and keeps the list', () => {
    const text = 'steps:\n  - name: a\n    depends: [x, y]\n';
    expect(write(text, 'steps[0]', ['y'], SCALAR)).toBe(
      'steps:\n  - name: a\n    depends: [y]\n'
    );
  });

  it('changes a scalar in place', () => {
    const text = 'steps:\n  - name: a\n    depends: x   # up\n';
    expect(write(text, 'steps[0]', ['y'], FLOW)).toBe(
      'steps:\n  - name: a\n    depends: y   # up\n'
    );
  });

  it('turns a scalar into a list for two names', () => {
    const text = 'steps:\n  - name: a\n    depends: x\n';
    expect(write(text, 'steps[0]', ['x', 'y'], FLOW)).toBe(
      'steps:\n  - name: a\n    depends: [x, y]\n'
    );
  });

  it('keeps the inline comment when a scalar becomes a list', () => {
    const text = 'steps:\n  - name: a\n    depends: x   # up\n';
    expect(write(text, 'steps[0]', ['x', 'y'], FLOW)).toBe(
      'steps:\n  - name: a\n    depends: [x, y]   # up\n'
    );
  });

  it('fills an empty depends in place', () => {
    const text = 'steps:\n  - name: a\n    depends:\n    run: b\n';
    expect(write(text, 'steps[0]', ['x'], FLOW)).toBe(
      'steps:\n  - name: a\n    depends: [x]\n    run: b\n'
    );
  });

  it('removes the key for an empty list', () => {
    expect(write(yamlArraySteps, 'steps[1]', [], FLOW)).toBe(
      yamlArraySteps.replace('    depends: [fetch]\n', '')
    );
  });

  it('removes a block list', () => {
    expect(write(yamlArraySteps, 'steps[2]', [], FLOW)).toBe(
      yamlArraySteps.replace('    depends:\n      - transform\n', '')
    );
  });

  it('gives noop for an empty list without the key', () => {
    const result = editDocument(yamlArraySteps, (doc) => {
      const step = findStep(doc, 'steps[0]');
      if (step && isMap(step.node)) writeDepends(doc, step.node, [], FLOW);
    });
    expect(result).toMatchObject({ ok: false, code: 'noop' });
  });
});

describe('referenceNameFor', () => {
  it('uses the explicit name', () => {
    expect(reference(yamlArraySteps, 'steps[0]').name).toBe('fetch');
  });

  it('uses the key in the map form', () => {
    expect(reference(yamlMapSteps, 'steps[1]').name).toBe('parse');
  });

  it('uses the id when there is no name', () => {
    const text = 'steps:\n  - id: first\n    run: a\n';
    const { result, name } = reference(text, 'steps[0]');
    expect(name).toBe('first');
    expect(result).toMatchObject({ ok: false, code: 'noop' });
  });

  it('writes the server name as first key', () => {
    const text = 'steps:\n  - run: a\n    id: first\n  - run: b\n';
    const options: ApplyEditOptions = {
      names: { byKey: { 'steps[1]': 'cmd_2' }, byName: { cmd_2: 'steps[1]' } },
    };
    const { result, name } = reference(text, 'steps[1]', options);
    expect(name).toBe('cmd_2');
    expect(result).toMatchObject({
      ok: true,
      text: 'steps:\n  - run: a\n    id: first\n  - name: cmd_2\n    run: b\n',
    });
  });

  it('fills an empty name key', () => {
    const text = 'steps:\n  - name: ""\n    run: a\n';
    const options: ApplyEditOptions = {
      names: { byKey: { 'steps[0]': 'cmd_1' }, byName: { cmd_1: 'steps[0]' } },
    };
    expect(reference(text, 'steps[0]', options).result).toMatchObject({
      ok: true,
      text: 'steps:\n  - name: cmd_1\n    run: a\n',
    });
  });

  it('refuses an unnamed step without server names', () => {
    const text = 'steps:\n  - run: a\n';
    expect(reference(text, 'steps[0]').result).toMatchObject({
      ok: false,
      code: 'invalidName',
    });
    expect(
      reference(text, 'steps[0]', { names: { byKey: {}, byName: {} } }).result
    ).toMatchObject({
      ok: false,
      code: 'invalidName',
    });
  });

  it('refuses a shorthand step', () => {
    expect(reference(yamlShorthandSteps, 'steps[0]').result).toMatchObject({
      ok: false,
      code: 'unsupportedForm',
    });
  });

  it('refuses a handler', () => {
    const text = 'handler_on:\n  failure:\n    run: a\nsteps:\n  - run: b\n';
    expect(reference(text, 'handler_on.failure').result).toMatchObject({
      ok: false,
      code: 'unsupportedForm',
    });
  });
});
