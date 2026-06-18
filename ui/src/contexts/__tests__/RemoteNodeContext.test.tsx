// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '../AppBarContext';
import { RemoteNodeProvider, useRemoteNode } from '../RemoteNodeContext';

const appBarValue = {
  setTitle: vi.fn(),
  selectedRemoteNode: 'app-node',
};

describe('useRemoteNode', () => {
  it('uses an explicit override before scoped and app-bar values', () => {
    const { result } = renderHook(() => useRemoteNode('override-node'), {
      wrapper: ({ children }) => (
        <AppBarContext.Provider value={appBarValue as never}>
          <RemoteNodeProvider remoteNode="scoped-node">
            {children}
          </RemoteNodeProvider>
        </AppBarContext.Provider>
      ),
    });

    expect(result.current).toBe('override-node');
  });

  it('uses the scoped remote node before the app-bar value', () => {
    const { result } = renderHook(() => useRemoteNode(), {
      wrapper: ({ children }) => (
        <AppBarContext.Provider value={appBarValue as never}>
          <RemoteNodeProvider remoteNode="scoped-node">
            {children}
          </RemoteNodeProvider>
        </AppBarContext.Provider>
      ),
    });

    expect(result.current).toBe('scoped-node');
  });

  it('trims remote node sources before returning them', () => {
    const overrideResult = renderHook(() => useRemoteNode(' override-node '), {
      wrapper: ({ children }) => (
        <AppBarContext.Provider
          value={{ ...appBarValue, selectedRemoteNode: ' app-node ' } as never}
        >
          <RemoteNodeProvider remoteNode=" scoped-node ">
            {children}
          </RemoteNodeProvider>
        </AppBarContext.Provider>
      ),
    });
    expect(overrideResult.result.current).toBe('override-node');

    const scopedResult = renderHook(() => useRemoteNode(), {
      wrapper: ({ children }) => (
        <AppBarContext.Provider
          value={{ ...appBarValue, selectedRemoteNode: ' app-node ' } as never}
        >
          <RemoteNodeProvider remoteNode=" scoped-node ">
            {children}
          </RemoteNodeProvider>
        </AppBarContext.Provider>
      ),
    });
    expect(scopedResult.result.current).toBe('scoped-node');

    const appBarResult = renderHook(() => useRemoteNode(), {
      wrapper: ({ children }) => (
        <AppBarContext.Provider
          value={{ ...appBarValue, selectedRemoteNode: ' app-node ' } as never}
        >
          <RemoteNodeProvider remoteNode="   ">{children}</RemoteNodeProvider>
        </AppBarContext.Provider>
      ),
    });
    expect(appBarResult.result.current).toBe('app-node');
  });

  it('falls back to the app-bar value and then local', () => {
    const appBarResult = renderHook(() => useRemoteNode(), {
      wrapper: ({ children }) => (
        <AppBarContext.Provider value={appBarValue as never}>
          {children}
        </AppBarContext.Provider>
      ),
    });
    expect(appBarResult.result.current).toBe('app-node');

    const localResult = renderHook(() => useRemoteNode(), {
      wrapper: ({ children }) => (
        <AppBarContext.Provider
          value={{ ...appBarValue, selectedRemoteNode: '' } as never}
        >
          {children}
        </AppBarContext.Provider>
      ),
    });
    expect(localResult.result.current).toBe('local');
  });
});
