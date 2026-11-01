// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { readDraft, removeDraft, writeDraft } from '../draftStorage';

const KEY = 'visual-editor:draft:test';

describe('draftStorage', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('round-trips a draft', () => {
    const draft = { text: 'steps: []', savedAt: 12, baseSha256: 'abc' };
    expect(writeDraft(KEY, draft)).toBe(true);
    expect(readDraft(KEY)).toEqual(draft);
  });

  it('returns null when nothing is stored', () => {
    expect(readDraft(KEY)).toBeNull();
  });

  it('removes a draft', () => {
    writeDraft(KEY, { text: 'a', savedAt: 1 });
    removeDraft(KEY);
    expect(readDraft(KEY)).toBeNull();
  });

  it('returns null for corrupt JSON', () => {
    localStorage.setItem(KEY, '{not json');
    expect(readDraft(KEY)).toBeNull();
  });

  it.each([
    ['null', 'null'],
    ['a string', '"x"'],
    ['text not a string', '{"text":1,"savedAt":1}'],
    ['savedAt missing', '{"text":"a"}'],
    ['savedAt not a number', '{"text":"a","savedAt":"1"}'],
    ['baseSha256 not a string', '{"text":"a","savedAt":1,"baseSha256":3}'],
  ])('returns null for a bad shape: %s', (_name, raw) => {
    localStorage.setItem(KEY, raw);
    expect(readDraft(KEY)).toBeNull();
  });

  it('does not throw when storage throws', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('full');
    });
    vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw new Error('denied');
    });
    expect(readDraft(KEY)).toBeNull();
    expect(writeDraft(KEY, { text: 'a', savedAt: 1 })).toBe(false);
    expect(() => removeDraft(KEY)).not.toThrow();
  });
});
