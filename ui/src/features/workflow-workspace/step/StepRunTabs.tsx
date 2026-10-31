// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo, useState, type ReactNode } from 'react';
import { NodeStatus, Status } from '@/api/v1/schema';
import { SubRunOpenProvider } from '@/features/dags/components/common/SubRunStackModal';
import { buildRunOverlay } from '@/features/visual-editor/run/runOverlay';
import { isEphemeralRun } from '@/features/visual-editor/run/inputProvenance';
import { useFollowedRun } from '@/features/visual-editor/run/useFollowedRun';
import { isRunActive } from '@/features/visual-editor/run/stepRunInfo';
import { useI18n } from '@/i18n/I18nProvider';
import type {
  ApiDAGRunDetails,
  ApiNode,
  WorkspaceRunValue,
} from '../contracts';
import {
  WorkspaceDataContext,
  useWorkspaceData,
  type WorkspaceDataValue,
} from '../data/context';
import { runHandlers } from '../data/handlerMapping';
import { firstFailedStep, nodesByStep, waitingCounts } from '../data/runFacts';
import { dockFacts, dockTabBadges, fallbackDockTab } from '../dock/dockTabs';
import { RunTabsPane } from '../dock/RunTabsPane';
import { RunTabEmpty } from '../dock/tabs/runTabBodies';
import { dockTabBodies } from '../dock/tabs';
import {
  scopeDetailsToStep,
  STEP_TAB_LABELS,
  stepDockTabs,
} from '../dock/tabs/stepScope';
import type { DockTab } from '../state/workspaceUrl';
import { StepFactsLine } from './StepFactsLine';
import { isForeachStep } from './StepForeach';
import { isAgentAwaitingAnswer } from './StepManualActions';

/** Prefix of the ids of the pane in the step view; the dock keeps its own. */
const ID_PREFIX = 'ww-step-output';

/** The tabs of a step that no run holds: the log has nothing, the output is declared. */
const NO_RUN_TABS: DockTab[] = ['log', 'outputs'];

/**
 * The tabs of the right pane. With a run, the tabs of the Run panel for the
 * run as the step sees it (stepDockTabs), so that the strip is the one of
 * Executions and every tab is about this step.
 */
export function stepRunTabs(
  details: ApiDAGRunDetails | null,
  step: string
): DockTab[] {
  return details ? stepDockTabs(details, step) : NO_RUN_TABS;
}

/** The Run panel value of a run that a step view follows. */
function followedRunValue(
  name: string,
  runId: string,
  details: ApiDAGRunDetails | null,
  active: boolean
): WorkspaceRunValue {
  const noop = () => undefined;
  return {
    selection: { runId },
    ref: { name, runId },
    details,
    hasRun: true,
    isLatest: false,
    isActive: details !== null ? isRunActive(details) : active,
    notFound: false,
    latestRef: null,
    overlay: buildRunOverlay(details ?? undefined),
    nodeByStep: nodesByStep(details),
    handlers: runHandlers(details),
    waiting: waitingCounts(details),
    failedStep: firstFailedStep(details),
    select: noop,
    applyNodeStatus: noop,
    // The followed run polls by itself.
    refresh: async () => undefined,
  };
}

export interface StepRunTabsProps {
  /** Server name of the step. */
  step: string;
  /** The run that the step view follows, or null before the step ran. */
  runId: string | null;
  /** Shown in the Output tab while no run exists: the declared outputs. */
  declared: ReactNode;
}

/**
 * The tab that opens first: a step that waits opens its Actions (or its
 * Agent tab when an agent waits), a failed step or run and a foreach step
 * (its log lists the items) open the log, the rest opens the outputs.
 */
export function defaultStepTab(
  details: ApiDAGRunDetails | null,
  node: ApiNode | undefined,
  tabs: readonly DockTab[]
): DockTab {
  if (node?.status === NodeStatus.Waiting) {
    if (tabs.includes('actions')) return 'actions';
    if (isAgentAwaitingAnswer(node) && tabs.includes('agent')) return 'agent';
  }
  if (
    node?.status === NodeStatus.Failed ||
    details?.status === Status.Failed ||
    (node && isForeachStep(node))
  ) {
    return 'log';
  }
  return 'outputs';
}

