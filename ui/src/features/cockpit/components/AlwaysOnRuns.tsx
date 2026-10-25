// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { useContext, useMemo } from 'react';
import { Link } from 'react-router-dom';
import { components, Status } from '@/api/v1/schema';
import RelativeTime from '@/components/ui/relative-time';
import StatusChip from '@/components/ui/status-chip';
import { AppBarContext } from '@/contexts/AppBarContext';
import { buildDAGRunPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import { useQuery } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import { formatRunDuration } from '@/lib/dagRunTiming';

type DAGRunSummary = components['schemas']['DAGRunSummary'];

/** Runs listed for an always-on DAG, newest first. */
const RUN_LIMIT = 20;
/** How far back the list looks. */
const WINDOW_DAYS = 7;
/** How often an open list reads again. A closed one reads nothing. */
const REFRESH_MS = 5000;

function isFailure(run: DAGRunSummary): boolean {
  return (
    run.status === Status.Failed ||
    run.status === Status.Aborted ||
    run.status === Status.Rejected
  );
}

/**
 * The latest runs of an always-on DAG. They are read when the list opens, not
 * streamed, so an always-on DAG that restarts all the time costs nothing while its
 * list is closed.
 */
export function AlwaysOnRuns({ name }: { name: string }): React.ReactElement {
  const { ts } = useI18n();
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  // Fixed for the life of the list, so the request key stays the same.
  const fromDate = useMemo(
    () => Math.floor(Date.now() / 1000) - WINDOW_DAYS * 24 * 60 * 60,
    []
  );
  const { data, error, isLoading } = useQuery(
    '/dag-runs/{name}',
    {
      params: {
        path: { name },
        query: { remoteNode, fromDate, limit: RUN_LIMIT },
      },
    },
    { refreshInterval: REFRESH_MS, keepPreviousData: true }
  );
  const runs = data?.dagRuns ?? [];
  const failed = runs.filter(isFailure).length;
  const succeeded = runs.filter((run) => run.status === Status.Success).length;

  if (error && runs.length === 0) {
    return (
      <p className="px-2.5 py-2 text-xs text-destructive">
        {ts('Failed to load runs')}
      </p>
    );
  }
  if (runs.length === 0) {
    return (
      <p className="px-2.5 py-2 text-xs text-muted-foreground">
        {isLoading ? ts('Loading runs...') : ts('No run in the last 7 days')}
      </p>
    );
  }

  return (
    <div>
      <p className="px-2.5 pb-1 pt-2 text-[11px] text-muted-foreground">
        {ts('Last {count} runs', { count: runs.length })}
        {' · '}
        {ts('{count} succeeded', { count: succeeded })}
        {failed > 0 && (
          <>
            {' · '}
            <span className="text-destructive">
              {ts('{count} failed', { count: failed })}
            </span>
          </>
        )}
      </p>
      <ul className="max-h-64 divide-y divide-border overflow-y-auto">
        {runs.map((run) => (
          <li key={run.dagRunId}>
            <Link
              to={buildDAGRunPageURL({
                rootDAGRunName: run.name,
                rootDAGRunId: run.dagRunId,
                remoteNode,
              })}
              className="flex items-center gap-3 px-2.5 py-1.5 text-xs transition-colors hover:bg-accent/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              <StatusChip status={run.status} size="xs">
                {run.statusLabel}
              </StatusChip>
              <RelativeTime
                timestamp={run.startedAt}
                compact
                fallback="--"
                className="w-10 text-muted-foreground"
              />
              <span className="w-16 tabular-nums text-muted-foreground">
                {formatRunDuration(run.startedAt, run.finishedAt)}
              </span>
              <code className="ml-auto truncate font-mono text-[11px] text-muted-foreground">
                {run.dagRunId}
              </code>
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
}
