// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ReactElement, useCallback, useEffect, useRef, useState } from 'react';

import { AlertCircle, X } from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { useIsMobile } from '@/hooks/useIsMobile';

import {
  ANIMATION_CLOSE_DURATION_MS,
  ANIMATION_OPEN_DURATION_MS,
  LAST_SESSION_STORAGE_KEY,
  SESSION_SIDEBAR_STORAGE_KEY,
} from '../constants';
import { useAgentChatContext } from '../context/AgentChatContext';
import { useAgentChat } from '../hooks/useAgentChat';
import { useResizableDraggable } from '../hooks/useResizableDraggable';
import { SessionWithState, DAGContext } from '../types';
import { AgentChatModalHeader } from './AgentChatModalHeader';
import { ChatInput } from './ChatInput';
import { ChatMessages } from './ChatMessages';
import { DelegatePanel } from './DelegatePanel';
import { ResizeHandles } from './ResizeHandles';
import { SessionSidebar } from './SessionSidebar';

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

function readLastSessionId(): string | null {
  try {
    const value = localStorage.getItem(LAST_SESSION_STORAGE_KEY);
    return value && value.trim() ? value : null;
  } catch {
    return null;
  }
}

function rememberLastSessionId(sessionId: string): void {
  try {
    localStorage.setItem(LAST_SESSION_STORAGE_KEY, sessionId);
  } catch {
    /* ignore */
  }
}

function forgetLastSessionId(): void {
  try {
    localStorage.removeItem(LAST_SESSION_STORAGE_KEY);
  } catch {
    /* ignore */
  }
}

export function AgentChatModal(): ReactElement | null {
  const {
    isOpen,
    isClosing,
    closeChat,
    initialInputValue,
    setInitialInputValue,
  } = useAgentChatContext();
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
  } = useAgentChat();
  const { bounds, dragHandlers, resizeHandlers } = useResizableDraggable({
    storageKey: 'agent-chat-modal-bounds',
    defaultWidth: 560,
  });

  const [sidebarOpen, setSidebarOpen] = useState(() => {
    try {
      const saved = localStorage.getItem(SESSION_SIDEBAR_STORAGE_KEY);
      return saved !== 'false';
    } catch {
      return true;
    }
  });

  const toggleSidebar = useCallback(() => {
    setSidebarOpen((prev) => {
      const next = !prev;
      try {
        localStorage.setItem(SESSION_SIDEBAR_STORAGE_KEY, String(next));
      } catch {
        /* ignore */
      }
      return next;
    });
  }, []);

  const hasAutoSelectedRef = useRef(false);
  const wasOpenRef = useRef(false);

  useEffect(() => {
    if (isOpen && !wasOpenRef.current) {
      hasAutoSelectedRef.current = false;
      fetchSessions();
    }
    wasOpenRef.current = isOpen;
  }, [isOpen, fetchSessions]);

  useEffect(() => {
    if (
      isOpen &&
      sessions.length > 0 &&
      !sessionId &&
      !hasAutoSelectedRef.current
    ) {
      hasAutoSelectedRef.current = true;
      const rememberedSessionId = readLastSessionId();
      const latest = findLatestSession(sessions);
      const targetSessionId = rememberedSessionId ?? latest?.session.id;
      if (targetSessionId) {
        selectSession(targetSessionId).catch((err) => {
          if (
            rememberedSessionId &&
            latest &&
            latest.session.id !== rememberedSessionId
          ) {
            forgetLastSessionId();
            selectSession(latest.session.id).catch((fallbackErr) =>
              setError(
                fallbackErr instanceof Error
                  ? fallbackErr.message
                  : 'Failed to load session'
              )
            );
            return;
          }
          if (rememberedSessionId) {
            forgetLastSessionId();
          }
          setError(
            err instanceof Error ? err.message : 'Failed to load session'
          );
        });
      }
    }
  }, [isOpen, sessions, sessionId, selectSession, setError]);

  useEffect(() => {
    if (sessionId) {
      rememberLastSessionId(sessionId);
    }
  }, [sessionId]);

  const handleSend = useCallback(
    (
      message: string,
      dagContexts?: DAGContext[],
      model?: string,
      soulId?: string
    ): void => {
      setInitialInputValue(null);
      // sendMessage handles its own error reporting via setError internally
      sendMessage(message, model, dagContexts, soulId).catch(() => {});
    },
    [sendMessage, setInitialInputValue]
  );

  const handleCancel = useCallback((): void => {
    cancelSession().catch((err) =>
      setError(err instanceof Error ? err.message : 'Failed to cancel')
    );
  }, [cancelSession, setError]);

  const handleClearSession = useCallback((): void => {
    // If the user explicitly requests a fresh session before the initial
    // session list finishes loading, suppress the one-time auto-select for
    // this open cycle so the latest session does not get re-selected.
    hasAutoSelectedRef.current = true;
    forgetLastSessionId();
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

  if (!isOpen) return null;

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

  const content = (
    <>
      <AgentChatModalHeader
        sessionId={sessionId}
        totalCost={sessionState?.total_cost}
        isSidebarOpen={sidebarOpen}
        onToggleSidebar={toggleSidebar}
        onClearSession={handleClearSession}
        onClose={closeChat}
        dragHandlers={isMobile ? undefined : dragHandlers}
        isMobile={isMobile}
      />
      <div className="flex flex-1 min-h-0 overflow-hidden relative">
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
        <div className="flex flex-col flex-1 min-w-0 min-h-0">
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
            placeholder="Ask me to create a DAG, run a command..."
            initialValue={initialInputValue}
            hasActiveSession={!!sessionId}
          />
        </div>
      </div>
    </>
  );

  // Mobile: fullscreen
  if (isMobile) {
    return (
      <div
        className={cn(
          'fixed inset-0 z-50',
          'flex flex-col',
          'bg-card dark:bg-zinc-950'
        )}
        style={{
          animation: isClosing
            ? `agent-modal-out ${ANIMATION_CLOSE_DURATION_MS}ms ease-in forwards`
            : `agent-modal-in ${ANIMATION_OPEN_DURATION_MS}ms ease-out`,
        }}
      >
        {content}
      </div>
    );
  }

  // Desktop: resizable/draggable window + delegate panels
  return (
    <>
      <div
        className={cn(
          'fixed z-50',
          'flex flex-col',
          'bg-card dark:bg-zinc-950 border border-border-strong rounded-lg overflow-hidden',
          'shadow-xl'
        )}
        style={{
          right: bounds.right,
          bottom: bounds.bottom,
          width: bounds.width,
          height: bounds.height,
          maxWidth: 'calc(100vw - 32px)',
          maxHeight: 'calc(100vh - 100px)',
          animation: isClosing
            ? `agent-modal-out ${ANIMATION_CLOSE_DURATION_MS}ms ease-in forwards`
            : `agent-modal-in ${ANIMATION_OPEN_DURATION_MS}ms ease-out`,
        }}
      >
        <ResizeHandles resizeHandlers={resizeHandlers} />
        {content}
      </div>
      {delegates.map((d) => (
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
