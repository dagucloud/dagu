// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * DAGActions component provides action buttons for DAG operations (start, stop, retry).
 *
 * @module features/dags/components/common
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
} from '@/components/ui/tooltip'; // Import Shadcn Tooltip
import dayjs from '@/lib/dayjs';
import ActionButton from '@/components/ui/action-button';
import StatusChip from '@/components/ui/status-chip';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { AlertTriangle, Ban, Play, RefreshCw, Square, X } from 'lucide-react';
import React from 'react';
import { Button } from '@/components/ui/button';
import { components, NodeStatus, Status } from '../../../../api/v1/schema';
import { AppBarContext } from '../../../../contexts/AppBarContext';
import { useCanManageProfiles } from '../../../../contexts/AuthContext';
import { useConfig } from '../../../../contexts/ConfigContext';
import { useUnsavedChanges } from '../../../../contexts/UnsavedChangesContext';
import { useClient, useQuery } from '../../../../hooks/api';
import { whenEnabled } from '../../../../hooks/queryUtils';
import ConfirmModal from '@/components/ui/confirm-dialog';
import LabeledItem from '@/components/ui/labeled-item';
import { getDAGRunTerminateActionDetails } from '../../../dag-runs/components/common/terminateAction';
import { DAGContext } from '../../contexts/DAGContext';
import { StartDAGModal } from '../dag-execution';

/**
 * Props for the DAGActions component
 */
type Props = {
  /** Current status of the DAG */
  status?:
    | components['schemas']['DAGRunSummary']
    | components['schemas']['DAGRunDetails'];
  /** File ID of the DAG */
  fileName: string;
  /** DAG definition */
  dag?: components['schemas']['DAG'] | components['schemas']['DAGDetails'];
  /** Whether to show text labels on buttons */
  label?: boolean;
  /** Function to refresh data after actions */
  refresh?: () => void;
  /** Display mode: 'compact' for icon-only, 'full' for text+icon buttons */
  displayMode?: 'compact' | 'full';
  /** Function to navigate to status tab after execution */
  navigateToStatusTab?: () => void;
};

/**
 * DAGActions component provides buttons to start, stop, and retry DAG executions
 */
