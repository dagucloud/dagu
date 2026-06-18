// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useDAGRunSSE } from '@/hooks/useDAGRunSSE';
import { useSubDAGRunSSE } from '@/hooks/useSubDAGRunSSE';
import { useBoundedDAGRunDetails } from '../useBoundedDAGRunDetails';

type TestSSEState = {
  current: {
    data: { dagRunDetails: { dagRunId?: string; name?: string } } | null;
    error: Error | null;
    isConnected: boolean;
    isConnecting: boolean;
    shouldUseFallback: boolean;
  };
};

const { fetchDAGRunDetailsMock, dagRunSSEState, subDAGRunSSEState } =
  vi.hoisted<{
    fetchDAGRunDetailsMock: ReturnType<typeof vi.fn>;
    dagRunSSEState: TestSSEState;
    subDAGRunSSEState: TestSSEState;
  }>(() => ({
    fetchDAGRunDetailsMock: vi.fn(),
    dagRunSSEState: {
      current: {
        data: null,
        error: null,
        isConnected: true,
        isConnecting: false,
        shouldUseFallback: false,
      },
    },
    subDAGRunSSEState: {
      current: {
        data: null,
        error: null,
        isConnected: false,
        isConnecting: false,
        shouldUseFallback: true,
      },
    },
  }));

vi.mock('@/hooks/useDAGRunSSE', () => ({
  useDAGRunSSE: vi.fn(() => dagRunSSEState.current),
}));

vi.mock('@/hooks/useSubDAGRunSSE', () => ({
  useSubDAGRunSSE: vi.fn(() => subDAGRunSSEState.current),
}));

vi.mock('../dagRunDetailsRequest', () => ({
  fetchDAGRunDetails: fetchDAGRunDetailsMock,
  matchesRequestedDAGRunDetails: (
    details: { dagRunId?: string; name?: string } | null | undefined,
    requestedDagRunId: string,
    requestedName?: string
  ) => {
    if (!details) {
      return false;
    }
    if (requestedName && details.name !== requestedName) {
      return false;
    }
    return (
      requestedDagRunId === 'latest' || details.dagRunId === requestedDagRunId
    );
  },
}));

function createTarget(overrides: Record<string, string> = {}) {
  return {
    remoteNode: 'local',
    name: 'billing',
    dagRunId: 'run-1',
    ...overrides,
  };
}

function createDeferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

