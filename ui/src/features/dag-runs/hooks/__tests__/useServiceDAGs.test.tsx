// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { RestartPolicy, ServiceDAGState } from '@/api/v1/schema';
import type { ServiceDAG } from '../useServiceDAGs';
import { failingFirst, useServiceDAGs } from '../useServiceDAGs';

const h = vi.hoisted(() => ({
  query: [] as Array<{ path: string; init: unknown }>,
  topic: [] as Array<{
    params: unknown;
    enabled: boolean;
    remoteNode?: string;
  }>,
  dags: [] as unknown[],
}));

vi.mock('@/hooks/api', () => ({
  useQuery: (path: string, init: unknown) => {
    h.query.push({ path, init });
    return {
      data: init === null ? undefined : { dags: h.dags, errors: [] },
      mutate: vi.fn(),
    };
  },
}));

vi.mock('@/hooks/useServiceDAGsSSE', () => ({
  useServiceDAGsSSE: (
    params: unknown,
    enabled: boolean,
    remoteNode?: string
  ) => {
    h.topic.push({ params, enabled, remoteNode });
    return {
      data: null,
      error: null,
      isConnected: true,
      isConnecting: false,
      shouldUseFallback: false,
    };
  },
}));

function dag(name: string, state: ServiceDAGState): ServiceDAG {
  return {
    fileName: name,
    name,
    restartPolicy: RestartPolicy.always,
    restartWaitSec: 5,
    state,
    suspended: false,
    consecutiveFailures: 0,
  } as ServiceDAG;
}

beforeEach(() => {
  h.query.length = 0;
  h.topic.length = 0;
  h.dags = [];
});

describe('failingFirst', () => {
  it('puts the failing DAGs first and keeps the other order', () => {
    const sorted = failingFirst([
      dag('a', ServiceDAGState.running),
      dag('b', ServiceDAGState.failing),
      dag('c', ServiceDAGState.restarting),
      dag('d', ServiceDAGState.failing),
    ]);
    expect(sorted.map((item) => item.name)).toEqual(['b', 'd', 'a', 'c']);
  });
});

describe('useServiceDAGs', () => {
  it('reads the list of the node and the workspace, and its live topic', () => {
    h.dags = [
      dag('a', ServiceDAGState.running),
      dag('b', ServiceDAGState.failing),
    ];
    const { result } = renderHook(() =>
      useServiceDAGs({ remoteNode: 'edge', workspace: 'ops' }, true)
    );
    expect(h.query[0]).toEqual({
      path: '/service-dags',
      init: { params: { query: { remoteNode: 'edge', workspace: 'ops' } } },
    });
    expect(h.topic[0]).toEqual({
      params: { workspace: 'ops' },
      enabled: true,
      remoteNode: 'edge',
    });
    expect(result.current.map((item) => item.name)).toEqual(['b', 'a']);
  });

  it('reads nothing while it is disabled', () => {
    h.dags = [dag('a', ServiceDAGState.running)];
    const { result } = renderHook(() =>
      useServiceDAGs({ remoteNode: 'local' }, false)
    );
    expect(h.query[0]!.init).toBeNull();
    expect(h.topic[0]!.enabled).toBe(false);
    expect(result.current).toEqual([]);
  });
});
