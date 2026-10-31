// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ArrowRight, CircleAlert, CircleCheck } from 'lucide-react';
import React from 'react';
import { Link } from 'react-router-dom';
import {
  DAGRunActivityQueueBlockedReason,
  Status,
  StatusLabel,
  type components,
} from '@/api/v1/schema';
import { RelativeTime } from '@/components/ui/relative-time';
import StatusChip from '@/components/ui/status-chip';
import Ticker from '@/components/ui/ticker';
import { useCanAccessSystemStatus } from '@/contexts/AuthContext';
import { DAGRunActions } from '@/features/dag-runs/components/common';
import { PeekLink } from '@/features/peek/PeekLink';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import dayjs from '@/lib/dayjs';
import { formatDurationMs, formatRoughDurationMs } from '@/lib/formatDuration';
import { runStatusText } from '@/lib/status-utils';
import { cn } from '@/lib/utils';
import { executionsURL } from '../lib/activityLinks';
import {
  type ActivityQueue,
  type ActivityRun,
  type AttentionItem,
  isQueueStalled,
  type SystemAlert,
  type TimeRange,
} from '../lib/activityModel';

type DAGRunSummary = components['schemas']['DAGRunSummary'];
type Upcoming = components['schemas']['DAGRunActivityUpcoming'];

/** Why a queue's runs cannot start, as a sentence and as a short label. */
const BLOCKED_REASONS: Record<
  DAGRunActivityQueueBlockedReason,
  { message: string; label: string }
> = {
  [DAGRunActivityQueueBlockedReason.noMatchingWorker]: {
    message:
      'Queued runs in {queue} cannot start: no healthy worker matches their selector.',
    label: 'no matching worker',
  },
  [DAGRunActivityQueueBlockedReason.noAvailableWorker]: {
    message:
      'Queued runs in {queue} cannot start: no healthy worker is available.',
    label: 'no worker available',
  },
  [DAGRunActivityQueueBlockedReason.workerDispatchUnavailable]: {
    message:
      'Queued runs in {queue} cannot start: they cannot be handed to a worker right now.',
    label: 'worker unreachable',
  },
};

/** Labels of the statuses a retry can start from. */
const RETRYABLE_LABELS: Partial<Record<Status, StatusLabel>> = {
  [Status.Failed]: StatusLabel.failed,
  [Status.Aborted]: StatusLabel.aborted,
  [Status.Rejected]: StatusLabel.rejected,
  [Status.PartialSuccess]: StatusLabel.partially_succeeded,
};

function isoTime(seconds?: number): string | undefined {
  return seconds === undefined ? undefined : dayjs.unix(seconds).toISOString();
}

/** The run fields the shared run actions read, built from activity data. */
function retrySummary(name: string, run: ActivityRun): DAGRunSummary | null {
  const statusLabel = RETRYABLE_LABELS[run.status];
  if (!statusLabel) return null;
  return {
    name,
    dagRunId: run.dagRunId,
    status: run.status,
    statusLabel,
    autoRetryCount: 0,
    startedAt: isoTime(run.startedAt) ?? '',
    finishedAt: isoTime(run.finishedAt) ?? '',
    artifactsAvailable: false,
  };
}

function SectionTitle({
  children,
  count,
}: {
  children: React.ReactNode;
  count?: number;
}): React.ReactElement {
  return (
    <h2 className="flex items-baseline gap-2 text-sm font-medium text-foreground">
      {children}
      {count !== undefined && (
        <span className="text-xs font-normal text-muted-foreground tabular-nums">
          {count}
        </span>
      )}
    </h2>
  );
}

function RunLink({
  name,
  run,
  remoteNode,
  children,
  className,
}: {
  name: string;
  run: ActivityRun;
  remoteNode: string;
  children: React.ReactNode;
  className?: string;
}): React.ReactElement {
  return (
    <PeekLink
      peek={{ dag: name, run: run.dagRunId }}
      remoteNode={remoteNode}
      className={className}
    >
      {children}
    </PeekLink>
  );
}

