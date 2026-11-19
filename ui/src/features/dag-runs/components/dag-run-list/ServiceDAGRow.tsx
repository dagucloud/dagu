// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo } from 'react';
import { ServiceDAGState } from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { RelativeTime } from '@/components/ui/relative-time';
import StatusChip from '@/components/ui/status-chip';
import { TableCell, TableRow } from '@/components/ui/table';
import ServiceActivityStrip from '@/features/dags/components/dag-list/ServiceActivityStrip';
import { SERVICE_MAX_CELLS } from '@/features/dags/components/dag-list/serviceActivity';
import { PeekLink } from '@/features/peek/PeekLink';
import { usePeekOrOpen } from '@/features/peek/usePeekOrOpen';
import { runHistorySource } from '@/features/workflow-workspace/continuous/sampleSource';
import {
  useSampleAvailability,
  useSamples,
} from '@/features/workflow-workspace/continuous/useContinuousData';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import {
  SERVICE_STATE_CHIP,
  type ServiceDAG,
} from '../../hooks/useServiceDAGs';

/** The window of a row: the dates of the filter. No end means now. */
export interface ServiceRunWindow {
  from: number;
  to?: number;
}

/**
 * What a row shows: the buckets of its window, which the samples query gives
 * for any window, and the last failure of the DAG. Both come once for a DAG,
 * and again each minute while the list shows them.
 */
function useServiceRunSummary(fileName: string, window: ServiceRunWindow) {
  const source = useMemo(() => runHistorySource(fileName), [fileName]);
  const samples = useSamples(
    source,
    window.to === undefined
      ? { since: window.from }
      : { from: window.from, to: window.to },
    SERVICE_MAX_CELLS,
    true
  );
  const availability = useSampleAvailability(source, true);
  const end = window.to ?? Date.now();
  return {
    activity: samples.data,
    windowSeconds: Math.max(1, Math.round((end - window.from) / 1000)),
    until: window.to,
    lastFailureAt: availability.data?.lastFailureAt,
  };
}

/** The server counts at most this many failures in a row. */
const STREAK_LIMIT = 10;

interface Props {
  dag: ServiceDAG;
  window: ServiceRunWindow;
}

function Title({ dag }: Pick<Props, 'dag'>) {
  const { ts } = useI18n();
  return (
    <div className="flex min-w-0 items-center gap-1">
      <PeekLink
        peek={{ dag: dag.fileName }}
        className="min-w-0 truncate hover:underline"
        onClick={(event) => event.stopPropagation()}
      >
        {dag.name}
      </PeekLink>
      <Badge
        variant="outline"
        className="h-[18px] shrink-0 rounded px-1.5 py-0 text-[11px] font-normal text-muted-foreground"
      >
        {ts('Service')}
      </Badge>
    </div>
  );
}

/** The state of the DAG, with the wait before a restart. */
function State({ dag }: Pick<Props, 'dag'>) {
  const { ts } = useI18n();
  const [status, label] = SERVICE_STATE_CHIP[dag.state];
  return (
    <span
      data-testid="service-dag-state"
      data-state={dag.state}
      className="flex flex-wrap items-center gap-x-2 gap-y-0.5"
    >
      <StatusChip status={status} size="xs">
        {label}
      </StatusChip>
      {dag.restartWaitSec > 0 && (
        <span className="whitespace-nowrap text-muted-foreground">
          {ts('Wait {seconds} s', { seconds: dag.restartWaitSec })}
        </span>
      )}
    </span>
  );
}

function LastFailure({ at }: { at?: string }) {
  const { ts } = useI18n();
  return (
    <span data-testid="service-dag-last-failure" className="whitespace-nowrap">
      <span className="text-muted-foreground">{ts('Last failure')} </span>
      {at ? (
        <RelativeTime
          timestamp={at}
          absolute={formatAbsoluteTime(at) ?? undefined}
          compact
          ago
        />
      ) : (
        ts('None')
      )}
    </span>
  );
}

/**
 * Why a failing DAG fails: the failures in a row, and the error of the
 * latest run or the reason why its state is unknown.
 */
function Failure({ dag }: Pick<Props, 'dag'>) {
  const { ts } = useI18n();
  if (dag.state !== ServiceDAGState.failing) return null;
  const error = dag.readError ?? dag.latestRun?.error;
  const streak = dag.consecutiveFailures;
  return (
    <span data-testid="service-dag-failure" className="flex flex-col">
      {streak > 0 && (
        <span className="whitespace-nowrap text-error">
          {ts('{count} failed in a row', {
            count: streak >= STREAK_LIMIT ? `${STREAK_LIMIT}+` : streak,
          })}
        </span>
      )}
      {error && (
        <span
          title={error}
          className="line-clamp-2 whitespace-normal break-words text-muted-foreground"
        >
          {error}
        </span>
      )}
    </span>
  );
}

/**
 * The row of the table for one service DAG: the name with its marker, the
 * strip of its run history over the window of the filters, its state with
 * the wait before a restart, and its last failure. A failing DAG adds the
 * failures in a row and the error. A click opens the page of the DAG. `bulk`
 * and `profile` tell which optional columns the table has.
 */
export function ServiceDAGRow({
  dag,
  window,
  bulk,
  profile,
}: Props & { bulk: boolean; profile: boolean }) {
  const summary = useServiceRunSummary(dag.fileName, window);
  const peekOrOpen = usePeekOrOpen();
  // The table: [select] Name, Run ID, Status, Trigger, [Profile], Queued At,
  // Started At, Duration, Worker. The strip spans the columns of the run ID
  // to the trigger; the state, the two dates; the failure, the other two.
  return (
    <TableRow
      data-testid="service-dag"
      data-service-dag={dag.name}
      className="cursor-pointer border-l-4 border-l-transparent bg-muted/30 hover:bg-muted/50"
      style={{ fontSize: '0.8125rem' }}
      onClick={(event) => peekOrOpen({ dag: dag.fileName }, event)}
    >
      {bulk && <TableCell className="px-0 py-1" />}
      <TableCell className="px-2 py-1 font-normal">
        <Title dag={dag} />
      </TableCell>
      <TableCell colSpan={profile ? 4 : 3} className="px-2 py-1">
        <ServiceActivityStrip
          activity={summary.activity}
          windowSeconds={summary.windowSeconds}
          until={summary.until}
          fluid
        />
      </TableCell>
      <TableCell colSpan={2} className="px-2 py-1">
        <State dag={dag} />
      </TableCell>
      <TableCell colSpan={2} className="px-2 py-1">
        <div className="flex flex-col">
          <LastFailure at={summary.lastFailureAt} />
          <Failure dag={dag} />
        </div>
      </TableCell>
    </TableRow>
  );
}

/** The card of the same row, for a narrow screen. */
export function ServiceDAGCard({ dag, window }: Props) {
  const summary = useServiceRunSummary(dag.fileName, window);
  const peekOrOpen = usePeekOrOpen();
  return (
    <div
      data-testid="service-dag"
      data-service-dag={dag.name}
      className="flex cursor-pointer flex-col gap-2 rounded-lg border border-l-4 border-border bg-muted/30 p-3"
      onClick={(event) => peekOrOpen({ dag: dag.fileName }, event)}
    >
      <Title dag={dag} />
      <ServiceActivityStrip
        activity={summary.activity}
        windowSeconds={summary.windowSeconds}
        until={summary.until}
        fluid
      />
      <div className="flex items-center justify-between text-xs">
        <State dag={dag} />
        <LastFailure at={summary.lastFailureAt} />
      </div>
      <Failure dag={dag} />
    </div>
  );
}
