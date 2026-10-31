// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { FileQuestion, Maximize2 } from 'lucide-react';
import {
  lazy,
  Suspense,
  useMemo,
  type ReactElement,
  type ReactNode,
} from 'react';
import { Link, useNavigate } from 'react-router-dom';
import type { components } from '@/api/v1/schema';
import { EmptyState, LoadErrorState } from '@/components/PageStates';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { DialogTitle } from '@/components/ui/dialog';
import RelativeTime from '@/components/ui/relative-time';
import StatusChip from '@/components/ui/status-chip';
import { useCanExecuteForWorkspace } from '@/contexts/AuthContext';
import { RunMetaLine } from '@/features/dag-runs/components/common/RunMetaLine';
import DAGActions from '@/features/dags/components/common/DAGActions';
import {
  RunError,
  RunNodeProgress,
  RunStatusMessage,
} from '@/features/dags/components/common/RunSummaryBlocks';
import { ScheduleLine } from '@/features/dags/components/dag-list/ScheduleSummary';
import { PanelHeader } from '@/features/dags/components/step-details/PanelShell';
import { buildRunOverlay } from '@/features/visual-editor/run/runOverlay';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { useI18n } from '@/i18n/I18nProvider';
import { isNotFoundQueryError } from '@/lib/queryErrors';
import {
  withoutWorkspaceLabels,
  workspaceNameFromLabels,
} from '@/lib/workspace';
import { peekPageURL, type PeekTarget } from './peekParams';
import { usePeekData } from './usePeekData';

// Lazy so that a list page loads the canvas only once a peek shows it.
const DetailsCanvas = lazy(
  () => import('@/features/visual-editor/DetailsCanvas')
);

type DAGRunDetails = components['schemas']['DAGRunDetails'];
type DAGFile = components['schemas']['DAGFile'];

const GRAPH_CLASS = 'h-[60vh] min-h-[360px] flex-none';

type Props = {
  target: PeekTarget;
  remoteNode: string;
  onClose(): void;
  /** Shows another run of the workflow in place of the shown one. */
  onSelectRun(run: DAGRunDetails): void;
};

/**
 * The content of the peek: the workflow of a target, the run on show and the
 * recent runs. It only reads; editing stays on the full page.
 */
export function WorkflowPeek({
  target,
  remoteNode,
  onClose,
  onSelectRun,
}: Props): ReactElement {
  const { ts } = useI18n();
  const navigate = useNavigate();
  const { fileName, workflow, shown, runError, refreshRun, recentRuns } =
    usePeekData(target, remoteNode);
  const data = workflow.data;
  const dag = data?.dag;
  const labels = [...(dag?.labels ?? []), ...(dag?.tags ?? [])];
  const canExecute = useCanExecuteForWorkspace(workspaceNameFromLabels(labels));
  const overlay = useMemo(() => buildRunOverlay(shown), [shown]);

  let body: ReactNode;
  if (target.run && !shown) {
    body = !runError ? (
      <p role="status" className="text-sm text-muted-foreground">
        {ts('Loading run...')}
      </p>
    ) : isNotFoundQueryError(runError) ? (
      <EmptyState
        icon={<FileQuestion />}
        title={ts('Run not found')}
        description={ts('This run may have been deleted.')}
      />
    ) : (
      <LoadErrorState
        title={ts("Couldn't load this run")}
        error={runError}
        onRetry={() => void refreshRun()}
      />
    );
  } else if (!target.run && workflow.error) {
    body = isNotFoundQueryError(workflow.error) ? (
      <EmptyState
        icon={<FileQuestion />}
        title={ts('Workflow not found')}
        description={
          <I18nTemplate
            text="No workflow named {name} exists. It may have been renamed or deleted."
            values={{ name: <code>{target.dag}</code> }}
          />
        }
      />
    ) : (
      <LoadErrorState
        title={ts("Couldn't load this workflow")}
        error={workflow.error}
        onRetry={() => void workflow.mutate()}
      />
    );
  } else if (!target.run && !data) {
    body = (
      <p role="status" className="text-sm text-muted-foreground">
        {ts('Loading workflow...')}
      </p>
    );
  } else {
    body = (
      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_18rem]">
        <div className="flex min-w-0 flex-col gap-4">
          {shown ? (
            <RunSummary
              run={shown}
              heading={ts(target.run ? 'Run' : 'Latest run')}
            />
          ) : (
            <p className="text-sm text-muted-foreground">{ts('No runs yet')}</p>
          )}
          {dag ? (
            <Suspense
              fallback={
                <div className={`${GRAPH_CLASS} rounded-md bg-muted/30`} />
              }
            >
              <DetailsCanvas
                dag={dag}
                overlay={overlay}
                className={GRAPH_CLASS}
                onSelectStep={(step) => {
                  if (step) navigate(peekPageURL(target, remoteNode, step));
                }}
              />
            </Suspense>
          ) : (
            // The workflow of a run can load after the run.
            !data &&
            !workflow.error && (
              <div className={`${GRAPH_CLASS} rounded-md bg-muted/30`} />
            )
          )}
        </div>
        <div className="flex min-w-0 flex-col gap-4">
          {data && dag && (
            <WorkflowFacts
              dagFile={{
                fileName,
                dag,
                latestDAGRun: data.latestDAGRun,
                nextRun: dag.nextRun,
                suspended: data.suspended,
                pinned: data.pinned,
                errors: data.errors,
              }}
              labels={withoutWorkspaceLabels(labels)}
            />
          )}
          {recentRuns.length > 0 && (
            <RecentRuns
              runs={recentRuns}
              currentId={shown?.dagRunId}
              onSelect={onSelectRun}
            />
          )}
        </div>
      </div>
    );
  }

  return (
    <>
      <PanelHeader closeLabel={ts('Close')} onClose={onClose}>
        <div className="min-w-0 flex-1 leading-tight">
          <DialogTitle className="truncate text-foreground">
            {dag?.name ?? shown?.name ?? target.dag}
          </DialogTitle>
          {dag?.description && (
            <p className="truncate text-xs text-muted-foreground">
              {dag.description}
            </p>
          )}
        </div>
      </PanelHeader>
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border px-3 py-2">
        <Button asChild variant="primary" size="sm">
          <Link to={peekPageURL(target, remoteNode)}>
            <Maximize2 aria-hidden="true" />
            {ts('Open workflow')}
          </Link>
        </Button>
        {dag && canExecute && (
          <DAGActions
            dag={dag}
            status={shown ?? data?.latestDAGRun}
            fileName={fileName}
            refresh={() => {
              void workflow.mutate();
              if (target.run) void refreshRun();
            }}
          />
        )}
      </div>
      <div className="min-h-0 flex-1 overflow-auto p-4">{body}</div>
    </>
  );
}

