// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { createTextHistory } from '../history';

describe('createTextHistory', () => {
  it('starts empty', () => {
    const h = createTextHistory('a');
    expect(h.canUndo()).toBe(false);
    expect(h.canRedo()).toBe(false);
    expect(h.undo()).toBeNull();
    expect(h.redo()).toBeNull();
  });

  it('undoes and redoes in order', () => {
    const h = createTextHistory('a');
    h.push('b');
    h.push('c');
    expect(h.canUndo()).toBe(true);
    expect(h.undo()).toBe('b');
    expect(h.canRedo()).toBe(true);
    expect(h.undo()).toBe('a');
    expect(h.undo()).toBeNull();
    expect(h.redo()).toBe('b');
    expect(h.redo()).toBe('c');
    expect(h.redo()).toBeNull();
    expect(h.canRedo()).toBe(false);
  });

  it('ignores a text equal to the current one', () => {
    const h = createTextHistory('a');
    h.push('a');
    expect(h.canUndo()).toBe(false);
    h.push('b');
    h.push('b');
    expect(h.undo()).toBe('a');
    expect(h.undo()).toBeNull();
  });

  it('compares with the current text after an undo', () => {
    const h = createTextHistory('a');
    h.push('b');
    h.undo();
    h.push('a');
    expect(h.canRedo()).toBe(true);
    expect(h.redo()).toBe('b');
  });

  it('clears redo on a push', () => {
    const h = createTextHistory('a');
    h.push('b');
    h.push('c');
    h.undo();
    h.push('d');
    expect(h.canRedo()).toBe(false);
    expect(h.undo()).toBe('b');
    expect(h.undo()).toBe('a');
  });

  it('keeps at most limit undo steps', () => {
    const h = createTextHistory('0', 3);
    for (const t of ['1', '2', '3', '4', '5']) h.push(t);
    expect(h.undo()).toBe('4');
    expect(h.undo()).toBe('3');
    expect(h.undo()).toBe('2');
    expect(h.undo()).toBeNull();
    expect(h.redo()).toBe('3');
  });

  it('defaults the limit to 100', () => {
    const h = createTextHistory('0');
    for (let i = 1; i <= 150; i++) h.push(String(i));
    let steps = 0;
    let last: string | null = null;
    for (let t = h.undo(); t !== null; t = h.undo()) {
      steps++;
      last = t;
    }
    expect(steps).toBe(100);
    expect(last).toBe('50');
  });

  it('resets everything', () => {
    const h = createTextHistory('a');
    h.push('b');
    h.push('c');
    h.undo();
    h.reset('z');
    expect(h.canUndo()).toBe(false);
    expect(h.canRedo()).toBe(false);
    h.push('y');
    expect(h.undo()).toBe('z');
  });

  it('coalesces pushes with one key', () => {
    const h = createTextHistory('a');
    h.push('b', 'type');
    h.push('bc', 'type');
    h.push('bcd', 'type');
    expect(h.undo()).toBe('a');
    expect(h.undo()).toBeNull();
    expect(h.redo()).toBe('bcd');
  });

  it('starts a new entry when the key changes', () => {
    const h = createTextHistory('a');
    h.push('b', 'x');
    h.push('c', 'y');
    h.push('d');
    h.push('e');
    expect(h.undo()).toBe('d');
    expect(h.undo()).toBe('c');
    expect(h.undo()).toBe('b');
    expect(h.undo()).toBe('a');
  });

  it('does not coalesce the first push of a key', () => {
    const h = createTextHistory('a');
    h.push('b');
    h.push('c', 'x');
    expect(h.undo()).toBe('b');
  });

  it('ends the chain on undo and redo', () => {
    const h = createTextHistory('a');
    h.push('b', 'x');
    h.push('c', 'x');
    h.undo();
    h.redo();
    h.push('d', 'x');
    expect(h.undo()).toBe('c');
    expect(h.undo()).toBe('a');
  });

  it('ends the chain on reset', () => {
    const h = createTextHistory('a');
    h.push('b', 'x');
    h.reset('z');
    h.push('y', 'x');
    expect(h.undo()).toBe('z');
  });

  it('ignores an equal text without breaking the chain', () => {
    const h = createTextHistory('a');
    h.push('b', 'x');
    h.push('b', 'x');
    h.push('c', 'x');
    expect(h.undo()).toBe('a');
  });

  it('keeps the limit with coalesced pushes', () => {
    const h = createTextHistory('0', 2);
    h.push('1', 'x');
    h.push('2', 'x');
    h.push('3');
    h.push('4');
    expect(h.undo()).toBe('3');
    expect(h.undo()).toBe('2');
    expect(h.undo()).toBeNull();
  });
});
