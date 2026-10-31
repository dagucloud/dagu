// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ArrowRight } from 'lucide-react';
import { AgentInteractionStatus, AgentSessionState } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { getManualActionState } from '@/features/dag-runs/lib/manualActionState';
import { ApprovalTab } from '@/features/dags/components/approval/ApprovalTab';
import { HumanTasksTab } from '@/features/dags/components/human-task/HumanTasksTab';
import { useI18n } from '@/i18n/I18nProvider';
import type { ApiDAGRunDetails, ApiNode } from '../contracts';
import { useWorkspaceData } from '../data/context';
import { useDockCommands } from '../dock/DockContext';

export interface StepManualActionsProps {
  /** The run that holds the step; null before it loads or when none is selected. */
  holder: ApiDAGRunDetails | null;
  node: ApiNode | undefined;
  /** The step belongs to a sub run, which the dock Agent tab does not show. */
  inSubRun?: boolean;
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
 * The manual action that a waiting step needs: its approval card, its human
 * task card, or a link to the dock Agent tab. Nothing for other steps.
 */
export function StepManualActions({
  holder,
  node,
  inSubRun = false,
}: StepManualActionsProps) {
  const { ts } = useI18n();
  const dock = useDockCommands();
  const { refresh } = useWorkspaceData('StepManualActions');
  if (!holder || !node) return null;

  const name = node.step.name;
  const { waitingApprovalNodes, waitingHumanTaskNodes } =
    getManualActionState(holder);
  const approval = waitingApprovalNodes.some((n) => n.step.name === name);
  const humanTask = waitingHumanTaskNodes.some((n) => n.step.name === name);
  const agent = !inSubRun && isAgentAwaitingAnswer(node);
  if (!approval && !humanTask && !agent) return null;

  return (
    <section
      className="space-y-2"
      aria-label={ts('Action required')}
      data-testid="ww-step-manual-actions"
    >
      <div className="text-xs font-medium text-muted-foreground">
        {ts('Action required')}
      </div>
      {approval && (
        <ApprovalTab dagRun={holder} dagName={holder.name} stepName={name} />
      )}
      {humanTask && (
        <HumanTasksTab
          key={`${holder.name}:${holder.dagRunId}`}
          dagRun={holder}
          onChanged={() => void refresh()}
          stepName={name}
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
            onClick={() => dock.setTab('agent', { user: true })}
          >
            {ts('Answer in the Agent tab')}
            <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
          </Button>
        </div>
      )}
    </section>
  );
}
