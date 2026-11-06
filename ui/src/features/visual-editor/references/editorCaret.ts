// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef, useState } from 'react';
import type { ReferenceCandidate } from '../contracts';
import type { ReferenceEditorState } from './editorState';
import { computeReferenceCompletions } from './monacoCompletion';

interface Position {
  lineNumber: number;
  column: number;
}

/** What the reference tools need of a Monaco editor. */
export interface CaretEditor {
  getModel(): {
    getValue(): string;
    getOffsetAt(position: Position): number;
    getPositionAt(offset: number): Position;
  } | null;
  getPosition?(): Position | null;
  setPosition?(position: Position): void;
  getSelection?(): {
    startLineNumber: number;
    startColumn: number;
    endLineNumber: number;
    endColumn: number;
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
  focus?(): void;
  onDidChangeCursorPosition?(listener: () => void): { dispose(): void };
}

// A cursor that moves through the text parses the document once it rests.
const CARET_REST_MS = 150;

/** The references of the field at the caret, or null at a key or a literal field. */
export function referencesAtCaret(
  editor: CaretEditor,
  state: ReferenceEditorState
): ReferenceCandidate[] | null {
  const model = editor.getModel();
  const position = editor.getPosition?.();
  if (!model?.getOffsetAt || !position) return null;
  return (
    computeReferenceCompletions(
      model.getValue(),
      model.getOffsetAt(position),
      state,
      { explicit: true }
    )?.items ?? null
  );
}

/**
 * The references of the field at the caret of `editor` while `active`, kept up
 * to date as the caret moves. Null when the caret is not in such a field, when
 * not `active`, and without an editor.
 */
export function useCaretReferences(
  editor: CaretEditor | null | undefined,
  active: boolean,
  state: ReferenceEditorState
): ReferenceCandidate[] | null {
  const [items, setItems] = useState<ReferenceCandidate[] | null>(null);
  const stateRef = useRef(state);
  stateRef.current = state;
  const { scope, model } = state;

  useEffect(() => {
    if (!active || !editor) {
      setItems(null);
      return;
    }
    const update = () => setItems(referencesAtCaret(editor, stateRef.current));
    update();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const cursor = editor.onDidChangeCursorPosition?.(() => {
      clearTimeout(timer);
      timer = setTimeout(update, CARET_REST_MS);
    });
    return () => {
      clearTimeout(timer);
      cursor?.dispose();
    };
  }, [active, editor, scope, model]);

  return items;
}

/**
 * Puts `token` at the selection of `editor`, as one undo step, with the caret
 * after it and the focus in the editor. False without an editor or a selection,
 * so that the host can copy the token.
 */
export function insertAtCaret(
  editor: CaretEditor | null | undefined,
  token: string
): boolean {
  const model = editor?.getModel();
  const selection = editor?.getSelection?.();
  if (!editor || !model || !selection) return false;
  const start = model.getOffsetAt({
    lineNumber: selection.startLineNumber,
    column: selection.startColumn,
  });
  editor.pushUndoStop();
  editor.executeEdits('reference', [{ range: selection, text: token }]);
  editor.pushUndoStop();
  editor.setPosition?.(model.getPositionAt(start + token.length));
  editor.focus?.();
  return true;
}
