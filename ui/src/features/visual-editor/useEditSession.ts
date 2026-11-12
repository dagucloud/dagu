// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useCallback,
  useLayoutEffect,
  useRef,
  useState,
  type RefObject,
} from 'react';
import type {
  DagEditOp,
  EditResult,
  StepNameMap,
  TextEdit,
  TextHistory,
} from './contracts';
import { applyEdit, createTextHistory } from './doc';

interface Position {
  lineNumber: number;
  column: number;
}

/** The part of a Monaco code editor that the session uses. */
export interface CodeEditorLike {
  getModel(): {
    getValue(): string;
    getPositionAt(offset: number): Position;
    canUndo?(): boolean;
    canRedo?(): boolean;
  } | null;
  executeEdits(
    source: string,
    edits: {
      range: {
        startLineNumber: number;
        startColumn: number;
        endLineNumber: number;
        endColumn: number;
      };
      text: string;
    }[]
  ): boolean;
  pushUndoStop(): boolean;
  trigger(source: string, handlerId: string, payload: unknown): void;
}

export interface EditSessionArgs {
  text: string;
  /** Receives every new text: from an edit, an undo or a redo. */
  setText(text: string): void;
  names: StepNameMap;
  /**
   * The visible YAML editor. When there is one, edits go through it, so the
   * editor keeps one undo stack for typed and visual edits.
   */
  editorRef?: RefObject<CodeEditorLike | null>;
}

export interface EditSession {
  /** Applies one operation to the latest text. Ops in one task make one undo step. */
  apply(op: DagEditOp): EditResult;
  /**
   * Applies an edit that `compute` makes from the latest text, as `apply`
   * does: a clipboard paste or duplicate becomes one undo step of its own.
   */
  applyWith(compute: (text: string) => EditResult): EditResult;
  undo(): void;
  redo(): void;
  canUndo: boolean;
  canRedo: boolean;
}

const EDIT_SOURCE = 'dagu-visual-editor';

function toMonacoEdits(editor: CodeEditorLike, edits: TextEdit[]) {
  const model = editor.getModel();
  if (!model) {
    return [];
  }
  return edits.map((edit) => {
    const start = model.getPositionAt(edit.from);
    const end = model.getPositionAt(edit.to);
    return {
      range: {
        startLineNumber: start.lineNumber,
        startColumn: start.column,
        endLineNumber: end.lineNumber,
        endColumn: end.column,
      },
      text: edit.insert,
    };
  });
}

/**
 * Applies visual edit operations to the DAG text and keeps their undo
 * history. Without a visible editor the session keeps a snapshot history;
 * text changed elsewhere becomes an undo step of its own.
 */
export function useEditSession({
  text,
  setText,
  names,
  editorRef,
}: EditSessionArgs): EditSession {
  const [history] = useState<TextHistory>(() => createTextHistory(text));
  // The last text that the history knows.
  const recorded = useRef(text);
  // The newest text, also between two edits of one task, before a render.
  const latest = useRef(text);
  const [, setVersion] = useState(0);
  const batch = useRef<{ key: string } | null>(null);
  const batchCount = useRef(0);

  useLayoutEffect(() => {
    latest.current = text;
  }, [text]);

  const visibleEditor = useCallback((): CodeEditorLike | null => {
    const editor = editorRef?.current ?? null;
    return editor?.getModel() ? editor : null;
  }, [editorRef]);

  const recordExternal = useCallback(() => {
    if (latest.current !== recorded.current) {
      history.push(latest.current);
      recorded.current = latest.current;
    }
  }, [history]);

  const applyWith = useCallback(
    (compute: (text: string) => EditResult): EditResult => {
      const input = latest.current;
      const result = compute(input);
      if (!result.ok) {
        return result;
      }

      const editor = visibleEditor();
      const first = batch.current === null;
      if (first) {
        batchCount.current += 1;
        batch.current = { key: `edit-${batchCount.current}` };
        queueMicrotask(() => {
          batch.current = null;
          visibleEditor()?.pushUndoStop();
        });
      }

      if (editor && editor.getModel()?.getValue() === input) {
        if (first) {
          editor.pushUndoStop();
        }
        // A result without text edits replaces the whole text.
        const edits =
          result.edits.length > 0 || result.text === input
            ? result.edits
            : [{ from: 0, to: input.length, insert: result.text }];
        editor.executeEdits(EDIT_SOURCE, toMonacoEdits(editor, edits));
      } else {
        recordExternal();
        history.push(result.text, batch.current?.key);
        recorded.current = result.text;
      }
      latest.current = result.text;
      setText(result.text);
      setVersion((v) => v + 1);
      return result;
    },
    [history, recordExternal, setText, visibleEditor]
  );
  const apply = useCallback(
    (op: DagEditOp): EditResult =>
      applyWith((input) => applyEdit(input, op, { names })),
    [applyWith, names]
  );

  const move = useCallback(
    (direction: 'undo' | 'redo') => {
      const editor = visibleEditor();
      if (editor) {
        editor.trigger(EDIT_SOURCE, direction, null);
        return;
      }
      recordExternal();
      const next = direction === 'undo' ? history.undo() : history.redo();
      if (next === null) {
        return;
      }
      recorded.current = next;
      latest.current = next;
      setText(next);
      setVersion((v) => v + 1);
    },
    [history, recordExternal, setText, visibleEditor]
  );

  const undo = useCallback(() => move('undo'), [move]);
  const redo = useCallback(() => move('redo'), [move]);

  const editor = visibleEditor();
  const model = editor?.getModel();
  const canUndo = model
    ? (model.canUndo?.() ?? true)
    : history.canUndo() || text !== recorded.current;
  const canRedo = model
    ? (model.canRedo?.() ?? true)
    : history.canRedo() && text === recorded.current;

  return { apply, applyWith, undo, redo, canUndo, canRedo };
}
