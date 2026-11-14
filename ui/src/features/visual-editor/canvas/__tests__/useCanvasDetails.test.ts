// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CANVAS_DETAILS_KEY, useCanvasDetails } from '../useCanvasDetails';

describe('useCanvasDetails', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    // Resets the shared value and its storage.
    const { result } = renderHook(() => useCanvasDetails());
    act(() => result.current[1](true));
    window.localStorage.clear();
  });

  it('shows the details by default', () => {
    const { result } = renderHook(() => useCanvasDetails());
    expect(result.current[0]).toBe(true);
  });

  it('keeps the choice in localStorage and shares it', () => {
    const first = renderHook(() => useCanvasDetails());
    const second = renderHook(() => useCanvasDetails());
    act(() => first.result.current[1](false));
    expect(window.localStorage.getItem(CANVAS_DETAILS_KEY)).toBe('0');
    expect(first.result.current[0]).toBe(false);
    expect(second.result.current[0]).toBe(false);
    act(() => first.result.current[1](true));
    expect(window.localStorage.getItem(CANVAS_DETAILS_KEY)).toBe('1');
    expect(second.result.current[0]).toBe(true);
  });

  it('reads a stored choice', () => {
    window.localStorage.setItem(CANVAS_DETAILS_KEY, '0');
    const { result } = renderHook(() => useCanvasDetails());
    expect(result.current[0]).toBe(false);
  });

  it('still works when the storage refuses the write', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('denied');
    });
    const { result } = renderHook(() => useCanvasDetails());
    act(() => result.current[1](false));
    expect(result.current[0]).toBe(false);
  });
});
