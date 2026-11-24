// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ComponentPropsWithoutRef } from 'react';
import type { components } from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';

type NodeRunCountsValue = components['schemas']['NodeRunCounts'];

interface NodeRunCountsProps
  extends Omit<ComponentPropsWithoutRef<'span'>, 'children'> {
  counts: NodeRunCountsValue;
}

/** What the counts say in words, for a label or a tooltip. */
export function nodeRunCountsLabel(
  ts: ReturnType<typeof useI18n>['ts'],
  counts: NodeRunCountsValue
): string {
  return ts(
    '{succeeded} succeeded, {failed} failed, {active} running of {total} nodes',
    {
      succeeded: counts.succeeded,
      failed: counts.failed + counts.aborted,
      active: counts.running + counts.waiting,
      total: counts.total,
    }
  );
}

/**
 * The node runs of a step with nodes in brief, such as `12 ✓ 1 ✗ 1 ⋯`:
 * succeeded, failed or aborted, and running or waiting, each only when there
 * are any. With none of them yet, the number of nodes.
 */
export function NodeRunCounts({
  counts,
  className,
  ...props
}: NodeRunCountsProps) {
  const { ts } = useI18n();
  const failed = counts.failed + counts.aborted;
  const active = counts.running + counts.waiting;
  const label = nodeRunCountsLabel(ts, counts);
  const none = counts.succeeded + failed + active === 0;
  return (
    <span
      role="img"
      aria-label={label}
      title={label}
      className={cn(
        'inline-flex shrink-0 items-center gap-1 font-mono text-[10px] tabular-nums',
        className
      )}
      {...props}
    >
      {counts.succeeded > 0 && (
        <span className="text-success">{`${counts.succeeded} ✓`}</span>
      )}
      {failed > 0 && <span className="text-destructive">{`${failed} ✗`}</span>}
      {active > 0 && <span className="text-info">{`${active} ⋯`}</span>}
      {none && (
        <span className="text-muted-foreground">{String(counts.total)}</span>
      )}
    </span>
  );
}
