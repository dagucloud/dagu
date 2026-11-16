// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { MissedSlotsReason, type components } from '@/api/v1/schema';
import { useQuery } from '@/hooks/api';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { useMissedSlotsNotice } from '../useMissedSlotsNotice';

const { post, showError } = vi.hoisted(() => ({
  post: vi.fn(),
  showError: vi.fn(),
}));
vi.mock('@/hooks/api', () => ({
  useQuery: vi.fn(),
  useClient: () => ({ POST: post }),
}));
vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError }),
}));
const useQueryMock = useQuery as unknown as ReturnType<typeof vi.fn>;
const mutate = vi.fn();

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
    mutate,
  });
  return {
    notice: useMissedSlotsNotice(`${dag}.yaml`, 'local', canBackfill),
    location: useLocation(),
  };
}

describe('useMissedSlotsNotice', () => {
  beforeEach(() => {
    useQueryMock.mockReset();
    mutate.mockReset();
    post.mockReset();
    showError.mockReset();
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

  // The server keeps the dismissal for every user and answers no missed
  // slots until a newer span adds some, so the page drops the slots it read
  // instead of reading them again.
  it('dismisses the missed slots for every user', async () => {
    post.mockResolvedValue({});
    const { result } = renderHook(() => useNoticeAndLocation(missed), {
      wrapper,
    });

    await act(async () => result.current.notice?.dismiss?.());

    expect(post).toHaveBeenCalledWith(
      '/dags/{fileName}/missed-slots/dismissal',
      {
        params: {
          path: { fileName: 'etl.yaml' },
          query: { remoteNode: 'local' },
        },
      }
    );
    await waitFor(() =>
      expect(mutate).toHaveBeenCalledWith({}, { revalidate: false })
    );
    expect(showError).not.toHaveBeenCalled();
  });

  // A dismissal the server did not keep leaves the notice and says so,
  // whether the server refused it or could not be reached.
  it.each([
    ['refused', () => post.mockResolvedValue({ error: { message: 'no' } })],
    ['unreachable', () => post.mockRejectedValue(new Error('offline'))],
  ])(
    'keeps the notice and shows an error when the dismissal is %s',
    async (_, fail) => {
      fail();
      const { result } = renderHook(() => useNoticeAndLocation(missed), {
        wrapper,
      });

      await act(async () => result.current.notice?.dismiss?.());

      expect(mutate).not.toHaveBeenCalled();
      expect(result.current.notice).not.toBeNull();
      expect(showError).toHaveBeenCalledOnce();
    }
  );
});
