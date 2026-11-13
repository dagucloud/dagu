// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, renderHook, screen } from '@testing-library/react';
import { useRef } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useElementWidth } from '../useElementWidth';

let resize: ResizeObserverCallback | null = null;

function Probe() {
  const ref = useRef<HTMLDivElement>(null);
  const width = useElementWidth(ref);
  return (
    <div
      ref={ref}
      data-testid="host"
      style={{ paddingLeft: 12, paddingRight: 12 }}
    >
      {String(width)}
    </div>
  );
}

// A box of the given outer width, with 12 px of padding on each side.
function elementOf(width: number): HTMLElement {
  const element = document.createElement('div');
  element.style.paddingLeft = '12px';
  element.style.paddingRight = '12px';
  vi.spyOn(element, 'clientWidth', 'get').mockReturnValue(width);
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

  // ResizeObserver reports the content box: the first read agrees with it.
  it('reads the content width on the first measure, without the padding', () => {
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(900);
    render(<Probe />);
    expect(screen.getByTestId('host')).toHaveTextContent('876');
  });

  it('is null where the element has no layout', () => {
    render(<Probe />);
    expect(screen.getByTestId('host')).toHaveTextContent('null');
  });

  it('reports the same box on a resize as on the first measure', () => {
    const ref = { current: elementOf(200) };
    const { result } = renderHook(() => useElementWidth(ref));
    expect(result.current).toBe(176);

    act(() => {
      resize?.(
        [{ contentRect: { width: 164 } } as ResizeObserverEntry],
        {} as ResizeObserver
      );
    });
    expect(result.current).toBe(164);
  });

  it('forgets the width when the element goes away', () => {
    const { result, rerender } = renderHook(
      ({ element }: { element: HTMLElement | null }) =>
        useElementWidth({ current: element }),
      { initialProps: { element: elementOf(200) as HTMLElement | null } }
    );
    expect(result.current).toBe(176);

    rerender({ element: null });
    expect(result.current).toBeNull();
  });
});
