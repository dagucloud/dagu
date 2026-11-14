// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useSelfUpgrade } from '../useSelfUpgrade';

const { client } = vi.hoisted(() => ({
  client: { GET: vi.fn(), POST: vi.fn() },
}));
vi.mock('../api', () => ({ useClient: () => client }));

describe('useSelfUpgrade', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    client.GET.mockReset();
    client.POST.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // A server that stops answering while it installs is restarting; it is
  // back once it reports another version.
  it('follows the update until Dagu comes back with another version', async () => {
    const onRestarted = vi.fn();
    client.POST.mockResolvedValue({
      data: { available: true, state: 'installing' },
    });
    client.GET.mockImplementation((path: string) => {
      if (path === '/system/upgrade') {
        return Promise.reject(new TypeError('Failed to fetch'));
      }
      return Promise.resolve({ data: { version: '2.1.0' } });
    });
    const { result } = renderHook(() => useSelfUpgrade('2.0.0', onRestarted));

    await act(() => result.current.start());
    expect(result.current.status?.state).toBe('installing');

    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(result.current.status?.state).toBe('restarting');
    expect(onRestarted).not.toHaveBeenCalled();

    await act(() => vi.advanceTimersByTimeAsync(2000));
    expect(client.GET).toHaveBeenCalledWith('/health');
    expect(onRestarted).toHaveBeenCalled();
  });

  it('keeps waiting while the old version still answers', async () => {
    const onRestarted = vi.fn();
    client.POST.mockResolvedValue({
      data: { available: true, state: 'restarting' },
    });
    client.GET.mockResolvedValue({ data: { version: '2.0.0' } });
    const { result } = renderHook(() => useSelfUpgrade('2.0.0', onRestarted));

    await act(() => result.current.start());
    await act(() => vi.advanceTimersByTimeAsync(6000));

    expect(onRestarted).not.toHaveBeenCalled();
    expect(result.current.slowRestart).toBe(false);
    await act(() => vi.advanceTimersByTimeAsync(120_000));
    expect(result.current.slowRestart).toBe(true);
  });

  it('follows the download progress', async () => {
    client.POST.mockResolvedValue({
      data: {
        available: true,
        state: 'downloading',
        downloadedBytes: 10,
        totalBytes: 100,
      },
    });
    client.GET.mockResolvedValue({
      data: {
        available: true,
        state: 'downloading',
        downloadedBytes: 60,
        totalBytes: 100,
      },
    });
    const { result } = renderHook(() => useSelfUpgrade('2.0.0', vi.fn()));

    await act(() => result.current.start());
    await act(() => vi.advanceTimersByTimeAsync(1000));

    expect(result.current.status?.downloadedBytes).toBe(60);
  });

  // An install that fails after one missed poll is still reported by the
  // server that keeps running.
  it('reports a failure the running server reports while restarting', async () => {
    const onRestarted = vi.fn();
    client.POST.mockResolvedValue({
      data: { available: true, state: 'restarting' },
    });
    client.GET.mockImplementation((path: string) =>
      Promise.resolve(
        path === '/health'
          ? { data: { version: '2.0.0' } }
          : { data: { available: true, state: 'failed', error: 'disk full' } }
      )
    );
    const { result } = renderHook(() => useSelfUpgrade('2.0.0', onRestarted));

    await act(() => result.current.start());
    await act(() => vi.advanceTimersByTimeAsync(2000));

    expect(result.current.status?.state).toBe('failed');
    expect(result.current.status?.error).toBe('disk full');
    expect(onRestarted).not.toHaveBeenCalled();
  });

  it('reports a start request that cannot reach the server', async () => {
    client.POST.mockRejectedValue(new TypeError('Failed to fetch'));
    const { result } = renderHook(() => useSelfUpgrade('2.0.0', vi.fn()));

    await act(() => result.current.start());

    expect(result.current.requestError).toBe('Failed to fetch');
    expect(result.current.status).toBeNull();
  });
});
