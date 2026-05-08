import {
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useNavigate } from 'react-router-dom';
import {
  components,
  ComponentsParametersAgentSessionPaginationMode,
} from '@/api/v1/schema';
import { useConfig } from '@/contexts/ConfigContext';
import { useUserPreferences } from '@/contexts/UserPreference';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useClient } from '@/hooks/api';
import { useAgentChatContext } from '../context/AgentChatContext';
import {
  DAGContext,
  DelegateStatus,
  Message,
  MessageType,
  PromptType,
  SessionWithState,
  StreamResponse,
  UIActionType,
  UserPromptResponse,
} from '../types';
import { useSSEConnection } from './useSSEConnection';
import { useDelegateManager } from './useDelegateManager';

type ApiSessionWithState = components['schemas']['AgentSessionWithState'];
type ApiMessage = components['schemas']['AgentMessage'];
type ApiSessionDetail = components['schemas']['AgentSessionDetailResponse'];
const FALLBACK_POLL_INTERVAL_MS = 2000;

type UseAgentChatOptions = {
  active?: boolean;
};

function convertApiMessage(msg: ApiMessage): Message {
  return {
    id: msg.id,
    session_id: msg.sessionId,
    type: msg.type as MessageType,
    sequence_id: msg.sequenceId,
    content: msg.content,
    tool_calls: msg.toolCalls?.map((tc) => ({
      id: tc.id,
      type: tc.type,
      function: tc.function,
    })),
    tool_results: msg.toolResults?.map((tr) => ({
      tool_call_id: tr.toolCallId,
      content: tr.content,
      is_error: tr.isError,
    })),
    ui_action: msg.uiAction?.type
      ? { type: msg.uiAction.type as UIActionType, path: msg.uiAction.path }
      : undefined,
    user_prompt: msg.userPrompt
      ? {
          prompt_id: msg.userPrompt.promptId,
          question: msg.userPrompt.question,
          options: msg.userPrompt.options,
          allow_free_text: msg.userPrompt.allowFreeText,
          free_text_placeholder: msg.userPrompt.freeTextPlaceholder,
          multi_select: msg.userPrompt.multiSelect,
          prompt_type: msg.userPrompt.promptType as PromptType | undefined,
          command: msg.userPrompt.command,
          working_dir: msg.userPrompt.workingDir,
        }
      : undefined,
    usage: msg.usage
      ? {
          prompt_tokens: msg.usage.promptTokens ?? 0,
          completion_tokens: msg.usage.completionTokens ?? 0,
          total_tokens: msg.usage.totalTokens ?? 0,
        }
      : undefined,
    cost: msg.cost,
    delegate_ids: msg.delegateIds ?? undefined,
    created_at: msg.createdAt,
  };
}

function convertApiSessions(
  sessions: ApiSessionWithState[]
): SessionWithState[] {
  return sessions.map((s) => ({
    session: {
      id: s.session.id,
      user_id: s.session.userId,
      title: s.session.title,
      created_at: s.session.createdAt,
      updated_at: s.session.updatedAt,
      parent_session_id: s.session.parentSessionId,
      delegate_task: s.session.delegateTask,
    },
    working: s.working,
    has_pending_prompt: s.hasPendingPrompt,
    model: s.model,
    total_cost: s.totalCost,
  }));
}

function convertApiSessionDetail(detail: ApiSessionDetail): StreamResponse {
  return {
    messages: detail.messages?.map(convertApiMessage),
    session: detail.session
      ? {
          id: detail.session.id,
          user_id: detail.session.userId,
          title: detail.session.title,
          created_at: detail.session.createdAt,
          updated_at: detail.session.updatedAt,
          parent_session_id: detail.session.parentSessionId,
          delegate_task: detail.session.delegateTask,
        }
      : undefined,
    session_state: detail.sessionState
      ? {
          session_id: detail.sessionState.sessionId,
          working: detail.sessionState.working,
          has_pending_prompt: detail.sessionState.hasPendingPrompt,
          model: detail.sessionState.model,
          total_cost: detail.sessionState.totalCost,
        }
      : undefined,
    delegates: detail.delegates?.map((d) => ({
      id: d.id,
      task: d.task,
      status: d.status as DelegateStatus,
      cost: d.cost,
    })),
  };
}

