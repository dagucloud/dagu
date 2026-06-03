// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useErrorModal } from '@/components/ui/error-modal';
import { Tab, Tabs } from '@/components/ui/tabs';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import {
  ActivitySquare,
  Archive,
  FileCode,
  GanttChart,
  GripHorizontal,
  MessageSquare,
  MousePointerClick,
  Package,
  ShieldCheck,
} from 'lucide-react';
import React, { useEffect, useState } from 'react';
import { useCookies } from 'react-cookie';
import { useNavigate } from 'react-router-dom';
import { components, NodeStatus, Status, Stream } from '../../../api/v1/schema';
import { AppBarContext } from '../../../contexts/AppBarContext';
import { useConfig } from '../../../contexts/ConfigContext';
import { useClient } from '../../../hooks/api';
import { cn, toMermaidNodeId } from '../../../lib/utils';
import BorderedBox from '@/components/ui/bordered-box';
import { DAGRunOutputs } from '../../dag-runs/components/dag-run-details';
import { DAGContext } from '../contexts/DAGContext';
import { getEventHandlers } from '../lib/getEventHandlers';
import { updateDAGRunNodeStatus } from '../lib/nodeStatus';
import { ApprovalTab } from './approval';
import ArtifactsTab from './artifacts/ArtifactsTab';
import { ChatHistoryTab } from './chat-history';
import { DAGStatusOverview, NodeStatusTable } from './dag-details';
import { DAGSpecReadOnly } from './dag-editor';
import { StepDetailsDrawer } from './step-details';
import {
  LogViewer,
  ParallelExecutionModal,
  StatusUpdateModal,
} from './dag-execution';
import { FlowchartType, Graph, TimelineChart } from './visualization';

type Props = {
  dagRun: components['schemas']['DAGRunDetails'];
  fileName: string;
  artifactEnabled?: boolean;
  initialTab?: StatusTab;
  fillHeight?: boolean;
};

export type StatusTab =
  | 'status'
  | 'timeline'
  | 'outputs'
  | 'artifacts'
  | 'chat'
  | 'spec'
  | 'approval';

/** Check if the current DAG run is a sub DAG-run (has a different root) */
function isSubDAGRun(dagRun: components['schemas']['DAGRunDetails']): boolean {
  return !!(
    dagRun.rootDAGRunId &&
    dagRun.rootDAGRunName &&
    dagRun.rootDAGRunId !== dagRun.dagRunId
  );
}

