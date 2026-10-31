// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AUTO_FIT_DELAY_MS, useAutoFit } from '../useAutoFit';

let notify: (width: number, height: number) => void = () => undefined;

class ResizeObserverStub {
  constructor(private readonly callback: ResizeObserverCallback) {}
  observe(target: Element): void {
    notify = (width, height) =>
      this.callback(
        [{ target, contentRect: { width, height } } as ResizeObserverEntry],
        this as unknown as ResizeObserver
      );
    notify(800, 600);
  }
  unobserve(): void {}
  disconnect(): void {}
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal('ResizeObserver', ResizeObserverStub);
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function setup() {
  const fit = vi.fn();
  const onHeightChange = vi.fn();
  const ref = { current: document.createElement('div') };
  const { result } = renderHook(() =>
    useAutoFit(ref, fit, undefined, onHeightChange)
  );
  return { fit, onHeightChange, result };
}

describe('useAutoFit', () => {
  // parity:graph-auto-fit-height
  // parity:canvas-auto-fit
  it('fits once after the size settles, not at the first size', () => {
    const { fit } = setup();
    act(() => vi.advanceTimersByTime(AUTO_FIT_DELAY_MS));
    expect(fit).not.toHaveBeenCalled();

    notify(600, 600);
    notify(500, 600);
    act(() => vi.advanceTimersByTime(AUTO_FIT_DELAY_MS));
    expect(fit).toHaveBeenCalledTimes(1);
  });

  // parity:canvas-auto-fit
  it('fits again 150 ms after a width change', () => {
    const { fit } = setup();
    notify(700, 600);
    act(() => vi.advanceTimersByTime(AUTO_FIT_DELAY_MS - 1));
    expect(fit).not.toHaveBeenCalled();
    act(() => vi.advanceTimersByTime(1));
    expect(fit).toHaveBeenCalledTimes(1);
    expect(AUTO_FIT_DELAY_MS).toBe(150);
  });

  // A dock that opens or closes changes the height only: the graph stays.
  it('does not fit again after a height-only change', () => {
    const { fit, onHeightChange, result } = setup();
    notify(800, 420);
    notify(800, 300);
    act(() => vi.advanceTimersByTime(AUTO_FIT_DELAY_MS * 4));
    expect(fit).not.toHaveBeenCalled();
    expect(onHeightChange).toHaveBeenCalledTimes(1);

    // A width change afterwards fits again, as before.
    notify(700, 300);
    act(() => vi.advanceTimersByTime(AUTO_FIT_DELAY_MS));
    expect(fit).toHaveBeenCalledTimes(1);
    expect(onHeightChange).toHaveBeenCalledTimes(1);

    // A user move stops the height callback too.
    result.current.onUserMove();
    notify(700, 200);
    act(() => vi.advanceTimersByTime(AUTO_FIT_DELAY_MS));
    expect(onHeightChange).toHaveBeenCalledTimes(1);
  });

  // parity:canvas-auto-fit
  it('stops after the user moves the view and resumes on request', () => {
    const { fit, result } = setup();

    notify(600, 600);
    act(() => vi.advanceTimersByTime(AUTO_FIT_DELAY_MS));
    expect(fit).toHaveBeenCalledTimes(1);

    result.current.onUserMove();
    notify(500, 600);
    act(() => vi.advanceTimersByTime(AUTO_FIT_DELAY_MS));
    expect(fit).toHaveBeenCalledTimes(1);

    result.current.resume();
    notify(400, 600);
    act(() => vi.advanceTimersByTime(AUTO_FIT_DELAY_MS));
    expect(fit).toHaveBeenCalledTimes(2);
  });

  // parity:canvas-auto-fit
  // An overlay, a notice or a run status redraws the canvas with a new fit
  // callback and may report the same size again: the view must not move.
  it('keeps zoom and pan when a change leaves the size as it is', () => {
    const fit = vi.fn();
    const ref = { current: document.createElement('div') };
    const { rerender } = renderHook(
      ({ onFit }: { onFit: () => void }) => useAutoFit(ref, onFit),
      { initialProps: { onFit: fit } }
    );
    const nextFit = vi.fn();
    rerender({ onFit: nextFit });
    notify(800, 600);
    notify(800, 600);
    act(() => vi.advanceTimersByTime(AUTO_FIT_DELAY_MS * 4));
    expect(fit).not.toHaveBeenCalled();
    expect(nextFit).not.toHaveBeenCalled();

    // A real resize afterwards calls the latest callback only.
    notify(700, 600);
    act(() => vi.advanceTimersByTime(AUTO_FIT_DELAY_MS));
    expect(fit).not.toHaveBeenCalled();
    expect(nextFit).toHaveBeenCalledTimes(1);
  });

  // A reveal by the host stops the resize fit, not the fit after an undo.
  it('refits on request after a host move, not after a user move', () => {
    const { fit, result } = setup();
    result.current.onHostMove();
    notify(500, 600);
    act(() => vi.advanceTimersByTime(AUTO_FIT_DELAY_MS));
    expect(fit).not.toHaveBeenCalled();

    result.current.refit();
    expect(fit).toHaveBeenCalledTimes(1);
    // The refit hands the view back to the automatic fit.
    notify(400, 600);
    act(() => vi.advanceTimersByTime(AUTO_FIT_DELAY_MS));
    expect(fit).toHaveBeenCalledTimes(2);

    result.current.onUserMove();
    result.current.refit();
    expect(fit).toHaveBeenCalledTimes(2);
  });
});
