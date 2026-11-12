// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { computeBlockChanges, computeLineChanges } from '../lineChanges';

const saved = 'a\nb\nc\nd\ne\n';

describe('computeLineChanges', () => {
  it('gives no change for equal texts', () => {
    expect(computeLineChanges(saved, saved)).toEqual({
      added: [],
      modified: [],
      deleted: [],
    });
  });

  it('marks an added line', () => {
    expect(computeLineChanges(saved, 'a\nb\nnew\nc\nd\ne\n')).toEqual({
      added: [3],
      modified: [],
      deleted: [],
    });
  });

  it('marks lines added at the start and at the end', () => {
    expect(computeLineChanges('a\nb', 'x\na\nb\ny')).toEqual({
      added: [1, 4],
      modified: [],
      deleted: [],
    });
  });

  it('marks a changed line as modified', () => {
    expect(computeLineChanges(saved, 'a\nB\nc\nd\ne\n')).toEqual({
      added: [],
      modified: [2],
      deleted: [],
    });
  });

  it('marks the lines that replace others as modified, then the extra ones as added', () => {
    expect(computeLineChanges(saved, 'a\nX\nY\nZ\nd\ne\n')).toEqual({
      added: [4],
      modified: [2, 3],
      deleted: [],
    });
  });

  it('marks the place of a deleted line above the line that follows', () => {
    expect(computeLineChanges(saved, 'a\nb\nd\ne\n')).toEqual({
      added: [],
      modified: [],
      deleted: [{ line: 3, below: false }],
    });
  });

  it('marks a deletion at the end of the text below the last line', () => {
    expect(computeLineChanges('a\nb\nc', 'a\nb')).toEqual({
      added: [],
      modified: [],
      deleted: [{ line: 2, below: true }],
    });
  });

  it('marks a deletion at the start above the first line', () => {
    expect(computeLineChanges('x\na\nb', 'a\nb')).toEqual({
      added: [],
      modified: [],
      deleted: [{ line: 1, below: false }],
    });
  });

  it('marks fewer new lines than old ones as modified and a deletion', () => {
    expect(computeLineChanges(saved, 'a\nX\ne\n')).toEqual({
      added: [],
      modified: [2],
      deleted: [{ line: 2, below: true }],
    });
  });

  it('keeps separate changes apart', () => {
    expect(computeLineChanges(saved, 'a\nB\nc\nd\nnew\ne\n')).toEqual({
      added: [5],
      modified: [2],
      deleted: [],
    });
  });

  it('shows the lines of an empty saved text as added', () => {
    expect(computeLineChanges('', 'a\nb').added).toEqual([1, 2]);
  });

  it('ignores a carriage return at the end of a line', () => {
    expect(computeLineChanges('a\r\nb\r\n', 'a\nb\n')).toEqual({
      added: [],
      modified: [],
      deleted: [],
    });
  });

  it('finds the common lines around a block that moved', () => {
    const changes = computeLineChanges('a\nb\nc\nd', 'a\nc\nb\nd');
    // One line moved: one line is added and one is gone.
    expect(changes.added.length + changes.modified.length).toBeGreaterThan(0);
    expect(changes.added.concat(changes.modified).every((l) => l >= 2)).toBe(
      true
    );
  });

  // The table of the diff has a limit: a very large change is one block.
  it('gives one block for a very large change', () => {
    const before = Array.from({ length: 2000 }, (_, i) => `a${i}`).join('\n');
    const after = Array.from({ length: 2000 }, (_, i) => `b${i}`).join('\n');
    const changes = computeLineChanges(
      `start\n${before}\nend`,
      `start\n${after}\nend`
    );
    expect(changes.modified).toHaveLength(2000);
    expect(changes.deleted).toEqual([]);
  });
});

describe('computeBlockChanges', () => {
  const step = '  - name: a\n    run: echo a\n    depends: [b]';

  it('gives the lines of the whole text, not of the block', () => {
    expect(
      computeBlockChanges(step, step.replace('echo a', 'echo A'), 12)
    ).toEqual({ added: [], modified: [13], deleted: [] });
    expect(computeBlockChanges(step, step + '\n    shell: sh', 12)).toEqual({
      added: [15],
      modified: [],
      deleted: [],
    });
    expect(
      computeBlockChanges(step, '  - name: a\n    depends: [b]', 12).deleted
    ).toEqual([{ line: 13, below: false }]);
  });

  it('marks every line of a block that is new', () => {
    expect(computeBlockChanges(null, step, 5)).toEqual({
      added: [5, 6, 7],
      modified: [],
      deleted: [],
    });
  });

  it('gives no change for the same block with blank lines and spaces', () => {
    expect(
      computeBlockChanges(step, step.replace('run:', '\n    run:') + '   \n', 3)
    ).toEqual({ added: [], modified: [], deleted: [] });
    expect(computeBlockChanges(step, step, 3)).toEqual({
      added: [],
      modified: [],
      deleted: [],
    });
  });

  it('marks a changed comment', () => {
    expect(computeBlockChanges(step, step + ' # x', 3).modified).toEqual([5]);
  });
});
