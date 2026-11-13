// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { findReferenceTokens, type ReferenceToken } from './tokens';

/**
 * A run of a prompt: text, a Markdown mark (`#`, `-`, `>`, a back tick, `**`)
 * or a `${...}` reference. The runs of a text join to the text, so that a
 * layer behind the text field has the same characters, and the same wrap, as
 * the field.
 */
export interface PromptSegment {
  kind: 'text' | 'mark' | 'ref';
  text: string;
  /** The reference that the run holds, for a `ref` run. */
  token?: ReferenceToken;
}

// The mark at the start of a line: a heading, a quote, a list item.
const LINE_MARK = /^[ \t]*(#{1,6}[ \t]+|>[ \t]?|[-*+][ \t]+|\d{1,9}[.)][ \t]+)/;
const FENCE = /^[ \t]*(```|~~~)/;
// Inline marks: a run of back ticks, `**`, `__`.
const INLINE_MARK = /`+|\*\*|__/g;

interface Mark {
  from: number;
  to: number;
}

// The Markdown marks of one line, as ranges of the line.
function lineMarks(line: string, inFence: boolean): Mark[] {
  if (FENCE.test(line)) return [{ from: 0, to: line.length }];
  if (inFence) return [];
  const marks: Mark[] = [];
  const lead = LINE_MARK.exec(line);
  if (lead) {
    const group = lead[1] ?? '';
    const from = lead[0].length - group.length;
    marks.push({ from, to: from + group.trimEnd().length });
  }
  for (const match of line.matchAll(INLINE_MARK)) {
    const from = match.index;
    if (marks.some((mark) => from >= mark.from && from < mark.to)) continue;
    marks.push({ from, to: from + match[0].length });
  }
  return marks;
}

/**
 * Splits a prompt into runs for a light highlight: references first, then the
 * Markdown marks outside the code fences. Text between runs is `text`. The
 * `text` of all runs, joined, is the input.
 */
export function highlightPrompt(text: string): PromptSegment[] {
  const refs = findReferenceTokens(text);
  const marks: Mark[] = [];
  let offset = 0;
  let inFence = false;
  for (const line of text.split('\n')) {
    for (const mark of lineMarks(line, inFence)) {
      marks.push({ from: offset + mark.from, to: offset + mark.to });
    }
    if (FENCE.test(line)) inFence = !inFence;
    offset += line.length + 1;
  }

  const segments: PromptSegment[] = [];
  const push = (kind: PromptSegment['kind'], from: number, to: number) => {
    if (to <= from) return;
    const last = segments[segments.length - 1];
    if (last && last.kind === kind && kind !== 'ref') {
      last.text += text.slice(from, to);
    } else {
      segments.push({ kind, text: text.slice(from, to) });
    }
  };
  let pos = 0;
  let r = 0;
  for (const mark of marks) {
    // The references before this mark.
    while (r < refs.length && (refs[r] as ReferenceToken).to <= mark.from) {
      const ref = refs[r] as ReferenceToken;
      push('text', pos, ref.from);
      segments.push({ kind: 'ref', text: ref.text, token: ref });
      pos = ref.to;
      r += 1;
    }
    // A mark inside a reference does not show.
    const inRef =
      r < refs.length &&
      (refs[r] as ReferenceToken).from < mark.to &&
      (refs[r] as ReferenceToken).to > mark.from;
    if (inRef || mark.from < pos) continue;
    push('text', pos, mark.from);
    push('mark', mark.from, mark.to);
    pos = mark.to;
  }
  for (; r < refs.length; r += 1) {
    const ref = refs[r] as ReferenceToken;
    if (ref.from < pos) continue;
    push('text', pos, ref.from);
    segments.push({ kind: 'ref', text: ref.text, token: ref });
    pos = ref.to;
  }
  push('text', pos, text.length);
  return segments;
}
