// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo } from 'react';
import type { components } from '@/api/v1/schema';
import RelativeTime from '@/components/ui/relative-time';
import { StatusDot } from '@/components/ui/status-dot';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { buildDAGPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import { useOpenPage } from '@/features/dag-runs/lib/useOpenPage';
import {
  cachedMsOf,
  runDurationMs,
} from '@/features/workflow-workspace/dock/runTimelineModel';
import { triggerTypeLabels } from '@/features/dags/components/common/TriggerTypeIndicator';
import { useQuery } from '@/hooks/api';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import { formatDurationMs } from '@/lib/formatDuration';
import { withoutWorkspaceLabels } from '@/lib/workspace';
import { cn } from '@/lib/utils';
import { runDisplayStatus } from '@/lib/runDisplayStatus';

import { failureCause, queueWaitMs, statusMoment } from './recentRunFacts';

type Run = components['schemas']['DAGRunDetails'];

/** The runs that the list under a workflow shows. */
export const RECENT_RUNS_SHOWN = 10;
const REFRESH_MS = 15_000;
/** The labels of a run that its line shows; the others go in the tooltip. */
const MAX_LABELS_SHOWN = 2;

/**
 * The latest runs of a workflow, under its row in the list. It reads the
 * history of the workflow when it mounts, so a collapsed row costs nothing.
 */
export function DAGRecentRuns({
  fileName,
  service,
}: {
  fileName: string;
  /** True when the workflow is a service DAG: only then a run reads Healthy. */
  service: boolean;
}) {
  const { ts } = useI18n();
  const remoteNode = useRemoteNode();
  const openPage = useOpenPage();
  const { data, error, isLoading } = useQuery(
    '/dags/{fileName}/dag-runs',
    { params: { query: { remoteNode }, path: { fileName } } },
    { refreshInterval: REFRESH_MS, revalidateOnFocus: false }
  );
  const runs = useMemo(
    () => (data?.dagRuns ?? []).slice(0, RECENT_RUNS_SHOWN),
    [data]
  );
  const columns = useMemo(() => runColumns(runs), [runs]);
  const open = (run: string, event?: React.MouseEvent) =>
    openPage(buildDAGPageURL({ fileName, remoteNode, run }), event);

  return (
    <div
      role="region"
      aria-label={ts('Recent runs of {name}', { name: fileName })}
      data-testid="dag-recent-runs"
      className="animate-in fade-in-0 slide-in-from-top-1 border-l-2 border-border/60 py-1 pl-3 text-xs duration-200"
    >
      {error ? (
        <p role="alert" className="py-1 text-destructive">
          <I18nText text={'Could not load the recent runs'} />
        </p>
      ) : isLoading ? (
        <p className="py-1 text-muted-foreground">
          <I18nText text={'Loading...'} />
        </p>
      ) : runs.length === 0 ? (
        <p className="py-1 text-muted-foreground">
          <I18nText text={'No runs'} />
        </p>
      ) : (
        <>
          <RunHeader columns={columns} />
          <ul className="divide-y divide-border/40">
            {runs.map((run) => (
              <RunLine
                key={run.dagRunId}
                run={run}
                service={service}
                columns={columns}
                onOpen={open}
              />
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

/** The columns of the runs of a workflow: those of the Executions page. */
interface RunColumns {
  profile: boolean;
  /** The CSS grid template that the header and every line share. */
  template: string;
}

/**
 * The columns of the Executions page, in its order. The status column also
 * holds the time of the run (scheduled, ended or started). Profile shows only when a run
 * fills it. The columns share the width in fractions:
 * a wide list spreads them, not leaves a gap.
 */
function runColumns(runs: readonly Run[]): RunColumns {
  const profile = runs.some((r) => !!r.profileName);
  const template = [
    'minmax(0,2fr)', // run id
    'minmax(0,1.6fr)', // status, and its time
    'minmax(0,1fr)', // trigger
    profile && 'minmax(0,1fr)',
    'minmax(0,1fr)', // queued
    'minmax(0,1fr)', // started
    'minmax(0,2fr)', // duration and its detail
    'minmax(0,1fr)', // worker
  ]
    .filter(Boolean)
    .join(' ');
  return { profile, template };
}

function RunHeader({ columns }: { columns: RunColumns }) {
  const head = (text: string) => (
    <span className="truncate">
      <I18nText text={text} />
    </span>
  );
  return (
    <div
      aria-hidden="true"
      style={{ gridTemplateColumns: columns.template }}
      className="grid items-center gap-x-3 border-b border-border/40 px-1 pb-1 text-[11px] text-muted-foreground"
    >
      {head('Run ID')}
      {head('Status')}
      {head('Trigger')}
      {columns.profile && head('Profile')}
      {head('Queued At')}
      {head('Started At')}
      {head('Duration')}
      {head('Worker')}
    </div>
  );
}

function RunLine({
  run,
  service,
  columns,
  onOpen,
}: {
  run: Run;
  service: boolean;
  columns: RunColumns;
  onOpen(runId: string, event?: React.MouseEvent): void;
}) {
  const { ts } = useI18n();
  // The time counts the cached time that the run took from other runs.
  const own = runDurationMs(run, Date.now());
  const cached = cachedMsOf(run);
  const total = own === null ? null : own + cached;
  const wait = queueWaitMs(run);
  const timeDetail = [
    own !== null && cached > 0
      ? ts('{own} + {cached} cached', {
          own: formatDurationMs(own),
          cached: formatDurationMs(cached),
        })
      : null,
    wait !== null
      ? ts('queued {time}', { time: formatDurationMs(wait) })
      : null,
  ].filter(Boolean);
  const cause = failureCause(run);
  const params = (run.params ?? '').trim();
  const labels = withoutWorkspaceLabels(run.labels ?? run.tags ?? []);
  const shownLabels = labels.slice(0, MAX_LABELS_SHOWN);
  const healthy = runDisplayStatus(run, { service }).healthy;
  const moment = statusMoment(run);
  const trigger = run.triggerType ? triggerTypeLabels[run.triggerType] : null;
  return (
    <li>
      <button
        type="button"
        onClick={(event) => {
          event.stopPropagation();
          onOpen(run.dagRunId, event);
        }}
        style={{ gridTemplateColumns: columns.template }}
        className="grid w-full cursor-pointer items-center gap-x-3 gap-y-0.5 rounded-sm px-1 py-1 text-left hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <span className="truncate font-mono text-muted-foreground">
          {run.dagRunId}
        </span>
        <span className="flex min-w-0 items-center gap-1.5">
          <StatusDot kind="run" status={run.status} healthy={healthy} />
          {moment && (
            <>
              <span className="text-muted-foreground">·</span>
              <Moment at={moment} />
            </>
          )}
        </span>
        <span>
          {trigger && (
            <span
              title={run.triggerActor || undefined}
              className="inline-block max-w-full truncate rounded border border-border/60 px-1.5 text-[11px] leading-4 text-muted-foreground"
            >
              {ts(trigger)}
            </span>
          )}
        </span>
        {columns.profile && (
          <span
            title={run.profileName}
            className="truncate font-mono text-muted-foreground"
          >
            {run.profileName || '-'}
          </span>
        )}
        <Moment at={run.queuedAt} />
        <span className="truncate text-foreground/80">
          <RelativeTime
            timestamp={run.startedAt}
            absolute={run.startedAt}
            compact
            ago
          />
        </span>
        <span
          className="truncate text-muted-foreground"
          title={
            own !== null && cached > 0
              ? ts('Total {total} = own {own} + cached {cached}', {
                  total: formatDurationMs(own + cached),
                  own: formatDurationMs(own),
                  cached: formatDurationMs(cached),
                })
              : undefined
          }
        >
          <span className="text-foreground/80">
            {total === null ? '-' : formatDurationMs(total)}
          </span>
          {timeDetail.length > 0 && <> · {timeDetail.join(' · ')}</>}
        </span>
        <span
          title={run.workerId ? `${ts('Worker')} ${run.workerId}` : undefined}
          className="truncate font-mono text-muted-foreground"
        >
          {run.workerId || '-'}
        </span>
        {(cause || params || labels.length > 0) && (
          <span className="col-span-full flex min-w-0 items-center gap-2 pl-1">
            <span
              title={cause ?? (params || undefined)}
              className={cn(
                'min-w-0 truncate',
                cause ? 'text-destructive' : 'font-mono text-muted-foreground'
              )}
            >
              {cause ?? params}
            </span>
            {shownLabels.length > 0 && (
              <span
                title={labels.join(', ')}
                className="flex shrink-0 items-center gap-0.5"
              >
                {shownLabels.map((label) => (
                  <span
                    key={label}
                    className="rounded bg-muted px-1.5 text-[11px] leading-4 text-muted-foreground"
                  >
                    {label}
                  </span>
                ))}
                {labels.length > shownLabels.length && (
                  <span className="text-[11px] text-muted-foreground">
                    +{labels.length - shownLabels.length}
                  </span>
                )}
              </span>
            )}
          </span>
        )}
      </button>
    </li>
  );
}

/** A time of a run in the compact form ("5m ago"), or a dash. */
function Moment({ at }: { at?: string }) {
  return (
    <span className="truncate text-muted-foreground">
      {at ? <RelativeTime timestamp={at} absolute={at} compact ago /> : '-'}
    </span>
  );
}
