import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import type { Dispatch, ReactNode, SetStateAction } from 'react';

import { ANIMATION_CLOSE_DURATION_MS } from '../constants';
import type { Message, SessionState, SessionWithState } from '../types';

interface AgentChatContextType {
  isOpen: boolean;
  isClosing: boolean;
  sessionId: string | null;
  messages: Message[];
  pendingUserMessage: string | null;
  sessionState: SessionState | null;
  sessions: SessionWithState[];
  hasMoreSessions: boolean;
  sessionPage: number;
  sessionCursor: string | null;
  openChat: () => void;
  closeChat: () => void;
  toggleChat: () => void;
  setSessionId: (id: string | null) => void;
  setMessages: Dispatch<SetStateAction<Message[]>>;
  setSessionState: (state: SessionState | null) => void;
  setSessions: Dispatch<SetStateAction<SessionWithState[]>>;
  appendSessions: (sessions: SessionWithState[]) => void;
  setHasMoreSessions: (has: boolean) => void;
  setSessionPage: Dispatch<SetStateAction<number>>;
  setSessionCursor: (cursor: string | null) => void;
  addMessage: (message: Message) => void;
  setPendingUserMessage: (message: string | null) => void;
  clearSession: () => void;
  initialInputValue: string | null;
  setInitialInputValue: (value: string | null) => void;
}

interface AgentChatProviderProps {
  children: ReactNode;
}

const AgentChatContext = createContext<AgentChatContextType | null>(null);

export function AgentChatProvider({ children }: AgentChatProviderProps): ReactNode {
  const [isOpen, setIsOpen] = useState(false);
  const [isClosing, setIsClosing] = useState(false);
  const closeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    return () => { if (closeTimerRef.current) clearTimeout(closeTimerRef.current); };
  }, []);
  const [sessionId, setSessionId] = useState<string | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [pendingUserMessage, setPendingUserMessage] = useState<string | null>(null);
  const [sessionState, setSessionState] =
    useState<SessionState | null>(null);
  const [sessions, setSessions] = useState<SessionWithState[]>([]);
  const [hasMoreSessions, setHasMoreSessions] = useState(true);
  const [sessionPage, setSessionPage] = useState(1);
  const [sessionCursor, setSessionCursor] = useState<string | null>(null);
  const [initialInputValue, setInitialInputValue] = useState<string | null>(null);

  const appendSessions = useCallback((newSessions: SessionWithState[]) => {
    setSessions(prev => [...prev, ...newSessions]);
  }, []);

  const openChat = useCallback(() => {
    if (closeTimerRef.current) {
      clearTimeout(closeTimerRef.current);
      closeTimerRef.current = null;
    }
    setIsClosing(false);
    setIsOpen(true);
  }, []);

  const closeChat = useCallback(() => {
    if (!isOpen || isClosing) return;
    setIsClosing(true);
    closeTimerRef.current = setTimeout(() => {
      setIsClosing(false);
      setIsOpen(false);
      closeTimerRef.current = null;
    }, ANIMATION_CLOSE_DURATION_MS);
  }, [isOpen, isClosing]);

  const toggleChat = useCallback(() => {
    if (isOpen) {
      closeChat();
    } else {
      openChat();
    }
  }, [isOpen, closeChat, openChat]);

  const addMessage = useCallback((message: Message) => {
    // When a user message arrives from SSE, clear the pending message
    if (message.type === 'user') {
      setPendingUserMessage(null);
    }

    setMessages((prev) => {
      // Check for exact ID match - update existing
      const existingIndex = prev.findIndex((m) => m.id === message.id);
      if (existingIndex !== -1) {
        const updated = [...prev];
        updated[existingIndex] = message;
        return updated;
      }

      // Add as new message
      return [...prev, message];
    });
  }, []);

  const clearSession = useCallback(() => {
    setSessionId(null);
    setMessages([]);
    setPendingUserMessage(null);
    setSessionState(null);
  }, []);

  const value = useMemo<AgentChatContextType>(() => ({
    isOpen,
    isClosing,
    sessionId,
    messages,
    pendingUserMessage,
    sessionState,
    sessions,
    hasMoreSessions,
    sessionPage,
    sessionCursor,
    openChat,
    closeChat,
    toggleChat,
    setSessionId,
    setMessages,
    setSessionState,
    setSessions,
    appendSessions,
    setHasMoreSessions,
    setSessionPage,
    setSessionCursor,
    addMessage,
    setPendingUserMessage,
    clearSession,
    initialInputValue,
    setInitialInputValue,
  }), [
    isOpen, isClosing, sessionId, messages, pendingUserMessage,
    sessionState, sessions, hasMoreSessions, sessionPage, sessionCursor,
    openChat, closeChat, toggleChat,
    setSessionId, setMessages, setSessionState, setSessions,
    appendSessions, setHasMoreSessions, setSessionPage, setSessionCursor,
    addMessage, setPendingUserMessage, clearSession,
    initialInputValue,
  ]);

  return (
    <AgentChatContext.Provider value={value}>
      {children}
    </AgentChatContext.Provider>
  );
}

export function useAgentChatContext(): AgentChatContextType {
  const context = useContext(AgentChatContext);
  if (!context) {
    throw new Error(
      'useAgentChatContext must be used within an AgentChatProvider'
    );
  }
  return context;
}
