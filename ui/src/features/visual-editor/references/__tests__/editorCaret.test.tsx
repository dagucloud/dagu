// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ReferenceScope } from '../../contracts';
import { scopeHttpJq } from '../../testing/fixtures';
import {
  insertAtCaret,
  useCaretReferences,
  type CaretEditor,
} from '../editorCaret';

function fakeEditor(initial: string, offset = 0) {
  let text = initial;
  let caret = offset;
  let selectionEnd = offset;
  const listeners = new Set<() => void>();
  const toPosition = (at: number) => {
    const lines = text.slice(0, at).split('\n');
    return {
      lineNumber: lines.length,
      column: lines[lines.length - 1]!.length + 1,
    };
  };
  const toOffset = (p: { lineNumber: number; column: number }) =>
    text
      .split('\n')
      .slice(0, p.lineNumber - 1)
      .reduce((n, line) => n + line.length + 1, 0) +
    p.column -
    1;
  const editor = {
    getModel: () => ({
      getValue: () => text,
      getOffsetAt: toOffset,
      getPositionAt: toPosition,
    }),
    getPosition: () => toPosition(caret),
    setPosition: vi.fn((p: { lineNumber: number; column: number }) => {
      caret = toOffset(p);
      selectionEnd = caret;
    }),
    getSelection: () => {
      const start = toPosition(caret);
      const end = toPosition(selectionEnd);
      return {
        startLineNumber: start.lineNumber,
        startColumn: start.column,
        endLineNumber: end.lineNumber,
        endColumn: end.column,
      };
    },
    executeEdits: vi.fn(
      (
        _source: string,
        edits: {
          range: {
            startLineNumber: number;
            startColumn: number;
            endLineNumber: number;
            endColumn: number;
          };
          text: string;
        }[]
      ) => {
        const [edit] = edits;
        const from = toOffset({
          lineNumber: edit!.range.startLineNumber,
          column: edit!.range.startColumn,
        });
        const to = toOffset({
          lineNumber: edit!.range.endLineNumber,
          column: edit!.range.endColumn,
        });
        text = text.slice(0, from) + edit!.text + text.slice(to);
        return true;
      }
    ),
    pushUndoStop: vi.fn(() => true),
    trigger: vi.fn(),
    focus: vi.fn(),
    onDidChangeCursorPosition: (listener: () => void) => {
      listeners.add(listener);
      return { dispose: () => listeners.delete(listener) };
    },
  };
  return {
    editor: editor as unknown as CaretEditor,
    raw: editor,
    text: () => text,
    moveTo(at: number, end = at) {
      caret = at;
      selectionEnd = end;
      listeners.forEach((listener) => listener());
    },
    listeners,
  };
}

const scope: ReferenceScope = {
  ...scopeHttpJq,
  steps: [
    {
      ...scopeHttpJq.steps[0]!,
      name: 'each',
      id: 'each',
      stepPath: 'steps[0]',
      upstream: [],
      outputs: [],
      foreach: {
        item: [{ name: 'item', reference: '${foreach.item}' }],
        steps: [
          {
            ...scopeHttpJq.steps[0]!,
            name: 'body',
            id: 'body',
            stepPath: 'steps[0].foreach.steps[0]',
            upstream: [],
            outputs: [],
          },
        ],
      },
    },
  ],
};

const yaml = [
  'name: demo',
  'steps:',
  '  - name: each',
  '    foreach:',
  '      items: [a]',
  '      steps:',
  '        - name: body',
  '          run: echo hi',
  '',
].join('\n');

describe('useCaretReferences', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  const tokens = (items: { token: string }[] | null) =>
    items?.map((item) => item.token) ?? null;

  it('follows the caret into a foreach body', () => {
    const fake = fakeEditor(yaml, yaml.indexOf('items:') + 8);
    const { result } = renderHook(() =>
      useCaretReferences(fake.editor, true, { scope })
    );
    expect(tokens(result.current)).not.toContain('${foreach.item}');
    act(() => {
      fake.moveTo(yaml.indexOf('echo hi') + 2);
      vi.advanceTimersByTime(500);
    });
    expect(tokens(result.current)).toContain('${foreach.item}');
  });

  it('gives null at a key, where the host shows the step list', () => {
    const fake = fakeEditor(yaml, yaml.indexOf('run:') + 1);
    const { result } = renderHook(() =>
      useCaretReferences(fake.editor, true, { scope })
    );
    expect(result.current).toBeNull();
  });

  it('gives null and stops listening while the YAML is not shown', () => {
    const fake = fakeEditor(yaml, yaml.indexOf('echo hi'));
    const { result, rerender } = renderHook(
      ({ active }) => useCaretReferences(fake.editor, active, { scope }),
      { initialProps: { active: true } }
    );
    expect(result.current).not.toBeNull();
    rerender({ active: false });
    expect(result.current).toBeNull();
    expect(fake.listeners.size).toBe(0);
  });

  it('gives null without an editor', () => {
    const { result } = renderHook(() =>
      useCaretReferences(null, true, { scope })
    );
    expect(result.current).toBeNull();
  });
});

describe('insertAtCaret', () => {
  it('puts the token at the caret and the caret after it', () => {
    const text = 'run: echo ';
    const fake = fakeEditor(text, text.length);
    expect(insertAtCaret(fake.editor, '${env.A}')).toBe(true);
    expect(fake.text()).toBe('run: echo ${env.A}');
    expect(fake.raw.setPosition).toHaveBeenCalledWith({
      lineNumber: 1,
      column: 'run: echo ${env.A}'.length + 1,
    });
    expect(fake.raw.focus).toHaveBeenCalledTimes(1);
  });

  it('replaces the selection', () => {
    const fake = fakeEditor('run: old', 0);
    fake.moveTo(5, 8);
    insertAtCaret(fake.editor, '${env.A}');
    expect(fake.text()).toBe('run: ${env.A}');
  });

  it('applies another edit with the token, and keeps the caret after it', () => {
    const text = 'a: 1\nrun: ';
    const fake = fakeEditor(text, text.length);
    insertAtCaret(fake.editor, '${env.A}', {
      from: 4,
      to: 4,
      insert: '\nb: 2',
    });
    expect(fake.raw.executeEdits).toHaveBeenCalledTimes(1);
    const [, edits] = fake.raw.executeEdits.mock.calls[0]!;
    expect(edits.map((edit) => edit.text)).toContain('\nb: 2');
    expect(edits.map((edit) => edit.text)).toContain('${env.A}');
  });

  it('makes one undo step of the edit', () => {
    const fake = fakeEditor('run: ', 5);
    insertAtCaret(fake.editor, '${env.A}');
    expect(fake.raw.pushUndoStop).toHaveBeenCalledTimes(2);
  });

  it('reports false without an editor, so that the host copies', () => {
    expect(insertAtCaret(null, '${env.A}')).toBe(false);
  });
});
