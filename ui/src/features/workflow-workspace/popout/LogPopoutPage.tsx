// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext } from 'react';
import { Link } from 'react-router-dom';
import { Stream } from '@/api/v1/schema';
import { PageLoading } from '@/components/PageStates';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { buildDAGPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import ExecutionLog from '@/features/dags/components/dag-execution/ExecutionLog';
import StepLog from '@/features/dags/components/dag-execution/StepLog';
import { DAGContext } from '@/features/dags/contexts/DAGContext';
import { useI18n } from '@/i18n/I18nProvider';
import type { ApiDAGRunDetails, DAGPageData } from '../contracts';
import { useWorkspaceUrl } from '../state/workspaceUrl';
import { PopoutBar, withCarry } from './PopoutBar';

export interface LogPopoutPageProps {
  fileName: string;
  dagData: DAGPageData;
  /** 'log' (step log) or 'dagRun-log' (run scheduler log). */
  tab: string | undefined;
  /**
   * The run that the page loaded for ?dagRunId, or for the sub-run of
   * ?subDAGRunId. Absent: the latest run of the workflow.
   */
  dagRun?: ApiDAGRunDetails;
  /** True while the run that the URL names loads. */
  runLoading?: boolean;
  /** Reloads the workflow and the run; the page DAGContext refresh when absent. */
  refresh?(): void | Promise<unknown>;
}

/**
 * Full-page log in workspace mode: /dags/:f/log shows the log of one step,
 * /dags/:f/dagRun-log the scheduler log of the run.
 */
export function LogPopoutPage({
  fileName,
  dagData,
  tab,
  dagRun,
  runLoading = false,
  refresh,
}: LogPopoutPageProps) {
  const { ts } = useI18n();
  const remoteNode = useRemoteNode();
  const dagContext = useContext(DAGContext);
  const [url] = useWorkspaceUrl();
  const kind: 'step' | 'run' = tab === 'dagRun-log' ? 'run' : 'step';
  const shown = dagRun ?? (runLoading ? undefined : dagData.latestDAGRun);
  // The latest run of a workflow that never ran can come without an ID.
  const run = shown?.dagRunId ? shown : undefined;
  const dagName = dagData.dag?.name ?? fileName;
  const step = url.step;
  const stream = url.stream === 'stderr' ? Stream.stderr : Stream.stdout;

  let body;
  if (runLoading) {
    body = <PageLoading />;
  } else if (!run) {
    body = <p className="text-sm text-muted-foreground">{ts('No runs yet')}</p>;
  } else if (kind === 'run') {
    body = (
      <ExecutionLog
        name={run.name || dagName}
        dagRunId={run.dagRunId}
        dagRun={run}
      />
    );
  } else if (!step) {
    body = (
      <div className="flex flex-col items-start gap-2 text-sm text-muted-foreground">
        <p>
          {ts('This link names no step. Open a step log from the workflow.')}
        </p>
        <Link
          to={withCarry(
            buildDAGPageURL({
              fileName,
              remoteNode,
              run: run.rootDAGRunId || run.dagRunId,
            }),
            url.carry
          )}
          className="font-medium text-primary hover:underline"
        >
          {ts('Open the workflow')}
        </Link>
      </div>
    );
  } else {
    body = (
      <StepLog
        dagName={run.name || dagName}
        dagRunId={run.dagRunId}
        stepName={step}
        dagRun={run}
        stream={stream}
        node={run.nodes?.find((node) => node.step.name === step)}
      />
    );
  }

  return (
    <div
      data-testid="ww-popout"
      data-tab={tab}
      className="flex h-full min-h-0 w-full min-w-0 flex-col gap-3 p-4"
    >
      <PopoutBar
        fileName={fileName}
        dagName={dagName}
        run={run}
        kind={kind}
        step={step}
        carry={url.carry}
        refresh={refresh ?? dagContext.refresh}
      />
      <div className="flex min-h-0 flex-1 flex-col">{body}</div>
    </div>
  );
}
