// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { readDraft, writeDraft } from '../draftStorage';
import { useDraftRecovery } from '../useDraftRecovery';

const KEY = 'visual-editor:draft:test';

type Props = Parameters<typeof useDraftRecovery>[0];

function setup(initial: Props) {
  return renderHook((props: Props) => useDraftRecovery(props), {
    initialProps: initial,
  });
}

describe('useDraftRecovery', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.useFakeTimers();
    vi.setSystemTime(5000);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe('store', () => {
    it('stores the dirty text after one second, not before', () => {
      setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'b',
        isDirty: true,
      });
      act(() => {
        vi.advanceTimersByTime(999);
      });
      expect(readDraft(KEY)).toBeNull();
      act(() => {
        vi.advanceTimersByTime(1);
      });
      expect(readDraft(KEY)).toEqual({ text: 'b', savedAt: 6000 });
    });

    it('stores the base hash with the text, also on pagehide', () => {
      const { rerender } = setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'b',
        isDirty: true,
        baseSha256: 'h1',
      });
      act(() => {
        vi.advanceTimersByTime(1000);
      });
      expect(readDraft(KEY)).toEqual({
        text: 'b',
        savedAt: 6000,
        baseSha256: 'h1',
      });

      rerender({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'bc',
        isDirty: true,
        baseSha256: 'h2',
      });
      act(() => {
        window.dispatchEvent(new Event('pagehide'));
      });
      expect(readDraft(KEY)?.baseSha256).toBe('h2');
    });

    it('restarts the timer on each change', () => {
      const { rerender } = setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'b',
        isDirty: true,
      });
      act(() => {
        vi.advanceTimersByTime(800);
      });
      rerender({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'bc',
        isDirty: true,
      });
      act(() => {
        vi.advanceTimersByTime(800);
      });
      expect(readDraft(KEY)).toBeNull();
      act(() => {
        vi.advanceTimersByTime(200);
      });
      expect(readDraft(KEY)?.text).toBe('bc');
    });

    it('stores nothing when clean', () => {
      setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'a',
        isDirty: false,
      });
      act(() => {
        vi.advanceTimersByTime(5000);
      });
      expect(readDraft(KEY)).toBeNull();
    });

    it('stores nothing when the text becomes clean before the delay', () => {
      const { rerender } = setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'b',
        isDirty: true,
      });
      rerender({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'a',
        isDirty: false,
      });
      act(() => {
        vi.advanceTimersByTime(5000);
      });
      expect(readDraft(KEY)).toBeNull();
    });

    it('stops the timer on unmount', () => {
      const { unmount } = setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'b',
        isDirty: true,
      });
      unmount();
      localStorage.clear();
      act(() => {
        vi.advanceTimersByTime(5000);
      });
      expect(readDraft(KEY)).toBeNull();
    });
  });

  describe('offer', () => {
    const stored = { text: 'edited', savedAt: 42 };

    it('offers the stored draft on remount', () => {
      writeDraft(KEY, stored);
      const { result } = setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'a',
        isDirty: false,
      });
      expect(result.current.draft).toEqual(stored);
    });

    it('offers nothing when the draft equals the server text', () => {
      writeDraft(KEY, stored);
      const { result } = setup({
        storageKey: KEY,
        serverText: 'edited',
        currentText: 'edited',
        isDirty: false,
      });
      expect(result.current.draft).toBeNull();
    });

    it('offers nothing when no draft is stored', () => {
      const { result } = setup({
        storageKey: KEY,
        serverText: 'a',
        isDirty: false,
      });
      expect(result.current.draft).toBeNull();
    });

    it('offers the draft once serverText arrives late', () => {
      writeDraft(KEY, stored);
      const { result, rerender } = setup({ storageKey: KEY, isDirty: false });
      expect(result.current.draft).toBeNull();
      rerender({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'a',
        isDirty: false,
      });
      expect(result.current.draft).toEqual(stored);
    });

    it('does not replace the offer with a later write of the edits', () => {
      writeDraft(KEY, stored);
      const { result, rerender } = setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'a',
        isDirty: false,
      });
      rerender({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'new',
        isDirty: true,
      });
      act(() => {
        vi.advanceTimersByTime(1000);
      });
      expect(readDraft(KEY)?.text).toBe('new');
      expect(result.current.draft).toEqual(stored);
    });

    it('offers the draft read at mount even if a write comes first', () => {
      writeDraft(KEY, stored);
      const { result, rerender } = setup({
        storageKey: KEY,
        currentText: 'new',
        isDirty: true,
      });
      act(() => {
        vi.advanceTimersByTime(1000);
      });
      rerender({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'new',
        isDirty: true,
      });
      expect(result.current.draft).toEqual(stored);
    });

    it('decides again for another storage key', () => {
      writeDraft('other', { text: 'x', savedAt: 1 });
      writeDraft(KEY, stored);
      const { result, rerender } = setup({
        storageKey: KEY,
        serverText: 'a',
        isDirty: false,
      });
      rerender({ storageKey: 'other', serverText: 'a', isDirty: false });
      expect(result.current.draft).toEqual({ text: 'x', savedAt: 1 });
    });

    it('offers nothing for corrupt stored data', () => {
      localStorage.setItem(KEY, '{broken');
      const { result } = setup({
        storageKey: KEY,
        serverText: 'a',
        isDirty: false,
      });
      expect(result.current.draft).toBeNull();
    });

    it('does not throw when getItem throws', () => {
      vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
        throw new Error('denied');
      });
      const { result } = setup({
        storageKey: KEY,
        serverText: 'a',
        isDirty: false,
      });
      expect(result.current.draft).toBeNull();
    });

    it('does not throw when setItem throws', () => {
      vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
        throw new Error('full');
      });
      const { result } = setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'b',
        isDirty: true,
      });
      expect(() => {
        act(() => {
          vi.advanceTimersByTime(1000);
        });
      }).not.toThrow();
      expect(result.current.draft).toBeNull();
    });
  });

  describe('discard', () => {
    it('removes the stored draft and hides the offer', () => {
      writeDraft(KEY, { text: 'edited', savedAt: 42 });
      const { result } = setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'a',
        isDirty: false,
      });
      expect(result.current.draft).not.toBeNull();
      act(() => {
        result.current.discard();
      });
      expect(result.current.draft).toBeNull();
      expect(readDraft(KEY)).toBeNull();
    });

    it('hides the offer when removeItem throws', () => {
      writeDraft(KEY, { text: 'edited', savedAt: 42 });
      const { result } = setup({
        storageKey: KEY,
        serverText: 'a',
        isDirty: false,
      });
      vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
        throw new Error('denied');
      });
      act(() => {
        expect(() => result.current.discard()).not.toThrow();
      });
      expect(result.current.draft).toBeNull();
    });

    it('does not offer a discarded draft that arrives before serverText', () => {
      writeDraft(KEY, { text: 'edited', savedAt: 42 });
      const { result, rerender } = setup({ storageKey: KEY, isDirty: false });
      act(() => {
        result.current.discard();
      });
      rerender({ storageKey: KEY, serverText: 'a', isDirty: false });
      expect(result.current.draft).toBeNull();
    });
  });

  describe('clear after a save', () => {
    it('removes the draft on a dirty-to-clean change with equal text', () => {
      const { rerender } = setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'b',
        isDirty: true,
      });
      act(() => {
        vi.advanceTimersByTime(1000);
      });
      expect(readDraft(KEY)?.text).toBe('b');
      rerender({
        storageKey: KEY,
        serverText: 'b',
        currentText: 'b',
        isDirty: false,
      });
      expect(readDraft(KEY)).toBeNull();
    });

    it('waits for serverText to match after the save', () => {
      const { rerender } = setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'b',
        isDirty: true,
      });
      act(() => {
        vi.advanceTimersByTime(1000);
      });
      rerender({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'b',
        isDirty: false,
      });
      expect(readDraft(KEY)?.text).toBe('b');
      rerender({
        storageKey: KEY,
        serverText: 'b',
        currentText: 'b',
        isDirty: false,
      });
      expect(readDraft(KEY)).toBeNull();
    });

    it('keeps the draft of a fresh mount with a clean buffer', () => {
      writeDraft(KEY, { text: 'edited', savedAt: 42 });
      const { result } = setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'a',
        isDirty: false,
      });
      expect(readDraft(KEY)?.text).toBe('edited');
      expect(result.current.draft?.text).toBe('edited');
    });

    it('keeps the draft when a clean buffer differs from the server text', () => {
      const { rerender } = setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'b',
        isDirty: true,
      });
      act(() => {
        vi.advanceTimersByTime(1000);
      });
      rerender({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'c',
        isDirty: false,
      });
      expect(readDraft(KEY)?.text).toBe('b');
    });
  });

  describe('flush', () => {
    it('writes the pending draft at once on pagehide', () => {
      setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'b',
        isDirty: true,
      });
      expect(readDraft(KEY)).toBeNull();
      act(() => {
        window.dispatchEvent(new Event('pagehide'));
      });
      expect(readDraft(KEY)).toEqual({ text: 'b', savedAt: 5000 });
    });

    it('writes the pending draft on unmount', () => {
      const { unmount } = setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'b',
        isDirty: true,
      });
      unmount();
      expect(readDraft(KEY)?.text).toBe('b');
    });

    // The editor switches to another workflow before the delay ends. Its
    // first render under the new key can still carry the old dirty text.
    it('writes the pending draft of the previous key when the key changes', () => {
      const OTHER = 'visual-editor:draft:other';
      const { rerender } = setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'b',
        isDirty: true,
        baseSha256: 'h1',
      });
      rerender({
        storageKey: OTHER,
        serverText: 'a',
        currentText: 'b',
        isDirty: true,
        baseSha256: 'h1',
      });
      rerender({
        storageKey: OTHER,
        serverText: 'x',
        currentText: 'x',
        isDirty: false,
      });
      expect(readDraft(KEY)).toEqual({
        text: 'b',
        savedAt: 5000,
        baseSha256: 'h1',
      });
      act(() => {
        vi.advanceTimersByTime(1000);
      });
      expect(readDraft(OTHER)).toBeNull();
    });

    it('writes nothing on pagehide or unmount when clean', () => {
      const { unmount } = setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'a',
        isDirty: false,
      });
      act(() => {
        window.dispatchEvent(new Event('pagehide'));
      });
      unmount();
      expect(readDraft(KEY)).toBeNull();
    });

    it('writes nothing after a save made the text clean', () => {
      const { rerender, unmount } = setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'b',
        isDirty: true,
      });
      rerender({
        storageKey: KEY,
        serverText: 'b',
        currentText: 'b',
        isDirty: false,
      });
      act(() => {
        window.dispatchEvent(new Event('pagehide'));
      });
      unmount();
      expect(readDraft(KEY)).toBeNull();
    });

    it('stops listening after unmount', () => {
      const { unmount } = setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'b',
        isDirty: true,
      });
      unmount();
      localStorage.clear();
      act(() => {
        window.dispatchEvent(new Event('pagehide'));
      });
      expect(readDraft(KEY)).toBeNull();
    });

    it('does not throw when setItem throws', () => {
      vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
        throw new Error('full');
      });
      const { unmount } = setup({
        storageKey: KEY,
        serverText: 'a',
        currentText: 'b',
        isDirty: true,
      });
      expect(() => {
        act(() => {
          window.dispatchEvent(new Event('pagehide'));
        });
        unmount();
      }).not.toThrow();
    });
  });

  describe('semantic comparison', () => {
    const sameLines = (a: string, b: string) =>
      a.replace(/\n+/g, '\n') === b.replace(/\n+/g, '\n');

    it('offers no draft that only differs by blank lines', () => {
      writeDraft(KEY, { text: 'a\n\n\nb', savedAt: 1 });
      const { result } = setup({
        storageKey: KEY,
        serverText: 'a\nb',
        isDirty: false,
        isSame: sameLines,
      });
      expect(result.current.draft).toBeNull();
    });

    it('keeps the strict comparison without isSame', () => {
      writeDraft(KEY, { text: 'a\n\n\nb', savedAt: 1 });
      const { result } = setup({
        storageKey: KEY,
        serverText: 'a\nb',
        isDirty: false,
      });
      expect(result.current.draft).not.toBeNull();
    });

    it('removes the stored draft when the text is the same again', () => {
      writeDraft(KEY, { text: 'x', savedAt: 1 });
      const { rerender } = setup({
        storageKey: KEY,
        serverText: 'a\nb',
        currentText: 'a\nb\nc',
        isDirty: true,
        isSame: sameLines,
      });
      rerender({
        storageKey: KEY,
        serverText: 'a\nb',
        currentText: 'a\n\nb',
        isDirty: false,
        isSame: sameLines,
      });
      expect(readDraft(KEY)).toBeNull();
    });
  });
});
