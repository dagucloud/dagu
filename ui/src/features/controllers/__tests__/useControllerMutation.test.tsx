// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useControllerMutation } from '../useControllerMutation';

const { showToast } = vi.hoisted(() => ({ showToast: vi.fn() }));

vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast }),
}));

describe('useControllerMutation', () => {
  beforeEach(() => {
    showToast.mockReset();
  });

  it('reports success when only the follow-up refresh fails', async () => {
    const refresh = vi.fn().mockRejectedValue(new Error('refresh failed'));
    const action = vi.fn().mockResolvedValue(undefined);
    const { result } = renderHook(() =>
      useControllerMutation(refresh, 'Refresh failed after success.', 'scope-a')
    );
    let succeeded = false;

    await act(async () => {
      succeeded = await result.current.run(action, 'Action completed');
    });

    expect(succeeded).toBe(true);
    expect(result.current.pending).toBe(false);
    expect(result.current.error).toBe('Refresh failed after success.');
    expect(showToast).toHaveBeenCalledWith('Action completed');
  });

  it('does not refresh or toast after a failed action', async () => {
    const refresh = vi.fn().mockResolvedValue(undefined);
    const action = vi.fn().mockRejectedValue(new Error('action failed'));
    const { result } = renderHook(() =>
      useControllerMutation(refresh, 'Refresh failed after success.', 'scope-a')
    );
    let succeeded = true;

    await act(async () => {
      succeeded = await result.current.run(action, 'Action completed');
    });

    expect(succeeded).toBe(false);
    expect(result.current.pending).toBe(false);
    expect(result.current.error).toBe('action failed');
    expect(refresh).not.toHaveBeenCalled();
    expect(showToast).not.toHaveBeenCalled();
  });

  it('runs only one action at a time', async () => {
    let releaseAction!: () => void;
    const firstAction = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          releaseAction = resolve;
        })
    );
    const secondAction = vi.fn().mockResolvedValue(undefined);
    const refresh = vi.fn().mockResolvedValue(undefined);
    const { result } = renderHook(() =>
      useControllerMutation(refresh, 'Refresh failed after success.', 'scope-a')
    );
    let firstResult!: Promise<boolean>;
    let secondSucceeded = true;

    act(() => {
      firstResult = result.current.run(firstAction, 'First completed');
    });
    await act(async () => {
      secondSucceeded = await result.current.run(
        secondAction,
        'Second completed'
      );
    });

    expect(secondSucceeded).toBe(false);
    expect(secondAction).not.toHaveBeenCalled();
    expect(result.current.pending).toBe(true);

    await act(async () => {
      releaseAction();
      expect(await firstResult).toBe(true);
    });

    expect(refresh).toHaveBeenCalledTimes(1);
    expect(showToast).toHaveBeenCalledWith('First completed');
    expect(result.current.pending).toBe(false);
  });

  it('ignores a completed action after its scope changes', async () => {
    let resolveAction!: () => void;
    const action = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveAction = resolve;
        })
    );
    const refresh = vi.fn().mockResolvedValue(undefined);
    const { result, rerender } = renderHook(
      ({ scope }) =>
        useControllerMutation(refresh, 'Refresh failed after success.', scope),
      { initialProps: { scope: 'scope-a' } }
    );
    let completed!: Promise<boolean>;

    act(() => {
      completed = result.current.run(action, 'Action completed');
    });
    expect(result.current.pending).toBe(true);

    rerender({ scope: 'scope-b' });
    expect(result.current.pending).toBe(false);
    await act(async () => {
      resolveAction();
      expect(await completed).toBe(false);
    });

    expect(refresh).not.toHaveBeenCalled();
    expect(showToast).not.toHaveBeenCalled();
    expect(result.current.error).toBeNull();
    expect(result.current.pending).toBe(false);
  });

  it('does not refresh or toast after unmount', async () => {
    let resolveAction!: () => void;
    const action = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveAction = resolve;
        })
    );
    const refresh = vi.fn().mockResolvedValue(undefined);
    const { result, unmount } = renderHook(() =>
      useControllerMutation(refresh, 'Refresh failed after success.', 'scope-a')
    );
    let completed!: Promise<boolean>;

    act(() => {
      completed = result.current.run(action, 'Action completed');
    });
    unmount();
    await act(async () => {
      resolveAction();
      expect(await completed).toBe(false);
    });

    expect(refresh).not.toHaveBeenCalled();
    expect(showToast).not.toHaveBeenCalled();
  });

  it('does not expose an action error to a new scope', async () => {
    let rejectAction!: (error: Error) => void;
    const action = vi.fn(
      () =>
        new Promise<void>((_, reject) => {
          rejectAction = reject;
        })
    );
    const refresh = vi.fn().mockResolvedValue(undefined);
    const { result, rerender } = renderHook(
      ({ scope }) =>
        useControllerMutation(refresh, 'Refresh failed after success.', scope),
      { initialProps: { scope: 'scope-a' } }
    );
    let completed!: Promise<boolean>;

    act(() => {
      completed = result.current.run(action, 'Action completed');
    });
    rerender({ scope: 'scope-b' });
    await act(async () => {
      rejectAction(new Error('old scope failed'));
      expect(await completed).toBe(false);
    });

    expect(result.current.error).toBeNull();
    expect(result.current.pending).toBe(false);
    expect(refresh).not.toHaveBeenCalled();
    expect(showToast).not.toHaveBeenCalled();
  });
});
