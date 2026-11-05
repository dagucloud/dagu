// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { OVERLAY_SLIDE_MS, useOverlayExit } from '../useOverlayExit';

function stubReducedMotion(reduce: boolean): void {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: reduce && query === '(prefers-reduced-motion: reduce)',
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
  }));
}

function renderExit(returned = true) {
  return renderHook(
    ({ isOpen, returned }) => useOverlayExit(isOpen, returned),
    { initialProps: { isOpen: true, returned } }
  );
}

describe('useOverlayExit', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    stubReducedMotion(false);
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('slides out for the length of the slide on a return to the page below', () => {
    const { result, rerender } = renderExit();
    expect(result.current).toBe(false);

    rerender({ isOpen: false, returned: true });
    expect(result.current).toBe(true);

    act(() => vi.advanceTimersByTime(OVERLAY_SLIDE_MS - 1));
    expect(result.current).toBe(true);
    act(() => vi.advanceTimersByTime(1));
    expect(result.current).toBe(false);
  });

  // Open workflow shows the same page in full: nothing slides away.
  it('ends at once on a close that moves on to another page', () => {
    const { result, rerender } = renderExit();

    rerender({ isOpen: false, returned: false });

    expect(result.current).toBe(false);
  });

  it('ends at once under reduced motion', () => {
    stubReducedMotion(true);
    const { result, rerender } = renderExit();

    rerender({ isOpen: false, returned: true });

    expect(result.current).toBe(false);
  });

  it('stops sliding out when the overlay opens again', () => {
    const { result, rerender } = renderExit();
    rerender({ isOpen: false, returned: true });

    rerender({ isOpen: true, returned: true });
    expect(result.current).toBe(false);

    act(() => vi.advanceTimersByTime(OVERLAY_SLIDE_MS));
    expect(result.current).toBe(false);
  });
});
