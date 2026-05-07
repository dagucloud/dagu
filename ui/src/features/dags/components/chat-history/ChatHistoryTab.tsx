// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, NodeStatus } from '@/api/v1/schema';
import { isActiveNodeStatus } from '@/lib/status-utils';
import { Loader2 } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import { StepMessagesTable } from './StepMessagesTable';

type DAGRunDetails = components['schemas']['DAGRunDetails'];

interface ChatHistoryTabProps {
  dagRun: DAGRunDetails;
}

export function ChatHistoryTab({ dagRun }: ChatHistoryTabProps) {
  // Find all LLM-backed steps that persist message history.
  const historySteps = useMemo(() => {
    return (
      dagRun.nodes?.filter(
        (node) =>
          node.step.executorConfig?.type === 'chat' ||
          node.step.executorConfig?.type === 'agent'
      ) || []
    );
  }, [dagRun.nodes]);

  // Determine default selected step: last finished chat/agent step
  const defaultStep = useMemo(() => {
    const finishedStatuses = [
      NodeStatus.Success,
      NodeStatus.Failed,
      NodeStatus.Aborted,
    ];
    const finishedSteps = historySteps.filter((n) =>
      finishedStatuses.includes(n.status as NodeStatus)
    );

    // Last finished = highest index among finished (assumes nodes are in execution order)
    if (finishedSteps.length > 0) {
      return finishedSteps[finishedSteps.length - 1]?.step.name;
    }

    // Fallback to first history step if none finished
    return historySteps[0]?.step.name;
  }, [historySteps]);

  const [selectedStep, setSelectedStep] = useState<string | undefined>(
    defaultStep
  );
  const [userSelectedStep, setUserSelectedStep] = useState(false);
  const previousRunId = useRef(dagRun.dagRunId);

  // Update selectedStep when defaultStep changes (e.g., when nodes arrive or runs switch)
  useEffect(() => {
    if (previousRunId.current !== dagRun.dagRunId) {
      previousRunId.current = dagRun.dagRunId;
      setUserSelectedStep(false);
      setSelectedStep(defaultStep);
      return;
    }
    if (defaultStep && !userSelectedStep) {
      setSelectedStep(defaultStep);
    }
  }, [dagRun.dagRunId, defaultStep, userSelectedStep]);

  const selectedStepExists = useMemo(() => {
    return historySteps.some((n) => n.step.name === selectedStep);
  }, [historySteps, selectedStep]);

  useEffect(() => {
    if (selectedStep && !selectedStepExists) {
      setUserSelectedStep(false);
      setSelectedStep(defaultStep);
    }
  }, [defaultStep, selectedStep, selectedStepExists]);

  const resolvedStep = selectedStepExists ? selectedStep : defaultStep;

  // Get selected node info
  const selectedNode = historySteps.find((n) => n.step.name === resolvedStep);
  const isSelectedActive = isActiveNodeStatus(selectedNode?.status);

  // Determine if this is a sub-DAG run
  const isSubDAGRun =
    dagRun.rootDAGRunId &&
    dagRun.rootDAGRunName &&
    dagRun.rootDAGRunId !== dagRun.dagRunId;

  if (historySteps.length === 0) {
    return (
      <div className="text-xs text-muted-foreground p-2">
        No chat or agent steps in this DAG run
      </div>
    );
  }

  return (
    <div className="space-y-2">
      {/* Step selector dropdown */}
      <div className="flex items-center gap-2 text-xs">
        <label htmlFor="chat-step-select" className="text-muted-foreground">
          Step:
        </label>
        <select
          id="chat-step-select"
          value={resolvedStep || ''}
          onChange={(e) => {
            setUserSelectedStep(true);
            setSelectedStep(e.target.value);
          }}
          className="h-6 px-2 text-xs border rounded bg-card focus:outline-none"
        >
          {historySteps.map((node) => (
            <option key={node.step.name} value={node.step.name}>
              {node.step.name} ({node.statusLabel})
            </option>
          ))}
        </select>
        {isSelectedActive && (
          <Loader2 className="h-3 w-3 animate-spin text-muted-foreground" />
        )}
      </div>

      {/* Messages table - only for selected step */}
      {resolvedStep && (
        <StepMessagesTable
          dagName={dagRun.name}
          dagRunId={dagRun.dagRunId}
          stepName={resolvedStep}
          isActive={isSelectedActive}
          subDAGRunId={isSubDAGRun ? dagRun.dagRunId : undefined}
          rootDagName={isSubDAGRun ? dagRun.rootDAGRunName : undefined}
          rootDagRunId={isSubDAGRun ? dagRun.rootDAGRunId : undefined}
        />
      )}
    </div>
  );
}
