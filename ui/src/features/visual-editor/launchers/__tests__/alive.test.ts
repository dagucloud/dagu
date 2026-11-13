// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Status } from '@/api/v1/schema';
import type { RestartPolicy } from '../../doc/ops/restart';
import { ALIVE_GRACE_MS, useAlive } from '../alive';

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

  it('is alive while a run is running or queued', () => {
    for (const status of [Status.Running, Status.Queued]) {
      expect(setup({ status, finishedAt: '-' }).result.current).toBe(true);
    }
  });

  it('is still with no run, and for a run that ended long ago', () => {
    expect(setup().result.current).toBe(false);
    expect(
      setup({ status: Status.Success, finishedAt: endedAgo(60_000) }).result
        .current
    ).toBe(false);
  });

  // The wait between two runs must not make the icon flicker.
  it('stays alive for the wait and the grace after a run ended, then stops', () => {
    const { result } = setup({
      status: Status.Success,
      finishedAt: endedAgo(500),
    });
    expect(result.current).toBe(true);
    const left = 1000 + ALIVE_GRACE_MS - 500;
    act(() => vi.advanceTimersByTime(left - 1));
    expect(result.current).toBe(true);
    act(() => vi.advanceTimersByTime(1));
    expect(result.current).toBe(false);
  });

  it('counts the wait of the file in the hold', () => {
    const ended = endedAgo(4000);
    expect(
      setup({ status: Status.Success, finishedAt: ended, waitSec: 1 }).result
        .current
    ).toBe(false);
    expect(
      setup({ status: Status.Success, finishedAt: ended, waitSec: 5 }).result
        .current
    ).toBe(true);
  });

  it('keeps one steady state across runs that follow each other', () => {
    const { result, rerender } = setup({
      status: Status.Success,
      finishedAt: endedAgo(0),
    });
    for (let i = 0; i < 3; i++) {
      act(() => vi.advanceTimersByTime(1500));
      expect(result.current).toBe(true);
      rerender({ ...base, status: Status.Running, finishedAt: '-' });
      expect(result.current).toBe(true);
      rerender({ ...base, status: Status.Success, finishedAt: endedAgo(0) });
      expect(result.current).toBe(true);
    }
  });

  it('is still at once for a run that an operator aborted under unless-stopped', () => {
    const aborted = {
      policy: 'unless-stopped' as const,
      status: Status.Aborted,
      finishedAt: endedAgo(0),
    };
    expect(setup(aborted).result.current).toBe(false);
    // With `always`, a run that was aborted still restarts.
    expect(setup({ ...aborted, policy: 'always' }).result.current).toBe(true);
  });

  it('is not alive with a suspended workflow, nor without a policy', () => {
    const running = { status: Status.Running, finishedAt: '-' };
    expect(setup({ ...running, suspended: true }).result.current).toBe(false);
    expect(setup({ ...running, policy: 'no' }).result.current).toBe(false);
  });
});
