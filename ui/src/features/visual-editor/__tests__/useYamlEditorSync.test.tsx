// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { parseDagDoc } from '../doc/parse';
import { yamlArraySteps } from '../testing/fixtures';
import {
  ACTIVE_BLOCK_CLASS,
  hiddenAreasAround,
  shiftVisibleLines,
  stepAtLine,
  useYamlEditorSync,
  type SyncableEditor,
} from '../useYamlEditorSync';

const model = parseDagDoc(yamlArraySteps);
const [first, second, third] = model.steps;

type Listener<T> = (event: T) => void;

type Sel = {
  selectionStartLineNumber: number;
  selectionStartColumn: number;
  positionLineNumber: number;
  positionColumn: number;
};
let selectionListener:
  | ((event: { selection: Sel; secondarySelections: Sel[] }) => void)
  | undefined;
let contentListener:
  | ((event: {
      changes: {
        range: {
          startLineNumber: number;
          startColumn: number;
          endLineNumber: number;
          endColumn: number;
        };
        text: string;
      }[];
    }) => void)
  | undefined;
let keyListener:
  | ((event: {
      keyCode: number;
      altKey?: boolean;
      shiftKey?: boolean;
      preventDefault(): void;
      stopPropagation(): void;
    }) => void)
  | undefined;

/** An editor that records what the hook asks of it. */
function fakeEditor(
  lineCount = 30,
  withGuards = false,
  text: () => string = () => yamlArraySteps
) {
  const cursorListeners: Listener<{
    position: { lineNumber: number };
    source?: string;
  }>[] = [];
  const state = {
    hidden: [] as unknown[],
    decorations: [] as {
      range: { startLineNumber: number; endLineNumber: number };
      options: { className?: string };
    }[],
    // The decorations of the second collection: the change bars.
    bars: [] as {
      range: { startLineNumber: number };
      options: { linesDecorationsClassName?: string };
    }[],
    lineNumbers: [] as unknown[],
    collections: 0,
    revealed: [] as number[],
    selections: [] as unknown[],
    keyPrevented: 0,
    current: [] as unknown[],
    disposed: 0,
    cleared: 0,
  };
  const editor = {
    getModel: () => ({
      getValue: text,
      getPositionAt: () => ({ lineNumber: 1, column: 1 }),
      getLineCount: () => lineCount,
      getLineMaxColumn: () => 40,
    }),
    executeEdits: () => true,
    pushUndoStop: () => true,
    trigger: () => undefined,
    setHiddenAreas: (ranges: unknown[]) => {
      state.hidden = ranges;
    },
    createDecorationsCollection: (initial: never[]) => {
      // The first collection lights the step; the second draws the bars.
      const key = state.collections++ === 0 ? 'decorations' : 'bars';
      (state as Record<string, unknown>)[key] = initial;
      return {
        set: (next: never[]) => {
          (state as Record<string, unknown>)[key] = next;
        },
        clear: () => {
          state.cleared += 1;
          (state as Record<string, unknown>)[key] = [];
        },
      };
    },
    onDidChangeCursorPosition: (listener: (typeof cursorListeners)[number]) => {
      cursorListeners.push(listener);
      return { dispose: () => (state.disposed += 1) };
    },
    revealLineInCenterIfOutsideViewport: (line: number) => {
      state.revealed.push(line);
    },
    updateOptions: (options: { lineNumbers: unknown }) => {
      state.lineNumbers.push(options.lineNumbers);
    },
    ...(withGuards
      ? {
          onDidChangeModelContent: (listener: typeof contentListener) => {
            contentListener = listener;
            return { dispose: () => (state.disposed += 1) };
          },
          onDidChangeCursorSelection: (listener: typeof selectionListener) => {
            selectionListener = listener;
            return { dispose: () => (state.disposed += 1) };
          },
          setSelections: (next: unknown[]) => {
            state.selections = next;
          },
          getSelections: () => state.current,
          onKeyDown: (listener: typeof keyListener) => {
            keyListener = listener;
            return { dispose: () => (state.disposed += 1) };
          },
        }
      : {}),
  } as unknown as SyncableEditor;
  return {
    select: (selection: Sel, secondarySelections: Sel[] = []) =>
      selectionListener?.({ selection, secondarySelections }),
    change: (range: [number, number, number, number], text: string) =>
      contentListener?.({
        changes: [
          {
            range: {
              startLineNumber: range[0],
              startColumn: range[1],
              endLineNumber: range[2],
              endColumn: range[3],
            },
            text,
          },
        ],
      }),
    press: (
      keyCode: number,
      modifiers: { altKey?: boolean; shiftKey?: boolean } = {}
    ) =>
      keyListener?.({
        keyCode,
        ...modifiers,
        preventDefault: () => (state.keyPrevented += 1),
        stopPropagation: () => undefined,
      }),
    editor,
    state,
    moveCursor: (lineNumber: number, source?: string) =>
      cursorListeners.forEach((listener) =>
        listener({ position: { lineNumber }, source })
      ),
  };
}

