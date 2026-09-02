// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Label } from '@/components/ui/label';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useClient } from '@/hooks/api';
import { CheckCircle2, RefreshCw, Trash2 } from 'lucide-react';
import React from 'react';
import { DAGRunSelectionItem } from '../../hooks/useBulkDAGRunSelection';
import {
  BatchActionResult,
  BatchActionType,
  useDAGRunBatchSubmission,
} from '../../hooks/useDAGRunBatchSubmission';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

interface DAGRunBatchActionsProps {
  loadedCount: number;
  onActionComplete?: () => Promise<void>;
  onClearSelection: () => void;
  onReplaceSelection: (items: DAGRunSelectionItem[]) => void;
  onSelectAllLoaded: () => void;
  selectedRuns: DAGRunSelectionItem[];
}

const actionLabels: Record<BatchActionType, string> = {
  delete: 'Delete selected',
  retry: 'Retry selected',
  reschedule: 'Reschedule selected',
};

const actionVerbs: Record<BatchActionType, string> = {
  delete: 'delete',
  retry: 'retry',
  reschedule: 'reschedule',
};

function DAGRunBatchActions({
  loadedCount,
  onActionComplete,
  onClearSelection,
  onReplaceSelection,
  onSelectAllLoaded,
  selectedRuns,
}: DAGRunBatchActionsProps) {
  const { ts } = useI18n();
  const appBarContext = React.useContext(AppBarContext);
  const client = useClient();
  const {
    activeBatch,
    closeDialog,
    isRunning,
    openBatchDialog,
    phase,
    progress,
    submitBatchAction,
  } = useDAGRunBatchSubmission({
    onActionComplete,
    onReplaceSelection,
    selectedRuns,
  });
  const selectedCount = selectedRuns.length;
  const snapshot = activeBatch?.snapshot ?? [];
  const totalCount = snapshot.length;
  const isLocked = phase === 'running' || progress.isRefreshing;
  const isProcessing = phase === 'running' || phase === 'complete';
  const [specFromFile, setSpecFromFile] = React.useState(false);
  const [useCurrentDagFile, setUseCurrentDagFile] = React.useState(false);
  const [rescheduleSourceLoading, setRescheduleSourceLoading] =
    React.useState(false);

  const summaryText =
    selectedCount === 0
      ? ts('{count} loaded', { count: loadedCount })
      : ts('{selected} selected of {loaded} loaded', {
          selected: selectedCount,
          loaded: loadedCount,
        });

  React.useEffect(() => {
    if (phase !== 'confirm' || activeBatch?.action !== 'reschedule') {
      setSpecFromFile(false);
      setUseCurrentDagFile(false);
      setRescheduleSourceLoading(false);
      return;
    }

    let cancelled = false;
    setRescheduleSourceLoading(true);

    Promise.all(
      activeBatch.snapshot.map(async (dagRun) => {
        const { data } = await client.GET('/dag-runs/{name}/{dagRunId}', {
          params: {
            path: {
              name: dagRun.name,
              dagRunId: dagRun.dagRunId,
            },
            query: {
              remoteNode: appBarContext.selectedRemoteNode || 'local',
            },
          },
        });

        return Boolean(data?.dagRunDetails?.specFromFile);
      })
    )
      .then((results) => {
        if (cancelled) {
          return;
        }
        const available = results.length > 0 && results.every(Boolean);
        setSpecFromFile(available);
        setUseCurrentDagFile(available);
      })
      .catch(() => {
        if (cancelled) {
          return;
        }
        setSpecFromFile(false);
        setUseCurrentDagFile(false);
      })
      .finally(() => {
        if (!cancelled) {
          setRescheduleSourceLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [activeBatch, appBarContext.selectedRemoteNode, client, phase]);

  const renderResultDetails = (
    action: BatchActionType,
    result: BatchActionResult
  ) => {
    if (!result.ok) {
      return <div className="mt-2 text-sm text-error">{result.error}</div>;
    }

    if (action === 'delete') {
      return (
        <div className="mt-2 text-sm text-muted-foreground">
          <I18nText text={'Delete request accepted'} />
        </div>
      );
    }

    if (action === 'retry') {
      return (
        <div className="mt-2 text-sm text-muted-foreground">
          <I18nText text={'Retry request accepted'} />
        </div>
      );
    }

    return (
      <div className="mt-2 space-y-1 text-sm">
        {result.newDagRunId ? (
          <div>
            <I18nText text={'New DAG run:'} />{' '}
            <span className="font-mono">{result.newDagRunId}</span>
          </div>
        ) : (
          <div className="text-muted-foreground">
            <I18nText text={'Reschedule request accepted'} />
          </div>
        )}
        {typeof result.queued === 'boolean' && (
          <div className="text-muted-foreground">
            {result.queued ? (
              <I18nText text={'Queued for execution'} />
            ) : (
              <I18nText text={'Started immediately'} />
            )}
          </div>
        )}
      </div>
    );
  };

  const getSubmitButtonText = (
    action: BatchActionType,
    count: number
  ): string => {
    const suffix = count === 1 ? 'Run' : 'Runs';
    if (action === 'delete') {
      return ts(`Delete {count} ${suffix}`, { count });
    }
    if (action === 'retry') {
      return ts(`Retry {count} ${suffix}`, { count });
    }
    return ts(`Reschedule {count} ${suffix}`, { count });
  };

  return (
    <>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2 rounded-lg border bg-card px-3 py-2">
        <div className="text-sm text-muted-foreground">{summaryText}</div>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="outline"
            onClick={onSelectAllLoaded}
            disabled={loadedCount === 0 || isRunning}
          >
            <I18nText text={'Select all loaded'} />
          </Button>
          <Button
            variant="outline"
            onClick={onClearSelection}
            disabled={selectedCount === 0 || isRunning}
          >
            <I18nText text={'Clear selection'} />
          </Button>
          <Button
            variant="outline"
            onClick={() => openBatchDialog('retry')}
            disabled={selectedCount === 0 || isRunning}
          >
            <I18nText text={'Retry selected'} />
          </Button>
          <Button
            variant="outline"
            onClick={() => openBatchDialog('reschedule')}
            disabled={selectedCount === 0 || isRunning}
          >
            <I18nText text={'Reschedule selected'} />
          </Button>
          <Button
            variant="destructive"
            onClick={() => openBatchDialog('delete')}
            disabled={selectedCount === 0 || isRunning}
          >
            <Trash2 className="h-4 w-4" />
            <I18nText text={'Delete selected'} />
          </Button>
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
            if (isLocked) {
              event.preventDefault();
            }
          }}
          onEscapeKeyDown={(event) => {
            if (isLocked) {
              event.preventDefault();
            }
          }}
        >
          <DialogHeader>
            <DialogTitle>
              {activeBatch ? (
                ts(actionLabels[activeBatch.action])
              ) : (
                <I18nText text="Batch action" />
              )}
            </DialogTitle>
            <DialogDescription>
              {phase === 'confirm' && activeBatch
                ? ts(
                    activeBatch.snapshot.length === 1
                      ? 'Submit {count} {action} request using the existing DAG-run API.'
                      : 'Submit {count} {action} requests using the existing DAG-run API.',
                    {
                      count: activeBatch.snapshot.length,
                      action: ts(actionVerbs[activeBatch.action]),
                    }
                  )
                : isProcessing
                  ? ts(
                      totalCount === 1
                        ? 'Processing {count} request using the existing DAG-run API.'
                        : 'Processing {count} requests using the existing DAG-run API.',
                      { count: totalCount }
                    )
                  : ''}
            </DialogDescription>
          </DialogHeader>

          {phase === 'confirm' && activeBatch && (
            <div className="space-y-3">
              <p className="text-sm text-foreground">
                {ts(
                  activeBatch.snapshot.length === 1
                    ? 'Do you want to {action} {count} selected DAG run?'
                    : 'Do you want to {action} {count} selected DAG runs?',
                  {
                    count: activeBatch.snapshot.length,
                    action: ts(actionVerbs[activeBatch.action]),
                  }
                )}
              </p>
              {activeBatch.action === 'delete' && (
                <div className="rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">
                  <I18nText
                    text={
                      'This permanently removes run records, logs, artifacts, and related run data.'
                    }
                  />
                </div>
              )}
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
              {activeBatch.action === 'reschedule' && (
                <div
                  role="button"
                  tabIndex={rescheduleSourceLoading || !specFromFile ? -1 : 0}
                  aria-disabled={rescheduleSourceLoading || !specFromFile}
                  onClick={() => {
                    if (rescheduleSourceLoading || !specFromFile) {
                      return;
                    }
                    setUseCurrentDagFile((value) => !value);
                  }}
                  onKeyDown={(event) => {
                    if (
                      rescheduleSourceLoading ||
                      !specFromFile ||
                      (event.key !== 'Enter' && event.key !== ' ')
                    ) {
                      return;
                    }
                    event.preventDefault();
                    setUseCurrentDagFile((value) => !value);
                  }}
                  className="flex w-full items-start gap-3 rounded-md border px-3 py-3 text-left transition-colors hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring aria-disabled:cursor-not-allowed aria-disabled:opacity-70 aria-disabled:hover:bg-transparent"
                >
                  <I18nProps>
                    <Checkbox
                      id="use-current-dag-file-batch"
                      aria-label="Use original DAG file"
                      checked={useCurrentDagFile}
                      disabled={rescheduleSourceLoading || !specFromFile}
                      onCheckedChange={(checked) =>
                        setUseCurrentDagFile(checked as boolean)
                      }
                      className="mt-0.5 h-5 w-5 border-border pointer-events-none"
                    />
                  </I18nProps>
                  <div className="space-y-0.5">
                    <Label
                      htmlFor="use-current-dag-file-batch"
                      className="cursor-pointer text-sm font-medium"
                    >
                      <I18nText text={'Use original DAG file'} />
                    </Label>
                    <p className="text-xs text-muted-foreground">
                      {rescheduleSourceLoading ? (
                        <I18nText
                          text={
                            'Checking whether the selected DAG runs still have their original DAG files.'
                          }
                        />
                      ) : specFromFile ? (
                        <I18nText
                          text={
                            'Use the current spec from the original DAG file for every selected DAG run.'
                          }
                        />
                      ) : (
                        <I18nText
                          text={
                            'Stored YAML snapshots will be used because one or more selected DAG runs do not have the original DAG file available.'
                          }
                        />
                      )}
                    </p>
                  </div>
                </div>
              )}
            </div>
          )}

          {isProcessing && activeBatch && (
            <div className="space-y-4">
              <div className="rounded-md border bg-muted/20 p-4">
                <div className="flex items-start gap-3">
                  {isLocked ? (
                    <RefreshCw className="mt-0.5 h-5 w-5 animate-spin text-muted-foreground" />
                  ) : (
                    <CheckCircle2 className="mt-0.5 h-5 w-5 text-success" />
                  )}
                  <div className="min-w-0 flex-1 space-y-2">
                    <div className="text-sm font-medium text-foreground">
                      {phase === 'running' ? (
                        <I18nText text={'Submitting requests...'} />
                      ) : progress.isRefreshing ? (
                        <I18nText text={'Refreshing DAG runs...'} />
                      ) : (
                        <I18nText text={'Finished submitting requests'} />
                      )}
                    </div>
                    <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
                      <span className="font-mono tabular-nums">
                        {progress.processedCount}/{totalCount}{' '}
                        <I18nText text={'processed'} />
                      </span>
                      <span>
                        {progress.successCount} <I18nText text={'succeeded'} />
                      </span>
                      <span>
                        {progress.failureCount} <I18nText text={'failed'} />
                      </span>
                    </div>
                  </div>
                </div>
              </div>

              <div className="rounded-md border bg-muted/20 p-3">
                <div className="mb-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                  <I18nText text={'Current item'} />
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
                    {progress.isRefreshing ? (
                      <I18nText text={'Refreshing the DAG-run list'} />
                    ) : (
                      <I18nText text={'All requests have been submitted'} />
                    )}
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
                  <I18nText text={'Results'} />
                </div>
                <div className="min-h-40 max-h-[45vh] space-y-3 overflow-y-auto p-3">
                  {progress.results.length === 0 ? (
                    <div className="flex min-h-32 items-center justify-center text-sm text-muted-foreground">
                      <I18nText
                        text={
                          'Results will appear here as each request finishes.'
                        }
                      />
                    </div>
                  ) : (
                    progress.results.map((result, index) => (
                      <div
                        key={`${result.name}-${result.dagRunId}-${index}`}
                        data-testid="batch-action-result-item"
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
                            className={`text-xs font-medium ${result.ok ? 'text-success' : 'text-error'}`}
                          >
                            {result.ok ? (
                              <I18nText text={'Succeeded'} />
                            ) : (
                              <I18nText text={'Failed'} />
                            )}
                          </div>
                        </div>
                        {renderResultDetails(activeBatch.action, result)}
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
                  <I18nText text={'Cancel'} />
                </Button>
                <Button
                  onClick={() =>
                    submitBatchAction(
                      activeBatch.action === 'reschedule'
                        ? { useCurrentDagFile }
                        : undefined
                    )
                  }
                  variant={
                    activeBatch.action === 'delete' ? 'destructive' : 'default'
                  }
                >
                  {activeBatch.action === 'delete' && (
                    <Trash2 className="h-4 w-4" />
                  )}
                  {getSubmitButtonText(
                    activeBatch.action,
                    activeBatch.snapshot.length
                  )}
                </Button>
              </>
            )}
            {(phase === 'running' || progress.isRefreshing) && (
              <Button disabled>
                <RefreshCw className="mr-2 h-4 w-4 animate-spin" />
                {phase === 'running' ? (
                  <I18nText text={'Submitting...'} />
                ) : (
                  <I18nText text={'Refreshing...'} />
                )}
              </Button>
            )}
            {phase === 'complete' && !progress.isRefreshing && (
              <Button variant="outline" onClick={closeDialog}>
                <I18nText text={'Close'} />
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

export default DAGRunBatchActions;
