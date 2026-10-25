// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Calendar,
  Check,
  Copy,
  Hash,
  Hourglass,
  type LucideIcon,
  Server,
  SlidersHorizontal,
  Timer,
  Zap,
} from 'lucide-react';
import React from 'react';
import { components, Status } from '@/api/v1/schema';
import RelativeTime from '@/components/ui/relative-time';
import Ticker from '@/components/ui/ticker';
import { triggerTypeLabels } from '@/features/dags/components/common/TriggerTypeIndicator';
import { useCopyFeedback } from '@/hooks/useCopyFeedback';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import { formatRunDuration } from '@/lib/dagRunTiming';
import dayjs from '@/lib/dayjs';
import { cn } from '@/lib/utils';

type RunFields = Pick<
  components['schemas']['DAGRunDetails'],
  | 'dagRunId'
  | 'status'
  | 'startedAt'
  | 'finishedAt'
  | 'queuedAt'
  | 'triggerType'
  | 'triggerActor'
  | 'workerId'
  | 'profileName'
  | 'params'
>;

type Props = {
  dagRun: RunFields;
  /**
   * Copies the run ID when the parent announces copies itself; otherwise the
   * line copies and announces on its own.
   */
  onCopyRunId?: () => void;
  runIdCopied?: boolean;
  /**
   * Classes of the root. A parent that lets the line grow to the end of its
   * row keeps the run ID at the right edge.
   */
  className?: string;
};

/** Queue time shorter than this is noise, not waiting. */
const MIN_QUEUE_WAIT_SECONDS = 1;

function MetaItem({
  icon: Icon,
  label,
  className,
  children,
}: {
  icon: LucideIcon;
  label: string;
  className?: string;
  children: React.ReactNode;
}) {
  const { ts } = useI18n();
  return (
    <div
      className={cn('flex min-w-0 items-center gap-1.5', className)}
      title={ts(label)}
    >
      <Icon className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
      <dt className="sr-only">{ts(label)}</dt>
      <dd className="min-w-0 text-foreground">{children}</dd>
    </div>
  );
}

// The API reports an unset time as "-".
function parseTime(value?: string): dayjs.Dayjs | undefined {
  if (!value || value === '-') return undefined;
  const time = dayjs(value);
  return time.isValid() ? time : undefined;
}

function queueWaitSeconds(queuedAt?: string, startedAt?: string): number {
  const queued = parseTime(queuedAt);
  const started = parseTime(startedAt);
  if (!queued || !started) return 0;
  return started.diff(queued, 'second');
}

/**
 * One line of what a run is: how it started, when, for how long, where, and
 * its ID. Both run and workflow headers use it, so each fact shows once.
 */
export function RunMetaLine({
  dagRun,
  onCopyRunId,
  runIdCopied,
  className,
}: Props): React.ReactElement {
  const { ts } = useI18n();
  const ownCopy = useCopyFeedback();
  const copied = onCopyRunId ? !!runIdCopied : ownCopy.copied;
  const copyRunId = () => {
    if (onCopyRunId) {
      onCopyRunId();
    } else {
      void ownCopy.copy(dagRun.dagRunId);
    }
  };
  const started = parseTime(dagRun.startedAt) !== undefined;
  const running = dagRun.status === Status.Running;
  const queueWait = queueWaitSeconds(dagRun.queuedAt, dagRun.startedAt);

  return (
    <div className={cn('flex min-w-0 flex-col gap-2', className)}>
      <dl className="flex flex-wrap items-center gap-x-5 gap-y-2 text-sm text-muted-foreground">
        {dagRun.triggerType && (
          <MetaItem icon={Zap} label="Trigger">
            {ts(triggerTypeLabels[dagRun.triggerType] ?? dagRun.triggerType)}
            {dagRun.triggerActor && (
              <span className="text-muted-foreground">
                {' '}
                · {dagRun.triggerActor}
              </span>
            )}
          </MetaItem>
        )}
        {started && (
          <>
            <MetaItem icon={Calendar} label="Started">
              <RelativeTime timestamp={dagRun.startedAt} fallback="--" />
            </MetaItem>
            <MetaItem icon={Timer} label="Duration">
              <span className="tabular-nums">
                {running ? (
                  <Ticker intervalMs={1000}>
                    {() => (
                      <span>
                        {formatRunDuration(dagRun.startedAt, undefined)}
                      </span>
                    )}
                  </Ticker>
                ) : (
                  formatRunDuration(dagRun.startedAt, dagRun.finishedAt)
                )}
              </span>
            </MetaItem>
          </>
        )}
        {queueWait >= MIN_QUEUE_WAIT_SECONDS && (
          <MetaItem icon={Hourglass} label="Queue wait">
            <span className="tabular-nums">
              {formatRunDuration(dagRun.queuedAt, dagRun.startedAt)}
            </span>
          </MetaItem>
        )}
        {dagRun.workerId && (
          <MetaItem icon={Server} label="Worker">
            <span className="font-mono text-xs">{dagRun.workerId}</span>
          </MetaItem>
        )}
        {dagRun.profileName && (
          <MetaItem icon={SlidersHorizontal} label="Profile">
            <span className="font-mono text-xs">{dagRun.profileName}</span>
          </MetaItem>
        )}
        {/* At the right edge, so the items that change with the run, such as a
            running duration, never move it. */}
        <MetaItem icon={Hash} label="Run ID" className="ml-auto">
          <span className="inline-flex items-center gap-1">
            <code className="break-all font-mono text-xs">
              {dagRun.dagRunId}
            </code>
            <button
              type="button"
              onClick={copyRunId}
              className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
              title={ts(copied ? 'Copied' : 'Copy run ID')}
              aria-label={ts(copied ? 'Copied' : 'Copy run ID')}
            >
              {copied ? (
                <Check className="h-3.5 w-3.5 text-success" />
              ) : (
                <Copy className="h-3.5 w-3.5" />
              )}
            </button>
            {!onCopyRunId && (
              <span className="sr-only" aria-live="polite">
                {ownCopy.copied ? (
                  <I18nText text={'Run ID copied to clipboard'} />
                ) : (
                  ''
                )}
              </span>
            )}
          </span>
        </MetaItem>
      </dl>
      {dagRun.params && (
        <details className="min-w-0 text-xs">
          <summary className="cursor-pointer font-medium text-muted-foreground hover:text-foreground">
            <I18nText text={'Parameters'} />
          </summary>
          <div className="mt-1.5 max-h-[120px] overflow-y-auto rounded-md border bg-accent px-3 py-1.5 font-mono text-foreground">
            {dagRun.params}
          </div>
        </details>
      )}
    </div>
  );
}