/** One system alert: what is wrong, and where to look. */
function SystemAlertRow({
  alert,
  canAccessSystemStatus,
}: {
  alert: SystemAlert;
  canAccessSystemStatus: boolean;
}): React.ReactElement {
  const { ts } = useI18n();
  const linkClass =
    'inline-flex h-7 items-center gap-1 rounded-md px-2 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground';
  return (
    <li className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2">
      <CircleAlert className="h-4 w-4 shrink-0 text-error" aria-hidden="true" />
      <span className="min-w-0 flex-1 text-sm text-foreground">
        {systemAlertText(alert, ts)}
      </span>
      {alert.kind === 'queue' ? (
        <Link
          to={`/queues/${encodeURIComponent(alert.queue)}`}
          className={linkClass}
        >
          {ts('Open')}
          <ArrowRight className="h-3 w-3" aria-hidden="true" />
        </Link>
      ) : (
        canAccessSystemStatus && (
          <Link to="/system-status" className={linkClass}>
            <I18nText text={'System Status'} />
            <ArrowRight className="h-3 w-3" aria-hidden="true" />
          </Link>
        )
      )}
    </li>
  );
}

function systemAlertText(
  alert: SystemAlert,
  ts: ReturnType<typeof useI18n>['ts']
): string {
  switch (alert.kind) {
    case 'scheduler':
      return ts(
        'The scheduler is not running, so scheduled and queued runs will not start.'
      );
    case 'queue':
      return ts(BLOCKED_REASONS[alert.reason].message, { queue: alert.queue });
    case 'disk':
      return ts(
        'The data disk is {percent}% full. Runs fail once it fills up.',
        { percent: alert.usedPercent }
      );
  }
}

/**
 * What needs a person now; one quiet line when nothing does. System alerts,
 * which hold back work of many runs, come first.
 */
export function NeedsAttention({
  items,
  alerts,
  remoteNode,
  onChanged,
}: {
  items: AttentionItem[];
  alerts: SystemAlert[];
  remoteNode: string;
  onChanged: () => void;
}): React.ReactElement {
  const { ts } = useI18n();
  const canAccessSystemStatus = useCanAccessSystemStatus();
  // Kept mounted across refreshes, so a screen reader announces an alert
  // when a refresh adds it.
  const alertStatus = (
    <p role="status" className="sr-only">
      {alerts.map((alert) => systemAlertText(alert, ts)).join(' ')}
    </p>
  );
  if (items.length === 0 && alerts.length === 0) {
    return (
      <>
        {alertStatus}
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <CircleCheck className="h-4 w-4 text-success" aria-hidden="true" />
          <I18nText
            text={'All clear. No failing workflows or runs waiting for input.'}
          />
        </p>
      </>
    );
  }
  return (
    <>
      {alertStatus}
      <section
        className="flex flex-col gap-2"
        aria-labelledby="needs-attention-title"
      >
        <div id="needs-attention-title">
          <SectionTitle count={items.length + alerts.length}>
            <I18nText text={'Needs attention'} />
          </SectionTitle>
        </div>
        <ul className="divide-y divide-border rounded-lg border border-border bg-card">
          {alerts.map((alert) => (
            <SystemAlertRow
              key={alert.kind === 'queue' ? `queue:${alert.queue}` : alert.kind}
              alert={alert}
              canAccessSystemStatus={canAccessSystemStatus}
            />
          ))}
          {items.map((item) => {
            const summary =
              item.kind === 'failing'
                ? retrySummary(item.name, item.run)
                : null;
            return (
              <li
                key={`${item.kind}:${item.name}:${item.run.dagRunId}`}
                className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2"
              >
                <StatusChip status={item.run.status} size="xs">
                  {runStatusText(item.run.status)}
                </StatusChip>
                <RunLink
                  name={item.name}
                  run={item.run}
                  remoteNode={remoteNode}
                  className="text-sm font-medium text-foreground hover:underline"
                >
                  {item.name}
                </RunLink>
                <span className="min-w-0 flex-1 truncate text-sm text-muted-foreground">
                  {item.kind === 'failing' &&
                    (item.run.failedStep
                      ? `${item.run.failedStep}: ${item.run.error ?? ''}`
                      : (item.run.error ?? ''))}
                  {item.kind === 'waiting' && ts('Waiting for input')}
                  {item.kind === 'slow' && (
                    <Ticker intervalMs={1000}>
                      {() => (
                        <span>
                          {ts('Running {elapsed}, usually {usual}', {
                            elapsed: formatDurationMs(
                              Date.now() - (item.run.startedAt ?? 0) * 1000
                            ),
                            usual: formatDurationMs(item.usualSeconds * 1000),
                          })}
                        </span>
                      )}
                    </Ticker>
                  )}
                </span>
                <RelativeTime
                  timestamp={isoTime(item.run.startedAt)}
                  compact
                  className="text-xs text-muted-foreground"
                />
                <div className="flex items-center gap-1">
                  {summary && (
                    <DAGRunActions
                      dagRun={summary}
                      name={item.name}
                      refresh={onChanged}
                      hideUnavailable
                    />
                  )}
                  <RunLink
                    name={item.name}
                    run={item.run}
                    remoteNode={remoteNode}
                    className="inline-flex h-7 items-center gap-1 rounded-md px-2 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground"
                  >
                    {item.kind === 'waiting' ? ts('Review') : ts('Open')}
                    <ArrowRight className="h-3 w-3" aria-hidden="true" />
                  </RunLink>
                </div>
              </li>
            );
          })}
        </ul>
      </section>
    </>
  );
}

