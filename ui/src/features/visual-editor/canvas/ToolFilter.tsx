// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Search, X } from 'lucide-react';
import type { ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';

export interface ToolFilterProps {
  value: string;
  onChange(value: string): void;
  /** Tools that match the text, and all tools. */
  matches: number;
  total: number;
  /** A narrow box, for a small canvas. */
  compact?: boolean;
  /** Enter goes to the next match, Shift+Enter to the previous one. */
  onStep?(delta: 1 | -1): void;
}

/**
 * Search box of the canvas toolbar of an agent DAG. The tool cards that
 * match the text stay lit and the others dim. Keys typed in the box never
 * reach the canvas shortcuts.
 */
export function ToolFilter({
  value,
  onChange,
  matches,
  total,
  compact = false,
  onStep,
}: ToolFilterProps): ReactElement {
  const { ts } = useI18n();
  const active = value.trim() !== '';
  return (
    <div
      data-testid="ve-tool-filter"
      className="flex items-center gap-1 rounded px-1.5 text-xs text-muted-foreground focus-within:ring-1 focus-within:ring-primary"
    >
      <Search className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
      <input
        type="search"
        value={value}
        placeholder={ts('Filter')}
        aria-label={ts('Filter tools by name')}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={(event) => {
          event.stopPropagation();
          if (event.nativeEvent.isComposing) {
            return;
          }
          if (event.key === 'Escape' && active) {
            onChange('');
          }
          if (event.key === 'Enter' && active) {
            event.preventDefault();
            onStep?.(event.shiftKey ? -1 : 1);
          }
        }}
        className={cn(
          // A short placeholder, and a floor that holds it: the box shrinks
          // in a full toolbar, but never below the word.
          'h-7 min-w-16 shrink bg-transparent text-foreground outline-none placeholder:text-muted-foreground [&::-webkit-search-cancel-button]:hidden',
          compact ? 'w-24' : 'w-40'
        )}
      />
      {active && (
        <>
          <span data-testid="ve-tool-filter-count" className="tabular-nums">
            {ts('{count} of {total} tools', { count: matches, total })}
          </span>
          <button
            type="button"
            aria-label={ts('Clear the filter')}
            title={ts('Clear the filter')}
            onClick={() => onChange('')}
            className="flex h-5 w-5 items-center justify-center rounded hover:bg-muted hover:text-foreground"
          >
            <X className="h-3 w-3" aria-hidden="true" />
          </button>
        </>
      )}
    </div>
  );
}