function RunSummary({
  run,
  heading,
}: {
  run: DAGRunDetails;
  heading: string;
}): ReactElement {
  return (
    <section className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <h3 className="text-sm font-medium text-foreground">{heading}</h3>
        <StatusChip status={run.status} size="sm">
          {run.statusLabel}
        </StatusChip>
        <RunStatusMessage status={run.status} />
      </div>
      <RunMetaLine dagRun={run} />
      <div className="flex items-center gap-2">
        <RunNodeProgress nodes={run.nodes} />
      </div>
      <RunError error={run.error} />
    </section>
  );
}

function WorkflowFacts({
  dagFile,
  labels,
}: {
  dagFile: DAGFile;
  labels: string[];
}): ReactElement {
  const { ts } = useI18n();
  return (
    <dl className="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-3 gap-y-2 text-xs">
      <dt className="text-muted-foreground">{ts('Schedule')}</dt>
      <dd className="min-w-0">
        <ScheduleLine dag={dagFile} />
      </dd>
      {labels.length > 0 && (
        <>
          <dt className="text-muted-foreground">{ts('Labels')}</dt>
          <dd className="flex min-w-0 flex-wrap gap-1">
            {labels.map((label) => (
              <Badge
                key={label}
                variant="outline"
                className="rounded border-transparent bg-muted font-normal text-muted-foreground"
              >
                {label}
              </Badge>
            ))}
          </dd>
        </>
      )}
    </dl>
  );
}

function RecentRuns({
  runs,
  currentId,
  onSelect,
}: {
  runs: DAGRunDetails[];
  currentId?: string;
  onSelect(run: DAGRunDetails): void;
}): ReactElement {
  const { ts } = useI18n();
  return (
    <section className="flex flex-col gap-1">
      <h3 className="text-sm font-medium text-foreground">
        {ts('Recent runs')}
      </h3>
      <ul className="flex flex-col">
        {runs.map((run) => (
          <li key={run.dagRunId}>
            <button
              type="button"
              aria-current={run.dagRunId === currentId ? 'true' : undefined}
              onClick={() => onSelect(run)}
              className="flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1 text-left text-xs outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring aria-[current=true]:bg-muted"
            >
              <StatusChip status={run.status} size="xs">
                {run.statusLabel}
              </StatusChip>
              <RelativeTime
                timestamp={run.startedAt || run.queuedAt}
                compact
                className="shrink-0 text-muted-foreground"
              />
              <code className="min-w-0 truncate font-mono text-muted-foreground">
                {run.dagRunId}
              </code>
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}
