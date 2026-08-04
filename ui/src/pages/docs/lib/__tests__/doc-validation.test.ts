// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { validateDocPath } from '../doc-validation';

describe('validateDocPath', () => {
  it('accepts document path segments that start with underscores', () => {
    expect(validateDocPath('_index')).toEqual({ isValid: true });
    expect(validateDocPath('guides/_partial')).toEqual({ isValid: true });
  });

  it('continues to reject hidden dot files', () => {
    expect(validateDocPath('.hidden').isValid).toBe(false);
  });

  it('rejects paths longer than the backend document ID limit', () => {
    expect(validateDocPath('a'.repeat(252)).isValid).toBe(true);
    expect(validateDocPath('a'.repeat(253)).isValid).toBe(false);
  });

  it('rejects segments ending in a space or dot', () => {
    expect(validateDocPath('guides /intro')).toEqual({
      isValid: false,
      error: 'Path segments cannot end with a space or dot.',
    });
    expect(validateDocPath('guides/intro.')).toEqual({
      isValid: false,
      error: 'Path segments cannot end with a space or dot.',
    });
  });

  it('rejects paths with the markdown file extension', () => {
    expect(validateDocPath('guide.md')).toEqual({
      isValid: false,
      error: 'Path should not include the .md extension.',
    });
    expect(validateDocPath('guides/intro.MD')).toEqual({
      isValid: false,
      error: 'Path should not include the .md extension.',
    });
  });

  it('rejects Windows reserved device names', () => {
    expect(validateDocPath('CON')).toEqual({
      isValid: false,
      error: 'Path segments cannot use reserved device names.',
    });
    expect(validateDocPath('guides/lpt9.txt').isValid).toBe(false);
    expect(validateDocPath('console').isValid).toBe(true);
    expect(validateDocPath('com10').isValid).toBe(true);
  });
});
