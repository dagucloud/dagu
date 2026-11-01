// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  resetRunAvailability,
  RunLinksContext,
  useRunAvailability,
  useRunLinks,
  type RunLinks,
} from '../RunLinks';

function links(over: Partial<RunLinks> = {}): RunLinks {
  return {
    select: vi.fn(),
    resolve: () => null,
    exists: vi.fn(async () => true),
    activate: vi.fn(),
    ...over,
  };
}

function wrap(value: RunLinks | null) {
  return ({ children }: { children: ReactNode }) => (
    <RunLinksContext.Provider value={value}>
      {children}
    </RunLinksContext.Provider>
  );
}

beforeEach(() => {
  resetRunAvailability();
});

describe('useRunLinks', () => {
  it('has no links outside a host', () => {
    expect(renderHook(() => useRunLinks()).result.current).toBeNull();
  });

  it('asks the host to read the history once a mark uses the links', () => {
    const value = links();
    renderHook(() => useRunLinks(), { wrapper: wrap(value) });
    expect(value.activate).toHaveBeenCalledTimes(1);
  });
});

describe('useRunAvailability', () => {
  it('is unknown without a host or a run', () => {
    expect(
      renderHook(() => useRunAvailability('r1', true)).result.current
    ).toEqual({ state: 'unknown' });
    expect(
      renderHook(() => useRunAvailability(undefined, true), {
        wrapper: wrap(links()),
      }).result.current
    ).toEqual({ state: 'unknown' });
  });

  it('is available, with the id of the history, and asks nothing', () => {
    const value = links({ resolve: () => 'R1' });
    const view = renderHook(() => useRunAvailability('r1', true), {
      wrapper: wrap(value),
    });
    expect(view.result.current).toEqual({ state: 'available', runId: 'R1' });
    expect(value.exists).not.toHaveBeenCalled();
  });

  it('asks the server for a run that the history does not hold, only while enabled', async () => {
    const value = links();
    const view = renderHook(
      ({ enabled }) => useRunAvailability('r1', enabled),
      { wrapper: wrap(value), initialProps: { enabled: false } }
    );
    expect(view.result.current).toEqual({ state: 'unknown' });
    expect(value.exists).not.toHaveBeenCalled();
    view.rerender({ enabled: true });
    expect(view.result.current).toEqual({ state: 'checking' });
    await waitFor(() =>
      expect(view.result.current).toEqual({ state: 'available', runId: 'r1' })
    );
    expect(value.exists).toHaveBeenCalledTimes(1);
  });

  it('does not give the answer of one run to the next run', async () => {
    const value = links({ exists: vi.fn(async () => false) });
    const view = renderHook(
      ({ runId, enabled }) => useRunAvailability(runId, enabled),
      { wrapper: wrap(value), initialProps: { runId: 'r1', enabled: true } }
    );
    await waitFor(() => expect(view.result.current).toEqual({ state: 'gone' }));
    view.rerender({ runId: 'r2', enabled: false });
    expect(view.result.current).toEqual({ state: 'unknown' });
  });

  it('says that a run is gone when the server does not have it, and remembers it', async () => {
    const value = links({ exists: vi.fn(async () => false) });
    const first = renderHook(() => useRunAvailability('r9', true), {
      wrapper: wrap(value),
    });
    await waitFor(() =>
      expect(first.result.current).toEqual({ state: 'gone' })
    );
    const second = renderHook(() => useRunAvailability('r9', true), {
      wrapper: wrap(value),
    });
    expect(second.result.current).toEqual({ state: 'gone' });
    expect(value.exists).toHaveBeenCalledTimes(1);
  });

  // Two workflows, or two servers, can each have a run with the same id.
  it('keeps the answers of one scope from another', async () => {
    const first = links({ scope: 'a', exists: vi.fn(async () => false) });
    const gone = renderHook(() => useRunAvailability('r1', true), {
      wrapper: wrap(first),
    });
    await waitFor(() => expect(gone.result.current).toEqual({ state: 'gone' }));
    const second = links({ scope: 'b', exists: vi.fn(async () => true) });
    const view = renderHook(() => useRunAvailability('r1', true), {
      wrapper: wrap(second),
    });
    await waitFor(() =>
      expect(view.result.current).toEqual({ state: 'available', runId: 'r1' })
    );
    expect(second.exists).toHaveBeenCalledTimes(1);
  });
});
