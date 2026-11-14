// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useElementWidth } from '../useElementWidth';

let resize: ResizeObserverCallback | null = null;

function elementOf(width: number): HTMLElement {
  const element = document.createElement('div');
  vi.spyOn(element, 'getBoundingClientRect').mockReturnValue({
    width,
  } as DOMRect);
  return element;
}

describe('useElementWidth', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'ResizeObserver',
      class {
        constructor(callback: ResizeObserverCallback) {
          resize = callback;
        }
        observe() {}
        disconnect() {}
      }
    );
  });

  afterEach(() => {
    resize = null;
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  // The content box is narrower by the padding: a resize must not report it.
  it('reports the same box on a resize as on the first measure', () => {
    const ref = { current: elementOf(200) };
    const { result } = renderHook(() => useElementWidth(ref));
    expect(result.current).toBe(200);

    act(() => {
      resize?.(
        [{ contentRect: { width: 184 } } as ResizeObserverEntry],
        {} as ResizeObserver
      );
    });
    expect(result.current).toBe(200);
  });

  it('forgets the width when the element goes away', () => {
    const { result, rerender } = renderHook(
      ({ element }: { element: HTMLElement | null }) =>
        useElementWidth({ current: element }),
      { initialProps: { element: elementOf(200) as HTMLElement | null } }
    );
    expect(result.current).toBe(200);

    rerender({ element: null });
    expect(result.current).toBeNull();
  });
});