function setup(
  overrides: Partial<Parameters<typeof useYamlEditorSync>[0]> = {},
  editorLines = 30,
  withGuards = false,
  text?: () => string
) {
  selectionListener = undefined;
  keyListener = undefined;
  contentListener = undefined;
  const fake = fakeEditor(editorLines, withGuards, text);
  const onCursorStep = vi.fn();
  const args = {
    editorRef: { current: fake.editor },
    visible: true,
    model,
    frozen: false,
    selected: null,
    scope: 'step' as const,
    onCursorStep,
    ...overrides,
  };
  const view = renderHook((props: typeof args) => useYamlEditorSync(props), {
    initialProps: args,
  });
  return { ...fake, onCursorStep, args, ...view };
}

describe('step lines', () => {
  it('finds the step that holds a line and the step declared on it', () => {
    expect(stepAtLine(model, first!.range.startLine)).toBe(first!.key);
    expect(stepAtLine(model, second!.range.endLine)).toBe(second!.key);
    expect(stepAtLine(model, 1)).toBeNull();
  });

  it('hides what is before and after a step, and nothing for the whole text', () => {
    const range = { startLine: 5, endLine: 8 } as never;
    expect(hiddenAreasAround({ range }, 20)).toEqual([
      expect.objectContaining({ startLineNumber: 1, endLineNumber: 4 }),
      expect.objectContaining({ startLineNumber: 9, endLineNumber: 20 }),
    ]);
    expect(
      hiddenAreasAround({ range: { startLine: 1, endLine: 20 } as never }, 20)
    ).toEqual([]);
    expect(
      hiddenAreasAround({ range: { startLine: 1, endLine: 6 } as never }, 20)
    ).toEqual([
      expect.objectContaining({ startLineNumber: 7, endLineNumber: 20 }),
    ]);
  });
});