function toDagContextsBody(dagContexts?: DAGContext[]) {
  return dagContexts?.map((dc) => ({
    dagFile: dc.dag_file,
    dagRunId: dc.dag_run_id,
  }));
}

function mergeMessages(current: Message[], incoming: Message[]): Message[] {
  if (incoming.length === 0) {
    return current;
  }

  const next = [...current];
  const indexById = new Map<string, number>();
  next.forEach((message, index) => {
    indexById.set(message.id, index);
  });

  let requiresSort = false;
  let lastAppendedSequence =
    current.length > 0
      ? current[current.length - 1]!.sequence_id
      : Number.NEGATIVE_INFINITY;

  for (const message of incoming) {
    const existingIndex = indexById.get(message.id);
    if (existingIndex === undefined) {
      indexById.set(message.id, next.length);
      next.push(message);
      if (message.sequence_id < lastAppendedSequence) {
        requiresSort = true;
      }
      lastAppendedSequence = message.sequence_id;
      continue;
    }

    const previousSequence =
      existingIndex > 0
        ? next[existingIndex - 1]!.sequence_id
        : Number.NEGATIVE_INFINITY;
    const followingSequence =
      existingIndex < next.length - 1
        ? next[existingIndex + 1]!.sequence_id
        : Number.POSITIVE_INFINITY;
    next[existingIndex] = message;
    if (
      message.sequence_id < previousSequence ||
      message.sequence_id > followingSequence
    ) {
      requiresSort = true;
    }
  }

  if (!requiresSort) {
    return next;
  }

  return next.sort((left, right) => left.sequence_id - right.sequence_id);
}

