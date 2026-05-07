import { useCallback, useLayoutEffect, useMemo, useRef } from 'react';
import { Loader2 } from 'lucide-react';

import { Badge } from '@/components/ui/badge';

import { DelegateInfo, Message, UserPromptResponse } from '../types';
import { CommandApprovalMessage } from './CommandApprovalMessage';
import { UserPromptMessage } from './UserPromptMessage';
import {
  UserMessage,
  AssistantMessage,
  ErrorMessage,
  UIActionMessage,
  ToolResultMessage,
} from './messages';

interface ChatMessagesProps {
  messages: Message[];
  pendingUserMessage: string | null;
  isWorking: boolean;
  onPromptRespond?: (
    response: UserPromptResponse,
    displayValue: string
  ) => Promise<void>;
  answeredPrompts?: Record<string, string>;
  delegateStatuses?: Record<string, DelegateInfo>;
  onOpenDelegate?: (id: string) => void;
}

export function ChatMessages({
  messages,
  pendingUserMessage,
  isWorking,
  onPromptRespond,
  answeredPrompts,
  delegateStatuses,
  onOpenDelegate,
}: ChatMessagesProps): React.ReactNode {
  const containerRef = useRef<HTMLDivElement>(null);
  const isNearBottomRef = useRef(true);

  const handleScroll = useCallback(() => {
    const el = containerRef.current;
    if (!el) return;
    const threshold = 80;
    isNearBottomRef.current =
      el.scrollHeight - el.scrollTop - el.clientHeight < threshold;
  }, []);

  useLayoutEffect(() => {
    if (isNearBottomRef.current) {
      const el = containerRef.current;
      if (el) {
        el.scrollTop = el.scrollHeight;
      }
    }
  }, [messages, pendingUserMessage, isWorking]);

  const completedToolCallIds = useMemo(() => {
    const ids = new Set<string>();
    for (const msg of messages) {
      if (msg.tool_results) {
        for (const tr of msg.tool_results) {
          const id = tr.tool_call_id;
          if (id) ids.add(id);
        }
      }
    }
    return ids;
  }, [messages]);

  const toolCallIds = useMemo(() => {
    const ids = new Set<string>();
    for (const msg of messages) {
      if (msg.tool_calls) {
        for (const tc of msg.tool_calls) {
          ids.add(tc.id);
        }
      }
    }
    return ids;
  }, [messages]);

  if (messages.length === 0 && !pendingUserMessage) {
    return (
      <div className="min-h-0 flex-1 overflow-y-auto bg-background">
        <div className="flex h-full items-center justify-center p-6 text-center">
          <div className="max-w-xs">
            <h3 className="text-sm font-medium text-foreground">
              Agent session ready
            </h3>
            <p className="mt-1 text-xs text-muted-foreground">
              Messages will appear here.
            </p>
          </div>
        </div>
      </div>
    );
  }

  const hasPendingPrompt = messages.some(
    (m) =>
      m.type === 'user_prompt' &&
      m.user_prompt &&
      !answeredPrompts?.[m.user_prompt.prompt_id]
  );

  return (
    <div
      ref={containerRef}
      onScroll={handleScroll}
      className="min-h-0 flex-1 overflow-y-auto bg-background px-3 py-4"
    >
      <div className="space-y-3">
        {messages.map((message, idx) => (
          <MessageItem
            key={message.id}
            message={message}
            messages={messages}
            messageIndex={idx}
            onPromptRespond={onPromptRespond}
            answeredPrompts={answeredPrompts}
            delegateStatuses={delegateStatuses}
            onOpenDelegate={onOpenDelegate}
            completedToolCallIds={completedToolCallIds}
            toolCallIds={toolCallIds}
          />
        ))}
        {pendingUserMessage && (
          <UserMessage content={pendingUserMessage} isPending />
        )}
        {isWorking && !hasPendingPrompt && (
          <div className="flex items-center gap-2">
            <Badge variant="warning" className="h-6 px-2">
              <Loader2 className="h-3 w-3 animate-spin" aria-hidden="true" />
              Agent is working
            </Badge>
          </div>
        )}
      </div>
    </div>
  );
}

interface MessageItemProps {
  message: Message;
  messages: Message[];
  messageIndex: number;
  onPromptRespond?: (
    response: UserPromptResponse,
    displayValue: string
  ) => Promise<void>;
  answeredPrompts?: Record<string, string>;
  delegateStatuses?: Record<string, DelegateInfo>;
  onOpenDelegate?: (id: string) => void;
  completedToolCallIds?: Set<string>;
  toolCallIds?: Set<string>;
}

function MessageItem({
  message,
  messages,
  messageIndex,
  onPromptRespond,
  answeredPrompts,
  delegateStatuses,
  onOpenDelegate,
  completedToolCallIds,
  toolCallIds,
}: MessageItemProps): React.ReactNode {
  const delegateIdsForToolCalls = useMemo(() => {
    const map = new Map<string, string[]>();
    if (message.type !== 'assistant' || !message.tool_calls) return map;
    for (let i = messageIndex + 1; i < messages.length; i++) {
      const m = messages[i]!;
      if (m.delegate_ids && m.delegate_ids.length > 0 && m.tool_results) {
        for (const tr of m.tool_results) {
          map.set(tr.tool_call_id, m.delegate_ids!);
        }
      }
    }
    return map;
  }, [message, messages, messageIndex]);

  switch (message.type) {
    case 'user':
      if (message.tool_results && message.tool_results.length > 0) {
        if (message.delegate_ids && message.delegate_ids.length > 0) {
          return null;
        }
        const unpairedResults = message.tool_results.filter(
          (result) => !toolCallIds?.has(result.tool_call_id)
        );
        if (unpairedResults.length === 0) {
          return null;
        }
        return <ToolResultMessage toolResults={unpairedResults} />;
      }
      return <UserMessage content={message.content ?? ''} />;
    case 'assistant':
      return (
        <AssistantMessage
          content={message.content ?? ''}
          toolCalls={message.tool_calls}
          delegateStatuses={delegateStatuses}
          onOpenDelegate={onOpenDelegate}
          completedToolCallIds={completedToolCallIds}
          delegateIdsForToolCalls={delegateIdsForToolCalls}
        />
      );
    case 'error':
      return <ErrorMessage content={message.content ?? ''} />;
    case 'ui_action':
      return <UIActionMessage action={message.ui_action} />;
    case 'user_prompt':
      if (!message.user_prompt || !onPromptRespond) return null;
      if (message.user_prompt.prompt_type === 'command_approval') {
        return (
          <CommandApprovalMessage
            prompt={message.user_prompt}
            onRespond={onPromptRespond}
            isAnswered={
              answeredPrompts?.[message.user_prompt.prompt_id] !== undefined
            }
            answeredValue={answeredPrompts?.[message.user_prompt.prompt_id]}
          />
        );
      }
      return (
        <UserPromptMessage
          prompt={message.user_prompt}
          onRespond={onPromptRespond}
          isAnswered={
            answeredPrompts?.[message.user_prompt.prompt_id] !== undefined
          }
          answeredValue={answeredPrompts?.[message.user_prompt.prompt_id]}
        />
      );
    default:
      return null;
  }
}
