// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { X } from 'lucide-react';
import { Status } from '@/api/v1/schema';
import LoadingIndicator from '@/components/ui/loading-indicator';
import StatusChip from '@/components/ui/status-chip';
import { Button } from '@/components/ui/button';
import { useBoundedDAGRunDetails } from '@/features/dag-runs/hooks/useBoundedDAGRunDetails';
import { useI18n } from '@/i18n/I18nProvider';

const RunProgressModal = React.lazy(() => import('./RunProgressModal'));

type RunProgressItem = {
  id: string;
  dagName: string;
  dagRunId: string;
  remoteNode: string;
};

export type AddRunProgressInput = Omit<RunProgressItem, 'id'>;

const RUN_PROGRESS_EVENT = 'dagu:run-progress';
const MAX_STACKED_RUNS = 5;
const SUCCESS_DISMISS_MS = 5000;

function shouldAutoDismiss(status: Status | undefined): boolean {
  return status === Status.Success || status === Status.PartialSuccess;
}

function runKey(run: AddRunProgressInput): string {
  return `${run.remoteNode}:${run.dagName}:${run.dagRunId}`;
}

export function pushRunProgress(run: AddRunProgressInput): void {
  window.dispatchEvent(
    new CustomEvent<AddRunProgressInput>(RUN_PROGRESS_EVENT, { detail: run })
  );
}

function RunProgressStack() {
  const { ts } = useI18n();
  const [runs, setRuns] = React.useState<RunProgressItem[]>([]);
  const [expandedRunId, setExpandedRunId] = React.useState<string | null>(null);
  const expandedRun = runs.find((run) => run.id === expandedRunId) ?? null;
  const visibleRuns = runs.filter((run) => run.id !== expandedRunId);

  const addRun = React.useCallback((run: AddRunProgressInput) => {
    const item = { ...run, id: runKey(run) };
    setRuns((current) =>
      [item, ...current.filter((existing) => existing.id !== item.id)].slice(
        0,
        MAX_STACKED_RUNS
      )
    );
  }, []);

  React.useEffect(() => {
    const handleRunProgress = (event: Event) => {
      const run = (event as CustomEvent<AddRunProgressInput>).detail;
      if (!run?.dagName || !run.dagRunId || !run.remoteNode) {
        return;
      }
      addRun(run);
    };

    window.addEventListener(RUN_PROGRESS_EVENT, handleRunProgress);
    return () =>
      window.removeEventListener(RUN_PROGRESS_EVENT, handleRunProgress);
  }, [addRun]);

  const dismissRun = React.useCallback((id: string) => {
    setRuns((current) => current.filter((run) => run.id !== id));
    setExpandedRunId((current) => (current === id ? null : current));
  }, []);

  return (
    <>
      {visibleRuns.length > 0 && (
        <div
          aria-label={ts('Run progress stack')}
          className="pointer-events-none fixed bottom-4 right-4 z-40 flex max-h-[calc(100dvh-2rem)] w-[min(24rem,calc(100vw-2rem))] flex-col-reverse gap-2 overflow-y-auto"
        >
          {visibleRuns.map((run) => (
            <RunProgressCard
              key={run.id}
              run={run}
              onOpen={() => setExpandedRunId(run.id)}
              onDismiss={() => dismissRun(run.id)}
            />
          ))}
        </div>
      )}
      {expandedRun && (
        <React.Suspense fallback={null}>
          <RunProgressModal
            dagName={expandedRun.dagName}
            dagRunId={expandedRun.dagRunId}
            remoteNode={expandedRun.remoteNode}
            visible={true}
            dismissModal={() => setExpandedRunId(null)}
          />
        </React.Suspense>
      )}
    </>
  );
}

function RunProgressCard({
  run,
  onOpen,
  onDismiss,
}: {
  run: RunProgressItem;
  onOpen: () => void;
  onDismiss: () => void;
}) {
  const { ts } = useI18n();
  const { data: dagRun, isLoading } = useBoundedDAGRunDetails({
    target: {
      remoteNode: run.remoteNode,
      name: run.dagName,
      dagRunId: run.dagRunId,
    },
    enabled: true,
    pollIntervalMs: 2000,
  });

  React.useEffect(() => {
    if (!shouldAutoDismiss(dagRun?.status)) {
      return;
    }
    const timer = window.setTimeout(onDismiss, SUCCESS_DISMISS_MS);
    return () => window.clearTimeout(timer);
  }, [dagRun?.status, onDismiss]);

  return (
    <div className="pointer-events-auto relative overflow-hidden rounded-lg border border-border bg-card text-card-foreground shadow-lg">
      <button
        type="button"
        aria-label={ts('Open run progress for {dagRunId}', {
          dagRunId: run.dagRunId,
        })}
        onClick={onOpen}
        className="block w-full px-4 py-3 pr-11 text-left transition-colors hover:bg-muted/70 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
      >
        <div className="flex items-center gap-2">
          <span className="min-w-0 flex-1 truncate text-sm font-medium">
            {dagRun?.name || run.dagName}
          </span>
          {dagRun?.status != null ? (
            <StatusChip status={dagRun.status} size="xs">
              {ts(dagRun.statusLabel)}
            </StatusChip>
          ) : isLoading ? (
            <LoadingIndicator />
          ) : null}
        </div>
        <div className="mt-1 truncate font-mono text-xs text-muted-foreground">
          {dagRun?.dagRunId || run.dagRunId}
        </div>
      </button>
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        aria-label={ts('Dismiss run progress')}
        className="absolute right-2 top-2"
        onClick={onDismiss}
      >
        <X className="h-4 w-4" />
      </Button>
    </div>
  );
}

export default RunProgressStack;
