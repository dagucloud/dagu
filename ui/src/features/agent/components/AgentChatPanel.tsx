// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ReactElement, useCallback, useEffect, useRef, useState } from 'react';

import { AlertCircle, X } from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { useIsMobile } from '@/hooks/useIsMobile';

import { SESSION_SIDEBAR_STORAGE_KEY } from '../constants';
import { useAgentChat } from '../hooks/useAgentChat';
import { DAGContext, SessionWithState } from '../types';
import { AgentChatModalHeader } from './AgentChatModalHeader';
import { ChatInput } from './ChatInput';
import { ChatMessages } from './ChatMessages';
import { DelegatePanel } from './DelegatePanel';
import { SessionSidebar } from './SessionSidebar';

export type AgentChatController = ReturnType<typeof useAgentChat>;

type AgentChatPanelProps = {
  active?: boolean;
  className?: string;
  defaultSidebarOpen?: boolean;
  onClose?: () => void;
  placeholder?: string;
  rememberSidebarState?: boolean;
  showDelegatePanels?: boolean;
};

type AgentChatPanelViewProps = AgentChatPanelProps & {
  controller: AgentChatController;
  onClose?: () => void;
};

function findLatestSession(
  sessions: SessionWithState[]
): SessionWithState | null {
  if (sessions.length === 0) return null;

  let latest: SessionWithState | null = null;
  for (const sess of sessions) {
    if (sess.session.parent_session_id) continue;
    if (
      !latest ||
      new Date(sess.session.updated_at) > new Date(latest.session.updated_at)
    ) {
      latest = sess;
    }
  }
  return latest;
}

export function AgentChatPanel({
  active = true,
  className,
  defaultSidebarOpen = true,
  onClose,
  placeholder,
  rememberSidebarState = true,
  showDelegatePanels = true,
}: AgentChatPanelProps): ReactElement {
  const controller = useAgentChat({ active });

  return (
    <AgentChatPanelView
      active={active}
      className={className}
      controller={controller}
      defaultSidebarOpen={defaultSidebarOpen}
      onClose={onClose}
      placeholder={placeholder}
      rememberSidebarState={rememberSidebarState}
      showDelegatePanels={showDelegatePanels}
    />
  );
}

