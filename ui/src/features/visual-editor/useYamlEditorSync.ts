// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useLayoutEffect, useRef, type RefObject } from 'react';
import {
  changeBarDecorations,
  CHANGE_BAR_DELAY_MS,
} from '@/features/dags/components/dag-editor/useChangeBars';
import { computeBlockChanges } from '@/features/dags/components/dag-editor/lineChanges';
import type { DagDocModel, StepKey, StepView } from './contracts';
import type { CodeEditorLike } from './useEditSession';

/** Which part of the text the YAML drawer shows. */
export type YamlScope = 'step' | 'file';

interface LineRange {
  startLineNumber: number;
  startColumn: number;
  endLineNumber: number;
  endColumn: number;
}

interface Disposable {
  dispose(): void;
}

interface Decoration {
  range: LineRange;
  options: {
    isWholeLine?: boolean;
    className?: string;
    stickiness?: number;
  };
}

/** What the sync needs of a Monaco editor, beyond what edits need. */
export interface SyncableEditor extends CodeEditorLike {
  setHiddenAreas?(ranges: LineRange[]): void;
  createDecorationsCollection?(decorations: Decoration[]): {
    set(decorations: Decoration[]): void;
    clear(): void;
  };
  onDidChangeCursorPosition?(
    listener: (event: {
      position: { lineNumber: number };
      /** 'keyboard' and 'mouse' for the user; 'api' and 'modelChange' else. */
      source?: string;
    }) => void
  ): Disposable;
  getPosition?(): { lineNumber: number } | null;
  onDidChangeModelContent?(
    listener: (event: { changes: { range: LineRange; text: string }[] }) => void
  ): Disposable;
  updateOptions?(options: {
    lineNumbers: 'on' | 'off' | ((lineNumber: number) => string);
  }): void;
  revealLineInCenterIfOutsideViewport?(line: number): void;
  revealLine?(line: number): void;
  onDidChangeCursorSelection?(
    listener: (event: {
      selection: CursorSelection;
      secondarySelections: CursorSelection[];
    }) => void
  ): Disposable;
  getSelections?(): CursorSelection[] | null;
  setSelections?(selections: CursorSelection[]): void;
  onKeyDown?(
    listener: (event: {
      keyCode: number;
      ctrlKey?: boolean;
      metaKey?: boolean;
      altKey?: boolean;
      shiftKey?: boolean;
      preventDefault(): void;
      stopPropagation(): void;
    }) => void
  ): Disposable;
}

/** A Monaco selection: the anchor and the moving end. */
export interface CursorSelection {
  selectionStartLineNumber: number;
  selectionStartColumn: number;
  positionLineNumber: number;
  positionColumn: number;
}

/** Monaco `KeyCode.Backspace`, `Delete`, `UpArrow` and `DownArrow`. */
const KEY_BACKSPACE = 1;
const KEY_DELETE = 20;
const KEY_UP = 16;
const KEY_DOWN = 18;

interface Spot {
  line: number;
  column: number;
}

/** Moves a position that is in a hidden area to the nearest visible edge. */
function clampSpot(
  spot: Spot,
  first: number,
  last: number,
  lastMaxColumn: number
): Spot {
  if (spot.line < first) return { line: first, column: 1 };
  if (spot.line > last) return { line: last, column: lastMaxColumn };
  return spot;
}

/**
 * The selections cut to the lines of the step, or null when none reaches a
 * hidden line. Select all, a drag past the edge and a multi-cursor select
 * stay inside the step, so an edit at the cursor cannot touch the hidden
 * lines.
 */
export function clampSelectionsToLines(
  selections: CursorSelection[],
  first: number,
  last: number,
  lastMaxColumn: number
): CursorSelection[] | null {
  let changed = false;
  const next = selections.map((item) => {
    const anchor = clampSpot(
      {
        line: item.selectionStartLineNumber,
        column: item.selectionStartColumn,
      },
      first,
      last,
      lastMaxColumn
    );
    const caret = clampSpot(
      { line: item.positionLineNumber, column: item.positionColumn },
      first,
      last,
      lastMaxColumn
    );
    if (
      anchor.line !== item.selectionStartLineNumber ||
      anchor.column !== item.selectionStartColumn ||
      caret.line !== item.positionLineNumber ||
      caret.column !== item.positionColumn
    ) {
      changed = true;
    }
    return {
      selectionStartLineNumber: anchor.line,
      selectionStartColumn: anchor.column,
      positionLineNumber: caret.line,
      positionColumn: caret.column,
    };
  });
  return changed ? next : null;
}

/** The class of the lines of the selected step. */
export const ACTIVE_BLOCK_CLASS = 've-block-active';
const MAX_COLUMN = 1_000_000;