function DAGStatus({
  dagRun,
  fileName,
  artifactEnabled = false,
  initialTab = 'status',
  fillHeight = false,
}: Props) {
  const appBarContext = React.useContext(AppBarContext);
  const dagContext = React.useContext(DAGContext);
  const config = useConfig();
  const navigate = useNavigate();
  const { showError } = useErrorModal();
  const [modal, setModal] = useState(false);
  const [activeTab, setActiveTab] = useState<StatusTab>(initialTab);
  const [displayDAGRun, setDisplayDAGRun] = useState(dagRun);

  useEffect(() => {
    setDisplayDAGRun(dagRun);
  }, [dagRun]);

  // Flowchart direction preference stored in cookies
  const [cookie, setCookie] = useCookies(['flowchart']);
  const [flowchart, setFlowchart] = useState<FlowchartType>(
    cookie['flowchart']
  );

  const [graphHeight, setGraphHeight] = useState(380);

  const [selectedStep, setSelectedStep] = useState<
    components['schemas']['Step'] | undefined
  >(undefined);
  const [selectedDetailStep, setSelectedDetailStep] = useState<
    components['schemas']['Step'] | undefined
  >(undefined);
  const [isStepDetailsOpen, setIsStepDetailsOpen] = useState(false);

  const closeStepDetails = React.useCallback(() => {
    setIsStepDetailsOpen(false);
  }, []);

  useEffect(() => {
    if (activeTab !== 'status') {
      closeStepDetails();
    }
  }, [activeTab, closeStepDetails]);

  const handleResizeMouseDown = (e: React.MouseEvent) => {
    e.preventDefault();
    const startY = e.clientY;
    const startHeight = graphHeight;

    const handleMouseMove = (mv: MouseEvent) => {
      const newHeight = startHeight + (mv.clientY - startY);
      setGraphHeight(Math.max(200, newHeight));
    };

    const handleMouseUp = () => {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
    };

    document.addEventListener('mousemove', handleMouseMove);
    document.addEventListener('mouseup', handleMouseUp);
  };
  // State for log viewer
  const [logViewer, setLogViewer] = useState<{
    isOpen: boolean;
    logType: 'execution' | 'step';
    stepName: string;
    dagRunId: string;
    stream: Stream;
    node?: components['schemas']['Node'];
  }>({
    isOpen: false,
    logType: 'step',
    stepName: '',
    dagRunId: '',
    stream: Stream.stdout,
  });
  // State for parallel execution modal
  const [parallelExecutionModal, setParallelExecutionModal] = useState<{
    isOpen: boolean;
    node?: components['schemas']['Node'];
  }>({
    isOpen: false,
  });
  const client = useClient();
  const dismissModal = () => setModal(false);

  /**
   * Handle flowchart direction change and save preference to cookie
   */
  const onChangeFlowchart = (value: FlowchartType) => {
    if (!value) {
      return;
    }
    setCookie('flowchart', value, { path: '/' });
    setFlowchart(value);
  };

  const applyDisplayNodeStatus = React.useCallback(
    (stepName: string, status: NodeStatus) => {
      setDisplayDAGRun((current) =>
        updateDAGRunNodeStatus(current, stepName, status)
      );
    },
    []
  );

  const onUpdateStatus = async (
    step: components['schemas']['Step'],
    status: NodeStatus
  ) => {
    const isSubRun = isSubDAGRun(displayDAGRun);

    // Define path parameters with proper typing
    const pathParams = {
      name: isSubRun ? displayDAGRun.rootDAGRunName : displayDAGRun.name,
      dagRunId: isSubRun ? displayDAGRun.rootDAGRunId : displayDAGRun.dagRunId,
      stepName: step.name,
      ...(isSubRun ? { subDAGRunId: displayDAGRun.dagRunId } : {}),
    };

    // Use the appropriate endpoint based on whether this is a sub DAG-run
    const endpoint = isSubRun
      ? '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/status'
      : '/dag-runs/{name}/{dagRunId}/steps/{stepName}/status';

    const { error } = await client.PATCH(endpoint, {
      params: {
        path: pathParams,
        query: {
          remoteNode: appBarContext.selectedRemoteNode || 'local',
        },
      },
      body: {
        status,
      },
    });
    if (error) {
      showError(
        error.message || 'Failed to update status',
        'Please try again or check the server connection.'
      );
      return;
    }
    applyDisplayNodeStatus(step.name, status);
    dagContext.refresh();
    dismissModal();
  };
  // Handle double-click on graph node (navigate to sub dagRun)
  const onSelectStepOnGraph = React.useCallback(
    async (id: string) => {
      // find the clicked step
      const n = displayDAGRun.nodes?.find(
        (n) => toMermaidNodeId(n.step.name) == id
      );
      if (!n) return;

      // Combine both regular children and repeated children
      const allSubRuns = [...(n.subRuns || []), ...(n.subRunsRepeated || [])];

      // Check for sub-DAG: step.call (for call steps) OR subRun.dagName (for chat tools, etc.)
      const subDAGName = n.step?.call || allSubRuns[0]?.dagName;
      if (subDAGName && allSubRuns.length > 0) {
        // Check if there are multiple sub runs (parallel execution or repeated)
        if (allSubRuns.length > 1) {
          // Show modal to select which execution to view
          setParallelExecutionModal({
            isOpen: true,
            node: n,
          });
        } else {
          // Single sub dagRun - navigate directly
          navigateToSubDagRun(n, 0);
        }
      }
    },
    [displayDAGRun, navigate, fileName]
  );

  const onInspectStepOnGraph = React.useCallback(
    (id: string) => {
      const n = displayDAGRun.nodes?.find(
        (node) => toMermaidNodeId(node.step.name) == id
      );
      if (!n) {
        return;
      }
      setSelectedDetailStep(n.step);
      setIsStepDetailsOpen(true);
    },
    [displayDAGRun]
  );

  // Helper function to navigate to a specific sub DAG run
  const navigateToSubDagRun = React.useCallback(
    (
      node: components['schemas']['Node'],
      childIndex: number,
      openInNewTab?: boolean
    ) => {
      // Combine both regular children and repeated children
      const allSubRuns = [
        ...(node.subRuns || []),
        ...(node.subRunsRepeated || []),
      ];
      const subDAGRun = allSubRuns[childIndex];

      if (subDAGRun && subDAGRun.dagRunId) {
        // Navigate to the sub DAG-run status page
        const dagRunId = displayDAGRun.rootDAGRunId || displayDAGRun.dagRunId;

        // Check if we're in a dagRun context or a DAG context
        const currentPath = window.location.pathname;
        const isModal =
          document.querySelector('.dagRun-modal-content') !== null;
        const isDAGRunContext = currentPath.startsWith('/dag-runs/') || isModal;

        let url: string;
        if (isDAGRunContext) {
          // For DAG runs, use query parameters to navigate to the DAG-run details page
          const searchParams = new URLSearchParams();
          searchParams.set('subDAGRunId', subDAGRun.dagRunId);

          // Use root DAG-run information
          if (displayDAGRun.rootDAGRunId) {
            searchParams.set('dagRunId', displayDAGRun.rootDAGRunId);
            searchParams.set('dagRunName', displayDAGRun.rootDAGRunName);
          } else {
            searchParams.set('dagRunId', displayDAGRun.dagRunId);
            searchParams.set('dagRunName', displayDAGRun.name);
          }

          searchParams.set('step', node.step.name);

          // Determine root DAG name
          const rootDAGName =
            displayDAGRun.rootDAGRunName || displayDAGRun.name;
          url = `/dag-runs/${rootDAGName}/${dagRunId}?${searchParams.toString()}`;
        } else {
          // For DAGs, use the existing approach with query parameters
          url = `/dags/${fileName}?subDAGRunId=${subDAGRun.dagRunId}&dagRunId=${dagRunId}&step=${node.step.name}&dagRunName=${encodeURIComponent(displayDAGRun.rootDAGRunName || displayDAGRun.name)}`;
        }

        if (openInNewTab) {
          window.open(url, '_blank');
        } else {
          navigate(url);
        }
      }
    },
    [displayDAGRun, navigate, fileName]
  );

  // Handle right-click on graph node (show status update modal)
  const onRightClickStepOnGraph = React.useCallback(
    (id: string) => {
      // Check if user has permission to run DAGs
      if (!config.permissions.runDags) {
        return;
      }

      const status = displayDAGRun.status;

      // Only allow status updates for completed DAG runs
      if (status !== Status.Running && status !== Status.NotStarted) {
        // find the right-clicked step
        const n = displayDAGRun.nodes?.find(
          (n) => toMermaidNodeId(n.step.name) == id
        );

        if (n) {
          // Show the modal (it will be centered by default)
          setSelectedStep(n.step);
          setModal(true);
        }
      }
    },
    [displayDAGRun, config.permissions.runDags]
  );

  const handlers = getEventHandlers(displayDAGRun);

  // Handler for opening log viewer
  const handleViewLog = (
    stepName: string,
    dagRunId: string,
    node?: components['schemas']['Node']
  ) => {
    // Check if this is a stderr log (indicated by _stderr suffix)
    const isStderr = stepName.endsWith('_stderr');
    const actualStepName = isStderr ? stepName.slice(0, -7) : stepName; // Remove '_stderr' suffix

    setLogViewer({
      isOpen: true,
      logType: 'step',
      stepName: actualStepName,
      dagRunId: dagRunId || displayDAGRun.dagRunId,
      stream: isStderr ? Stream.stderr : Stream.stdout,
      node,
    });
  };

  // Check if timeline should be shown (any status except not started)
  const showTimeline = displayDAGRun.status !== Status.NotStarted;

  // Check if there are any chat steps
  const hasChatSteps = !!displayDAGRun.nodes?.some(
    (node) => node.step.executorConfig?.type === 'chat'
  );

  // Check if there are any steps awaiting approval
  const waitingStepCount =
    displayDAGRun.nodes?.filter((node) => node.status === NodeStatus.Waiting)
      .length || 0;
  const hasWaitingSteps = waitingStepCount > 0;
  const hasArtifacts = artifactEnabled || !!displayDAGRun.artifactsAvailable;

  useEffect(() => {
    setActiveTab(initialTab);
  }, [displayDAGRun.dagRunId, initialTab]);

  // Reset to status tab if selected tab is not available
  useEffect(() => {
    if (activeTab === 'timeline' && !showTimeline) {
      setActiveTab('status');
    }
    if (activeTab === 'chat' && !hasChatSteps) {
      setActiveTab('status');
    }
    if (activeTab === 'approval' && !hasWaitingSteps) {
      setActiveTab('status');
    }
    if (activeTab === 'artifacts' && !hasArtifacts) {
      setActiveTab('status');
    }
  }, [showTimeline, hasChatSteps, hasWaitingSteps, hasArtifacts, activeTab]);

  // Auto-switch to approval tab when steps enter waiting state
  useEffect(() => {
    if (hasWaitingSteps) {
      setActiveTab('approval');
    }
  }, [hasWaitingSteps]);

  const scrollPaneClassName = fillHeight
    ? 'min-h-0 flex-1 overflow-auto pr-1'
    : '';

  return (
    <div
      className={cn(
        'space-y-4',
        fillHeight && 'flex h-full min-h-0 flex-col gap-4 space-y-0'
      )}
    >
      {/* Status Detail Tabs */}
      <div
        className={cn(
          'w-full min-w-0 overflow-x-auto',
          fillHeight && 'shrink-0'
        )}
      >
        <Tabs className="w-max min-w-full whitespace-nowrap">
          <Tab
            isActive={activeTab === 'status'}
            onClick={() => setActiveTab('status')}
            className="flex cursor-pointer items-center gap-2 px-3 sm:px-4"
          >
            <ActivitySquare className="h-4 w-4" />
            Status
          </Tab>
          {hasWaitingSteps && (
            <Tab
              isActive={activeTab === 'approval'}
              onClick={() => setActiveTab('approval')}
              className="flex cursor-pointer items-center gap-2 px-3 sm:px-4"
            >
              <ShieldCheck className="h-4 w-4" />
              Approval
              <span className="rounded-full bg-warning/15 px-1.5 py-0.5 text-xs font-medium text-warning">
                {waitingStepCount}
              </span>
            </Tab>
          )}
          {showTimeline && (
            <Tab
              isActive={activeTab === 'timeline'}
              onClick={() => setActiveTab('timeline')}
              className="flex cursor-pointer items-center gap-2 px-3 sm:px-4"
            >
              <GanttChart className="h-4 w-4" />
              Timeline
            </Tab>
          )}
          <Tab
            isActive={activeTab === 'outputs'}
            onClick={() => setActiveTab('outputs')}
            className="flex cursor-pointer items-center gap-2 px-3 sm:px-4"
          >
            <Package className="h-4 w-4" />
            Outputs
          </Tab>
          {hasArtifacts && (
            <Tab
              isActive={activeTab === 'artifacts'}
              onClick={() => setActiveTab('artifacts')}
              className="flex cursor-pointer items-center gap-2 px-3 sm:px-4"
            >
              <Archive className="h-4 w-4" />
              Artifacts
            </Tab>
          )}
          {hasChatSteps && (
            <Tab
              isActive={activeTab === 'chat'}
              onClick={() => setActiveTab('chat')}
              className="flex cursor-pointer items-center gap-2 px-3 sm:px-4"
            >
              <MessageSquare className="h-4 w-4" />
              Chat
            </Tab>
          )}
          <Tab
            isActive={activeTab === 'spec'}
            onClick={() => setActiveTab('spec')}
            className="flex cursor-pointer items-center gap-2 px-3 sm:px-4"
          >
            <FileCode className="h-4 w-4" />
            Spec
          </Tab>
        </Tabs>
      </div>

      {/* Status Tab Content */}
      {activeTab === 'status' && (
        <div className={cn('space-y-6', scrollPaneClassName)}>
          {/* DAG Graph Visualization */}
          {displayDAGRun.nodes && displayDAGRun.nodes.length > 0 && (
            <div className="flex flex-col">
              <BorderedBox className="pt-4 px-4 pb-0 flex flex-col items-stretch overflow-hidden">
                <div className="flex justify-end mb-2">
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <div
                        className="flex h-7 w-7 items-center justify-center rounded bg-muted text-muted-foreground cursor-help"
                        aria-label="Graph interactions"
                      >
                        <MousePointerClick className="h-3.5 w-3.5" />
                      </div>
                    </TooltipTrigger>
                    <TooltipContent>
                      <div className="space-y-1">
                        <p>Click: Inspect step details</p>
                        <p>Double-click: Navigate to sub dagRun</p>
                        {config.permissions.runDags && (
                          <p>Right-click: Update node status</p>
                        )}
                      </div>
                    </TooltipContent>
                  </Tooltip>
                </div>
                <div className="overflow-x-auto -mx-4 px-4">
                  <Graph
                    steps={displayDAGRun.nodes}
                    type="status"
                    flowchart={flowchart}
                    onChangeFlowchart={onChangeFlowchart}
                    onClickNode={onInspectStepOnGraph}
                    selectOnClick
                    onDoubleClickNode={onSelectStepOnGraph}
                    onRightClickNode={
                      config.permissions.runDags
                        ? onRightClickStepOnGraph
                        : undefined
                    }
                    showIcons={displayDAGRun.status > Status.NotStarted}
                    animate={displayDAGRun.status == Status.Running}
                    height={graphHeight}
                  />
                </div>
                <div
                  className="flex justify-center items-center py-2 cursor-row-resize hover:bg-muted/50 transition-colors w-full select-none"
                  onMouseDown={handleResizeMouseDown}
                >
                  <GripHorizontal className="h-4 w-4 text-muted-foreground/50" />
                </div>
              </BorderedBox>
            </div>
          )}

          <DAGContext.Consumer>
            {(props) => (
              <>
                <div className="grid min-w-0 grid-cols-1 gap-6">
                  {/* Status Overview */}
                  <div className="bg-surface border border-border rounded-lg p-4">
                    <DAGStatusOverview
                      status={displayDAGRun}
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

                  {/* Steps Table */}
                  <NodeStatusTable
                    nodes={displayDAGRun.nodes}
                    status={displayDAGRun}
                    {...props}
                    onViewLog={handleViewLog}
                    onNodeStatusUpdated={applyDisplayNodeStatus}
                  />
                </div>

                {/* Lifecycle Hooks */}
                {handlers?.length ? (
                  <NodeStatusTable
                    nodes={handlers}
                    status={displayDAGRun}
                    {...props}
                    onViewLog={handleViewLog}
                    onNodeStatusUpdated={applyDisplayNodeStatus}
                  />
                ) : null}
              </>
            )}
          </DAGContext.Consumer>
        </div>
      )}

      {/* Approval Tab Content */}
      {activeTab === 'approval' && hasWaitingSteps && (
        <div className={scrollPaneClassName}>
          <ApprovalTab dagRun={displayDAGRun} dagName={displayDAGRun.name} />
        </div>
      )}

      {/* Timeline Tab Content */}
      {activeTab === 'timeline' && showTimeline && (
        <div className={scrollPaneClassName}>
          <TimelineChart status={displayDAGRun} />
        </div>
      )}

      {/* Outputs Tab Content */}
      {activeTab === 'outputs' && (
        <div className={scrollPaneClassName}>
          <DAGRunOutputs
            dagName={displayDAGRun.name}
            dagRunId={displayDAGRun.dagRunId}
          />
        </div>
      )}

      {activeTab === 'artifacts' && hasArtifacts && (
        <ArtifactsTab
          dagRun={displayDAGRun}
          artifactEnabled={artifactEnabled}
          className={fillHeight ? 'min-h-0 flex-1' : undefined}
          fillHeight={fillHeight}
        />
      )}

      {/* Chat Tab Content */}
      {activeTab === 'chat' && (
        <div className={scrollPaneClassName}>
          <ChatHistoryTab dagRun={displayDAGRun} />
        </div>
      )}

      {/* Spec Tab Content */}
      {activeTab === 'spec' && (
        <div className={scrollPaneClassName}>
          <DAGSpecReadOnly
            dagName={
              isSubDAGRun(displayDAGRun)
                ? displayDAGRun.rootDAGRunName
                : displayDAGRun.name
            }
            dagRunId={
              isSubDAGRun(displayDAGRun)
                ? displayDAGRun.rootDAGRunId
                : displayDAGRun.dagRunId
            }
            subDAGRunId={
              isSubDAGRun(displayDAGRun) ? displayDAGRun.dagRunId : undefined
            }
            sourceFileName={
              isSubDAGRun(displayDAGRun)
                ? undefined
                : displayDAGRun.sourceFileName
            }
          />
        </div>
      )}

      <StatusUpdateModal
        visible={modal}
        step={selectedStep}
        dismissModal={dismissModal}
        onSubmit={onUpdateStatus}
      />

      <StepDetailsDrawer
        dagName={displayDAGRun.name}
        isOpen={isStepDetailsOpen}
        step={selectedDetailStep}
        onClose={closeStepDetails}
      />

      {/* Log viewer modal */}
      <LogViewer
        isOpen={logViewer.isOpen}
        onClose={() => setLogViewer((prev) => ({ ...prev, isOpen: false }))}
        logType={logViewer.logType}
        dagName={displayDAGRun.name}
        dagRunId={logViewer.dagRunId}
        stepName={logViewer.stepName}
        dagRun={displayDAGRun}
        stream={logViewer.stream}
        node={logViewer.node}
      />

      {/* Parallel execution selection modal */}
      {parallelExecutionModal.isOpen && parallelExecutionModal.node && (
        <ParallelExecutionModal
          isOpen={parallelExecutionModal.isOpen}
          onClose={() => setParallelExecutionModal({ isOpen: false })}
          stepName={parallelExecutionModal.node.step.name}
          subDAGName={parallelExecutionModal.node.step.call || ''}
          subRuns={[
            ...(parallelExecutionModal.node.subRuns || []),
            ...(parallelExecutionModal.node.subRunsRepeated || []),
          ]}
          rootDagName={displayDAGRun.rootDAGRunName}
          rootDagRunId={displayDAGRun.rootDAGRunId}
          parentDagRunId={displayDAGRun.dagRunId}
          onSelectSubRun={(subRunIndex, openInNewTab) => {
            navigateToSubDagRun(
              parallelExecutionModal.node!,
              subRunIndex,
              openInNewTab
            );
            if (!openInNewTab) {
              setParallelExecutionModal({ isOpen: false });
            }
          }}
        />
      )}
    </div>
  );
}

export default DAGStatus;
