import type { ReactNode } from 'react';
import { renderHook, act } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { AppBarContext } from '@/contexts/AppBarContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import {
  AgentChatProvider,
  useAgentChatContext,
} from '../../context/AgentChatContext';
import { useAgentChat } from '../useAgentChat';

// Combined hook so tests can call openChat() to simulate the modal being visible.
// Polling only runs when isChatOpen is true.
function useAgentChatWithOpen() {
  const ctx = useAgentChatContext();
  const chat = useAgentChat();
  return { ...chat, openChat: ctx.openChat };
}

function useAgentChatAlwaysActive(): ReturnType<typeof useAgentChat> {
  return useAgentChat({ active: true });
}

const getMock = vi.fn();
const postMock = vi.fn();
const navigateMock = vi.fn();
const useSSEConnectionMock = vi.fn();
let mockedSSEStatus = {
  isSessionLive: false,
};

vi.mock('@/hooks/api', () => ({
  useClient: () => ({
    GET: getMock,
    POST: postMock,
  }),
}));

vi.mock('../useSSEConnection', () => ({
  useSSEConnection: (...args: unknown[]) => useSSEConnectionMock(...args),
}));

vi.mock('react-router-dom', () => ({
  useNavigate: () => navigateMock,
}));

const testConfig: Config = {
  apiURL: '/api/v1',
  basePath: '/',
  title: 'Dagu',
  navbarColor: '#000000',
  tz: 'UTC',
  tzOffsetInSec: 0,
  version: 'test',
  maxDashboardPageLimit: 100,
  remoteNodes: '',
  initialWorkspaces: [],
  authMode: 'none',
  setupRequired: false,
  oidcEnabled: false,
  oidcButtonLabel: '',
  terminalEnabled: true,
  gitSyncEnabled: false,
  agentEnabled: true,
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
};

function makeApiMessage(id: string, content: string, sequenceId = 1) {
  return {
    id,
    sessionId: 'sess-1',
    type: 'assistant',
    sequenceId,
    content,
    createdAt: '2026-03-13T00:00:00Z',
  };
}

function makeApiUIActionMessage(
  id: string,
  path: string,
  sequenceId = 1,
  sessionId = 'sess-1'
) {
  return {
    id,
    sessionId,
    type: 'ui_action',
    sequenceId,
    uiAction: {
      type: 'navigate',
      path,
    },
    createdAt: '2026-03-13T00:00:00Z',
  };
}

type ApiTestMessage =
  | ReturnType<typeof makeApiMessage>
  | ReturnType<typeof makeApiUIActionMessage>;

function makeSessionDetailResponse(options?: {
  id?: string;
  delegateTask?: string;
  messages?: ApiTestMessage[];
  working?: boolean;
  delegates?: Array<{
    id: string;
    task: string;
    status: 'running' | 'completed';
  }>;
}) {
  const id = options?.id ?? 'sess-1';
  return {
    session: {
      id,
      title: `Session ${id}`,
      createdAt: '2026-03-13T00:00:00Z',
      updatedAt: '2026-03-13T00:00:00Z',
      delegateTask: options?.delegateTask,
    },
    sessionState: {
      sessionId: id,
      working: options?.working ?? false,
      hasPendingPrompt: false,
      model: 'gpt-test',
      totalCost: 0,
    },
    messages: options?.messages ?? [],
    delegates: options?.delegates ?? [],
  };
}

function TestProviders({ children }: { children: ReactNode }) {
  return (
    <ConfigContext.Provider value={testConfig}>
      <AppBarContext.Provider
        value={{
          title: '',
          setTitle: () => undefined,
          remoteNodes: ['local'],
          setRemoteNodes: () => undefined,
          selectedRemoteNode: 'local',
          selectRemoteNode: () => undefined,
        }}
      >
        <UserPreferencesProvider>
          <AgentChatProvider>{children}</AgentChatProvider>
        </UserPreferencesProvider>
      </AppBarContext.Provider>
    </ConfigContext.Provider>
  );
}

