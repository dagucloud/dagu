// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { NodeStatus } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import {
  ChatHistoryTab,
  hasChatHistory,
} from '@/features/dags/components/chat-history/ChatHistoryTab';
import {
  findRunNode,
  isRunActive,
} from '@/features/visual-editor/run/stepRunInfo';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { useI18n } from '@/i18n/I18nProvider';
import type { ApiDAGRunDetails, ApiNode } from '../contracts';
import { useWorkspaceData } from '../data/context';
import { StepDeclared } from './StepDeclared';
import { StepFacts } from './StepFacts';
import { StepLogPane } from './StepLogPane';
import { StepManualActions } from './StepManualActions';

export interface StepRunTarget {
  /** Server step name in the selected run. */
  step: string;
  stream?: 'stdout' | 'stderr';
  /** -> ForeachBodyStepLog with { stepName: step, item } */
  foreach?: { item: string; bodyStep: string };
  /** The step belongs to this sub run of the selected root run. */
  subRun?: { dagName: string; subDAGRunId: string };
}

export interface StepRunViewProps {
  target: StepRunTarget;
  /** Only 'logs' mounts StepLog or ForeachBodyStepLog. */
  variant: 'facts' | 'logs';
  onOpenLogs?(t: StepRunTarget): void;
}

const ACTIVE_SUB_RUN_REFRESH_MS = 2000;

/** The run that holds the target step, and the step's node in it. */
function useTargetRun(target: StepRunTarget): {
  holder: ApiDAGRunDetails | null;
  node: ApiNode | undefined;
} {
  const { run } = useWorkspaceData('StepRunView');
  const remoteNode = useRemoteNode();
  const subDAGRunId = target.subRun?.subDAGRunId;
  const { data } = useQuery(
    '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}',
    whenEnabled(!!subDAGRunId && !!run.ref, {
      params: {
        path: {
          name: run.ref?.name ?? '',
          dagRunId: run.ref?.runId ?? '',
          subDAGRunId: subDAGRunId ?? '',
        },
        query: { remoteNode },
      },
    }),
    {
      refreshInterval: (latest) =>
        isRunActive(latest?.dagRunDetails) ? ACTIVE_SUB_RUN_REFRESH_MS : 0,
    }
  );
  if (subDAGRunId) {
    const details = data?.dagRunDetails ?? null;
    return {
      holder: details,
      node: findRunNode(details ?? undefined, target.step),
    };
  }
  return { holder: run.details, node: run.nodeByStep.get(target.step) };
}

/** The transcript of a chat or agent step that started. */
function StepChat({
  holder,
  node,
}: {
  holder: ApiDAGRunDetails | null;
  node: ApiNode | undefined;
}) {
  const { ts } = useI18n();
  if (!holder || !node || !hasChatHistory(node)) return null;
  if (node.status === NodeStatus.NotStarted) return null;
  return (
    <section className="space-y-1" aria-label={ts('Chat')}>
      <div className="text-xs font-medium text-muted-foreground">
        {ts('Chat')}
      </div>
      <ChatHistoryTab dagRun={holder} stepName={node.step.name} />
    </section>
  );
}

/**
 * Every run fact and action of one step. 'facts' goes in the OUTPUT column
 * of the step view, as a region named '<step> run'; 'logs' goes in the dock
 * and is the only one with a log.
 */
export function StepRunView({ target, variant, onOpenLogs }: StepRunViewProps) {
  const { ts } = useI18n();
  const { holder, node } = useTargetRun(target);
  return (
    <div
      role={variant === 'facts' ? 'region' : undefined}
      aria-label={
        variant === 'facts'
          ? ts('{step} run', { step: target.step })
          : undefined
      }
      data-testid="ww-step-run"
      data-step={target.step}
      data-variant={variant}
      className={variant === 'logs' ? 'h-full min-h-0' : undefined}
    >
      {variant === 'logs' ? (
        <StepLogPane target={target} holder={holder} node={node} />
      ) : (
        <div className="space-y-3">
          <StepDeclared step={target.step} node={node} />
          <StepFacts
            target={target}
            holder={holder}
            node={node}
            onOpenLogs={onOpenLogs}
          />
          <StepManualActions
            holder={holder}
            node={node}
            inSubRun={!!target.subRun}
          />
          <StepChat holder={holder} node={node} />
        </div>
      )}
    </div>
  );
}
