// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { components, AlwaysOnDAGState } from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import Ticker from '@/components/ui/ticker';
import { cn } from '@/lib/utils';
import { useI18n } from '@/i18n/I18nProvider';

type AlwaysOnDAG = components['schemas']['AlwaysOnDAG'];
type BadgeVariant = React.ComponentProps<typeof Badge>['variant'];

const STATE_LABEL: Record<AlwaysOnDAGState, string> = {
  [AlwaysOnDAGState.pending]: 'Pending',
  [AlwaysOnDAGState.running]: 'Running',
  [AlwaysOnDAGState.restarting]: 'Restarting',
  [AlwaysOnDAGState.failing]: 'Failing',
  [AlwaysOnDAGState.suspended]: 'Suspended',
  [AlwaysOnDAGState.stopped]: 'Stopped',
};

const STATE_VARIANT: Record<AlwaysOnDAGState, BadgeVariant> = {
  [AlwaysOnDAGState.pending]: 'default',
  [AlwaysOnDAGState.running]: 'running',
  [AlwaysOnDAGState.restarting]: 'success',
  [AlwaysOnDAGState.failing]: 'error',
  [AlwaysOnDAGState.suspended]: 'warning',
  [AlwaysOnDAGState.stopped]: 'default',
};

function formatSpan(totalSeconds: number): string {
  const seconds = Math.max(0, Math.floor(totalSeconds));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ${minutes % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

/**
 * What an always-on DAG does right now, as a source string for the catalog and its
 * values. The state comes from the server and does not change while an always-on DAG
 * keeps running, so the time-based part is computed here from the clock.
 */
export function describeAlwaysOn(
  dag: AlwaysOnDAG,
  now: number
): { text: string; values?: Record<string, string | number> } {
  const startedAt = dag.latestRun?.startedAt
    ? Date.parse(dag.latestRun.startedAt)
    : NaN;
  const finishedAt = dag.latestRun?.finishedAt
    ? Date.parse(dag.latestRun.finishedAt)
    : NaN;

  switch (dag.state) {
    case AlwaysOnDAGState.running:
      return Number.isNaN(startedAt)
        ? { text: 'Running' }
        : {
            text: 'Up for {duration}',
            values: { duration: formatSpan((now - startedAt) / 1000) },
          };
    case AlwaysOnDAGState.restarting:
    case AlwaysOnDAGState.failing: {
      if (Number.isNaN(finishedAt)) return { text: 'Restarting…' };
      const remaining = Math.ceil(
        (finishedAt + dag.restartWaitSec * 1000 - now) / 1000
      );
      return remaining > 0
        ? { text: 'Restart in {seconds}s', values: { seconds: remaining } }
        : { text: 'Restarting…' };
    }
    case AlwaysOnDAGState.stopped:
      return { text: 'Stopped by an operator' };
    case AlwaysOnDAGState.suspended:
      return { text: 'No new run' };
    default:
      return { text: 'Waiting for a run' };
  }
}

/** The state of an always-on DAG as a badge. */
export function AlwaysOnStateBadge({
  state,
}: {
  state: AlwaysOnDAGState;
}): React.ReactElement {
  const { ts } = useI18n();
  return <Badge variant={STATE_VARIANT[state]}>{ts(STATE_LABEL[state])}</Badge>;
}

/** What an always-on DAG does right now, kept current by the local clock. */
export function AlwaysOnStatusText({
  dag,
  className,
}: {
  dag: AlwaysOnDAG;
  className?: string;
}): React.ReactElement {
  const { ts } = useI18n();
  return (
    <Ticker intervalMs={1000}>
      {() => {
        const { text, values } = describeAlwaysOn(dag, Date.now());
        return <span className={className}>{ts(text, values)}</span>;
      }}
    </Ticker>
  );
}

const STATE_DOT: Record<AlwaysOnDAGState, string> = {
  [AlwaysOnDAGState.pending]: 'bg-muted-foreground',
  [AlwaysOnDAGState.running]: 'bg-info',
  [AlwaysOnDAGState.restarting]: 'bg-success',
  [AlwaysOnDAGState.failing]: 'bg-error',
  [AlwaysOnDAGState.suspended]: 'bg-warning',
  [AlwaysOnDAGState.stopped]: 'bg-muted-foreground',
};

/** The state of an always-on DAG as a dot, for lines too narrow for a badge. */
export function AlwaysOnStateDot({
  state,
}: {
  state: AlwaysOnDAGState;
}): React.ReactElement {
  const { ts } = useI18n();
  return (
    <span
      role="img"
      aria-label={ts(STATE_LABEL[state])}
      title={ts(STATE_LABEL[state])}
      className={cn('h-2 w-2 shrink-0 rounded-full', STATE_DOT[state])}
    />
  );
}
