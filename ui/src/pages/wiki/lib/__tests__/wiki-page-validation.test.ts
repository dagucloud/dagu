// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { validateWikiPagePath } from '../wiki-page-validation';

describe('validateWikiPagePath', () => {
  it('accepts Wiki page path segments that start with underscores', () => {
    expect(validateWikiPagePath('_index')).toEqual({ isValid: true });
    expect(validateWikiPagePath('guides/_partial')).toEqual({ isValid: true });
  });

  it('continues to reject hidden dot files', () => {
    expect(validateWikiPagePath('.hidden').isValid).toBe(false);
  });

  it('rejects paths longer than the backend Wiki page ID limit', () => {
    expect(validateWikiPagePath('a'.repeat(252)).isValid).toBe(true);
    expect(validateWikiPagePath('a'.repeat(253)).isValid).toBe(false);
  });

  it('rejects segments ending in a space or dot', () => {
    expect(validateWikiPagePath('guides /intro')).toEqual({
      isValid: false,
      error: 'Path segments cannot end with a space or dot.',
    });
    expect(validateWikiPagePath('guides/intro.')).toEqual({
      isValid: false,
      error: 'Path segments cannot end with a space or dot.',
    });
  });

  it('rejects paths with the markdown file extension', () => {
    expect(validateWikiPagePath('guide.md')).toEqual({
      isValid: false,
      error: 'Path should not include the .md extension.',
    });
    expect(validateWikiPagePath('guides/intro.MD')).toEqual({
      isValid: false,
      error: 'Path should not include the .md extension.',
    });
  });

  it('rejects Windows reserved device names', () => {
    expect(validateWikiPagePath('CON')).toEqual({
      isValid: false,
      error: 'Path segments cannot use reserved device names.',
    });
    expect(validateWikiPagePath('guides/lpt9.txt').isValid).toBe(false);
    expect(validateWikiPagePath('console').isValid).toBe(true);
    expect(validateWikiPagePath('com10').isValid).toBe(true);
  });
});
