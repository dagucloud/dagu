// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useCallback,
  useMemo,
  useRef,
  useState,
  type ComponentType,
  type MouseEvent,
  type ReactNode,
  type KeyboardEvent,
} from 'react';
import { NodeStatus, Status, type components } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClock } from '@/hooks/useClock';
import DAGRunOutputs from '@/features/dag-runs/components/dag-run-details/DAGRunOutputs';
import { buildDAGRunPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import { AgentTimeline } from '@/features/dags/components/agent/AgentTimeline';
import { TaskChecklistTab } from '@/features/dags/components/agent/TaskChecklistTab';
import { AgentSessionTab } from '@/features/dags/components/agent-session/AgentSessionTab';
import { ApprovalResumeAlert } from '@/features/dags/components/approval/ApprovalResumeAlert';
import { ApprovalTab } from '@/features/dags/components/approval/ApprovalTab';
import ArtifactsTab from '@/features/dags/components/artifacts/ArtifactsTab';
import {
  ChatHistoryTab,
  hasChatHistory,
} from '@/features/dags/components/chat-history/ChatHistoryTab';
import {
  SubRunOpenProvider,
  useOpenSubRun,
  type SubRunStackEntry,
} from '@/features/dags/components/common/SubRunStackModal';
import ExecutionLog from '@/features/dags/components/dag-execution/ExecutionLog';
import { HumanTasksTab } from '@/features/dags/components/human-task/HumanTasksTab';
import TimelineChart from '@/features/dags/components/visualization/TimelineChart';
import { getManualActionState } from '@/features/dag-runs/lib/manualActionState';
import { isSubDAGRun } from '@/features/dags/hooks/useStepLogQuery';
import { useStepOutputs } from '@/features/visual-editor/api/useStepOutputs';
import { isRunActive } from '@/features/visual-editor/run/stepRunInfo';
import { useCaptureStepOutputs } from '@/features/visual-editor/run/useCaptureStepOutputs';
import { useI18n } from '@/i18n/I18nProvider';
import { Button } from '@/components/ui/button';
import { PanelMessage } from '@/components/ui/panel-message';
import { useDock } from '../DockContext';
import { CachedLogs, cachedSteps } from '../CachedLogs';
import { StepFailureBanner } from '../StepFailure';
import { useDockStep } from '../useDockStep';
import { ContinuousPanel } from '../../continuous/ContinuousPanel';
import { pickContinuousNode } from '../../continuous/continuousNode';
import { useWorkspaceUrl } from '../../state/workspaceUrl';
import { StepForeach, isForeachStep } from '../../step/StepForeach';
import { StepManualActions } from '../../step/StepManualActions';
import { StepDeclared } from '../../step/StepDeclared';
import { StepOutputRows } from '../../step/StepOutputRows';
import { StepRunView, type StepRunTarget } from '../../step/StepRunView';
import { AgentRunSummary } from './AgentRunSummary';
import { logTargetFromUrl } from './logTarget';
import { scopeDetailsToStep } from './stepScope';
import type { DockTabBodyProps, RunTabProps } from './types';

type AgentEvent = components['schemas']['AgentEvent'];

const SCROLL = 'h-full min-h-0 overflow-auto p-3';

/** The centred message of a tab that has nothing to show yet. */
export function RunTabEmpty({ children }: { children: ReactNode }) {
  return (
    <PanelMessage
      tone="muted"
      align="center"
      className="flex h-full min-h-24 items-center justify-center"
    >
      {children}
    </PanelMessage>
  );
}

/**
 * Gives a run tab the details of the selected run. The key is the run ID, so
 * the tab mounts again when another run is selected. A host that shows one
 * step gives `step`: the tab then reads the run as that step sees it
 * (scopeDetailsToStep), so that no tab shows another step.
 */
function runTab(
  Inner: ComponentType<RunTabProps>,
  name: string
): ComponentType<DockTabBodyProps> {
  function RunTab(props: DockTabBodyProps) {
    const { ts } = useI18n();
    const { step } = props;
    const runDetails = props.run.details;
    const details = useMemo(
      () =>
        runDetails && step ? scopeDetailsToStep(runDetails, step) : runDetails,
      [runDetails, step]
    );
    if (!details) {
      return (
        <RunTabEmpty>
          {ts(
            props.run.hasRun
              ? 'Loading run...'
              : 'This workflow has not run yet.'
          )}
        </RunTabEmpty>
      );
    }
    return (
      <Inner
        key={props.run.ref?.runId ?? details.dagRunId}
        {...props}
        details={details}
      />
    );
  }
  RunTab.displayName = name;
  return RunTab;
}

/**
 * Child rows open on the sub-run stack of the workspace. With Ctrl or Cmd
 * they open the run page in a new tab. TimelineChart reads the opener from
 * the context, so this view puts a modifier-aware opener in front of it.
 */
function TimelineBody({ details }: RunTabProps) {
  const openOnStack = useOpenSubRun();
  const remoteNode = useRemoteNode();
  const newTab = useRef(false);
  const note = (event: MouseEvent | KeyboardEvent) => {
    newTab.current = event.ctrlKey || event.metaKey;
  };
  const open = useCallback(
    (entry: SubRunStackEntry) => {
      if (!newTab.current && openOnStack) {
        openOnStack(entry);
        return;
      }
      window.open(
        buildDAGRunPageURL({
          rootDAGRunName: details.rootDAGRunName || details.name,
          rootDAGRunId: details.rootDAGRunId || details.dagRunId,
          subDAGRunId: entry.dagRunId,
          remoteNode,
        }),
        '_blank'
      );
    },
    [details, openOnStack, remoteNode]
  );
  return (
    <div className={SCROLL} onClickCapture={note} onKeyDownCapture={note}>
      <SubRunOpenProvider value={openOnStack ? open : null}>
        <TimelineChart status={details} />
      </SubRunOpenProvider>
    </div>
  );
}

/**
 * True while the run is active and for 15 s after it ended. The shared clock
 * ticks inside that window only, so the value turns false when it closes.
 */
function useEndedRecently(details: RunTabProps['details']): boolean {
  const active = isRunActive(details);
  const finished = Date.parse(details.finishedAt ?? '');
  const recent = (now: number) =>
    active || (Number.isFinite(finished) && now - finished < 15_000);
  return useClock(active || !recent(Date.now()) ? null : 1000, recent);
}

/**
 * A host that shows one step: a step that did not run in this run (not
 * started, skipped, or not in the run) has no output, so the tab shows what
 * its definition declares.
 */
function stepHasNoOutput(run: RunTabProps['run'], step: string): boolean {
  const status = run.nodeByStep.get(step)?.status;
  return (
    status === undefined ||
    status === NodeStatus.NotStarted ||
    status === NodeStatus.Skipped
  );
}

/**
 * The outputs of one step that ran. Only a root run has the route; a sub-run
 * (and a step that no hook can ask for) shows the list of the run.
 */
function StepOutputsBody({
  details,
  step,
  fileName,
}: {
  details: RunTabProps['details'];
  step: string;
  fileName: string;
}) {
  const { ts } = useI18n();
  const readable = !isSubDAGRun(details);
  const { data, error } = useStepOutputs(
    details.name,
    readable ? details.dagRunId : null,
    readable ? step : null
  );
  // The outputs that this tab shows go to the cache of the browser.
  useCaptureStepOutputs({
    fileName: readable ? fileName : undefined,
    dagName: details.name,
    runId: readable ? details.dagRunId : null,
    step,
    data,
  });
  if (!readable) {
    // The API has no route for the outputs of one step of a sub-run, and the
    // list of the run holds the outputs of other steps.
    return (
      <PanelMessage tone="muted" align="start" className="p-0">
        {ts('The outputs of a step of a sub-run are not available here.')}
      </PanelMessage>
    );
  }
  if (error && !data) {
    return (
      <PanelMessage tone="error" align="start" className="p-0">
        {ts('Could not load {thing}', {
          thing: ts('the outputs of this step'),
        })}
      </PanelMessage>
    );
  }
  if (!data) {
    return (
      <PanelMessage tone="muted" align="start" className="p-0">
        {ts('Loading outputs...')}
      </PanelMessage>
    );
  }
  if (data.outputs.length === 0 && !data.outputVariable) {
    return (
      <PanelMessage tone="muted" align="start" className="p-0">
        {ts('No outputs in this run')}
      </PanelMessage>
    );
  }
  return (
    <StepOutputRows
      outputVariable={data.outputVariable}
      outputs={data.outputs}
    />
  );
}

function RunOutputsList({ details }: { details: RunTabProps['details'] }) {
  const pollWhileEmpty = useEndedRecently(details);
  return (
    <>
      {/* The outputs load once: they load again when the status changes,
          as a running run has none yet. */}
      <DAGRunOutputs
        key={details.status}
        dagName={details.name}
        dagRunId={details.dagRunId}
        padded={false}
        bare
        prettyValues
        pollWhileEmpty={pollWhileEmpty}
      />
    </>
  );
}

function OutputsBody({ run, details, step, fileName }: RunTabProps) {
  if (step && stepHasNoOutput(run, step)) {
    return (
      <div className={SCROLL}>
        <StepDeclared step={step} node={run.nodeByStep.get(step)} />
      </div>
    );
  }
  return (
    <div className={SCROLL}>
      {step ? (
        <StepOutputsBody details={details} step={step} fileName={fileName} />
      ) : (
        <RunOutputsList details={details} />
      )}
    </div>
  );
}

function ArtifactsBody({ details, dag, step }: RunTabProps) {
  // The artifacts of a run are not tied to a step: a step view has no such tab.
  if (step) return null;
  return (
    <ArtifactsTab
      dagRun={details}
      artifactEnabled={!!dag.artifacts?.enabled}
      className="h-full min-h-0"
      fillHeight
    />
  );
}

/** Human tasks and approval gates of the run, as one tab. */
function ActionsBody({ run, details, step }: RunTabProps) {
  const { ts } = useI18n();
  const { hasHumanTaskWork, waitingApprovalNodes, waitingHumanTaskNodes } =
    getManualActionState(details);
  if (step) {
    // A host that shows one step: the cards of this step only. An agent that
    // waits is answered in the Agent tab of the same strip, so the link of
    // StepManualActions to the dock is off (inSubRun).
    const waits = [...waitingApprovalNodes, ...waitingHumanTaskNodes].some(
      (n) => n.step.name === step
    );
    const resumePending =
      details.status === Status.Waiting && details.approvalResumePending;
    if (!waits && !resumePending) {
      return <RunTabEmpty>{ts('This step needs no action.')}</RunTabEmpty>;
    }
    return (
      <div className={`${SCROLL} space-y-4`}>
        {resumePending && <ApprovalResumeAlert dagRun={details} />}
        <StepManualActions
          holder={details}
          node={run.nodeByStep.get(step)}
          inSubRun
        />
      </div>
    );
  }
  return (
    <div className={`${SCROLL} space-y-4`}>
      {details.status === Status.Waiting && details.approvalResumePending && (
        <ApprovalResumeAlert dagRun={details} />
      )}
      {hasHumanTaskWork && (
        <HumanTasksTab dagRun={details} onChanged={run.refresh} bare />
      )}
      {waitingApprovalNodes.length > 0 && (
        <ApprovalTab dagRun={details} dagName={details.name} bare />
      )}
    </div>
  );
}

/** The step named by ?step when it has an agent session, else null. */
function sessionStep(
  details: RunTabProps['details'],
  step: string | null
): string | null {
  if (step === null) return null;
  const found = details.nodes?.some(
    (node) => node.step.name === step && !!node.agentSession
  );
  return found ? step : null;
}

/** The transcript of a chat or agent step that started. */
function StepChat({
  details,
  step,
}: {
  details: RunTabProps['details'];
  step: string;
}) {
  const { ts } = useI18n();
  const node = details.nodes?.find((n) => n.step.name === step);
  if (!node || !hasChatHistory(node)) return null;
  if (node.status === NodeStatus.NotStarted) return null;
  return (
    <section className="space-y-1" aria-label={ts('Chat')}>
      <div className="text-xs font-medium text-muted-foreground">
        {ts('Chat')}
      </div>
      <ChatHistoryTab dagRun={details} stepName={step} />
    </section>
  );
}

/**
 * The decision timeline of an agent run above its sessions. A host that
 * shows one step opens its session and adds its transcript.
 */
function AgentBody({ run, details, step }: RunTabProps) {
  const [url] = useWorkspaceUrl();
  const openOnStack = useOpenSubRun();
  const focus = step ?? url.step;
  const linked = sessionStep(details, focus);
  const [selected, setSelected] = useState(linked ?? '');
  const [seenStep, setSeenStep] = useState(focus);
  if (focus !== seenStep) {
    setSeenStep(focus);
    if (linked) setSelected(linked);
  }

  const events = details.agentEvents ?? [];
  const openChild = useCallback(
    (event: AgentEvent) => {
      if (!event.childDagRunId || !openOnStack) return;
      openOnStack({
        name: event.childDagName || event.name || 'child',
        dagRunId: event.childDagRunId,
      });
    },
    [openOnStack]
  );

  return (
    <div className={`${SCROLL} space-y-4`}>
      {step && <StepChat details={details} step={step} />}
      <AgentRunSummary details={details} />
      {events.length > 0 && (
        <AgentTimeline events={events} onOpenChildRun={openChild} />
      )}
      <AgentSessionTab
        dagRun={details}
        onChanged={run.refresh}
        selectedStep={selected}
        onSelectedStepChange={setSelected}
      />
    </div>
  );
}

function ChatBody({ details, step }: RunTabProps) {
  const own = step
    ? details.nodes?.find((n) => n.step.name === step)
    : undefined;
  return (
    <div className={SCROLL}>
      <ChatHistoryTab
        dagRun={details}
        stepName={own && hasChatHistory(own) ? step : undefined}
      />
    </div>
  );
}

function TasksBody({ details, step }: RunTabProps) {
  // The tasks of an agent run are not tied to a step: a step view has no such tab.
  if (step) return null;
  return (
    <div className={SCROLL}>
      <TaskChecklistTab tasks={details.agentTasks ?? []} />
    </div>
  );
}

/**
 * The continuous step of the run: the selected step when it is continuous,
 * else the first one. A sub-run has no route for the samples.
 */
function ContinuousBody({ details, step }: RunTabProps) {
  const { ts } = useI18n();
  const dockStep = useDockStep();
  const node = pickContinuousNode(details, step ?? dockStep);
  if (!node) return <RunTabEmpty>{ts('No samples yet')}</RunTabEmpty>;
  if (isSubDAGRun(details)) {
    return (
      <RunTabEmpty>
        {ts('The samples of a step of a sub-run are not available here.')}
      </RunTabEmpty>
    );
  }
  return (
    <ContinuousPanel
      // One panel for each step: the window and the selection start again.
      key={node.step.name}
      name={details.name}
      runId={details.dagRunId}
      node={node}
      active={isRunActive(details)}
      startedAt={details.startedAt}
    />
  );
}

/**
 * The step log that ?step names, or the step that the auto-open chose. A
 * failed step opens on stderr, where the text of the failure is. Without a
 * step, the scheduler log of the run.
 */
function LogBody({ run, details, step }: RunTabProps) {
  const { ts } = useI18n();
  const [url, setUrl] = useWorkspaceUrl();
  const { autoStep, openStep } = useDock();
  // A host that shows one step names it, and the URL does not choose.
  const linked = step ? { step } : logTargetFromUrl(url, details);
  const auto =
    linked === null && autoStep && run.nodeByStep.has(autoStep.step)
      ? autoStep
      : null;
  const chosen =
    linked ?? (auto ? { ...auto, stream: auto.stream ?? 'stderr' } : null);
  if (chosen) {
    const node = run.nodeByStep.get(chosen.step);
    if (!node) {
      return (
        <RunTabEmpty>
          {ts('This step did not run in the selected run.')}
        </RunTabEmpty>
      );
    }
    const target: StepRunTarget =
      !chosen.stream &&
      !chosen.subRun &&
      !chosen.foreach &&
      node.status === NodeStatus.Failed
        ? { ...chosen, stream: 'stderr' }
        : chosen;
    const foreachItems = !target.foreach && isForeachStep(node);
    return (
      <div className="flex h-full min-h-0 flex-col">
        {/* The error is the one of a step of this run, not of a sub run. */}
        {!target.subRun && (
          <StepFailureBanner step={target.step} className="shrink-0" />
        )}
        {foreachItems &&
          (step ? (
            // The node view lists the items, with their failures, over the
            // raw output of the step; a body step log opens in the panel.
            <div className="max-h-[50%] shrink-0 overflow-auto border-b px-3 py-2">
              <StepForeach
                node={node}
                holder={details}
                onOpenLogs={(t) => openStep(t)}
              />
            </div>
          ) : (
            // The log of a foreach step is its raw output: the items, with
            // their failures, are in the node view.
            <div className="flex shrink-0 items-center gap-2 border-b px-3 py-1 text-xs text-muted-foreground">
              <span>{ts('This step runs once for each item.')}</span>
              <Button
                type="button"
                variant="ghost"
                size="xs"
                onClick={() =>
                  setUrl(
                    { step: target.step, ndv: 'expanded' },
                    { replace: true }
                  )
                }
              >
                {ts('View items')}
              </Button>
            </div>
          ))}
        <div className="min-h-0 flex-1">
          <StepRunView
            // The stream is not in the key: the viewer switches it in place,
            // and a remount would empty the log while the other stream loads.
            key={JSON.stringify({ ...target, stream: undefined })}
            target={target}
            variant="logs"
          />
        </div>
      </div>
    );
  }
  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* The padding keeps the first row clear of the panel edges; the log
          scrolls inside this box. The facts of the run are in the run
          header. */}
      <div
        data-testid="ww-run-log"
        className="min-h-0 flex-1 overflow-hidden px-3 pb-2 pt-2"
      >
        <ExecutionLog
          name={details.name}
          dagRunId={details.dagRunId}
          dagRun={details}
          pageView={false}
          // The reused steps ran before the run: the log puts their lines
          // before its own, or after them when the newest line is at the top.
          cachedLines={
            cachedSteps(details).length > 0
              ? (mode) => <CachedLogs details={details} mode={mode} />
              : undefined
          }
        />
      </div>
    </div>
  );
}

export const TimelineTab = runTab(TimelineBody, 'TimelineTab');
export const OutputsTab = runTab(OutputsBody, 'OutputsTab');
export const ArtifactsDockTab = runTab(ArtifactsBody, 'ArtifactsDockTab');
export const ActionsTab = runTab(ActionsBody, 'ActionsTab');
export const AgentTab = runTab(AgentBody, 'AgentTab');
export const ChatTab = runTab(ChatBody, 'ChatTab');
export const TasksTab = runTab(TasksBody, 'TasksTab');
export const ContinuousTab = runTab(ContinuousBody, 'ContinuousTab');
export const LogTab = runTab(LogBody, 'LogTab');
