// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Stream } from '@/api/v1/schema';
import { ForeachItemsSection } from '@/features/dags/components/step-details/foreach/ForeachItemsSection';
import type { ApiDAGRunDetails, ApiNode } from '../contracts';
import type { StepRunTarget } from './StepRunView';

export interface StepForeachProps {
  node: ApiNode;
  /** The run that holds the foreach step. */
  holder: ApiDAGRunDetails;
  /** Set when the holder is a sub run of the selected run. */
  subRun?: StepRunTarget['subRun'];
  /** Opens a body step log, normally in the dock. */
  onOpenLogs(t: StepRunTarget): void;
}

export function isForeachStep(node: ApiNode): boolean {
  return node.step.executorConfig?.type === 'foreach';
}

/** The items of a foreach step; a body step log opens in the dock. */
export function StepForeach({
  node,
  holder,
  subRun,
  onOpenLogs,
}: StepForeachProps) {
  return (
    <ForeachItemsSection
      stepRef={{
        dagName: holder.name,
        dagRunId: holder.dagRunId,
        stepName: node.step.name,
        dagRun: holder,
      }}
      stepStatus={node.status}
      onOpenLog={(bodyStep, foreach, stream) =>
        onOpenLogs({
          step: foreach.stepName,
          stream: stream === Stream.stderr ? 'stderr' : 'stdout',
          foreach: { item: foreach.item, bodyStep },
          ...(subRun ? { subRun } : {}),
        })
      }
    />
  );
}
