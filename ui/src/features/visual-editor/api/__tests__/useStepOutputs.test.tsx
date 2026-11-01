// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStepOutputs } from '../useStepOutputs';

const { get } = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock('@/hooks/api', async () => {
  const { default: useSWR } = await import('swr');
  return {
    // As in swr-openapi, null params skip the request.
    useQuery: (path: string, params: unknown) =>
      useSWR(params === null ? null : [path, params], async () => {
        const result = await get(path, params);
        return result.data;
      }),
  };
});
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'node-1',
}));

const wrapper = ({ children }: { children: ReactNode }) => (
  <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
    {children}
  </SWRConfig>
);

describe('useStepOutputs', () => {
  beforeEach(() => {
    get.mockReset();
    get.mockResolvedValue({ data: { outputs: {} } });
  });

  it('makes no request without a run or step', async () => {
    renderHook(() => useStepOutputs('dag', null, 'a'), { wrapper });
    renderHook(() => useStepOutputs('dag', 'r1', null), { wrapper });
    renderHook(() => useStepOutputs('dag', '', 'a'), { wrapper });
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(get).not.toHaveBeenCalled();
  });

  it('requests the outputs path with remoteNode', async () => {
    const { result } = renderHook(() => useStepOutputs('dag', 'r1', 'a'), {
      wrapper,
    });
    await waitFor(() => expect(result.current.data).toBeDefined());
    expect(get).toHaveBeenCalledWith(
      '/dag-runs/{name}/{dagRunId}/steps/{stepName}/outputs',
      {
        params: {
          path: { name: 'dag', dagRunId: 'r1', stepName: 'a' },
          query: { remoteNode: 'node-1' },
        },
      }
    );
  });
});
