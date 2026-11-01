// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Summary blocks of one DAG run: the step progress, the status message, the
 * run error, the runtime conditions and the unmet preconditions.
 */
import RelativeTime from '@/components/ui/relative-time';
import {
  humanizeIdentifier,
  runtimeConditionLabel,
  RuntimeCondition,
} from '@/features/dag-runs/components/common/runtimeConditions';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { statusLabelText } from '@/lib/status-utils';
import {
  AlertCircle,
  Clock,
  Info,
  LucideIcon,
  PlayCircle,
  StopCircle,
  Terminal,
} from 'lucide-react';
import React, { useMemo } from 'react';
import {
  components,
  DAGRunConditionStatus,
  NodeStatusLabel,
  Status,
} from '../../../../api/v1/schema';

type NodeStatusConfig = {
  key: NodeStatusLabel;
  colorClass: string;
};

// Unified status colors matching the execution graph
const NODE_STATUS_CONFIG: NodeStatusConfig[] = [
  { key: NodeStatusLabel.succeeded, colorClass: 'bg-[var(--status-success)]' },
  { key: NodeStatusLabel.running, colorClass: 'bg-[var(--status-running)]' },
  { key: NodeStatusLabel.retrying, colorClass: 'bg-[var(--status-warning)]' },
  {
    key: NodeStatusLabel.partially_succeeded,
    colorClass: 'bg-[var(--status-warning)]',
  },
  { key: NodeStatusLabel.failed, colorClass: 'bg-[var(--status-error)]' },
  {
    key: NodeStatusLabel.not_started,
    colorClass: 'bg-[var(--status-neutral)]',
  },
  { key: NodeStatusLabel.skipped, colorClass: 'bg-[var(--status-neutral)]' },
  { key: NodeStatusLabel.aborted, colorClass: 'bg-[var(--status-aborted)]' },
  { key: NodeStatusLabel.waiting, colorClass: 'bg-[var(--status-warning)]' },
  { key: NodeStatusLabel.rejected, colorClass: 'bg-[var(--status-error)]' },
];

type ExecutionStatusConfig = {
  status: Status;
  icon: LucideIcon;
  iconClass: string;
  message: string;
};

const EXECUTION_STATUS_CONFIG: ExecutionStatusConfig[] = [
  {
    status: Status.Running,
    icon: PlayCircle,
    iconClass: 'text-[var(--status-running)]',
    message: 'Execution in progress',
  },
  {
    status: Status.Queued,
    icon: Clock,
    iconClass: 'text-[var(--status-neutral)]',
    message: 'DAGRun is queued for execution',
  },
  {
    status: Status.Aborted,
    icon: StopCircle,
    iconClass: 'text-[var(--status-aborted)]',
    message: 'Execution was aborted',
  },
  {
    status: Status.Waiting,
    icon: Clock,
    iconClass: 'text-[var(--status-warning)]',
    message: 'Waiting for manual action',
  },
  {
    status: Status.Rejected,
    icon: StopCircle,
    iconClass: 'text-[var(--status-error)]',
    message: 'Execution was rejected',
  },
];

type RunNodeProgressProps = {
  nodes?: components['schemas']['Node'][];
};

/** The bar of step statuses and the count of succeeded steps. */
export function RunNodeProgress({
  nodes,
}: RunNodeProgressProps): React.JSX.Element {
  const { ts } = useI18n();
  const nodeStatus = useMemo(
    () =>
      nodes?.reduce<Record<string, number>>((acc, node) => {
        const statusKey = node.statusLabel.toLowerCase().replace(' ', '_');
        acc[statusKey] = (acc[statusKey] || 0) + 1;
        return acc;
      }, {}),
    [nodes]
  );
  const totalNodes = nodes?.length ?? 0;

  return (
    <>
      {totalNodes > 0 && nodeStatus && (
        <div className="flex-1 h-2 bg-muted rounded-full overflow-hidden flex">
          {NODE_STATUS_CONFIG.map((config) => {
            const count = nodeStatus[config.key];
            if (!count) return null;
            return (
              <div
                key={config.key}
                className={`h-full ${config.colorClass}`}
                style={{ width: `${(count / totalNodes) * 100}%` }}
                title={`${ts(statusLabelText(config.key))}: ${count}`}
              />
            );
          })}
        </div>
      )}
      <span className="text-xs text-muted-foreground tabular-nums">
        {nodeStatus?.succeeded ?? 0}/{totalNodes}
      </span>
    </>
  );
}

type SchedulerLogButtonProps = {
  dagRunId: string;
  onViewLog: (dagRunId: string) => void;
};

/** Opens the scheduler log of the run. */
export function SchedulerLogButton({
  dagRunId,
  onViewLog,
}: SchedulerLogButtonProps): React.JSX.Element {
  return (
    <I18nProps>
      <button
        onClick={() => onViewLog(dagRunId)}
        className="inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium rounded border border-border bg-card hover:bg-muted transition-colors cursor-pointer"
        title="View scheduler log"
      >
        <Terminal className="h-3 w-3" />
        <span>
          <I18nText text={'Log'} />
        </span>
      </button>
    </I18nProps>
  );
}

type RunProgressProps = {
  status: components['schemas']['DAGRunDetails'];
  onViewLog?: (dagRunId: string) => void;
};

