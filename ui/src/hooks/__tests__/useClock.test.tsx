// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { subscribeClock, useClock, useClockNow } from '../useClock';

function setHidden(hidden: boolean) {
  Object.defineProperty(document, 'hidden', {
    configurable: true,
    get: () => hidden,
  });
  document.dispatchEvent(new Event('visibilitychange'));
}

describe('shared clock', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-01-01T00:00:00Z'));
  });

  afterEach(() => {
    setHidden(false);
    vi.useRealTimers();
  });

  it('keeps one timer for all subscribers of a period', () => {
    const spy = vi.spyOn(globalThis, 'setInterval');
    const a = vi.fn();
    const b = vi.fn();
    const stopA = subscribeClock(1000, a);
    const stopB = subscribeClock(1000, b);
    expect(spy).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(3000);
    expect(a).toHaveBeenCalledTimes(3);
    expect(b).toHaveBeenCalledTimes(3);
    stopA();
    stopB();
    expect(vi.getTimerCount()).toBe(0);
    spy.mockRestore();
  });

  it('stops its timer when the last subscriber leaves', () => {
    const stop = subscribeClock(1000, () => {});
    expect(vi.getTimerCount()).toBe(1);
    stop();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('pauses while the tab is hidden and catches up when it shows', () => {
    const listener = vi.fn();
    const stop = subscribeClock(1000, listener);
    setHidden(true);
    expect(vi.getTimerCount()).toBe(0);
    vi.advanceTimersByTime(5000);
    expect(listener).not.toHaveBeenCalled();
    setHidden(false);
    expect(listener).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(1);
    vi.advanceTimersByTime(1000);
    expect(listener).toHaveBeenCalledTimes(2);
    stop();
  });

  it('renders again only when the derived value changes', () => {
    let renders = 0;
    function Minutes() {
      renders++;
      const text = useClock(1000, (now) => `${Math.floor(now / 60000)}m`);
      return <span>{text}</span>;
    }
    const view = render(<Minutes />);
    const first = renders;
    act(() => {
      vi.advanceTimersByTime(30_000);
    });
    expect(renders).toBe(first);
    act(() => {
      vi.advanceTimersByTime(30_000);
    });
    expect(renders).toBe(first + 1);
    expect(view.container.textContent).toBe(
      `${Math.floor(Date.now() / 60000)}m`
    );
    view.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('starts no timer with a null period', () => {
    function Idle() {
      return <span>{useClock(null, () => 'x')}</span>;
    }
    render(<Idle />);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('gives the time of the last tick to useClockNow', () => {
    const seen: number[] = [];
    function Now() {
      const now = useClockNow(1000);
      seen.push(now);
      return null;
    }
    render(<Now />);
    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(seen[seen.length - 1]! - seen[0]!).toBe(2000);
  });
});
