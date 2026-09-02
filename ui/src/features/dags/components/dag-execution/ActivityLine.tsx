// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { AnsiLine } from '@/lib/ansi';
import type { SchedulerLogLine } from '@/lib/scheduler-log';
import { I18nText } from '@/i18n/I18nText';

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

export function ActivityLine({
  line,
  lineNumber,
}: {
  line: SchedulerLogLine;
  lineNumber: number;
}) {
  if (!line.structured) {
    return (
      <div
        data-line-number={lineNumber}
        className="whitespace-normal break-words border-b border-border px-3 py-2 font-mono text-sm last:border-b-0"
      >
        <AnsiLine text={line.message} />
      </div>
    );
  }

  return (
    <div
      data-line-number={lineNumber}
      className="grid grid-cols-[5.5rem_3rem_minmax(0,1fr)] items-start gap-x-3 gap-y-1 border-b border-border px-3 py-2 last:border-b-0 sm:grid-cols-[5.5rem_3rem_minmax(8rem,12rem)_minmax(0,1fr)]"
    >
      <time
        className="pt-0.5 font-mono text-xs text-muted-foreground"
        title={line.timestamp}
      >
        {shortTimestamp(line.timestamp)}
      </time>
      <span
        className={`rounded px-1.5 py-0.5 text-center text-[10px] font-semibold ${levelClass(line.level)}`}
      >
        {line.level}
      </span>
      <span
        className="min-w-0 truncate pt-0.5 font-mono text-sm font-semibold text-foreground"
        title={line.step}
      >
        {line.step}
      </span>
      <div className="col-span-3 min-w-0 sm:col-span-1">
        <span className="whitespace-normal break-words text-sm text-foreground">
          <AnsiLine text={line.message} />
        </span>
        {line.details && (
          <details className="mt-1 text-xs text-muted-foreground">
            <summary className="w-fit cursor-pointer select-none">
              <I18nText text={"Details"} />
            </summary>
            <code className="mt-1 block whitespace-pre-wrap break-words font-mono">
              {line.details}
            </code>
          </details>
        )}
      </div>
    </div>
  );
}