export function useAgentChat(options: UseAgentChatOptions = {}) {
  const config = useConfig();
  const client = useClient();
  const navigate = useNavigate();
  const { preferences } = useUserPreferences();
  const appBarContext = useContext(AppBarContext);
  const {
    isOpen: isChatOpen,
    sessionId,
    messages,
    pendingUserMessage,
    sessionState,
    sessions,
    hasMoreSessions,
    sessionCursor,
    setSessionId,
    setMessages,
    setSessionState,
    setSessions,
    appendSessions,
    setHasMoreSessions,
    setSessionPage,
    setSessionCursor,
    setPendingUserMessage,
    clearSession,
  } = useAgentChatContext();

  const selectGenRef = useRef(0);
  const handledUIActionIdsRef = useRef<Map<string, Set<string>>>(new Map());
  const hydratedUIActionsRef = useRef<Set<string>>(new Set());
  const allowInitialUIActionsRef = useRef<Set<string>>(new Set());
  const delegateCatalogHydratedRef = useRef(false);
  const isLoadingMoreRef = useRef(false);
  const [isSending, setIsSending] = useState(false);
  const [isLoadingMore, setIsLoadingMore] = useState(false);
  const [optimisticWorking, setOptimisticWorking] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [answeredPrompts, setAnsweredPrompts] = useState<
    Record<string, string>
  >({});

  const apiURL = config.apiURL;
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const isActive = options.active ?? isChatOpen;

  const dm = useDelegateManager();
  const {
    delegates,
    delegateStatuses,
    delegateMessages,
    reconcileDelegateSnapshots,
    applyDelegateSessionSnapshot,
    handleDelegateMessages,
    handleDelegateEvent,
    resetDelegates,
    bringToFront,
    openDelegate,
    hasDelegateMessages,
    removeDelegate,
  } = dm;
  const openDelegateSessionIds = delegates.map((delegate) => delegate.id);
  const sortedOpenDelegateSessionIds = useMemo(
    () => [...openDelegateSessionIds].sort(),
    [openDelegateSessionIds]
  );

  const delegateStatusesRef = useRef(delegateStatuses);
  delegateStatusesRef.current = delegateStatuses;

  const consumeNavigateUIActions = useCallback(
    (
      targetSessionId: string | null | undefined,
      sessionMessages: Message[]
    ) => {
      if (!targetSessionId) {
        return;
      }

      const navigateMessages = sessionMessages.filter(
        (message) =>
          message.session_id === targetSessionId &&
          message.type === 'ui_action' &&
          message.ui_action?.type === 'navigate'
      );
      let handled = handledUIActionIdsRef.current.get(targetSessionId);
      if (!handled) {
        handled = new Set<string>();
        handledUIActionIdsRef.current.set(targetSessionId, handled);
      }

      if (!hydratedUIActionsRef.current.has(targetSessionId)) {
        hydratedUIActionsRef.current.add(targetSessionId);
        const shouldReplayExistingActions =
          allowInitialUIActionsRef.current.has(targetSessionId);
        allowInitialUIActionsRef.current.delete(targetSessionId);

        if (!shouldReplayExistingActions) {
          for (const message of navigateMessages) {
            handled.add(message.id);
          }
          return;
        }
      }

      for (const message of navigateMessages) {
        if (handled.has(message.id)) {
          continue;
        }
        handled.add(message.id);
        if (message.ui_action?.path) {
          navigate(message.ui_action.path);
        }
      }
    },
    [navigate]
  );

  useEffect(() => {
    const allowed = new Set<string>();
    if (sessionId && allowInitialUIActionsRef.current.has(sessionId)) {
      allowed.add(sessionId);
    }
    handledUIActionIdsRef.current = new Map();
    hydratedUIActionsRef.current = new Set();
    allowInitialUIActionsRef.current = allowed;
    delegateCatalogHydratedRef.current = false;
  }, [sessionId]);

  useEffect(() => {
    consumeNavigateUIActions(sessionId, messages);
  }, [consumeNavigateUIActions, messages, sessionId]);

  const updateSessionListEntry = useCallback(
    (snapshot: StreamResponse) => {
      const targetId = snapshot.session?.id ?? snapshot.session_state?.session_id;
      if (!targetId || (!snapshot.session && !snapshot.session_state)) {
        return;
      }

      setSessions((current) => {
        let found = false;
        const next = current.map((item) => {
          if (item.session.id !== targetId) {
            return item;
          }

          found = true;
          const nextSession = snapshot.session
            ? { ...item.session, ...snapshot.session }
            : item.session;
          const nextState = snapshot.session_state
            ? {
                working: snapshot.session_state.working,
                has_pending_prompt: snapshot.session_state.has_pending_prompt,
                model: snapshot.session_state.model ?? item.model,
                total_cost: snapshot.session_state.total_cost ?? item.total_cost,
              }
            : {};

          return {
            ...item,
            session: nextSession,
            ...nextState,
          };
        });

        return found ? next : current;
      });
    },
    [setSessions]
  );

  const applySessionSnapshot = useCallback(
    (snapshot: StreamResponse) => {
      const nextMessages = snapshot.messages || [];
      if (
        pendingUserMessage &&
        nextMessages.some((message) => message.type === 'user')
      ) {
        setPendingUserMessage(null);
      }

      setMessages(nextMessages);
      if (snapshot.session_state) {
        setOptimisticWorking(false);
        setSessionState(snapshot.session_state);
      }
      updateSessionListEntry(snapshot);

      const nextDelegates = snapshot.delegates || [];
      if (delegateCatalogHydratedRef.current) {
        for (const delegate of nextDelegates) {
          if (!delegateStatusesRef.current[delegate.id]) {
            allowInitialUIActionsRef.current.add(delegate.id);
          }
        }
      }
      delegateCatalogHydratedRef.current = true;
      reconcileDelegateSnapshots(nextDelegates);
    },
    [
      pendingUserMessage,
      setPendingUserMessage,
      setMessages,
      setSessionState,
      updateSessionListEntry,
      reconcileDelegateSnapshots,
    ]
  );

  const applySessionEvent = useCallback(
    (event: StreamResponse, replace = false) => {
      if (replace) {
        applySessionSnapshot(event);
        return;
      }

      if (event.messages && event.messages.length > 0) {
        if (
          pendingUserMessage &&
          event.messages.some((message) => message.type === 'user')
        ) {
          setPendingUserMessage(null);
        }

        setMessages((current) => mergeMessages(current, event.messages || []));
      }

      // Only clear the pending message once the actual user message appears in
      // the stream. Previously this also cleared on working=true, but that
      // caused the pending bubble to vanish before the real message arrived.

      if (event.session_state) {
        setOptimisticWorking(false);
        setSessionState(event.session_state);
      }
      updateSessionListEntry(event);

      if (event.delegates) {
        if (delegateCatalogHydratedRef.current) {
          for (const delegate of event.delegates) {
            if (!delegateStatusesRef.current[delegate.id]) {
              allowInitialUIActionsRef.current.add(delegate.id);
            }
          }
        }
        delegateCatalogHydratedRef.current = true;
        reconcileDelegateSnapshots(event.delegates);
      }

      if (event.delegate_event) {
        if (event.delegate_event.type === 'started') {
          allowInitialUIActionsRef.current.add(
            event.delegate_event.delegate_id
          );
        }
        handleDelegateEvent(event.delegate_event);
      }

      if (event.delegate_messages) {
        handleDelegateMessages(event.delegate_messages);
        consumeNavigateUIActions(
          event.delegate_messages.delegate_id,
          event.delegate_messages.messages
        );
      }
    },
    [
      applySessionSnapshot,
      pendingUserMessage,
      setPendingUserMessage,
      setMessages,
      setSessionState,
      updateSessionListEntry,
      reconcileDelegateSnapshots,
      handleDelegateEvent,
      handleDelegateMessages,
      consumeNavigateUIActions,
    ]
  );

  const applyDelegateSnapshot = useCallback(
    (delegateId: string, snapshot: StreamResponse) => {
      const existing = delegateStatusesRef.current[delegateId];
      const nextMessages = snapshot.messages || [];
      applyDelegateSessionSnapshot(
        delegateId,
        snapshot.session?.delegate_task || existing?.task || '',
        snapshot.session_state?.working ? 'running' : 'completed',
        nextMessages
      );
      consumeNavigateUIActions(delegateId, nextMessages);
    },
    [applyDelegateSessionSnapshot, consumeNavigateUIActions]
  );

  const applySessionSnapshotRef = useRef(applySessionSnapshot);
  applySessionSnapshotRef.current = applySessionSnapshot;

  const applyDelegateSnapshotRef = useRef(applyDelegateSnapshot);
  applyDelegateSnapshotRef.current = applyDelegateSnapshot;

  const fetchSessionDetail = useCallback(
    async (id: string): Promise<StreamResponse> => {
      const { data, error: apiError } = await client.GET(
        '/agent/sessions/{sessionId}',
        {
          params: { path: { sessionId: id }, query: { remoteNode } },
        }
      );
      if (apiError)
        throw new Error(apiError.message || 'Failed to load session');
      if (!data) throw new Error('Failed to load session');
      return convertApiSessionDetail(data);
    },
    [client, remoteNode]
  );

  const fetchSessionDetailRef = useRef(fetchSessionDetail);
  fetchSessionDetailRef.current = fetchSessionDetail;

  const sseStatus = useSSEConnection(sessionId, apiURL, remoteNode, {
    onEvent: (event, replace) => {
      applySessionEvent(event, replace);
    },
    onNavigate: (path) => navigate(path),
  });

  useEffect(() => {
    // Only poll while a chat surface is active and a session is selected.
    // Without this check, polling continues after the modal closes because
    // sessionId stays set in the context, wasting connection slots.
    if (!isActive || !sessionId || sseStatus.isSessionLive) {
      return;
    }

    let active = true;
    let nextPollTimeout: ReturnType<typeof setTimeout> | null = null;

    const scheduleNextPoll = () => {
      if (!active) {
        return;
      }
      nextPollTimeout = setTimeout(() => {
        void runPollLoop();
      }, FALLBACK_POLL_INTERVAL_MS);
    };

    const runPollLoop = async () => {
      if (sessionId) {
        try {
          const snapshot = await fetchSessionDetailRef.current(sessionId);
          if (!active) {
            return;
          }
          applySessionSnapshotRef.current(snapshot);
        } catch {
          // Best effort — the next poll or SSE recovery can still heal state.
        }
      }

      for (const delegateId of sortedOpenDelegateSessionIds) {
        try {
          const snapshot = await fetchSessionDetailRef.current(delegateId);
          if (!active) {
            return;
          }
          applyDelegateSnapshotRef.current(delegateId, snapshot);
        } catch {
          // Best effort — the next poll or SSE recovery can still heal state.
        }
      }

      scheduleNextPoll();
    };

    scheduleNextPoll();

    return () => {
      active = false;
      if (nextPollTimeout) {
        clearTimeout(nextPollTimeout);
      }
    };
  }, [
    isActive,
    sessionId,
    sseStatus.isSessionLive,
    sortedOpenDelegateSessionIds,
  ]);

  const fetchSessionsPage = useCallback(
    async (cursor: string | null): Promise<void> => {
      try {
        const { data, error: apiError } = await client.GET('/agent/sessions', {
          params: {
            query: {
              remoteNode,
              paginationMode:
                ComponentsParametersAgentSessionPaginationMode.cursor,
              perPage: 30,
              ...(cursor ? { cursor } : {}),
            },
          },
        });
        if (apiError)
          throw new Error(apiError.message || 'Failed to fetch sessions');
        if (!data) return;

        const converted = convertApiSessions(data.sessions);
        if (!cursor) {
          setSessions(converted);
          setSessionPage(1);
        } else {
          appendSessions(converted);
          setSessionPage((prev) => prev + 1);
        }
        setSessionCursor(data.nextCursor ?? null);
        setHasMoreSessions(Boolean(data.nextCursor));
      } catch (err) {
        setError(
          err instanceof Error ? err.message : 'Failed to fetch sessions'
        );
        if (!cursor) {
          setSessions([]);
          setSessionCursor(null);
        }
      }
    },
    [
      client,
      remoteNode,
      setSessions,
      appendSessions,
      setHasMoreSessions,
      setSessionPage,
      setSessionCursor,
    ]
  );

  const loadMoreSessions = useCallback(async (): Promise<void> => {
    if (!hasMoreSessions || !sessionCursor || isLoadingMoreRef.current) return;
    isLoadingMoreRef.current = true;
    setIsLoadingMore(true);
    try {
      await fetchSessionsPage(sessionCursor);
    } finally {
      isLoadingMoreRef.current = false;
      setIsLoadingMore(false);
    }
  }, [fetchSessionsPage, sessionCursor, hasMoreSessions]);

  const startSession = useCallback(
    async (
      message: string,
      model?: string,
      dagContexts?: DAGContext[],
      soulId?: string
    ): Promise<string> => {
      const { data, error: apiError } = await client.POST('/agent/sessions', {
        params: { query: { remoteNode } },
        body: {
          message,
          model,
          dagContexts: toDagContextsBody(dagContexts),
          safeMode: preferences.safeMode,
          soulId: soulId || undefined,
        },
      });
      if (apiError)
        throw new Error(apiError.message || 'Failed to create session');
      allowInitialUIActionsRef.current = new Set([data.sessionId]);
      setSessionId(data.sessionId);
      await fetchSessionsPage(null);
      return data.sessionId;
    },
    [client, remoteNode, setSessionId, fetchSessionsPage, preferences.safeMode]
  );

  const sendMessage = useCallback(
    async (
      message: string,
      model?: string,
      dagContexts?: DAGContext[],
      soulId?: string
    ): Promise<void> => {
      setIsSending(true);
      setError(null);
      setPendingUserMessage(message);

      try {
        if (!sessionId) {
          setOptimisticWorking(true);
          await startSession(message, model, dagContexts, soulId);
          return;
        }
        const { error: apiError } = await client.POST(
          '/agent/sessions/{sessionId}/chat',
          {
            params: { path: { sessionId }, query: { remoteNode } },
            body: {
              message,
              model,
              dagContexts: toDagContextsBody(dagContexts),
              safeMode: preferences.safeMode,
              soulId: soulId || undefined,
            },
          }
        );
        if (apiError)
          throw new Error(apiError.message || 'Failed to send message');
        setOptimisticWorking(true);
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to send message');
        setPendingUserMessage(null);
        setOptimisticWorking(false);
        throw err;
      } finally {
        setIsSending(false);
      }
    },
    [
      client,
      remoteNode,
      sessionId,
      startSession,
      setPendingUserMessage,
      preferences.safeMode,
    ]
  );

  const cancelSession = useCallback(async (): Promise<void> => {
    if (!sessionId) return;
    const { error: apiError } = await client.POST(
      '/agent/sessions/{sessionId}/cancel',
      {
        params: { path: { sessionId }, query: { remoteNode } },
      }
    );
    if (apiError)
      throw new Error(apiError.message || 'Failed to cancel session');
  }, [client, remoteNode, sessionId]);

  const respondToPrompt = useCallback(
    async (
      response: UserPromptResponse,
      displayValue: string
    ): Promise<void> => {
      if (!sessionId) return;

      try {
        const { error: apiError } = await client.POST(
          '/agent/sessions/{sessionId}/respond',
          {
            params: { path: { sessionId }, query: { remoteNode } },
            body: {
              promptId: response.prompt_id,
              selectedOptionIds: response.selected_option_ids,
              freeTextResponse: response.free_text_response,
              cancelled: response.cancelled,
            },
          }
        );
        if (apiError)
          throw new Error(apiError.message || 'Failed to submit response');
        setOptimisticWorking(true);
        setAnsweredPrompts((prev) => ({
          ...prev,
          [response.prompt_id]: displayValue,
        }));
      } catch (err) {
        setOptimisticWorking(false);
        setError(
          err instanceof Error ? err.message : 'Failed to submit response'
        );
      }
    },
    [client, remoteNode, sessionId]
  );

  const fetchSessions = useCallback(async (): Promise<void> => {
    await fetchSessionsPage(null);
  }, [fetchSessionsPage]);

  const selectSession = useCallback(
    async (id: string): Promise<void> => {
      const gen = ++selectGenRef.current;
      // Set sessionId first so the old agent EventSource closes and frees
      // a connection slot. Without this, fetchSessionDetail would deadlock
      // waiting for a connection while the old SSE holds it.
      allowInitialUIActionsRef.current = new Set();
      setSessionId(id);
      setAnsweredPrompts({});
      try {
        const converted = await fetchSessionDetail(id);
        if (gen !== selectGenRef.current) return;
        applySessionSnapshot(converted);
      } catch {
        // The SSE connection or polling fallback will recover state.
      }
    },
    [fetchSessionDetail, setSessionId, applySessionSnapshot]
  );

  const isWorking =
    isSending || optimisticWorking || sessionState?.working || false;

  const clearError = useCallback(() => setError(null), []);

  const handleClearSession = useCallback(() => {
    selectGenRef.current++;
    allowInitialUIActionsRef.current = new Set();
    clearSession();
    setOptimisticWorking(false);
    setAnsweredPrompts({});
    resetDelegates();
  }, [clearSession, resetDelegates]);

  const reopenDelegate = useCallback(
    async (delegateId: string, task: string) => {
      if (!hasDelegateMessages(delegateId)) {
        try {
          const snapshot = await fetchSessionDetail(delegateId);
          applyDelegateSnapshot(delegateId, snapshot);
        } catch {
          // Best effort — panel will show empty state
        }
      }
      openDelegate(delegateId, task);
    },
    [
      fetchSessionDetail,
      hasDelegateMessages,
      applyDelegateSnapshot,
      openDelegate,
    ]
  );

  return {
    sessionId,
    messages,
    pendingUserMessage,
    sessionState,
    sessions,
    hasMoreSessions,
    isLoadingMore,
    isWorking,
    error,
    answeredPrompts,
    setError,
    sendMessage,
    cancelSession,
    clearSession: handleClearSession,
    clearError,
    fetchSessions,
    loadMoreSessions,
    selectSession,
    respondToPrompt,
    delegates,
    delegateStatuses,
    delegateMessages,
    bringToFront,
    reopenDelegate,
    removeDelegate,
  };
}
