// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useCopyFeedback } from '../useCopyFeedback';

const copyTextMock = vi.hoisted(() => vi.fn());

vi.mock('@/lib/clipboard', () => ({
  copyText: copyTextMock,
}));

afterEach(() => {
  copyTextMock.mockReset();
  vi.useRealTimers();
});

describe('useCopyFeedback', () => {
  it('flips copied on and back off after the reset delay', async () => {
    vi.useFakeTimers();
    copyTextMock.mockResolvedValue(true);
    const { result } = renderHook(() => useCopyFeedback());

    await act(async () => {
      await result.current.copy('hello: world');
    });
    expect(copyTextMock).toHaveBeenCalledWith('hello: world');
    expect(result.current.copied).toBe(true);

    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(result.current.copied).toBe(false);
  });

  it('stays unset when the clipboard write fails', async () => {
    copyTextMock.mockResolvedValue(false);
    const { result } = renderHook(() => useCopyFeedback());

    await act(async () => {
      await result.current.copy('hello');
    });
    expect(result.current.copied).toBe(false);
  });
});
