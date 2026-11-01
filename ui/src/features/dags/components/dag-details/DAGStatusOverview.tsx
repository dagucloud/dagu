// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * DAGStatusOverview component displays summary information about a DAG run.
 *
 * @module features/dags/components/dag-details
 */
import dayjs from '@/lib/dayjs';
import RelativeTime from '@/components/ui/relative-time';
import { Check, Copy, SlidersHorizontal, Terminal } from 'lucide-react';
import React, { useCallback, useState } from 'react';
import { useClock } from '@/hooks/useClock';
import { components, Status } from '../../../../api/v1/schema';
import {
  PreconditionErrors,
  RunError,
  RunProgress,
  RunStatusMessage,
  RuntimeConditions,
} from '../common/RunSummaryBlocks';
import { triggerTypeLabels } from '../common/TriggerTypeIndicator';
import { I18nText } from '@/i18n/I18nText';

type Props = {
  status?: components['schemas']['DAGRunDetails'];
  onViewLog?: (dagRunId: string) => void;
  /**
   * Shows parameters, timing, trigger, worker, and run ID. Leave it off
   * where a header already shows them.
   */
  showRunMeta?: boolean;
};

function truncateId(id: string): string {
  if (id.length <= 16) return id;
  return `${id.slice(0, 8)}...${id.slice(-4)}`;
}

function formatDuration(
  startedAt: string | undefined,
  finishedAt: string | undefined
): string {
  if (!startedAt || startedAt === '-') {
    return '-';
  }

  const start = dayjs(startedAt);
  if (!start.isValid()) {
    return '-';
  }

  const end = finishedAt && finishedAt !== '-' ? dayjs(finishedAt) : dayjs();
  if (!end.isValid()) {
    return '-';
  }

  const diff = end.diff(start, 'second');
  if (diff < 0) {
    return '-';
  }

  const hours = Math.floor(diff / 3600);
  const minutes = Math.floor((diff % 3600) / 60);
  const seconds = diff % 60;

  if (hours > 0) {
    return `${hours}h ${minutes}m ${seconds}s`;
  }
  if (minutes > 0) {
    return `${minutes}m ${seconds}s`;
  }
  return `${seconds}s`;
}

function DAGStatusOverview({
  status,
  onViewLog,
  showRunMeta = true,
}: Props): React.JSX.Element | null {
  const [copied, setCopied] = useState(false);

  const isRunning = status?.status === Status.Running;

  // A running run reads the shared one-second clock; a finished one never ticks.
  const currentDuration = useClock(
    isRunning && status?.startedAt ? 1000 : null,
    () => formatDuration(status?.startedAt, status?.finishedAt)
  );

  const copyRunId = useCallback(async () => {
    if (!status?.dagRunId) return;
    try {
      await navigator.clipboard.writeText(status.dagRunId);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Fallback for older browsers
      const textArea = document.createElement('textarea');
      textArea.value = status.dagRunId;
      document.body.appendChild(textArea);
      textArea.select();
      document.execCommand('copy');
      document.body.removeChild(textArea);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  }, [status?.dagRunId]);

  if (!status) {
    return null;
  }

  return (
    <div className="min-w-0 space-y-2">
      {showRunMeta && (
        <>
          {/* Parameters - Only show when present */}
          {status.params && (
            <div className="flex min-w-0 items-center gap-1.5 text-xs font-mono">
              <Terminal className="h-3 w-3 text-muted-foreground flex-shrink-0" />
              <span
                className="min-w-0 truncate text-foreground"
                title={status.params}
              >
                {status.params}
              </span>
            </div>
          )}

          {/* Timing & Metadata - Compact grid */}
          <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 text-xs">
            {status.scheduleTime && (
              <span>
                <span className="text-muted-foreground">
                  <I18nText text={'Scheduled'} />{' '}
                </span>
                <RelativeTime
                  className="font-mono text-foreground"
                  timestamp={status.scheduleTime}
                />
              </span>
            )}
            {status.queuedAt && (
              <span>
                <span className="text-muted-foreground">
                  <I18nText text={'Queued'} />{' '}
                </span>
                <RelativeTime
                  className="font-mono text-foreground"
                  timestamp={status.queuedAt}
                />
              </span>
            )}
            <span>
              <span className="text-muted-foreground">
                <I18nText text={'Started'} />{' '}
              </span>
              <RelativeTime
                className="font-mono text-foreground"
                timestamp={status.startedAt}
              />
            </span>
            <span>
              <span className="text-muted-foreground">
                <I18nText text={'Finished'} />{' '}
              </span>
              <RelativeTime
                className="font-mono text-foreground"
                timestamp={status.finishedAt}
              />
            </span>
            <span className="flex items-center gap-1">
              <span className="text-muted-foreground">
                <I18nText text={'Duration'} />{' '}
              </span>
              <span className="font-mono font-medium text-foreground">
                {currentDuration}
              </span>
              {isRunning && (
                <span className="inline-block w-1.5 h-1.5 rounded-full bg-success animate-pulse" />
              )}
            </span>
          </div>

          {/* Metadata row */}
          <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs">
            {status.triggerType && (
              <span>
                <span className="text-muted-foreground">
                  <I18nText text={'Trigger'} />{' '}
                </span>
                <span className="font-medium text-foreground">
                  {triggerTypeLabels[status.triggerType] ?? status.triggerType}
                </span>
              </span>
            )}
            {status.triggerActor && (
              <span>
                <span className="text-muted-foreground">
                  <I18nText text={'Actor'} />{' '}
                </span>
                <span className="font-medium text-foreground">
                  {status.triggerActor}
                </span>
              </span>
            )}
            {status.profileName && (
              <span className="inline-flex max-w-[180px] items-center gap-1 truncate">
                <SlidersHorizontal className="h-3 w-3 text-muted-foreground" />
                <span className="font-medium text-foreground">
                  {status.profileName}
                </span>
              </span>
            )}
            {status.workerId && (
              <span className="truncate max-w-[180px]" title={status.workerId}>
                <span className="text-muted-foreground">
                  <I18nText text={'Worker'} />{' '}
                </span>
                <span className="font-medium text-foreground">
                  {status.workerId}
                </span>
              </span>
            )}
            {status.dagRunId && (
              <button
                onClick={copyRunId}
                className="inline-flex min-w-0 max-w-full cursor-pointer items-center gap-1 font-mono text-muted-foreground transition-colors hover:text-foreground"
                title={`Click to copy: ${status.dagRunId}`}
              >
                <span className="truncate">{truncateId(status.dagRunId)}</span>
                {copied ? (
                  <Check className="h-3 w-3 text-success" />
                ) : (
                  <Copy className="h-3 w-3 opacity-50" />
                )}
              </button>
            )}
          </div>
        </>
      )}

      {/* Node Status - Progress bar with inline counts */}
      <RunProgress status={status} onViewLog={onViewLog} />

      {/* Execution status message */}
      <RunStatusMessage status={status.status} />

      <RunError error={status.error} />
      <RuntimeConditions conditions={status.conditions} />
      <PreconditionErrors preconditions={status.preconditions} />
    </div>
  );
}

export default DAGStatusOverview;