function DAGActions({
  status,
  fileName,
  dag,
  refresh,
  displayMode = 'compact',
  navigateToStatusTab,
}: Props) {
  const appBarContext = React.useContext(AppBarContext);
  const dagContext = React.useContext(DAGContext);
  const config = useConfig();
  const { hasUnsavedChanges } = useUnsavedChanges();
  const { showError } = useErrorModal();
  const { showToast } = useSimpleToast();
  const canManageProfiles = useCanManageProfiles();
  const [isEnqueueModal, setIsEnqueueModal] = React.useState(false);
  const [startModalDag, setStartModalDag] =
    React.useState<components['schemas']['DAGDetails']>();
  const [startModalLoading, setStartModalLoading] = React.useState(false);
  const [startModalLoadError, setStartModalLoadError] = React.useState<
    string | null
  >(null);
  const [isStopModal, setIsStopModal] = React.useState(false);
  const [isRetryModal, setIsRetryModal] = React.useState(false);
  const [isUnsavedChangesModal, setIsUnsavedChangesModal] =
    React.useState(false);
  const [retryDagRunId, setRetryDagRunId] = React.useState<string>('');
  const [stopAllRunning, setStopAllRunning] = React.useState(false);
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
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const profilesQuery = React.useMemo(
    () =>
      whenEnabled(canManageProfiles, {
        params: {
          query: { remoteNode },
        },
      }),
    [canManageProfiles, remoteNode]
  );
  const { data: profilesData, isLoading: profilesLoading } = useQuery(
    '/profiles',
    profilesQuery
  );
  const runtimeProfiles = profilesData?.profiles || [];
  const dagSettingsQuery = React.useMemo(
    () =>
      whenEnabled(isEnqueueModal && !!fileName, {
        params: {
          path: { fileName },
          query: { remoteNode },
        },
      }),
    [fileName, isEnqueueModal, remoteNode]
  );
  const { data: dagSettingsData, isLoading: dagSettingsLoading } = useQuery(
    '/dags/{fileName}/settings',
    dagSettingsQuery
  );

  React.useEffect(() => {
    if (!isRetryModal || !status?.name || !retryDagRunId) {
      return;
    }

    let cancelled = false;
    setRescheduleSourceLoading(true);

    void (async () => {
      try {
        const { data } = await client.GET('/dag-runs/{name}/{dagRunId}', {
          params: {
            path: {
              name: status.name,
              dagRunId: retryDagRunId,
            },
            query: {
              remoteNode: appBarContext.selectedRemoteNode || 'local',
            },
          },
        });
        if (cancelled) {
          return;
        }
        const available = Boolean(data?.dagRunDetails?.specFromFile);
        setSpecFromFile(available);
        setUseCurrentDagFile(available);
      } catch {
        if (cancelled) {
          return;
        }
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
  }, [
    appBarContext.selectedRemoteNode,
    client,
    isRetryModal,
    retryDagRunId,
    status?.name,
  ]);

  // Auto-open start modal when requested (e.g., from cockpit preview)
  React.useEffect(() => {
    if (dagContext.autoOpenStartModal) {
      setIsEnqueueModal(true);
    }
  }, [dagContext.autoOpenStartModal]);

  React.useEffect(() => {
    if (!isEnqueueModal) {
      return;
    }

    let cancelled = false;
    setStartModalLoading(true);
    setStartModalLoadError(null);

    void (async () => {
      try {
        const { data, error } = await client.GET('/dags/{fileName}', {
          params: {
            path: { fileName },
            query: {
              remoteNode: appBarContext.selectedRemoteNode || 'local',
            },
          },
        });
        if (cancelled) {
          return;
        }
        if (error || !data?.dag) {
          setStartModalDag(undefined);
          setStartModalLoadError(
            error?.message || 'Failed to load DAG details for execution.'
          );
          return;
        }
        setStartModalDag(data.dag);
      } catch (error) {
        if (!cancelled) {
          setStartModalDag(undefined);
          setStartModalLoadError(
            error instanceof Error
              ? error.message
              : 'Failed to load DAG details for execution.'
          );
        }
      } finally {
        if (!cancelled) {
          setStartModalLoading(false);
        }
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [appBarContext.selectedRemoteNode, client, fileName, isEnqueueModal]);

  /**
   * Reload DAG data after an action is performed
   */
  const reloadData = () => {
    if (refresh) {
      refresh();
    }
  };

  const isWaiting = status?.status === Status.Waiting;
  const hasNodes =
    status &&
    'nodes' in status &&
    Array.isArray((status as components['schemas']['DAGRunDetails']).nodes);
  const terminateDetails = getDAGRunTerminateActionDetails(status, {
    copy: {
      stopTooltipText: 'Stop DAG execution',
      cancelTooltipText: 'Cancel auto-retry for this failed DAG execution',
      stopConfirmText:
        status?.name && status?.dagRunId
          ? `Do you really want to stop the dag-run "${status.name}"?`
          : 'Do you really want to cancel the DAG?',
      cancelConfirmText: `Do you really want to cancel auto-retry for the dag-run "${status?.name || ''}"?`,
    },
  });
  const terminateAction = terminateDetails.action;

  // Determine which buttons should be enabled based on current status
  const buttonState = {
    enqueue: true,
    terminate: terminateAction !== 'none',
    reject: isWaiting && hasNodes,
    retry:
      Boolean(status?.dagRunId) &&
      status?.status !== Status.Running &&
      status?.status !== Status.Queued,
  };

  if (!dag || !config.permissions.runDags) {
    return <></>;
  }

  return (
    <TooltipProvider delayDuration={300}>
      <div
        className={`flex items-center ${displayMode === 'compact' ? 'space-x-1' : 'space-x-2'}`}
      >
        {/* Enqueue Button */}
        <Tooltip>
          <TooltipTrigger asChild>
            <ActionButton
              label={displayMode !== 'compact'}
              icon={<Play className="h-4 w-4" />}
              disabled={!buttonState['enqueue']}
              onClick={() => {
                if (hasUnsavedChanges) {
                  setIsUnsavedChangesModal(true);
                } else {
                  setIsEnqueueModal(true);
                }
              }}
              className="cursor-pointer"
            >
              Enqueue
            </ActionButton>
          </TooltipTrigger>
          <TooltipContent>
            <p>Start DAG execution</p>
          </TooltipContent>
        </Tooltip>

        {/* Stop / Reject Button */}
        {isWaiting && hasNodes ? (
          <Tooltip>
            <TooltipTrigger asChild>
              <ActionButton
                label={displayMode !== 'compact'}
                icon={<Ban className="h-4 w-4" />}
                disabled={!buttonState['reject']}
                onClick={() => setIsRejectModal(true)}
                className="cursor-pointer"
              >
                Reject
              </ActionButton>
            </TooltipTrigger>
            <TooltipContent>
              <p>Reject all waiting steps</p>
            </TooltipContent>
          </Tooltip>
        ) : (
          <Tooltip>
            <TooltipTrigger asChild>
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
                onClick={() => {
                  setStopAllRunning(false);
                  setIsStopModal(true);
                }}
                className="cursor-pointer"
              >
                {terminateDetails.buttonText}
              </ActionButton>
            </TooltipTrigger>
            <TooltipContent>
              <p>{terminateDetails.tooltipText}</p>
            </TooltipContent>
          </Tooltip>
        )}

        {/* Retry Button */}
        <Tooltip>
          <TooltipTrigger asChild>
            <ActionButton
              label={displayMode !== 'compact'}
              icon={<RefreshCw className="h-4 w-4" />}
              disabled={!buttonState['retry']}
              onClick={async () => {
                // Get the current URL parameters
                const urlParams = new URLSearchParams(window.location.search);
                const idxParam = urlParams.get('idx');

                // Default to current status dagRunId
                let dagRunIdToUse = status?.dagRunId || '';

                // If we're in the history page or modal history tab with a specific run selected
                const isInHistoryPage =
                  window.location.pathname.includes('/history');
                const isInModalHistoryTab =
                  document.querySelector(
                    '.dag-modal-content [data-tab="history"]'
                  ) !== null;

                if (
                  (isInHistoryPage || isInModalHistoryTab) &&
                  idxParam !== null
                ) {
                  try {
                    // Get all dag-runs for this DAG to find the correct dagRunId
                    const { data } = await client.GET(
                      '/dags/{fileName}/dag-runs',
                      {
                        params: {
                          path: {
                            fileName: fileName,
                          },
                          query: {
                            remoteNode:
                              appBarContext.selectedRemoteNode || 'local',
                          },
                        },
                      }
                    );

                    if (data?.dagRuns && data.dagRuns.length > 0) {
                      // Convert idx to integer
                      const selectedIdx = parseInt(idxParam);

                      // Get the dag-run at the selected index (reversed order)
                      const selectedDagRun = [...data.dagRuns].reverse()[
                        selectedIdx
                      ];

                      if (selectedDagRun && selectedDagRun.dagRunId) {
                        dagRunIdToUse = selectedDagRun.dagRunId;
                      }
                    }
                  } catch (err) {
                    console.error('Error fetching dag-runs for retry:', err);
                  }
                }

                // Set the dagRunId to use for retry
                setRetryDagRunId(dagRunIdToUse);

                // Show the modal
                setIsRetryModal(true);
              }}
              className="cursor-pointer"
            >
              Retry
            </ActionButton>
          </TooltipTrigger>
          <TooltipContent>
            <p>Retry DAG execution</p>
          </TooltipContent>
        </Tooltip>
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
                    status as components['schemas']['DAGRunDetails'];
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
                            name: status!.name,
                            dagRunId: status!.dagRunId,
                            stepName: node.step.name,
                          },
                          query: {
                            remoteNode:
                              appBarContext.selectedRemoteNode || 'local',
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

        <ConfirmModal
          title="Confirmation"
          buttonText={terminateDetails.buttonText}
          visible={isStopModal}
          dismissModal={() => {
            setIsStopModal(false);
            setStopAllRunning(false);
          }}
          onSubmit={async () => {
            setIsStopModal(false);

            // If stopAllRunning is checked, use the stop-all endpoint
            if (terminateAction === 'stop' && stopAllRunning) {
              const { error } = await client.POST('/dags/{fileName}/stop-all', {
                params: {
                  path: { fileName },
                  query: {
                    remoteNode: appBarContext.selectedRemoteNode || 'local',
                  },
                },
              });
              if (error) {
                console.error('Stop all API error:', error);
                showError(
                  error.message || 'Failed to stop all DAG instances',
                  'Some instances may have already completed or the worker is unavailable.'
                );
                return;
              }
              setStopAllRunning(false);
              reloadData();
            } else {
              // Use dag-run API - requires DAG name and ID
              if (status?.name && status?.dagRunId) {
                const { error } = await client.POST(
                  '/dag-runs/{name}/{dagRunId}/stop',
                  {
                    params: {
                      query: {
                        remoteNode: appBarContext.selectedRemoteNode || 'local',
                      },
                      path: {
                        name: status.name,
                        dagRunId: status.dagRunId,
                      },
                    },
                  }
                );
                if (error) {
                  console.error('Stop dag-run API error:', error);
                  showError(
                    error.message || terminateDetails.errorTitle,
                    terminateDetails.errorDescription
                  );
                  return;
                }
                reloadData();
              } else {
                console.error('Cannot stop DAG: missing DAG name or run ID');
                showError(
                  'Cannot stop DAG: missing DAG name or run ID',
                  'Please ensure you have selected a valid DAG run.'
                );
              }
            }
          }}
        >
          <div>
            <p className="mb-2">
              {terminateAction === 'stop' && stopAllRunning
                ? `Do you really want to stop all running instances of this DAG?`
                : terminateDetails.confirmText}
            </p>
            {!stopAllRunning && status?.name && (
              <LabeledItem label="DAG-Run-Name">
                <span className="font-mono text-sm">{status.name}</span>
              </LabeledItem>
            )}
            {!stopAllRunning && status?.dagRunId && (
              <LabeledItem label="DAG-Run-ID">
                <span className="font-mono text-sm">{status.dagRunId}</span>
              </LabeledItem>
            )}
            {!stopAllRunning && status?.startedAt && (
              <LabeledItem label="Started At">
                <span className="text-sm">
                  {dayjs(status.startedAt).format('YYYY-MM-DD HH:mm:ss Z')}
                </span>
              </LabeledItem>
            )}
            {!stopAllRunning && status?.status !== undefined && (
              <LabeledItem label="Status">
                <StatusChip status={status.status} size="sm">
                  {status.statusLabel || ''}
                </StatusChip>
              </LabeledItem>
            )}
            {terminateAction === 'stop' && (
              <div className="mt-4 flex items-center space-x-2 p-2 bg-warning-muted rounded border border-warning/30">
                <Checkbox
                  id="stop-all"
                  checked={stopAllRunning}
                  onCheckedChange={(checked) =>
                    setStopAllRunning(checked as boolean)
                  }
                  className="border-warning data-[state=checked]:bg-warning data-[state=checked]:border-warning data-[state=checked]:text-black"
                />
                <label
                  htmlFor="stop-all"
                  className="text-sm font-medium leading-none peer-disabled:cursor-not-allowed peer-disabled:opacity-70 text-warning"
                >
                  Stop all running instances
                </label>
              </div>
            )}
          </div>
        </ConfirmModal>
        <ConfirmModal
          title={retryAsNew ? 'Reschedule DAG Run' : 'Confirmation'}
          buttonText={retryAsNew ? 'Reschedule' : 'Rerun'}
          visible={isRetryModal}
          dismissModal={() => {
            setIsRetryModal(false);
            setRetryAsNew(false);
            setNewRunId('');
            setDagNameOverride('');
            setSpecFromFile(false);
            setUseCurrentDagFile(false);
          }}
          onSubmit={async () => {
            setIsRetryModal(false);

            // Use dag-run API - requires DAG name and ID
            if (status?.name && retryDagRunId) {
              if (retryAsNew) {
                // Use reschedule endpoint for retry-as-new
                const { error, data } = await client.POST(
                  '/dag-runs/{name}/{dagRunId}/reschedule',
                  {
                    params: {
                      path: {
                        name: status.name,
                        dagRunId: retryDagRunId,
                      },
                      query: {
                        remoteNode: appBarContext.selectedRemoteNode || 'local',
                      },
                    },
                    body: {
                      dagRunId: newRunId || undefined, // Auto-generate if empty
                      ...(dagNameOverride ? { dagName: dagNameOverride } : {}),
                      useCurrentDagFile,
                    },
                  }
                );
                if (error) {
                  showError(
                    error.message || 'Failed to reschedule DAG run',
                    'Check if the worker is running and the DAG definition is valid.'
                  );
                  // Reset state on error
                  setRetryAsNew(false);
                  setNewRunId('');
                  setDagNameOverride('');
                  setSpecFromFile(false);
                  setUseCurrentDagFile(false);
                  return;
                }
                // Show success message with new run ID
                if (data?.dagRunId) {
                  showToast(`New DAG run created: ${data.dagRunId}`);
                }
                // Reset state after success
                setRetryAsNew(false);
                setNewRunId('');
                setDagNameOverride('');
                setSpecFromFile(false);
                setUseCurrentDagFile(false);
              } else {
                // Use retry endpoint for regular retry
                const { error } = await client.POST(
                  '/dag-runs/{name}/{dagRunId}/retry',
                  {
                    params: {
                      path: {
                        name: status.name,
                        dagRunId: retryDagRunId,
                      },
                      query: {
                        remoteNode: appBarContext.selectedRemoteNode || 'local',
                      },
                    },
                    body: {
                      dagRunId: retryDagRunId,
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
            } else {
              console.error('Cannot retry DAG: missing DAG name or run ID');
              showError(
                'Cannot retry DAG: missing DAG name or run ID',
                'Please ensure you have selected a valid DAG run.'
              );
            }
          }}
        >
          {/* Keep modal content structure */}
          <div className="space-y-3">
            <p className="mb-2">
              {status?.name && retryDagRunId
                ? `Do you really want to retry the dag-run "${status.name}"?`
                : 'Do you really want to rerun the following execution?'}
            </p>
            <LabeledItem label="DAG-Run-Name">
              <span className="font-mono text-sm">{status?.name || 'N/A'}</span>
            </LabeledItem>
            <LabeledItem label="DAG-Run-ID">
              <span className="font-mono text-sm">
                {retryDagRunId || status?.dagRunId || 'N/A'}
              </span>
            </LabeledItem>
            {status?.startedAt && (
              <LabeledItem label="Started At">
                <span className="text-sm">
                  {dayjs(status.startedAt).format('YYYY-MM-DD HH:mm:ss Z')}
                </span>
              </LabeledItem>
            )}
            {status?.status !== undefined && (
              <LabeledItem label="Status">
                <StatusChip status={status.status} size="sm">
                  {status.statusLabel || ''}
                </StatusChip>
              </LabeledItem>
            )}

            {/* Reschedule checkbox */}
            <div className="flex items-center space-x-2 pt-2 border-t">
              <Checkbox
                id="reschedule-dag"
                checked={retryAsNew}
                onCheckedChange={(checked) => setRetryAsNew(checked as boolean)}
                className="border-border"
              />
              <Label
                htmlFor="reschedule-dag"
                className="cursor-pointer text-sm"
              >
                Reschedule with new DAG-run
              </Label>
            </div>

            {/* Conditional inputs when reschedule is checked */}
            {retryAsNew && (
              <div className="space-y-3 pt-2">
                <div className="space-y-1.5">
                  <Label htmlFor="new-dagrun-id-dag" className="text-sm">
                    New DAG-Run ID (optional)
                  </Label>
                  <Input
                    id="new-dagrun-id-dag"
                    placeholder="Auto-generated if empty"
                    value={newRunId}
                    onChange={(e) => setNewRunId(e.target.value)}
                    className="h-8 text-sm"
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="dag-name-override-dag" className="text-sm">
                    DAG Name Override (optional)
                  </Label>
                  <Input
                    id="dag-name-override-dag"
                    placeholder={`Leave empty to use: ${status?.name || 'original'}`}
                    value={dagNameOverride}
                    onChange={(e) => setDagNameOverride(e.target.value)}
                    className="h-8 text-sm"
                  />
                </div>
                <div className="flex items-center space-x-2">
                  <Checkbox
                    id="use-current-dag-file-dag"
                    checked={useCurrentDagFile}
                    disabled={rescheduleSourceLoading || !specFromFile}
                    onCheckedChange={(checked) =>
                      setUseCurrentDagFile(checked as boolean)
                    }
                    className="border-border"
                  />
                  <div className="space-y-0.5">
                    <Label
                      htmlFor="use-current-dag-file-dag"
                      className="cursor-pointer text-sm"
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
        <StartDAGModal
          dag={startModalDag}
          visible={isEnqueueModal}
          loading={startModalLoading}
          loadError={startModalLoadError}
          action={dagContext.forceEnqueue ? 'enqueue' : undefined}
          profiles={runtimeProfiles}
          profilesLoading={profilesLoading}
          defaultProfile={dagSettingsData?.profile}
          defaultProfileLoading={dagSettingsLoading}
          onSubmit={async (params, dagRunId, immediate, profile) => {
            if (dagContext.onEnqueue) {
              const result =
                profile !== undefined
                  ? await dagContext.onEnqueue(
                      params,
                      dagRunId,
                      immediate,
                      profile
                    )
                  : await dagContext.onEnqueue(params, dagRunId, immediate);
              const startedRunId =
                typeof result === 'string' && result ? result : dagRunId;
              if (startedRunId) {
                await dagContext.onRunStarted?.(startedRunId);
              }
              return;
            }

            const body: {
              params: string;
              dagRunId?: string;
              profile?: string;
            } = { params };
            if (dagRunId) {
              body.dagRunId = dagRunId;
            }
            if (profile !== undefined) {
              body.profile = profile;
            }

            // Use /start endpoint if immediate is true, otherwise use /enqueue
            const { data, error } = await (immediate
              ? client.POST('/dags/{fileName}/start', {
                  params: {
                    path: {
                      fileName: fileName,
                    },
                    query: {
                      remoteNode,
                    },
                  },
                  body,
                })
              : client.POST('/dags/{fileName}/enqueue', {
                  params: {
                    path: {
                      fileName: fileName,
                    },
                    query: {
                      remoteNode,
                    },
                  },
                  body,
                }));
            if (error) {
              throw new Error(
                error.message || 'Failed to start DAG execution.'
              );
            }

            if (data?.dagRunId) {
              await dagContext.onRunStarted?.(data.dagRunId);
            }
            // Just refresh the current page data
            reloadData();
            // Navigate to status tab after execution (if available)
            if (navigateToStatusTab) {
              navigateToStatusTab();
            }
          }}
          dismissModal={() => {
            setIsEnqueueModal(false);
            setStartModalDag(undefined);
            setStartModalLoadError(null);
          }}
        />
        <ConfirmModal
          title="Unsaved Changes"
          buttonText="Run Anyway"
          visible={isUnsavedChangesModal}
          dismissModal={() => {
            setIsUnsavedChangesModal(false);
          }}
          onSubmit={() => {
            setIsUnsavedChangesModal(false);
            setIsEnqueueModal(true);
          }}
        >
          <div className="flex items-start gap-3">
            <AlertTriangle className="h-5 w-5 text-warning flex-shrink-0 mt-0.5" />
            <div className="space-y-2">
              <p className="font-medium">
                You have unsaved changes in the DAG definition.
              </p>
              <p className="text-sm text-muted-foreground">
                The DAG will run with the last saved version, not your current
                edits. Save your changes first if you want them to take effect.
              </p>
            </div>
          </div>
        </ConfirmModal>
      </div>
    </TooltipProvider>
  );
}

export default DAGActions;