describe('useYamlEditorSync', () => {
  it('puts no button in the gutter at the declaration of a step', () => {
    const { state } = setup();

    expect(
      state.decorations.filter((d) =>
        Object.keys(d.options).some((key) => key.startsWith('glyphMargin'))
      )
    ).toEqual([]);
  });

  it('lights the lines of the selected step in the file scope', () => {
    const { state, rerender, args } = setup({
      selected: second!.key,
      scope: 'file',
    });

    const lit = () =>
      state.decorations.filter(
        (d) => d.options.className === ACTIVE_BLOCK_CLASS
      );
    expect(lit()).toHaveLength(1);
    expect(lit()[0]?.range).toMatchObject({
      startLineNumber: second!.range.startLine,
      endLineNumber: second!.range.endLine,
    });
    rerender({ ...args, selected: null });
    expect(lit()).toHaveLength(0);
  });

  // The step scope shows the selected step alone: lighting it adds nothing.
  it('does not light the selected step in the step scope', () => {
    const { state } = setup({ selected: second!.key, scope: 'step' });
    expect(
      state.decorations.filter(
        (d) => d.options.className === ACTIVE_BLOCK_CLASS
      )
    ).toHaveLength(0);
  });

  it('tells which step the cursor is in, and none between steps', () => {
    const { moveCursor, onCursorStep } = setup();

    moveCursor(second!.range.startLine + 1);
    expect(onCursorStep).toHaveBeenLastCalledWith(second!.key, true);
    moveCursor(1);
    expect(onCursorStep).toHaveBeenLastCalledWith(null, true);
  });

  // The editor moves the cursor itself when the text changes under it; a
  // selection of the canvas must not follow that.
  it('tells that the user moved the cursor with the mouse or the keys only', () => {
    const { moveCursor, onCursorStep } = setup();
    const line = second!.range.startLine + 1;

    moveCursor(line, 'mouse');
    expect(onCursorStep).toHaveBeenLastCalledWith(second!.key, true);
    moveCursor(line, 'keyboard');
    expect(onCursorStep).toHaveBeenLastCalledWith(second!.key, true);
    moveCursor(line, 'modelChange');
    expect(onCursorStep).toHaveBeenLastCalledWith(second!.key, false);
    moveCursor(line, 'api');
    expect(onCursorStep).toHaveBeenLastCalledWith(second!.key, false);
  });

  it('shows only the selected step in the step scope', () => {
    const { state } = setup({ selected: second!.key });

    expect(state.hidden).toEqual([
      expect.objectContaining({
        startLineNumber: 1,
        endLineNumber: second!.range.startLine - 1,
      }),
      expect.objectContaining({ startLineNumber: second!.range.endLine + 1 }),
    ]);
    expect(state.revealed).toContain(second!.range.startLine);
  });

  it('shows the whole file in the file scope, without a selection or with a broken text', () => {
    expect(
      setup({ selected: second!.key, scope: 'file' }).state.hidden
    ).toEqual([]);
    expect(setup({ selected: null }).state.hidden).toEqual([]);
    expect(setup({ selected: second!.key, frozen: true }).state.hidden).toEqual(
      []
    );
  });

  it('shows the whole file again when the scope changes or the drawer closes', () => {
    const { state, rerender, args } = setup({ selected: second!.key });
    expect(state.hidden.length).toBeGreaterThan(0);

    rerender({ ...args, selected: second!.key, scope: 'file' });
    expect(state.hidden).toEqual([]);
    rerender({ ...args, selected: second!.key, scope: 'step' });
    expect(state.hidden.length).toBeGreaterThan(0);
    rerender({ ...args, selected: second!.key, visible: false });
    expect(state.hidden).toEqual([]);
  });

  // The editor loads after the drawer opens, as in a link to a step.
  it('starts when the editor mounts after the drawer', () => {
    const fake = fakeEditor();
    const editorRef = { current: null as SyncableEditor | null };
    const { rerender, args } = setup({
      editorRef,
      selected: second!.key,
    });
    expect(fake.state.hidden).toEqual([]);

    editorRef.current = fake.editor;
    rerender({ ...args, editorRef, selected: second!.key });

    expect(fake.state.hidden.length).toBeGreaterThan(0);
  });

  it('follows the selection from one step to another', () => {
    const { state, rerender, args } = setup({ selected: first!.key });

    rerender({ ...args, selected: third!.key });

    expect(state.hidden[0]).toMatchObject({
      endLineNumber: third!.range.startLine - 1,
    });
  });

  it('does nothing while the drawer is closed', () => {
    const { state, moveCursor, onCursorStep } = setup({
      visible: false,
      selected: second!.key,
    });

    expect(state.decorations).toEqual([]);
    expect(state.hidden).toEqual([]);
    moveCursor(first!.range.startLine);
    expect(onCursorStep).not.toHaveBeenCalled();
  });

  it('stops listening and clears its marks when it unmounts', () => {
    const { state, unmount } = setup({ selected: second!.key });

    unmount();

    expect(state.disposed).toBe(1);
    expect(state.cleared).toBe(1);
    expect(state.hidden).toEqual([]);
  });

  it('works with an editor that has none of the extra methods', () => {
    const bare = {
      getModel: () => ({
        getValue: () => '',
        getPositionAt: () => ({ lineNumber: 1, column: 1 }),
      }),
    } as unknown as SyncableEditor;

    expect(() =>
      setup({ editorRef: { current: bare }, selected: second!.key })
    ).not.toThrow();
  });
});

