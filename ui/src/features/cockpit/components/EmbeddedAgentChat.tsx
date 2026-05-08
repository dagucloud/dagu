// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { useCallback, useEffect, useRef, useState } from 'react';
import { AlertCircle, MessageSquare, PanelLeftClose, PanelLeftOpen, X } from 'lucide-react';
import { cn } from '@/lib/utils';
import { useAgentChatContext } from '@/features/agent/context/AgentChatContext';
import { useAgentChat } from '@/features/agent/hooks/useAgentChat';
import { ChatMessages } from '@/features/agent/components/ChatMessages';
import { ChatInput } from '@/features/agent/components/ChatInput';
import { SessionSidebar } from '@/features/agent/components/SessionSidebar';
import { SESSION_SIDEBAR_STORAGE_KEY } from '@/features/agent/constants';
import type { SessionWithState, DAGContext } from '@/features/agent/types';

function findLatestSession(sessions: SessionWithState[]): SessionWithState | null {
  if (sessions.length === 0) return null;
  let latest: SessionWithState | null = null;
  for (const sess of sessions) {
    if (sess.session.parent_session_id) continue;
    if (!latest || new Date(sess.session.updated_at) > new Date(latest.session.updated_at)) {
      latest = sess;
    }
  }
  return latest;
}

export function EmbeddedAgentChat(): React.ReactElement {
  const { initialInputValue, setInitialInputValue } = useAgentChatContext();
  const {
    sessionId,
    messages,
    pendingUserMessage,
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
    delegateStatuses,
  } = useAgentChat();

  const [sidebarOpen, setSidebarOpen] = useState(() => {
    try {
      const saved = localStorage.getItem(SESSION_SIDEBAR_STORAGE_KEY);
      return saved !== 'false';
    } catch { return true; }
  });

  const toggleSidebar = useCallback(() => {
    setSidebarOpen((prev) => {
      const next = !prev;
      try { localStorage.setItem(SESSION_SIDEBAR_STORAGE_KEY, String(next)); }
      catch { /* ignore */ }
      return next;
    });
  }, []);

  const hasAutoSelectedRef = useRef(false);

  useEffect(() => {
    hasAutoSelectedRef.current = false;
    fetchSessions();
  }, [fetchSessions]);

  useEffect(() => {
    if (sessions.length > 0 && !sessionId && !hasAutoSelectedRef.current) {
      hasAutoSelectedRef.current = true;
      const latest = findLatestSession(sessions);
      if (latest) {
        selectSession(latest.session.id).catch((err) =>
          setError(err instanceof Error ? err.message : 'Failed to load session')
        );
      }
    }
  }, [sessions, sessionId, selectSession, setError]);

  const handleSend = useCallback(
    (message: string, dagContexts?: DAGContext[], model?: string, soulId?: string): void => {
      setInitialInputValue(null);
      sendMessage(message, model, dagContexts, soulId).catch(() => {});
    },
    [sendMessage, setInitialInputValue]
  );

  const handleCancel = useCallback((): void => {
    cancelSession().catch((err) =>
      setError(err instanceof Error ? err.message : 'Failed to cancel')
    );
  }, [cancelSession, setError]);

  const handleSelectSession = useCallback(
    (value: string): void => {
      if (value === 'new') {
        clearSession();
        return;
      }
      selectSession(value).catch((err) =>
        setError(err instanceof Error ? err.message : 'Failed to select session')
      );
    },
    [selectSession, clearSession, setError]
  );

  return (
    <div className="flex flex-col h-full bg-card">
      {/* Header */}
      <div className="flex items-center gap-2 px-3 py-2 border-b border-border">
        <button
          type="button"
          aria-label={sidebarOpen ? 'Collapse session sidebar' : 'Expand session sidebar'}
          onClick={toggleSidebar}
          className="text-muted-foreground hover:text-foreground p-0.5"
        >
          {sidebarOpen ? <PanelLeftClose size={16} /> : <PanelLeftOpen size={16} />}
        </button>
        <MessageSquare size={14} className="text-muted-foreground" />
        <span className="text-xs font-medium">Agent</span>
        {sessionId && (
          <button
            onClick={clearSession}
            className="ml-auto text-[11px] text-muted-foreground hover:text-foreground"
          >
            New
          </button>
        )}
      </div>

      <div className="flex flex-1 min-h-0 overflow-hidden">
        <SessionSidebar
          isOpen={sidebarOpen}
          sessions={sessions}
          activeSessionId={sessionId}
          onSelectSession={handleSelectSession}
          onClose={toggleSidebar}
          onLoadMore={loadMoreSessions}
          hasMore={hasMoreSessions}
          isLoadingMore={isLoadingMore}
        />
        <div className="flex flex-col flex-1 min-w-0 min-h-0">
          {error && (
            <div className="mx-3 mt-2 p-2 bg-destructive/10 border border-destructive/20 rounded-md flex items-start gap-2">
              <AlertCircle className="h-4 w-4 text-destructive flex-shrink-0 mt-0.5" />
              <p className="flex-1 text-xs text-destructive">{error}</p>
              <button
                type="button"
                aria-label="Dismiss error"
                onClick={clearError}
                className="text-destructive/60 hover:text-destructive flex-shrink-0"
              >
                <X className="h-3 w-3" />
              </button>
            </div>
          )}
          <ChatMessages
            messages={messages}
            pendingUserMessage={pendingUserMessage}
            isWorking={isWorking}
            onPromptRespond={respondToPrompt}
            answeredPrompts={answeredPrompts}
            delegateStatuses={delegateStatuses}
            onOpenDelegate={undefined}
          />
          <ChatInput
            onSend={handleSend}
            onCancel={handleCancel}
            isWorking={isWorking}
            placeholder="Ask the agent..."
            initialValue={initialInputValue}
            hasActiveSession={!!sessionId}
          />
        </div>
      </div>
    </div>
  );
}
