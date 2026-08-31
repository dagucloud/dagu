// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  ArrowLeft,
  CheckCircle2,
  Layers,
  RefreshCw,
  Trash2,
} from 'lucide-react';
import React from 'react';
import { Link, useParams } from 'react-router-dom';
import { components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { RefreshButton } from '@/components/ui/refresh-button';
import { AppBarContext } from '@/contexts/AppBarContext';
import { DAGRunDetailsModal } from '@/features/dag-runs/components/dag-run-details';
import { formatQueuedCount } from '@/features/queues/components/QueueCard';
import QueueRunsTable from '@/features/queues/components/QueueRunsTable';
import {
  QueueBatchResult,
  useQueueBatchDequeue,
} from '@/features/queues/hooks/useQueueBatchDequeue';
import { useQueuedItemsFeed } from '@/features/queues/hooks/useQueuedItemsFeed';
import { useQueueSelection } from '@/features/queues/hooks/useQueueSelection';
import { useQuery } from '@/hooks/api';
import Title from '@/components/ui/title';

function useAutoLoadMore(
  sentinelRef: React.RefObject<HTMLDivElement | null>,
  enabled: boolean,
  onLoadMore: () => void
) {
  React.useEffect(() => {
    const element = sentinelRef.current;
    if (!element || !enabled || typeof IntersectionObserver === 'undefined') {
      return;
    }

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry?.isIntersecting) {
          onLoadMore();
        }
      },
      { threshold: 0.1 }
    );

    observer.observe(element);
    return () => observer.disconnect();
  }, [enabled, onLoadMore, sentinelRef]);
}

function decodeQueueName(rawName: string | undefined): string {
  if (!rawName) {
    return '';
  }
  try {
    return decodeURIComponent(rawName);
  } catch {
    return rawName;
  }
}

function queueRefreshToken(
  queue: components['schemas']['Queue'] | undefined
): string {
  if (!queue) {
    return '';
  }
  return JSON.stringify({
    name: queue.name,
    queuedCount: queue.queuedCount,
    running: (queue.running ?? []).map(
      (dagRun) => `${dagRun.name}:${dagRun.dagRunId}:${dagRun.status}`
    ),
  });
}

