// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import {
  RestartPolicy,
  ServiceDAGState,
  type components,
} from '@/api/v1/schema';
import { useServiceDAGs } from '../useServiceDAGs';

type ServiceDAG = components['schemas']['ServiceDAG'];

const response = vi.hoisted(() => ({
  dags: [] as ServiceDAG[],
  error: undefined as unknown,
}));

vi.mock('@/hooks/api', () => ({
  useQuery: () => ({
    data: { dags: response.dags, errors: [] },
    error: response.error,
    mutate: vi.fn(),
  }),
}));

vi.mock('@/hooks/useServiceDAGsSSE', () => ({
  useServiceDAGsSSE: () => ({
    data: null,
    error: null,
    isConnected: false,
    isConnecting: false,
    shouldUseFallback: false,
  }),
}));

function dag(name: string, overrides: Partial<ServiceDAG> = {}): ServiceDAG {
  return {
    fileName: name,
    name,
    restartPolicy: RestartPolicy.always,
    restartWaitSec: 5,
    queue: name,
    state: ServiceDAGState.running,
    suspended: false,
    consecutiveFailures: 0,
    ...overrides,
  };
}

describe('useServiceDAGs', () => {
  // The server sorts by name; a DAG that keeps failing or waits for a person
  // must not hide in a long list.
  it('puts the DAGs that need a person first and keeps the order of each group', () => {
    response.dags = [
      dag('a'),
      dag('b', { state: ServiceDAGState.failing }),
      dag('c'),
      dag('d', { consecutiveFailures: 2 }),
      dag('e', { state: ServiceDAGState.waiting }),
    ];

    const { result } = renderHook(() => useServiceDAGs());

    expect(result.current.serviceDAGs.map((d) => d.name)).toEqual([
      'b',
      'd',
      'e',
      'a',
      'c',
    ]);
  });

  // A query fails with the error body of the API, which is no Error object.
  it('reports a list that cannot be read', () => {
    response.error = { code: 'internal_error', message: 'store is down' };

    const { result } = renderHook(() => useServiceDAGs());

    expect(result.current.error).toEqual({
      code: 'internal_error',
      message: 'store is down',
    });
    response.error = undefined;
  });
});
