// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { components } from '@/api/v1/schema';
import {
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import { overlaySample } from '../../testing/fixtures';
import { useRunOverlay } from '../useRunOverlay';

type DAGRunDetails = components['schemas']['DAGRunDetails'];

const { useQueryMock, useSSEMock } = vi.hoisted(() => ({
  useQueryMock: vi.fn(),
  useSSEMock: vi.fn(),
}));

vi.mock('@/hooks/api', () => ({ useQuery: useQueryMock }));
vi.mock('@/hooks/useDAGRunSSE', () => ({ useDAGRunSSE: useSSEMock }));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'node-1',
}));

const idleSSE = {
  data: null,
  error: null,
  isConnected: true,
  isConnecting: false,
  shouldUseFallback: false,
};

function details(
  dagRunId: string,
  status: NodeStatus = NodeStatus.Success
): DAGRunDetails {
  return {
    dagRunId,
    name: 'dag',
    status: Status.Success,
    statusLabel: StatusLabel.succeeded,
    nodes: [
      {
        step: { name: 'fetch' },
        stdout: '',
        stderr: '',
        startedAt: '2026-01-15T02:00:00Z',
        finishedAt: '2026-01-15T02:00:03Z',
        status,
        statusLabel: NodeStatusLabel.succeeded,
        retryCount: 0,
        doneCount: 1,
      },
    ],
  } as DAGRunDetails;
}

describe('useRunOverlay', () => {
  beforeEach(() => {
    useQueryMock.mockReset();
    useQueryMock.mockReturnValue({ data: undefined, isLoading: false });
    useSSEMock.mockReset();
    useSSEMock.mockReturnValue(idleSSE);
  });

  it('makes no request without a run', () => {
    const { result } = renderHook(() =>
      useRunOverlay({ dagName: 'dag', runId: null })
    );
    expect(useQueryMock.mock.calls[0]?.[1]).toBeNull();
    expect(useSSEMock.mock.calls.every((call) => call[2] === false)).toBe(true);
    expect(result.current).toEqual({
      overlay: {},
      runId: null,
      isLoading: false,
    });
  });

  it('gives the agent facts only for the run of an agent DAG', () => {
    useQueryMock.mockReturnValue({
      data: {
        dagRunDetails: {
          ...details('r1'),
          agentTasks: [{ name: 'goal', status: 'open' }],
          agentEvents: [{ turn: 2, kind: 'action', name: 'fetch' }],
        },
      },
      isLoading: false,
    });
    const { result } = renderHook(() =>
      useRunOverlay({ dagName: 'dag', runId: 'r1' })
    );
    expect(result.current.agent).toEqual({
      iterations: 2,
      calls: { fetch: 1 },
      tasks: { goal: 'open' },
    });
  });

  it('requests the run with remoteNode', () => {
    renderHook(() => useRunOverlay({ dagName: 'dag', runId: 'r1' }));
    expect(useQueryMock).toHaveBeenCalledWith(
      '/dag-runs/{name}/{dagRunId}',
      {
        params: {
          path: { name: 'dag', dagRunId: 'r1' },
          query: { remoteNode: 'node-1' },
        },
      },
      expect.anything()
    );
  });

  it('resolves latest to the run ID of the details', () => {
    useQueryMock.mockReturnValue({
      data: { dagRunDetails: details('20260115_020000') },
      isLoading: false,
    });
    const { result } = renderHook(() =>
      useRunOverlay({ dagName: 'dag', runId: 'latest' })
    );
    expect(result.current.runId).toBe('20260115_020000');
    expect(result.current.overlay.fetch?.status).toBe(
      overlaySample.fetch?.status
    );
  });

  it('has no run ID while latest is loading', () => {
    useQueryMock.mockReturnValue({ data: undefined, isLoading: true });
    const { result } = renderHook(() =>
      useRunOverlay({ dagName: 'dag', runId: 'latest' })
    );
    expect(result.current).toEqual({
      overlay: {},
      runId: null,
      isLoading: true,
    });
  });

  it('follows a concrete run over SSE', () => {
    renderHook(() => useRunOverlay({ dagName: 'dag', runId: 'r1' }));
    expect(useSSEMock).toHaveBeenLastCalledWith('dag', 'r1', true, 'node-1');
  });

  it('prefers SSE data to the query data', () => {
    useQueryMock.mockReturnValue({
      data: { dagRunDetails: details('r1', NodeStatus.Running) },
      isLoading: false,
    });
    useSSEMock.mockReturnValue({
      ...idleSSE,
      data: { dagRunDetails: details('r1', NodeStatus.Success) },
    });
    const { result } = renderHook(() =>
      useRunOverlay({ dagName: 'dag', runId: 'r1' })
    );
    expect(result.current.overlay.fetch?.status).toBe(NodeStatus.Success);
  });

  it('uses the query data when SSE falls back', () => {
    useQueryMock.mockReturnValue({
      data: { dagRunDetails: details('r1', NodeStatus.Running) },
      isLoading: false,
    });
    useSSEMock.mockReturnValue({
      ...idleSSE,
      isConnected: false,
      shouldUseFallback: true,
      data: { dagRunDetails: details('r1', NodeStatus.Success) },
    });
    const { result } = renderHook(() =>
      useRunOverlay({ dagName: 'dag', runId: 'r1' })
    );
    expect(result.current.overlay.fetch?.status).toBe(NodeStatus.Running);
    expect(useQueryMock.mock.calls[0]?.[2].refreshInterval).toBeGreaterThan(0);
  });

  // SSE follows the resolved run: the query polls only as its fallback.
  it('follows the resolved run of latest and does not poll with SSE up', () => {
    useQueryMock.mockReturnValue({
      data: { dagRunDetails: details('r9') },
      isLoading: false,
    });
    const { result } = renderHook(() =>
      useRunOverlay({ dagName: 'dag', runId: 'latest' })
    );
    expect(useSSEMock).toHaveBeenLastCalledWith('dag', 'r9', true, 'node-1');
    expect(result.current.runId).toBe('r9');
    expect(useQueryMock.mock.lastCall?.[2].refreshInterval).toBe(0);
  });

  it('ignores SSE data of another run', () => {
    useQueryMock.mockReturnValue({
      data: { dagRunDetails: details('r1', NodeStatus.Running) },
      isLoading: false,
    });
    useSSEMock.mockReturnValue({
      ...idleSSE,
      data: { dagRunDetails: details('r0', NodeStatus.Success) },
    });
    const { result } = renderHook(() =>
      useRunOverlay({ dagName: 'dag', runId: 'r1' })
    );
    expect(result.current.overlay.fetch?.status).toBe(NodeStatus.Running);
  });
});
