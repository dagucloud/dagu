// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it } from 'vitest';
import { MissedSlotsReason } from '@/api/v1/schema';
import { useMissedSlotsNotice } from '../useMissedSlotsNotice';

const gap = {
  from: '2026-11-01T09:00:00Z',
  to: '2026-11-01T21:00:00Z',
  reason: MissedSlotsReason.suspended,
  slots: 12,
};

function wrapper({ children }: { children: ReactNode }) {
  return (
    <MemoryRouter initialEntries={['/dags/etl?run=latest']}>
      {children}
    </MemoryRouter>
  );
}

function useNoticeAndLocation(missed: typeof gap | undefined, dag = 'etl') {
  return {
    notice: useMissedSlotsNotice(missed, dag, 'local'),
    location: useLocation(),
  };
}

describe('useMissedSlotsNotice', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('says how many slots passed and why', () => {
    const { result } = renderHook(() => useNoticeAndLocation(gap), {
      wrapper,
    });

    expect(result.current.notice?.message).toBe(
      '12 slots passed while this DAG was suspended'
    );
  });

  // The action opens the Backfills tab, whose start panel opens on the gap.
  it('opens the Backfills tab on the gap', () => {
    const { result } = renderHook(() => useNoticeAndLocation(gap), {
      wrapper,
    });

    act(() => result.current.notice?.action?.run());

    const search = new URLSearchParams(result.current.location.search);
    expect(result.current.location.pathname).toBe('/dags/etl');
    expect(search.get('run')).toBe('latest');
    expect(search.get('panel')).toBe('workflow');
    expect(search.get('wtab')).toBe('backfills');
    expect(search.get('gapFrom')).toBe(gap.from);
    expect(search.get('gapTo')).toBe(gap.to);
  });

  // The scheduler counts up to 10,000 slots, so a gap at the cap may hold
  // more.
  it('tells a count at the cap as a lower bound', () => {
    const { result } = renderHook(
      () => useNoticeAndLocation({ ...gap, slots: 10000 }),
      { wrapper }
    );

    expect(result.current.notice?.message).toBe(
      '10000+ slots passed while this DAG was suspended'
    );
  });

  // An outage makes the same gap for every DAG; dismissing it for one
  // leaves the others.
  it('stays dismissed for that DAG only', () => {
    const { result, rerender } = renderHook(
      ({ dag }) => useNoticeAndLocation(gap, dag),
      { wrapper, initialProps: { dag: 'etl' } }
    );

    act(() => result.current.notice?.dismiss?.());
    expect(result.current.notice).toBeNull();

    rerender({ dag: 'report' });
    expect(result.current.notice).not.toBeNull();
  });

  it('stays dismissed for that gap only', () => {
    const { result, rerender } = renderHook(
      ({ missed }) => useNoticeAndLocation(missed),
      { wrapper, initialProps: { missed: gap } }
    );

    act(() => result.current.notice?.dismiss?.());
    expect(result.current.notice).toBeNull();

    rerender({ missed: { ...gap, to: '2026-11-02T09:00:00Z' } });
    expect(result.current.notice).not.toBeNull();
  });
});
