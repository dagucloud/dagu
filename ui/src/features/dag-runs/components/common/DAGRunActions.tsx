/**
 * DAGRunActions component provides action buttons for DAGRun operations (stop, retry).
 *
 * @module features/dagRuns/components/common
 */
import { Checkbox } from '@/components/ui/checkbox';
import { useErrorModal } from '@/components/ui/error-modal';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import dayjs from '@/lib/dayjs';
import ActionButton from '@/components/ui/action-button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Ban, RefreshCw, Square, X } from 'lucide-react';
import React from 'react';
import { Button } from '@/components/ui/button';
import { components, NodeStatus, Status } from '../../../../api/v1/schema';
import { useConfig } from '../../../../contexts/ConfigContext';
import { useRemoteNode } from '../../../../contexts/RemoteNodeContext';
import { useClient } from '../../../../hooks/api';
import ConfirmModal from '@/components/ui/confirm-dialog';
import LabeledItem from '@/components/ui/labeled-item';
import StatusChip from '@/components/ui/status-chip';
import { getDAGRunTerminateActionDetails } from './terminateAction';

/**
 * Props for the DAGRunActions component
 */
type Props = {
  /** Current status of the DAGRun */
  dagRun?:
    | components['schemas']['DAGRunSummary']
    | components['schemas']['DAGRunDetails'];
  /** Name of the DAGRun */
  name: string;
  /** Whether to show text labels on buttons */
  label?: boolean;
  /** Function to refresh data after actions */
  refresh?: () => void;
  /** Display mode: 'compact' for icon-only, 'full' for text+icon buttons */
  displayMode?: 'compact' | 'full';
  /** Whether this is a root level dagRun (controls availability of retry/stop actions) */
  isRootLevel?: boolean;
};

/**
 * DAGRunActions component provides buttons to stop and retry DAGRun executions
 */
