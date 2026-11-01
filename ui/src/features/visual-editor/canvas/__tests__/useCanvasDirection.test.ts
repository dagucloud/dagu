// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  CANVAS_DIRECTION_KEY,
  useCanvasDirection,
} from '../useCanvasDirection';

function setCookie(value: string | null) {
  document.cookie =
    value === null
      ? 'flowchart=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/'
      : `flowchart=${value}; path=/`;
}

beforeEach(() => {
  localStorage.clear();
  setCookie(null);
});

afterEach(() => {
  vi.restoreAllMocks();
  setCookie(null);
});

// parity:graph-direction-toggle parity:canvas-direction-toggle parity:run-graph-layout-toggle
describe('useCanvasDirection', () => {
  it('starts top to bottom without a stored value or a cookie', () => {
    const { result } = renderHook(() => useCanvasDirection());
    expect(result.current[0]).toBe('TB');
    expect(localStorage.getItem(CANVAS_DIRECTION_KEY)).toBeNull();
  });

  it('seeds TB from the cookie TD one time', () => {
    setCookie('TD');
    const first = renderHook(() => useCanvasDirection());
    expect(first.result.current[0]).toBe('TB');
    expect(localStorage.getItem(CANVAS_DIRECTION_KEY)).toBe('TB');
    first.unmount();

    setCookie('LR');
    const next = renderHook(() => useCanvasDirection());
    expect(next.result.current[0]).toBe('TB');
  });

  it('seeds LR from the cookie LR', () => {
    setCookie('LR');
    const { result } = renderHook(() => useCanvasDirection());
    expect(result.current[0]).toBe('LR');
    expect(localStorage.getItem(CANVAS_DIRECTION_KEY)).toBe('LR');
  });

  it('ignores a cookie with another value', () => {
    setCookie('RL');
    const { result } = renderHook(() => useCanvasDirection());
    expect(result.current[0]).toBe('TB');
    expect(localStorage.getItem(CANVAS_DIRECTION_KEY)).toBeNull();
  });

  it('prefers the stored value over the cookie', () => {
    localStorage.setItem(CANVAS_DIRECTION_KEY, 'TB');
    setCookie('LR');
    const { result } = renderHook(() => useCanvasDirection());
    expect(result.current[0]).toBe('TB');
  });

  it('ignores a stored value that is not a direction', () => {
    localStorage.setItem(CANVAS_DIRECTION_KEY, 'diagonal');
    setCookie('LR');
    const { result } = renderHook(() => useCanvasDirection());
    expect(result.current[0]).toBe('LR');
  });

  it('stores a change and keeps it after unmount', () => {
    const { result, unmount } = renderHook(() => useCanvasDirection());
    act(() => result.current[1]('LR'));
    expect(result.current[0]).toBe('LR');
    expect(localStorage.getItem(CANVAS_DIRECTION_KEY)).toBe('LR');

    unmount();
    const next = renderHook(() => useCanvasDirection());
    expect(next.result.current[0]).toBe('LR');
  });

  // VisualEditor, DetailsCanvas and RunSnapshotCanvas each call the hook:
  // a change in one canvas changes all of them.
  it('gives the same value to every canvas after a change', () => {
    const editor = renderHook(() => useCanvasDirection());
    const details = renderHook(() => useCanvasDirection());
    act(() => details.result.current[1]('LR'));
    expect(editor.result.current[0]).toBe('LR');
    expect(details.result.current[0]).toBe('LR');

    act(() => editor.result.current[1]('TB'));
    expect(details.result.current[0]).toBe('TB');
  });

  it('gives TB and does not throw when the storage throws', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('denied');
    });
    const a = renderHook(() => useCanvasDirection());
    const b = renderHook(() => useCanvasDirection());
    expect(a.result.current[0]).toBe('TB');

    expect(() => act(() => a.result.current[1]('LR'))).not.toThrow();
    expect(a.result.current[0]).toBe('LR');
    expect(b.result.current[0]).toBe('LR');

    // A change with a working storage stores the value again.
    vi.restoreAllMocks();
    act(() => a.result.current[1]('TB'));
    expect(localStorage.getItem(CANVAS_DIRECTION_KEY)).toBe('TB');
  });

  // A full storage refuses the write but still gives the old stored value.
  it('applies a change that the storage refuses over the stored value', () => {
    localStorage.setItem(CANVAS_DIRECTION_KEY, 'TB');
    const { result } = renderHook(() => useCanvasDirection());
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('quota');
    });
    act(() => result.current[1]('LR'));
    expect(result.current[0]).toBe('LR');

    vi.restoreAllMocks();
    act(() => result.current[1]('TB'));
    expect(result.current[0]).toBe('TB');
  });
});