describe('step scope guards', () => {
  const sel = (a: number, ac: number, p: number, pc: number) => ({
    selectionStartLineNumber: a,
    selectionStartColumn: ac,
    positionLineNumber: p,
    positionColumn: pc,
  });

  // Select all in the step scope must not reach the hidden lines of the file.
  it('cuts select all to the lines of the step', () => {
    const { select, state } = setup({ selected: second!.key }, 30, true);

    select(sel(1, 1, 30, 40));

    expect(state.selections).toEqual([
      sel(second!.range.startLine, 1, second!.range.endLine, 40),
    ]);
  });

  it('leaves a selection inside the step alone', () => {
    const { select, state } = setup({ selected: second!.key }, 30, true);

    select(sel(second!.range.startLine, 3, second!.range.startLine + 1, 5));

    expect(state.selections).toEqual([]);
  });

  it('cuts a secondary cursor and keeps the direction of a backward selection', () => {
    const { select, state } = setup({ selected: second!.key }, 30, true);

    select(sel(second!.range.endLine, 2, second!.range.startLine, 2), [
      sel(1, 1, 1, 1),
    ]);

    expect(state.selections).toEqual([
      sel(second!.range.endLine, 2, second!.range.startLine, 2),
      sel(second!.range.startLine, 1, second!.range.startLine, 1),
    ]);
  });

  it('does not cut anything in the file scope', () => {
    const { select, state } = setup(
      { selected: second!.key, scope: 'file' },
      30,
      true
    );

    select(sel(1, 1, 30, 40));

    expect(state.selections).toEqual([]);
  });

  it('refuses a Backspace that would join the step to a hidden line', () => {
    const { press, state } = setup({ selected: second!.key }, 30, true);

    state.current = [
      sel(second!.range.startLine, 1, second!.range.startLine, 1),
    ];
    press(1);
    expect(state.keyPrevented).toBe(1);

    state.current = [
      sel(second!.range.startLine, 2, second!.range.startLine, 2),
    ];
    press(1);
    expect(state.keyPrevented).toBe(1);
  });

  it('refuses a Delete at the end of the last line of the step', () => {
    const { press, state } = setup({ selected: second!.key }, 30, true);

    state.current = [sel(second!.range.endLine, 40, second!.range.endLine, 40)];
    press(20);
    press(1);

    expect(state.keyPrevented).toBe(1);
  });

  // Alt+Up and Alt+Down move the lines of the selection over the next line.
  it('refuses a line move over the edge of the step', () => {
    const { press, state } = setup({ selected: second!.key }, 30, true);
    const { startLine, endLine } = second!.range;

    state.current = [sel(startLine, 3, startLine, 3)];
    press(16, { altKey: true });
    expect(state.keyPrevented).toBe(1);
    press(18, { altKey: true });
    press(16, { altKey: true, shiftKey: true });
    expect(state.keyPrevented).toBe(1);

    state.current = [sel(startLine, 1, endLine, 5)];
    press(18, { altKey: true });
    expect(state.keyPrevented).toBe(2);
  });

  it('stops guarding when the scope changes', () => {
    const { state, rerender, args } = setup(
      { selected: second!.key },
      30,
      true
    );

    rerender({ ...args, selected: second!.key, scope: 'file' });

    expect(state.disposed).toBe(3);
  });
});

const lastOf = <T,>(list: T[]): T | undefined => list[list.length - 1];

describe('shiftVisibleLines', () => {
  const at = (
    line: number,
    column: number,
    endLine = line,
    endColumn = column
  ) =>
    ({
      startLineNumber: line,
      startColumn: column,
      endLineNumber: endLine,
      endColumn: endColumn,
    }) as const;
  const lines = { first: 10, last: 14 };

  it('stretches the lines when Enter ends the last line', () => {
    expect(
      shiftVisibleLines(lines, [{ range: at(14, 30), text: '\n    ' }])
    ).toEqual({ first: 10, last: 15 });
  });

  it('stretches the lines when Enter is typed at the start of the first line', () => {
    expect(
      shiftVisibleLines(lines, [{ range: at(10, 1), text: '\n' }])
    ).toEqual({ first: 10, last: 15 });
  });

  it('counts every line of a paste', () => {
    expect(
      shiftVisibleLines(lines, [{ range: at(14, 30), text: 'a\nb\nc\nd' }])
    ).toEqual({ first: 10, last: 17 });
  });

  it('shrinks the lines when lines are deleted, down to one line', () => {
    expect(
      shiftVisibleLines(lines, [{ range: at(11, 1, 13, 5), text: '' }])
    ).toEqual({ first: 10, last: 12 });
    expect(
      shiftVisibleLines(lines, [{ range: at(10, 1, 14, 30), text: '' }])
    ).toEqual({ first: 10, last: 10 });
  });

  it('keeps the lines for a change in one line', () => {
    expect(
      shiftVisibleLines(lines, [{ range: at(12, 3), text: 'abc' }])
    ).toEqual(lines);
  });

  it('moves the lines for a change before them, and keeps them for one after', () => {
    expect(
      shiftVisibleLines(lines, [{ range: at(3, 1), text: 'id: x\n' }])
    ).toEqual({ first: 11, last: 15 });
    expect(
      shiftVisibleLines(lines, [{ range: at(20, 1), text: '\n' }])
    ).toEqual(lines);
  });

  it('gives null for a change across an edge', () => {
    expect(
      shiftVisibleLines(lines, [{ range: at(1, 1, 30, 1), text: 'x' }])
    ).toBeNull();
    expect(
      shiftVisibleLines(lines, [{ range: at(12, 1, 20, 1), text: '' }])
    ).toBeNull();
  });
});

