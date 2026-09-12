// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import { useDAGSSE } from '@/hooks/useDAGSSE';
import DAGDetailsPanel from '../DAGDetailsPanel';

vi.mock('@/hooks/api', () => ({
  useQuery: vi.fn(),
}));

vi.mock('@/hooks/useDAGSSE', () => ({
  useDAGSSE: vi.fn(),
}));

vi.mock('@/hooks/useSSECacheSync', () => ({
  sseFallbackOptions: vi.fn(() => ({})),
  useSSECacheSync: vi.fn(),
}));

vi.mock('../DAGDetailsContent', () => ({
  default: ({
    dag,
    activeTab,
    dagRunId,
    editorHints,
    fillHeight,
  }: {
    dag: { name: string };
    activeTab: string;
    dagRunId?: string;
    editorHints?: { inheritedLegacyDefinitions?: unknown[] };
    fillHeight?: boolean;
  }) => (
    <div>
      <div
        data-fill-height={String(fillHeight)}
        data-testid="dag-details-content"
      >
        Previewing {dag.name} [{activeTab}] {dagRunId || 'latest'}
      </div>
      <div>
        Inherited hints: {editorHints?.inheritedLegacyDefinitions?.length ?? 0}
      </div>
    </div>
  ),
}));

const appBarValue = {
  title: 'DAGs',
  setTitle: vi.fn(),
  remoteNodes: ['local'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'local',
  selectRemoteNode: vi.fn(),
};

const liveState = {
  data: null,
  error: null,
  isConnected: false,
  isConnecting: false,
  shouldUseFallback: true,
};

const useQueryMock = useQuery as unknown as {
  mockImplementation: (fn: (path: string, init?: unknown) => unknown) => void;
};

function renderPanel(appBarOverride?: Partial<typeof appBarValue>) {
  return render(
    <MemoryRouter>
      <AppBarContext.Provider value={{ ...appBarValue, ...appBarOverride }}>
        <DAGDetailsPanel fileName="example" onClose={vi.fn()} />
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

afterEach(() => {
  vi.clearAllMocks();
});

describe('DAGDetailsPanel', () => {
  it('passes editor hints through to the dag list detail panel spec flow', () => {
    vi.mocked(useDAGSSE).mockReturnValue(liveState);
    useQueryMock.mockImplementation((path) => {
      if (path === '/dags/{fileName}') {
        return {
          data: {
            dag: { name: 'example-dag' },
            latestDAGRun: undefined,
            localDags: [],
            editorHints: {
              inheritedLegacyDefinitions: [{ name: 'greet' }],
            },
          },
          error: undefined,
          mutate: vi.fn(),
        } as never;
      }

      return {
        data: undefined,
        error: undefined,
        mutate: vi.fn(),
      } as never;
    });

    renderPanel();

    expect(
      screen.getByText('Previewing example-dag [status] latest')
    ).toBeInTheDocument();
    expect(screen.getByTestId('dag-details-content')).toHaveAttribute(
      'data-fill-height',
      'true'
    );
    expect(screen.getByText('Inherited hints: 1')).toBeInTheDocument();
  });

  it('uses the resolved remote node for DAG detail fetches and SSE', () => {
    const queryCalls: Array<{ path: string; init?: unknown }> = [];
    vi.mocked(useDAGSSE).mockReturnValue(liveState);
    useQueryMock.mockImplementation((path, init) => {
      queryCalls.push({ path, init });
      if (path === '/dags/{fileName}') {
        return {
          data: {
            dag: { name: 'example-dag' },
            latestDAGRun: undefined,
            localDags: [],
          },
          error: undefined,
          mutate: vi.fn(),
        } as never;
      }

      return {
        data: undefined,
        error: undefined,
        mutate: vi.fn(),
      } as never;
    });

    renderPanel({ selectedRemoteNode: 'edge-a' });

    expect(useDAGSSE).toHaveBeenCalledWith('example', true, 'edge-a');
    expect(
      queryCalls.find((call) => call.path === '/dags/{fileName}')?.init
    ).toMatchObject({
      params: {
        query: { remoteNode: 'edge-a' },
        path: { fileName: 'example' },
      },
    });
  });
});
