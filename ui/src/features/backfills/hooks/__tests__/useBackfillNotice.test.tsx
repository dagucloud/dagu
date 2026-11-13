// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  BackfillOnFailure,
  BackfillRerun,
  BackfillStatus,
  type components,
} from '@/api/v1/schema';
import {
  DISMISSED_BACKFILLS_KEY,
  useBackfillNotice,
} from '../useBackfillNotice';

type Backfill = components['schemas']['Backfill'];

const sse = vi.hoisted(() => ({
  data: null as Backfill | null,
  calls: [] as unknown[][],
}));

vi.mock('@/hooks/useBackfillSSE', () => ({
  useBackfillSSE: (...args: unknown[]) => {
    sse.calls.push(args);
    return {
      data: sse.data,
      isConnected: true,
      isConnecting: false,
      shouldUseFallback: false,
    };
  },
}));

const wrapper = ({ children }: { children: ReactNode }) => (
  <MemoryRouter>{children}</MemoryRouter>
);

function backfill(overrides: Partial<Backfill> = {}): Backfill {
  return {
    id: 'bf123456',
    dag: 'etl',
    status: BackfillStatus.running,
    submittedAt: '2026-11-01T00:00:00Z',
    rerun: BackfillRerun.none,
    maxConcurrency: 1,
    onFailure: BackfillOnFailure.stop,
    slotCount: 28,
    counts: { succeeded: 12, pending: 16 },
    ...overrides,
  };
}

describe('useBackfillNotice', () => {
  beforeEach(() => {
    localStorage.clear();
    sse.data = null;
    sse.calls = [];
  });

  it('shows the progress of an open backfill from its live feed', () => {
    sse.data = backfill({ counts: { succeeded: 13, failed: 1, pending: 14 } });
    const { result } = renderHook(
      () => useBackfillNotice(backfill(), 'local'),
      { wrapper }
    );

    expect(result.current?.message).toBe(
      'Backfill running: 14 of 28 slots done, 1 failed'
    );
    expect(result.current?.dismissible).toBeFalsy();
    expect(sse.calls[sse.calls.length - 1]).toEqual(['bf123456', true, 'local']);
  });

  it('keeps a failure notice until it is dismissed, then for good', () => {
    const failed = backfill({
      status: BackfillStatus.failed,
      counts: { succeeded: 8, failed: 1, 'not run': 19 },
    });
    const first = renderHook(() => useBackfillNotice(failed, 'local'), {
      wrapper,
    });
    expect(first.result.current?.message).toBe(
      'Backfill failed: 20 slots did not succeed'
    );
    expect(sse.calls[sse.calls.length - 1]).toEqual(['bf123456', false, 'local']);

    act(() => first.result.current?.dismiss?.());
    expect(first.result.current).toBeNull();
    expect(localStorage.getItem(DISMISSED_BACKFILLS_KEY)).toContain('bf123456');

    const again = renderHook(() => useBackfillNotice(failed, 'local'), {
      wrapper,
    });
    expect(again.result.current).toBeNull();
  });

  it('shows nothing without a latest backfill', () => {
    const { result } = renderHook(() => useBackfillNotice(undefined, 'local'), {
      wrapper,
    });
    expect(result.current).toBeNull();
  });
});