describe('useBoundedDAGRunDetails', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    fetchDAGRunDetailsMock.mockReset();
    vi.mocked(useDAGRunSSE).mockClear();
    vi.mocked(useSubDAGRunSSE).mockClear();
    dagRunSSEState.current = {
      data: null,
      error: null,
      isConnected: true,
      isConnecting: false,
      shouldUseFallback: false,
    };
    subDAGRunSSEState.current = {
      data: null,
      error: null,
      isConnected: false,
      isConnecting: false,
      shouldUseFallback: true,
    };
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('does not keep polling while the DAG-run SSE topic is healthy', async () => {
    fetchDAGRunDetailsMock.mockResolvedValue({ dagRunId: 'run-1' });

    renderHook(() =>
      useBoundedDAGRunDetails({
        target: createTarget(),
        pollIntervalMs: 2000,
      })
    );

    await act(async () => {
      await Promise.resolve();
    });
    expect(fetchDAGRunDetailsMock).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000);
    });

    expect(fetchDAGRunDetailsMock).toHaveBeenCalledTimes(1);
    expect(useDAGRunSSE).toHaveBeenCalledWith(
      'billing',
      'run-1',
      true,
      'local'
    );
  });

  it('keeps polling while the DAG-run SSE topic is still connecting', async () => {
    dagRunSSEState.current = {
      data: null,
      error: null,
      isConnected: false,
      isConnecting: true,
      shouldUseFallback: false,
    };
    fetchDAGRunDetailsMock.mockResolvedValue({ dagRunId: 'run-1' });

    renderHook(() =>
      useBoundedDAGRunDetails({
        target: createTarget(),
        pollIntervalMs: 2000,
      })
    );

    await act(async () => {
      await Promise.resolve();
    });
    expect(fetchDAGRunDetailsMock).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });

    expect(fetchDAGRunDetailsMock).toHaveBeenCalledTimes(2);
  });

  it('hydrates from SSE payloads and aborts the in-flight fallback request', async () => {
    const deferred = createDeferred<{ dagRunId: string }>();
    let capturedSignal: AbortSignal | undefined;
    fetchDAGRunDetailsMock.mockImplementation(
      (_target: unknown, init?: { signal?: AbortSignal }) => {
        capturedSignal = init?.signal;
        return deferred.promise;
      }
    );

    const { result, rerender } = renderHook(() =>
      useBoundedDAGRunDetails({
        target: createTarget(),
        pollIntervalMs: 2000,
      })
    );

    await act(async () => {
      await Promise.resolve();
    });
    expect(fetchDAGRunDetailsMock).toHaveBeenCalledTimes(1);

    act(() => {
      dagRunSSEState.current = {
        data: {
          dagRunDetails: {
            dagRunId: 'run-1',
            name: 'billing',
          },
        },
        error: null,
        isConnected: true,
        isConnecting: false,
        shouldUseFallback: false,
      };
      rerender();
    });

    await act(async () => {
      await Promise.resolve();
    });
    expect(result.current.data).toMatchObject({
      dagRunId: 'run-1',
      name: 'billing',
    });

    expect(capturedSignal?.aborted).toBe(true);
  });

  it('clears previous details when the request target changes', async () => {
    const deferred = createDeferred<{ dagRunId: string; name: string }>();
    fetchDAGRunDetailsMock
      .mockResolvedValueOnce({ dagRunId: 'run-1', name: 'billing' })
      .mockReturnValueOnce(deferred.promise);

    const { result, rerender } = renderHook(
      ({ target }) =>
        useBoundedDAGRunDetails({
          target,
          pollIntervalMs: 2000,
        }),
      {
        initialProps: {
          target: createTarget(),
        },
      }
    );

    await act(async () => {
      await Promise.resolve();
    });
    expect(result.current.data).toMatchObject({
      dagRunId: 'run-1',
      name: 'billing',
    });

    rerender({
      target: createTarget({
        remoteNode: 'remote-b',
        name: 'deploy',
        dagRunId: 'latest',
      }),
    });

    await act(async () => {
      await Promise.resolve();
    });
    expect(result.current.data).toBeNull();
  });

  it('passes the selected remote node to sub DAG-run SSE subscriptions', () => {
    renderHook(() =>
      useBoundedDAGRunDetails({
        target: createTarget({
          remoteNode: 'remote-a',
          parentName: 'billing',
          parentDAGRunId: 'parent-run',
          subDAGRunId: 'sub-run',
        }),
      })
    );

    expect(useSubDAGRunSSE).toHaveBeenCalledWith(
      'billing',
      'parent-run',
      'sub-run',
      true,
      'remote-a'
    );
  });

  it('hydrates sub DAG-run details from SSE payloads without matching the root DAG name', async () => {
    fetchDAGRunDetailsMock.mockResolvedValue({
      dagRunId: 'fetched-run',
      name: 'fetched-dag',
    });
    subDAGRunSSEState.current = {
      data: {
        dagRunDetails: {
          dagRunId: 'sub-run',
          name: 'child-dag',
        },
      },
      error: null,
      isConnected: true,
      isConnecting: false,
      shouldUseFallback: false,
    };

    const { result } = renderHook(() =>
      useBoundedDAGRunDetails({
        target: createTarget({
          name: 'root-dag',
          dagRunId: 'root-run',
          parentName: 'root-dag',
          parentDAGRunId: 'root-run',
          subDAGRunId: 'sub-run',
        }),
        pollIntervalMs: 2000,
      })
    );

    await act(async () => {
      await Promise.resolve();
    });

    expect(result.current.data).toMatchObject({
      dagRunId: 'sub-run',
      name: 'child-dag',
    });
  });
});
