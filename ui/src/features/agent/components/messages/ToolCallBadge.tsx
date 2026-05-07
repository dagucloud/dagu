import { useMemo, useState } from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';

import { cn } from '@/lib/utils';
import { ToolCall } from '../../types';

function parseToolArguments(jsonString: string): Record<string, unknown> {
  try {
    return JSON.parse(jsonString) as Record<string, unknown>;
  } catch {
    return {};
  }
}

function getStringArg(
  args: Record<string, unknown>,
  key: string
): string | undefined {
  const value = args[key];
  return typeof value === 'string' && value.trim() !== ''
    ? value.trim()
    : undefined;
}

type ToolDisplay = {
  emoji: string;
  label: string;
};

function patchToolDisplay(args: Record<string, unknown>): ToolDisplay {
  switch (getStringArg(args, 'operation')) {
    case 'create':
      return { emoji: '📝', label: 'create DAG' };
    case 'delete':
      return { emoji: '🗑️', label: 'delete DAG' };
    default:
      return { emoji: '✏️', label: 'edit DAG' };
  }
}

function toolDisplay(toolName: string, args: Record<string, unknown>): ToolDisplay {
  switch (toolName) {
    case 'patch':
      return patchToolDisplay(args);
    case 'read':
      return { emoji: '📖', label: 'read file' };
    case 'bash':
      return { emoji: '⌨️', label: 'run command' };
    case 'dag_def_manage':
      return { emoji: '🧩', label: 'inspect DAG' };
    case 'dag_run_manage':
      return { emoji: '📈', label: 'inspect run' };
    case 'ask_user':
      return { emoji: '❓', label: 'ask user' };
    case 'navigate':
      return { emoji: '🧭', label: 'navigate' };
    case 'think':
      return { emoji: '💭', label: 'think' };
    case 'delegate':
      return { emoji: '👥', label: 'delegate' };
    case 'session_search':
      return { emoji: '🔎', label: 'search chats' };
    case 'remote_agent':
      return { emoji: '🌐', label: 'remote agent' };
    case 'list_contexts':
      return { emoji: '🗂️', label: 'list contexts' };
    case 'runbook_manage':
      return { emoji: '📘', label: 'runbook' };
    case 'web_search':
      return { emoji: '🌐', label: 'search web' };
    case 'web_extract':
      return { emoji: '📄', label: 'read web page' };
    default:
      return { emoji: '⚙️', label: toolName };
  }
}

export function ToolCallBadge({
  toolCall,
}: {
  toolCall: ToolCall;
}): React.ReactNode {
  const [expanded, setExpanded] = useState(false);
  const args = useMemo(
    () => parseToolArguments(toolCall.function.arguments),
    [toolCall.function.arguments]
  );
  const display = toolDisplay(toolCall.function.name, args);

  return (
    <span className="inline-flex min-w-0 flex-col">
      <button
        type="button"
        aria-expanded={expanded}
        onClick={() => setExpanded(!expanded)}
        className="inline-flex max-w-full items-center gap-1.5 rounded-sm px-1 py-0.5 text-left text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
      >
        <span aria-hidden="true">{display.emoji}</span>
        <span className="truncate">{display.label}</span>
        <span className="sr-only">: {toolCall.function.name}</span>
        {expanded ? (
          <ChevronDown className="h-3 w-3 shrink-0" aria-hidden="true" />
        ) : (
          <ChevronRight className="h-3 w-3 shrink-0" aria-hidden="true" />
        )}
      </button>
      {expanded && (
        <span className="mt-1 block rounded-sm border border-border/70 bg-muted/30 p-2 text-xs text-foreground">
          <span className="mb-1 block text-[11px] text-muted-foreground">
            {toolCall.function.name}
          </span>
          <pre className="max-h-[180px] overflow-auto whitespace-pre-wrap break-words text-[11px] leading-relaxed">
            {JSON.stringify(args, null, 2)}
          </pre>
        </span>
      )}
    </span>
  );
}

export function ToolCallList({
  toolCalls,
  className,
}: {
  toolCalls: ToolCall[];
  className?: string;
}): React.ReactNode {
  return (
    <div className={cn('flex flex-wrap gap-x-2 gap-y-1', className)}>
      {toolCalls.map((tc) => (
        <ToolCallBadge key={tc.id} toolCall={tc} />
      ))}
    </div>
  );
}
