// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it } from 'vitest';
import type { DagEditOp, StepNameMap } from '../contracts';
import { parseDagDoc, resolveStepNames } from '../doc';
import { yamlArraySteps } from '../testing/fixtures';
import { useEditSession, type CodeEditorLike } from '../useEditSession';

const names: StepNameMap = resolveStepNames(parseDagDoc(yamlArraySteps));

const setFirstRun = (run: string): DagEditOp => ({
  kind: 'setStepField',
  step: 'steps[0]',
  path: ['run'],
  value: run,
});

function useHarness(editor: CodeEditorLike | null = null) {
  const [text, setText] = useState(yamlArraySteps);
  const editorRef = { current: editor };
  const session = useEditSession({ text, setText, names, editorRef });
  return { text, setText, session };
}

// A small stand-in for a Monaco editor on one text model.
function fakeEditor(initial: string) {
  let value = initial;
  const undoStops: number[] = [];
  const triggers: string[] = [];
  const offsetAt = (line: number, column: number) => {
    const lines = value.split('\n');
    let offset = 0;
    for (let i = 0; i < line - 1; i++) offset += lines[i]!.length + 1;
    return offset + column - 1;
  };
  const editor: CodeEditorLike = {
    getModel: () => ({
      getValue: () => value,
      getPositionAt: (offset: number) => {
        const before = value.slice(0, offset).split('\n');
        return {
          lineNumber: before.length,
          column: before[before.length - 1]!.length + 1,
        };
      },
    }),
    executeEdits: (_source, edits) => {
      const offsets = edits.map((edit) => ({
        from: offsetAt(edit.range.startLineNumber, edit.range.startColumn),
        to: offsetAt(edit.range.endLineNumber, edit.range.endColumn),
        text: edit.text,
      }));
      for (const edit of [...offsets].reverse()) {
        value = value.slice(0, edit.from) + edit.text + value.slice(edit.to);
      }
      return true;
    },
    pushUndoStop: () => {
      undoStops.push(1);
      return true;
    },
    trigger: (_source, handlerId) => {
      triggers.push(handlerId);
    },
  };
  return { editor, value: () => value, undoStops, triggers };
}

describe('useEditSession without an editor', () => {
  it('undoes and redoes an edit', () => {
    const { result } = renderHook(() => useHarness());

    act(() => {
      expect(result.current.session.apply(setFirstRun('echo one')).ok).toBe(
        true
      );
    });
    const edited = result.current.text;
    expect(edited).toContain('echo one');
    expect(result.current.session.canUndo).toBe(true);

    act(() => result.current.session.undo());
    expect(result.current.text).toBe(yamlArraySteps);
    expect(result.current.session.canRedo).toBe(true);

    act(() => result.current.session.redo());
    expect(result.current.text).toBe(edited);
  });

  it('makes one undo step of the edits of one task', async () => {
    const { result } = renderHook(() => useHarness());

    await act(async () => {
      result.current.session.apply(setFirstRun('echo one'));
      result.current.session.apply(setFirstRun('echo two'));
      await Promise.resolve();
    });
    expect(result.current.text).toContain('echo two');

    act(() => result.current.session.undo());
    expect(result.current.text).toBe(yamlArraySteps);
  });

  it('keeps text changed elsewhere as an undo step', async () => {
    const { result } = renderHook(() => useHarness());
    const typed = yamlArraySteps + '# typed\n';

    act(() => result.current.setText(typed));
    await act(async () => {
      result.current.session.apply(setFirstRun('echo one'));
      await Promise.resolve();
    });

    act(() => result.current.session.undo());
    expect(result.current.text).toBe(typed);
    act(() => result.current.session.undo());
    expect(result.current.text).toBe(yamlArraySteps);
  });

  // Two clipboard edits in a row: one undo reverts exactly one of them.
  it('makes each computed edit an undo step of its own', async () => {
    const { result } = renderHook(() => useHarness());
    const append = (line: string) => (input: string) => ({
      ok: true as const,
      text: `${input}${line}\n`,
      edits: [{ from: input.length, to: input.length, insert: `${line}\n` }],
    });

    await act(async () => {
      result.current.session.applyWith(append('# one'));
      await Promise.resolve();
    });
    await act(async () => {
      result.current.session.applyWith(append('# two'));
      await Promise.resolve();
    });
    const one = `${yamlArraySteps}# one\n`;
    expect(result.current.text).toBe(`${one}# two\n`);

    act(() => result.current.session.undo());
    expect(result.current.text).toBe(one);
    act(() => result.current.session.undo());
    expect(result.current.text).toBe(yamlArraySteps);
  });

  it('returns the failure of an edit and keeps the text', () => {
    const { result } = renderHook(() => useHarness());

    let outcome: ReturnType<typeof result.current.session.apply> | undefined;
    act(() => {
      outcome = result.current.session.apply({
        kind: 'setStepField',
        step: 'steps[9]',
        path: ['run'],
        value: 'x',
      });
    });
    expect(outcome).toMatchObject({ ok: false, code: 'unknownStep' });
    expect(result.current.text).toBe(yamlArraySteps);
  });
});

describe('useEditSession with an editor', () => {
  it('applies the edits through the editor', async () => {
    const fake = fakeEditor(yamlArraySteps);
    const { result } = renderHook(() => useHarness(fake.editor));

    await act(async () => {
      result.current.session.apply(setFirstRun('echo one'));
      await Promise.resolve();
    });

    expect(fake.value()).toBe(result.current.text);
    expect(fake.value()).toContain('echo one');
    expect(fake.undoStops.length).toBeGreaterThanOrEqual(2);
  });

  it('replaces the whole text for a computed edit without text edits', async () => {
    const fake = fakeEditor(yamlArraySteps);
    const { result } = renderHook(() => useHarness(fake.editor));
    const next = `${yamlArraySteps}# pasted\n`;

    await act(async () => {
      result.current.session.applyWith(() => ({
        ok: true,
        text: next,
        edits: [],
      }));
      await Promise.resolve();
    });

    expect(fake.value()).toBe(next);
    expect(result.current.text).toBe(next);
  });

  it('lets the editor undo', () => {
    const fake = fakeEditor(yamlArraySteps);
    const { result } = renderHook(() => useHarness(fake.editor));

    act(() => result.current.session.undo());
    act(() => result.current.session.redo());

    expect(fake.triggers).toEqual(['undo', 'redo']);
  });
});