function QueueDetailsPage() {
  const { name } = useParams();
  const queueName = React.useMemo(() => decodeQueueName(name), [name]);
  const appBarContext = React.useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const [modalDAGRun, setModalDAGRun] = React.useState<{
    name: string;
    dagRunId: string;
  } | null>(null);
  const sentinelRef = React.useRef<HTMLDivElement>(null);

  React.useEffect(() => {
    appBarContext.setTitle(queueName ? `Queue ${queueName}` : 'Queue');
  }, [appBarContext, queueName]);

  const {
    data: queue,
    error,
    isLoading,
    isValidating,
    mutate,
  } = useQuery(
    '/queues/{name}',
    queueName
      ? {
          params: {
            path: { name: queueName },
            query: {
              remoteNode,
            },
          },
        }
      : null,
    {
      refreshInterval: 3000,
      keepPreviousData: true,
      revalidateIfStale: false,
      revalidateOnFocus: false,
      revalidateOnReconnect: false,
    }
  );

  const refreshToken = React.useMemo(() => queueRefreshToken(queue), [queue]);
  const mayHaveQueuedItems =
    (queue?.queuedCount ?? 0) > 0 || queue?.queuedCountCapped === true;
  const {
    items: queuedItems,
    error: queuedItemsError,
    hasMore,
    isLoading: isQueuedItemsLoading,
    isLoadingMore,
    loadMore,
    reload,
  } = useQueuedItemsFeed({
    enabled: mayHaveQueuedItems,
    queueName,
    refreshToken,
  });

  const handleLoadMore = React.useCallback(() => {
    void loadMore();
  }, [loadMore]);

  useAutoLoadMore(
    sentinelRef,
    hasMore && !isLoadingMore && !queuedItemsError,
    handleLoadMore
  );

  const {
    clearSelection,
    isSelected,
    replaceSelection,
    selectAllLoaded,
    selectedCount,
    selectedRuns,
    toggleSelection,
  } = useQueueSelection(queuedItems);

  const {
    activeBatch,
    closeDialog,
    isRunning,
    openBatchDialog,
    phase,
    progress,
    submitBatchDequeue,
  } = useQueueBatchDequeue({
    onActionComplete: async () => {
      await Promise.all([mutate(), reload()]);
    },
    onReplaceSelection: replaceSelection,
    selectedRuns,
  });

  const handleRefresh = React.useCallback(async () => {
    await Promise.all([mutate(), reload()]);
  }, [mutate, reload]);

  const handleDAGRunClick = React.useCallback(
    (dagRun: components['schemas']['DAGRunSummary']) => {
      setModalDAGRun({ name: dagRun.name, dagRunId: dagRun.dagRunId });
    },
    []
  );

  const headerCheckboxState =
    queuedItems.length === 0
      ? false
      : selectedCount === queuedItems.length
        ? true
        : selectedCount > 0
          ? 'indeterminate'
          : false;

  const selectionSummary =
    selectedCount === 0
      ? `${queuedItems.length} loaded`
      : `${selectedCount} selected of ${queuedItems.length} loaded`;

  const totalBatchCount = activeBatch?.snapshot.length ?? 0;
  const isDialogLocked = phase === 'running' || progress.isRefreshing;
  const isProcessing = phase === 'running' || phase === 'complete';
  const utilization = queue?.maxConcurrency
    ? Math.round(((queue.runningCount || 0) / queue.maxConcurrency) * 100)
    : null;

  const renderResultDetails = (result: QueueBatchResult): React.JSX.Element => {
    if (!result.ok) {
      return <div className="mt-2 text-sm text-error">{result.error}</div>;
    }

    return (
      <div className="mt-2 text-sm text-muted-foreground">
        {result.message ?? 'Dequeue request accepted.'}
      </div>
    );
  };

  if (!queueName) {
    return (
      <div className="flex h-full items-center justify-center">
        <div className="space-y-3 text-center">
          <Layers className="mx-auto h-10 w-10 text-muted-foreground" />
          <p className="text-sm text-muted-foreground">
            Queue name is missing.
          </p>
          <Button variant="outline" asChild>
            <Link to="/queues">Back to queues</Link>
          </Button>
        </div>
      </div>
    );
  }

  if (error && !queue) {
    const errorData = error as components['schemas']['Error'];
    return (
      <div className="flex h-full items-center justify-center">
        <div className="space-y-3 text-center">
          <Layers className="mx-auto h-10 w-10 text-muted-foreground" />
          <p className="text-sm text-muted-foreground">
            {errorData?.message || 'Failed to load queue details'}
          </p>
          <Button variant="outline" asChild>
            <Link to="/queues">Back to queues</Link>
          </Button>
        </div>
      </div>
    );
  }

  return (
    <>
      <div className="flex h-full max-w-7xl flex-col gap-4 overflow-hidden">
        <Title>Queue</Title>

        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="ghost" size="sm" asChild>
              <Link to="/queues">
                <ArrowLeft className="h-4 w-4" />
                Back
              </Link>
            </Button>
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-lg font-medium">
                {queue?.name || queueName}
              </span>
            </div>
          </div>

          <div className="flex items-center gap-2">
            <RefreshButton onRefresh={handleRefresh} />
          </div>
        </div>

        <div className="flex flex-wrap items-baseline gap-x-5 gap-y-1 text-sm text-muted-foreground">
          <div className="flex items-baseline gap-1">
            <span className="text-lg font-light tabular-nums text-foreground">
              {isLoading && !queue ? '-' : queue?.runningCount || 0}
            </span>
            <span className="text-xs">running</span>
          </div>
          <div className="flex items-baseline gap-1">
            <span className="text-lg font-light tabular-nums text-foreground">
              {isLoading && !queue
                ? '-'
                : formatQueuedCount(
                    queue?.queuedCount || 0,
                    queue?.queuedCountCapped === true
                  )}
            </span>
            <span className="text-xs">queued</span>
          </div>
          {queue?.maxConcurrency && (
            <div className="flex items-baseline gap-1">
              <span className="text-lg font-light tabular-nums text-foreground">
                {queue.runningCount || 0}/{queue.maxConcurrency}
              </span>
              <span className="text-xs">capacity</span>
            </div>
          )}
          {utilization !== null && (
            <div className="flex items-baseline gap-1">
              <span className="text-lg font-light tabular-nums text-foreground">
                {utilization}%
              </span>
              <span className="text-xs">util</span>
            </div>
          )}
          {isValidating && queue && (
            <span className="text-xs text-muted-foreground">Refreshing...</span>
          )}
        </div>

        <div className="flex-1 min-h-0 overflow-auto space-y-4 pr-1">
          <section className="card-obsidian">
            <div className="border-b px-3 py-2">
              <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                Running ({queue?.runningCount || 0})
              </span>
            </div>
            <div className="px-3 py-2 bg-muted/10">
              {queue && queue.running.length > 0 ? (
                <QueueRunsTable
                  items={queue.running}
                  onDAGRunClick={handleDAGRunClick}
                />
              ) : (
                <div className="rounded-md border border-dashed px-3 py-4 text-sm text-muted-foreground">
                  No DAG runs are currently executing in this queue.
                </div>
              )}
            </div>
          </section>

          <section className="card-obsidian">
            <div className="border-b px-3 py-2">
              <div className="flex flex-col gap-2 lg:flex-row lg:items-center lg:justify-between">
                <div className="flex flex-wrap items-center gap-3">
                  <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                    Queued (
                    {formatQueuedCount(
                      queue?.queuedCount || 0,
                      queue?.queuedCountCapped === true
                    )}
                    )
                  </span>
                  <span className="text-xs text-muted-foreground">
                    {selectionSummary}
                  </span>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={selectAllLoaded}
                    disabled={queuedItems.length === 0 || isRunning}
                  >
                    Select all loaded
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={clearSelection}
                    disabled={selectedCount === 0 || isRunning}
                  >
                    Clear selection
                  </Button>
                  <Button
                    size="sm"
                    onClick={openBatchDialog}
                    disabled={selectedCount === 0 || isRunning}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                    <span className="ml-1">Dequeue selected</span>
                  </Button>
                </div>
              </div>
            </div>

            <div className="px-3 py-2 bg-muted/10">
              {isQueuedItemsLoading && queuedItems.length === 0 ? (
                <div className="flex items-center justify-center py-8 text-sm text-muted-foreground">
                  Loading queued items...
                </div>
              ) : queuedItems.length > 0 ? (
                <>
                  <QueueRunsTable
                    items={queuedItems}
                    onDAGRunClick={handleDAGRunClick}
                    selectable
                    disableSelection={isRunning}
                    headerCheckboxState={headerCheckboxState}
                    isSelected={isSelected}
                    onToggleAll={(checked) => {
                      if (checked) {
                        selectAllLoaded();
                        return;
                      }
                      clearSelection();
                    }}
                    onToggleSelection={(dagRun) =>
                      toggleSelection({
                        name: dagRun.name,
                        dagRunId: dagRun.dagRunId,
                      })
                    }
                    showQueuedAt
                  />

                  {queuedItemsError && (
                    <div className="mt-3 flex flex-wrap items-center justify-between gap-2 rounded-md border border-error/30 bg-error-muted px-3 py-2 text-sm text-error">
                      <span>{queuedItemsError}</span>
                      <Button size="sm" variant="outline" onClick={reload}>
                        Retry
                      </Button>
                    </div>
                  )}

                  {isLoadingMore && (
                    <div className="mt-3 flex items-center gap-2 text-sm text-muted-foreground">
                      <RefreshCw className="h-4 w-4 animate-spin" />
                      Loading more queued items...
                    </div>
                  )}

                  {hasMore && <div ref={sentinelRef} className="h-4 w-full" />}

                  {!hasMore && (
                    <div className="mt-3 text-xs text-muted-foreground">
                      End of queued items
                    </div>
                  )}
                </>
              ) : mayHaveQueuedItems ? (
                <div className="space-y-3 rounded-md border border-dashed px-3 py-4 text-sm text-muted-foreground">
                  <div>
                    No visible queued items were returned for this queue.
                  </div>
                  {queuedItemsError && (
                    <div className="flex flex-wrap items-center gap-2">
                      <span>{queuedItemsError}</span>
                      <Button size="sm" variant="outline" onClick={reload}>
                        Retry
                      </Button>
                    </div>
                  )}
                  {!queuedItemsError && hasMore && (
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={handleLoadMore}
                      disabled={isLoadingMore}
                    >
                      {isLoadingMore && (
                        <RefreshCw className="h-3.5 w-3.5 animate-spin" />
                      )}
                      {isLoadingMore ? 'Scanning...' : 'Continue scanning'}
                    </Button>
                  )}
                </div>
              ) : (
                <div className="rounded-md border border-dashed px-3 py-4 text-sm text-muted-foreground">
                  No queued items in this queue.
                </div>
              )}
            </div>
          </section>
        </div>
      </div>

      <Dialog
        open={phase !== null}
        onOpenChange={(open) => {
          if (!open) {
            closeDialog();
          }
        }}
      >
        <DialogContent
          hideCloseButton
          className="sm:max-w-2xl"
          onPointerDownOutside={(event) => {
            if (isDialogLocked) {
              event.preventDefault();
            }
          }}
          onEscapeKeyDown={(event) => {
            if (isDialogLocked) {
              event.preventDefault();
            }
          }}
        >
          <DialogHeader>
            <DialogTitle>Dequeue selected</DialogTitle>
            <DialogDescription>
              {phase === 'confirm'
                ? `Submit ${totalBatchCount} dequeue request${totalBatchCount === 1 ? '' : 's'} using the existing DAG-run API.`
                : isProcessing
                  ? `Processing ${totalBatchCount} request${totalBatchCount === 1 ? '' : 's'} using the existing DAG-run API.`
                  : ''}
            </DialogDescription>
          </DialogHeader>

          {phase === 'confirm' && activeBatch && (
            <div className="space-y-3">
              <p className="text-sm text-foreground">
                Do you want to dequeue {activeBatch.snapshot.length} selected
                DAG run{activeBatch.snapshot.length === 1 ? '' : 's'}?
              </p>
              <div className="max-h-56 space-y-2 overflow-y-auto rounded-md border bg-muted/20 p-3">
                {activeBatch.snapshot.map((dagRun) => (
                  <div
                    key={`${dagRun.name}-${dagRun.dagRunId}`}
                    className="text-sm"
                  >
                    <div className="font-medium">{dagRun.name}</div>
                    <div className="font-mono text-xs text-muted-foreground">
                      {dagRun.dagRunId}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}

          {isProcessing && activeBatch && (
            <div className="space-y-4">
              <div className="rounded-md border bg-muted/20 p-4">
                <div className="flex items-start gap-3">
                  {isDialogLocked ? (
                    <RefreshCw className="mt-0.5 h-5 w-5 animate-spin text-muted-foreground" />
                  ) : (
                    <CheckCircle2 className="mt-0.5 h-5 w-5 text-success" />
                  )}
                  <div className="min-w-0 flex-1 space-y-2">
                    <div className="text-sm font-medium text-foreground">
                      {phase === 'running'
                        ? 'Submitting requests...'
                        : progress.isRefreshing
                          ? 'Refreshing queue data...'
                          : 'Finished submitting requests'}
                    </div>
                    <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
                      <span className="font-mono tabular-nums">
                        {progress.processedCount}/{totalBatchCount} processed
                      </span>
                      <span>{progress.successCount} succeeded</span>
                      <span>{progress.skippedCount} skipped</span>
                      <span>{progress.failureCount} failed</span>
                    </div>
                  </div>
                </div>
              </div>

              <div className="rounded-md border bg-muted/20 p-3">
                <div className="mb-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                  Current item
                </div>
                {progress.currentItem ? (
                  <>
                    <div className="font-medium">
                      {progress.currentItem.name}
                    </div>
                    <div className="font-mono text-xs text-muted-foreground">
                      {progress.currentItem.dagRunId}
                    </div>
                  </>
                ) : (
                  <div className="text-sm text-muted-foreground">
                    {progress.isRefreshing
                      ? 'Refreshing the queue detail page'
                      : 'All requests have been submitted'}
                  </div>
                )}
              </div>

              {progress.refreshError && (
                <div className="rounded-md border border-error/30 bg-error-muted p-3 text-sm text-error">
                  {progress.refreshError}
                </div>
              )}

              <div className="rounded-md border">
                <div className="border-b px-3 py-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                  Results
                </div>
                <div className="min-h-40 max-h-[45vh] space-y-3 overflow-y-auto p-3">
                  {progress.results.length === 0 ? (
                    <div className="flex min-h-32 items-center justify-center text-sm text-muted-foreground">
                      Results will appear here as each request finishes.
                    </div>
                  ) : (
                    progress.results.map((result, index) => (
                      <div
                        key={`${result.name}-${result.dagRunId}-${index}`}
                        className="rounded-md border p-3"
                      >
                        <div className="flex items-start justify-between gap-3">
                          <div className="min-w-0">
                            <div className="font-medium">{result.name}</div>
                            <div className="font-mono text-xs text-muted-foreground">
                              {result.dagRunId}
                            </div>
                          </div>
                          <div
                            className={
                              result.ok
                                ? 'text-xs font-medium text-success'
                                : 'text-xs font-medium text-error'
                            }
                          >
                            {result.ok
                              ? result.skipped
                                ? 'Skipped'
                                : 'Succeeded'
                              : 'Failed'}
                          </div>
                        </div>
                        {renderResultDetails(result)}
                      </div>
                    ))
                  )}
                </div>
              </div>
            </div>
          )}

          <DialogFooter>
            {phase === 'confirm' && activeBatch && (
              <>
                <Button variant="outline" onClick={closeDialog}>
                  Cancel
                </Button>
                <Button onClick={submitBatchDequeue}>
                  Dequeue {activeBatch.snapshot.length} Run
                  {activeBatch.snapshot.length === 1 ? '' : 's'}
                </Button>
              </>
            )}
            {(phase === 'running' || progress.isRefreshing) && (
              <Button disabled>
                <RefreshCw className="mr-2 h-4 w-4 animate-spin" />
                {phase === 'running' ? 'Submitting...' : 'Refreshing...'}
              </Button>
            )}
            {phase === 'complete' && !progress.isRefreshing && (
              <Button variant="outline" onClick={closeDialog}>
                Close
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {modalDAGRun && (
        <DAGRunDetailsModal
          name={modalDAGRun.name}
          dagRunId={modalDAGRun.dagRunId}
          isOpen={Boolean(modalDAGRun)}
          onClose={() => setModalDAGRun(null)}
        />
      )}
    </>
  );
}

export default QueueDetailsPage;
