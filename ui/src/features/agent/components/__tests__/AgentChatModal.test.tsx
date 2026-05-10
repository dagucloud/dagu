// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { AgentChatModal } from '../AgentChatModal';
import { LAST_SESSION_STORAGE_KEY } from '../../constants';
import type { SessionWithState } from '../../types';

const useAgentChatMock = vi.fn();
const useAgentChatContextMock = vi.fn();

vi.mock('../../hooks/useAgentChat', () => ({
  useAgentChat: () => useAgentChatMock(),
}));

vi.mock('../../context/AgentChatContext', () => ({
  useAgentChatContext: () => useAgentChatContextMock(),
}));

vi.mock('@/hooks/useIsMobile', () => ({
  useIsMobile: () => false,
}));

vi.mock('../../hooks/useResizableDraggable', () => ({
  useResizableDraggable: () => ({
    bounds: {
      right: 16,
      bottom: 16,
      width: 560,
      height: 640,
    },
    dragHandlers: {},
    resizeHandlers: {},
  }),
}));

vi.mock('../AgentChatModalHeader', () => ({
  AgentChatModalHeader: ({
    onClearSession,
  }: {
    onClearSession: () => void;
  }) => (
    <button type="button" onClick={onClearSession}>
      New session
    </button>
  ),
}));

vi.mock('../ChatMessages', () => ({
  ChatMessages: () => <div>Messages</div>,
}));

vi.mock('../ChatInput', () => ({
  ChatInput: () => <div>Input</div>,
}));

vi.mock('../SessionSidebar', () => ({
  SessionSidebar: ({
    onSelectSession,
  }: {
    onSelectSession: (sessionId: string) => void;
  }) => (
    <button type="button" onClick={() => onSelectSession('new')}>
      Sidebar new session
    </button>
  ),
}));

vi.mock('../DelegatePanel', () => ({
  DelegatePanel: () => null,
}));

vi.mock('../ResizeHandles', () => ({
  ResizeHandles: () => null,
}));

function makeSession(id: string, updatedAt: string): SessionWithState {
  return {
    session: {
      id,
      user_id: 'user-1',
      title: `Session ${id}`,
      created_at: '2026-04-05T00:00:00Z',
      updated_at: updatedAt,
      parent_session_id: undefined,
      delegate_task: undefined,
    },
    working: false,
    has_pending_prompt: false,
    model: 'gpt-test',
    total_cost: 0,
  };
}

describe('AgentChatModal', () => {
  let chatState: ReturnType<typeof useAgentChatMock>;
  let clearSessionMock: ReturnType<typeof vi.fn>;
  let fetchSessionsMock: ReturnType<typeof vi.fn>;
  let selectSessionMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    localStorage.clear();

    clearSessionMock = vi.fn();
    fetchSessionsMock = vi.fn();
    selectSessionMock = vi.fn().mockResolvedValue(undefined);

    chatState = {
      sessionId: null,
      messages: [],
      pendingUserMessage: null,
      sessionState: null,
      sessions: [],
      hasMoreSessions: false,
      isLoadingMore: false,
      isWorking: false,
      error: null,
      answeredPrompts: {},
      setError: vi.fn(),
      sendMessage: vi.fn().mockResolvedValue(undefined),
      cancelSession: vi.fn().mockResolvedValue(undefined),
      clearSession: clearSessionMock,
      clearError: vi.fn(),
      fetchSessions: fetchSessionsMock,
      loadMoreSessions: vi.fn(),
      selectSession: selectSessionMock,
      respondToPrompt: vi.fn().mockResolvedValue(undefined),
      delegates: [],
      delegateStatuses: {},
      delegateMessages: {},
      bringToFront: vi.fn(),
      reopenDelegate: vi.fn(),
      removeDelegate: vi.fn(),
    };

    useAgentChatMock.mockImplementation(() => chatState);
    useAgentChatContextMock.mockReturnValue({
      isOpen: true,
      isClosing: false,
      closeChat: vi.fn(),
      initialInputValue: null,
      setInitialInputValue: vi.fn(),
    });
  });

  afterEach(() => {
    cleanup();
    localStorage.clear();
  });

  it('auto-selects the latest session on first open when the user has not chosen a fresh session', async () => {
    const { rerender } = render(<AgentChatModal />);

    expect(fetchSessionsMock).toHaveBeenCalledTimes(1);

    chatState.sessions = [
      makeSession('sess-1', '2026-04-05T00:00:00Z'),
      makeSession('sess-2', '2026-04-05T01:00:00Z'),
    ];

    rerender(<AgentChatModal />);

    await waitFor(() => {
      expect(selectSessionMock).toHaveBeenCalledWith('sess-2');
    });
  });

  it('auto-selects the remembered session instead of the latest session', async () => {
    localStorage.setItem(LAST_SESSION_STORAGE_KEY, 'sess-1');

    const { rerender } = render(<AgentChatModal />);

    chatState.sessions = [
      makeSession('sess-1', '2026-04-05T00:00:00Z'),
      makeSession('sess-2', '2026-04-05T01:00:00Z'),
    ];

    rerender(<AgentChatModal />);

    await waitFor(() => {
      expect(selectSessionMock).toHaveBeenCalledWith('sess-1');
    });
    expect(selectSessionMock).not.toHaveBeenCalledWith('sess-2');
  });

  it('remembers the current opened session in browser storage', () => {
    const { rerender } = render(<AgentChatModal />);

    chatState.sessionId = 'sess-1';
    rerender(<AgentChatModal />);

    expect(localStorage.getItem(LAST_SESSION_STORAGE_KEY)).toBe('sess-1');
  });

  it('does not auto-select a previous session after the user clicks new session before sessions finish loading', () => {
    const { rerender } = render(<AgentChatModal />);

    fireEvent.click(screen.getByRole('button', { name: 'New session' }));

    expect(clearSessionMock).toHaveBeenCalledTimes(1);

    chatState.sessions = [makeSession('sess-1', '2026-04-05T01:00:00Z')];

    rerender(<AgentChatModal />);

    expect(selectSessionMock).not.toHaveBeenCalled();
  });

  it('clears the session when the sidebar requests a new session', () => {
    render(<AgentChatModal />);

    fireEvent.click(screen.getByRole('button', { name: 'Sidebar new session' }));

    expect(clearSessionMock).toHaveBeenCalledTimes(1);
    expect(selectSessionMock).not.toHaveBeenCalled();
  });
});
