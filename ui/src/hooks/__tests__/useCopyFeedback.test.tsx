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

  it('clears copied at once on reset', async () => {
    vi.useFakeTimers();
    copyTextMock.mockResolvedValue(true);
    const { result } = renderHook(() => useCopyFeedback());

    await act(async () => {
      await result.current.copy('hello');
    });
    act(() => {
      result.current.reset();
    });
    expect(result.current.copied).toBe(false);
    expect(vi.getTimerCount()).toBe(0);
  });

  // A reset timer that outlives the component would fire after teardown.
  it('leaves no reset pending after unmount', async () => {
    vi.useFakeTimers();
    copyTextMock.mockResolvedValue(true);
    const { result, unmount } = renderHook(() => useCopyFeedback());

    await act(async () => {
      await result.current.copy('hello');
    });
    expect(vi.getTimerCount()).toBe(1);
    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  // A clipboard write still in progress at reset or unmount must not show
  // feedback or start a timer once it finishes.
  it.each(['reset', 'unmount'] as const)(
    'ignores a copy that finishes after %s',
    async (event) => {
      vi.useFakeTimers();
      let finish: (copied: boolean) => void = () => undefined;
      copyTextMock.mockReturnValue(
        new Promise<boolean>((resolve) => {
          finish = resolve;
        })
      );
      const { result, unmount } = renderHook(() => useCopyFeedback());

      let pending: Promise<void> = Promise.resolve();
      act(() => {
        pending = result.current.copy('hello');
      });
      if (event === 'reset') {
        act(() => {
          result.current.reset();
        });
      } else {
        unmount();
      }
      await act(async () => {
        finish(true);
        await pending;
      });

      expect(result.current.copied).toBe(false);
      expect(vi.getTimerCount()).toBe(0);
    }
  );

  it('stays unset when the clipboard write fails', async () => {
    copyTextMock.mockResolvedValue(false);
    const { result } = renderHook(() => useCopyFeedback());

    await act(async () => {
      await result.current.copy('hello');
    });
    expect(result.current.copied).toBe(false);
  });
});
