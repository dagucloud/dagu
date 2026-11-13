// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import {
  UnsavedChangesProvider,
  useUnsavedChanges,
} from '../UnsavedChangesContext';

const wrapper = ({ children }: { children: ReactNode }) => (
  <UnsavedChangesProvider>{children}</UnsavedChangesProvider>
);

describe('UnsavedChangesContext save', () => {
  it('saves with the registered editor, until the editor withdraws', async () => {
    const { result } = renderHook(() => useUnsavedChanges(), { wrapper });
    expect(result.current.canSave).toBe(false);
    expect(await result.current.save()).toBe(false);

    let withdraw = () => {};
    act(() => {
      withdraw = result.current.registerSave(async () => true);
    });
    expect(result.current.canSave).toBe(true);
    expect(await result.current.save()).toBe(true);

    act(() => withdraw());
    expect(result.current.canSave).toBe(false);
    expect(await result.current.save()).toBe(false);
  });

  it('keeps the save of a later editor when an earlier one withdraws', async () => {
    const { result } = renderHook(() => useUnsavedChanges(), { wrapper });
    let withdrawFirst = () => {};
    act(() => {
      withdrawFirst = result.current.registerSave(async () => false);
      result.current.registerSave(async () => true);
    });

    act(() => withdrawFirst());
    expect(result.current.canSave).toBe(true);
    expect(await result.current.save()).toBe(true);
  });

  it('resolves false when the save throws', async () => {
    const logged = vi.spyOn(console, 'error').mockImplementation(() => {});
    const { result } = renderHook(() => useUnsavedChanges(), { wrapper });
    act(() => {
      result.current.registerSave(async () => {
        throw new Error('network down');
      });
    });

    expect(await result.current.save()).toBe(false);
    expect(logged).toHaveBeenCalled();
    logged.mockRestore();
  });
});
