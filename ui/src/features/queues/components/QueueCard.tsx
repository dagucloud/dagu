// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronRight } from 'lucide-react';
import React from 'react';
import { Link } from 'react-router-dom';
import { components } from '@/api/v1/schema';
import { cn } from '@/lib/utils';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

interface QueueCardProps {
  queue: components['schemas']['Queue'];
}

function QueueCard({ queue }: QueueCardProps) {
  const runningCount = queue.runningCount || 0;
  const queuedCount = queue.queuedCount || 0;
  const queuedCapped = queue.queuedCountCapped === true;
  const utilization = queue.maxConcurrency
    ? Math.round((runningCount / queue.maxConcurrency) * 100)
    : null;

  return (
    <Link
      to={`/queues/${encodeURIComponent(queue.name)}`}
      className="card-obsidian group flex h-full flex-col gap-4 px-4 py-4 transition-all duration-200 hover:border-border-strong hover:bg-muted/5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="whitespace-normal break-words text-base font-semibold text-foreground">
            {queue.name}
          </h3>
          <p className="mt-1 text-sm text-muted-foreground">
            {formatActivityLine(runningCount, queuedCount, queuedCapped)}
          </p>
        </div>
        <ChevronRight className="mt-0.5 h-4 w-4 flex-shrink-0 text-muted-foreground transition-transform duration-200 group-hover:translate-x-0.5" />
      </div>

      {queue.maxConcurrency && (
        <div className="space-y-1.5">
          <div className="flex items-center justify-between text-xs text-muted-foreground">
            <span>
              <I18nText text={'Capacity'} />
            </span>
            <span className="tabular-nums">
              {runningCount}/{queue.maxConcurrency} <I18nText text={'in use'} />
            </span>
          </div>
          <div className="h-1.5 overflow-hidden rounded-full bg-muted">
            <div
              className={cn(
                'h-full transition-all duration-300',
                queuedCount > 0 ? 'bg-warning' : 'bg-primary'
              )}
              style={{ width: `${utilization || 0}%` }}
            />
          </div>
        </div>
      )}

      <div className="grid grid-cols-2 gap-3">
        <I18nProps>
          <SummaryStat label="Running" value={runningCount} />
        </I18nProps>
        <I18nProps>
          <SummaryStat
            label="Queued"
            value={formatQueuedCount(queuedCount, queuedCapped)}
            emphasized={queuedCount > 0}
          />
        </I18nProps>
      </div>
    </Link>
  );
}

// formatQueuedCount renders a capped count as a lower bound, e.g. "500+",
// so a truncated server-side scan is never shown as an exact total.
export function formatQueuedCount(count: number, capped: boolean): string {
  return capped ? `${count}+` : `${count}`;
}

function formatActivityLine(
  runningCount: number,
  queuedCount: number,
  queuedCapped = false
): React.ReactNode {
  const queuedLabel = formatQueuedCount(queuedCount, queuedCapped);
  if (queuedCount > 0 && runningCount > 0) {
    return (
      <I18nText
        text="{queued} queued, {running} running"
        values={{ queued: queuedLabel, running: runningCount }}
      />
    );
  }
  if (queuedCount > 0) {
    return <I18nText text="{count} queued" values={{ count: queuedLabel }} />;
  }
  if (runningCount > 0) {
    return <I18nText text="{count} running" values={{ count: runningCount }} />;
  }
  return <I18nText text="No activity" />;
}

function SummaryStat({
  label,
  value,
  emphasized = false,
}: {
  label: string;
  value: React.ReactNode;
  emphasized?: boolean;
}) {
  return (
    <div
      className={cn(
        'rounded-md border px-3 py-3',
        emphasized
          ? 'border-warning/30 bg-warning/10'
          : 'border-border/80 bg-muted/10'
      )}
    >
      <div
        className={cn(
          'text-3xl font-semibold tabular-nums leading-none',
          emphasized ? 'text-warning' : 'text-foreground'
        )}
      >
        {value}
      </div>
      <div className="mt-2 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
        {label}
      </div>
    </div>
  );
}

export default QueueCard;