/** The step progress, with the scheduler log button when onViewLog is set. */
export function RunProgress({
  status,
  onViewLog,
}: RunProgressProps): React.JSX.Element {
  return (
    <div className="flex items-center gap-2 text-xs">
      <RunNodeProgress nodes={status.nodes} />
      {status.dagRunId && onViewLog && (
        <SchedulerLogButton dagRunId={status.dagRunId} onViewLog={onViewLog} />
      )}
    </div>
  );
}

/** The message of a run that runs, waits, is queued, aborted or rejected. */
export function RunStatusMessage({
  status,
}: {
  status: Status;
}): React.JSX.Element | null {
  const config = EXECUTION_STATUS_CONFIG.find((c) => c.status === status);
  if (!config) return null;
  const Icon = config.icon;
  return (
    <div className="flex items-center gap-1 text-xs text-muted-foreground">
      <Icon className={`h-3 w-3 ${config.iconClass}`} />
      <span>
        <I18nText text={config.message} />
      </span>
    </div>
  );
}

function getRuntimeConditionGroups(
  conditions: RuntimeCondition[] | undefined
): {
  summary?: RuntimeCondition;
  details: RuntimeCondition[];
} {
  const summary = conditions?.find(
    (condition) => condition.type === 'Runnable'
  );
  const details =
    conditions?.filter(
      (condition) =>
        condition !== summary && condition.status !== DAGRunConditionStatus.True
    ) ?? [];

  return { summary, details };
}

function RuntimeConditionCard({
  condition,
}: {
  condition: RuntimeCondition;
}): React.JSX.Element {
  return (
    <div className="rounded-md border border-border bg-slate-200 p-1.5 text-xs whitespace-normal break-words dark:bg-slate-700">
      <div className="mb-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5">
        <span className="font-semibold text-foreground">
          {runtimeConditionLabel(condition)}
        </span>
        <RelativeTime
          className="font-mono text-muted-foreground"
          timestamp={condition.checkedAt}
        />
      </div>
      <div className="text-muted-foreground break-words">
        {condition.message}
      </div>
      {condition.reason && (
        <div className="mt-0.5 text-muted-foreground break-words">
          <I18nText text={'Reason:'} /> {humanizeIdentifier(condition.reason)}
        </div>
      )}
    </div>
  );
}

/** The runtime conditions that are not true. */
export function RuntimeConditions({
  conditions,
}: {
  conditions?: components['schemas']['DAGRunCondition'][];
}): React.JSX.Element | null {
  const { summary, details } = getRuntimeConditionGroups(conditions);
  const visibleSummary =
    summary?.status === DAGRunConditionStatus.True ? undefined : summary;
  const visibleConditions = visibleSummary
    ? [visibleSummary, ...details]
    : details;

  if (visibleConditions.length === 0) {
    return null;
  }

  return (
    <div className="pb-2" data-testid="runtime-conditions">
      <div className="flex items-center mb-1">
        <Info className="h-3.5 w-3.5 mr-1 text-muted-foreground" />
        <span className="text-xs font-semibold text-muted-foreground">
          <I18nText text={'Runtime Conditions'} />
        </span>
      </div>
      <div className="space-y-2">
        {visibleConditions.map((condition, idx) => (
          <RuntimeConditionCard
            key={`${condition.type}-${condition.reason}-${idx}`}
            condition={condition}
          />
        ))}
      </div>
    </div>
  );
}

/** The error of the run. */
export function RunError({
  error,
}: {
  error?: string;
}): React.JSX.Element | null {
  if (!error) {
    return null;
  }
  return (
    <div className="pb-2" data-testid="dag-run-error">
      <div className="flex items-center mb-1">
        <AlertCircle className="h-3.5 w-3.5 mr-1 text-error" />
        <span className="text-xs font-semibold text-error">
          <I18nText text={'Run error'} />
        </span>
      </div>
      <div className="p-1.5 bg-error-muted border border-error/20 rounded-md text-xs text-error font-medium whitespace-pre-wrap break-words">
        {error}
      </div>
    </div>
  );
}

/** The run preconditions that were not met. */
export function PreconditionErrors({
  preconditions,
}: {
  preconditions?: components['schemas']['Condition'][];
}): React.JSX.Element | null {
  const errors = preconditions?.filter((cond) => cond.error);

  if (!errors || errors.length === 0) {
    return null;
  }

  return (
    <div className="pb-2">
      <div className="flex items-center mb-1">
        <Info className="h-3.5 w-3.5 mr-1 text-warning" />
        <span className="text-xs font-semibold text-warning">
          <I18nText text={'Run precondition not met'} />
        </span>
      </div>
      <div className="space-y-2">
        {errors.map((cond, idx) => (
          <div
            key={idx}
            className="p-1.5 bg-warning-muted border border-warning/20 rounded-md text-xs text-warning font-medium whitespace-normal break-words"
          >
            <div className="mb-0.5 break-words">
              <I18nText text={'Condition:'} /> {cond.condition}
            </div>
            <div className="mb-0.5 break-words">
              <I18nText text={'Expected:'} /> {cond.expected}
            </div>
            <div className="break-words">
              <I18nText text={'Error:'} /> {cond.error}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
