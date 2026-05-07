import { FilePlus, FileX } from 'lucide-react';
import { useUserPreferences } from '@/contexts/UserPreference';
import { cn } from '@/lib/utils';
import type { PatchToolInput } from '../../types';
import { JsonPatchViewer, type PatchViewStatus } from '../JsonPatchViewer';
import { DefaultToolViewer } from './DefaultToolViewer';
import type { ToolViewerProps } from './index';

function patchStatus(toolResult: ToolViewerProps['toolResult']): PatchViewStatus {
  if (!toolResult) return 'proposed';
  return toolResult.is_error ? 'failed' : 'applied';
}

const STATUS_LABELS: Record<PatchViewStatus, string> = {
  proposed: 'Proposed patch',
  applied: 'Patch applied',
  failed: 'Patch failed',
};

const STATUS_CLASSES: Record<PatchViewStatus, string> = {
  proposed: 'text-muted-foreground',
  applied: 'text-green-600 dark:text-green-400',
  failed: 'text-red-600 dark:text-red-400',
};

function splitPatchPreviewLines(value: string): string[] {
  const lines = value.split('\n');
  return lines[lines.length - 1] === '' ? lines.slice(0, -1) : lines;
}

export function PatchToolViewer({ args, toolName, toolResult }: ToolViewerProps): React.ReactNode {
  const { preferences } = useUserPreferences();
  const isDark = preferences.theme === 'dark';
  const { path, operation, content, old_string, new_string, anchor } = args as unknown as PatchToolInput;
  const status = patchStatus(toolResult);

  // Replace operation with old_string and new_string - use existing JsonPatchViewer
  if (old_string !== undefined && new_string !== undefined) {
    return <JsonPatchViewer patch={{ path, old_string, new_string }} status={status} />;
  }

  // Append and insert operations - show added content without implying an applied diff
  if ((operation === 'append' || operation === 'insert_before' || operation === 'insert_after') && content !== undefined) {
    const lines = splitPatchPreviewLines(content);
    const anchorLines = anchor ? splitPatchPreviewLines(anchor) : [];
    const filename = path?.split('/').pop() || path;

    return (
      <div className={cn(
        'rounded border overflow-hidden text-xs font-mono',
        status === 'failed' ? 'border-red-500/40' : 'border-border'
      )}>
        <div className="flex items-center gap-2 px-2 py-1 bg-muted border-b border-border text-muted-foreground">
          <FilePlus className="h-3 w-3 text-green-600 dark:text-green-400" />
          <span className="truncate" title={path}>{filename}</span>
          <span className={cn('ml-auto', STATUS_CLASSES[status])}>{STATUS_LABELS[status]}</span>
          <span className="text-green-600 dark:text-green-400">+{lines.length}</span>
        </div>
        <div className="max-h-[300px] overflow-auto">
          {operation === 'insert_after' && anchorLines.map((line, idx) => (
            <DiffPreviewLine key={`anchor-before-${idx}`} line={line} type="context" isDark={isDark} />
          ))}
          {lines.map((line, idx) => (
            <DiffPreviewLine key={`content-${idx}`} line={line} type="addition" isDark={isDark} />
          ))}
          {operation === 'insert_before' && anchorLines.map((line, idx) => (
            <DiffPreviewLine key={`anchor-after-${idx}`} line={line} type="context" isDark={isDark} />
          ))}
        </div>
      </div>
    );
  }

  // Create operation - show file creation with content preview (all additions)
  if (operation === 'create' && content !== undefined) {
    const lines = splitPatchPreviewLines(content);
    const filename = path?.split('/').pop() || path;

    return (
      <div className={cn(
        'rounded border overflow-hidden text-xs font-mono',
        status === 'failed' ? 'border-red-500/40' : 'border-border'
      )}>
        <div className="flex items-center gap-2 px-2 py-1 bg-muted border-b border-border text-muted-foreground">
          <FilePlus className="h-3 w-3 text-green-600 dark:text-green-400" />
          <span className="truncate" title={path}>{filename}</span>
          <span className={cn('ml-auto', STATUS_CLASSES[status])}>{STATUS_LABELS[status]}</span>
          <span className="text-green-600 dark:text-green-400">+{lines.length}</span>
        </div>
        <div className="max-h-[300px] overflow-auto">
          {lines.map((line, idx) => (
            <div
              key={idx}
              className="px-2 py-0.5 whitespace-pre"
              style={{
                backgroundColor: isDark ? 'rgba(34,197,94,0.1)' : '#d1fae5',
                color: isDark ? '#4ade80' : '#14532d',
              }}
            >
              <span className="select-none mr-1">+</span>
              {line}
            </div>
          ))}
        </div>
      </div>
    );
  }

  // Create operation without content - show just the file creation indicator
  if (operation === 'create') {
    const filename = path?.split('/').pop() || path;
    return (
      <div className="flex items-center gap-2 px-2 py-1 text-xs font-mono text-green-600 dark:text-green-400">
        <FilePlus className="h-3 w-3" />
        <span className="truncate" title={path}>{filename}</span>
        <span className={cn('ml-auto', STATUS_CLASSES[status])}>{STATUS_LABELS[status]}</span>
      </div>
    );
  }

  // Delete operation - show file deletion indicator
  if (operation === 'delete') {
    return (
      <div className="flex items-center gap-2 px-2 py-1 text-xs font-mono text-red-600 dark:text-red-400">
        <FileX className="h-3 w-3" />
        <span className="truncate" title={path}>{path}</span>
        <span className={cn('ml-auto', STATUS_CLASSES[status])}>{STATUS_LABELS[status]}</span>
      </div>
    );
  }

  // Fallback for unknown patch formats
  return <DefaultToolViewer args={args} toolName={toolName} />;
}

function DiffPreviewLine({
  line,
  type,
  isDark,
}: {
  line: string;
  type: 'addition' | 'context';
  isDark: boolean;
}): React.ReactNode {
  const isAddition = type === 'addition';
  return (
    <div
      className="px-2 py-0.5 whitespace-pre"
      style={{
        backgroundColor: isAddition ? (isDark ? 'rgba(34,197,94,0.1)' : '#d1fae5') : 'transparent',
        color: isAddition ? (isDark ? '#4ade80' : '#14532d') : 'inherit',
      }}
    >
      <span className="select-none mr-1">{isAddition ? '+' : ' '}</span>
      {line}
    </div>
  );
}
