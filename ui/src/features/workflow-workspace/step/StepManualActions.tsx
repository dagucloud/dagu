// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ArrowRight } from 'lucide-react';
import type { ReactNode } from 'react';
import {
  AgentInteractionStatus,
  AgentSessionState,
  NodeStatus,
  Status,
} from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { getManualActionState } from '@/features/dag-runs/lib/manualActionState';
import { ApprovalTab } from '@/features/dags/components/approval/ApprovalTab';
import {
  useOpenSubRun,
  type SubRunStackEntry,
} from '@/features/dags/components/common/SubRunStackModal';
import { HumanTasksTab } from '@/features/dags/components/human-task/HumanTasksTab';
import { isSubDAGRun } from '@/features/dags/hooks/useStepLogQuery';
import { useQuery } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import type { ApiDAGRunDetails, ApiNode } from '../contracts';
import { useWorkspaceData } from '../data/context';
import { useDockCommands } from '../dock/DockContext';
import { allSubRunsOf, useStepSubRunsOpener } from './useStepRunActions';

/** How often the runs under a waiting step are read again, in ms. */
const WAITING_REFRESH_MS = 3000;
/** The waiting runs of one step that show their actions; a picker has all. */
const MAX_WAITING_SHOWN = 5;
/** The depth of calls that the actions are looked for in. */
const MAX_WAITING_DEPTH = 8;

export interface StepManualActionsProps {
  /** The run that holds the step; null before it loads or when none is selected. */
  holder: ApiDAGRunDetails | null;
  node: ApiNode | undefined;
  /** The step belongs to a sub run, which the dock Agent tab does not show. */
  inSubRun?: boolean;
  /**
   * Shows the agent sessions of `holder`, where a waiting agent is answered:
   * a host of a sub run gives it, the dock Agent tab is the default.
   */
  onOpenAgent?(): void;
  /** Called after an approval or a task changed the run of the step. */
  onChanged?(): void;
  /**
   * The runs from the run of the panel down to `holder`, for a step that
   * waits in a run that a step of the panel called. Its card shows this path
   * in place of the title.
   */
  trail?: readonly SubRunStackEntry[];
}

/**
 * Whether the agent session of the step needs the Agent tab: it waits for a
 * question or permission answer, or it is unavailable (as the run's waiting
 * agent count).
 */
export function isAgentAwaitingAnswer(node: ApiNode): boolean {
  const session = node.agentSession;
  if (!session) return false;
  return (
    session.state === AgentSessionState.waiting ||
    session.state === AgentSessionState.unavailable ||
    (session.interactions ?? []).some(
      (interaction) => interaction.status === AgentInteractionStatus.pending
    )
  );
}

/**
 * True for a step that calls a workflow and waits while its sub-run waits
 * for a person: the action is in the sub-run.
 */
export function waitsInSubRun(node: ApiNode): boolean {
  return node.status === NodeStatus.Waiting && allSubRunsOf(node).length > 0;
}

/**
 * The manual action that a waiting step needs: its approval card, its human
 * task card, a link to the dock Agent tab, or, for a step whose sub-run
 * waits, the cards of the steps that wait in its sub-runs, at any depth.
 * Nothing for other steps.
 */
