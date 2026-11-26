// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Status } from '@/api/v1/schema';
import { ACTIVITY_WINDOW_SECONDS } from '@/features/dags/components/dag-list/activityBarsModel';
import { useClient, useQuery } from '@/hooks/api';
import { useDAGRunChangesSSE } from '@/hooks/useDAGRunChangesSSE';
import { useDAGListActivity } from '../useDAGListActivity';

vi.mock('@/hooks/api', () => ({ useQuery: vi.fn(), useClient: vi.fn() }));
vi.mock('@/hooks/useDAGRunChangesSSE', () => ({
  useDAGRunChangesSSE: vi.fn(),
}));

const useQueryMock = vi.mocked(
  useQuery as unknown as (
    path: string,
    init: unknown,
    config: Record<string, unknown>
  ) => unknown
);
const useSSEMock = vi.mocked(useDAGRunChangesSSE);
const getMock = vi.fn();
vi.mocked(useClient).mockReturnValue({ GET: getMock } as never);

type GetInit = {
  params: { query: { fileName: string[]; window: number; remoteNode: string } };
  signal: AbortSignal;
};

const T0 = Date.parse('2026-11-25T00:00:00.000Z');
const at = (ms: number) => new Date(T0 + ms).toISOString();

/** The server time that the activity requests answer with. */
let readAt = at(0);
/** The change feed that the feed query holds. */
let feed: unknown;

function answer(names: string[], runs = 1) {
  return {
    data: {
      dags: names.map((fileName) => ({
        fileName,
        activity: {
          runs: Array.from({ length: runs }, (_, index) => ({
            dagRunId: `${fileName}-${index}`,
            status: Status.Success,
            startedAt: at(0),
          })),
          truncated: false,
        },
      })),
      errors: [],
      readAt,
    },
    response: { status: 200 },
  };
}

function failure(status: number) {
  return {
    data: undefined,
    error: { code: status === 404 ? 'not_found' : 'internal_error' },
    response: { status },
  };
}

function feedOf(
  since: number,
  readAtMs: number,
  ...changes: [string, number][]
) {
  return {
    since: at(since),
    readAt: at(readAtMs),
    changes: changes.map(([fileName, changedAt]) => ({
      fileName,
      changedAt: at(changedAt),
    })),
  };
}

function requested(): string[][] {
  return getMock.mock.calls.map(
    (call) => (call[1] as GetInit).params.query.fileName
  );
}

function lastCall<T>(mock: { mock: { calls: T[] } }): T | undefined {
  return mock.mock.calls[mock.mock.calls.length - 1];
}

function setVisibility(state: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    get: () => state,
  });
  document.dispatchEvent(new Event('visibilitychange'));
}

type Props = { names: string[]; node: string };

function renderActivity(names: string[], node = 'local') {
  return renderHook(
    (props: Props) => useDAGListActivity(props.names, props.node, true),
    { initialProps: { names, node } }
  );
}

/** Lets the effects and the answered reads run. */
async function settle() {
  for (let i = 0; i < 5; i++) {
    await act(async () => {});
  }
}

