// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { parseDagDoc } from '../doc/parse';
import { savedStepText } from '../stepBaseline';

const saved = `name: demo
steps:
  - name: a
    run: echo a

  - id: b
    name: b
    run: echo b
`;

describe('savedStepText', () => {
  const savedModel = parseDagDoc(saved);

  it('gives the saved lines of a step that is still there', () => {
    const current = parseDagDoc(saved.replace('echo b', 'echo B'));
    expect(savedStepText(savedModel, current, 'steps[1]')).toBe(
      '  - id: b\n    name: b\n    run: echo b'
    );
  });

  it('follows a step that moved, by its name', () => {
    const current = parseDagDoc(
      saved.replace('steps:\n', 'steps:\n  - name: new\n    run: echo n\n')
    );
    expect(savedStepText(savedModel, current, 'steps[1]')).toBe(
      '  - name: a\n    run: echo a'
    );
  });

  it('gives null for a step that the saved text does not have', () => {
    const current = parseDagDoc(
      saved.replace('steps:\n', 'steps:\n  - name: new\n    run: echo n\n')
    );
    expect(savedStepText(savedModel, current, 'steps[0]')).toBeNull();
    expect(savedStepText(savedModel, current, 'steps[9]')).toBeNull();
  });

  // The deleted step `a` was at the place of `b`: the name wins over it.
  it('follows a step by its name before its place', () => {
    const current = parseDagDoc(
      saved.replace('  - name: a\n    run: echo a\n\n', '')
    );
    expect(savedStepText(savedModel, current, 'steps[0]')).toBe(
      '  - id: b\n    name: b\n    run: echo b'
    );
  });

  it('takes a renamed step for the same step', () => {
    const current = parseDagDoc(saved.replace('name: a', 'name: renamed'));
    expect(savedStepText(savedModel, current, 'steps[0]')).toBe(
      '  - name: a\n    run: echo a'
    );
  });
});
