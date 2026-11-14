// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { isSameYaml } from '@/features/visual-editor/doc/format';

/** A point where lines of the saved text are missing in the current text. */
export interface DeletionMark {
  /** The line of the current text that the mark sits on. */
  line: number;
  /** True when the lines were after this line, false when before it. */
  below: boolean;
}

/** The lines of the current text that differ from the saved text. */
export interface LineChanges {
  /** Lines that the saved text does not have. */
  added: number[];
  /** Lines that replace other lines of the saved text. */
  modified: number[];
  /** Places where lines of the saved text are gone. */
  deleted: DeletionMark[];
}

// Above this many cells of the table the diff gives one block of changes.
const MAX_CELLS = 2_000_000;

function splitLines(text: string): string[] {
  return text.split('\n').map((line) => line.replace(/\r$/, ''));
}

interface Hunk {
  /** Index in the current lines of the first added line. */
  at: number;
  removed: number;
  added: number;
}

function hunksOf(x: string[], y: string[], offset: number): Hunk[] {
  if (x.length === 0 && y.length === 0) return [];
  if ((x.length + 1) * (y.length + 1) > MAX_CELLS) {
    return [{ at: offset, removed: x.length, added: y.length }];
  }
  // lcs[i][j]: the length of the longest common run of x[i:] and y[j:].
  const width = y.length + 1;
  const lcs = new Uint32Array((x.length + 1) * width);
  for (let i = x.length - 1; i >= 0; i--) {
    for (let j = y.length - 1; j >= 0; j--) {
      lcs[i * width + j] =
        x[i] === y[j]
          ? (lcs[(i + 1) * width + j + 1] ?? 0) + 1
          : Math.max(
              lcs[(i + 1) * width + j] ?? 0,
              lcs[i * width + j + 1] ?? 0
            );
    }
  }
  const hunks: Hunk[] = [];
  let open: Hunk | null = null;
  const close = () => {
    if (open) hunks.push(open);
    open = null;
  };
  let i = 0;
  let j = 0;
  while (i < x.length || j < y.length) {
    if (i < x.length && j < y.length && x[i] === y[j]) {
      close();
      i++;
      j++;
      continue;
    }
    open ??= { at: offset + j, removed: 0, added: 0 };
    const skipCurrent =
      j < y.length &&
      (i === x.length ||
        (lcs[i * width + j + 1] ?? 0) >= (lcs[(i + 1) * width + j] ?? 0));
    if (skipCurrent) {
      open.added++;
      j++;
    } else {
      open.removed++;
      i++;
    }
  }
  close();
  return hunks;
}

/**
 * The lines of `current` that differ from `saved`, as an editor shows them in
 * its margin: added lines, modified lines (a block of changed lines replaces
 * as many lines as it can) and the places where lines are gone. Both texts
 * split at line ends; a line number is 1-based. The texts are equal when the
 * three lists are empty.
 */
export function computeLineChanges(
  saved: string,
  current: string
): LineChanges {
  const changes: LineChanges = { added: [], modified: [], deleted: [] };
  if (saved === current) return changes;
  // A file that had no text: every line is new.
  if (saved === '') {
    changes.added = splitLines(current).map((_, index) => index + 1);
    return changes;
  }
  const a = splitLines(saved);
  const b = splitLines(current);
  let head = 0;
  while (head < a.length && head < b.length && a[head] === b[head]) head++;
  let tail = 0;
  while (
    tail < a.length - head &&
    tail < b.length - head &&
    a[a.length - 1 - tail] === b[b.length - 1 - tail]
  ) {
    tail++;
  }
  const x = a.slice(head, a.length - tail);
  const y = b.slice(head, b.length - tail);
  for (const hunk of hunksOf(x, y, head)) {
    const replaced = Math.min(hunk.removed, hunk.added);
    for (let k = 0; k < hunk.added; k++) {
      (k < replaced ? changes.modified : changes.added).push(hunk.at + k + 1);
    }
    if (hunk.removed > hunk.added) {
      if (hunk.added > 0) {
        changes.deleted.push({ line: hunk.at + hunk.added, below: true });
      } else if (hunk.at < b.length) {
        changes.deleted.push({ line: hunk.at + 1, below: false });
      } else {
        changes.deleted.push({ line: b.length, below: true });
      }
    }
  }
  return changes;
}

/**
 * The changes of one block of lines of the text, such as a step, against the
 * saved block. Line numbers are those of the whole text: `firstLine` is the
 * line of the first line of `current`. A saved block of null means the block
 * is new: every line is added. Blocks that are the same document, but for
 * blank lines or trailing spaces, have no change.
 */
export function computeBlockChanges(
  saved: string | null,
  current: string,
  firstLine: number
): LineChanges {
  if (saved !== null && isSameYaml(saved, current)) {
    return { added: [], modified: [], deleted: [] };
  }
  const changes = computeLineChanges(saved ?? '', current);
  const shift = firstLine - 1;
  return {
    added: changes.added.map((line) => line + shift),
    modified: changes.modified.map((line) => line + shift),
    deleted: changes.deleted.map((mark) => ({
      ...mark,
      line: mark.line + shift,
    })),
  };
}
