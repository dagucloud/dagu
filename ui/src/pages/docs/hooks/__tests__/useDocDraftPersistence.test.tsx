// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useDocDraftPersistence } from '../useDocDraftPersistence';

const mocks = vi.hoisted(() => ({
  clearDraft: vi.fn(),
  getDraft: vi.fn(),
  setDraft: vi.fn(),
}));

vi.mock('@/contexts/DocTabContext', () => ({
  useDocTabContext: () => mocks,
}));

describe('useDocDraftPersistence', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('restores the draft for the current key', () => {
    mocks.getDraft.mockReturnValue('restored draft');
    const setCurrentValue = vi.fn();

    renderHook(() =>
      useDocDraftPersistence({
        draftKey: 'tab-1',
        currentValue: 'server content',
        hasUnsavedChanges: false,
        setCurrentValue,
      })
    );

    expect(setCurrentValue).toHaveBeenCalledWith('restored draft');
  });

  it('persists dirty content after the debounce and on pagehide', () => {
    const setCurrentValue = vi.fn();
    renderHook(() =>
      useDocDraftPersistence({
        draftKey: 'tab-1',
        currentValue: 'local draft',
        hasUnsavedChanges: true,
        setCurrentValue,
      })
    );

    act(() => vi.advanceTimersByTime(300));
    expect(mocks.setDraft).toHaveBeenCalledWith('tab-1', 'local draft');

    mocks.setDraft.mockClear();
    act(() => window.dispatchEvent(new Event('pagehide')));
    expect(mocks.setDraft).toHaveBeenCalledWith('tab-1', 'local draft');
  });

  it('cancels pending persistence when a draft is cleared', () => {
    const setCurrentValue = vi.fn();
    const { result, unmount } = renderHook(() =>
      useDocDraftPersistence({
        draftKey: 'tab-1',
        currentValue: 'discarded draft',
        hasUnsavedChanges: true,
        setCurrentValue,
      })
    );

    act(() => result.current.clearPersistedDraft());
    unmount();
    act(() => vi.runAllTimers());

    expect(mocks.clearDraft).toHaveBeenCalledWith('tab-1');
    expect(mocks.setDraft).not.toHaveBeenCalled();
  });
});
