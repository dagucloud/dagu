// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Plus, X } from 'lucide-react';
import { useId, useState, type ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { remainingNames, WILDCARD } from '@/features/ai-endpoints/endpoint';
import { useI18n } from '@/i18n/I18nProvider';
import {
  ChatField,
  errorIssue,
} from '@/features/visual-editor/inspector/sections/chat/fields';

/**
 * A list of names that may use an endpoint: the value `*` for all of them, or
 * names that the user picks from `suggestions` or types. `onQuery` tells the
 * host the typed text, so that it can search for suggestions.
 */
export function AllowField({
  label,
  allLabel,
  help,
  value,
  suggestions,
  problem,
  onChange,
  onQuery,
}: {
  label: string;
  /** The text of the choice that matches every name. */
  allLabel: string;
  help?: string;
  value: string[];
  suggestions: readonly string[];
  problem?: string | undefined;
  onChange(next: string[]): void;
  onQuery?(text: string): void;
}): ReactElement {
  const { ts } = useI18n();
  const id = useId();
  const [query, setQuery] = useState('');
  const all = value.length === 1 && value[0] === WILDCARD;
  const add = (name: string) => {
    const clean = name.trim();
    if (clean === '' || clean === WILDCARD || value.includes(clean)) return;
    onChange([...value, clean]);
    setQuery('');
    onQuery?.('');
  };
  const shown = remainingNames(suggestions, value, query).slice(0, 8);
  return (
    <ChatField
      label={label}
      htmlFor={`${id}-input`}
      help={help}
      issues={problem ? [errorIssue(problem)] : undefined}
    >
      <div role="group" aria-label={label} className="space-y-1.5">
        <label className="flex items-center gap-2 text-xs">
          <Checkbox
            checked={all}
            onCheckedChange={(checked) =>
              onChange(checked === true ? [WILDCARD] : [])
            }
            aria-label={allLabel}
          />
          {allLabel}
        </label>
        {!all && (
          <>
            {value.length > 0 && (
              <ul className="flex flex-wrap gap-1">
                {value.map((name) => (
                  <li
                    key={name}
                    className="inline-flex items-center gap-1 rounded-md border border-border bg-muted px-1.5 py-0.5 font-mono text-xs"
                  >
                    {name}
                    <button
                      type="button"
                      aria-label={ts('Remove {name}', { name })}
                      className="rounded text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                      onClick={() =>
                        onChange(value.filter((item) => item !== name))
                      }
                    >
                      <X className="size-3" aria-hidden="true" />
                    </button>
                  </li>
                ))}
              </ul>
            )}
            <div className="flex gap-1.5">
              <Input
                id={`${id}-input`}
                value={query}
                placeholder={ts('Type a name')}
                autoComplete="off"
                className="h-7 text-xs"
                onChange={(event) => {
                  setQuery(event.target.value);
                  onQuery?.(event.target.value);
                }}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') {
                    event.preventDefault();
                    add(query);
                  }
                }}
              />
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-7"
                disabled={query.trim() === ''}
                onClick={() => add(query)}
              >
                <Plus className="size-3.5" aria-hidden="true" />
                {ts('Add')}
              </Button>
            </div>
            {shown.length > 0 && (
              <ul
                aria-label={ts('Suggestions')}
                className="flex flex-wrap gap-1"
              >
                {shown.map((name) => (
                  <li key={name}>
                    <button
                      type="button"
                      className="rounded-md border border-dashed border-border px-1.5 py-0.5 font-mono text-xs text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                      onClick={() => add(name)}
                    >
                      {name}
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </>
        )}
      </div>
    </ChatField>
  );
}