describe('useAgentChat fallback polling', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    localStorage.clear();
    getMock.mockReset();
    postMock.mockReset();
    navigateMock.mockReset();
    mockedSSEStatus = {
      isSessionLive: false,
    };
    useSSEConnectionMock.mockReset();
    useSSEConnectionMock.mockImplementation(() => mockedSSEStatus);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('polls the selected session while multiplex SSE is offline and stops after reconnect', async () => {
    const sessionResponses = [
      makeSessionDetailResponse({
        messages: [makeApiMessage('msg-1', 'initial snapshot')],
        working: false,
      }),
      makeSessionDetailResponse({
        messages: [makeApiMessage('msg-2', 'polled snapshot')],
        working: true,
      }),
    ];
    let sessionFetchCount = 0;

    getMock.mockImplementation(
      async (
        _path: string,
        request?: { params?: { path?: { sessionId?: string } } }
      ) => {
        if (request?.params?.path?.sessionId === 'sess-1') {
          const idx = Math.min(sessionFetchCount, sessionResponses.length - 1);
          sessionFetchCount += 1;
          return { data: sessionResponses[idx] };
        }
        throw new Error('unexpected request');
      }
    );

    const { result, rerender } = renderHook(() => useAgentChatWithOpen(), {
      wrapper: TestProviders,
    });

    act(() => {
      result.current.openChat();
    });
    await act(async () => {
      await result.current.selectSession('sess-1');
    });

    expect(result.current.messages).toHaveLength(1);
    expect(result.current.messages[0]?.content).toBe('initial snapshot');
    expect(result.current.sessionState?.working).toBe(false);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });

    expect(sessionFetchCount).toBe(2);
    expect(result.current.messages[0]?.content).toBe('polled snapshot');
    expect(result.current.sessionState?.working).toBe(true);

    mockedSSEStatus = {
      isSessionLive: true,
    };
    rerender();
    const callsAfterReconnect = sessionFetchCount;

    await act(async () => {
      await vi.advanceTimersByTimeAsync(4000);
    });

    expect(sessionFetchCount).toBe(callsAfterReconnect);
  });

  it('polls while explicitly active without opening the floating modal', async () => {
    let sessionFetchCount = 0;
    getMock.mockImplementation(
      async (
        _path: string,
        request?: { params?: { path?: { sessionId?: string } } }
      ) => {
        if (request?.params?.path?.sessionId === 'sess-1') {
          sessionFetchCount += 1;
          return {
            data: makeSessionDetailResponse({
              messages: [
                makeApiMessage(
                  `msg-${sessionFetchCount}`,
                  sessionFetchCount === 1 ? 'initial' : 'embedded poll'
                ),
              ],
              working: sessionFetchCount > 1,
            }),
          };
        }
        throw new Error('unexpected request');
      }
    );

    const { result } = renderHook(() => useAgentChatAlwaysActive(), {
      wrapper: TestProviders,
    });

    await act(async () => {
      await result.current.selectSession('sess-1');
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });

    expect(sessionFetchCount).toBe(2);
    expect(result.current.messages[0]?.content).toBe('embedded poll');
    expect(result.current.sessionState?.working).toBe(true);
  });

  it('does not replay historical navigate actions when opening an existing session', async () => {
    getMock.mockImplementation(
      async (
        _path: string,
        request?: { params?: { path?: { sessionId?: string } } }
      ) => {
        if (request?.params?.path?.sessionId === 'sess-1') {
          return {
            data: makeSessionDetailResponse({
              messages: [makeApiUIActionMessage('ui-1', '/dags/existing-dag')],
            }),
          };
        }
        throw new Error('unexpected request');
      }
    );

    const { result } = renderHook(() => useAgentChatWithOpen(), {
      wrapper: TestProviders,
    });

    act(() => {
      result.current.openChat();
    });
    await act(async () => {
      await result.current.selectSession('sess-1');
    });

    expect(navigateMock).not.toHaveBeenCalled();
  });

  it('navigates when a ui_action arrives via fallback polling', async () => {
    const sessionResponses = [
      makeSessionDetailResponse({
        messages: [makeApiMessage('msg-1', 'initial snapshot', 1)],
      }),
      makeSessionDetailResponse({
        messages: [
          makeApiMessage('msg-1', 'initial snapshot', 1),
          makeApiUIActionMessage(
            'ui-2',
            '/dags/github_webhook_codex_analysis',
            2
          ),
        ],
      }),
    ];
    let sessionFetchCount = 0;

    getMock.mockImplementation(
      async (
        _path: string,
        request?: { params?: { path?: { sessionId?: string } } }
      ) => {
        if (request?.params?.path?.sessionId === 'sess-1') {
          const idx = Math.min(sessionFetchCount, sessionResponses.length - 1);
          sessionFetchCount += 1;
          return { data: sessionResponses[idx] };
        }
        throw new Error('unexpected request');
      }
    );

    const { result } = renderHook(() => useAgentChatWithOpen(), {
      wrapper: TestProviders,
    });

    act(() => {
      result.current.openChat();
    });
    await act(async () => {
      await result.current.selectSession('sess-1');
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });

    expect(navigateMock).toHaveBeenCalledWith(
      '/dags/github_webhook_codex_analysis'
    );
  });

  it('navigates ui_actions from the first poll after creating a session', async () => {
    getMock.mockImplementation(
      async (
        path: string,
        request?: { params?: { path?: { sessionId?: string } } }
      ) => {
        if (path === '/agent/sessions') {
          return {
            data: {
              sessions: [
                {
                  session: {
                    id: 'sess-new',
                    title: 'Session sess-new',
                    createdAt: '2026-03-13T00:00:00Z',
                    updatedAt: '2026-03-13T00:00:00Z',
                  },
                  working: true,
                  hasPendingPrompt: false,
                  model: 'gpt-test',
                  totalCost: 0,
                },
              ],
              pagination: {
                currentPage: 1,
                totalPages: 1,
              },
            },
          };
        }
        if (request?.params?.path?.sessionId === 'sess-new') {
          return {
            data: makeSessionDetailResponse({
              id: 'sess-new',
              messages: [
                makeApiUIActionMessage(
                  'ui-new',
                  '/dags/github_webhook_codex_analysis',
                  1,
                  'sess-new'
                ),
              ],
              working: false,
            }),
          };
        }
        throw new Error('unexpected request');
      }
    );
    postMock.mockImplementation(async (path: string) => {
      if (path === '/agent/sessions') {
        return { data: { sessionId: 'sess-new' } };
      }
      throw new Error('unexpected request');
    });

    const { result } = renderHook(() => useAgentChatAlwaysActive(), {
      wrapper: TestProviders,
    });

    await act(async () => {
      await result.current.sendMessage('open the DAG page');
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });

    expect(navigateMock).toHaveBeenCalledWith(
      '/dags/github_webhook_codex_analysis'
    );
  });

  it('loads additional sessions with the cursor returned by the previous page', async () => {
    getMock.mockImplementation(async (path: string, request?: unknown) => {
      if (path !== '/agent/sessions') {
        throw new Error('unexpected request');
      }
      const params = request as {
        params?: { query?: { cursor?: string; perPage?: number } };
      };
      if (!params.params?.query?.cursor) {
        return {
          data: {
            sessions: [
              {
                session: {
                  id: 'sess-1',
                  title: 'first session',
                  createdAt: '2026-05-08T10:00:00Z',
                  updatedAt: '2026-05-08T10:00:00Z',
                },
                working: false,
                hasPendingPrompt: false,
                model: 'gpt-test',
                totalCost: 0,
              },
            ],
            nextCursor: 'cursor-1',
            pagination: {
              currentPage: 1,
              totalPages: 2,
            },
          },
        };
      }
      if (params.params.query.cursor === 'cursor-1') {
        return {
          data: {
            sessions: [
              {
                session: {
                  id: 'sess-2',
                  title: 'second session',
                  createdAt: '2026-05-08T09:00:00Z',
                  updatedAt: '2026-05-08T09:00:00Z',
                },
                working: false,
                hasPendingPrompt: false,
                model: 'gpt-test',
                totalCost: 0,
              },
            ],
            pagination: {
              currentPage: 1,
              totalPages: 1,
            },
          },
        };
      }
      throw new Error('unexpected cursor');
    });

    const { result } = renderHook(() => useAgentChatAlwaysActive(), {
      wrapper: TestProviders,
    });

    await act(async () => {
      await result.current.fetchSessions();
    });
    await act(async () => {
      await result.current.loadMoreSessions();
    });

    expect(getMock).toHaveBeenNthCalledWith(
      1,
      '/agent/sessions',
      expect.objectContaining({
        params: {
          query: {
            remoteNode: 'local',
            paginationMode: 'cursor',
            perPage: 30,
          },
        },
      })
    );
    expect(getMock).toHaveBeenNthCalledWith(
      2,
      '/agent/sessions',
      expect.objectContaining({
        params: {
          query: {
            remoteNode: 'local',
            paginationMode: 'cursor',
            perPage: 30,
            cursor: 'cursor-1',
          },
        },
      })
    );
    expect(result.current.sessions.map((session) => session.session.id)).toEqual(
      ['sess-1', 'sess-2']
    );
    expect(result.current.hasMoreSessions).toBe(false);
  });

  it('ignores concurrent load more calls while a cursor page is already loading', async () => {
    let resolveLoadMore:
      | ((value: {
          data: {
            sessions: unknown[];
            pagination: { currentPage: number; totalPages: number };
          };
        }) => void)
      | undefined;

    getMock.mockImplementation(async (path: string, request?: unknown) => {
      if (path !== '/agent/sessions') {
        throw new Error('unexpected request');
      }
      const params = request as {
        params?: { query?: { cursor?: string } };
      };
      if (!params.params?.query?.cursor) {
        return {
          data: {
            sessions: [
              {
                session: {
                  id: 'sess-1',
                  title: 'first session',
                  createdAt: '2026-05-08T10:00:00Z',
                  updatedAt: '2026-05-08T10:00:00Z',
                },
                working: false,
                hasPendingPrompt: false,
                model: 'gpt-test',
                totalCost: 0,
              },
            ],
            nextCursor: 'cursor-1',
            pagination: {
              currentPage: 1,
              totalPages: 2,
            },
          },
        };
      }
      return new Promise((resolve) => {
        resolveLoadMore = resolve;
      });
    });

    const { result } = renderHook(() => useAgentChatAlwaysActive(), {
      wrapper: TestProviders,
    });

    await act(async () => {
      await result.current.fetchSessions();
    });

    let firstLoad: Promise<void> | undefined;
    act(() => {
      firstLoad = result.current.loadMoreSessions();
      void result.current.loadMoreSessions();
    });

    expect(getMock).toHaveBeenCalledTimes(2);
    expect(result.current.isLoadingMore).toBe(true);

    await act(async () => {
      resolveLoadMore?.({
        data: {
          sessions: [],
          pagination: {
            currentPage: 1,
            totalPages: 1,
          },
        },
      });
      await firstLoad;
    });

    expect(result.current.isLoadingMore).toBe(false);
  });

  it('clears the session list running marker when the selected session snapshot is idle', async () => {
    getMock.mockImplementation(
      async (
        path: string,
        request?: { params?: { path?: { sessionId?: string } } }
      ) => {
        if (path === '/agent/sessions') {
          return {
            data: {
              sessions: [
                {
                  session: {
                    id: 'sess-1',
                    title: 'tracked session',
                    createdAt: '2026-05-08T10:00:00Z',
                    updatedAt: '2026-05-08T10:00:00Z',
                  },
                  working: true,
                  hasPendingPrompt: false,
                  model: 'gpt-test',
                  totalCost: 0,
                },
              ],
              pagination: {
                currentPage: 1,
                totalPages: 1,
              },
            },
          };
        }
        if (request?.params?.path?.sessionId === 'sess-1') {
          return {
            data: makeSessionDetailResponse({
              id: 'sess-1',
              working: false,
            }),
          };
        }
        throw new Error('unexpected request');
      }
    );

    const { result } = renderHook(() => useAgentChatAlwaysActive(), {
      wrapper: TestProviders,
    });

    await act(async () => {
      await result.current.fetchSessions();
    });
    expect(result.current.sessions[0]?.working).toBe(true);

    await act(async () => {
      await result.current.selectSession('sess-1');
    });

    expect(result.current.sessions[0]?.working).toBe(false);
  });

  it('navigates when a delegate ui_action arrives via fallback polling', async () => {
    let delegateFetchCount = 0;
    getMock.mockImplementation(
      async (
        _path: string,
        request?: { params?: { path?: { sessionId?: string } } }
      ) => {
        const sessionId = request?.params?.path?.sessionId;
        if (sessionId === 'sess-1') {
          return {
            data: makeSessionDetailResponse({
              messages: [makeApiMessage('msg-root', 'root session')],
              delegates: [
                { id: 'delegate-1', task: 'Delegate task', status: 'running' },
              ],
            }),
          };
        }
        if (sessionId === 'delegate-1') {
          delegateFetchCount += 1;
          return {
            data: makeSessionDetailResponse({
              id: 'delegate-1',
              delegateTask: 'Delegate task',
              messages:
                delegateFetchCount === 1
                  ? [makeApiMessage('delegate-msg-1', 'delegate initial', 1)]
                  : [
                      makeApiMessage('delegate-msg-1', 'delegate initial', 1),
                      makeApiUIActionMessage(
                        'delegate-ui-1',
                        '/dags/delegate-target',
                        2,
                        'delegate-1'
                      ),
                    ],
              working: delegateFetchCount === 1,
            }),
          };
        }
        throw new Error('unexpected request');
      }
    );

    const { result } = renderHook(() => useAgentChatWithOpen(), {
      wrapper: TestProviders,
    });

    act(() => {
      result.current.openChat();
    });
    await act(async () => {
      await result.current.selectSession('sess-1');
    });
    await act(async () => {
      await result.current.reopenDelegate('delegate-1', 'Delegate task');
    });

    expect(navigateMock).not.toHaveBeenCalled();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });

    expect(navigateMock).toHaveBeenCalledWith('/dags/delegate-target');
  });

  it('polls open delegate panes while the root agent stream is offline and stops after reconnect', async () => {
    mockedSSEStatus = {
      isSessionLive: false,
    };
    useSSEConnectionMock.mockImplementation(() => mockedSSEStatus);

    let delegateFetchCount = 0;
    getMock.mockImplementation(
      async (
        _path: string,
        request?: { params?: { path?: { sessionId?: string } } }
      ) => {
        const sessionId = request?.params?.path?.sessionId;
        if (sessionId === 'sess-1') {
          return {
            data: makeSessionDetailResponse({
              messages: [makeApiMessage('msg-root', 'root session')],
              delegates: [
                { id: 'delegate-1', task: 'Delegate task', status: 'running' },
              ],
            }),
          };
        }
        if (sessionId === 'delegate-1') {
          delegateFetchCount += 1;
          return {
            data: makeSessionDetailResponse({
              id: 'delegate-1',
              delegateTask: 'Delegate task',
              messages: [
                makeApiMessage(
                  `delegate-${delegateFetchCount}`,
                  delegateFetchCount === 1
                    ? 'delegate initial'
                    : 'delegate polled'
                ),
              ],
              working: delegateFetchCount === 1,
            }),
          };
        }
        throw new Error('unexpected request');
      }
    );

    const { result, rerender } = renderHook(() => useAgentChatWithOpen(), {
      wrapper: TestProviders,
    });

    act(() => {
      result.current.openChat();
    });
    await act(async () => {
      await result.current.selectSession('sess-1');
    });

    await act(async () => {
      await result.current.reopenDelegate('delegate-1', 'Delegate task');
    });

    expect(result.current.delegateMessages['delegate-1']?.[0]?.content).toBe(
      'delegate initial'
    );

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });

    expect(delegateFetchCount).toBe(2);
    expect(result.current.delegateMessages['delegate-1']?.[0]?.content).toBe(
      'delegate polled'
    );

    mockedSSEStatus = {
      isSessionLive: true,
    };
    rerender();
    const callsAfterReconnect = delegateFetchCount;

    await act(async () => {
      await vi.advanceTimersByTimeAsync(4000);
    });

    expect(delegateFetchCount).toBe(callsAfterReconnect);
  });

  it('merges incremental stream updates from the dedicated agent stream', async () => {
    getMock.mockImplementation(
      async (
        _path: string,
        request?: { params?: { path?: { sessionId?: string } } }
      ) => {
        if (request?.params?.path?.sessionId === 'sess-1') {
          return {
            data: makeSessionDetailResponse({
              messages: [makeApiMessage('msg-1', 'initial snapshot', 1)],
            }),
          };
        }
        throw new Error('unexpected request');
      }
    );

    const { result } = renderHook(() => useAgentChatWithOpen(), {
      wrapper: TestProviders,
    });

    act(() => {
      result.current.openChat();
    });
    await act(async () => {
      await result.current.selectSession('sess-1');
    });

    const latestCall =
      useSSEConnectionMock.mock.calls[
        useSSEConnectionMock.mock.calls.length - 1
      ];
    const callbacks = latestCall?.[3] as
      | {
          onEvent: (event: unknown, replace: boolean) => void;
        }
      | undefined;
    expect(callbacks).toBeDefined();
    if (!callbacks) {
      throw new Error('expected agent stream callbacks');
    }

    act(() => {
      callbacks.onEvent(
        {
          messages: [
            {
              id: 'msg-3',
              session_id: 'sess-1',
              type: 'assistant',
              sequence_id: 3,
              content: 'streamed reply',
              created_at: '2026-03-13T00:00:01Z',
            },
          ],
        },
        false
      );
      callbacks.onEvent(
        {
          messages: [
            {
              id: 'msg-3',
              session_id: 'sess-1',
              type: 'assistant',
              sequence_id: 3,
              content: 'streamed reply updated',
              created_at: '2026-03-13T00:00:01Z',
            },
          ],
        },
        false
      );
      callbacks.onEvent(
        {
          messages: [
            {
              id: 'msg-2',
              session_id: 'sess-1',
              type: 'assistant',
              sequence_id: 2,
              content: 'out-of-order reply',
              created_at: '2026-03-13T00:00:01Z',
            },
          ],
        },
        false
      );
      callbacks.onEvent(
        {
          delegate_event: {
            type: 'started',
            delegate_id: 'delegate-1',
            task: 'Delegate task',
          },
        },
        false
      );
      callbacks.onEvent(
        {
          delegate_messages: {
            delegate_id: 'delegate-1',
            messages: [
              {
                id: 'delegate-msg-1',
                session_id: 'delegate-1',
                type: 'assistant',
                sequence_id: 1,
                content: 'delegate reply',
                created_at: '2026-03-13T00:00:02Z',
              },
            ],
          },
        },
        false
      );
    });

    expect(result.current.messages).toHaveLength(3);
    expect(result.current.messages.map((message) => message.id)).toEqual([
      'msg-1',
      'msg-2',
      'msg-3',
    ]);
    expect(result.current.messages[1]?.content).toBe('out-of-order reply');
    expect(result.current.messages[2]?.content).toBe('streamed reply updated');
    expect(result.current.delegateStatuses['delegate-1']?.status).toBe(
      'running'
    );
    expect(result.current.delegateMessages['delegate-1']?.[0]?.content).toBe(
      'delegate reply'
    );
  });

  it('heals correctly when polling updates state before the stream reconnect snapshot arrives', async () => {
    mockedSSEStatus = {
      isSessionLive: false,
    };
    useSSEConnectionMock.mockImplementation(() => mockedSSEStatus);

    const sessionResponses = [
      makeSessionDetailResponse({
        messages: [makeApiMessage('msg-1', 'initial snapshot', 1)],
      }),
      makeSessionDetailResponse({
        messages: [makeApiMessage('msg-2', 'polled snapshot', 2)],
        working: true,
      }),
    ];
    let sessionFetchCount = 0;

    getMock.mockImplementation(
      async (
        _path: string,
        request?: { params?: { path?: { sessionId?: string } } }
      ) => {
        if (request?.params?.path?.sessionId === 'sess-1') {
          const idx = Math.min(sessionFetchCount, sessionResponses.length - 1);
          sessionFetchCount += 1;
          return { data: sessionResponses[idx] };
        }
        throw new Error('unexpected request');
      }
    );

    const { result, rerender } = renderHook(() => useAgentChatWithOpen(), {
      wrapper: TestProviders,
    });

    act(() => {
      result.current.openChat();
    });
    await act(async () => {
      await result.current.selectSession('sess-1');
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });

    expect(result.current.messages.map((message) => message.id)).toEqual([
      'msg-2',
    ]);
    expect(result.current.messages[0]?.content).toBe('polled snapshot');
    expect(result.current.sessionState?.working).toBe(true);

    mockedSSEStatus = {
      isSessionLive: true,
    };
    rerender();

    const latestCall =
      useSSEConnectionMock.mock.calls[
        useSSEConnectionMock.mock.calls.length - 1
      ];
    const callbacks = latestCall?.[3] as
      | {
          onEvent: (event: unknown, replace: boolean) => void;
        }
      | undefined;
    expect(callbacks).toBeDefined();
    if (!callbacks) {
      throw new Error('expected agent stream callbacks');
    }

    act(() => {
      callbacks.onEvent(
        {
          messages: [makeApiMessage('msg-3', 'reconnect snapshot', 3)],
          session_state: {
            session_id: 'sess-1',
            working: false,
            has_pending_prompt: false,
            model: 'gpt-test',
            total_cost: 0,
          },
        },
        true
      );
      callbacks.onEvent(
        {
          messages: [
            {
              id: 'msg-4',
              session_id: 'sess-1',
              type: 'assistant',
              sequence_id: 4,
              content: 'post-reconnect delta',
              created_at: '2026-03-13T00:00:03Z',
            },
          ],
        },
        false
      );
    });

    expect(result.current.messages.map((message) => message.id)).toEqual([
      'msg-3',
      'msg-4',
    ]);
    expect(result.current.messages[0]?.content).toBe('reconnect snapshot');
    expect(result.current.messages[1]?.content).toBe('post-reconnect delta');
    expect(result.current.sessionState?.working).toBe(false);
  });

  it('shows working immediately after sending a message without forcing an extra session refresh', async () => {
    getMock.mockImplementation(
      async (
        _path: string,
        request?: { params?: { path?: { sessionId?: string } } }
      ) => {
        if (request?.params?.path?.sessionId === 'sess-1') {
          return {
            data: makeSessionDetailResponse({
              messages: [makeApiMessage('msg-1', 'user echoed', 1)],
              working: true,
            }),
          };
        }
        throw new Error('unexpected request');
      }
    );
    postMock.mockResolvedValue({ data: { status: 'accepted' } });

    const { result } = renderHook(() => useAgentChatWithOpen(), {
      wrapper: TestProviders,
    });

    act(() => {
      result.current.openChat();
    });

    await act(async () => {
      await result.current.selectSession('sess-1');
    });

    getMock.mockClear();

    await act(async () => {
      await result.current.sendMessage('follow up');
    });

    expect(result.current.isWorking).toBe(true);
    expect(postMock).toHaveBeenCalledWith(
      '/agent/sessions/{sessionId}/chat',
      expect.objectContaining({
        params: {
          path: { sessionId: 'sess-1' },
          query: { remoteNode: 'local' },
        },
      })
    );
    expect(getMock).not.toHaveBeenCalled();
  });
});
