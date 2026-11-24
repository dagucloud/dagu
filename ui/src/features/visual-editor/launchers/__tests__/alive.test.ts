// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Status } from '@/api/v1/schema';
import type { RestartPolicy } from '../../doc/ops/restart';
import { ALIVE_GRACE_MS, useAlive, useSecondsLeft } from '../alive';

interface Input {
  status: Status | undefined;
  finishedAt: string | undefined;
  policy: RestartPolicy;
  waitSec: number | null;
  suspended: boolean;
}

const base: Input = {
  status: undefined,
  finishedAt: undefined,
  policy: 'always',
  waitSec: 1,
  suspended: false,
};

// A run that ended `agoMs` ago, as the server writes the time.
const endedAgo = (agoMs: number) => new Date(Date.now() - agoMs).toISOString();

// The state of a wait that ends `inMs` from now.
const waiting = (inMs: number) => ({
  phase: 'waiting',
  nextRunAt: Date.now() + inMs,
});

function setup(input: Partial<Input> = {}) {
  return renderHook((props: Input) => useAlive(props), {
    initialProps: { ...base, ...input },
  });
}

describe('useAlive', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-11-08T10:00:00Z'));
  });
  afterEach(() => vi.useRealTimers());

  it('is running while a run is running, queued or waiting', () => {
    for (const status of [Status.Running, Status.Queued, Status.Waiting]) {
      expect(setup({ status, finishedAt: '-' }).result.current).toEqual({
        phase: 'running',
      });
    }
  });

  it('is idle with no run, and for a run that ended long ago', () => {
    expect(setup().result.current).toBeNull();
    expect(
      setup({ status: Status.Success, finishedAt: endedAgo(60_000) }).result
        .current
    ).toBeNull();
  });

  it('waits for the next run, then tells the start for the grace', () => {
    const { result } = setup({
      status: Status.Success,
      finishedAt: endedAgo(0),
      waitSec: 3,
    });
    expect(result.current).toEqual(waiting(3000));
    act(() => vi.advanceTimersByTime(2999));
    expect(result.current).toEqual(waiting(1));
    act(() => vi.advanceTimersByTime(1));
    expect(result.current).toEqual({ phase: 'starting' });
    act(() => vi.advanceTimersByTime(ALIVE_GRACE_MS - 1));
    expect(result.current).toEqual({ phase: 'starting' });
    act(() => vi.advanceTimersByTime(1));
    expect(result.current).toBeNull();
  });

  it('counts the wait of the file in the hold', () => {
    const ended = endedAgo(ALIVE_GRACE_MS + 2000);
    expect(
      setup({ status: Status.Success, finishedAt: ended, waitSec: 1 }).result
        .current
    ).toBeNull();
    expect(
      setup({ status: Status.Success, finishedAt: ended, waitSec: 5 }).result
        .current
    ).toEqual({ phase: 'starting' });
  });

  // The clock of the browser can differ from the clock of the server: a run
  // that the hook sees end holds from that moment, whatever its end time.
  it('holds from the moment a run is seen to end, whatever its end time', () => {
    for (const finishedAt of ['2020-01-01T00:00:00Z', '2040-01-01T00:00:00Z']) {
      const { result, rerender } = setup({
        status: Status.Running,
        finishedAt: '-',
      });
      rerender({ ...base, status: Status.Success, finishedAt });
      expect(result.current).toEqual(waiting(1000));
      act(() => vi.advanceTimersByTime(1000 + ALIVE_GRACE_MS - 1));
      expect(result.current).toEqual({ phase: 'starting' });
      act(() => vi.advanceTimersByTime(1));
      expect(result.current).toBeNull();
    }
  });

  // A run that shows up after the mount and was not seen running is old
  // data, not a run that just ended.
  it('counts a run that it did not see running from its end time', () => {
    const { result, rerender } = setup();
    rerender({ ...base, status: Status.Success, finishedAt: endedAgo(60_000) });
    expect(result.current).toBeNull();
    rerender({ ...base, status: Status.Success, finishedAt: endedAgo(500) });
    expect(result.current).toEqual(waiting(500));
    act(() => vi.advanceTimersByTime(1000 + ALIVE_GRACE_MS - 500));
    expect(result.current).toBeNull();
  });

  it('is idle at once when the latest run goes away', () => {
    const { result, rerender } = setup({
      status: Status.Success,
      finishedAt: endedAgo(0),
    });
    expect(result.current).not.toBeNull();
    rerender(base);
    expect(result.current).toBeNull();
  });

  // An edit of the wait changes the length of the hold, not its start.
  it('follows a wait that changes during the hold', () => {
    const ended = { status: Status.Success, finishedAt: endedAgo(0) };
    const { result, rerender } = setup({ ...ended, waitSec: 60 });
    act(() => vi.advanceTimersByTime(5000));
    expect(result.current).toEqual(waiting(55_000));
    rerender({ ...base, ...ended, waitSec: 1 });
    expect(result.current).toEqual({ phase: 'starting' });
    act(() => vi.advanceTimersByTime(1000 + ALIVE_GRACE_MS - 5000 - 1));
    expect(result.current).toEqual({ phase: 'starting' });
    act(() => vi.advanceTimersByTime(1));
    expect(result.current).toBeNull();
    rerender({ ...base, ...ended, waitSec: 60 });
    expect(result.current).toEqual(waiting(49_000));
  });

  it('stays alive across runs that follow each other', () => {
    const { result, rerender } = setup({
      status: Status.Success,
      finishedAt: endedAgo(0),
    });
    for (let i = 0; i < 3; i++) {
      act(() => vi.advanceTimersByTime(1500));
      expect(result.current).toEqual({ phase: 'starting' });
      rerender({ ...base, status: Status.Running, finishedAt: '-' });
      expect(result.current).toEqual({ phase: 'running' });
      rerender({ ...base, status: Status.Success, finishedAt: endedAgo(0) });
      expect(result.current).toEqual(waiting(1000));
    }
  });

  it('is idle at once for an aborted run under unless-stopped', () => {
    const aborted = {
      policy: 'unless-stopped' as const,
      status: Status.Aborted,
      finishedAt: endedAgo(0),
    };
    expect(setup(aborted).result.current).toBeNull();
    // With `always`, a run that was aborted still restarts.
    expect(setup({ ...aborted, policy: 'always' }).result.current).toEqual(
      waiting(1000)
    );
  });

  it('is idle with a suspended workflow, and without a policy', () => {
    const running = { status: Status.Running, finishedAt: '-' };
    expect(setup({ ...running, suspended: true }).result.current).toBeNull();
    expect(setup({ ...running, policy: 'no' }).result.current).toBeNull();
  });

  // A card that cannot be alive waits for nothing.
  it('arms no timer without a policy, and one at a time with a policy', () => {
    const ended = { status: Status.Success, finishedAt: endedAgo(0) };
    setup({ ...ended, policy: 'no' });
    expect(vi.getTimerCount()).toBe(0);
    setup(ended);
    expect(vi.getTimerCount()).toBe(1);
  });
});

describe('useSecondsLeft', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-11-08T10:00:00Z'));
  });
  afterEach(() => vi.useRealTimers());

  it('counts down the whole seconds left, once a second', () => {
    const at = Date.now() + 2500;
    const { result } = renderHook(() => useSecondsLeft(at));
    expect(result.current).toBe(3);
    act(() => vi.advanceTimersByTime(500));
    expect(result.current).toBe(2);
    act(() => vi.advanceTimersByTime(1000));
    expect(result.current).toBe(1);
    act(() => vi.advanceTimersByTime(1000));
    expect(result.current).toBe(0);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('is 0 and arms no timer without a moment', () => {
    const { result } = renderHook(() => useSecondsLeft(null));
    expect(result.current).toBe(0);
    expect(vi.getTimerCount()).toBe(0);
  });
});
