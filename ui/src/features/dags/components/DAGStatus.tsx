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
  AlertTriangle,
  Archive,
  Bot,
  ClipboardCheck,
  FileCode,
  ListChecks,
  GanttChart,
  GripHorizontal,
  MessageSquare,
  MousePointerClick,
  Package,
  ShieldCheck,
  ScrollText,
} from 'lucide-react';
import React, { useEffect, useState } from 'react';
import { useCookies } from 'react-cookie';
import { useNavigate } from 'react-router-dom';
import { components, NodeStatus, Status, Stream } from '../../../api/v1/schema';
import { useConfig } from '../../../contexts/ConfigContext';
import { useRemoteNode } from '../../../contexts/RemoteNodeContext';
import { useClient } from '../../../hooks/api';
import { cn, toMermaidNodeId } from '../../../lib/utils';
import BorderedBox from '@/components/ui/bordered-box';
import { DAGRunOutputs } from '../../dag-runs/components/dag-run-details';
import { getManualActionState } from '../../dag-runs/lib/manualActionState';
import { DAGContext } from '../contexts/DAGContext';
import { getEventHandlers } from '../lib/getEventHandlers';
import { updateDAGRunNodeStatus } from '../lib/nodeStatus';
import { ApprovalTab } from './approval';
import { AgentSessionTab } from './agent-session';
import ArtifactsTab from './artifacts/ArtifactsTab';
import { ChatHistoryTab } from './chat-history';
import { AgentTimeline, TaskChecklistTab } from './agent';
import {
  SubRunOpenProvider,
  SubRunStackModal,
  useOpenSubRun,
  type SubRunStackEntry,
} from './common';
import { DAGStatusOverview, NodeStatusTable } from './dag-details';
import { DAGSpecReadOnly } from './dag-editor';
import { StepDetailsDrawer } from './step-details';
import {
  LogViewer,
  ParallelExecutionModal,
  StatusUpdateModal,
} from './dag-execution';
import { FlowchartType, Graph, TimelineChart } from './visualization';
import { HumanTasksTab } from './human-task';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

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
  | 'agent'
  | 'chat'
  | 'tasks'
  | 'spec'
  | 'approval'
  | 'human-tasks';

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
  const { ts } = useI18n();
  const dagContext = React.useContext(DAGContext);
  const config = useConfig();
  const remoteNode = useRemoteNode();
  const navigate = useNavigate();
  const { showError } = useErrorModal();
  const [modal, setModal] = useState(false);
  const [activeTab, setActiveTab] = useState<StatusTab>(initialTab);
  const [selectedAgentStep, setSelectedAgentStep] = useState('');
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
  // Stored by name and re-derived each render so the open drawer tracks live
  // node updates arriving over SSE.
  const [selectedDetailStepName, setSelectedDetailStepName] = useState<
    string | undefined
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
          remoteNode,
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
          openSubRunAt(n, 0);
        }
      }
    },
    [displayDAGRun, navigate, fileName, remoteNode]
  );

  const onInspectStepOnGraph = React.useCallback(
    (id: string) => {
      const n = displayDAGRun.nodes?.find(
        (node) => toMermaidNodeId(node.step.name) == id
      );
      if (!n) {
        return;
      }
      setSelectedDetailStepName(n.step.name);
      setIsStepDetailsOpen(true);
    },
    [displayDAGRun]
  );

  const selectedDetailNode = React.useMemo(
    () =>
      selectedDetailStepName
        ? displayDAGRun.nodes?.find(
            (node) => node.step.name === selectedDetailStepName
          )
        : undefined,
    [displayDAGRun, selectedDetailStepName]
  );

  // Child runs open as a stack rather than a navigation, so the run you started
  // from stays put. A nested DAGStatus pushes onto the stack already open.
  const [childRunStack, setChildRunStack] = React.useState<SubRunStackEntry[]>(
    []
  );
  const pushOntoOpenStack = useOpenSubRun();

  const openSubRun = React.useCallback(
    (entry: SubRunStackEntry) => {
      if (pushOntoOpenStack) {
        pushOntoOpenStack(entry);
        return;
      }
      setChildRunStack([entry]);
    },
    [pushOntoOpenStack]
  );

  // Opens one of a node's child runs. A plain click stacks it; callers pass
  // openInNewTab for a modifier click, which still opens a real page.
  const openSubRunAt = React.useCallback(
    (node: components['schemas']['Node'], childIndex: number) => {
      const all = [...(node.subRuns || []), ...(node.subRunsRepeated || [])];
      const child = all[childIndex];
      if (!child?.dagRunId) return;
      openSubRun({
        name: child.dagName || node.step.call || node.step.name,
        dagRunId: child.dagRunId,
      });
    },
    [openSubRun]
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
          searchParams.set('remoteNode', remoteNode);
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
          const searchParams = new URLSearchParams();
          searchParams.set('remoteNode', remoteNode);
          searchParams.set('subDAGRunId', subDAGRun.dagRunId);
          searchParams.set('dagRunId', dagRunId);
          searchParams.set('step', node.step.name);
          searchParams.set(
            'dagRunName',
            displayDAGRun.rootDAGRunName || displayDAGRun.name
          );
          url = `/dags/${fileName}?${searchParams.toString()}`;
        }

        if (openInNewTab) {
          window.open(url, '_blank');
        } else {
          navigate(url);
        }
      }
    },
    [displayDAGRun, navigate, fileName, remoteNode]
  );

  const openAgentChildRun = React.useCallback(
    (event: components['schemas']['AgentEvent']) => {
      if (!event.childDagRunId) return;
      openSubRun({
        name: event.childDagName || event.name || 'child',
        dagRunId: event.childDagRunId,
      });
    },
    [openSubRun]
  );

  // Handle right-click on graph node (show status update modal)
  const onRightClickStepOnGraph = React.useCallback(
    (id: string) => {
      // Check if user has permission to run DAGs
      if (!config.permissions.runDags) {
        return;
      }

      const status = displayDAGRun.status;
      if (
        status === Status.NotStarted ||
        status === Status.Running ||
        status === Status.Queued ||
        status === Status.Waiting
      ) {
        return;
      }

      const node = displayDAGRun.nodes?.find(
        (candidate) => toMermaidNodeId(candidate.step.name) === id
      );
      if (!node || node.step.humanTask) {
        return;
      }

      setSelectedStep(node.step);
      setModal(true);
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

  // Chat and agent steps both persist an LLM transcript. Runs recorded before
  // agent DAGs were renamed still carry the 'controller' executor type.
  const hasChatSteps = !!displayDAGRun.nodes?.some((node) =>
    ['chat', 'agent', 'controller'].includes(
      node.step.executorConfig?.type ?? ''
    )
  );
  const agentNodes = (displayDAGRun.nodes || []).filter(
    (node) => !!node.agentSession
  );
  const hasAgentSessions = agentNodes.length > 0;
  const waitingAgentCount = agentNodes.filter(
    (node) =>
      node.agentSession?.state === 'waiting' ||
      node.agentSession?.state === 'unavailable'
  ).length;

  // Agent DAG-runs track goal progress alongside their steps.
  const agentTasks = displayDAGRun.agentTasks ?? [];
  const hasAgentTasks = agentTasks.length > 0;
  const failedNode = displayDAGRun.nodes?.find(
    (node) =>
      node.status === NodeStatus.Failed || node.status === NodeStatus.Rejected
  );

  // An agent has no dependency edges, so its graph carries no information.
  // The decision timeline takes that slot instead.
  const agentEvents = displayDAGRun.agentEvents ?? [];
  const isAgentRun = hasAgentTasks || agentEvents.length > 0;

  const { waitingApprovalNodes, waitingHumanTaskNodes, hasHumanTaskWork } =
    getManualActionState(displayDAGRun);
  const waitingApprovalCount = waitingApprovalNodes.length;
  const waitingHumanTaskCount = waitingHumanTaskNodes.length;
  const hasWaitingApprovals = waitingApprovalCount > 0;
  const hasArtifacts = artifactEnabled || !!displayDAGRun.artifactsAvailable;
  const displayDAGRunIdentity = JSON.stringify([
    remoteNode,
    displayDAGRun.name,
    displayDAGRun.dagRunId,
  ]);

  useEffect(() => {
    setActiveTab(initialTab);
    setSelectedAgentStep('');
  }, [displayDAGRunIdentity, initialTab]);

  // Reset to status tab if selected tab is not available
  useEffect(() => {
    if (activeTab === 'timeline' && !showTimeline) {
      setActiveTab('status');
    }
    if (activeTab === 'chat' && !hasChatSteps) {
      setActiveTab('status');
    }
    if (activeTab === 'agent' && !hasAgentSessions) {
      setActiveTab('status');
    }
    if (activeTab === 'tasks' && !hasAgentTasks) {
      setActiveTab('status');
    }
    if (activeTab === 'approval' && !hasWaitingApprovals) {
      setActiveTab('status');
    }
    if (activeTab === 'human-tasks' && !hasHumanTaskWork) {
      setActiveTab('status');
    }
    if (activeTab === 'artifacts' && !hasArtifacts) {
      setActiveTab('status');
    }
  }, [
    showTimeline,
    hasChatSteps,
    hasAgentSessions,
    hasAgentTasks,
    hasWaitingApprovals,
    hasHumanTaskWork,
    hasArtifacts,
    activeTab,
  ]);

  // Surface a newly available manual action from the default status tab.
  useEffect(() => {
    setActiveTab((currentTab) => {
      if (currentTab !== 'status') {
        return currentTab;
      }
      if (waitingAgentCount > 0) {
        return 'agent';
      }
      if (hasHumanTaskWork) {
        return 'human-tasks';
      }
      if (hasWaitingApprovals) {
        return 'approval';
      }
      return currentTab;
    });
  }, [
    displayDAGRunIdentity,
    waitingAgentCount,
    hasHumanTaskWork,
    hasWaitingApprovals,
  ]);

  const scrollPaneClassName = fillHeight
    ? 'min-h-0 flex-1 overflow-auto pr-1'
    : '';

  return (
    // Everything below can drill into a child run through the same stack.
    <SubRunOpenProvider value={openSubRun}>
      <div
        className={cn(
          'w-full min-w-0 max-w-full overflow-hidden space-y-4',
          fillHeight && 'flex h-full min-h-0 flex-col gap-4 space-y-0'
        )}
      >
        {/* Status Detail Tabs */}
        <div
          className={cn(
            'w-full min-w-0 max-w-full overflow-hidden',
            fillHeight && 'shrink-0'
          )}
        >
          <div className="flex w-full min-w-0 flex-wrap items-center gap-2">
            <div className="min-w-0 flex-1 overflow-x-auto">
              <Tabs className="min-w-max whitespace-nowrap">
                <I18nProps>
                  <Tab
                    aria-label="Status"
                    isActive={activeTab === 'status'}
                    onClick={() => setActiveTab('status')}
                    className="flex cursor-pointer items-center gap-2 px-3 sm:px-4"
                  >
                    <ActivitySquare className="h-4 w-4" />
                    <span>
                      <I18nText text={'Status'} />
                    </span>
                  </Tab>
                </I18nProps>
                {hasAgentSessions && (
                  <I18nProps>
                    <Tab
                      aria-label="Agent"
                      isActive={activeTab === 'agent'}
                      onClick={() => setActiveTab('agent')}
                      className="flex cursor-pointer items-center gap-2 px-3 sm:px-4"
                    >
                      <Bot className="h-4 w-4" />
                      <span>
                        <I18nText text={'Agent'} />
                      </span>
                      {waitingAgentCount > 0 && (
                        <span className="rounded-full bg-warning/15 px-1.5 py-0.5 text-xs font-medium text-warning">
                          {waitingAgentCount}
                        </span>
                      )}
                    </Tab>
                  </I18nProps>
                )}
                {hasHumanTaskWork && (
                  <I18nProps>
                    <Tab
                      aria-label="Human tasks"
                      isActive={activeTab === 'human-tasks'}
                      onClick={() => setActiveTab('human-tasks')}
                      className="flex cursor-pointer items-center gap-2 px-3 sm:px-4"
                    >
                      <ClipboardCheck className="h-4 w-4" />
                      <span>
                        <I18nText text={'Human tasks'} />
                      </span>
                      {waitingHumanTaskCount > 0 && (
                        <span className="rounded-full bg-warning/15 px-1.5 py-0.5 text-xs font-medium text-warning">
                          {waitingHumanTaskCount}
                        </span>
                      )}
                    </Tab>
                  </I18nProps>
                )}
                {hasWaitingApprovals && (
                  <I18nProps>
                    <Tab
                      aria-label="Approval"
                      isActive={activeTab === 'approval'}
                      onClick={() => setActiveTab('approval')}
                      className="flex cursor-pointer items-center gap-2 px-3 sm:px-4"
                    >
                      <ShieldCheck className="h-4 w-4" />
                      <span>
                        <I18nText text={'Approval'} />
                      </span>
                      <span className="rounded-full bg-warning/15 px-1.5 py-0.5 text-xs font-medium text-warning">
                        {waitingApprovalCount}
                      </span>
                    </Tab>
                  </I18nProps>
                )}
                {showTimeline && (
                  <I18nProps>
                    <Tab
                      aria-label="Timeline"
                      isActive={activeTab === 'timeline'}
                      onClick={() => setActiveTab('timeline')}
                      className="flex cursor-pointer items-center gap-2 px-3 sm:px-4"
                    >
                      <GanttChart className="h-4 w-4" />
                      <span>
                        <I18nText text={'Timeline'} />
                      </span>
                    </Tab>
                  </I18nProps>
                )}
                <I18nProps>
                  <Tab
                    aria-label="Outputs"
                    isActive={activeTab === 'outputs'}
                    onClick={() => setActiveTab('outputs')}
                    className="flex cursor-pointer items-center gap-2 px-3 sm:px-4"
                  >
                    <Package className="h-4 w-4" />
                    <span>
                      <I18nText text={'Outputs'} />
                    </span>
                  </Tab>
                </I18nProps>
                {hasArtifacts && (
                  <I18nProps>
                    <Tab
                      aria-label="Artifacts"
                      isActive={activeTab === 'artifacts'}
                      onClick={() => setActiveTab('artifacts')}
                      className="flex cursor-pointer items-center gap-2 px-3 sm:px-4"
                    >
                      <Archive className="h-4 w-4" />
                      <span>
                        <I18nText text={'Artifacts'} />
                      </span>
                    </Tab>
                  </I18nProps>
                )}
                {hasAgentTasks && (
                  <I18nProps>
                    <Tab
                      aria-label="Tasks"
                      isActive={activeTab === 'tasks'}
                      onClick={() => setActiveTab('tasks')}
                      className="flex cursor-pointer items-center gap-2 px-3 sm:px-4"
                    >
                      <ListChecks className="h-4 w-4" />
                      <span>
                        <I18nText text={'Tasks'} />
                      </span>
                    </Tab>
                  </I18nProps>
                )}
                {hasChatSteps && (
                  <I18nProps>
                    <Tab
                      aria-label="Chat"
                      isActive={activeTab === 'chat'}
                      onClick={() => setActiveTab('chat')}
                      className="flex cursor-pointer items-center gap-2 px-3 sm:px-4"
                    >
                      <MessageSquare className="h-4 w-4" />
                      <span>
                        <I18nText text={'Chat'} />
                      </span>
                    </Tab>
                  </I18nProps>
                )}
                <I18nProps>
                  <Tab
                    aria-label="Spec"
                    isActive={activeTab === 'spec'}
                    onClick={() => setActiveTab('spec')}
                    className="flex cursor-pointer items-center gap-2 px-3 sm:px-4"
                  >
                    <FileCode className="h-4 w-4" />
                    <span>
                      <I18nText text={'Spec'} />
                    </span>
                  </Tab>
                </I18nProps>
              </Tabs>
            </div>
          </div>
        </div>

        {/* Status Tab Content */}
        {childRunStack.length > 0 && (
          <SubRunStackModal
            rootName={displayDAGRun.rootDAGRunName || displayDAGRun.name}
            rootDAGRunId={displayDAGRun.rootDAGRunId || displayDAGRun.dagRunId}
            rootLabel={displayDAGRun.name}
            stack={childRunStack}
            onChange={setChildRunStack}
          />
        )}

        {activeTab === 'status' && (
          <div className={cn('space-y-6', scrollPaneClassName)}>
            {failedNode && (
              <div
                role="alert"
                className="rounded-lg border border-destructive/40 bg-destructive/5 p-4"
              >
                <div className="flex items-start gap-3">
                  <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-destructive" />
                  <div className="min-w-0 flex-1 space-y-2">
                    <div>
                      <div className="font-medium text-destructive">
                        {ts(
                          failedNode.status === NodeStatus.Rejected
                            ? 'Rejected at {step}'
                            : 'Failed at {step}',
                          { step: failedNode.step.name }
                        )}
                      </div>
                      <div className="mt-1 max-h-28 overflow-auto whitespace-pre-wrap break-words text-sm text-foreground">
                        {failedNode.error || failedNode.rejectionReason || (
                          <I18nText
                            text={'The step failed without an error message.'}
                          />
                        )}
                      </div>
                    </div>
                    <div className="flex flex-wrap gap-2">
                      <button
                        type="button"
                        className="inline-flex items-center gap-1.5 rounded-md border border-border bg-background px-2.5 py-1.5 text-xs font-medium hover:bg-muted"
                        onClick={() =>
                          handleViewLog(
                            `${failedNode.step.name}_stderr`,
                            displayDAGRun.dagRunId,
                            failedNode
                          )
                        }
                      >
                        <ScrollText className="h-3.5 w-3.5" />
                        <I18nText text={'View stderr'} />
                      </button>
                      <button
                        type="button"
                        className="rounded-md border border-border bg-background px-2.5 py-1.5 text-xs font-medium hover:bg-muted"
                        onClick={() =>
                          onInspectStepOnGraph(
                            toMermaidNodeId(failedNode.step.name)
                          )
                        }
                      >
                        <I18nText text={'Inspect step'} />
                      </button>
                    </div>
                  </div>
                </div>
              </div>
            )}

            {/* Agent runs show execution order instead of a graph */}
            {isAgentRun && agentEvents.length > 0 && (
              <AgentTimeline
                events={agentEvents}
                onOpenChildRun={openAgentChildRun}
              />
            )}

            {/* DAG Graph Visualization */}
            {!isAgentRun &&
              displayDAGRun.nodes &&
              displayDAGRun.nodes.length > 0 && (
                <div className="flex flex-col">
                  <BorderedBox className="pt-4 px-4 pb-0 flex flex-col items-stretch overflow-hidden">
                    <div className="flex justify-end mb-2">
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <div
                            className="flex h-7 w-7 items-center justify-center rounded bg-muted text-muted-foreground cursor-help"
                            aria-label={ts('Graph interactions')}
                          >
                            <MousePointerClick className="h-3.5 w-3.5" />
                          </div>
                        </TooltipTrigger>
                        <TooltipContent>
                          <div className="space-y-1">
                            <p>
                              <I18nText text={'Click: Inspect step details'} />
                            </p>
                            <p>
                              <I18nText
                                text={'Double-click: Navigate to sub dagRun'}
                              />
                            </p>
                            {config.permissions.runDags && (
                              <p>
                                <I18nText
                                  text={'Right-click: Update node status'}
                                />
                              </p>
                            )}
                          </div>
                        </TooltipContent>
                      </Tooltip>
                    </div>
                    <div className="w-full min-w-0 max-w-full overflow-x-auto">
                      <Graph
                        steps={displayDAGRun.nodes}
                        name={displayDAGRun.name}
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
                fileName={fileName}
                onViewLog={handleViewLog}
                onNodeStatusUpdated={applyDisplayNodeStatus}
              />
            </div>

            {/* Lifecycle Hooks */}
            {handlers?.length ? (
              <NodeStatusTable
                nodes={handlers}
                status={displayDAGRun}
                fileName={fileName}
                onViewLog={handleViewLog}
                onNodeStatusUpdated={applyDisplayNodeStatus}
                hideActions
              />
            ) : null}
          </div>
        )}

        {/* Agent Tab Content */}
        {activeTab === 'agent' && hasAgentSessions && (
          <div className={scrollPaneClassName}>
            <AgentSessionTab
              key={displayDAGRunIdentity}
              dagRun={displayDAGRun}
              onChanged={dagContext.refresh}
              selectedStep={selectedAgentStep}
              onSelectedStepChange={setSelectedAgentStep}
            />
          </div>
        )}

        {/* Human Tasks Tab Content */}
        {activeTab === 'human-tasks' && hasHumanTaskWork && (
          <div className={scrollPaneClassName}>
            <HumanTasksTab
              key={displayDAGRunIdentity}
              dagRun={displayDAGRun}
              onChanged={dagContext.refresh}
            />
          </div>
        )}

        {/* Approval Tab Content */}
        {activeTab === 'approval' && hasWaitingApprovals && (
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

        {/* Tasks Tab Content */}
        {activeTab === 'tasks' && hasAgentTasks && (
          <div className={scrollPaneClassName}>
            <TaskChecklistTab tasks={agentTasks} />
          </div>
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
          step={selectedDetailNode?.step}
          node={selectedDetailNode}
          onClose={closeStepDetails}
          onViewLog={(node, stream) =>
            handleViewLog(
              stream === 'stderr' ? `${node.step.name}_stderr` : node.step.name,
              displayDAGRun.dagRunId,
              node
            )
          }
          onOpenSubRun={(node, subRunIndex) => openSubRunAt(node, subRunIndex)}
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
              if (openInNewTab) {
                navigateToSubDagRun(
                  parallelExecutionModal.node!,
                  subRunIndex,
                  true
                );
                return;
              }
              openSubRunAt(parallelExecutionModal.node!, subRunIndex);
              setParallelExecutionModal({ isOpen: false });
            }}
          />
        )}
      </div>
    </SubRunOpenProvider>
  );
}

export default DAGStatus;