export function AgentChatPanelView({
  active = true,
  className,
  controller,
  defaultSidebarOpen = true,
  onClose,
  placeholder = 'Ask me to create a DAG, run a command...',
  rememberSidebarState = true,
  showDelegatePanels = true,
}: AgentChatPanelViewProps): ReactElement {
  const isMobile = useIsMobile();
  const {
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
    clearSession,
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
  } = controller;

  const [sidebarOpen, setSidebarOpen] = useState(() => {
    if (!rememberSidebarState) return defaultSidebarOpen;
    try {
      const saved = localStorage.getItem(SESSION_SIDEBAR_STORAGE_KEY);
      return saved == null ? defaultSidebarOpen : saved !== 'false';
    } catch {
      return defaultSidebarOpen;
    }
  });

  const hasAutoSelectedRef = useRef(false);
  const wasActiveRef = useRef(false);

  useEffect(() => {
    if (active && !wasActiveRef.current) {
      hasAutoSelectedRef.current = false;
      fetchSessions();
    }
    wasActiveRef.current = active;
  }, [active, fetchSessions]);

  useEffect(() => {
    if (
      active &&
      sessions.length > 0 &&
      !sessionId &&
      !hasAutoSelectedRef.current
    ) {
      hasAutoSelectedRef.current = true;
      const latest = findLatestSession(sessions);
      if (latest) {
        selectSession(latest.session.id).catch((err) =>
          setError(
            err instanceof Error ? err.message : 'Failed to load session'
          )
        );
      }
    }
  }, [active, sessions, sessionId, selectSession, setError]);

  const toggleSidebar = useCallback(() => {
    setSidebarOpen((prev) => {
      const next = !prev;
      if (rememberSidebarState) {
        try {
          localStorage.setItem(SESSION_SIDEBAR_STORAGE_KEY, String(next));
        } catch {
          // Ignore storage failures.
        }
      }
      return next;
    });
  }, [rememberSidebarState]);

  const handleSend = useCallback(
    (
      message: string,
      dagContexts?: DAGContext[],
      model?: string,
      soulId?: string
    ): void => {
      sendMessage(message, model, dagContexts, soulId).catch(() => {});
    },
    [sendMessage]
  );

  const handleCancel = useCallback((): void => {
    cancelSession().catch((err) =>
      setError(err instanceof Error ? err.message : 'Failed to cancel')
    );
  }, [cancelSession, setError]);

  const handleClearSession = useCallback((): void => {
    hasAutoSelectedRef.current = true;
    clearSession();
  }, [clearSession]);

  const handleSelectSession = useCallback(
    (value: string): void => {
      if (value === 'new') {
        handleClearSession();
        return;
      }
      selectSession(value).catch((err) =>
        setError(
          err instanceof Error ? err.message : 'Failed to select session'
        )
      );
    },
    [selectSession, handleClearSession, setError]
  );

  const handleOpenDelegate = useCallback(
    (id: string) => {
      const info = delegateStatuses[id];
      if (info) {
        if (delegates.some((d) => d.id === id)) {
          removeDelegate(id);
        } else {
          reopenDelegate(id, info.task);
        }
      }
    },
    [delegateStatuses, delegates, reopenDelegate, removeDelegate]
  );

  const errorBanner = error && (
    <Alert
      variant="destructive"
      className="mx-3 mt-3 px-3 py-2 pr-10 text-xs [&>svg]:left-3 [&>svg]:top-2.5 [&>svg~*]:pl-6"
    >
      <AlertCircle className="h-4 w-4" aria-hidden="true" />
      <div className="min-w-0">
        <AlertDescription className="break-words text-xs">
          {error}
        </AlertDescription>
        <Button
          type="button"
          aria-label="Dismiss chat error"
          onClick={clearError}
          variant="ghost"
          size="icon-sm"
          className="absolute right-2 top-1.5 h-6 w-6 text-destructive/70 hover:bg-destructive/10 hover:text-destructive"
        >
          <X className="h-3 w-3" />
        </Button>
      </div>
    </Alert>
  );

  return (
    <>
      <div
        className={cn(
          'flex h-full min-h-0 flex-col overflow-hidden bg-card dark:bg-zinc-950',
          className
        )}
      >
        <AgentChatModalHeader
          sessionId={sessionId}
          totalCost={sessionState?.total_cost}
          isSidebarOpen={sidebarOpen}
          onToggleSidebar={toggleSidebar}
          onClearSession={handleClearSession}
          onClose={onClose}
          isMobile={isMobile}
        />
        <div className="relative flex min-h-0 flex-1 overflow-hidden">
          <SessionSidebar
            isOpen={sidebarOpen}
            isMobile={isMobile}
            sessions={sessions}
            activeSessionId={sessionId}
            onSelectSession={handleSelectSession}
            onClose={toggleSidebar}
            onLoadMore={loadMoreSessions}
            hasMore={hasMoreSessions}
            isLoadingMore={isLoadingMore}
          />
          <div className="flex min-h-0 min-w-0 flex-1 flex-col">
            {errorBanner}
            <ChatMessages
              messages={messages}
              pendingUserMessage={pendingUserMessage}
              isWorking={isWorking}
              onPromptRespond={respondToPrompt}
              answeredPrompts={answeredPrompts}
              delegateStatuses={delegateStatuses}
              onOpenDelegate={handleOpenDelegate}
            />
            <ChatInput
              onSend={handleSend}
              onCancel={handleCancel}
              isWorking={isWorking}
              placeholder={placeholder}
              hasActiveSession={!!sessionId}
            />
          </div>
        </div>
      </div>
      {showDelegatePanels &&
        delegates.map((d) => (
          <DelegatePanel
            key={d.id}
            delegateId={d.id}
            task={d.task}
            status={d.status}
            zIndex={d.zIndex}
            index={d.positionIndex}
            messages={delegateMessages[d.id] || []}
            onClose={() => removeDelegate(d.id)}
            onBringToFront={() => bringToFront(d.id)}
          />
        ))}
    </>
  );
}
