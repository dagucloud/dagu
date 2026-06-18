// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * DAGExecutionHistory component displays the execution history of a DAG.
 *
 * @module features/dags/components/dag-execution
 */
import { useErrorModal } from '@/components/ui/error-modal';
import React, { useEffect, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import {
  components,
  NodeStatus,
  Status,
  Stream,
} from '../../../../api/v1/schema';
import { useRemoteNode } from '../../../../contexts/RemoteNodeContext';
import { useClient, useQuery } from '../../../../hooks/api';
import { useDAGHistorySSE } from '../../../../hooks/useDAGHistorySSE';
import { buildDAGPageURL } from '../../../dag-runs/lib/dagRunUrls';
import {
  sseFallbackOptions,
  useSSECacheSync,
} from '../../../../hooks/useSSECacheSync';
import { toMermaidNodeId } from '../../../../lib/utils';
import LoadingIndicator from '@/components/ui/loading-indicator';
import { DAGContext } from '../../contexts/DAGContext';
import { getEventHandlers } from '../../lib/getEventHandlers';
import { updateDAGRunsNodeStatus } from '../../lib/nodeStatus';
import { DAGStatusOverview, NodeStatusTable } from '../dag-details';
import { StepDetailsDrawer } from '../step-details';
import { DAGGraph } from '../visualization';
import { HistoryTable, LogViewer, StatusUpdateModal } from './';

/**
 * Props for the DAGExecutionHistory component
 */
type Props = {
  /** DAG file name */
  fileName: string;
  /** Whether the component is rendered in a modal */
  isInModal?: boolean;
  /** The active tab in the parent component */
  activeTab?: string;
};

/**
 * DAGExecutionHistory displays the execution history of a DAG
 * including a history table, graph visualization, and status details
 */
function DAGExecutionHistory({
  fileName,
}: Omit<Props, 'isInModal' | 'activeTab'>) {
  const remoteNode = useRemoteNode();

  const historySSE = useDAGHistorySSE(fileName, !!fileName, remoteNode);
  // Fetch execution history data — SWR is the single source of truth, refreshed by live invalidations
  const { data, mutate } = useQuery(
    '/dags/{fileName}/dag-runs',
    {
      params: {
        query: {
          remoteNode,
        },
        path: {
          fileName: fileName,
        },
      },
    },
    sseFallbackOptions(historySSE)
  );
  useSSECacheSync(historySSE, mutate);

  // Show loading indicator while fetching data
  if (!data) {
    return <LoadingIndicator />;
  }

  // Show message if no execution history is found
  if (!data.dagRuns?.length) {
    return <div>Execution history was not found.</div>;
  }

  return (
    <DAGHistoryTable
      fileName={fileName}
      dagRuns={data.dagRuns}
      gridData={data.gridData}
      refreshHistory={() => void mutate()}
    />
  );
}

/**
 * Props for the DAGHistoryTable component
 */
type HistoryTableProps = {
  /** DAG file name */
  fileName: string;
  /** Grid data for visualization */
  gridData: components['schemas']['DAGGridItem'][] | null;
  /** List of DAG dagRuns */
  dagRuns: components['schemas']['DAGRunDetails'][] | null;
  /** Refresh execution history list */
  refreshHistory: () => void;
};

/**
 * DAGHistoryTable displays detailed execution history with interactive elements
 */
function DAGHistoryTable({
  fileName,
  gridData,
  dagRuns,
  refreshHistory,
}: HistoryTableProps) {
  const remoteNode = useRemoteNode();
  const dagContext = React.useContext(DAGContext);
  const client = useClient();
  const navigate = useNavigate();
  const { showError } = useErrorModal();
  const [modal, setModal] = React.useState(false);
  const [displayDAGRuns, setDisplayDAGRuns] = React.useState(dagRuns);

  React.useEffect(() => {
    setDisplayDAGRuns(dagRuns);
  }, [dagRuns]);

  // State for log viewer
  const [logViewer, setLogViewer] = useState<{
    isOpen: boolean;
    logType: 'execution' | 'step';
    stepName: string;
    dagRunId: string;
    stream: Stream;
  }>({
    isOpen: false,
    logType: 'step',
    stepName: '',
    dagRunId: '',
    stream: Stream.stdout,
  });

  // Get the selected dagRun index from URL parameters using React Router
  const [searchParams, setSearchParams] = useSearchParams();
  const idxParam = searchParams.get('idx');
  const [idx, setIdx] = React.useState(
    idxParam
      ? parseInt(idxParam)
      : dagRuns && dagRuns.length
        ? dagRuns.length - 1
        : 0
  );

  // Removed unused context since we're no longer directly updating it

  // Ensure index is valid when dagRuns change (e.g., when switching DAGs)
  React.useEffect(() => {
    if (!displayDAGRuns || displayDAGRuns.length === 0) return;

    // Clamp the index to be within valid range
    const maxIdx = displayDAGRuns.length - 1;
    const validIdx = Math.max(0, Math.min(idx, maxIdx));

    // Only update if the index needs adjustment
    if (validIdx !== idx) {
      const newParams = new URLSearchParams(searchParams);
      newParams.set('idx', validIdx.toString());
      setSearchParams(newParams);
      setIdx(validIdx);
    }
  }, [displayDAGRuns, idx]);

  /**
   * Update the selected dagRun index and update URL parameters
   */
  const updateIdx = (newIdx: number) => {
    // Ensure newIdx is within valid range
    if (newIdx < 0 || !displayDAGRuns || newIdx >= displayDAGRuns.length) {
      return;
    }

    setIdx(newIdx);
    const reversedDAGRuns = [...(displayDAGRuns || [])].reverse();

    if (reversedDAGRuns && reversedDAGRuns[newIdx]) {
      // Instead of directly updating the context, update the URL with the dagRun ID
      const selectedDAGRun = reversedDAGRuns[newIdx];
      const newParams = new URLSearchParams(searchParams);
      newParams.set('idx', newIdx.toString());

      // Add or update the dagRunId parameter
      newParams.set('dagRunId', selectedDAGRun.dagRunId);

      // Add dagRunName parameter to avoid waiting for DAG details
      newParams.set('dagRunName', selectedDAGRun.name);

      setSearchParams(newParams);
    }
  };

  // Listen for URL parameter changes
  useEffect(() => {
    if (idxParam) {
      const newIdx = parseInt(idxParam);
      if (!isNaN(newIdx) && newIdx !== idx) {
        setIdx(newIdx);

        // No longer updating the RootDAGRunContext here
        // The status details page will handle this based on URL parameters
      }
    }
  }, [idxParam, idx]);

  /**
   * Handle keyboard navigation with arrow keys
   */
  const handleKeyDown = React.useCallback(
    (event: KeyboardEvent) => {
      if (event.key === 'ArrowLeft') {
        // Navigate to previous history item
        updateIdx(idx - 1);
      } else if (event.key === 'ArrowRight') {
        // Navigate to next history item
        updateIdx(idx + 1);
      }
    },
    [idx, displayDAGRuns, updateIdx]
  );

  // Add and remove keyboard event listener
  React.useEffect(() => {
    document.addEventListener('keydown', handleKeyDown);
    return () => {
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [handleKeyDown]);

  // Reverse the dagRuns array for display (newest first)
  const reversedDAGRuns = [...(displayDAGRuns || [])].reverse();
  const selectedDAGRun = reversedDAGRuns[idx];
  const handlers = selectedDAGRun ? getEventHandlers(selectedDAGRun) : null;

  // State for the selected step in the status update modal
  const [selectedStep, setSelectedStep] = React.useState<
    components['schemas']['Step'] | undefined
  >(undefined);
  const [selectedDetailStep, setSelectedDetailStep] = React.useState<
    components['schemas']['Step'] | undefined
  >(undefined);
  const [isStepDetailsOpen, setIsStepDetailsOpen] = React.useState(false);

  const closeStepDetails = React.useCallback(() => {
    setIsStepDetailsOpen(false);
  }, []);

  React.useEffect(() => {
    closeStepDetails();
  }, [idx, closeStepDetails]);

  /**
   * Close the status update modal
   */
  const dismissModal = () => setModal(false);

  /**
   * Update the status of a step
   */
  const onUpdateStatus = async (
    _step: components['schemas']['Step'],
    status: NodeStatus
  ) => {
    if (!selectedStep || !selectedDAGRun) {
      return;
    }

    // Call the API to update the step status
    const { error } = await client.PATCH(
      '/dag-runs/{name}/{dagRunId}/steps/{stepName}/status',
      {
        params: {
          path: {
            name: selectedDAGRun.name,
            dagRunId: selectedDAGRun.dagRunId,
            stepName: selectedStep.name,
          },
          query: {
            remoteNode,
          },
        },
        body: {
          status,
        },
      }
    );

    if (error) {
      showError(
        error.message || 'Failed to update status',
        'Please try again or check the server connection.'
      );
      return;
    }

    setDisplayDAGRuns((current) =>
      updateDAGRunsNodeStatus(
        current,
        selectedDAGRun.dagRunId,
        selectedStep.name,
        status
      )
    );
    refreshHistory();
    dagContext.refresh();
    dismissModal();
  };

  const applyDisplayedNodeStatus = React.useCallback(
    (dagRunId: string, stepName: string, status: NodeStatus) => {
      setDisplayDAGRuns((current) =>
        updateDAGRunsNodeStatus(current, dagRunId, stepName, status)
      );
    },
    []
  );

  // Removed the effect that updates the DAG status context
  // The status details page will handle this based on URL parameters

  /**
   * Handle double-click on graph node (navigate to sub dagRun)
   */
  const onSelectStepOnGraph = React.useCallback(
    async (id: string) => {
      const dagRun = reversedDAGRuns[idx];
      if (!dagRun) {
        return;
      }

      // Find the clicked step
      const n = dagRun.nodes?.find((n) => toMermaidNodeId(n.step.name) == id);
      if (!n) return;

      // If it's a sub dagRun, navigate to its details
      const subRuns = [...(n.subRuns ?? []), ...(n.subRunsRepeated ?? [])];

      // Check for sub-DAG: step.call (for call steps) OR subRun.dagName (for chat tools, etc.)
      const subDAGName = n.step?.call || subRuns[0]?.dagName;
      if (!subDAGName || subRuns.length === 0) return;

      const subDAGRun = subRuns[0];
      if (subDAGRun && subDAGRun.dagRunId) {
        navigate(
          buildDAGPageURL({
            fileName,
            remoteNode,
            rootDAGRunId: dagRun.rootDAGRunId,
            rootDAGRunName: dagRun.rootDAGRunName,
            subDAGRunId: subDAGRun.dagRunId,
          })
        );
      }
    },
    [fileName, idx, navigate, remoteNode, reversedDAGRuns]
  );

  const onInspectStepOnGraph = React.useCallback(
    (id: string) => {
      const dagRun = reversedDAGRuns[idx];
      if (!dagRun) {
        return;
      }

      const n = dagRun.nodes?.find(
        (node) => toMermaidNodeId(node.step.name) == id
      );
      if (!n) {
        return;
      }

      setSelectedDetailStep(n.step);
      setIsStepDetailsOpen(true);
    },
    [reversedDAGRuns, idx]
  );

  /**
   * Handle right-click on graph node (show status update modal)
   */
  const onRightClickStepOnGraph = React.useCallback(
    (id: string) => {
      const dagRun = reversedDAGRuns[idx];
      if (!dagRun) {
        return;
      }

      // Only allow status updates for completed dagRuns
      if (
        dagRun.status == Status.Running ||
        dagRun.status == Status.NotStarted
      ) {
        return;
      }

      // Find the right-clicked step
      const n = dagRun.nodes?.find((n) => toMermaidNodeId(n.step.name) == id);

      if (n) {
        setSelectedStep(n.step);
        setModal(true);
      }
    },
    [reversedDAGRuns, idx]
  );

  return (
    <DAGContext.Consumer>
      {(props) => (
        <div className="space-y-6">
          <HistoryTable
            dagRuns={reversedDAGRuns || []}
            gridData={gridData || []}
            onSelect={updateIdx}
            idx={idx}
          />

          {selectedDAGRun ? (
            <React.Fragment>
              <DAGGraph
                dagRun={selectedDAGRun}
                onClickStep={onInspectStepOnGraph}
                onSelectStep={onSelectStepOnGraph}
                onRightClickStep={onRightClickStepOnGraph}
              />

              <div className="bg-surface border border-border rounded-lg p-4">
                <DAGStatusOverview
                  status={selectedDAGRun}
                  onViewLog={(dagRunId) => {
                    setLogViewer({
                      isOpen: true,
                      logType: 'execution',
                      stepName: '',
                      dagRunId,
                      stream: Stream.stdout,
                    });
                  }}
                />
              </div>

              <NodeStatusTable
                nodes={selectedDAGRun.nodes}
                status={selectedDAGRun}
                {...props}
                onNodeStatusUpdated={(stepName, status) =>
                  applyDisplayedNodeStatus(
                    selectedDAGRun.dagRunId,
                    stepName,
                    status
                  )
                }
                onViewLog={(stepName, dagRunId) => {
                  const isStderr = stepName.endsWith('_stderr');
                  const actualStepName = isStderr
                    ? stepName.slice(0, -7)
                    : stepName;

                  setLogViewer({
                    isOpen: true,
                    logType: 'step',
                    stepName: actualStepName,
                    dagRunId: dagRunId || selectedDAGRun.dagRunId,
                    stream: isStderr ? Stream.stderr : Stream.stdout,
                  });
                }}
              />

              {handlers && handlers.length ? (
                <NodeStatusTable
                  nodes={handlers}
                  status={selectedDAGRun}
                  {...props}
                  onNodeStatusUpdated={(stepName, status) =>
                    applyDisplayedNodeStatus(
                      selectedDAGRun.dagRunId,
                      stepName,
                      status
                    )
                  }
                  onViewLog={(stepName, dagRunId) => {
                    const isStderr = stepName.endsWith('_stderr');
                    const actualStepName = isStderr
                      ? stepName.slice(0, -7)
                      : stepName;

                    setLogViewer({
                      isOpen: true,
                      logType: 'step',
                      stepName: actualStepName,
                      dagRunId: dagRunId || selectedDAGRun.dagRunId,
                      stream: isStderr ? Stream.stderr : Stream.stdout,
                    });
                  }}
                />
              ) : null}

              {/* Log viewer modal - moved outside to handle all log viewing */}
              <LogViewer
                isOpen={logViewer.isOpen}
                onClose={() =>
                  setLogViewer((prev) => ({ ...prev, isOpen: false }))
                }
                logType={logViewer.logType}
                dagName={selectedDAGRun.name}
                dagRunId={logViewer.dagRunId}
                stepName={logViewer.stepName}
                dagRun={selectedDAGRun}
                stream={logViewer.stream}
              />
            </React.Fragment>
          ) : null}

          <StatusUpdateModal
            visible={modal}
            step={selectedStep}
            dismissModal={dismissModal}
            onSubmit={onUpdateStatus}
          />

          <StepDetailsDrawer
            dagName={selectedDAGRun?.name}
            isOpen={isStepDetailsOpen}
            step={selectedDetailStep}
            onClose={closeStepDetails}
          />
        </div>
      )}
    </DAGContext.Consumer>
  );
}

export default DAGExecutionHistory;
