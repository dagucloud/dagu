import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useExactDAGRuns, type DAGRunListQuery } from '../dagRunPagination';

const getMock = vi.fn();
const client = {
  GET: getMock,
};
const { useDAGRunsListSSEMock } = vi.hoisted(() => ({
  useDAGRunsListSSEMock: vi.fn(() => ({
    data: null,
    error: null,
    isConnected: false,
    isConnecting: false,
    shouldUseFallback: true,
  })),
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => client,
  useQuery: vi.fn(),
}));

vi.mock('@/hooks/useDAGRunsListSSE', () => ({
  useDAGRunsListSSE: useDAGRunsListSSEMock,
}));

function createQuery(
  overrides: Partial<DAGRunListQuery> = {}
): DAGRunListQuery {
  return {
    fromDate: 100,
    ...overrides,
  };
}

function createDeferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe('useExactDAGRuns', () => {
  beforeEach(() => {
    getMock.mockReset();
    useDAGRunsListSSEMock.mockClear();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('does not refetch when rerendered with an equivalent inline query object', async () => {
    getMock.mockResolvedValue({ data: { dagRuns: [] } });

    const { rerender } = renderHook(({ query }) => useExactDAGRuns({ query }), {
      initialProps: { query: createQuery() },
    });

    await waitFor(() => {
      expect(getMock).toHaveBeenCalledTimes(1);
    });

    rerender({ query: createQuery() });

    await act(async () => {
      await Promise.resolve();
    });

    expect(getMock).toHaveBeenCalledTimes(1);
  });

  it('refetches exactly once when the semantic query changes', async () => {
    getMock.mockResolvedValue({ data: { dagRuns: [] } });

    const { rerender } = renderHook(({ query }) => useExactDAGRuns({ query }), {
      initialProps: { query: createQuery() },
    });

    await waitFor(() => {
      expect(getMock).toHaveBeenCalledTimes(1);
    });

    rerender({ query: createQuery({ fromDate: 200, name: 'billing' }) });

    await waitFor(() => {
      expect(getMock).toHaveBeenCalledTimes(2);
    });

    expect(getMock.mock.calls[1]?.[1]).toMatchObject({
      params: {
        query: expect.objectContaining({
          fromDate: 200,
          name: 'billing',
          remoteNode: 'local',
        }),
      },
    });
  });

  it('refresh uses the latest query after rerender instead of a stale closure', async () => {
    getMock.mockResolvedValue({ data: { dagRuns: [] } });

    const { result, rerender } = renderHook(
      ({ query }) => useExactDAGRuns({ query }),
      {
        initialProps: { query: createQuery({ fromDate: 100 }) },
      }
    );

    await waitFor(() => {
      expect(getMock).toHaveBeenCalledTimes(1);
    });

    rerender({ query: createQuery({ fromDate: 300, name: 'ops' }) });

    await waitFor(() => {
      expect(getMock).toHaveBeenCalledTimes(2);
    });

    await act(async () => {
      await result.current.refresh();
    });

    expect(getMock).toHaveBeenCalledTimes(3);
    expect(getMock.mock.calls[2]?.[1]).toMatchObject({
      params: {
        query: expect.objectContaining({
          fromDate: 300,
          name: 'ops',
          remoteNode: 'local',
        }),
      },
    });
  });

  it('ignores stale responses after a query change and aborts the older request', async () => {
    const firstRequest = createDeferred<{
      data: { dagRuns: Array<{ name: string; dagRunId: string }> };
    }>();
    const secondRequest = createDeferred<{
      data: { dagRuns: Array<{ name: string; dagRunId: string }> };
    }>();

    getMock
      .mockImplementationOnce(
        (_path: string, request?: { signal?: AbortSignal }) => {
          return firstRequest.promise.then((value) => {
            if (request?.signal?.aborted) {
              throw new DOMException('Aborted', 'AbortError');
            }
            return value;
          });
        }
      )
      .mockImplementationOnce(
        (_path: string, request?: { signal?: AbortSignal }) => {
          return secondRequest.promise.then((value) => {
            if (request?.signal?.aborted) {
              throw new DOMException('Aborted', 'AbortError');
            }
            return value;
          });
        }
      );

    const { result, rerender } = renderHook(
      ({ query }) => useExactDAGRuns({ query }),
      {
        initialProps: { query: createQuery({ fromDate: 100 }) },
      }
    );

    await waitFor(() => {
      expect(getMock).toHaveBeenCalledTimes(1);
    });

    const firstSignal = getMock.mock.calls[0]?.[1]?.signal as AbortSignal;

    rerender({ query: createQuery({ fromDate: 400, name: 'new-dag' }) });

    await waitFor(() => {
      expect(getMock).toHaveBeenCalledTimes(2);
    });

    expect(firstSignal.aborted).toBe(true);

    firstRequest.resolve({
      data: {
        dagRuns: [{ name: 'stale', dagRunId: 'old' }],
      },
    });
    secondRequest.resolve({
      data: {
        dagRuns: [{ name: 'fresh', dagRunId: 'new' }],
      },
    });

    await waitFor(() => {
      expect(result.current.data).toEqual([{ name: 'fresh', dagRunId: 'new' }]);
    });
  });

  it('streams the first page before pagination completes and uses smaller page limits', async () => {
    const firstPage = createDeferred<{
      data: {
        dagRuns: Array<{ name: string; dagRunId: string }>;
        nextCursor: string;
      };
    }>();
    const secondPage = createDeferred<{
      data: { dagRuns: Array<{ name: string; dagRunId: string }> };
    }>();

    getMock
      .mockImplementationOnce(() => firstPage.promise)
      .mockImplementationOnce(() => secondPage.promise);

    const { result } = renderHook(() =>
      useExactDAGRuns({ query: createQuery() })
    );

    await waitFor(() => {
      expect(getMock).toHaveBeenCalledTimes(1);
    });

    expect(getMock.mock.calls[0]?.[1]).toMatchObject({
      params: {
        query: expect.objectContaining({
          remoteNode: 'local',
          fromDate: 100,
          limit: 100,
        }),
      },
    });
    expect(result.current.isLoading).toBe(true);

    firstPage.resolve({
      data: {
        dagRuns: [{ name: 'first', dagRunId: 'run-1' }],
        nextCursor: 'cursor-1',
      },
    });

    await waitFor(() => {
      expect(result.current.data).toEqual([
        { name: 'first', dagRunId: 'run-1' },
      ]);
    });

    expect(result.current.isLoading).toBe(false);
    expect(result.current.isValidating).toBe(true);

    await waitFor(() => {
      expect(getMock).toHaveBeenCalledTimes(2);
    });

    expect(getMock.mock.calls[1]?.[1]).toMatchObject({
      params: {
        query: expect.objectContaining({
          remoteNode: 'local',
          fromDate: 100,
          limit: 100,
          cursor: 'cursor-1',
        }),
      },
    });

    secondPage.resolve({
      data: {
        dagRuns: [{ name: 'second', dagRunId: 'run-2' }],
      },
    });

    await waitFor(() => {
      expect(result.current.data).toEqual([
        { name: 'first', dagRunId: 'run-1' },
        { name: 'second', dagRunId: 'run-2' },
      ]);
    });

    expect(result.current.isValidating).toBe(false);
  });

  it('polls only on the configured fallback interval instead of every rerender', async () => {
    vi.useFakeTimers();
    getMock.mockResolvedValue({ data: { dagRuns: [] } });

    renderHook(() =>
      useExactDAGRuns({
        query: createQuery(),
        fallbackIntervalMs: 2000,
      })
    );

    await act(async () => {
      await Promise.resolve();
    });
    expect(getMock).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1999);
    });
    expect(getMock).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(getMock).toHaveBeenCalledTimes(2);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    expect(getMock).toHaveBeenCalledTimes(3);
  });

  it('does not keep polling while the DAG-runs SSE topic is healthy', async () => {
    vi.useFakeTimers();
    getMock.mockResolvedValue({ data: { dagRuns: [] } });
    useDAGRunsListSSEMock.mockReturnValue({
      data: null,
      error: null,
      isConnected: true,
      isConnecting: false,
      shouldUseFallback: false,
    });

    renderHook(() =>
      useExactDAGRuns({
        query: createQuery(),
        fallbackIntervalMs: 2000,
      })
    );

    await act(async () => {
      await Promise.resolve();
    });
    expect(getMock).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000);
    });

    expect(getMock).toHaveBeenCalledTimes(1);
  });

  it('keeps polling while the DAG-runs SSE topic is still connecting', async () => {
    vi.useFakeTimers();
    getMock.mockResolvedValue({ data: { dagRuns: [] } });
    useDAGRunsListSSEMock.mockReturnValue({
      data: null,
      error: null,
      isConnected: false,
      isConnecting: true,
      shouldUseFallback: false,
    });

    renderHook(() =>
      useExactDAGRuns({
        query: createQuery(),
        fallbackIntervalMs: 2000,
      })
    );

    await act(async () => {
      await Promise.resolve();
    });
    expect(getMock).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });

    expect(getMock).toHaveBeenCalledTimes(2);
  });
});