function DAGRunActions({
  dagRun,
  name,
  refresh,
  displayMode = 'compact',
  isRootLevel = true,
}: Props) {
  const remoteNode = useRemoteNode();
  const config = useConfig();
  const { showError } = useErrorModal();
  const { showToast } = useSimpleToast();
  const [isStopModal, setIsStopModal] = React.useState(false);
  const [isRetryModal, setIsRetryModal] = React.useState(false);
  const [isDequeueModal, setIsDequeueModal] = React.useState(false);
  const [isRejectModal, setIsRejectModal] = React.useState(false);
  const [rejectReason, setRejectReason] = React.useState('');

  // Retry-as-new modal state
  const [retryAsNew, setRetryAsNew] = React.useState(false);
  const [newRunId, setNewRunId] = React.useState('');
  const [dagNameOverride, setDagNameOverride] = React.useState('');
  const [specFromFile, setSpecFromFile] = React.useState(false);
  const [useCurrentDagFile, setUseCurrentDagFile] = React.useState(false);
  const [rescheduleSourceLoading, setRescheduleSourceLoading] =
    React.useState(false);

  const client = useClient();

  /**
   * Reload DAGRun data after an action is performed
   */
  const reloadData = () => {
    if (refresh) {
      refresh();
    }
  };

  const resetRetryModalState = React.useCallback(() => {
    setRetryAsNew(false);
    setNewRunId('');
    setDagNameOverride('');
    setSpecFromFile(false);
    setUseCurrentDagFile(false);
  }, []);

  React.useEffect(() => {
    if (!isRetryModal || !dagRun?.dagRunId) {
      return;
    }

    let cancelled = false;
    setRescheduleSourceLoading(true);

    void (async () => {
      try {
        const { data } = await client.GET('/dag-runs/{name}/{dagRunId}', {
          params: {
            path: {
              name,
              dagRunId: dagRun.dagRunId,
            },
            query: {
              remoteNode,
            },
          },
        });
        if (cancelled) {
          return;
        }
        const available = Boolean(data?.dagRunDetails?.specFromFile);
        setSpecFromFile(available);
        setUseCurrentDagFile(available);
      } catch (error) {
        if (cancelled) {
          return;
        }
        console.error(
          'Failed to fetch DAG run details for reschedule source',
          error
        );
        setSpecFromFile(false);
        setUseCurrentDagFile(false);
      } finally {
        if (!cancelled) {
          setRescheduleSourceLoading(false);
        }
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [client, dagRun?.dagRunId, isRetryModal, name, remoteNode]);

  const isWaiting = dagRun?.status === Status.Waiting;
  const hasNodes =
    dagRun &&
    'nodes' in dagRun &&
    Array.isArray((dagRun as components['schemas']['DAGRunDetails']).nodes);
  const terminateDetails = getDAGRunTerminateActionDetails(dagRun, {
    isRootLevel,
  });
  const terminateAction = terminateDetails.action;

  // Determine which buttons should be enabled based on current status and root level
  const buttonState = {
    terminate: terminateAction !== 'none',
    reject: isRootLevel && isWaiting && hasNodes,
    retry:
      isRootLevel &&
      dagRun?.status !== Status.Running &&
      dagRun?.status !== Status.Queued &&
      dagRun?.dagRunId !== '',
    dequeue: isRootLevel && dagRun?.status === Status.Queued,
  };

  if (!dagRun || !config.permissions.runDags) {
    return <></>;
  }

  return (
    <TooltipProvider delayDuration={300}>
      <div
        className={`flex items-center ${displayMode === 'compact' ? 'space-x-1' : 'space-x-2'}`}
      >
        {/* Stop / Reject Button */}
        {isWaiting && hasNodes ? (
          <Tooltip>
            <TooltipTrigger asChild>
              <span>
                <ActionButton
                  label={displayMode !== 'compact'}
                  icon={<Ban className="h-4 w-4" />}
                  disabled={!buttonState['reject']}
                  onClick={() => setIsRejectModal(true)}
                  className="cursor-pointer"
                >
                  Reject
                </ActionButton>
              </span>
            </TooltipTrigger>
            <TooltipContent>
              <p>Reject all waiting steps</p>
            </TooltipContent>
          </Tooltip>
        ) : (
          <Tooltip>
            <TooltipTrigger asChild>
              <span>
                <ActionButton
                  label={displayMode !== 'compact'}
                  icon={
                    terminateAction === 'cancel' ? (
                      <X className="h-4 w-4" />
                    ) : (
                      <Square className="h-4 w-4" />
                    )
                  }
                  disabled={!buttonState['terminate']}
                  onClick={() => setIsStopModal(true)}
                  className="cursor-pointer"
                >
                  {terminateDetails.buttonText}
                </ActionButton>
              </span>
            </TooltipTrigger>
            <TooltipContent>
              <p>{terminateDetails.tooltipText}</p>
            </TooltipContent>
          </Tooltip>
        )}

        {/* Retry Button */}
        <Tooltip>
          <TooltipTrigger asChild>
            <span>
              <ActionButton
                label={displayMode !== 'compact'}
                icon={<RefreshCw className="h-4 w-4" />}
                disabled={!buttonState['retry']}
                onClick={() => setIsRetryModal(true)}
                className="cursor-pointer"
              >
                Retry
              </ActionButton>
            </span>
          </TooltipTrigger>
          <TooltipContent>
            <p>
              {isRootLevel
                ? 'Retry DAGRun execution'
                : 'Retry action only available at root dagRun level'}
            </p>
          </TooltipContent>
        </Tooltip>

        {/* Dequeue Button */}
        <Tooltip>
          <TooltipTrigger asChild>
            <span>
              <ActionButton
                label={displayMode !== 'compact'}
                icon={<X className="h-4 w-4" />}
                disabled={!buttonState['dequeue']}
                onClick={() => setIsDequeueModal(true)}
                className="cursor-pointer"
              >
                Dequeue
              </ActionButton>
            </span>
          </TooltipTrigger>
          <TooltipContent>
            <p>
              {isRootLevel
                ? 'Remove DAGRun from queue'
                : 'Dequeue action only available at root dagRun level'}
            </p>
          </TooltipContent>
        </Tooltip>

        {/* Stop Confirmation Modal */}
        <ConfirmModal
          title="Confirmation"
          buttonText={terminateDetails.buttonText}
          visible={isStopModal}
          dismissModal={() => setIsStopModal(false)}
          onSubmit={async () => {
            setIsStopModal(false);
            const { error } = await client.POST(
              '/dag-runs/{name}/{dagRunId}/stop',
              {
                params: {
                  query: {
                    remoteNode,
                  },
                  path: {
                    name: name,
                    dagRunId: dagRun.dagRunId,
                  },
                },
              }
            );
            if (error) {
              console.error('Stop API error:', error);
              showError(
                error.message || terminateDetails.errorTitle,
                terminateDetails.errorDescription
              );
              return;
            }
            reloadData();
          }}
        >
          <div>{terminateDetails.confirmText}</div>
        </ConfirmModal>

        {/* Retry Confirmation Modal */}
        <ConfirmModal
          title={retryAsNew ? 'Reschedule DAG Run' : 'Confirmation'}
          buttonText={retryAsNew ? 'Reschedule' : 'Retry'}
          visible={isRetryModal}
          dismissModal={() => {
            setIsRetryModal(false);
            resetRetryModalState();
          }}
          onSubmit={async () => {
            setIsRetryModal(false);

            if (retryAsNew) {
              // Use reschedule endpoint for retry-as-new
              const { error, data } = await client.POST(
                '/dag-runs/{name}/{dagRunId}/reschedule',
                {
                  params: {
                    path: {
                      name: name,
                      dagRunId: dagRun.dagRunId,
                    },
                    query: {
                      remoteNode,
                    },
                  },
                  body: {
                    dagRunId: newRunId || undefined, // Auto-generate if empty
                    ...(dagNameOverride ? { dagName: dagNameOverride } : {}), // Use original if empty
                    useCurrentDagFile,
                  },
                }
              );
              if (error) {
                showError(
                  error.message || 'Failed to reschedule DAG run',
                  'Check if the worker is running and the DAG definition is valid.'
                );
                resetRetryModalState();
                return;
              }
              // Show success message with new run ID
              if (data?.dagRunId) {
                showToast(`New DAG run created: ${data.dagRunId}`);
              }
              resetRetryModalState();
            } else {
              // Use retry endpoint for regular retry
              const { error } = await client.POST(
                '/dag-runs/{name}/{dagRunId}/retry',
                {
                  params: {
                    path: {
                      name: name,
                      dagRunId: dagRun.dagRunId,
                    },
                    query: {
                      remoteNode,
                    },
                  },
                  body: {
                    dagRunId: dagRun.dagRunId,
                  },
                }
              );
              if (error) {
                showError(
                  error.message || 'Failed to retry DAG run',
                  'Check if the worker is running and accessible.'
                );
                return;
              }
            }
            reloadData();
          }}
        >
          {/* Modal content structure */}
          <div className="space-y-3">
            <p className="mb-2">
              Do you really want to retry the following execution?
            </p>
            <LabeledItem label="DAGRun-Name">
              <span className="font-mono text-sm">{dagRun?.name || 'N/A'}</span>
            </LabeledItem>
            <LabeledItem label="DAGRun-ID">
              <span className="font-mono text-sm">
                {dagRun?.dagRunId || 'N/A'}
              </span>
            </LabeledItem>
            {dagRun?.startedAt && (
              <LabeledItem label="Started At">
                <span className="text-sm">
                  {dayjs(dagRun.startedAt).format('YYYY-MM-DD HH:mm:ss Z')}
                </span>
              </LabeledItem>
            )}
            {dagRun?.status !== undefined && (
              <LabeledItem label="Status">
                <StatusChip status={dagRun.status} size="sm">
                  {dagRun.statusLabel || ''}
                </StatusChip>
              </LabeledItem>
            )}

            {/* Reschedule checkbox */}
            <div className="flex items-center space-x-2 pt-2 border-t">
              <Checkbox
                id="reschedule"
                checked={retryAsNew}
                onCheckedChange={(checked) => setRetryAsNew(checked as boolean)}
                className="border-border"
              />
              <Label htmlFor="reschedule" className="cursor-pointer text-sm">
                Reschedule with new DAG-run
              </Label>
            </div>

            {/* Conditional inputs when reschedule is checked */}
            {retryAsNew && (
              <div className="space-y-3 pt-2">
                <div className="space-y-1.5">
                  <Label htmlFor="new-dagrun-id" className="text-sm">
                    New DAG-Run ID (optional)
                  </Label>
                  <Input
                    id="new-dagrun-id"
                    placeholder="Auto-generated if empty"
                    value={newRunId}
                    onChange={(e) => setNewRunId(e.target.value)}
                    className="h-8 text-sm"
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="dag-name-override" className="text-sm">
                    DAG Name Override (optional)
                  </Label>
                  <Input
                    id="dag-name-override"
                    placeholder={`Leave empty to use: ${dagRun?.name || 'original'}`}
                    value={dagNameOverride}
                    onChange={(e) => setDagNameOverride(e.target.value)}
                    className="h-8 text-sm"
                  />
                </div>
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
                  <Checkbox
                    id="use-current-dag-file"
                    aria-label="Use original DAG file"
                    checked={useCurrentDagFile}
                    disabled={rescheduleSourceLoading || !specFromFile}
                    onCheckedChange={(checked) =>
                      setUseCurrentDagFile(checked as boolean)
                    }
                    className="mt-0.5 h-5 w-5 border-border pointer-events-none"
                  />
                  <div className="space-y-0.5">
                    <Label
                      htmlFor="use-current-dag-file"
                      className="cursor-pointer text-sm font-medium"
                    >
                      Use original DAG file
                    </Label>
                    <p className="text-xs text-muted-foreground">
                      {specFromFile
                        ? 'Use the current spec from the original DAG file instead of the stored YAML snapshot.'
                        : 'Stored YAML snapshot will be used because the original DAG file is not available.'}
                    </p>
                  </div>
                </div>
              </div>
            )}
          </div>
        </ConfirmModal>

        {/* Reject Modal */}
        <Dialog
          open={isRejectModal}
          onOpenChange={(open) => {
            if (!open) {
              setIsRejectModal(false);
              setRejectReason('');
            }
          }}
        >
          <DialogContent className="sm:max-w-[450px]">
            <DialogHeader>
              <DialogTitle>Reject DAG Run</DialogTitle>
            </DialogHeader>
            <div className="py-2">
              <textarea
                className="w-full px-3 py-2 text-sm border border-border rounded bg-background focus:outline-none focus:border-ring resize-none"
                placeholder="Reason (optional)..."
                rows={2}
                value={rejectReason}
                onChange={(e) => setRejectReason(e.target.value)}
              />
            </div>
            <DialogFooter>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setIsRejectModal(false);
                  setRejectReason('');
                }}
              >
                <X className="h-4 w-4" /> Cancel
              </Button>
              <Button
                variant="default"
                size="sm"
                onClick={async () => {
                  setIsRejectModal(false);
                  const details =
                    dagRun as components['schemas']['DAGRunDetails'];
                  const waitingNodes = details.nodes.filter(
                    (n) => n.status === NodeStatus.Waiting
                  );
                  const errors: string[] = [];
                  for (const node of waitingNodes) {
                    const { error } = await client.POST(
                      '/dag-runs/{name}/{dagRunId}/steps/{stepName}/reject',
                      {
                        params: {
                          path: {
                            name,
                            dagRunId: dagRun!.dagRunId,
                            stepName: node.step.name,
                          },
                          query: {
                            remoteNode,
                          },
                        },
                        body: { reason: rejectReason || undefined },
                      }
                    );
                    if (error) {
                      errors.push(node.step.name);
                    }
                  }
                  if (errors.length > 0) {
                    showError(
                      `Failed to reject ${errors.length} step(s)`,
                      `Failed to reject: ${errors.join(', ')}`
                    );
                  }
                  setRejectReason('');
                  reloadData();
                }}
              >
                <Ban className="h-4 w-4" /> Reject
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>

        {/* Dequeue Confirmation Modal */}
        <ConfirmModal
          title="Confirmation"
          buttonText="Dequeue"
          visible={isDequeueModal}
          dismissModal={() => setIsDequeueModal(false)}
          onSubmit={async () => {
            setIsDequeueModal(false);

            const { error } = await client.GET(
              '/dag-runs/{name}/{dagRunId}/dequeue',
              {
                params: {
                  path: {
                    name: name,
                    dagRunId: dagRun.dagRunId,
                  },
                  query: {
                    remoteNode,
                  },
                },
              }
            );
            if (error) {
              showError(
                error.message || 'Failed to dequeue DAG run',
                'The DAG run may have already started or been removed from the queue.'
              );
              return;
            }
            reloadData();
          }}
        >
          <div>
            <p className="mb-2">
              Do you really want to dequeue the following dagRun?
            </p>
            <LabeledItem label="DAGRun-Name">
              <span className="font-mono text-sm">{dagRun?.name || 'N/A'}</span>
            </LabeledItem>
            <LabeledItem label="DAGRun-ID">
              <span className="font-mono text-sm">
                {dagRun?.dagRunId || 'N/A'}
              </span>
            </LabeledItem>
            {dagRun?.status !== undefined && (
              <LabeledItem label="Status">
                <StatusChip status={dagRun.status} size="sm">
                  {dagRun.statusLabel || ''}
                </StatusChip>
              </LabeledItem>
            )}
          </div>
        </ConfirmModal>
      </div>
    </TooltipProvider>
  );
}

export default DAGRunActions;