/**
 * The OUTPUT column of the step view in the editor: the same tab strip and
 * the same tab bodies as the right side of the Run panel in Executions
 * (RunTabsPane, dockTabBodies), with the facts line of the step above them.
 * The run that the workspace selected comes from the workspace data, so it
 * has one read and one stream. Another run id, such as the one that 'Execute
 * step' started, has its own read. With no run, the Output tab shows the
 * declared outputs and the Log tab asks to execute the step.
 */
export function StepRunTabs(props: StepRunTabsProps) {
  const data = useWorkspaceData('StepRunTabs');
  if (props.runId !== null && props.runId === data.run.ref?.runId) {
    return <StepRunTabsView {...props} run={data.run} context={data} />;
  }
  return <FollowedStepRunTabs {...props} />;
}

/** A run that the workspace did not select: this view reads it. */
function FollowedStepRunTabs(props: StepRunTabsProps) {
  const data = useWorkspaceData('StepRunTabs');
  const dagName = data.dagData.dag?.name ?? data.run.ref?.name ?? '';
  const followed = useFollowedRun(dagName, props.runId);
  const details = props.runId ? followed.details : null;
  const run = useMemo(
    () =>
      props.runId
        ? followedRunValue(dagName, props.runId, details, followed.active)
        : null,
    [dagName, props.runId, details, followed.active]
  );
  const context = useMemo(() => (run ? { ...data, run } : null), [data, run]);
  return (
    <StepRunTabsView
      {...props}
      run={run}
      context={context}
      // The sub-run stack of the workspace belongs to the selected run, not
      // to this one: a child run opens in a new tab.
      ownSubRuns
    />
  );
}

interface StepRunTabsViewProps extends StepRunTabsProps {
  /** The run to show, or null while the step has none. */
  run: WorkspaceRunValue | null;
  /** The workspace data with `run` in it. */
  context: WorkspaceDataValue | null;
  ownSubRuns?: boolean;
}

function StepRunTabsView({
  step,
  runId,
  declared,
  run,
  context,
  ownSubRuns = false,
}: StepRunTabsViewProps) {
  const { ts } = useI18n();
  const data = useWorkspaceData('StepRunTabs');
  const dag = data.dagData.dag;
  const details = run?.details ?? null;
  const node = run?.nodeByStep.get(step);

  // The badges count what this step waits for, not the run.
  const facts = useMemo(
    () =>
      details
        ? dockFacts(scopeDetailsToStep(details, step), {
            artifactEnabled: false,
          })
        : null,
    [details, step]
  );
  const tabs = useMemo(() => stepRunTabs(details, step), [details, step]);
  const badges = useMemo(() => dockTabBadges(facts), [facts]);
  const [chosen, setChosen] = useState<DockTab | null>(null);
  const preferred: DockTab = chosen ?? defaultStepTab(details, node, tabs);
  const shown = fallbackDockTab(preferred, tabs);
  const Body = dockTabBodies[shown];

  let body: ReactNode = null;
  if (!runId || !run || !context || !dag) {
    body =
      shown === 'outputs' ? (
        <div className="h-full overflow-auto">{declared}</div>
      ) : (
        <RunTabEmpty>{ts('Execute the step to see the log.')}</RunTabEmpty>
      );
  } else if (Body) {
    const tab = (
      <WorkspaceDataContext.Provider value={context}>
        <Body
          key={shown}
          run={run}
          fileName={data.fileName}
          dag={dag}
          step={step}
        />
      </WorkspaceDataContext.Provider>
    );
    body = ownSubRuns ? (
      <SubRunOpenProvider value={null}>{tab}</SubRunOpenProvider>
    ) : (
      tab
    );
  }

  return (
    <div
      data-testid="ww-step-output"
      data-run-id={runId ?? undefined}
      className="flex h-full min-h-64 flex-col"
    >
      <StepFactsLine node={node} testRun={isEphemeralRun(details)} />
      <RunTabsPane
        tabs={tabs}
        active={shown}
        badges={badges}
        onSelect={setChosen}
        idPrefix={ID_PREFIX}
        label="Run"
        labels={STEP_TAB_LABELS}
      >
        {body}
      </RunTabsPane>
    </div>
  );
}
