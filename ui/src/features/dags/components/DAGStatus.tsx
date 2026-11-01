// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useErrorModal } from '@/components/ui/error-modal';
import { Tab, Tabs } from '@/components/ui/tabs';
import {
  ActivitySquare,
  AlertTriangle,
  Archive,
  ArrowRight,
  Bot,
  ClipboardCheck,
  FileCode,
  ListChecks,
  GanttChart,
  MessageSquare,
  Package,
  ShieldCheck,
  ScrollText,
} from 'lucide-react';
import React, { useEffect, useState } from 'react';
import { useCookies } from 'react-cookie';
import { Link, useNavigate } from 'react-router-dom';
import { components, NodeStatus, Status, Stream } from '../../../api/v1/schema';
import { useConfig } from '../../../contexts/ConfigContext';
import { useRemoteNode } from '../../../contexts/RemoteNodeContext';
import { useClient } from '../../../hooks/api';
import { cn, toMermaidNodeId } from '../../../lib/utils';
import { useScrollFade } from '@/hooks/useScrollFade';
import BorderedBox from '@/components/ui/bordered-box';
import { DAGRunOutputs } from '../../dag-runs/components/dag-run-details';
import { buildDAGRunPageURL } from '../../dag-runs/lib/dagRunUrls';
import { DAGContext } from '../contexts/DAGContext';
import { getEventHandlers } from '../lib/getEventHandlers';
import { updateDAGRunNodeStatus } from '../lib/nodeStatus';
import {
  autoRunTab,
  fallbackRunTab,
  runViewFacts,
  visibleRunTabs,
  type RunViewTab,
} from '../lib/runViewTabs';
import { canChangeStepStatus } from '../lib/stepStatusGate';
import { ApprovalTab } from './approval';
import { ApprovalResumeAlert } from './approval/ApprovalResumeAlert';
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
import DAGSpecReadOnly from './dag-editor/DAGSpecReadOnly';
import { StepDetailsDrawer } from './step-details';
import type { ForeachLogTarget } from '../hooks/useStepLogQuery';
import {
  LogViewer,
  ParallelExecutionModal,
  StatusUpdateModal,
} from './dag-execution';
import { FlowchartType, Graph, TimelineChart } from './visualization';
import { GraphInteractionsHint } from './visualization/GraphInteractionsHint';
import {
  GraphResizeHandle,
  useResizableGraphHeight,
} from './visualization/GraphResizeHandle';
import { HumanTasksTab } from './human-task';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

type Props = {
  dagRun: components['schemas']['DAGRunDetails'];
  fileName: string;
  artifactEnabled?: boolean;
  initialTab?: StatusTab;
  activeTab?: StatusTab;
  onTabChange?: (tab: StatusTab) => void;
  fillHeight?: boolean;
  /**
   * Shows only the run's status and any action waiting on a person, with a
   * link to the run page for the other run views.
   */
  summary?: boolean;
  /** Called when the user picks a tab; automatic switches do not call it. */
  onTabSelect?: (tab: StatusTab) => void;
  /** Step whose details open on load, such as one named in the URL. */
  initialStepName?: string;
  /** Called with the step whose details open, or undefined when they close. */
  onStepSelect?: (stepName: string | undefined) => void;
  /** Repeats run metadata above the steps; off where a header shows it. */
  showRunMeta?: boolean;
};

export type StatusTab = RunViewTab;

/** Check if the current DAG run is a sub DAG-run (has a different root) */
function isSubDAGRun(dagRun: components['schemas']['DAGRunDetails']): boolean {
  return !!(
    dagRun.rootDAGRunId &&
    dagRun.rootDAGRunName &&
    dagRun.rootDAGRunId !== dagRun.dagRunId
  );
}

/**
 * Points an agent that waits for an answer to the run page, since its
 * conversation needs the full view. Otherwise nothing shows: the run ID of the
 * header links to the run page, where the timeline, outputs, artifacts and run
 * spec live.
 */
function RunSummaryBar({
  dagRun,
  remoteNode,
  waitingAgentCount,
}: {
  dagRun: components['schemas']['DAGRunDetails'];
  remoteNode: string;
  waitingAgentCount: number;
}) {
  if (waitingAgentCount === 0) {
    return null;
  }
  const subRun = isSubDAGRun(dagRun);
  const runURL = buildDAGRunPageURL({
    rootDAGRunName: subRun ? dagRun.rootDAGRunName : dagRun.name,
    rootDAGRunId: subRun ? dagRun.rootDAGRunId : dagRun.dagRunId,
    subDAGRunId: subRun ? dagRun.dagRunId : undefined,
    remoteNode,
  });
  return (
    <div className="flex flex-wrap items-center justify-between gap-2">
      <p className="text-sm text-muted-foreground">
        <I18nText text={'An agent is waiting for an answer.'} />
      </p>
      <Link
        to={runURL}
        className="inline-flex items-center gap-1 text-sm font-medium text-primary hover:underline"
      >
        <I18nText text={'Open run'} />
        <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
      </Link>
    </div>
  );
}

