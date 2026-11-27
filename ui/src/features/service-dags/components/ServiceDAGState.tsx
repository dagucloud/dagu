// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import {
  components,
  ServiceDAGRestartHold,
  ServiceDAGState,
  Status,
} from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import Ticker from '@/components/ui/ticker';
import { formatElapsed } from '@/lib/dagRunTiming';
import dayjs from '@/lib/dayjs';
import { formatDurationMs } from '@/lib/formatDuration';
import { useI18n } from '@/i18n/I18nProvider';

type ServiceDAG = components['schemas']['ServiceDAG'];
type BadgeVariant = React.ComponentProps<typeof Badge>['variant'];

const STATE_LABEL: Record<ServiceDAGState, string> = {
  [ServiceDAGState.pending]: 'Pending',
  [ServiceDAGState.running]: 'Running',
  [ServiceDAGState.waiting]: 'Waiting',
  [ServiceDAGState.restarting]: 'Restarting',
  [ServiceDAGState.failing]: 'Failing',
  [ServiceDAGState.suspended]: 'Suspended',
  [ServiceDAGState.stopped]: 'Stopped',
};

const STATE_VARIANT: Record<ServiceDAGState, BadgeVariant> = {
  [ServiceDAGState.pending]: 'default',
  [ServiceDAGState.running]: 'running',
  [ServiceDAGState.waiting]: 'warning',
  [ServiceDAGState.restarting]: 'success',
  [ServiceDAGState.failing]: 'error',
  [ServiceDAGState.suspended]: 'warning',
  [ServiceDAGState.stopped]: 'default',
};

// The chip of a list takes the colour of the run status that looks like the
// state: a DAG that runs or waits to restart is green, one that fails is red,
// one that is suspended or waits for a person is amber.
export const SERVICE_STATE_CHIP: Record<ServiceDAGState, [Status, string]> = {
  [ServiceDAGState.pending]: [Status.Queued, 'Pending'],
  [ServiceDAGState.running]: [Status.Success, 'Healthy'],
  [ServiceDAGState.waiting]: [Status.Waiting, 'Waiting'],
  [ServiceDAGState.restarting]: [Status.Success, 'Healthy'],
  [ServiceDAGState.failing]: [Status.Failed, 'Failing'],
  [ServiceDAGState.suspended]: [Status.Waiting, 'Suspended'],
  [ServiceDAGState.stopped]: [Status.Aborted, 'Stopped'],
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
    case ServiceDAGState.waiting:
      return { text: 'Waiting for input' };
    case ServiceDAGState.restarting:
    case ServiceDAGState.failing: {
      if (dag.restartHold === ServiceDAGRestartHold.paused) {
        return { text: 'Restart on hold: the scheduler is paused' };
      }
      if (dag.restartHold === ServiceDAGRestartHold.noScheduler) {
        return { text: 'Restart on hold: no scheduler is running' };
      }
      // The server knows how long the scheduler backs off a DAG that keeps
      // failing; restartWaitSec alone would end the count too early.
      const nextRestart = dag.nextRestartAt
        ? Date.parse(dag.nextRestartAt)
        : NaN;
      if (Number.isNaN(nextRestart)) {
        // The next run waits in the queue: it has no restart time.
        const status = dag.latestRun?.status;
        if (
          dag.state === ServiceDAGState.restarting &&
          (status === Status.Queued || status === Status.NotStarted)
        ) {
          return { text: 'Restarting…' };
        }
        // A DAG whose state cannot be read, or one from an older server, has
        // no restart time, so nothing says that a restart is under way.
        return { text: 'Next restart unknown' };
      }
      const remaining = Math.ceil((nextRestart - now) / 1000);
      return remaining > 0
        ? {
            text: 'Restart in {duration}',
            values: { duration: formatDurationMs(remaining * 1000) },
          }
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

/**
 * When the scheduler starts the next run of a service DAG, in words, or
 * nothing while no restart is due: the DAG is not restarting or failing, its
 * restarts are on hold, or the time is unknown.
 */
export function ServiceDAGNextRun({
  dag,
  className,
}: {
  dag: ServiceDAG;
  className?: string;
}): React.ReactElement | null {
  const { locale, ts } = useI18n();
  const at = dag.nextRestartAt ? Date.parse(dag.nextRestartAt) : NaN;
  if (
    (dag.state !== ServiceDAGState.restarting &&
      dag.state !== ServiceDAGState.failing) ||
    dag.restartHold ||
    dag.readError ||
    Number.isNaN(at)
  ) {
    return null;
  }
  // The words change slowly: "in a few seconds", "in 4 minutes", "in an hour".
  return (
    <Ticker intervalMs={15_000}>
      {() => (
        <span className={className}>
          {at > Date.now()
            ? ts('Restart {when}', {
                when: dayjs(at)
                  .locale(locale === 'zh-CN' ? 'zh-cn' : locale)
                  .fromNow(),
              })
            : ts('Restarting…')}
        </span>
      )}
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
