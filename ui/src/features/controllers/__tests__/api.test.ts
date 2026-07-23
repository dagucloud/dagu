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

function dagPage(currentPage: number, totalPages: number) {
  return {
    data: {
      dags: [],
      errors: [],
      pagination: {
        totalRecords: 0,
        currentPage,
        totalPages,
        nextPage: currentPage + 1,
        prevPage: Math.max(0, currentPage - 1),
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

  it('fails when paginated DAG responses stop advancing', async () => {
    fakes.get.mockResolvedValue(dagPage(1, 2));

    const { result } = renderHook(() => useControllerDAGOptions('ops'), {
      wrapper: TestSWRProvider,
    });

    await waitFor(() => {
      expect(result.current.error).toEqual(
        new Error('DAG pagination is inconsistent')
      );
    });
    expect(fakes.get).toHaveBeenCalledTimes(2);
    expect(
      fakes.get.mock.calls.map((call) => call[1].params.query.page)
    ).toEqual([1, 2]);
  });
});
