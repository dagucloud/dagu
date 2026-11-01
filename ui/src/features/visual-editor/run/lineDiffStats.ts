// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

export interface LineDiffStats {
  added: number;
  removed: number;
}

// Above this many table cells the LCS is skipped (about 16 MB of memory).
const MAX_CELLS = 4_000_000;

function splitLines(text: string): string[] {
  if (text === '') return [];
  const lines = text.split(/\r?\n/);
  // A final line end does not start a new line.
  if (lines[lines.length - 1] === '') lines.pop();
  return lines;
}

/**
 * Count the lines removed from `original` and added in `edited`.
 * The result is exact (longest common subsequence of lines). For very large
 * inputs the result is an upper bound: the common head and tail are
 * excluded, and every line between them counts as changed.
 */
export function lineDiffStats(original: string, edited: string): LineDiffStats {
  const a = splitLines(original);
  const b = splitLines(edited);

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
  if (x.length === 0 || y.length === 0) {
    return { added: y.length, removed: x.length };
  }
  if ((x.length + 1) * (y.length + 1) > MAX_CELLS) {
    return { added: y.length, removed: x.length };
  }

  // Two-row LCS length.
  let prev = new Array<number>(y.length + 1).fill(0);
  let cur = new Array<number>(y.length + 1).fill(0);
  for (let i = 1; i <= x.length; i++) {
    for (let j = 1; j <= y.length; j++) {
      cur[j] =
        x[i - 1] === y[j - 1]
          ? (prev[j - 1] ?? 0) + 1
          : Math.max(prev[j] ?? 0, cur[j - 1] ?? 0);
    }
    [prev, cur] = [cur, prev];
  }
  const common = prev[y.length] ?? 0;
  return { added: y.length - common, removed: x.length - common };
}

/** The lines of `text`, each with its line end. */
function linesWithEnds(text: string): string[] {
  return text.match(/[^\n]*\n|[^\n]+$/g) ?? [];
}

/**
 * Where the first change is: its line (from 0) and the number of lines of
 * the longer text, or null when the texts are equal. A change of only a line
 * end is on the line that has it.
 */
export function firstChange(
  original: string,
  edited: string
): { line: number; lines: number } | null {
  if (original === edited) return null;
  const a = linesWithEnds(original);
  const b = linesWithEnds(edited);
  let head = 0;
  while (head < a.length && head < b.length && a[head] === b[head]) head++;
  return { line: head, lines: Math.max(a.length, b.length) };
}