describe('useDAGListActivity', () => {
  beforeEach(() => {
    readAt = at(0);
    feed = undefined;
    setVisibility('visible');
    useSSEMock.mockReturnValue({
      data: null,
      error: null,
      isConnected: true,
      isConnecting: false,
      shouldUseFallback: false,
    } as never);
    useQueryMock.mockImplementation(() => ({ data: feed, mutate: vi.fn() }));
    getMock.mockImplementation(async (_path: string, init: GetInit) =>
      answer(init.params.query.fileName)
    );
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  it('reads the listed workflows in requests of 100', async () => {
    const names = Array.from({ length: 250 }, (_, index) => `dag-${index}`);
    const { result } = renderActivity(names, 'edge');

    await waitFor(() => expect(result.current?.size).toBe(250));
    expect(requested().map((batch) => batch.length)).toEqual([100, 100, 50]);
    expect((getMock.mock.calls[0]?.[1] as GetInit).params.query).toEqual({
      fileName: names.slice(0, 100),
      window: ACTIVITY_WINDOW_SECONDS,
      remoteNode: 'edge',
    });
  });

  // Long names fill the 8 KiB request line that common proxies accept before
  // 100 of them do.
  it('keeps each request line under 8 KiB with long names', async () => {
    const names = Array.from(
      { length: 120 },
      (_, index) => `${'n'.repeat(96)}-${String(index).padStart(3, '0')}`
    );
    const { result } = renderActivity(names);

    await waitFor(() => expect(result.current?.size).toBe(120));
    expect(requested().flat()).toEqual(names);
    for (const batch of requested()) {
      const query = new URLSearchParams(
        batch.map((name) => ['fileName', name])
      ).toString();
      expect(query.length).toBeLessThan(8 * 1024);
    }
  });

  // A filter, a sort or a later page changes the rows: the rows that stay
  // keep the activity they have.
  it('reads only the workflows it does not hold', async () => {
    const { result, rerender } = renderActivity(['a', 'b']);
    await waitFor(() => expect(result.current?.size).toBe(2));

    rerender({ names: ['b', 'c'], node: 'local' });

    await waitFor(() => expect(result.current?.has('c')).toBe(true));
    expect(requested()).toEqual([['a', 'b'], ['c']]);
    expect([...(result.current?.keys() ?? [])].sort()).toEqual(['b', 'c']);
  });

  // The feed repeats its last minutes, so a change that was read already is
  // not read again.
  it('reads again only the workflows that the feed names', async () => {
    readAt = at(1000);
    const { result, rerender } = renderActivity(['a', 'b', 'c']);
    await waitFor(() => expect(result.current?.size).toBe(3));

    readAt = at(3000);
    feed = feedOf(0, 2000, ['b', 1500], ['other', 1600]);
    rerender({ names: ['a', 'b', 'c'], node: 'local' });
    await waitFor(() => expect(requested()).toHaveLength(2));
    expect(requested()[1]).toEqual(['b']);

    feed = { ...(feed as object) };
    rerender({ names: ['a', 'b', 'c'], node: 'local' });
    await settle();
    expect(requested()).toHaveLength(2);
  });

  // The feed holds the changes after its since only: a row read before it may
  // miss a change.
  it('reads all again when the feed starts after the reads', async () => {
    readAt = at(1000);
    const { result, rerender } = renderActivity(['a', 'b']);
    await waitFor(() => expect(result.current?.size).toBe(2));

    feed = feedOf(1500, 2000);
    rerender({ names: ['a', 'b'], node: 'local' });

    await waitFor(() =>
      expect(requested()).toEqual([
        ['a', 'b'],
        ['a', 'b'],
      ])
    );
  });

  // A feed that names no change of a row tells that the row is current up to
  // the feed, so the drop of an old change from a later feed does not read it.
  it('keeps a workflow current up to each feed', async () => {
    readAt = at(1000);
    const { result, rerender } = renderActivity(['a']);
    await waitFor(() => expect(result.current?.has('a')).toBe(true));

    feed = feedOf(0, 5000);
    rerender({ names: ['a'], node: 'local' });
    await settle();
    feed = feedOf(3000, 8000);
    rerender({ names: ['a'], node: 'local' });
    await settle();

    expect(requested()).toHaveLength(1);
  });

  it.each([
    { readAtMs: 1000, again: true },
    { readAtMs: 9000, again: false },
  ])(
    'checks a read against a feed that came during it (read at $readAtMs)',
    async ({ readAtMs, again }) => {
      let release: () => void = () => {};
      getMock.mockImplementationOnce(
        (_path: string, init: GetInit) =>
          new Promise((resolve) => {
            release = () => resolve(answer(init.params.query.fileName));
          })
      );
      const { rerender } = renderActivity(['b']);
      await waitFor(() => expect(requested()).toHaveLength(1));

      feed = feedOf(0, 6000, ['b', 5000]);
      rerender({ names: ['b'], node: 'local' });
      readAt = at(readAtMs);
      await act(async () => release());
      await settle();

      expect(requested()).toHaveLength(again ? 2 : 1);
    }
  );

  it('polls the feed every minute while the stream is unavailable', () => {
    useSSEMock.mockReturnValue({
      data: null,
      error: null,
      isConnected: false,
      isConnecting: false,
      shouldUseFallback: true,
    } as never);
    renderActivity(['a']);

    const [path, init, config] = lastCall(useQueryMock) ?? [];
    expect(path).toBe('/dag-run-changes');
    expect(init).toEqual({ params: { query: { remoteNode: 'local' } } });
    expect(config?.refreshInterval).toBe(60_000);
  });

  it('reads nothing while the tab is hidden', async () => {
    setVisibility('hidden');
    const { result } = renderActivity(['a']);
    await settle();

    expect(getMock).not.toHaveBeenCalled();
    expect(lastCall(useSSEMock)?.[0]).toBe(false);
    expect(lastCall(useQueryMock)?.[1]).toBeNull();

    act(() => setVisibility('visible'));
    await waitFor(() => expect(result.current?.has('a')).toBe(true));
  });

  // Two nodes can each hold a deploy workflow, which is not the same one.
  it('drops the activity of another node', async () => {
    const { result, rerender } = renderActivity(['deploy'], 'a');
    await waitFor(() => expect(result.current?.has('deploy')).toBe(true));

    let release: () => void = () => {};
    getMock.mockImplementationOnce(
      (_path: string, init: GetInit) =>
        new Promise((resolve) => {
          release = () => resolve(answer(init.params.query.fileName, 5));
        })
    );
    rerender({ names: ['deploy'], node: 'b' });

    expect(result.current?.has('deploy')).toBe(false);
    await waitFor(() => expect(requested()).toHaveLength(2));
    expect(
      (getMock.mock.calls[1]?.[1] as GetInit).params.query.remoteNode
    ).toBe('b');

    rerender({ names: ['deploy'], node: 'c' });
    await act(async () => release());
    await waitFor(() =>
      expect(result.current?.get('deploy')?.runs).toHaveLength(1)
    );
  });

  it.each([404, 501])(
    'gives no activity for a node that answers %i',
    async (status) => {
      getMock.mockImplementation(async () => failure(status));
      const { result } = renderActivity(['a']);

      await waitFor(() => expect(result.current).toBeUndefined());
      expect(lastCall(useSSEMock)?.[0]).toBe(false);
      await settle();
      expect(getMock).toHaveBeenCalledTimes(1);
    }
  );

  // A node with the earlier form of the endpoint answers without readAt.
  // Without the event store the feed answers 501, and the list would read
  // every row each minute: it gives no activity instead.
  it.each([404, 501])(
    'gives no activity for a node whose feed answers %i',
    async (status) => {
      const error = { code: 'internal_error', response: { status } };
      useQueryMock.mockImplementation(() => ({
        data: undefined,
        error,
        mutate: vi.fn(),
      }));
      const { result } = renderActivity(['a']);

      await waitFor(() => expect(result.current).toBeUndefined());
      expect(lastCall(useSSEMock)?.[0]).toBe(false);
    }
  );

  it('gives no activity for a node with the earlier endpoint', async () => {
    getMock.mockImplementation(async (_path: string, init: GetInit) => {
      const page = answer(init.params.query.fileName);
      return { ...page, data: { ...page.data, readAt: undefined } };
    });
    const { result } = renderActivity(['a']);

    await waitFor(() => expect(result.current).toBeUndefined());
  });

  it('keeps the activity when a read fails, and reads again later', async () => {
    vi.useFakeTimers();
    readAt = at(1000);
    const { result, rerender } = renderActivity(['a']);
    await act(() => vi.advanceTimersByTimeAsync(0));
    expect(result.current?.get('a')?.runs).toHaveLength(1);

    getMock.mockImplementationOnce(async () => failure(500));
    feed = feedOf(0, 3000, ['a', 2000]);
    rerender({ names: ['a'], node: 'local' });
    await act(() => vi.advanceTimersByTimeAsync(0));
    expect(requested()).toHaveLength(2);
    expect(result.current?.get('a')?.runs).toHaveLength(1);

    await act(() => vi.advanceTimersByTimeAsync(4_999));
    expect(requested()).toHaveLength(2);
    readAt = at(9000);
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(requested()).toHaveLength(3);
  });

  it('aborts the reads in flight when it unmounts', async () => {
    getMock.mockImplementation(() => new Promise(() => {}));
    const { unmount } = renderActivity(['a']);
    await waitFor(() => expect(getMock).toHaveBeenCalled());

    unmount();

    expect((getMock.mock.calls[0]?.[1] as GetInit).signal.aborted).toBe(true);
  });
});
