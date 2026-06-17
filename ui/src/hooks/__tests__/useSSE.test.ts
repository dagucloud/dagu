// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { sseFallbackOptions } from '../useSSECacheSync';
import { buildSSEEndpoint } from '../useSSE';

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

  it('keeps polling disabled while SSE is reconnecting below the fallback threshold', () => {
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

  it('enables polling only when the SSE connection asks for fallback', () => {
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
