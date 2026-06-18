// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { sseFallbackOptions } from '../useSSECacheSync';
import { buildSSEEndpoint, useSSE } from '../useSSE';
import { sseManager } from '../SSEManager';

class MockEventSource {
  static instances: MockEventSource[] = [];

  readonly url: string;
  readonly close = vi.fn();
  onerror: (() => void) | null = null;
  private listeners = new Map<
    string,
    Set<(event: MessageEvent<string>) => void>
  >();

  constructor(url: string) {
    this.url = url;
    MockEventSource.instances.push(this);
  }

  addEventListener(
    type: string,
    listener: (event: MessageEvent<string>) => void
  ) {
    const listeners = this.listeners.get(type) ?? new Set();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  emit(type: string, data: unknown, lastEventId = '') {
    const listeners = this.listeners.get(type);
    if (!listeners) {
      return;
    }

    const event = {
      data: JSON.stringify(data),
      lastEventId,
    } as MessageEvent<string>;
    for (const listener of listeners) {
      listener(event);
    }
  }
}

const testConfig = {
  apiURL: '/api/v1',
  authMode: 'none',
} as Config;

function wrapper({ children }: { children: React.ReactNode }) {
  return React.createElement(
    ConfigContext.Provider,
    { value: testConfig },
    children
  );
}

describe('buildSSEEndpoint', () => {
  it('serializes array query values as repeated parameters', () => {
    expect(
      buildSSEEndpoint('/events/dag-runs', {
        status: [5, 1],
        fromDate: 100,
      })
    ).toBe('/events/dag-runs?status=5&status=1&fromDate=100');
  });
});

describe('sseFallbackOptions', () => {
  it('disables polling when SSE is connected even before the first payload', () => {
    expect(
      sseFallbackOptions({
        data: null,
        error: null,
        isConnected: true,
        isConnecting: false,
        shouldUseFallback: false,
      })
    ).toMatchObject({
      revalidateIfStale: false,
      revalidateOnFocus: false,
      refreshInterval: 0,
    });
  });

  it('keeps polling disabled while SSE is reconnecting before fallback', () => {
    expect(
      sseFallbackOptions({
        data: null,
        error: new Error('SSE connection lost'),
        isConnected: false,
        isConnecting: true,
        shouldUseFallback: false,
      })
    ).toMatchObject({
      revalidateIfStale: false,
      revalidateOnFocus: false,
      refreshInterval: 0,
    });
  });

  it('enables polling when the SSE connection asks for fallback', () => {
    expect(
      sseFallbackOptions(
        {
          data: null,
          error: new Error('SSE connection lost'),
          isConnected: false,
          isConnecting: false,
          shouldUseFallback: true,
        },
        5000
      )
    ).toMatchObject({
      revalidateIfStale: true,
      revalidateOnFocus: true,
      refreshInterval: 5000,
    });
  });
});

describe('useSSE', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    MockEventSource.instances = [];
    vi.stubGlobal('EventSource', MockEventSource);
  });

  afterEach(() => {
    sseManager.disposeAll();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('does not treat cached payload replay as an active topic connection', async () => {
    const first = renderHook(
      () => useSSE<{ value: string }>('/events/dags/test.yaml', true, 'local'),
      { wrapper }
    );

    const eventSource = MockEventSource.instances[0];
    expect(eventSource).toBeDefined();
    if (!eventSource) {
      throw new Error('expected EventSource instance');
    }

    await act(async () => {
      eventSource.emit('control', {
        sessionID: 'session-1',
        subscribed: ['dag:test.yaml'],
      });
      eventSource.emit('message', {
        topic: 'dag:test.yaml',
        payload: { value: 'cached' },
      });
    });

    expect(first.result.current).toMatchObject({
      data: { value: 'cached' },
      isConnected: true,
      isConnecting: false,
    });

    await act(async () => {
      eventSource.onerror?.();
    });

    expect(first.result.current).toMatchObject({
      isConnected: false,
      isConnecting: true,
    });

    const second = renderHook(
      () => useSSE<{ value: string }>('/events/dags/test.yaml', true, 'local'),
      { wrapper }
    );

    expect(second.result.current).toMatchObject({
      data: { value: 'cached' },
      isConnected: false,
      isConnecting: true,
    });

    first.unmount();
    second.unmount();
  });
});
