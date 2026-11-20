// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it, vi } from 'vitest';
import type { languages } from 'monaco-editor';
import { registerStepLenses, type StepLensSource } from '../stepLenses';

// A stand-in for the parts of Monaco that the lenses use.
function fakeMonaco() {
  let provider: languages.CodeLensProvider | null = null;
  const fire = vi.fn();
  const monaco = {
    Emitter: class {
      event = vi.fn();
      fire = fire;
      dispose = vi.fn();
    },
    languages: {
      registerCodeLensProvider: (
        _id: string,
        next: languages.CodeLensProvider
      ) => {
        provider = next;
        return { dispose: vi.fn() };
      },
    },
  };
  return { monaco, fire, provider: () => provider! };
}

function fakeEditor(model: object, lines = 20) {
  let handler: ((accessor: unknown, ...args: unknown[]) => void) | null = null;
  const editor = {
    addCommand: (_key: number, run: typeof handler) => {
      handler = run;
      return 'cmd-1';
    },
    getModel: () => model,
  };
  const modelWithLines = Object.assign(model, {
    getLineCount: () => lines,
    getValue: () => '',
  });
  return {
    editor,
    model: modelWithLines,
    run: (...a: unknown[]) => handler!(null, ...a),
  };
}

const source = (open = vi.fn()): StepLensSource => ({
  label: 'Open step',
  open,
  steps: () => [
    { line: 3, name: 'first' },
    { line: 9, name: 'second' },
    { line: 99, name: 'beyond' },
  ],
});

describe('registerStepLenses', () => {
  it('puts one lens above the first row of each step of the model', () => {
    const { monaco, provider } = fakeMonaco();
    const { editor, model } = fakeEditor({});
    registerStepLenses(monaco as never, editor as never, () => source());
    const result = provider().provideCodeLenses(
      model as never,
      {} as never
    ) as {
      lenses: languages.CodeLens[];
    };
    expect(result.lenses.map((l) => l.range.startLineNumber)).toEqual([3, 9]);
    expect(result.lenses[0]?.command).toMatchObject({
      id: 'cmd-1',
      title: 'Open step',
      arguments: ['first'],
    });
  });

  it('gives no lens to another model, or without a source', () => {
    const { monaco, provider } = fakeMonaco();
    const { editor } = fakeEditor({});
    registerStepLenses(monaco as never, editor as never, () => source());
    const other = { getLineCount: () => 20, getValue: () => '' };
    expect(
      (
        provider().provideCodeLenses(other as never, {} as never) as {
          lenses: unknown[];
        }
      ).lenses
    ).toEqual([]);
    const { monaco: m2, provider: p2 } = fakeMonaco();
    const own = fakeEditor({});
    registerStepLenses(m2 as never, own.editor as never, () => null);
    expect(
      (
        p2().provideCodeLenses(own.model as never, {} as never) as {
          lenses: unknown[];
        }
      ).lenses
    ).toEqual([]);
  });

  // A query can come before the host has read an edit, and Monaco drops the
  // query that would follow when the editor loses the focus. The lines must
  // come from the text of the model at the time of the query.
  it('reads the steps from the text of the model at each query', () => {
    const { monaco, provider } = fakeMonaco();
    const { editor, model } = fakeEditor({});
    let text = 'steps:\n  - id: a';
    Object.assign(model, { getValue: () => text });
    const stepsOf = (t: string) =>
      t
        .split('\n')
        .flatMap((row, i) =>
          row.startsWith('  - id: ')
            ? [{ line: i + 1, name: row.slice(8) }]
            : []
        );
    registerStepLenses(monaco as never, editor as never, () => ({
      label: 'Open step',
      open: vi.fn(),
      steps: stepsOf,
    }));
    const rows = () =>
      (
        provider().provideCodeLenses(model as never, {} as never) as {
          lenses: languages.CodeLens[];
        }
      ).lenses.map((l) => l.range.startLineNumber);
    expect(rows()).toEqual([2]);
    // A line is added above the step: no refresh happens, the lens moves.
    text = 'steps:\n  # note\n  - id: a';
    expect(rows()).toEqual([3]);
  });

  it('opens the step that the lens names', () => {
    const { monaco } = fakeMonaco();
    const open = vi.fn();
    const { editor, run } = fakeEditor({});
    registerStepLenses(monaco as never, editor as never, () => source(open));
    run('second');
    expect(open).toHaveBeenCalledWith('second');
  });

  it('draws the lenses again on refresh', () => {
    const { monaco, fire } = fakeMonaco();
    const { editor } = fakeEditor({});
    const registration = registerStepLenses(
      monaco as never,
      editor as never,
      () => source()
    );
    registration.refresh();
    expect(fire).toHaveBeenCalledTimes(1);
  });
});