export function StepManualActions({
  holder,
  node,
  inSubRun = false,
  onChanged,
  onOpenAgent,
  trail = [],
}: StepManualActionsProps) {
  const { ts } = useI18n();
  const dock = useDockCommands();
  const { refresh } = useWorkspaceData('StepManualActions');
  const openOnStack = useOpenSubRun();
  const changed = () => {
    onChanged?.();
    void refresh();
  };
  if (!holder || !node) return null;

  const name = node.step.name;
  const { waitingApprovalNodes, waitingHumanTaskNodes } =
    getManualActionState(holder);
  const approval = waitingApprovalNodes.some((n) => n.step.name === name);
  const humanTask = waitingHumanTaskNodes.some((n) => n.step.name === name);
  const agent =
    (!inSubRun || onOpenAgent !== undefined) && isAgentAwaitingAnswer(node);
  const below = !approval && !humanTask && waitsInSubRun(node);
  if (!approval && !humanTask && !agent && !below) return null;
  const nested = trail.length > 0;

  const cards = (
    <>
      {approval && (
        <ApprovalTab
          dagRun={holder}
          dagName={holder.name}
          stepName={name}
          onChanged={changed}
          bare
        />
      )}
      {/* The server completes the human tasks of a root run only. */}
      {humanTask &&
        (isSubDAGRun(holder) ? (
          <p
            className="text-sm text-muted-foreground"
            data-testid="ww-step-sub-run-task"
          >
            {ts('A human task of a sub-run cannot be completed here.')}
          </p>
        ) : (
          <HumanTasksTab
            key={`${holder.name}:${holder.dagRunId}`}
            dagRun={holder}
            onChanged={changed}
            stepName={name}
            bare
          />
        ))}
      {below && !nested && <SubRunWaitingNotice holder={holder} node={node} />}
      {below && (
        <WaitingSubRuns
          holder={holder}
          node={node}
          trail={trail}
          onChanged={onChanged}
        />
      )}
      {agent && (
        <div
          className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-warning/40 bg-warning/5 p-3"
          data-testid="ww-step-agent-waiting"
        >
          <p className="text-sm text-muted-foreground">
            {ts('An agent is waiting for an answer.')}
          </p>
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={() =>
              onOpenAgent ? onOpenAgent() : dock.setTab('agent', { user: true })
            }
          >
            {ts('Answer in the Agent tab')}
            <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
          </Button>
        </div>
      )}
    </>
  );

  // A step that waits in its own sub-runs leads on to their cards only.
  if (nested && below) return cards;
  if (nested) {
    // The runs of the trail open on the stack, down to this step.
    const openTrail = openOnStack
      ? () =>
          trail.forEach((entry, i) =>
            openOnStack(
              i === trail.length - 1 ? { ...entry, step: name } : entry
            )
          )
      : undefined;
    return (
      <section
        className="space-y-1.5"
        aria-label={trail.map((entry) => entry.name).join(' › ')}
        data-testid="ww-step-nested-action"
      >
        <div className="flex min-w-0 items-center justify-between gap-2">
          <span className="min-w-0 truncate text-xs text-muted-foreground">
            {trail.map((entry) => entry.name).join(' › ')}
          </span>
          {openTrail && (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              className="h-6 shrink-0 px-1.5 text-xs"
              onClick={openTrail}
            >
              {ts('Open sub-run')}
              <ArrowRight className="h-3 w-3" aria-hidden="true" />
            </Button>
          )}
        </div>
        {cards}
      </section>
    );
  }
  return (
    <section
      className="space-y-2"
      aria-label={ts('Action required')}
      data-testid="ww-step-manual-actions"
    >
      <div className="text-xs font-medium text-muted-foreground">
        {ts('Action required')}
      </div>
      {cards}
    </section>
  );
}

/**
 * The way to the sub-runs of a step that waits in them. `named` says the
 * name of the step, for a list of the steps of a run.
 */
export function SubRunWaitingNotice({
  holder,
  node,
  named = false,
}: {
  holder: ApiDAGRunDetails;
  node: ApiNode;
  named?: boolean;
}) {
  const { ts } = useI18n();
  const subRuns = useStepSubRunsOpener();
  return (
    <div
      className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-warning/40 bg-warning/5 p-3"
      data-testid="ww-step-sub-run-waiting"
    >
      <p className="text-sm text-muted-foreground">
        {named
          ? ts('A sub-run of {step} is waiting for an action.', {
              step: node.step.name,
            })
          : ts('A sub-run of this step is waiting for an action.')}
      </p>
      <Button
        type="button"
        size="sm"
        variant="outline"
        onClick={() => subRuns.open(node, holder)}
      >
        {ts('Open sub-run')}
        <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
      </Button>
      {subRuns.picker}
    </div>
  );
}