function DAGStatus({
  dagRun,
  fileName,
  artifactEnabled = false,
  initialTab = 'status',
  activeTab: controlledActiveTab,
  onTabChange,
  fillHeight = false,
  summary = false,
  onTabSelect,
  initialStepName,
  onStepSelect,
  showRunMeta = true,
}: Props) {
  const { ts } = useI18n();
  const dagContext = React.useContext(DAGContext);
  const config = useConfig();
  const remoteNode = useRemoteNode();
  const navigate = useNavigate();
  const { showError } = useErrorModal();
  const [modal, setModal] = useState(false);
  const [internalActiveTab, setInternalActiveTab] = useState<StatusTab>(initialTab);
  const activeTab = controlledActiveTab ?? internalActiveTab;
  const isTabControlled = controlledActiveTab !== undefined;
  // Follows a tab change from the URL, such as navigating to another run.
  useEffect(() => {
    if (!isTabControlled) {
      setInternalActiveTab(initialTab);
    }
  }, [initialTab, isTabControlled]);
  const [selectedAgentStep, setSelectedAgentStep] = useState('');
  const [displayDAGRun, setDisplayDAGRun] = useState(dagRun);
  const [tabRowRef, tabRowFade] = useScrollFade<HTMLDivElement>('x');

  function setActiveTab(value: React.SetStateAction<StatusTab>): void {
    if (!isTabControlled) {
      setInternalActiveTab(value);
      return;
    }
    const nextTab = typeof value === 'function' ? value(activeTab) : value;
    if (nextTab !== activeTab) {
      onTabChange?.(nextTab);
    }
  }

  function selectTab(tab: StatusTab): void {
    setActiveTab(tab);
    onTabSelect?.(tab);
  }

  useEffect(() => {
    setDisplayDAGRun(dagRun);
  }, [dagRun]);

  // Flowchart direction preference stored in cookies
  const [cookie, setCookie] = useCookies(['flowchart']);
  const [flowchart, setFlowchart] = useState<FlowchartType>(
    cookie['flowchart']
  );

  const {
    height: graphHeight,
    graphBoxRef,
    handleProps: graphResizeHandleProps,
  } = useResizableGraphHeight();

  const [selectedStep, setSelectedStep] = useState<
    components['schemas']['Step'] | undefined
  >(undefined);
  // Stored by name and re-derived each render so the open drawer tracks live
  // node updates arriving over SSE.
  const [selectedDetailStepName, setSelectedDetailStepName] = useState<
    string | undefined
  >(undefined);
  const [isStepDetailsOpen, setIsStepDetailsOpen] = useState(false);

  // Held in a ref so a parent's inline callback does not churn the
  // callbacks below.
  const onStepSelectRef = React.useRef(onStepSelect);
  onStepSelectRef.current = onStepSelect;

  const closeStepDetails = React.useCallback(() => {
    setIsStepDetailsOpen(false);
    onStepSelectRef.current?.(undefined);
  }, []);

  const openStepDetails = React.useCallback((stepName: string) => {
    setSelectedDetailStepName(stepName);
    setIsStepDetailsOpen(true);
    onStepSelectRef.current?.(stepName);
  }, []);

  // Each step named by the URL opens once the run lists it, and the drawer
  // closes when the URL stops naming one. The run is part of the key because
  // the page stays mounted when the URL moves to another run.
  const appliedStep = React.useRef<string | undefined>(undefined);
  const stepLinked = React.useRef(initialStepName);
  stepLinked.current = initialStepName;
  const linkedStepKey = initialStepName
    ? JSON.stringify([dagRun.name, dagRun.dagRunId, initialStepName])
    : undefined;
  useEffect(() => {
    if (linkedStepKey === appliedStep.current) {
      return;
    }
    if (!initialStepName) {
      appliedStep.current = undefined;
      setIsStepDetailsOpen(false);
      return;
    }
    if (!dagRun.nodes?.some((node) => node.step.name === initialStepName)) {
      return;
    }
    appliedStep.current = linkedStepKey;
    setSelectedDetailStepName(initialStepName);
    setIsStepDetailsOpen(true);
  }, [dagRun.nodes, initialStepName, linkedStepKey]);

  useEffect(() => {
    if (activeTab !== 'status') {
      setIsStepDetailsOpen(false);
    }
  }, [activeTab]);

  // State for log viewer
  const [logViewer, setLogViewer] = useState<{
    isOpen: boolean;
    logType: 'execution' | 'step';
    stepName: string;
    dagRunId: string;
    stream: Stream;
    node?: components['schemas']['Node'];
    foreach?: ForeachLogTarget;
    bodyStepStatus?: NodeStatus;
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
    [displayDAGRun, navigate, remoteNode]
  );

  const onInspectStepOnGraph = React.useCallback(
    (id: string) => {
      const n = displayDAGRun.nodes?.find(
        (node) => toMermaidNodeId(node.step.name) == id
      );
      if (!n) {
        return;
      }
      openStepDetails(n.step.name);
    },
    [displayDAGRun, openStepDetails]
  );

  const openStepName = isStepDetailsOpen ? selectedDetailStepName : undefined;

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
        const url = buildDAGRunPageURL({
          rootDAGRunName: displayDAGRun.rootDAGRunName || displayDAGRun.name,
          rootDAGRunId: displayDAGRun.rootDAGRunId || displayDAGRun.dagRunId,
          subDAGRunId: subDAGRun.dagRunId,
          remoteNode,
        });

        if (openInNewTab) {
          window.open(url, '_blank');
        } else {
          navigate(url);
        }
      }
    },
    [displayDAGRun, navigate, remoteNode]
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
      const node = displayDAGRun.nodes?.find(
        (candidate) => toMermaidNodeId(candidate.step.name) === id
      );
      if (
        !node ||
        !canChangeStepStatus(displayDAGRun, node, {
          runDags: config.permissions.runDags,
        }).ok
      ) {
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

  const runFacts = runViewFacts(displayDAGRun, { artifactEnabled });
  const visibleTabs = visibleRunTabs(runFacts);
  const isTabVisible = (tab: StatusTab) => visibleTabs.includes(tab);
  const showTimeline = isTabVisible('timeline');
  const hasChatSteps = isTabVisible('chat');
  const hasAgentSessions = isTabVisible('agent');
  const waitingAgentCount = runFacts.agentWaiting;

  // Agent DAG-runs track goal progress alongside their steps.
  const agentTasks = displayDAGRun.agentTasks ?? [];
  const hasAgentTasks = isTabVisible('tasks');
  const failedNode = displayDAGRun.nodes?.find(
    (node) =>
      node.status === NodeStatus.Failed || node.status === NodeStatus.Rejected
  );

  // An agent has no dependency edges, so its graph carries no information.
  // The decision timeline takes that slot instead.
  const agentEvents = displayDAGRun.agentEvents ?? [];
  const isAgentRun = hasAgentTasks || agentEvents.length > 0;

  const waitingApprovalCount = runFacts.waitingApprovals;
  const waitingHumanTaskCount = runFacts.humanTasksWaiting;
  const hasHumanTaskWork = isTabVisible('human-tasks');
  const hasWaitingApprovals = isTabVisible('approval');
  const hasArtifacts = isTabVisible('artifacts');
  const displayDAGRunIdentity = JSON.stringify([
    remoteNode,
    displayDAGRun.name,
    displayDAGRun.dagRunId,
  ]);

  useEffect(() => {
    if (!isTabControlled) {
      setInternalActiveTab(initialTab);
    }
  }, [displayDAGRunIdentity, initialTab, isTabControlled]);

  useEffect(() => {
    setSelectedAgentStep('');
  }, [displayDAGRunIdentity]);

  // Reset to status tab if selected tab is not available
  const visibleTabsKey = visibleTabs.join(',');
  useEffect(() => {
    const nextTab = fallbackRunTab(activeTab, visibleTabs);
    if (nextTab !== activeTab) {
      setActiveTab(nextTab);
    }
  }, [visibleTabsKey, activeTab]);

  // Surface a newly available manual action from the default status tab.
  // A summary shows the action inline instead, and a step named by the URL
  // stays in view.
  useEffect(() => {
    if (summary || stepLinked.current) {
      return;
    }
    const autoTab = autoRunTab(runFacts);
    setActiveTab((currentTab) =>
      currentTab === 'status' && autoTab ? autoTab : currentTab
    );
  }, [
    displayDAGRunIdentity,
    waitingAgentCount,
    hasHumanTaskWork,
    hasWaitingApprovals,
    summary,
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
        {!summary && (
        <div
          className={cn(
            'w-full min-w-0 max-w-full overflow-hidden',
            fillHeight && 'shrink-0'
          )}
        >
          <div className="flex w-full min-w-0 flex-wrap items-center gap-2">
            <div
              ref={tabRowRef}
              style={tabRowFade}
              className="min-w-0 flex-1 overflow-x-auto"
            >
              <Tabs className="min-w-max whitespace-nowrap">
                <I18nProps>
                  <Tab
                    aria-label="Status"
                    isActive={activeTab === 'status'}
                    onClick={() => selectTab('status')}
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
                      onClick={() => selectTab('agent')}
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
                      onClick={() => selectTab('human-tasks')}
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
                      onClick={() => selectTab('approval')}
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
                      onClick={() => selectTab('timeline')}
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
                    onClick={() => selectTab('outputs')}
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
                      onClick={() => selectTab('artifacts')}
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
                      onClick={() => selectTab('tasks')}
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
                      onClick={() => selectTab('chat')}
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
                    onClick={() => selectTab('spec')}
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
        )}

        {displayDAGRun.status === Status.Waiting &&
          displayDAGRun.approvalResumePending && (
            <ApprovalResumeAlert
              key={displayDAGRunIdentity}
              dagRun={displayDAGRun}
            />
          )}

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

        {summary && (
          <RunSummaryBar
            dagRun={displayDAGRun}
            remoteNode={remoteNode}
            waitingAgentCount={waitingAgentCount}
          />
        )}
        {summary && (hasWaitingApprovals || hasHumanTaskWork) && (
          <section
            aria-labelledby="action-required-title"
            className="space-y-3 rounded-lg border border-warning/40 bg-warning/5 p-4"
          >
            <h2
              id="action-required-title"
              className="text-sm font-semibold text-foreground"
            >
              <I18nText text={'Action required'} />
            </h2>
            {hasHumanTaskWork && (
              <HumanTasksTab
                key={displayDAGRunIdentity}
                dagRun={displayDAGRun}
                onChanged={dagContext.refresh}
              />
            )}
            {hasWaitingApprovals && (
              <ApprovalTab
                dagRun={displayDAGRun}
                dagName={displayDAGRun.name}
              />
            )}
          </section>
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
                  <BorderedBox className="flex flex-col items-stretch gap-0 overflow-hidden p-0">
                    <div ref={graphBoxRef} className="w-full min-w-0">
                      <Graph
                        toolbarStart={
                          <GraphInteractionsHint
                            inspect
                            openSubRun
                            updateStatus={config.permissions.runDags}
                          />
                        }
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
                        selectedStepName={openStepName}
                      />
                    </div>
                    <GraphResizeHandle {...graphResizeHandleProps} />
                  </BorderedBox>
                </div>
              )}

            <div className="grid min-w-0 grid-cols-1 gap-6">
              {/* Status Overview */}
              <div className="bg-surface border border-border rounded-lg p-4">
                <DAGStatusOverview
                  status={displayDAGRun}
                  showRunMeta={showRunMeta}
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
                selectedStepName={openStepName}
                onInspectStep={openStepDetails}
              />
            </div>

            {/* Lifecycle Hooks */}
            {handlers?.length ? (
              <section className="space-y-2">
                <h2 className="text-sm font-semibold text-foreground">
                  <I18nText text={'Lifecycle handlers'} />
                </h2>
                <NodeStatusTable
                  nodes={handlers}
                  status={displayDAGRun}
                  fileName={fileName}
                  onViewLog={handleViewLog}
                  onNodeStatusUpdated={applyDisplayNodeStatus}
                  hideActions
                />
              </section>
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
          dagRun={displayDAGRun}
          onClose={closeStepDetails}
          onViewLog={(node, stream) =>
            handleViewLog(
              stream === 'stderr' ? `${node.step.name}_stderr` : node.step.name,
              displayDAGRun.dagRunId,
              node
            )
          }
          onViewBodyStepLog={(bodyStepName, foreach, stream, bodyStepStatus) =>
            setLogViewer({
              isOpen: true,
              logType: 'step',
              stepName: bodyStepName,
              dagRunId: displayDAGRun.dagRunId,
              stream,
              foreach,
              bodyStepStatus,
            })
          }
          onOpenSubRun={(node, subRunIndex) => openSubRunAt(node, subRunIndex)}
        />

        {/* Log viewer modal */}
        <LogViewer
          isOpen={logViewer.isOpen}
          onClose={() => setLogViewer((prev) => ({ ...prev, isOpen: false }))}
          logType={logViewer.logType}
          dagName={displayDAGRun.name}
          fileName={fileName}
          dagRunId={logViewer.dagRunId}
          stepName={logViewer.stepName}
          dagRun={displayDAGRun}
          stream={logViewer.stream}
          node={logViewer.node}
          foreach={logViewer.foreach}
          bodyStepStatus={logViewer.bodyStepStatus}
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