describe('step view while the user types', () => {
  const sel = (line: number, column: number) => ({
    selectionStartLineNumber: line,
    selectionStartColumn: column,
    positionLineNumber: line,
    positionColumn: column,
  });
  const last = second!.range.endLine;

  // Enter at the end of the last line makes a line of the step. The cursor
  // lands on it, and the guard must not move the cursor back.
  it('lets the cursor reach the line that Enter makes at the end of the step', async () => {
    const { change, select, state } = setup(
      { selected: second!.key },
      30,
      true
    );

    change([last, 40, last, 40], '\n    ');
    select(sel(last + 1, 5));

    expect(state.selections).toEqual([]);
    await Promise.resolve();
    expect(lastOf(state.hidden)).toMatchObject({ startLineNumber: last + 2 });
  });

  it('keeps following the step on later lines and on a paste', async () => {
    const { change, select, state } = setup(
      { selected: second!.key },
      30,
      true
    );

    change([last, 40, last, 40], '\n');
    select(sel(last + 1, 1));
    change([last + 1, 1, last + 1, 1], 'a\nb\nc');
    select(sel(last + 3, 2));
    await Promise.resolve();

    expect(state.selections).toEqual([]);
    expect(lastOf(state.hidden)).toMatchObject({ startLineNumber: last + 4 });
    // The new end holds: the line after it is still out of reach.
    select(sel(last + 6, 1));
    expect(state.selections).toEqual([sel(last + 3, 40)]);
  });

  it('stretches the lines when Enter is typed at the start of the first line', async () => {
    const { change, select, state } = setup(
      { selected: second!.key },
      30,
      true
    );

    change([second!.range.startLine, 1, second!.range.startLine, 1], '\n');
    select(sel(second!.range.startLine + 1, 1));
    await Promise.resolve();

    expect(state.selections).toEqual([]);
    // The first line stays the first line of the view.
    expect(state.hidden[0]).toMatchObject({
      endLineNumber: second!.range.startLine - 1,
    });
  });

  // The model leaves out the blank lines around a step. A blank line that
  // the user made next to the step stays in the view when the model catches up.
  it('keeps a blank line made above the step when the model reads the new text', async () => {
    const start = second!.range.startLine;
    const edited = yamlArraySteps.replace(
      '  - name: transform',
      '\n  - name: transform'
    );
    const { change, state, rerender, args } = setup(
      { selected: second!.key },
      31,
      true,
      () => edited
    );
    change([start, 1, start, 1], '\n');
    await Promise.resolve();

    const next = parseDagDoc(edited);
    expect(next.steps[1]!.range.startLine).toBe(start + 1);
    rerender({ ...args, model: next, selected: next.steps[1]!.key });

    // The blank line is still in the first line of the view.
    expect(state.hidden[0]).toMatchObject({ endLineNumber: start - 1 });
    expect(state.hidden[1]).toMatchObject({
      startLineNumber: next.steps[1]!.range.endLine + 1,
    });
  });

  it('keeps a blank line made after the step when the model reads the new text', async () => {
    const end = second!.range.endLine;
    const edited = yamlArraySteps
      .split('\n')
      .flatMap((line, index) => (index === end - 1 ? [line, ''] : [line]))
      .join('\n');
    const { change, state, rerender, args } = setup(
      { selected: second!.key },
      31,
      true,
      () => edited
    );
    change([end, 40, end, 40], '\n');
    await Promise.resolve();

    const next = parseDagDoc(edited);
    rerender({ ...args, model: next });

    // The model ends the step at the same line: the new line stays in view.
    expect(next.steps[1]!.range.endLine).toBe(end);
    expect(lastOf(state.hidden)).toMatchObject({ startLineNumber: end + 2 });
  });

  it('forgets the lines of another step', async () => {
    const { change, state, rerender, args } = setup(
      { selected: second!.key },
      31,
      true,
      () => yamlArraySteps
    );
    change([second!.range.endLine, 40, second!.range.endLine, 40], '\n');
    await Promise.resolve();

    rerender({ ...args, selected: third!.key });

    expect(state.hidden[0]).toMatchObject({
      endLineNumber: third!.range.startLine - 1,
    });
  });

  it('numbers the lines from the first line of the step', () => {
    const { state, unmount } = setup({ selected: second!.key }, 30, true);

    const numbers = lastOf(state.lineNumbers) as (line: number) => string;
    expect(numbers(second!.range.startLine)).toBe('1');
    expect(numbers(second!.range.startLine + 3)).toBe('4');

    unmount();
    expect(lastOf(state.lineNumbers)).toBe('on');
  });

  // The editor component applies its own options again at times: the cursor
  // and the edits show the numbers of the step again, with one function.
  it('shows the numbers again after an edit or a move, with the same function', () => {
    const { change, select, state } = setup(
      { selected: second!.key },
      30,
      true
    );
    const before = state.lineNumbers.length;

    change([last, 40, last, 40], 'x');
    select(sel(second!.range.startLine, 3));

    expect(state.lineNumbers.length).toBeGreaterThan(before);
    expect(new Set(state.lineNumbers).size).toBe(1);
  });

  it('keeps the number of the whole file in the file scope', () => {
    const { state } = setup({ selected: second!.key, scope: 'file' }, 30, true);
    expect(state.lineNumbers).toEqual([]);
  });
});

