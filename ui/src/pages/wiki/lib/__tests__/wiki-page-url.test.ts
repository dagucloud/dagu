// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { encodeWikiPagePathForURL } from '../wiki-page-path';
import { normalizeWikiPagePathFromURL } from '../wiki-page-url';

describe('normalizeWikiPagePathFromURL', () => {
  it('strips a markdown extension from URL paths', () => {
    expect(normalizeWikiPagePathFromURL('docs/deploy.md')).toBe('docs/deploy');
    expect(normalizeWikiPagePathFromURL('docs/DEPLOY.MD')).toBe('docs/DEPLOY');
  });

  it('keeps leading-underscore names visible after stripping the extension', () => {
    expect(normalizeWikiPagePathFromURL('_index.md')).toBe('_index');
    expect(normalizeWikiPagePathFromURL('guides/_partial.md')).toBe(
      'guides/_partial'
    );
  });

  it('does not strip md text from non-markdown suffixes', () => {
    expect(normalizeWikiPagePathFromURL('notes.md.backup')).toBe(
      'notes.md.backup'
    );
  });

  it('round-trips stored page IDs that end with a markdown extension', () => {
    const encoded = encodeWikiPagePathForURL('imports/notes.md');

    expect(normalizeWikiPagePathFromURL(decodeURIComponent(encoded))).toBe(
      'imports/notes.md'
    );
  });
});
