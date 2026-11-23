// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { ApiDAGDetails } from '../../contracts';
import { parseDagDoc, resolveStepNames } from '../parse';

// Expected keys come from `spec.LoadYAML` on the same texts (Go
// `buildStepsFromMap` sorts by byte order; `buildStepsFromArray` flattens
// nested groups in place). Upper case sorts before lower case.
describe('StepKey numbering against the server', () => {
  it('sorts map-form steps by byte order', () => {
    const model = parseDagDoc(
      'steps:\n  zeta:\n    run: echo z\n  alpha:\n    run: echo a\n  Mid:\n    run: echo m\n'
    );
    expect(model.steps.map((s) => [s.key, s.explicitName])).toEqual([
      ['steps[0]', 'Mid'],
      ['steps[1]', 'alpha'],
      ['steps[2]', 'zeta'],
    ]);
  });

  it('flattens shorthand and nested groups in order', () => {
    const model = parseDagDoc(
      'steps:\n  - echo one\n  - - name: g1\n      run: echo g1\n    - name: g2\n      run: echo g2\n  - name: last\n    run: echo last\n'
    );
    expect(
      model.steps.map((s) => [s.key, s.form, s.explicitName ?? s.runPreview])
    ).toEqual([
      ['steps[0]', 'shorthand', 'echo one'],
      ['steps[1]', 'parallelGroup', 'g1'],
      ['steps[2]', 'parallelGroup', 'g2'],
      ['steps[3]', 'block', 'last'],
    ]);
  });
});

describe('resolveStepNames', () => {
  const details = (...names: string[]) =>
    ({ steps: names.map((name) => ({ name })) }) as ApiDAGDetails;

  // The server names a step without a name from its id.
  it('matches by id when the details are stale', () => {
    const model = parseDagDoc(
      'steps:\n  - id: y\n    run: echo y\n  - id: x\n    run: echo x\n  - name: c\n    run: echo c\n'
    );
    const stale = {
      steps: [{ name: 'x', id: 'x' }, { name: 'y', id: 'y' }, { name: 'c' }],
    } as ApiDAGDetails;
    expect(resolveStepNames(model, stale).byKey).toEqual({
      'steps[0]': 'y',
      'steps[1]': 'x',
      'steps[2]': 'c',
    });
  });

  // The server trims the name and names a step with an empty name like an
  // unnamed one.
  it('maps by position a padded or an empty name with fresh details', () => {
    const model = parseDagDoc(
      'steps:\n  - name: " a "\n    run: echo a\n  - name: ""\n    run: echo b\n'
    );
    expect(resolveStepNames(model, details('a', 'cmd_2')).byKey).toEqual({
      'steps[0]': 'a',
      'steps[1]': 'cmd_2',
    });
  });

  it('trims the names of a text with no details', () => {
    const model = parseDagDoc(
      'steps:\n  - name: " a "\n    run: echo a\n  - name: ""\n    run: echo b\n'
    );
    expect(resolveStepNames(model).byKey).toEqual({ 'steps[0]': 'a' });
  });
});

describe('resolveStepNames against older details', () => {
  // The details of the saved DAG while a draft swaps two steps: an explicit
  // name wins over the position.
  it('matches a step by its explicit name when the position disagrees', () => {
    const model = parseDagDoc(
      'steps:\n  - name: b\n    run: echo b\n  - name: a\n    run: echo a\n  - echo c\n'
    );
    const details = {
      name: 'demo',
      steps: [{ name: 'a' }, { name: 'b' }, { name: 'cmd_3' }],
    } as never;
    expect(resolveStepNames(model, details).byKey).toEqual({
      'steps[0]': 'b',
      'steps[1]': 'a',
      'steps[2]': 'cmd_3',
    });
  });
});
