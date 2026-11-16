// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, onTestFinished, vi } from 'vitest';
import { MissedSlotsReason, type components } from '@/api/v1/schema';
import { useQuery } from '@/hooks/api';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { useMissedSlotsNotice } from '../useMissedSlotsNotice';

vi.mock('@/hooks/api', () => ({ useQuery: vi.fn() }));
const useQueryMock = useQuery as unknown as ReturnType<typeof vi.fn>;

type MissedSlots = components['schemas']['MissedSlots'];

const missed: MissedSlots = {
  slots: 12,
  first: '2026-11-01T09:00:00Z',
  last: '2026-11-01T20:00:00Z',
  reasons: [MissedSlotsReason.suspended],
  until: '2026-11-01T20:30:00Z',
};

function wrapper({ children }: { children: ReactNode }) {
  return (
    <MemoryRouter initialEntries={['/dags/etl?run=latest']}>
      {children}
    </MemoryRouter>
  );
}

const bound = (value: string) => formatAbsoluteTime(value, { seconds: false });

// The first and last slot as the notice tells them, in the browser's zone.
const span = `from ${bound(missed.first)} to ${bound(missed.last)}`;

// The endpoint gives the DAG's missed slots as given.
function useNoticeAndLocation(
  given: MissedSlots | undefined,
  dag = 'etl',
  canBackfill = true
) {
  useQueryMock.mockReturnValue({
    data: given ? { missedSlots: given } : {},
  });
  return {
    notice: useMissedSlotsNotice(`${dag}.yaml`, dag, 'local', canBackfill),
    location: useLocation(),
  };
}

