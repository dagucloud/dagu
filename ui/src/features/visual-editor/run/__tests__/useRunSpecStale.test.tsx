// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useRunSpecDiff, useRunSpecStale } from '../useRunSpecStale';

const { useQueryMock, specs } = vi.hoisted(() => ({
  useQueryMock: vi.fn(),
  specs: {
    run: undefined as string | undefined,
    saved: undefined as string | undefined,
  },
}));

vi.mock('@/hooks/api', () => ({ useQuery: useQueryMock }));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'node-1',
}));

function stale(runId: string | null = 'r1') {
  return renderHook(() =>
    useRunSpecStale({ dagName: 'dag', fileName: 'dag.yaml', runId })
  ).result.current;
}

beforeEach(() => {
  specs.run = undefined;
  specs.saved = undefined;
  useQueryMock.mockReset();
  useQueryMock.mockImplementation((path: string) => {
    if (path === '/dag-runs/{name}/{dagRunId}/spec') {
      return {
        data: specs.run === undefined ? undefined : { spec: specs.run },
      };
    }
    if (path === '/dags/{fileName}/spec') {
      return {
        data: specs.saved === undefined ? undefined : { spec: specs.saved },
      };
    }
    return { data: undefined };
  });
});

// parity:run-status-notes parity:test-ve-run
describe('useRunSpecStale', () => {
  it('is false when the specs are equal after normalisation', () => {
    specs.run = 'steps:\n  - run: a\n\n';
    specs.saved = 'steps:\r\n  - run: a';
    expect(stale()).toBe(false);
  });

  it('is true when the specs differ', () => {
    specs.run = 'steps:\n  - run: a\n';
    specs.saved = 'steps:\n  - run: b\n';
    expect(stale()).toBe(true);
  });

  it('is false until both specs are loaded', () => {
    specs.run = 'steps: []\n';
    expect(stale()).toBe(false);
  });

  it('asks for both specs of the run with remoteNode', () => {
    stale('r1');
    expect(useQueryMock).toHaveBeenCalledWith(
      '/dag-runs/{name}/{dagRunId}/spec',
      {
        params: {
          path: { name: 'dag', dagRunId: 'r1' },
          query: { remoteNode: 'node-1' },
        },
      }
    );
    expect(useQueryMock).toHaveBeenCalledWith('/dags/{fileName}/spec', {
      params: {
        path: { fileName: 'dag.yaml' },
        query: { remoteNode: 'node-1' },
      },
    });
  });

  it('makes no request when runId is null', () => {
    specs.run = 'a';
    specs.saved = 'b';
    expect(stale(null)).toBe(false);
    expect(useQueryMock).toHaveBeenCalled();
    expect(useQueryMock.mock.calls.every((call) => call[1] === null)).toBe(
      true
    );
  });
});

describe('useRunSpecDiff', () => {
  function diff(liveText?: string, runId: string | null = 'r1') {
    return renderHook(() =>
      useRunSpecDiff({
        dagName: 'dag',
        fileName: 'dag.yaml',
        runId,
        ...(liveText !== undefined ? { liveText } : {}),
      })
    ).result.current;
  }

  it('compares the run with the live text as parsed values', () => {
    specs.run = 'steps:\n  - name: a\n    run: x\n  - name: b\n    run: y\n';
    const result = diff(
      '# note\nsteps:\n  - name: a\n    run: x\n  - name: b\n    run: z\n'
    );
    expect(result?.headChanged).toBe(false);
    expect([...(result?.changed ?? [])]).toEqual(['b']);
  });

  it('uses the saved definition without a live text', () => {
    specs.run = 'steps:\n  - name: a\n    run: x\n';
    specs.saved = 'steps:\n  - name: a\n    run: y\n';
    expect([...(diff()?.changed ?? [])]).toEqual(['a']);
  });

  it('is null until the definitions are loaded and without a runId', () => {
    specs.saved = 'steps: []\n';
    expect(diff()).toBeNull();
    specs.run = 'steps: []\n';
    expect(diff(undefined, null)).toBeNull();
  });

  it('asks only for the spec of the run when it has a live text', () => {
    specs.run = 'steps: []\n';
    diff('steps: []\n');
    const live = useQueryMock.mock.calls.filter((call) => call[1] !== null);
    expect(live.map((call) => call[0])).toEqual([
      '/dag-runs/{name}/{dagRunId}/spec',
    ]);
  });
});
