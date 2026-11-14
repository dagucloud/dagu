// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Link } from 'react-router-dom';
import { type components } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { buildDAGRunPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import { useI18n } from '@/i18n/I18nProvider';
import dayjs from '@/lib/dayjs';

type DAGFile = components['schemas']['DAGFile'];
type DAGRunSummary = components['schemas']['DAGRunSummary'];
type SubDAGRunSummary = components['schemas']['SubDAGRunSummary'];

/**
 * The run the list shows for a DAG: its own latest run, or a newer run that
 * another DAG started with dag.run, given as sub. Run actions keep using the
 * DAG's own latest run.
 */
export function shownRun(dag: DAGFile): {
  run: DAGRunSummary;
  sub?: SubDAGRunSummary;
} {
  const sub = dag.latestSubDAGRun;
  if (!sub) return { run: dag.latestDAGRun };
  const own = dayjs(dag.latestDAGRun.startedAt);
  const other = dayjs(sub.run.startedAt);
  if (other.isValid() && (!own.isValid() || other.isAfter(own))) {
    return { run: sub.run, sub };
  }
  return { run: dag.latestDAGRun };
}

/** Links a run that another DAG started to its page under the root run. */
export function SubRunLink({ sub }: { sub: SubDAGRunSummary }) {
  const { ts } = useI18n();
  const remoteNode = useRemoteNode();
  return (
    <Link
      to={buildDAGRunPageURL({
        rootDAGRunName: sub.rootDAGRunName,
        rootDAGRunId: sub.rootDAGRunId,
        subDAGRunId: sub.run.dagRunId,
        remoteNode,
      })}
      className="truncate text-xs text-muted-foreground hover:underline"
      // The row opens the DAG; this link opens the run instead.
      onClick={(event) => event.stopPropagation()}
    >
      {ts('via {name}', { name: sub.parentDAGRunName })}
    </Link>
  );
}
