import { DelegateInfo, TokenUsage, ToolCall, ToolResult } from '../../types';
import { formatCost } from '../../utils/formatCost';
import { ToolCallList } from './ToolCallBadge';
import { SubAgentChips } from './SubAgentChips';

export function AssistantMessage({
  content,
  toolCalls,
  usage,
  cost,
  delegateStatuses,
  onOpenDelegate,
  completedToolCallIds,
  delegateIdsForToolCalls,
  toolResultsByCallId,
}: {
  content: string;
  toolCalls?: ToolCall[];
  usage?: TokenUsage;
  cost?: number;
  delegateStatuses?: Record<string, DelegateInfo>;
  onOpenDelegate?: (id: string) => void;
  completedToolCallIds?: Set<string>;
  delegateIdsForToolCalls?: Map<string, string[]>;
  toolResultsByCallId?: Map<string, ToolResult>;
}): React.ReactNode {
  const delegateCalls = toolCalls?.filter((tc) => tc.function.name === 'delegate') ?? [];
  const otherCalls = toolCalls?.filter((tc) => tc.function.name !== 'delegate') ?? [];

  return (
    <div className="pl-1 space-y-1">
      {content && (
        <p className="whitespace-pre-wrap break-words text-foreground/90 pl-4">
          {content}
        </p>
      )}
      {otherCalls.length > 0 && (
        <ToolCallList toolCalls={otherCalls} className="pl-4" toolResultsByCallId={toolResultsByCallId} />
      )}
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
      {usage && usage.total_tokens > 0 && (
        <p className="text-[10px] text-muted-foreground/60 pl-4">
          {usage.total_tokens.toLocaleString()} tokens
          {cost != null && cost > 0 && ` · ${formatCost(cost)}`}
        </p>
      )}
    </div>
  );
}
