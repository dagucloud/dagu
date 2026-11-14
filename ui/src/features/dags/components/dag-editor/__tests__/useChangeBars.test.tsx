// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  CHANGE_BAR_CLASS,
  CHANGE_BAR_DELAY_MS,
  changeBarDecorations,
  useChangeBars,
  type ChangeBarEditor,
} from '../useChangeBars';

type Decorations = Parameters<
  NonNullable<ChangeBarEditor['createDecorationsCollection']>
>[0];

function fakeEditor() {
  const state = { shown: [] as Decorations, cleared: 0, created: 0 };
  const editor: ChangeBarEditor = {
    createDecorationsCollection: (initial) => {
      state.created += 1;
      state.shown = initial;
      return {
        set: (next) => {
          state.shown = next;
        },
        clear: () => {
          state.cleared += 1;
          state.shown = [];
        },
      };
    },
  };
  return { editor, state };
}

const classes = (shown: Decorations) =>
  shown.map(
    (item) =>
      `${item.range.startLineNumber}:${item.options.linesDecorationsClassName}`
  );

describe('changeBarDecorations', () => {
  it('puts the class of each kind of change on its line', () => {
    expect(
      classes(
        changeBarDecorations({
          added: [2],
          modified: [4],
          deleted: [
            { line: 6, below: false },
            { line: 9, below: true },
          ],
        })
      )
    ).toEqual([
      `2:${CHANGE_BAR_CLASS.added}`,
      `4:${CHANGE_BAR_CLASS.modified}`,
      `6:${CHANGE_BAR_CLASS.deletedAbove}`,
      `9:${CHANGE_BAR_CLASS.deletedBelow}`,
    ]);
  });
});

describe('useChangeBars', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  const wait = () =>
    act(() => {
      vi.advanceTimersByTime(CHANGE_BAR_DELAY_MS);
    });

  it('shows no bar while the text equals the saved text', () => {
    const { editor, state } = fakeEditor();
    renderHook(() => useChangeBars(editor, 'a\nb', 'a\nb'));
    wait();
    expect(state.shown).toEqual([]);
    expect(state.created).toBe(0);
  });

  it('shows the bars after the delay and follows the text', () => {
    const { editor, state } = fakeEditor();
    const { rerender } = renderHook(
      ({ text }) => useChangeBars(editor, 'a\nb', text),
      { initialProps: { text: 'a\nb' } }
    );
    rerender({ text: 'a\nb\nc' });
    expect(state.shown).toEqual([]);
    wait();
    expect(classes(state.shown)).toEqual([`3:${CHANGE_BAR_CLASS.added}`]);

    rerender({ text: 'a\nB\nc' });
    wait();
    expect(classes(state.shown).sort()).toEqual([
      `2:${CHANGE_BAR_CLASS.modified}`,
      `3:${CHANGE_BAR_CLASS.added}`,
    ]);
    // One collection for all the updates.
    expect(state.created).toBe(1);
  });

  it('waits for the last edit of a burst', () => {
    const { editor, state } = fakeEditor();
    const { rerender } = renderHook(
      ({ text }) => useChangeBars(editor, 'a', text),
      { initialProps: { text: 'a' } }
    );
    rerender({ text: 'ab' });
    act(() => {
      vi.advanceTimersByTime(CHANGE_BAR_DELAY_MS - 10);
    });
    rerender({ text: 'abc' });
    act(() => {
      vi.advanceTimersByTime(CHANGE_BAR_DELAY_MS - 10);
    });
    expect(state.shown).toEqual([]);
    act(() => {
      vi.advanceTimersByTime(10);
    });
    expect(classes(state.shown)).toEqual([`1:${CHANGE_BAR_CLASS.modified}`]);
  });

  it('clears the bars when the text is the saved text again', () => {
    const { editor, state } = fakeEditor();
    const { rerender } = renderHook(
      ({ text }) => useChangeBars(editor, 'a', text),
      { initialProps: { text: 'b' } }
    );
    wait();
    expect(state.shown).not.toEqual([]);
    rerender({ text: 'a' });
    expect(state.shown).toEqual([]);
  });

  it('clears the bars without a baseline and on unmount', () => {
    const { editor, state } = fakeEditor();
    const { rerender, unmount } = renderHook(
      ({ baseline }: { baseline: string | undefined }) =>
        useChangeBars(editor, baseline, 'b'),
      { initialProps: { baseline: 'a' as string | undefined } }
    );
    wait();
    expect(state.shown).not.toEqual([]);
    rerender({ baseline: undefined });
    expect(state.shown).toEqual([]);
    rerender({ baseline: 'a' });
    wait();
    expect(state.shown).not.toEqual([]);
    unmount();
    expect(state.shown).toEqual([]);
  });

  it('does nothing without an editor', () => {
    expect(() => {
      renderHook(() => useChangeBars(null, 'a', 'b'));
      wait();
    }).not.toThrow();
  });

  // A blank line or a trailing space is no change of the document.
  it('shows no bar for blank lines and trailing spaces', () => {
    const { editor, state } = fakeEditor();
    const saved = 'name: a\nsteps:\n  - name: x\n    run: y\n';
    const { rerender } = renderHook(
      ({ text }) => useChangeBars(editor, saved, text),
      { initialProps: { text: saved } }
    );
    rerender({ text: saved.replace('steps:', '\n\nsteps:   ') + '\n' });
    wait();
    expect(state.shown).toEqual([]);

    rerender({ text: saved.replace('run: y', 'run: z') });
    wait();
    expect(classes(state.shown)).toEqual([`4:${CHANGE_BAR_CLASS.modified}`]);
  });
});
