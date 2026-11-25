// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useClient, useQuery } from '@/hooks/api';
import { useDAGListActivitySSE } from '@/hooks/useDAGListActivitySSE';
import { RequestTimeoutError } from '@/lib/requestTimeout';
import { useDAGListActivity } from '../useDAGListActivity';

vi.mock('@/hooks/api', () => ({ useQuery: vi.fn(), useClient: vi.fn() }));
vi.mock('@/hooks/useDAGListActivitySSE', () => ({
  useDAGListActivitySSE: vi.fn(),
}));

const useQueryMock = vi.mocked(
  useQuery as unknown as (
    path: string,
    init: unknown,
    config: Record<string, unknown>
  ) => unknown
);
vi.mocked(useClient).mockReturnValue({ GET: vi.fn() } as never);

const FIRST_PAGE_ONLY = { through: 1, readWith: '' };

function lastQueryConfig(): Record<string, unknown> | undefined {
  const calls = useQueryMock.mock.calls;
  return calls[calls.length - 1]?.[2];
}

describe('useDAGListActivity', () => {
  beforeEach(() => {
    vi.mocked(useDAGListActivitySSE).mockReturnValue({
      data: null,
      error: null,
      isConnected: false,
      isConnecting: false,
      shouldUseFallback: true,
    } as never);
    useQueryMock.mockReturnValue({ data: undefined, mutate: vi.fn() });
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  // SWR does not poll a key that holds an error: without a retry, one failed
  // read would stop the fallback poll.
  it('retries a failed read, but not on a node without the endpoint', () => {
    renderHook(() =>
      useDAGListActivity({ remoteNode: 'local' }, FIRST_PAGE_ONLY, true)
    );
    const retries = lastQueryConfig()?.shouldRetryOnError as (
      error: unknown
    ) => boolean;

    expect(retries({ code: 'internal_error', message: 'failed' })).toBe(true);
    expect(retries(new RequestTimeoutError(10_000))).toBe(true);
    expect(retries({ code: 'not_found', message: 'no such path' })).toBe(false);
    expect(retries({ status: 501, message: 'not implemented' })).toBe(false);
  });
});
