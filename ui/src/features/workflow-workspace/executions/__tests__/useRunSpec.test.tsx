// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useQuery } from '@/hooks/api';
import type { RunRef } from '../../data/types';
import { useRunSpec } from '../useRunSpec';

const h = vi.hoisted(() => ({
  GET: vi.fn(),
  runSpec: { spec: 'name: run\n' } as { spec: string } | null,
  savedSpec: 'name: saved\n',
}));

// The real swr-openapi hook over a fake client, so SWR keys and dedupe are real.
vi.mock('@/hooks/api', async () => {
  const { createQueryHook } = await import('swr-openapi');
  return {
    useQuery: createQueryHook({ GET: h.GET } as never, '/'),
    useClient: () => ({ GET: h.GET }),
  };
});
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'node-1',
}));

const REF: RunRef = { name: 'demo', runId: 'run-1' };

function wrapper({ children }: { children: ReactNode }) {
  return (
    <SWRConfig value={{ provider: () => new Map(), shouldRetryOnError: false }}>
      {children}
    </SWRConfig>
  );
}

function callsTo(path: string) {
  return h.GET.mock.calls.filter(([p]) => p === path);
}

beforeEach(() => {
  h.runSpec = { spec: 'name: run\n' };
  h.GET.mockReset();
  h.GET.mockImplementation(async (path: string) => {
    if (path === '/dag-runs/{name}/{dagRunId}/spec') {
      return h.runSpec
        ? { data: h.runSpec }
        : {
            error: { code: 'not_found', message: 'not found' },
            response: { status: 404 },
          };
    }
    if (path === '/dags/{fileName}/spec') {
      return { data: { spec: h.savedSpec, errors: [] } };
    }
    throw new Error(`unexpected GET ${path}`);
  });
});

// parity:api-run-spec
describe('useRunSpec', () => {
  it('reads the stored definition of the run with the remote node', async () => {
    const { result } = renderHook(() => useRunSpec(REF, 'demo.yaml'), {
      wrapper,
    });

    await waitFor(() => expect(result.current.text).toBe('name: run\n'));
    expect(result.current.missing).toBe(false);
    expect(callsTo('/dag-runs/{name}/{dagRunId}/spec')).toEqual([
      [
        '/dag-runs/{name}/{dagRunId}/spec',
        {
          params: {
            path: { name: 'demo', dagRunId: 'run-1' },
            query: { remoteNode: 'node-1' },
          },
        },
      ],
    ]);
    expect(callsTo('/dags/{fileName}/spec')).toEqual([]);
  });

  it('makes no request without a run', async () => {
    const { result } = renderHook(() => useRunSpec(null, 'demo.yaml'), {
      wrapper,
    });

    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(h.GET).not.toHaveBeenCalled();
    expect(result.current).toEqual({
      text: null,
      missing: false,
      isLoading: false,
      error: null,
    });
  });

  it('falls back to the saved definition on a 404', async () => {
    h.runSpec = null;
    const { result } = renderHook(() => useRunSpec(REF, 'demo.yaml'), {
      wrapper,
    });

    await waitFor(() => expect(result.current.text).toBe('name: saved\n'));
    expect(result.current.missing).toBe(true);
    expect(callsTo('/dags/{fileName}/spec')).toEqual([
      [
        '/dags/{fileName}/spec',
        {
          params: {
            query: { remoteNode: 'node-1' },
            path: { fileName: 'demo.yaml' },
          },
        },
      ],
    ]);
  });

  it('falls back to the saved definition on an empty snapshot', async () => {
    h.runSpec = { spec: '' };
    const { result } = renderHook(() => useRunSpec(REF, 'demo.yaml'), {
      wrapper,
    });

    await waitFor(() => expect(result.current.text).toBe('name: saved\n'));
    expect(result.current.missing).toBe(true);
  });

  it('reports a failed read that is not a 404, with no fallback', async () => {
    h.GET.mockImplementation(async () => ({
      error: { code: 'internal', message: 'disk full' },
      response: { status: 500 },
    }));
    const { result } = renderHook(() => useRunSpec(REF, 'demo.yaml'), {
      wrapper,
    });

    await waitFor(() => expect(result.current.error).toBe('disk full'));
    expect(result.current).toMatchObject({
      text: null,
      missing: false,
      isLoading: false,
    });
    expect(callsTo('/dags/{fileName}/spec')).toEqual([]);
  });

  it('shares one request for the saved definition with DAGSpec', async () => {
    h.runSpec = null;
    const { result } = renderHook(
      () => {
        // The query of DAGSpec (dag-editor/DAGSpec.tsx), with its own options.
        const editor = useQuery(
          '/dags/{fileName}/spec',
          {
            params: {
              query: { remoteNode: 'node-1' },
              path: { fileName: 'demo.yaml' },
            },
          },
          { refreshInterval: 0 }
        );
        return { editor, run: useRunSpec(REF, 'demo.yaml') };
      },
      { wrapper }
    );

    await waitFor(() => expect(result.current.run.text).toBe('name: saved\n'));
    expect(result.current.editor.data?.spec).toBe('name: saved\n');
    expect(callsTo('/dags/{fileName}/spec')).toHaveLength(1);
  });
});
