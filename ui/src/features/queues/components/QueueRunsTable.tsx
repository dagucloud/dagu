// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { components, DAGRunConditionStatus, Status } from '@/api/v1/schema';
import { Checkbox } from '@/components/ui/checkbox';
import { useConfig } from '@/contexts/ConfigContext';
import {
  humanizeIdentifier,
  runtimeConditionLabel,
  RuntimeCondition,
} from '@/features/dag-runs/components/common/runtimeConditions';
import dayjs from '@/lib/dayjs';
import { cn } from '@/lib/utils';
import StatusChip from '@/components/ui/status-chip';

type QueueDAGRun = components['schemas']['DAGRunSummary'];

interface QueueRunsTableProps {
  items: QueueDAGRun[];
  onDAGRunClick: (dagRun: QueueDAGRun) => void;
  selectable?: boolean;
  disableSelection?: boolean;
  headerCheckboxState?: boolean | 'indeterminate';
  isSelected?: (dagRun: QueueDAGRun) => boolean;
  onToggleSelection?: (dagRun: QueueDAGRun) => void;
  onToggleAll?: (checked: boolean) => void;
  showQueuedAt?: boolean;
}

const QUEUED_CONDITION_FALLBACK_PRIORITY = [
  'QueueReady',
  'RunRecordReady',
  'ConcurrencyReady',
  'WorkerReady',
  'WorkerAssignmentReady',
  'StartObserved',
];

function queuedConditionFallbackRank(condition: RuntimeCondition): number {
  const index = QUEUED_CONDITION_FALLBACK_PRIORITY.indexOf(condition.type);
  return index === -1 ? QUEUED_CONDITION_FALLBACK_PRIORITY.length : index;
}

function compareQueuedConditionFallback(
  left: RuntimeCondition,
  right: RuntimeCondition
): number {
  return (
    queuedConditionFallbackRank(left) - queuedConditionFallbackRank(right) ||
    left.type.localeCompare(right.type) ||
    left.status.localeCompare(right.status) ||
    (left.reason ?? '').localeCompare(right.reason ?? '') ||
    (left.checkedAt ?? '').localeCompare(right.checkedAt ?? '')
  );
}

function getQueuedConditionSummary(
  conditions: RuntimeCondition[] | undefined
): RuntimeCondition | undefined {
  const runnable = conditions?.find(
    (condition) =>
      condition.type === 'Runnable' &&
      condition.status !== DAGRunConditionStatus.True
  );
  return (
    runnable ??
    conditions
      ?.filter((condition) => condition.status !== DAGRunConditionStatus.True)
      .sort(compareQueuedConditionFallback)[0]
  );
}

function QueueRunsTable({
  items,
  onDAGRunClick,
  selectable = false,
  disableSelection = false,
  headerCheckboxState = false,
  isSelected,
  onToggleSelection,
  onToggleAll,
  showQueuedAt = false,
}: QueueRunsTableProps) {
  const config = useConfig();

  const formatDateTime = React.useCallback(
    (datetime: string | undefined): string => {
      if (!datetime) {
        return 'N/A';
      }
      const date = dayjs(datetime);
      const offset = config.tzOffsetInSec;
      const format = 'MMM D, HH:mm:ss';
      return offset !== undefined
        ? date.utcOffset(offset / 60).format(format)
        : date.format(format);
    },
    [config.tzOffsetInSec]
  );

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-xs">
        <thead>
          <tr className="border-b border-border">
            {selectable && (
              <th className="w-10 py-1 px-2 align-middle">
                <div className="flex h-8 w-8 items-center justify-center">
                  <Checkbox
                    aria-label="Select all loaded queue items"
                    checked={headerCheckboxState}
                    disabled={disableSelection || items.length === 0}
                    onCheckedChange={(checked) =>
                      onToggleAll?.(Boolean(checked))
                    }
                  />
                </div>
              </th>
            )}
            <th className="text-left py-1 px-2 font-medium text-muted-foreground">
              DAG
            </th>
            <th className="text-left py-1 px-2 font-medium text-muted-foreground">
              Status
            </th>
            <th className="text-left py-1 px-2 font-medium text-muted-foreground">
              Timing
            </th>
            <th className="text-left py-1 px-2 font-medium text-muted-foreground">
              Run ID
            </th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border/50">
          {items.map((dagRun) => {
            const selected = selectable && Boolean(isSelected?.(dagRun));
            const queuedConditionSummary =
              dagRun.status === Status.Queued
                ? getQueuedConditionSummary(dagRun.conditions)
                : undefined;

            return (
              <tr
                key={dagRun.dagRunId}
                onClick={() => onDAGRunClick(dagRun)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' || event.key === ' ') {
                    event.preventDefault();
                    onDAGRunClick(dagRun);
                  }
                }}
                role="button"
                tabIndex={0}
                className={cn(
                  'cursor-pointer transition-colors focus:bg-muted/50 focus:outline-none hover:bg-muted/30',
                  selected && 'bg-muted/20'
                )}
              >
                {selectable && (
                  <td
                    className="w-10 py-1.5 px-2 align-middle"
                    onClick={(event) => event.stopPropagation()}
                    onKeyDown={(event) => event.stopPropagation()}
                  >
                    <div className="flex h-8 w-8 items-center justify-center">
                      <Checkbox
                        aria-label={`Select ${dagRun.name} ${dagRun.dagRunId}`}
                        checked={selected}
                        disabled={disableSelection}
                        onCheckedChange={() => onToggleSelection?.(dagRun)}
                      />
                    </div>
                  </td>
                )}
                <td className="py-1.5 px-2 text-xs font-medium">
                  {dagRun.name}
                </td>
                <td className="py-1.5 px-2">
                  <StatusChip status={dagRun.status} size="xs">
                    {dagRun.statusLabel}
                  </StatusChip>
                </td>
                <td className="py-1.5 px-2 text-xs text-muted-foreground tabular-nums">
                  <div className="flex flex-col gap-0.5">
                    {dagRun.scheduleTime && (
                      <span>
                        <span className="text-muted-foreground/80">
                          Scheduled{' '}
                        </span>
                        {formatDateTime(dagRun.scheduleTime)}
                      </span>
                    )}
                    <span>
                      <span className="text-muted-foreground/80">
                        {showQueuedAt ? 'Queued ' : 'Started '}
                      </span>
                      {formatDateTime(
                        showQueuedAt ? dagRun.queuedAt : dagRun.startedAt
                      )}
                    </span>
                    {queuedConditionSummary && (
                      <span
                        className="max-w-[28rem] whitespace-normal break-words leading-snug"
                      >
                        <span className="font-medium text-foreground">
                          {runtimeConditionLabel(queuedConditionSummary)}
                        </span>
                        <span className="text-muted-foreground/90">: </span>
                        <span className="text-muted-foreground/90">
                          {queuedConditionSummary.message}
                        </span>
                        {queuedConditionSummary.reason && (
                          <span className="ml-1 text-muted-foreground/80">
                            Reason:{' '}
                            {humanizeIdentifier(queuedConditionSummary.reason)}
                          </span>
                        )}
                        <span className="ml-1 text-muted-foreground/70">
                          Checked{' '}
                          {formatDateTime(queuedConditionSummary.checkedAt)}
                        </span>
                      </span>
                    )}
                  </div>
                </td>
                <td className="py-1.5 px-2 text-xs text-muted-foreground font-mono">
                  {dagRun.dagRunId}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

export default QueueRunsTable;
