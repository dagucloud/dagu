import { Bot } from 'lucide-react';

import { Markdown } from '@/components/ui/markdown';

import { DelegateInfo, ToolCall } from '../../types';
import { ToolCallList } from './ToolCallBadge';
import { SubAgentChips } from './SubAgentChips';

export function AssistantMessage({
  content,
  toolCalls,
  delegateStatuses,
  onOpenDelegate,
  completedToolCallIds,
  delegateIdsForToolCalls,
}: {
  content: string;
  toolCalls?: ToolCall[];
  delegateStatuses?: Record<string, DelegateInfo>;
  onOpenDelegate?: (id: string) => void;
  completedToolCallIds?: Set<string>;
  delegateIdsForToolCalls?: Map<string, string[]>;
}): React.ReactNode {
  const delegateCalls =
    toolCalls?.filter((tc) => tc.function.name === 'delegate') ?? [];
  const otherCalls =
    toolCalls?.filter((tc) => tc.function.name !== 'delegate') ?? [];

  return (
    <div className="flex items-start gap-2 pr-2">
      <div className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md border border-border bg-muted text-muted-foreground">
        <Bot className="h-4 w-4" aria-hidden="true" />
      </div>
      <div className="min-w-0 flex-1 space-y-2">
        {content && (
          <div className="rounded-md border border-border bg-card px-3 py-2 text-card-foreground shadow-sm">
            <Markdown
              content={content}
              className="text-sm prose-p:my-0 prose-pre:my-2 prose-code:text-[0.8125rem]"
            />
          </div>
        )}
        {otherCalls.length > 0 && <ToolCallList toolCalls={otherCalls} />}
        {delegateCalls.map((tc) => (
          <SubAgentChips
            key={tc.id}
            toolCall={tc}
            delegateStatuses={delegateStatuses}
            onOpenDelegate={onOpenDelegate}
            isCompleted={completedToolCallIds?.has(tc.id) ?? false}
            delegateIds={delegateIdsForToolCalls?.get(tc.id)}
          />
        ))}
      </div>
    </div>
  );
}
