// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Workflow } from 'lucide-react';
import type { KeyboardEvent, ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';

export interface DocumentChoice {
  value: string;
  label: string;
  /** The parent workflow of the file; the others are its child workflows. */
  parent?: boolean;
}

/**
 * The tabs of the workflows of a multi-document YAML file: the parent, then
 * each child workflow that its steps can call. A child tab has the icon of a
 * step that calls a workflow. Left and right arrows move between the tabs.
 */
export function DocumentTabs({
  value,
  onChange,
  documents,
}: {
  value: string;
  onChange(value: string): void;
  documents: DocumentChoice[];
}): ReactElement {
  const { ts } = useI18n();

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
    event.preventDefault();
    const at = documents.findIndex((document) => document.value === value);
    const step = event.key === 'ArrowRight' ? 1 : -1;
    const next = documents[(at + step + documents.length) % documents.length];
    if (!next) return;
    onChange(next.value);
    event.currentTarget
      .querySelector<HTMLElement>(`[data-document="${CSS.escape(next.value)}"]`)
      ?.focus();
  };

  return (
    <div
      role="tablist"
      aria-label={ts('Document')}
      onKeyDown={onKeyDown}
      className="pointer-events-auto flex h-7 min-w-0 max-w-[min(45vw,36rem)] items-center gap-0.5 overflow-x-auto rounded-md border border-border bg-card/90 p-0.5 text-xs [scrollbar-width:none]"
    >
      {documents.map((document) => {
        const active = document.value === value;
        return (
          <button
            key={document.value}
            type="button"
            role="tab"
            aria-selected={active}
            tabIndex={active ? 0 : -1}
            data-document={document.value}
            title={
              document.parent
                ? `${document.label} ${ts('(Parent)')}`
                : document.label
            }
            onClick={() => onChange(document.value)}
            className={cn(
              'flex h-6 max-w-[14rem] shrink-0 items-center gap-1.5 rounded px-2 outline-none focus-visible:ring-2 focus-visible:ring-ring',
              active
                ? 'bg-muted font-medium text-foreground'
                : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground'
            )}
          >
            {!document.parent && (
              <Workflow className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
            )}
            <span className="truncate">{document.label}</span>
          </button>
        );
      })}
    </div>
  );
}
