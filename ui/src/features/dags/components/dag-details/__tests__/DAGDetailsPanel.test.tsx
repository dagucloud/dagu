// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { PageContextProvider } from '@/contexts/PageContext';
import { useQuery } from '@/hooks/api';
import { useDAGRunSSE } from '@/hooks/useDAGRunSSE';
import { useDAGSSE } from '@/hooks/useDAGSSE';
import DAGDetailsPanel from '../DAGDetailsPanel';

vi.mock('@/hooks/api', () => ({
  useQuery: vi.fn(),
}));

vi.mock('@/hooks/useDAGSSE', () => ({
  useDAGSSE: vi.fn(),
}));

vi.mock('@/hooks/useDAGRunSSE', () => ({
  useDAGRunSSE: vi.fn(),
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
    onRunStarted,
  }: {
    dag: { name: string };
    activeTab: string;
    dagRunId?: string;
    editorHints?: { inheritedLegacyDefinitions?: unknown[] };
    onRunStarted?: (dagRunId: string) => void;
  }) => (
    <div>
      <div>
        Previewing {dag.name} [{activeTab}] {dagRunId || 'latest'}
      </div>
      <div>
        Inherited hints: {editorHints?.inheritedLegacyDefinitions?.length ?? 0}
      </div>
      {onRunStarted ? (
        <button type="button" onClick={() => onRunStarted('started-run')}>
          Mark Started
        </button>
      ) : null}
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

function renderPanel() {
  return render(
    <MemoryRouter>
      <PageContextProvider>
        <AppBarContext.Provider value={appBarValue}>
          <DAGDetailsPanel fileName="example" onClose={vi.fn()} />
        </AppBarContext.Provider>
      </PageContextProvider>
    </MemoryRouter>
  );
}

afterEach(() => {
  vi.clearAllMocks();
});

describe('DAGDetailsPanel', () => {
  it('passes editor hints through to the dag list detail panel spec flow', () => {
    vi.mocked(useDAGSSE).mockReturnValue(liveState);
    vi.mocked(useDAGRunSSE).mockReturnValue(liveState);
    useQueryMock.mockImplementation((path) => {
      if (path === '/dags/{fileName}') {
        return {
          data: {
            dag: { name: 'example-dag' },
            filePath: '/tmp/example.yaml',
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
    expect(screen.getByText('Inherited hints: 1')).toBeInTheDocument();
  });

  it('tracks a just-started DAG-run and reads its exact live status', async () => {
    const dagData = {
      dag: { name: 'example-dag' },
      filePath: '/tmp/example.yaml',
      latestDAGRun: undefined,
      localDags: [],
    };
    const trackedRunData = {
      dagRunDetails: {
        name: 'example-dag',
        dagRunId: 'started-run',
      },
    };

    vi.mocked(useDAGSSE).mockReturnValue(liveState);
    vi.mocked(useDAGRunSSE).mockReturnValue(liveState);
    useQueryMock.mockImplementation((path) => {
      if (path === '/dags/{fileName}') {
        return {
          data: dagData,
          error: undefined,
          mutate: vi.fn(),
        } as never;
      }

      if (path === '/dag-runs/{name}/{dagRunId}') {
        return {
          data: trackedRunData,
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

    fireEvent.click(screen.getByRole('button', { name: 'Mark Started' }));

    expect(
      await screen.findByText('Previewing example-dag [status] started-run')
    ).toBeInTheDocument();
    expect(useDAGRunSSE).toHaveBeenCalledWith(
      'example-dag',
      'started-run',
      true,
      'local'
    );
  });
});
