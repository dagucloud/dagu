// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { AnsiLine } from '@/lib/ansi';
import type { SchedulerLogLine } from '@/lib/scheduler-log';

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
      className="flex items-start gap-3 border-b border-border px-3 py-2 last:border-b-0"
    >
      <time
        className="w-[5.5rem] shrink-0 pt-0.5 font-mono text-xs text-muted-foreground"
        title={line.timestamp}
      >
        {shortTimestamp(line.timestamp)}
      </time>
      <span
        className={`w-12 shrink-0 rounded px-1.5 py-0.5 text-center text-[10px] font-semibold ${levelClass(line.level)}`}
      >
        {line.level}
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-baseline gap-2">
          <span className="whitespace-normal break-words font-medium text-foreground">
            <AnsiLine text={line.message} />
          </span>
          {line.step && (
            <span className="rounded bg-primary/10 px-1.5 py-0.5 font-mono text-xs text-primary">
              {line.step}
            </span>
          )}
        </div>
        {line.details && (
          <details className="mt-1 text-xs text-muted-foreground">
            <summary className="w-fit cursor-pointer select-none">
              Details
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