function everyStep(model: DagDocModel): StepView[] {
  return [...model.steps, ...model.handlers];
}

/** The step whose entry holds `line`, or null. */
export function stepAtLine(model: DagDocModel, line: number): StepKey | null {
  const hit = everyStep(model).find(
    (step) => line >= step.range.startLine && line <= step.range.endLine
  );
  return hit ? hit.key : null;
}

/**
 * The lines to hide so that only `step` shows: everything before it and
 * everything after it. Empty when it is the whole text.
 */
export function hiddenAreasAround(
  step: Pick<StepView, 'range'>,
  lineCount: number
): LineRange[] {
  const areas: LineRange[] = [];
  if (step.range.startLine > 1) {
    areas.push({
      startLineNumber: 1,
      startColumn: 1,
      endLineNumber: step.range.startLine - 1,
      endColumn: MAX_COLUMN,
    });
  }
  if (step.range.endLine < lineCount) {
    areas.push({
      startLineNumber: step.range.endLine + 1,
      startColumn: 1,
      endLineNumber: lineCount,
      endColumn: MAX_COLUMN,
    });
  }
  return areas;
}

/** The first and the last line of the lines that the step view shows. */
export interface VisibleLines {
  first: number;
  last: number;
}

/**
 * Where the shown lines are after an edit. An edit that starts inside them
 * stretches or shrinks them, so that a line made by Enter at the end of the
 * last line, or a paste of many lines, stays in the view; an edit before them
 * moves them. Null when an edit reaches across an edge: the model decides.
 * The changes use the lines of the text before the edit, as Monaco gives them.
 */
export function shiftVisibleLines(
  lines: VisibleLines,
  changes: readonly { range: LineRange; text: string }[]
): VisibleLines | null {
  let first = lines.first;
  let last = lines.last;
  for (const change of changes) {
    const start = change.range.startLineNumber;
    const end = change.range.endLineNumber;
    const delta = change.text.split('\n').length - 1 - (end - start);
    if (end < lines.first) {
      first += delta;
      last += delta;
    } else if (start >= lines.first && start <= lines.last) {
      if (end > lines.last) return null;
      last += delta;
    } else if (start > lines.last) {
      continue;
    } else {
      return null;
    }
  }
  return { first, last: Math.max(first, last) };
}

export interface YamlEditorSyncArgs {
  editorRef: RefObject<CodeEditorLike | null>;
  /** True while the YAML drawer shows. Nothing is shown or heard otherwise. */
  visible: boolean;
  model: DagDocModel;
  /** The model is the last good one, and its lines may not fit the text. */
  frozen: boolean;
  selected: StepKey | null;
  scope: YamlScope;
  /**
   * The saved text of the step that the step scope shows: the bars in the
   * margin mark the lines that differ from it. Null when the step is not in
   * the saved text (every line is new). Undefined shows no bar.
   */
  baseline?: string | null;
  /**
   * The cursor moved into the entry of a step, or out of every step.
   * `byUser` is false when the editor moved the cursor itself, as it does
   * when the text changes under it.
   */
  onCursorStep(step: StepKey | null, byUser: boolean): void;
}

/**
 * Ties the text editor to the canvas: the lines of the selected step are
 * lit in the file scope, the cursor tells the host which step it is in, and in the step
 * scope only the selected step shows.
 */
