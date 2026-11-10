// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  AUTO_FIT_DELAY_MS,
  AUTO_FIT_SETTLE_MS,
  useAutoFit,
} from '../useAutoFit';

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

function setup(element: HTMLDivElement = document.createElement('div')) {
  const fit = vi.fn();
  const ref = { current: element };
  const { result } = renderHook(() => useAutoFit(ref, fit));
  return { fit, result };
}

/** An element with a layout: jsdom gives every element a size of 0. */
function laidOut(width: number, height: number): HTMLDivElement {
  const element = document.createElement('div');
  Object.defineProperty(element, 'clientWidth', { value: width });
  Object.defineProperty(element, 'clientHeight', { value: height });
  return element;
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

  // The parts of a page that opens (its triggers, its overlays) change the
  // graph one after another: the view jumps for them, and glides for a
  // change once the canvas has been shown for a while.
  it('jumps for a fit just after the canvas shows, and glides later', () => {
    const { fit, result } = setup();
    act(() => result.current.refit());
    expect(fit).toHaveBeenLastCalledWith(false);

    act(() => vi.advanceTimersByTime(AUTO_FIT_SETTLE_MS));
    act(() => result.current.refit());
    expect(fit).toHaveBeenLastCalledWith(true);
    notify(700, 600);
    act(() => vi.advanceTimersByTime(AUTO_FIT_DELAY_MS));
    expect(fit).toHaveBeenLastCalledWith(true);
  });

  // Another document takes the place of the graph as a new page would: its
  // fits jump again for a while.
  it('jumps again for a while after settle', () => {
    const { fit, result } = setup();
    act(() => vi.advanceTimersByTime(AUTO_FIT_SETTLE_MS));
    act(() => result.current.settle());
    act(() => result.current.refit());
    expect(fit).toHaveBeenLastCalledWith(false);
    act(() => vi.advanceTimersByTime(AUTO_FIT_SETTLE_MS));
    act(() => result.current.refit());
    expect(fit).toHaveBeenLastCalledWith(true);
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

  // A dock that opens under the canvas takes its height: a graph that kept
  // its place was cut by the dock while the space above it stayed empty.
  it('fits again after a height-only change, until the user moves', () => {
    const { fit, result } = setup();
    notify(800, 420);
    notify(800, 300);
    act(() => vi.advanceTimersByTime(AUTO_FIT_DELAY_MS));
    expect(fit).toHaveBeenCalledTimes(1);

    result.current.onUserMove();
    notify(800, 200);
    act(() => vi.advanceTimersByTime(AUTO_FIT_DELAY_MS));
    expect(fit).toHaveBeenCalledTimes(1);
  });

  // The flow fits the size that it measures at its first layout. A panel
  // restored from memory can change it before the observer first reports.
  it('fits again when the first report differs from the size at mount', () => {
    const { fit } = setup(laidOut(1000, 600));
    act(() => vi.advanceTimersByTime(AUTO_FIT_DELAY_MS));
    expect(fit).toHaveBeenCalledTimes(1);
  });

  it('does not fit again when the first report is the size at mount', () => {
    const { fit } = setup(laidOut(800, 600));
    act(() => vi.advanceTimersByTime(AUTO_FIT_DELAY_MS));
    expect(fit).not.toHaveBeenCalled();
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
