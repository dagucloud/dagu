// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// SWR arms a refresh timer even for a skipped query (null init). The shared
// useQuery drops the refresh interval of a skipped query, so that no timer
// wakes the page for nothing.

import { beforeEach, describe, expect, it, vi } from 'vitest';

const swrQuery = vi.hoisted(() => vi.fn());

vi.mock('swr-openapi', () => ({
  createQueryHook: () => swrQuery,
  createImmutableHook: () => vi.fn(),
  createInfiniteHook: () => vi.fn(),
}));

describe('useQuery refresh of a skipped query', () => {
  beforeEach(() => {
    swrQuery.mockReset();
    vi.stubGlobal('getConfig', () => ({ apiURL: '/api/v1' }));
  });

  it('drops the refresh interval when the init is null', async () => {
    const { useQuery } = await import('../api');
    (useQuery as unknown as (...a: unknown[]) => unknown)('/dags', null, {
      refreshInterval: 2000,
      revalidateOnFocus: false,
    });
    expect(swrQuery).toHaveBeenCalledWith('/dags', null, {
      refreshInterval: 0,
      revalidateOnFocus: false,
    });
  });

  it('keeps the refresh interval of a query that runs', async () => {
    const { useQuery } = await import('../api');
    const config = { refreshInterval: 2000 };
    (useQuery as unknown as (...a: unknown[]) => unknown)('/dags', {}, config);
    expect(swrQuery).toHaveBeenCalledWith('/dags', {}, config);
  });
});
