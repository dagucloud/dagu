// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
import * as React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { AuthProvider } from '@/contexts/AuthContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { SearchStateProvider } from '@/contexts/SearchStateContext';
import { useClient, useQuery } from '@/hooks/api';
import EventLogsPage from '../index';

vi.mock('@/hooks/api', () => ({
  useQuery: vi.fn(),
  useClient: vi.fn(),
}));

type QueryCall = {
  path: string;
  init: unknown;
};

const useQueryMock = useQuery as unknown as {
  mockImplementation: (fn: (path: string, init: unknown) => unknown) => void;
};

const useClientMock = useClient as unknown as {
  mockReturnValue: (value: unknown) => void;
};

function makeConfig(overrides: Partial<Config> = {}): Config {
  return {
    apiURL: '/api/v1',
    basePath: '/',
    title: 'Dagu',
    navbarColor: '',
    tz: 'UTC',
    tzOffsetInSec: 0,
    version: 'test',
    maxDashboardPageLimit: 100,
    remoteNodes: 'local,remote-a',
    initialWorkspaces: [],
    authMode: 'none',
    setupRequired: false,
    oidcEnabled: false,
    oidcButtonLabel: '',
    terminalEnabled: false,
    gitSyncEnabled: false,
    updateAvailable: false,
    latestVersion: '',
    permissions: {
      writeDags: true,
      runDags: true,
    },
    license: {
      valid: true,
      plan: 'community',
      expiry: '',
      features: [],
      gracePeriod: false,
      community: true,
      source: 'test',
      warningCode: '',
    },
    paths: {
      dagsDir: '',
      logDir: '',
      suspendFlagsDir: '',
      adminLogsDir: '',
      baseConfig: '',
      dagRunsDir: '',
      queueDir: '',
      procDir: '',
      serviceRegistryDir: '',
      configFileUsed: '',
      gitSyncDir: '',
      auditLogsDir: '',
    },
    ...overrides,
  };
}

function renderPage({
  initialEntry = '/event-logs',
  selectedRemoteNode = 'remote-a',
  configOverrides,
}: {
  initialEntry?: string;
  selectedRemoteNode?: string;
  configOverrides?: Partial<Config>;
} = {}) {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <ConfigContext.Provider value={makeConfig(configOverrides)}>
        <AuthProvider>
          <SearchStateProvider>
            <AppBarContext.Provider
              value={{
                title: '',
                setTitle: () => undefined,
                remoteNodes: ['local', 'remote-a'],
                setRemoteNodes: () => undefined,
                selectedRemoteNode,
                selectRemoteNode: () => undefined,
              }}
            >
              <EventLogsPage />
            </AppBarContext.Provider>
          </SearchStateProvider>
        </AuthProvider>
      </ConfigContext.Provider>
    </MemoryRouter>
  );
}

function mockQueryResult(overrides: Record<string, unknown> = {}) {
  return {
    data: {
      entries: [],
      nextCursor: undefined,
    },
    error: undefined,
    isLoading: false,
    mutate: vi.fn(),
    ...overrides,
  } as never;
}

function latestEventLogsCall(calls: QueryCall[]): QueryCall | undefined {
  return [...calls]
    .reverse()
    .find((call) => call.path === '/event-logs' && call.init !== null);
}

