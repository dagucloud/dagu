// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import { useDAGRunSSE } from '@/hooks/useDAGRunSSE';
import { useDAGSSE } from '@/hooks/useDAGSSE';
import DAGDetailsSidePanel from '../DAGDetailsSidePanel';

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
    forceEnqueue,
    onEnqueue,
  }: {
    dag: { name: string };
    activeTab: string;
    dagRunId?: string;
    editorHints?: { inheritedLegacyDefinitions?: unknown[] };
    forceEnqueue?: boolean;
    onEnqueue?: (
      params: string,
      dagRunId?: string,
      immediate?: boolean
    ) => void | Promise<void>;
  }) => (
    <div>
      <div>
        Previewing {dag.name} [{activeTab}]{' '}
        {forceEnqueue ? 'forced' : 'default'} {dagRunId || 'latest'}
      </div>
      <div>
        Inherited hints: {editorHints?.inheritedLegacyDefinitions?.length ?? 0}
      </div>
      {onEnqueue ? (
        <button
          type="button"
          onClick={() => void onEnqueue('["x"]', 'manual-run')}
        >
          Enqueue Now
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

function renderPanel(
  props?: Partial<React.ComponentProps<typeof DAGDetailsSidePanel>>
) {
  return render(
    <MemoryRouter>
      <AppBarContext.Provider value={appBarValue}>
        <DAGDetailsSidePanel
          fileName="example"
          isOpen={true}
          onClose={vi.fn()}
          initialTab="status"
          {...props}
        />
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

afterEach(() => {
  vi.clearAllMocks();
});

describe('DAGDetailsSidePanel', () => {
  it('shows a loading state while DAG details are pending', () => {
    vi.mocked(useDAGSSE).mockReturnValue(liveState);
    vi.mocked(useDAGRunSSE).mockReturnValue(liveState);
    useQueryMock.mockImplementation((path) => {
      if (path === '/dags/{fileName}') {
        return {
          data: undefined,
          error: undefined,
          mutate: vi.fn(),
        } as never;
      }

      return {
        data: undefined,
      } as never;
    });

    renderPanel();

    expect(screen.getByText('Loading DAG details...')).toBeInTheDocument();
  });

  it('uses a null query key while closed so the detail request is truly disabled', () => {
    const queryCalls: Array<{ path: string; init?: unknown }> = [];
    vi.mocked(useDAGSSE).mockReturnValue(liveState);
    vi.mocked(useDAGRunSSE).mockReturnValue(liveState);
    useQueryMock.mockImplementation((path, init) => {
      queryCalls.push({ path, init });
      return {
        data: undefined,
        error: undefined,
        mutate: vi.fn(),
      } as never;
    });

    renderPanel({ isOpen: false });

    expect(
      queryCalls.find((call) => call.path === '/dags/{fileName}')?.init
    ).toBeNull();
  });

  it('shows a not-found state with a close action for 404s', () => {
    const onClose = vi.fn();
    vi.mocked(useDAGSSE).mockReturnValue(liveState);
    vi.mocked(useDAGRunSSE).mockReturnValue(liveState);
    useQueryMock.mockImplementation((path) => {
      if (path === '/dags/{fileName}') {
        return {
          data: undefined,
          error: { status: 404, message: 'not found' },
          mutate: vi.fn(),
        } as never;
      }

      return {
        data: undefined,
      } as never;
    });

    renderPanel({ onClose });

    expect(
      screen.getByText('DAG not found or has been deleted.')
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalled();
  });

  it('shows an error state with retry when the load fails', () => {
    const mutate = vi.fn();
    vi.mocked(useDAGSSE).mockReturnValue(liveState);
    vi.mocked(useDAGRunSSE).mockReturnValue(liveState);
    useQueryMock.mockImplementation((path) => {
      if (path === '/dags/{fileName}') {
        return {
          data: undefined,
          error: { message: 'backend unavailable' },
          mutate,
        } as never;
      }

      return {
        data: undefined,
      } as never;
    });

    renderPanel();

    expect(screen.getByText('backend unavailable')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(mutate).toHaveBeenCalled();
  });

  it('renders DAG details content when data is available', () => {
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
          },
          error: undefined,
          mutate: vi.fn(),
        } as never;
      }

      return {
        data: undefined,
      } as never;
    });

    renderPanel({ forceEnqueue: true, initialTab: 'history' });

    expect(
      screen.getByText('Previewing example-dag [history] forced latest')
    ).toBeInTheDocument();
  });

  it('passes editor hints through to the modal DAG spec flow', () => {
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
      } as never;
    });

    renderPanel();

    expect(screen.getByText('Inherited hints: 1')).toBeInTheDocument();
  });

  it('tracks the returned dag run, switches to status, and revalidates after enqueue', async () => {
    const mutate = vi.fn().mockResolvedValue(undefined);
    const onEnqueue = vi.fn().mockResolvedValue('queued-run');

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
          },
          error: undefined,
          mutate,
        } as never;
      }

      if (path === '/dag-runs/{name}/{dagRunId}') {
        return {
          data: undefined,
          error: undefined,
          mutate: vi.fn(),
        } as never;
      }

      return {
        data: undefined,
      } as never;
    });

    renderPanel({ initialTab: 'history', forceEnqueue: true, onEnqueue });

    fireEvent.click(screen.getByRole('button', { name: 'Enqueue Now' }));

    expect(
      await screen.findByText(
        'Previewing example-dag [status] forced queued-run'
      )
    ).toBeInTheDocument();
    expect(onEnqueue).toHaveBeenCalledWith('["x"]', 'manual-run', undefined);
    expect(mutate).toHaveBeenCalled();
  });
});