export function useYamlEditorSync({
  editorRef,
  visible,
  model,
  frozen,
  selected,
  scope,
  baseline,
  onCursorStep,
}: YamlEditorSyncArgs): void {
  // The listeners live as long as the drawer shows and read the latest values.
  const live = useRef({ model, onCursorStep, baseline });
  useLayoutEffect(() => {
    live.current = { model, onCursorStep, baseline };
  });
  // The editor loads after the drawer: the host renders again when it mounts,
  // and the effects below start again with it.
  const mounted = editorRef.current;

  useEffect(() => {
    const editor = editorRef.current as SyncableEditor | null;
    if (!visible || !editor?.getModel()) {
      return;
    }
    const subscriptions: Disposable[] = [];
    const cursor = editor.onDidChangeCursorPosition?.((event) =>
      live.current.onCursorStep(
        stepAtLine(live.current.model, event.position.lineNumber),
        event.source === undefined ||
          event.source === 'keyboard' ||
          event.source === 'mouse'
      )
    );
    if (cursor) subscriptions.push(cursor);
    return () => subscriptions.forEach((item) => item.dispose());
  }, [editorRef, mounted, visible]);

  const decorations = useRef<ReturnType<
    NonNullable<SyncableEditor['createDecorationsCollection']>
  > | null>(null);

  useEffect(() => {
    const editor = editorRef.current as SyncableEditor | null;
    if (
      !visible ||
      !editor?.getModel() ||
      !editor.createDecorationsCollection
    ) {
      return;
    }
    const next: Decoration[] = [];
    for (const step of everyStep(model)) {
      const { startLine, endLine } = step.range;
      // The step scope shows only the selected step: no need to light it.
      if (step.key === selected && scope === 'file') {
        next.push({
          range: {
            startLineNumber: startLine,
            startColumn: 1,
            endLineNumber: endLine,
            endColumn: 1,
          },
          options: { isWholeLine: true, className: ACTIVE_BLOCK_CLASS },
        });
      }
    }
    if (!decorations.current) {
      decorations.current = editor.createDecorationsCollection(next);
    } else {
      decorations.current.set(next);
    }
  }, [editorRef, mounted, visible, model, selected, scope]);

  useEffect(
    () => () => {
      decorations.current?.clear();
      decorations.current = null;
    },
    [visible]
  );

  const step = selected
    ? everyStep(model).find((item) => item.key === selected)
    : undefined;
  const startLine = step?.range.startLine;
  const endLine = step?.range.endLine;

  // Set by the effect of the step view: moves the shown lines to the lines
  // that the model gives, and the repaint of the bars.
  const resync = useRef<((first: number, last: number) => void) | null>(null);
  // The lines that the step view shows now, for the step with this key.
  const shownLines = useRef<{
    key: StepKey | null;
    first: number;
    last: number;
  } | null>(null);
  const needsResync = useRef(false);
  const repaintBars = useRef<(() => void) | null>(null);

  useEffect(() => {
    const editor = editorRef.current as SyncableEditor | null;
    const textModel = editor?.getModel() as
      | (ReturnType<CodeEditorLike['getModel']> & {
          getLineCount?(): number;
          getLineMaxColumn?(line: number): number;
        })
      | null
      | undefined;
    if (!editor?.setHiddenAreas || !textModel) {
      return;
    }
    const lineCount = textModel.getLineCount?.() ?? 0;
    const alone =
      visible &&
      scope === 'step' &&
      !frozen &&
      startLine !== undefined &&
      endLine !== undefined &&
      lineCount > 0;
    // The lines that show. The model leaves out the blank lines around a
    // step; lines that the user made there stay in the view, so that a line
    // that Enter makes next to the step does not vanish when the model reads
    // the new text.
    let first = startLine ?? 1;
    let last = endLine ?? 1;
    if (alone) {
      const before = shownLines.current;
      if (before && before.key === selected) {
        const text = textModel.getValue().split('\n');
        const blank = (from: number, to: number) =>
          text.slice(from - 1, to).every((line) => line.trim() === '');
        if (before.first < first && blank(before.first, first - 1)) {
          first = before.first;
        }
        if (before.last > last && blank(last + 1, before.last)) {
          last = Math.min(before.last, lineCount);
        }
      }
      shownLines.current = { key: selected, first, last };
    } else {
      shownLines.current = null;
    }
    editor.setHiddenAreas(
      alone
        ? hiddenAreasAround(
            { range: { startLine: first, endLine: last } as StepView['range'] },
            lineCount
          )
        : []
    );
    // In the whole file, a selection that the cursor made stays where the
    // cursor is: the editor does not scroll to the start of the step.
    const cursorLine = editor.getPosition?.()?.lineNumber;
    const cursorInside =
      scope === 'file' &&
      cursorLine !== undefined &&
      startLine !== undefined &&
      endLine !== undefined &&
      cursorLine >= startLine &&
      cursorLine <= endLine;
    if (visible && startLine !== undefined && !cursorInside) {
      editor.revealLineInCenterIfOutsideViewport?.(startLine);
    }
    const guards: Disposable[] = [];
    let bars: { set(d: Decoration[]): void; clear(): void } | null = null;
    let barTimer: number | undefined;
    let closed = false;
    if (alone) {
      // The lines follow the edits of the user: a line that Enter makes at
      // the end of the step is a line of the step, whatever the model says
      // before it has read the new text.
      const lastMaxColumn = () => textModel.getLineMaxColumn?.(last) ?? 1;
      // The first line of the step is line 1. The function keeps its identity
      // while `first` holds, so that showing it again changes nothing: the
      // editor component applies its own options again when it mounts, and
      // the cursor and the edits show the numbers again after that.
      const numbersFrom = (from: number) => (line: number) =>
        String(line - from + 1);
      let numbers = numbersFrom(first);
      const showNumbers = () => {
        numbers = first === shownFirst ? numbers : numbersFrom(first);
        shownFirst = first;
        editor.updateOptions?.({ lineNumbers: numbers });
      };
      let shownFirst = first;
      showNumbers();
      const paintBars = () => {
        const saved = live.current.baseline;
        if (saved === undefined || !editor.createDecorationsCollection) {
          bars?.clear();
          return;
        }
        const block = textModel
          .getValue()
          .split('\n')
          .slice(first - 1, last)
          .map((line) => line.replace(/\r$/, ''))
          .join('\n');
        const decorations = changeBarDecorations(
          computeBlockChanges(saved, block, first)
        );
        if (bars) bars.set(decorations);
        else bars = editor.createDecorationsCollection(decorations);
      };
      const scheduleBars = () => {
        window.clearTimeout(barTimer);
        barTimer = window.setTimeout(() => {
          if (!closed) paintBars();
        }, CHANGE_BAR_DELAY_MS);
      };
      repaintBars.current = scheduleBars;
      scheduleBars();
      const reapply = () => {
        const count = textModel.getLineCount?.() ?? 0;
        editor.setHiddenAreas?.(
          hiddenAreasAround(
            { range: { startLine: first, endLine: last } as StepView['range'] },
            count
          )
        );
      };
      resync.current = (nextFirst, nextLast) => {
        const moved = nextFirst !== first;
        first = nextFirst;
        last = nextLast;
        shownLines.current = { key: selected, first, last };
        reapply();
        if (moved) showNumbers();
        scheduleBars();
      };
      // The editor holds the whole file: keep the cursor and the edits at it
      // to the lines that show. Edits of the whole text (undo, Replace All,
      // the edits of the canvas) still reach the hidden lines.
      const selection = editor.onDidChangeCursorSelection?.((event) => {
        const next = clampSelectionsToLines(
          [event.selection, ...event.secondarySelections],
          first,
          last,
          lastMaxColumn()
        );
        if (next) editor.setSelections?.(next);
        else showNumbers();
      });
      if (selection) guards.push(selection);
      const content = editor.onDidChangeModelContent?.((event) => {
        const next = shiftVisibleLines({ first, last }, event.changes);
        if (next === null) {
          needsResync.current = true;
        } else if (next.first !== first || next.last !== last) {
          first = next.first;
          last = next.last;
          shownLines.current = { key: selected, first, last };
          // The hidden areas follow the lines the same way; they are set
          // again once the editor has finished with this edit.
          queueMicrotask(() => {
            if (closed) return;
            reapply();
          });
        }
        showNumbers();
        scheduleBars();
      });
      if (content) guards.push(content);
      // Backspace at the first column of the step and Delete at the end of it
      // would join the step to a hidden line. Alt+Up on the first line and
      // Alt+Down on the last swap a line with a hidden one.
      const keys = editor.onKeyDown?.((event) => {
        const selections = editor.getSelections?.() ?? [];
        if (
          event.altKey &&
          !event.shiftKey &&
          (event.keyCode === KEY_UP || event.keyCode === KEY_DOWN)
        ) {
          const edge = event.keyCode === KEY_UP ? first : last;
          if (
            selections.some(
              (item) =>
                item.positionLineNumber === edge ||
                item.selectionStartLineNumber === edge
            )
          ) {
            event.preventDefault();
            event.stopPropagation();
          }
          return;
        }
        if (event.keyCode !== KEY_BACKSPACE && event.keyCode !== KEY_DELETE) {
          return;
        }
        const atEdge = selections.some((item) =>
          event.keyCode === KEY_BACKSPACE
            ? item.positionLineNumber === first &&
              item.selectionStartLineNumber === first &&
              item.positionColumn === 1 &&
              item.selectionStartColumn === 1
            : item.positionLineNumber === last &&
              item.selectionStartLineNumber === last &&
              item.positionColumn === lastMaxColumn() &&
              item.selectionStartColumn === lastMaxColumn()
        );
        if (atEdge) {
          event.preventDefault();
          event.stopPropagation();
        }
      });
      if (keys) guards.push(keys);
    }
    return () => {
      closed = true;
      window.clearTimeout(barTimer);
      bars?.clear();
      resync.current = null;
      repaintBars.current = null;
      guards.forEach((item) => item.dispose());
      if (alone) editor.updateOptions?.({ lineNumbers: 'on' });
      editor.setHiddenAreas?.([]);
    };
  }, [
    editorRef,
    mounted,
    visible,
    scope,
    frozen,
    selected,
    startLine,
    endLine,
  ]);

  // The model reads text that an edit changed across an edge of the step.
  useEffect(() => {
    if (
      needsResync.current &&
      startLine !== undefined &&
      endLine !== undefined
    ) {
      needsResync.current = false;
      resync.current?.(startLine, endLine);
    }
    // Only a new model is a new reading of the text.
  }, [model]);

  // The saved text of the step changed: the bars follow.
  useEffect(() => {
    repaintBars.current?.();
  }, [baseline]);
}
