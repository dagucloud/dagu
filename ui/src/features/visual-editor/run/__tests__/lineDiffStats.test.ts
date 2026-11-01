// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { firstChange, lineDiffStats } from '../lineDiffStats';

describe('lineDiffStats', () => {
  it('returns zero for equal texts', () => {
    expect(lineDiffStats('a\nb\n', 'a\nb\n')).toEqual({ added: 0, removed: 0 });
    expect(lineDiffStats('', '')).toEqual({ added: 0, removed: 0 });
  });

  it('counts added lines', () => {
    expect(lineDiffStats('a\nc\n', 'a\nb\nc\n')).toEqual({
      added: 1,
      removed: 0,
    });
  });

  it('counts removed lines', () => {
    expect(lineDiffStats('a\nb\nc\n', 'a\nc\n')).toEqual({
      added: 0,
      removed: 1,
    });
  });

  it('counts a changed line as one removed and one added', () => {
    expect(lineDiffStats('a\nb\nc\n', 'a\nx\nc\n')).toEqual({
      added: 1,
      removed: 1,
    });
  });

  it('handles interleaved changes', () => {
    expect(lineDiffStats('a\nb\nc\nd\ne\n', 'a\nc\nx\ne\ny\n')).toEqual({
      added: 2,
      removed: 2,
    });
  });

  it('handles empty sides', () => {
    expect(lineDiffStats('', 'a\nb\n')).toEqual({ added: 2, removed: 0 });
    expect(lineDiffStats('a\nb\n', '')).toEqual({ added: 0, removed: 2 });
  });

  it('ignores the final line end and CRLF', () => {
    expect(lineDiffStats('a\nb', 'a\r\nb\r\n')).toEqual({
      added: 0,
      removed: 0,
    });
  });

  it('falls back to an upper bound on very large middles', () => {
    const a = Array.from({ length: 3000 }, (_, i) => `a${i}`);
    const b = Array.from({ length: 3000 }, (_, i) => `b${i}`);
    const r = lineDiffStats(
      ['h', ...a, 't'].join('\n'),
      ['h', ...b, 't'].join('\n')
    );
    expect(r).toEqual({ added: 3000, removed: 3000 });
  });
});

describe('firstChange', () => {
  it('is null for equal texts', () => {
    expect(firstChange('a\nb\n', 'a\nb\n')).toBeNull();
  });

  it('is the first changed line and the line count of the longer text', () => {
    expect(firstChange('a\nb\nc\n', 'a\nx\nc\nd\n')).toEqual({
      line: 1,
      lines: 4,
    });
  });

  // The diff shows a change of only a line end on the line that has it.
  it('is the line whose line end changed', () => {
    expect(firstChange('a\nb', 'a\nb\n')).toEqual({ line: 1, lines: 2 });
    expect(firstChange('a\nb\nc\n', 'a\nb\r\nc\n')).toEqual({
      line: 1,
      lines: 3,
    });
  });
});
