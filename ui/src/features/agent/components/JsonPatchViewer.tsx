import { useMemo } from 'react';
import { useUserPreferences } from '@/contexts/UserPreference';
import { cn } from '@/lib/utils';
import { type JsonPatch, type DiffLine, computeDiffLines } from '../utils/diffUtils';

interface JsonPatchViewerProps {
  patch: JsonPatch;
  status?: PatchViewStatus;
}

export type PatchViewStatus = 'proposed' | 'applied' | 'failed';

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

export function JsonPatchViewer({ patch, status = 'proposed' }: JsonPatchViewerProps): React.ReactNode {
  const { preferences } = useUserPreferences();
  const isDark = preferences.theme === 'dark';

  const diffLines = useMemo(
    () => computeDiffLines(patch.old_string, patch.new_string),
    [patch.old_string, patch.new_string]
  );

  const stats = useMemo(() => {
    let additions = 0;
    let deletions = 0;
    for (const line of diffLines) {
      if (line.type === 'addition') additions++;
      if (line.type === 'deletion') deletions++;
    }
    return { additions, deletions };
  }, [diffLines]);

  return (
    <div className={cn(
      'rounded border overflow-hidden text-xs font-mono',
      status === 'failed' ? 'border-red-500/40' : 'border-border'
    )}>
      {/* Header */}
      <div className="flex items-center gap-2 px-2 py-1 bg-muted border-b border-border text-muted-foreground">
        {patch.path && (
          <span className="truncate">{patch.path.split('/').pop()}</span>
        )}
        <span className={cn('ml-auto', STATUS_CLASSES[status])}>{STATUS_LABELS[status]}</span>
        <span className="text-green-600 dark:text-green-400">+{stats.additions}</span>
        <span className="text-red-600 dark:text-red-400">-{stats.deletions}</span>
      </div>

      {/* Diff lines */}
      <div className="max-h-[300px] overflow-auto">
        <div className="min-w-fit">
          {diffLines.map((line, idx) => (
            <DiffLineRow key={idx} line={line} isDark={isDark} />
          ))}
        </div>
      </div>
    </div>
  );
}

const DIFF_STYLES = {
  prefix: { addition: '+', deletion: '-', context: ' ' },
  light: {
    bg: { addition: '#d1fae5', deletion: '#fee2e2', context: 'transparent' },
    text: { addition: '#14532d', deletion: '#7f1d1d', context: 'inherit' },
  },
  dark: {
    bg: { addition: 'rgba(34,197,94,0.1)', deletion: 'rgba(239,68,68,0.1)', context: 'transparent' },
    text: { addition: '#4ade80', deletion: '#f87171', context: 'inherit' },
  },
} as const;

function DiffLineRow({ line, isDark }: { line: DiffLine; isDark: boolean }): React.ReactNode {
  const theme = isDark ? DIFF_STYLES.dark : DIFF_STYLES.light;
  return (
    <div
      className="px-2 py-0.5 whitespace-pre"
      style={{ backgroundColor: theme.bg[line.type], color: theme.text[line.type] }}
    >
      <span className="select-none mr-1">{DIFF_STYLES.prefix[line.type]}</span>
      {line.content}
    </div>
  );
}
