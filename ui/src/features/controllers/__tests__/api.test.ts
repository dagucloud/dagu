// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as React from 'react';
import { renderHook, waitFor } from '@testing-library/react';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useControllerDAGOptions } from '../api';
import { controllerDAGOption } from '../dagOptions';

const fakes = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ GET: fakes.get }),
  useQuery: vi.fn(),
}));

function TestSWRProvider({ children }: React.PropsWithChildren) {
  return React.createElement(
    SWRConfig,
    {
      value: {
        dedupingInterval: 0,
        provider: () => new Map(),
        shouldRetryOnError: false,
      },
    },
    children
  );
}

function dagPage(
  dags: Array<{
    fileName: string;
    dag: { name: string; description?: string; params?: string[] };
  }>
) {
  return {
    data: {
      dags,
      errors: [],
      pagination: {
        totalRecords: dags.length,
        currentPage: 1,
        totalPages: 8,
        nextPage: 2,
        prevPage: 0,
      },
    },
    response: new Response(),
  };
}

beforeEach(() => {
  fakes.get.mockReset();
});

describe('Controller DAG options', () => {
  it('keeps DAGs with stable identity and named parameters', () => {
    expect(
      controllerDAGOption({
        fileName: 'classify',
        dag: {
          name: 'classify',
          description: 'Classify an alert.',
          params: ['severity=warning'],
        },
      })
    ).toEqual({
      fileName: 'classify',
      description: 'Classify an alert.',
    });
  });

  it('omits DAGs with unstable identity or positional parameters', () => {
    expect(
      controllerDAGOption({
        fileName: 'classify',
        dag: { name: 'renamed', params: ['severity=warning'] },
      })
    ).toBeNull();
    expect(
      controllerDAGOption({
        fileName: 'classify',
        dag: { name: 'classify', params: ['1=warning'] },
      })
    ).toBeNull();
  });

  it('searches a bounded DAG page only after a query is entered', async () => {
    fakes.get.mockResolvedValue(
      dagPage([
        {
          fileName: 'classify',
          dag: {
            name: 'classify',
            description: 'Classify an alert.',
            params: ['severity=warning'],
          },
        },
      ])
    );

    const { result, rerender } = renderHook(
      ({ search }) => useControllerDAGOptions('ops', search),
      {
        initialProps: { search: '' },
        wrapper: TestSWRProvider,
      }
    );

    expect(result.current.data).toEqual([]);
    expect(fakes.get).not.toHaveBeenCalled();

    rerender({ search: 'class' });

    await waitFor(() => {
      expect(result.current.data).toEqual([
        {
          fileName: 'classify',
          description: 'Classify an alert.',
        },
      ]);
    });
    expect(fakes.get).toHaveBeenCalledOnce();
    expect(fakes.get).toHaveBeenCalledWith(
      '/dags',
      expect.objectContaining({
        params: {
          query: expect.objectContaining({
            workspace: 'ops',
            remoteNode: 'local',
            name: 'class',
            page: 1,
            perPage: 20,
          }),
        },
      })
    );
  });
});