describe('EventLogsPage', () => {
  const clientGetMock = vi.fn();

  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    clientGetMock.mockReset();
    useClientMock.mockReturnValue({
      GET: clientGetMock,
    });
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('shows a loading state while the event feed request is pending', async () => {
    useQueryMock.mockImplementation(() =>
      mockQueryResult({
        data: undefined,
        isLoading: true,
      })
    );

    renderPage();

    expect(
      await screen.findByText('Loading event feed...')
    ).toBeInTheDocument();
  });

  it('queries event logs with a fixed dag_run kind and the selected remote node', async () => {
    const calls: QueryCall[] = [];
    useQueryMock.mockImplementation((path, init) => {
      calls.push({ path, init });
      return mockQueryResult();
    });

    renderPage();

    await waitFor(() => {
      const call = latestEventLogsCall(calls);
      expect(call).toBeDefined();
      expect(call?.init).toEqual(
        expect.objectContaining({
          params: {
            query: expect.objectContaining({
              remoteNode: 'remote-a',
              kind: 'dag_run',
              paginationMode: 'cursor',
              limit: 50,
            }),
          },
        })
      );
    });
  });

  it('restores URL filters and pagination into the event log query', async () => {
    const calls: QueryCall[] = [];
    useQueryMock.mockImplementation((path, init) => {
      calls.push({ path, init });
      return mockQueryResult();
    });

    renderPage({
      initialEntry: '/event-logs?dagName=payments&type=dag.run.failed',
    });

    await waitFor(() => {
      const call = latestEventLogsCall(calls);
      expect(call?.init).toEqual(
        expect.objectContaining({
          params: {
            query: expect.objectContaining({
              dagName: 'payments',
              type: 'dag.run.failed',
              paginationMode: 'cursor',
            }),
          },
        })
      );
    });
  });

  it('applies filter changes and opens the raw event dialog', async () => {
    const calls: QueryCall[] = [];
    useQueryMock.mockImplementation((path, init) => {
      calls.push({ path, init });
      return mockQueryResult({
        data: {
          entries: [
            {
              id: 'evt-1',
              schemaVersion: 1,
              occurredAt: '2026-04-01T00:00:00Z',
              recordedAt: '2026-04-01T00:00:01Z',
              kind: 'dag_run',
              type: 'dag.run.failed',
              sourceService: 'scheduler',
              dagName: 'demo',
              dagRunId: 'run-1',
              attemptId: 'attempt-1',
              status: 'failed',
              data: {
                reason: 'boom',
              },
            },
          ],
          nextCursor: undefined,
        },
      });
    });

    renderPage();

    const dagNameInputs =
      await screen.findAllByPlaceholderText('Filter by DAG name');
    const dagNameInput = dagNameInputs[dagNameInputs.length - 1];
    expect(dagNameInput).toBeDefined();
    if (!dagNameInput) {
      throw new Error('Expected DAG name input to be rendered');
    }
    fireEvent.change(dagNameInput, { target: { value: 'demo' } });
    fireEvent.click(screen.getByRole('button', { name: 'Apply Filters' }));

    await waitFor(() => {
      const call = latestEventLogsCall(calls);
      expect(call?.init).toEqual(
        expect.objectContaining({
          params: {
            query: expect.objectContaining({
              dagName: 'demo',
            }),
          },
        })
      );
    });

    fireEvent.click(screen.getByRole('button', { name: /View Raw/i }));

    expect(await screen.findByText('Raw Event')).toBeInTheDocument();
    expect(screen.getByText(/"id": "evt-1"/)).toBeInTheDocument();
    expect(screen.getByText(/"reason": "boom"/)).toBeInTheDocument();
  });

  it('persists only applied filters and ignores unsaved draft edits on remount', async () => {
    const calls: QueryCall[] = [];
    useQueryMock.mockImplementation((path, init) => {
      calls.push({ path, init });
      return mockQueryResult();
    });

    const firstRender = renderPage();
    const dagNameInput =
      await screen.findByPlaceholderText('Filter by DAG name');
    fireEvent.change(dagNameInput, { target: { value: 'draft-only' } });

    firstRender.unmount();
    calls.length = 0;

    renderPage();

    await waitFor(() => {
      const call = latestEventLogsCall(calls);
      expect(call?.init).toEqual(
        expect.objectContaining({
          params: {
            query: expect.not.objectContaining({
              dagName: 'draft-only',
            }),
          },
        })
      );
    });

    expect(
      await screen.findByPlaceholderText('Filter by DAG name')
    ).toHaveValue('');
  });

  it('restores applied filters on remount', async () => {
    const calls: QueryCall[] = [];
    useQueryMock.mockImplementation((path, init) => {
      calls.push({ path, init });
      return mockQueryResult();
    });

    const firstRender = renderPage();
    const dagNameInput =
      await screen.findByPlaceholderText('Filter by DAG name');
    fireEvent.change(dagNameInput, { target: { value: 'payments' } });
    fireEvent.click(screen.getByRole('button', { name: 'Apply Filters' }));

    await waitFor(() => {
      const call = latestEventLogsCall(calls);
      expect(call?.init).toEqual(
        expect.objectContaining({
          params: {
            query: expect.objectContaining({
              dagName: 'payments',
            }),
          },
        })
      );
    });

    firstRender.unmount();
    calls.length = 0;

    renderPage();

    await waitFor(() => {
      const call = latestEventLogsCall(calls);
      expect(call?.init).toEqual(
        expect.objectContaining({
          params: {
            query: expect.objectContaining({
              dagName: 'payments',
            }),
          },
        })
      );
    });

    expect(
      await screen.findByPlaceholderText('Filter by DAG name')
    ).toHaveValue('payments');
  });

  it('loads older events using the opaque cursor', async () => {
    const calls: QueryCall[] = [];
    clientGetMock.mockResolvedValue({
      data: {
        entries: [
          {
            id: 'evt-2',
            schemaVersion: 1,
            occurredAt: '2026-04-01T00:00:10Z',
            recordedAt: '2026-04-01T00:00:11Z',
            kind: 'dag_run',
            type: 'dag.run.succeeded',
            sourceService: 'scheduler',
            dagName: 'demo',
            dagRunId: 'run-2',
            attemptId: 'attempt-2',
          },
        ],
      },
      error: undefined,
    });
    useQueryMock.mockImplementation((path, init) => {
      calls.push({ path, init });
      return mockQueryResult({
        data: {
          entries: [
            {
              id: 'evt-1',
              schemaVersion: 1,
              occurredAt: '2026-04-01T00:01:00Z',
              recordedAt: '2026-04-01T00:01:01Z',
              kind: 'dag_run',
              type: 'dag.run.failed',
              sourceService: 'scheduler',
              dagName: 'demo',
              dagRunId: 'run-1',
              attemptId: 'attempt-1',
            },
          ],
          nextCursor: 'cursor-1',
        },
      });
    });

    renderPage();

    fireEvent.click(await screen.findByRole('button', { name: 'Load More' }));

    await waitFor(() => {
      expect(clientGetMock).toHaveBeenCalledWith('/event-logs', {
        params: {
          query: expect.objectContaining({
            remoteNode: 'remote-a',
            kind: 'dag_run',
            limit: 50,
            paginationMode: 'cursor',
            cursor: 'cursor-1',
          }),
        },
      });
    });

    expect(await screen.findByText('run-2')).toBeInTheDocument();
  });
});
