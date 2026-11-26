// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useState, type MouseEvent } from 'react';
import { NodeRunStatus, type components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { NodeRunCounts } from '@/components/ui/node-run-counts';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { buildDAGRunPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import DAGPagination from '@/features/dags/components/common/DAGPagination';
import NodeStatusChip from '@/features/dags/components/common/NodeStatusChip';
import { useOpenSubRun } from '@/features/dags/components/common/SubRunStackModal';
import { useI18n } from '@/i18n/I18nProvider';
import { formatElapsed } from '@/lib/dagRunTiming';
import { cn } from '@/lib/utils';
import type { ApiDAGRunDetails } from '../data/types';
import { NODE_RUN_STATUS, NODE_RUNS_PER_PAGE, useNodeRuns } from './nodeRuns';

type NodeRun = components['schemas']['NodeRun'];
type NodeRunStep = components['schemas']['NodeRunStep'];

/**
 * The node runs of a run (RFC 013 §Run view): one row per node, with its run
 * for each step with nodes. Rows with a failed run come first, as the server
 * orders them. A cell opens its child run on the sub-run stack, or in a new
 * tab with Ctrl or Cmd. `step` marks the column of the step a host shows.
 */
export function NodeRunsPanel({
  details,
  step,
}: {
  details: ApiDAGRunDetails;
  step?: string;
}) {
  const { ts } = useI18n();
  const [failedOnly, setFailedOnly] = useState(false);
  const [page, setPage] = useState(1);
  const { data, isLoading, error, mutate } = useNodeRuns(details, {
    failedOnly,
    page,
  });
  const open = useOpenNodeRun(details);
  // Fewer rows, such as failed rows that a retry fixed, can leave the page
  // past the last one: it reads the last page instead.
  const lastPage = data ? Math.max(1, data.pagination.totalPages) : undefined;
  useEffect(() => {
    if (lastPage !== undefined && page > lastPage) setPage(lastPage);
  }, [lastPage, page]);

  if (error && !data) {
    return (
      <div className="flex items-center gap-2 text-xs">
        <span className="text-destructive">
          {ts('Could not load the node runs.')}
        </span>
        <Button
          type="button"
          variant="ghost"
          size="xs"
          onClick={() => void mutate()}
        >
          {ts('Retry')}
        </Button>
      </div>
    );
  }

  if (!data || data.steps.length === 0) {
    return (
      <p className="text-xs text-muted-foreground">
        {ts(
          isLoading && !data
            ? 'Loading node runs...'
            : 'No node runs recorded yet.'
        )}
      </p>
    );
  }

  const anyFailed = data.steps.some(
    ({ counts }) => counts.failed + counts.aborted > 0
  );
  const selectFailedOnly = (next: boolean) => {
    setFailedOnly(next);
    setPage(1);
  };
  const { totalPages } = data.pagination;

  return (
    <section className="space-y-2" aria-label={ts('Nodes')}>
      {(anyFailed || failedOnly) && (
        <button
          type="button"
          aria-pressed={failedOnly}
          onClick={() => selectFailedOnly(!failedOnly)}
          className={cn(
            'rounded-full border px-2 py-0.5 text-xs transition-colors',
            failedOnly
              ? 'border-primary/40 bg-primary/10 text-primary'
              : 'border-border bg-background text-muted-foreground hover:bg-muted'
          )}
        >
          {ts('Failed only')}
        </button>
      )}
      <div className="overflow-x-auto rounded-md border border-border">
        <table className="w-full text-sm" data-testid="node-runs-table">
          <thead className="bg-muted/40 text-xs text-muted-foreground">
            <tr>
              <th scope="col" className="px-2 py-1.5 text-left font-medium">
                {ts('Node')}
              </th>
              {data.steps.map((column) => (
                <StepHeader
                  key={column.name}
                  column={column}
                  marked={column.name === step}
                />
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-border/60">
            {data.items.map((row) => (
              <tr key={row.node} data-testid="node-runs-row">
                <th
                  scope="row"
                  className="px-2 py-1.5 text-left font-mono text-xs font-normal"
                >
                  {row.node}
                </th>
                {data.steps.map((column) => (
                  <NodeRunCell
                    key={column.name}
                    node={row.node}
                    run={row.runs.find((run) => run.step === column.name)}
                    marked={column.name === step}
                    open={open}
                  />
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {totalPages > 1 && (
        <DAGPagination
          totalPages={totalPages}
          page={Math.min(page, totalPages)}
          pageLimit={NODE_RUNS_PER_PAGE}
          pageChange={setPage}
        />
      )}
    </section>
  );
}

function StepHeader({
  column,
  marked,
}: {
  column: NodeRunStep;
  marked: boolean;
}) {
  return (
    <th
      scope="col"
      className={cn(
        'px-2 py-1.5 text-left font-medium',
        marked && 'bg-primary/5'
      )}
    >
      <span className="flex items-center gap-2">
        <span className="truncate">{column.name}</span>
        <NodeRunCounts counts={column.counts} />
      </span>
    </th>
  );
}

/** Labels of the statuses of a node that did not run. */
const NODE_RUN_LABEL: Partial<Record<NodeRunStatus, string>> = {
  [NodeRunStatus.disabled]: 'Disabled',
  [NodeRunStatus.removed]: 'Removed',
};

function NodeRunCell({
  node,
  run,
  marked,
  open,
}: {
  node: string;
  run?: NodeRun;
  marked: boolean;
  open: (run: NodeRun, newTab: boolean) => void;
}) {
  const { ts } = useI18n();
  const cellClass = cn('px-2 py-1.5', marked && 'bg-primary/5');
  if (!run) {
    return (
      <td className={cn(cellClass, 'text-muted-foreground')}>
        <span title={ts('The step did not select this node')}>·</span>
      </td>
    );
  }
  const duration = formatElapsed(run.startedAt, run.finishedAt);
  const label = NODE_RUN_LABEL[run.status] ?? run.status;
  const content = (
    <span className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
      <NodeStatusChip status={NODE_RUN_STATUS[run.status]} size="sm">
        {label}
      </NodeStatusChip>
      {run.failedStep && (
        <span
          className="truncate text-xs text-destructive"
          title={ts('Failed at {step}', { step: run.failedStep })}
        >
          {run.failedStep}
        </span>
      )}
      {run.steps && (
        <span className="text-xs tabular-nums text-muted-foreground">
          {`${run.steps.done}/${run.steps.total}`}
        </span>
      )}
      {duration && (
        <span className="text-xs tabular-nums text-muted-foreground">
          {duration}
        </span>
      )}
    </span>
  );
  if (!run.dagRunId) {
    return <td className={cellClass}>{content}</td>;
  }
  const onClick = (event: MouseEvent) =>
    open(run, event.ctrlKey || event.metaKey);
  return (
    <td className={cellClass}>
      <button
        type="button"
        onClick={onClick}
        aria-label={ts('Open the run of {node} for {step}', {
          node,
          step: run.step,
        })}
        className="rounded text-left hover:bg-muted/60"
      >
        {content}
      </button>
    </td>
  );
}

/** Opens the child run of a node on the sub-run stack, or in a new tab. */
function useOpenNodeRun(details: ApiDAGRunDetails) {
  const openOnStack = useOpenSubRun();
  const remoteNode = useRemoteNode();
  return useCallback(
    (run: NodeRun, newTab: boolean) => {
      if (!run.dagRunId) return;
      if (!newTab && openOnStack) {
        openOnStack({ name: run.dagName || run.step, dagRunId: run.dagRunId });
        return;
      }
      window.open(
        buildDAGRunPageURL({
          rootDAGRunName: details.rootDAGRunName || details.name,
          rootDAGRunId: details.rootDAGRunId || details.dagRunId,
          subDAGRunId: run.dagRunId,
          remoteNode,
        }),
        '_blank'
      );
    },
    [details, openOnStack, remoteNode]
  );
}
