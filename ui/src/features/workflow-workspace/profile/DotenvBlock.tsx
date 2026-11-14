// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronDown, ChevronUp } from 'lucide-react';
import { useId, useState } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { SYNTAX_CLASS } from '@/lib/syntax-colors';
import { cn } from '@/lib/utils';
import {
  DOTENV_MASK,
  DOTENV_PREVIEW_LINES,
  dotenvValueTokens,
  visibleEntries,
} from './dotenv';
import type { EffectiveEntry } from './runtimeProfile';

export interface DotenvBlockProps {
  /** The name of the group, for example "Global environment". */
  title: string;
  entries: readonly EffectiveEntry[];
  /** The name that a layer has in a comment, for "overrides ...". */
  layerName: (layer: EffectiveEntry['layer']) => string;
  testId: string;
}

/**
 * The variables of one group as a .env file: a comment line with the group
 * name, then one `NAME=value` line for each variable, coloured as in the code
 * editor. A secret shows `******`. A variable that wins over a weaker layer
 * ends with a comment `# overrides ...`. The block shows the first lines and
 * a button reveals the rest.
 */
export function DotenvBlock({
  title,
  entries,
  layerName,
  testId,
}: DotenvBlockProps) {
  const { ts } = useI18n();
  const listId = useId();
  const [expanded, setExpanded] = useState(false);
  const { shown, hidden } = visibleEntries(
    entries,
    expanded,
    DOTENV_PREVIEW_LINES
  );
  const canCollapse = expanded && entries.length > DOTENV_PREVIEW_LINES;

  return (
    <div
      role="group"
      aria-label={title}
      data-testid={testId}
      className="overflow-hidden rounded-md border border-border bg-background font-mono text-[11px] leading-5"
    >
      <p
        className={cn(
          'flex items-baseline justify-between gap-2 border-b border-border px-3 py-1',
          SYNTAX_CLASS.comment
        )}
      >
        <span className="min-w-0 break-words">
          <span aria-hidden="true"># </span>
          {title}
        </span>
        <span className="shrink-0 not-italic">{entries.length}</span>
      </p>
      <ul id={listId} className="py-1">
        {shown.map((entry) => {
          const secret = entry.value === undefined;
          return (
            <li
              key={entry.key}
              data-testid={`ww-rp-row-${entry.key}`}
              className="break-all px-3"
            >
              <span className={SYNTAX_CLASS.key}>{entry.key}</span>
              <span className={SYNTAX_CLASS.punct}>=</span>
              {secret ? (
                <span
                  aria-label={ts('Secret value hidden')}
                  className={SYNTAX_CLASS.comment}
                >
                  {DOTENV_MASK}
                </span>
              ) : (
                dotenvValueTokens(entry).map((token, i) => (
                  <span key={i} className={SYNTAX_CLASS[token.kind]}>
                    {token.text}
                  </span>
                ))
              )}
              {(secret || entry.overrides.length > 0) && (
                <>
                  {' '}
                  <span className={cn('ml-1', SYNTAX_CLASS.comment)}>
                    <span aria-hidden="true"># </span>
                    {[
                      secret ? ts('Secret') : null,
                      entry.overrides.length > 0
                        ? ts('overrides {layers}', {
                            layers: entry.overrides.map(layerName).join(', '),
                          })
                        : null,
                    ]
                      .filter(Boolean)
                      .join(', ')}
                  </span>
                </>
              )}
            </li>
          );
        })}
      </ul>
      {(hidden > 0 || canCollapse) && (
        <button
          type="button"
          aria-expanded={expanded}
          aria-controls={listId}
          onClick={() => setExpanded((open) => !open)}
          className="flex w-full items-center gap-1 border-t border-border px-3 py-1 font-sans text-xs text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          {expanded ? (
            <ChevronUp className="h-3.5 w-3.5" aria-hidden="true" />
          ) : (
            <ChevronDown className="h-3.5 w-3.5" aria-hidden="true" />
          )}
          {expanded
            ? ts('Show less')
            : ts('Show {count} more', { count: String(hidden) })}
        </button>
      )}
    </div>
  );
}
