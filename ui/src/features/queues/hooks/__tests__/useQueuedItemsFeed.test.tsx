// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook, waitFor } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useQueuedItemsFeed } from '../useQueuedItemsFeed';

const getMock = vi.fn();
const client = {
  GET: getMock,
};

vi.mock('@/hooks/api', () => ({
  useClient: () => client,
}));

const feedProps = { enabled: true, queueName: 'q', refreshToken: '' };

function expectLastCursor(cursor: string): void {
  expect(getMock).toHaveBeenLastCalledWith(
    '/queues/{name}/items',
    expect.objectContaining({
      params: expect.objectContaining({
        query: expect.objectContaining({ cursor }),
      }),
    })
  );
}

describe('useQueuedItemsFeed', () => {
  beforeEach(() => {
    getMock.mockReset();
    getMock
      .mockResolvedValueOnce({ data: { items: [], nextCursor: 'cursor-1' } })
      .mockResolvedValue({ data: { items: [] } });
  });

  // A layout effect runs in the commit that reports hasMore, before passive
  // effects: the earliest point a caller can act on the rendered page.
  it('loads the next page as soon as hasMore is reported', async () => {
    renderHook(() => {
      const feed = useQueuedItemsFeed(feedProps);
      const { hasMore, loadMore } = feed;
      React.useLayoutEffect(() => {
        if (hasMore) {
          void loadMore();
        }
      }, [hasMore, loadMore]);
      return feed;
    });

    await waitFor(() => {
      expect(getMock).toHaveBeenCalledTimes(2);
    });
    expectLastCursor('cursor-1');
  });

  it('requests the next page once while a load is in flight', async () => {
    const { result } = renderHook(() => useQueuedItemsFeed(feedProps));
    await waitFor(() => {
      expect(result.current.hasMore).toBe(true);
    });

    act(() => {
      void result.current.loadMore();
      void result.current.loadMore();
    });

    await waitFor(() => {
      expect(result.current.hasMore).toBe(false);
    });
    expect(getMock).toHaveBeenCalledTimes(2);
    expectLastCursor('cursor-1');
  });
});