describe('useMissedSlotsNotice', () => {
  beforeEach(() => {
    localStorage.clear();
    useQueryMock.mockReset();
  });

  // The page reads the missed slots as it opens and after a backfill
  // starts; it neither polls nor reads them again on focus, on reconnect,
  // or after a failed read, which may be a slow first import of the DAG's
  // slot history.
  it("reads the DAG's missed slots without polling or retrying", () => {
    renderHook(() => useNoticeAndLocation(missed), { wrapper });

    expect(useQueryMock).toHaveBeenCalledWith(
      '/dags/{fileName}/missed-slots',
      {
        params: {
          path: { fileName: 'etl.yaml' },
          query: { remoteNode: 'local' },
        },
      },
      {
        revalidateOnFocus: false,
        revalidateOnReconnect: false,
        shouldRetryOnError: false,
      }
    );
  });

  it('says how many slots passed, when, and why', () => {
    const { result } = renderHook(() => useNoticeAndLocation(missed), {
      wrapper,
    });

    expect(result.current.notice?.message).toBe(
      `12 slots passed ${span} while this DAG was suspended`
    );
  });

  it('says when a single slot passed', () => {
    const { result } = renderHook(
      () =>
        useNoticeAndLocation({
          ...missed,
          slots: 1,
          last: missed.first,
          reasons: [MissedSlotsReason.down],
        }),
      { wrapper }
    );

    expect(result.current.notice?.message).toBe(
      `1 slot passed at ${bound(missed.first)} while the scheduler was down`
    );
  });

  // Slots missed for more than one reason name none of them.
  it('says the DAG could not run for slots missed for several reasons', () => {
    const { result } = renderHook(
      () =>
        useNoticeAndLocation({
          ...missed,
          reasons: [MissedSlotsReason.paused, MissedSlotsReason.down],
        }),
      { wrapper }
    );

    expect(result.current.notice?.message).toBe(
      `12 slots passed ${span} while this DAG could not run`
    );
  });

  // At most 10,000 slots are looked at, so a count at the cap may be short.
  it('tells a count at the cap as a lower bound', () => {
    const { result } = renderHook(
      () => useNoticeAndLocation({ ...missed, slots: 10000 }),
      { wrapper }
    );

    expect(result.current.notice?.message).toBe(
      `10000+ slots passed ${span} while this DAG was suspended`
    );
  });

  it('shows nothing when no slot was missed', () => {
    const { result } = renderHook(() => useNoticeAndLocation(undefined), {
      wrapper,
    });

    expect(result.current.notice).toBeNull();
  });

  // A user who may not run the DAG can read and dismiss the notice only.
  it('offers no backfill to a user who may not run the DAG', () => {
    const { result } = renderHook(
      () => useNoticeAndLocation(missed, 'etl', false),
      { wrapper }
    );

    expect(result.current.notice?.action).toBeUndefined();
    expect(result.current.notice?.dismissible).toBe(true);
  });

  // The action opens the Backfills tab, whose start panel opens on the
  // first and last slot missed.
  it('opens the Backfills tab on the missed slots', () => {
    const { result } = renderHook(() => useNoticeAndLocation(missed), {
      wrapper,
    });

    act(() => result.current.notice?.action?.run());

    const search = new URLSearchParams(result.current.location.search);
    expect(result.current.location.pathname).toBe('/dags/etl');
    expect(search.get('run')).toBe('latest');
    expect(search.get('panel')).toBe('workflow');
    expect(search.get('wtab')).toBe('backfills');
    expect(search.get('gapFrom')).toBe(missed.first);
    expect(search.get('gapTo')).toBe(missed.last);
  });

  // An outage leaves every DAG missed slots; dismissing them for one DAG
  // leaves the others.
  it('stays dismissed for that DAG only', () => {
    const { result, rerender } = renderHook(
      ({ dag }) => useNoticeAndLocation(missed, dag),
      { wrapper, initialProps: { dag: 'etl' } }
    );

    act(() => result.current.notice?.dismiss?.());
    expect(result.current.notice).toBeNull();

    rerender({ dag: 'report' });
    expect(result.current.notice).not.toBeNull();
  });

  // A backfill of some of the slots leaves the newest span as it was; a
  // newer span that adds missed slots shows the notice again.
  it('stays dismissed until a newer span adds missed slots', () => {
    const { result, rerender } = renderHook(
      ({ given }) => useNoticeAndLocation(given),
      { wrapper, initialProps: { given: missed } }
    );

    act(() => result.current.notice?.dismiss?.());
    rerender({ given: { ...missed, first: '2026-11-01T15:00:00Z', slots: 6 } });
    expect(result.current.notice).toBeNull();

    rerender({ given: { ...missed, until: '2026-11-02T09:30:00Z' } });
    expect(result.current.notice).not.toBeNull();
  });

  // A backfill of the newest span's slots leaves an older span's, whose end
  // is earlier than the one dismissed.
  it('stays dismissed when a backfill fills the newest span', () => {
    const { result, rerender } = renderHook(
      ({ given }) => useNoticeAndLocation(given),
      { wrapper, initialProps: { given: missed } }
    );

    act(() => result.current.notice?.dismiss?.());
    rerender({ given: { ...missed, slots: 3, until: '2026-11-01T12:30:00Z' } });
    expect(result.current.notice).toBeNull();
  });

  // The DAG page renders again on each live update; the dismissal is read
  // from the browser only when the missed slots or the DAG change.
  it('reads the dismissal again only when the notice may change', () => {
    const getItem = vi.spyOn(Storage.prototype, 'getItem');
    onTestFinished(() => getItem.mockRestore());
    const { rerender } = renderHook(() => useNoticeAndLocation(missed), {
      wrapper,
    });
    const reads = getItem.mock.calls.length;

    rerender();
    rerender();

    expect(getItem).toHaveBeenCalledTimes(reads);
  });

  // One outage leaves every scheduled DAG missed slots, and each dismissal
  // holds however many DAGs were dismissed after it.
  it('keeps the dismissal of every DAG', () => {
    const dismissing = renderHook(
      ({ dag }) => useNoticeAndLocation(missed, dag),
      { wrapper, initialProps: { dag: 'dag-0' } }
    );
    for (let i = 0; i < 30; i++) {
      dismissing.rerender({ dag: `dag-${i}` });
      act(() => dismissing.result.current.notice?.dismiss?.());
    }
    dismissing.unmount();

    const { result } = renderHook(() => useNoticeAndLocation(missed, 'dag-0'), {
      wrapper,
    });
    expect(result.current.notice).toBeNull();
  });
});
