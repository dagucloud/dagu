import { useCallback, useEffect, useMemo, useRef } from 'react';
import { Loader2, Terminal } from 'lucide-react';
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
  onPromptRespond?: (response: UserPromptResponse, displayValue: string) => Promise<void>;
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
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const isNearBottomRef = useRef(true);

  const handleScroll = useCallback(() => {
    const el = containerRef.current;
    if (!el) return;
    const threshold = 80;
    isNearBottomRef.current =
      el.scrollHeight - el.scrollTop - el.clientHeight < threshold;
  }, []);

  useEffect(() => {
    if (isNearBottomRef.current) {
      messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
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

  const toolResultsByCallId = useMemo(() => {
    const results = new Map<string, NonNullable<Message['tool_results']>[number]>();
    for (const msg of messages) {
      if (msg.tool_results) {
        for (const tr of msg.tool_results) {
          if (tr.tool_call_id) results.set(tr.tool_call_id, tr);
        }
      }
    }
    return results;
  }, [messages]);

  if (messages.length === 0 && !pendingUserMessage) {
    return (
      <div className="flex-1 flex items-center justify-center text-muted-foreground p-4 bg-popover">
        <div className="text-center">
          <Terminal className="h-8 w-8 mx-auto mb-2 opacity-30" />
          <p className="text-xs text-muted-foreground">
            Ask the agent to create DAGs, run commands, or help with workflows
          </p>
        </div>
      </div>
    );
  }

  const hasPendingPrompt = messages.some(
    (m) => m.type === 'user_prompt' && m.user_prompt && !answeredPrompts?.[m.user_prompt.prompt_id]
  );

  return (
    <div ref={containerRef} onScroll={handleScroll} className="flex-1 overflow-y-auto p-2 space-y-2 font-mono text-xs bg-popover">
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
          toolResultsByCallId={toolResultsByCallId}
        />
      ))}
      {pendingUserMessage && (
        <UserMessage content={pendingUserMessage} isPending />
      )}
      {isWorking && !hasPendingPrompt && (
        <div className="flex items-center gap-1.5 text-orange-600 dark:text-orange-400 pl-1">
          <Loader2 className="h-3 w-3 animate-spin" />
          <span>processing...</span>
        </div>
      )}
      <div ref={messagesEndRef} />
    </div>
  );
}

interface MessageItemProps {
  message: Message;
  messages: Message[];
  messageIndex: number;
  onPromptRespond?: (response: UserPromptResponse, displayValue: string) => Promise<void>;
  answeredPrompts?: Record<string, string>;
  delegateStatuses?: Record<string, DelegateInfo>;
  onOpenDelegate?: (id: string) => void;
  completedToolCallIds?: Set<string>;
  toolResultsByCallId?: Map<string, NonNullable<Message['tool_results']>[number]>;
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
  toolResultsByCallId,
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
        return <ToolResultMessage toolResults={message.tool_results} />;
      }
      return <UserMessage content={message.content ?? ''} />;
    case 'assistant':
      return (
        <AssistantMessage
          content={message.content ?? ''}
          toolCalls={message.tool_calls}
          usage={message.usage}
          cost={message.cost}
          delegateStatuses={delegateStatuses}
          onOpenDelegate={onOpenDelegate}
          completedToolCallIds={completedToolCallIds}
          delegateIdsForToolCalls={delegateIdsForToolCalls}
          toolResultsByCallId={toolResultsByCallId}
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
            isAnswered={answeredPrompts?.[message.user_prompt.prompt_id] !== undefined}
            answeredValue={answeredPrompts?.[message.user_prompt.prompt_id]}
          />
        );
      }
      return (
        <UserPromptMessage
          prompt={message.user_prompt}
          onRespond={onPromptRespond}
          isAnswered={answeredPrompts?.[message.user_prompt.prompt_id] !== undefined}
          answeredValue={answeredPrompts?.[message.user_prompt.prompt_id]}
        />
      );
    default:
      return null;
  }
}