/**
 * The cards of the steps that wait for a person in the runs that `node`
 * called, at any depth, each under the path of its run: an action that
 * waits deep in the calls is taken from the step that waits for it.
 * `fallback` shows while no such step is known, as before the runs load.
 */
export function WaitingSubRuns({
  holder,
  node,
  trail = [],
  onChanged,
  fallback = null,
}: {
  holder: ApiDAGRunDetails;
  node: ApiNode;
  trail?: readonly SubRunStackEntry[];
  onChanged?(): void;
  fallback?: ReactNode;
}) {
  const { ts } = useI18n();
  const remoteNode = useRemoteNode();
  const subRuns = useStepSubRunsOpener();
  const rootName = holder.rootDAGRunName || holder.name;
  const rootId = holder.rootDAGRunId || holder.dagRunId;
  const { data } = useQuery(
    '/dag-runs/{name}/{dagRunId}/sub-dag-runs',
    {
      params: {
        path: { name: rootName, dagRunId: rootId },
        query: {
          remoteNode,
          parentSubDAGRunId: isSubDAGRun(holder) ? holder.dagRunId : undefined,
        },
      },
    },
    { refreshInterval: WAITING_REFRESH_MS }
  );
  // Deeper than the lookup goes, the step leads to its sub-runs.
  if (trail.length >= MAX_WAITING_DEPTH) {
    return (
      fallback ?? <SubRunWaitingNotice holder={holder} node={node} named />
    );
  }
  const ids = new Set(allSubRunsOf(node).map((run) => run.dagRunId));
  const waiting = (data?.subRuns ?? []).filter(
    (run) => ids.has(run.dagRunId) && run.status === Status.Waiting
  );
  if (waiting.length === 0) return fallback;
  const hidden = waiting.length - MAX_WAITING_SHOWN;
  return (
    <div className="space-y-3" data-testid="ww-waiting-sub-runs">
      {waiting.slice(0, MAX_WAITING_SHOWN).map((run) => (
        <WaitingSubRun
          key={run.dagRunId}
          rootName={rootName}
          rootId={rootId}
          entry={{
            name: run.dagName || node.step.call || node.step.name,
            dagRunId: run.dagRunId,
          }}
          trail={trail}
          onChanged={onChanged}
        />
      ))}
      {hidden > 0 && (
        <Button
          type="button"
          size="sm"
          variant="ghost"
          className="h-6 px-1.5 text-xs"
          onClick={() => subRuns.open(node, holder)}
        >
          {ts('{count} more sub-runs are waiting', { count: hidden })}
        </Button>
      )}
      {subRuns.picker}
    </div>
  );
}

/** The cards of the steps that wait in one sub-run. */
function WaitingSubRun({
  rootName,
  rootId,
  entry,
  trail,
  onChanged,
}: {
  rootName: string;
  rootId: string;
  entry: SubRunStackEntry;
  trail: readonly SubRunStackEntry[];
  onChanged?(): void;
}) {
  const remoteNode = useRemoteNode();
  const { data, mutate } = useQuery(
    '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}',
    {
      params: {
        path: { name: rootName, dagRunId: rootId, subDAGRunId: entry.dagRunId },
        query: { remoteNode },
      },
    },
    { refreshInterval: WAITING_REFRESH_MS }
  );
  const details = data?.dagRunDetails;
  if (!details) return null;
  const { waitingApprovalNodes, waitingHumanTaskNodes, waitingSubRunNodes } =
    getManualActionState(details);
  const nodes = [
    ...waitingApprovalNodes,
    ...waitingHumanTaskNodes.filter((n) => !waitingApprovalNodes.includes(n)),
    ...waitingSubRunNodes,
  ];
  const path = [...trail, entry];
  return (
    <>
      {nodes.map((waitingNode) => (
        <StepManualActions
          key={waitingNode.step.name}
          holder={details}
          node={waitingNode}
          inSubRun
          trail={path}
          onChanged={() => {
            void mutate();
            onChanged?.();
          }}
        />
      ))}
    </>
  );
}
