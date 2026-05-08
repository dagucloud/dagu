import type React from 'react';
import { useState } from 'react';
import { CheckCircle, ChevronDown, ChevronRight, XCircle } from 'lucide-react';

import { Badge } from '@/components/ui/badge';

import { cn } from '@/lib/utils';
import { ToolResult } from '../../types';
import { TOOL_RESULT_PREVIEW_LENGTH } from '../../constants';

function truncateContent(content: string, maxLength: number): string {
  if (content.length <= maxLength) return content;
  return content.substring(0, maxLength) + '...';
}

function ToolResultItem({ result }: { result: ToolResult }): React.ReactNode {
  const [expanded, setExpanded] = useState(false);
  const content = result.content ?? '';
  const preview = truncateContent(content, TOOL_RESULT_PREVIEW_LENGTH);

  const StatusIcon = result.is_error ? XCircle : CheckCircle;
  const statusColor = result.is_error ? 'text-red-500' : 'text-green-500';
  const borderStyle = result.is_error
    ? 'border-destructive/20 bg-destructive/5'
    : 'border-success/20 bg-success/5';

  return (
    <div
      className={cn('overflow-hidden rounded-md border text-xs', borderStyle)}
    >
      <button
        type="button"
        aria-expanded={expanded}
        onClick={() => setExpanded(!expanded)}
        className="flex w-full items-center gap-2 px-2.5 py-2 text-left transition-colors hover:bg-muted/70"
      >
        <StatusIcon
          className={cn('h-3.5 w-3.5 flex-shrink-0', statusColor)}
          aria-hidden="true"
        />
        <span className="min-w-0 flex-1 truncate font-mono">
          {expanded ? 'Result' : preview}
        </span>
        <Badge
          variant={result.is_error ? 'error' : 'success'}
          className="shrink-0"
        >
          {result.is_error ? 'Error' : 'OK'}
        </Badge>
        {expanded ? (
          <ChevronDown
            className="h-3.5 w-3.5 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
        ) : (
          <ChevronRight
            className="h-3.5 w-3.5 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
        )}
      </button>
      {expanded && (
        <div className="border-t border-border bg-card px-2.5 py-2 dark:bg-surface">
          <pre className="text-xs overflow-x-auto whitespace-pre-wrap break-words max-h-[200px] overflow-y-auto">
            {content}
          </pre>
        </div>
      )}
    </div>
  );
}

export function ToolResultMessage({
  toolResults,
}: {
  toolResults: ToolResult[];
}): React.ReactNode {
  return (
    <div className="ml-9 space-y-2">
      {toolResults.map((tr) => (
        <ToolResultItem key={tr.tool_call_id} result={tr} />
      ))}
    </div>
  );
}
