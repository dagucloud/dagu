// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef } from 'react';
import { isSameYaml } from '@/features/visual-editor/doc/format';
import { computeLineChanges, type LineChanges } from './lineChanges';

/** The margin classes of the change bars; global.css draws them. */
export const CHANGE_BAR_CLASS = {
  added: 'dag-change-added',
  modified: 'dag-change-modified',
  deletedAbove: 'dag-change-deleted-above',
  deletedBelow: 'dag-change-deleted-below',
} as const;

/** The delay between an edit and the new bars. */
export const CHANGE_BAR_DELAY_MS = 100;

interface BarDecoration {
  range: {
    startLineNumber: number;
    startColumn: number;
    endLineNumber: number;
    endColumn: number;
  };
  options: { linesDecorationsClassName: string; stickiness?: number };
}

/** What the bars need of a Monaco editor. */
export interface ChangeBarEditor {
  createDecorationsCollection?(decorations: BarDecoration[]): {
    set(decorations: BarDecoration[]): void;
    clear(): void;
  };
}

/** The margin decorations of a set of changes. */
export function changeBarDecorations(changes: LineChanges): BarDecoration[] {
  const at = (line: number, className: string): BarDecoration => ({
    range: {
      startLineNumber: line,
      startColumn: 1,
      endLineNumber: line,
      endColumn: 1,
    },
    // 1 is NeverGrowsWhenTypingAtEdges: typing does not stretch a bar.
    options: { linesDecorationsClassName: className, stickiness: 1 },
  });
  return [
    ...changes.added.map((line) => at(line, CHANGE_BAR_CLASS.added)),
    ...changes.modified.map((line) => at(line, CHANGE_BAR_CLASS.modified)),
    ...changes.deleted.map((mark) =>
      at(
        mark.line,
        mark.below
          ? CHANGE_BAR_CLASS.deletedBelow
          : CHANGE_BAR_CLASS.deletedAbove
      )
    ),
  ];
}

/**
 * Draws the change bars of an editor: a bar in the margin for each line of
 * `text` that differs from `baseline`, as a code editor does for the lines
 * that changed since the last save. The bars follow the text after a short
 * delay, and are gone when the texts are equal or `baseline` is undefined.
 */
export function useChangeBars(
  editor: ChangeBarEditor | null,
  baseline: string | undefined,
  text: string
): void {
  const collection = useRef<ReturnType<
    NonNullable<ChangeBarEditor['createDecorationsCollection']>
  > | null>(null);
  const owner = useRef<ChangeBarEditor | null>(null);

  useEffect(() => {
    if (owner.current !== editor) {
      collection.current?.clear();
      collection.current = null;
      owner.current = editor;
    }
    if (!editor?.createDecorationsCollection) return;
    if (baseline === undefined || baseline === text) {
      collection.current?.clear();
      return;
    }
    const timer = window.setTimeout(() => {
      // A text that is the same document, but for blank lines and trailing
      // spaces, has no bar.
      const decorations = isSameYaml(baseline, text)
        ? []
        : changeBarDecorations(computeLineChanges(baseline, text));
      if (collection.current) collection.current.set(decorations);
      else
        collection.current =
          editor.createDecorationsCollection?.(decorations) ?? null;
    }, CHANGE_BAR_DELAY_MS);
    return () => window.clearTimeout(timer);
  }, [editor, baseline, text]);

  useEffect(
    () => () => {
      collection.current?.clear();
      collection.current = null;
    },
    []
  );
}
