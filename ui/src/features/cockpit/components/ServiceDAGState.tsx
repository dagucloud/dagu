// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { components, ServiceDAGState } from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import Ticker from '@/components/ui/ticker';
import { formatElapsed } from '@/lib/dagRunTiming';
import { useI18n } from '@/i18n/I18nProvider';

type ServiceDAG = components['schemas']['ServiceDAG'];
type BadgeVariant = React.ComponentProps<typeof Badge>['variant'];

const STATE_LABEL: Record<ServiceDAGState, string> = {
  [ServiceDAGState.pending]: 'Pending',
  [ServiceDAGState.running]: 'Running',
  [ServiceDAGState.restarting]: 'Restarting',
  [ServiceDAGState.failing]: 'Failing',
  [ServiceDAGState.suspended]: 'Suspended',
  [ServiceDAGState.stopped]: 'Stopped',
};

const STATE_VARIANT: Record<ServiceDAGState, BadgeVariant> = {
  [ServiceDAGState.pending]: 'default',
  [ServiceDAGState.running]: 'running',
  [ServiceDAGState.restarting]: 'success',
  [ServiceDAGState.failing]: 'error',
  [ServiceDAGState.suspended]: 'warning',
  [ServiceDAGState.stopped]: 'default',
};

/**
 * What a service DAG does right now, as a source string for the catalog and its
 * values. The state comes from the server and does not change while a service DAG
 * keeps running, so the time-based part is computed here from the clock.
 */
export function describeServiceDAG(
  dag: ServiceDAG,
  now: number
): { text: string; values?: Record<string, string | number> } {
  const finishedAt = dag.latestRun?.finishedAt
    ? Date.parse(dag.latestRun.finishedAt)
    : NaN;

  switch (dag.state) {
    case ServiceDAGState.running: {
      const duration = formatElapsed(dag.latestRun?.startedAt, undefined, {
        live: true,
        now,
      });
      return duration
        ? { text: 'Up for {duration}', values: { duration } }
        : { text: 'Running' };
    }
    case ServiceDAGState.restarting:
    case ServiceDAGState.failing: {
      if (Number.isNaN(finishedAt)) return { text: 'Restarting…' };
      const remaining = Math.ceil(
        (finishedAt + dag.restartWaitSec * 1000 - now) / 1000
      );
      return remaining > 0
        ? { text: 'Restart in {seconds}s', values: { seconds: remaining } }
        : { text: 'Restarting…' };
    }
    case ServiceDAGState.stopped:
      return { text: 'Stopped by an operator' };
    case ServiceDAGState.suspended:
      return { text: 'No new run' };
    default:
      return { text: 'Waiting for a run' };
  }
}

/** The state of a service DAG as a badge. */
export function ServiceDAGStateBadge({
  state,
}: {
  state: ServiceDAGState;
}): React.ReactElement {
  const { ts } = useI18n();
  return <Badge variant={STATE_VARIANT[state]}>{ts(STATE_LABEL[state])}</Badge>;
}

/** What a service DAG does right now, kept current by the local clock. */
export function ServiceDAGStatusText({
  dag,
  className,
}: {
  dag: ServiceDAG;
  className?: string;
}): React.ReactElement {
  const { ts } = useI18n();
  return (
    <Ticker intervalMs={1000}>
      {() => {
        const { text, values } = describeServiceDAG(dag, Date.now());
        return <span className={className}>{ts(text, values)}</span>;
      }}
    </Ticker>
  );
}

/** The API reports at most this many failures in a row. */
const MAX_REPORTED_FAILURES = 10;

/**
 * Why a service DAG fails, or null while it does not: the error that kept its
 * state from being read, or how many runs in a row failed and the error of the
 * latest one.
 */
export function ServiceDAGFailure({
  dag,
  className,
}: {
  dag: ServiceDAG;
  className?: string;
}): React.ReactElement | null {
  const { ts } = useI18n();
  if (dag.readError) {
    return (
      <span className={className} title={dag.readError}>
        {dag.readError}
      </span>
    );
  }
  const count = dag.consecutiveFailures;
  if (count === 0) {
    return null;
  }
  const streak =
    count === 1
      ? ts('Last run failed')
      : ts('{count} failures in a row', {
          count: count >= MAX_REPORTED_FAILURES ? `${count}+` : count,
        });
  const error = dag.latestRun?.error;
  return (
    <span className={className} title={error}>
      {error ? `${streak}: ${error}` : streak}
    </span>
  );
}
