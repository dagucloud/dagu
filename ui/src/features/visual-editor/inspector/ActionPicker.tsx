// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronDown } from 'lucide-react';
import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type ReactElement,
} from 'react';
import { Input } from '@/components/ui/input';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { cn } from '@/lib/utils';
import type { ActionCatalogEntry } from '../contracts';
import { searchCatalog } from './catalogSearch';

export interface ActionPickerProps {
  catalog: ActionCatalogEntry[];
  /** Name of the current entry: an action name, or `run`. */
  current: string;
  disabled: boolean;
  onPick(entry: ActionCatalogEntry): void;
}

/**
 * Button with the current action; it opens a search box and a list of the
 * catalog entries. Arrow keys move in the list, Enter picks the active
 * entry and Escape closes the list.
 */
export function ActionPicker({
  catalog,
  current,
  disabled,
  onPick,
}: ActionPickerProps): ReactElement {
  const { ts } = useI18n();
  const listId = useId();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const button = useRef<HTMLButtonElement>(null);
  const results = useMemo(
    () => searchCatalog(catalog, query),
    [catalog, query]
  );
  const entry = catalog.find((item) => item.name === current);

  useEffect(() => setActive(0), [query]);

  const close = () => {
    setOpen(false);
    setQuery('');
    button.current?.focus();
  };
  const pick = (item: ActionCatalogEntry | undefined) => {
    if (!item) {
      return;
    }
    close();
    onPick(item);
  };

  return (
    <div className="space-y-1">
      <button
        ref={button}
        type="button"
        disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={ts('Change the action')}
        onClick={() => (open ? close() : setOpen(true))}
        className="flex h-7 w-full items-center gap-2 rounded-md border border-input bg-card px-2 text-left text-xs disabled:cursor-not-allowed disabled:opacity-50"
      >
        <span className="truncate font-medium text-foreground">
          {entry?.title ?? current}
        </span>
        <span className="truncate font-mono text-muted-foreground">
          {current}
        </span>
        <ChevronDown className="ml-auto size-3.5 shrink-0" aria-hidden />
      </button>
      {open && (
        <div className="rounded-md border border-border bg-card p-1 shadow-sm">
          <Input
            autoFocus
            className="h-7 px-2 text-xs"
            role="combobox"
            aria-label={ts('Search actions')}
            aria-controls={listId}
            aria-expanded
            aria-activedescendant={
              results[active] ? `${listId}-${active}` : undefined
            }
            placeholder={ts('Search actions')}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={(event) => {
              if (event.nativeEvent.isComposing) return;
              if (event.key === 'ArrowDown') {
                event.preventDefault();
                setActive((index) => Math.min(index + 1, results.length - 1));
              } else if (event.key === 'ArrowUp') {
                event.preventDefault();
                setActive((index) => Math.max(index - 1, 0));
              } else if (event.key === 'Enter') {
                event.preventDefault();
                pick(results[active]);
              } else if (event.key === 'Escape') {
                event.preventDefault();
                close();
              }
            }}
          />
          <ul
            id={listId}
            role="listbox"
            className="mt-1 max-h-64 overflow-auto"
          >
            {results.map((item, index) => (
              <li
                key={item.name}
                id={`${listId}-${index}`}
                role="option"
                aria-selected={index === active}
                onMouseDown={(event) => event.preventDefault()}
                onMouseEnter={() => setActive(index)}
                onClick={() => pick(item)}
                className={cn(
                  'flex cursor-pointer items-baseline gap-2 rounded px-2 py-0.5 text-xs',
                  index === active && 'bg-muted',
                  item.name === current && 'font-semibold'
                )}
              >
                <span className="text-foreground">{item.title}</span>
                <span className="truncate font-mono text-muted-foreground">
                  {item.name}
                </span>
              </li>
            ))}
            {results.length === 0 && (
              <li className="px-2 py-0.5 text-xs text-muted-foreground">
                <I18nText text="No matching actions" />
              </li>
            )}
          </ul>
        </div>
      )}
    </div>
  );
}
