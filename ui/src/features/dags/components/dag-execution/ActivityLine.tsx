// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronDown } from 'lucide-react';
import React from 'react';
import { AnsiLine } from '@/lib/ansi';
import type { SchedulerLogLine } from '@/lib/scheduler-log';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';

function shortTimestamp(timestamp?: string): string {
  return timestamp?.match(/T(\d{2}:\d{2}:\d{2}(?:\.\d{3})?)/)?.[1] || '';
}

function levelClass(level?: string): string {
  switch (level) {
    case 'ERROR':
      return 'bg-error/10 text-error';
    case 'WARN':
      return 'bg-warning/10 text-warning';
    default:
      return 'bg-muted text-muted-foreground';
  }
}

/**
 * One line of the scheduler log in the Activity view: a compact row of 24px
 * with the time, the level, the step and the message. A line with details
 * ends with a chevron on the same line; the whole line toggles the details.
 */
export function ActivityLine({
  line,
  lineNumber,
}: {
  line: SchedulerLogLine;
  lineNumber: number;
}) {
  const { ts } = useI18n();
  const [open, setOpen] = React.useState(false);
  if (!line.structured) {
    return (
      <div
        data-line-number={lineNumber}
        className="whitespace-normal break-words border-b border-border px-3 py-1 font-mono text-xs leading-4 last:border-b-0"
      >
        <AnsiLine text={line.message} />
      </div>
    );
  }

  const expandable = !!line.details;
  // A click that ends a text selection must not toggle the details.
  const toggle = () => {
    if (window.getSelection()?.toString()) return;
    setOpen((value) => !value);
  };

  return (
    <div
      data-line-number={lineNumber}
      onClick={expandable ? toggle : undefined}
      className={cn(
        'grid grid-cols-[5.5rem_3rem_minmax(0,1fr)] items-start gap-x-3 border-b border-border px-3 py-1 text-xs leading-4 last:border-b-0 sm:grid-cols-[5.5rem_3rem_minmax(8rem,12rem)_minmax(0,1fr)]',
        expandable && 'cursor-pointer hover:bg-muted/60'
      )}
    >
      <time
        className="font-mono text-xs leading-4 text-muted-foreground"
        title={line.timestamp}
      >
        {shortTimestamp(line.timestamp)}
      </time>
      <span
        className={`rounded px-1.5 text-center text-[10px] font-semibold leading-4 ${levelClass(line.level)}`}
      >
        {line.level}
      </span>
      <span
        className="min-w-0 truncate font-mono text-xs font-semibold leading-4 text-foreground"
        title={line.step}
      >
        {line.step}
      </span>
      <div className="col-span-3 min-w-0 sm:col-span-1">
        <div className="flex min-w-0 items-start gap-1">
          <span className="min-w-0 flex-1 whitespace-normal break-words text-xs leading-4 text-foreground">
            <AnsiLine text={line.message} />
          </span>
          {expandable && (
            <button
              type="button"
              aria-expanded={open}
              aria-label={ts('Details')}
              title={ts('Details')}
              // A press of the chevron toggles even with text selected.
              onClick={(event) => {
                event.stopPropagation();
                setOpen((value) => !value);
              }}
              className="inline-flex size-4 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            >
              <ChevronDown
                aria-hidden="true"
                className={cn('size-3.5', open && 'rotate-180')}
              />
            </button>
          )}
        </div>
        {expandable && open && (
          <code className="mt-1 block whitespace-pre-wrap break-words font-mono text-xs text-muted-foreground">
            {line.details}
          </code>
        )}
      </div>
    </div>
  );
}