describe('step view change bars', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  const lines = yamlArraySteps.split('\n');
  const block = (step: typeof second) =>
    lines.slice(step!.range.startLine - 1, step!.range.endLine).join('\n');
  const classes = (state: { bars: unknown[] }) =>
    (
      state.bars as {
        range: { startLineNumber: number };
        options: { linesDecorationsClassName: string };
      }[]
    ).map((bar) => [
      bar.range.startLineNumber,
      bar.options.linesDecorationsClassName,
    ]);

  it('draws no bar for a step that is the saved step', async () => {
    const { state } = setup(
      { selected: second!.key, baseline: block(second) },
      30,
      true
    );
    await vi.advanceTimersByTimeAsync(150);

    expect(classes(state)).toEqual([]);
  });

  it('marks a modified line with the number of the whole file', async () => {
    const edited = yamlArraySteps.replace('sort data.csv', 'sort -u data.csv');
    const { state } = setup(
      { selected: second!.key, baseline: block(second) },
      30,
      true,
      () => edited
    );
    await vi.advanceTimersByTimeAsync(150);

    const target =
      lines.findIndex((line) => line.includes('sort data.csv')) + 1;
    expect(classes(state)).toEqual([[target, 'dag-change-modified']]);
  });

  it('marks every line of a step that the saved text does not have', async () => {
    const { state } = setup(
      { selected: second!.key, baseline: null },
      30,
      true
    );
    await vi.advanceTimersByTimeAsync(150);

    const marks = classes(state);
    expect(marks).toHaveLength(
      second!.range.endLine - second!.range.startLine + 1
    );
    expect(marks.every(([, name]) => name === 'dag-change-added')).toBe(true);
    expect(marks[0]![0]).toBe(second!.range.startLine);
  });

  it('draws no bar for blank lines and trailing spaces', async () => {
    const edited = yamlArraySteps.replace(
      '    depends: [fetch]\n',
      '    depends: [fetch]   \n\n'
    );
    const { state, change } = setup(
      { selected: second!.key, baseline: block(second) },
      30,
      true,
      () => edited
    );
    // The editor reports the new line, as it does for the typed Enter.
    const line = lines.findIndex((text) => text.includes('depends: [fetch]'));
    change([line + 1, 20, line + 1, 20], '   \n');
    await vi.advanceTimersByTimeAsync(150);

    expect(classes(state)).toEqual([]);
  });

  it('draws nothing without a baseline', async () => {
    const { state } = setup({ selected: second!.key }, 30, true);
    await vi.advanceTimersByTimeAsync(150);

    expect(state.collections).toBe(1);
  });
});