/**
 * Runs that are running, oldest first, with live elapsed time, then one line
 * per queue with work: how full it is and how long its next run has waited.
 */
export function InProgress({
  runs,
  queues,
  remoteNode,
  nowSeconds,
}: {
  runs: ActivityRun[];
  queues: ActivityQueue[];
  remoteNode: string;
  nowSeconds: number;
}): React.ReactElement {
  const { ts } = useI18n();
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle count={runs.length}>
        <I18nText text={'In progress'} />
      </SectionTitle>
      {runs.length === 0 && queues.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          <I18nText text={'Nothing is running.'} />
        </p>
      ) : (
        <ul className="flex flex-col gap-1">
          {runs.map((run) => (
            <li key={run.dagRunId} className="flex items-center gap-2 text-sm">
              <StatusChip status={run.status} size="xs">
                {runStatusText(run.status)}
              </StatusChip>
              <RunLink
                name={run.name}
                run={run}
                remoteNode={remoteNode}
                className="min-w-0 flex-1 truncate text-foreground hover:underline"
              >
                {run.name}
              </RunLink>
              {run.startedAt !== undefined && (
                <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
                  <Ticker intervalMs={1000}>
                    {() => (
                      <span>
                        {formatDurationMs(
                          Date.now() - (run.startedAt ?? 0) * 1000
                        )}
                      </span>
                    )}
                  </Ticker>
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
      {queues.length > 0 && (
        <ul
          aria-label={ts('Queues')}
          className={cn(
            'flex flex-col gap-1',
            runs.length > 0 && 'border-t border-border pt-2'
          )}
        >
          {queues.map((queue) => (
            <li key={queue.name} className="flex flex-col text-sm">
              <Link
                to={`/queues/${encodeURIComponent(queue.name)}`}
                className="truncate text-foreground hover:underline"
              >
                {queue.name}
              </Link>
              <span className="text-xs text-muted-foreground tabular-nums">
                {queue.maxConcurrency === undefined
                  ? ts('{running} running', { running: runningCount(queue) })
                  : ts('{running} of {max} running', {
                      running: runningCount(queue),
                      max: queue.maxConcurrency,
                    })}
                {queue.queued > 0 || queue.queuedCapped ? (
                  <>
                    {' · '}
                    {ts(
                      queue.queuedCapped ? '{count}+ queued' : '{count} queued',
                      { count: queue.queued }
                    )}
                  </>
                ) : null}
                {queue.nextQueuedAt !== undefined && (
                  <span
                    className={cn(
                      'whitespace-nowrap',
                      !queue.blockedReason &&
                        isQueueStalled(queue, nowSeconds) &&
                        'text-warning'
                    )}
                  >
                    {' · '}
                    <Ticker intervalMs={1000}>
                      {() => (
                        <span>
                          {ts('next waiting {elapsed}', {
                            elapsed: formatRoughDurationMs(
                              Math.max(
                                0,
                                Date.now() - (queue.nextQueuedAt ?? 0) * 1000
                              )
                            ),
                          })}
                        </span>
                      )}
                    </Ticker>
                  </span>
                )}
              </span>
              {queue.blockedReason && (
                <span className="text-xs text-error">
                  {ts(BLOCKED_REASONS[queue.blockedReason].label)}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

/** A queue's running count, marked when it is a lower bound. */
function runningCount(queue: ActivityQueue): string | number {
  return queue.runningCapped ? `${queue.running}+` : queue.running;
}

/** The next scheduled runs with a countdown to each. */
export function UpNext({
  upcoming,
  remoteNode,
  tzOffsetSeconds = 0,
}: {
  upcoming: Upcoming[];
  remoteNode: string;
  tzOffsetSeconds?: number;
}): React.ReactElement {
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle>
        <I18nText text={'Up next'} />
      </SectionTitle>
      {upcoming.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          <I18nText text={'No scheduled runs.'} />
        </p>
      ) : (
        <ul className="flex flex-col gap-1">
          {upcoming.map((item) => (
            <li key={item.fileName} className="flex items-center gap-2 text-sm">
              <PeekLink
                peek={{ dag: item.fileName }}
                remoteNode={remoteNode}
                className="min-w-0 flex-1 truncate text-foreground hover:underline"
              >
                {item.name}
              </PeekLink>
              <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
                {dayjs
                  .unix(item.nextRun)
                  .utcOffset(tzOffsetSeconds / 60)
                  .format('HH:mm')}
              </span>
              <span className="min-w-16 shrink-0 whitespace-nowrap text-right text-xs text-muted-foreground tabular-nums">
                <Ticker intervalMs={1000}>
                  {() => (
                    <span>
                      {formatRoughDurationMs(
                        Math.max(0, item.nextRun * 1000 - Date.now())
                      )}
                    </span>
                  )}
                </Ticker>
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

/** Statuses in the order the counts row lists them. */
const COUNT_ORDER = [
  Status.Success,
  Status.Failed,
  Status.Running,
  Status.Waiting,
  Status.Queued,
  Status.Aborted,
  Status.PartialSuccess,
  Status.Rejected,
  Status.NotStarted,
];

/**
 * Counts of the loaded runs by status, each linking to the matching runs of
 * the window. They cover every run in the window only when none is left to
 * load, so the total says "latest runs" otherwise.
 */
export function ActivityCounts({
  runs,
  window: range,
  hasMore,
}: {
  runs: ActivityRun[];
  window: TimeRange;
  hasMore: boolean;
}): React.ReactElement {
  const { ts } = useI18n();
  const counts = new Map<Status, number>();
  for (const run of runs) {
    counts.set(run.status, (counts.get(run.status) ?? 0) + 1);
  }
  return (
    <div className="flex flex-wrap items-baseline gap-x-5 gap-y-1">
      <Link
        to={executionsURL(range)}
        className="group flex items-baseline gap-1.5"
      >
        <span className="text-xl font-semibold text-foreground tabular-nums group-hover:underline">
          {runs.length.toLocaleString()}
        </span>
        <span className="text-xs text-muted-foreground">
          {hasMore ? (
            <I18nText text={'latest runs'} />
          ) : (
            <I18nText text={'runs'} />
          )}
        </span>
      </Link>
      {COUNT_ORDER.filter(
        (status) =>
          (counts.get(status) ?? 0) > 0 ||
          status === Status.Success ||
          status === Status.Failed
      ).map((status) => (
        <Link
          key={status}
          to={executionsURL({ ...range, status })}
          className="group flex items-baseline gap-1.5"
        >
          <span
            className={
              status === Status.Failed && (counts.get(status) ?? 0) > 0
                ? 'text-xl font-semibold text-error tabular-nums group-hover:underline'
                : 'text-xl font-semibold text-foreground tabular-nums group-hover:underline'
            }
          >
            {(counts.get(status) ?? 0).toLocaleString()}
          </span>
          <span className="text-xs text-muted-foreground">
            {ts(runStatusText(status)).toLowerCase()}
          </span>
        </Link>
      ))}
    </div>
  );
}
