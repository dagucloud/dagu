/**
 * DAGStatusOverview component displays summary information about a DAG run.
 *
 * @module features/dags/components/dag-details
 */
import dayjs from '@/lib/dayjs';
import {
  Check,
  Clock,
  Copy,
  Info,
  LucideIcon,
  PlayCircle,
  StopCircle,
  Terminal,
} from 'lucide-react';
import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { components, Status, TriggerType } from '../../../../api/v1/schema';
import { triggerTypeLabels } from '../common/TriggerTypeIndicator';

type Props = {
  status?: components['schemas']['DAGRunDetails'];
  onViewLog?: (dagRunId: string) => void;
};

type NodeStatusConfig = {
  key: string;
  label: string;
  colorClass: string;
};

// Unified status colors matching the execution graph
const NODE_STATUS_CONFIG: NodeStatusConfig[] = [
  { key: 'succeeded', label: 'success', colorClass: 'bg-[#166534]' },
  { key: 'running', label: 'running', colorClass: 'bg-[#43a047]' },
  { key: 'retrying', label: 'retrying', colorClass: 'bg-[#e37400]' },
  { key: 'failed', label: 'failed', colorClass: 'bg-[#d93025]' },
  { key: 'queued', label: 'queued', colorClass: 'bg-[#5f6368]' },
  { key: 'not_started', label: 'not started', colorClass: 'bg-[#5f6368]' },
  { key: 'skipped', label: 'skipped', colorClass: 'bg-[#5f6368]' },
  { key: 'aborted', label: 'aborted', colorClass: 'bg-[#d946ef]' },
  { key: 'waiting', label: 'waiting', colorClass: 'bg-[#e37400]' },
  { key: 'rejected', label: 'rejected', colorClass: 'bg-[#d93025]' },
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
    iconClass: 'text-[#43a047]',
    message: 'Execution in progress',
  },
  {
    status: Status.Queued,
    icon: Clock,
    iconClass: 'text-[#5f6368]',
    message: 'DAGRun is queued for execution',
  },
  {
    status: Status.Aborted,
    icon: StopCircle,
    iconClass: 'text-[#d946ef]',
    message: 'Execution was aborted',
  },
  {
    status: Status.Waiting,
    icon: Clock,
    iconClass: 'text-[#e37400]',
    message: 'Waiting for approval',
  },
  {
    status: Status.Rejected,
    icon: StopCircle,
    iconClass: 'text-[#d93025]',
    message: 'Execution was rejected',
  },
];

function formatTimestamp(timestamp: string | undefined): string {
  if (!timestamp || timestamp === '-') {
    return '-';
  }
  const parsed = dayjs(timestamp);
  if (!parsed.isValid()) {
    return timestamp;
  }
  return parsed.format('YYYY-MM-DD HH:mm:ss');
}

function truncateId(id: string): string {
  if (id.length <= 16) return id;
  return `${id.slice(0, 8)}...${id.slice(-4)}`;
}

type PreconditionErrorsProps = {
  preconditions?: components['schemas']['Condition'][];
};

function PreconditionErrors({
  preconditions,
}: PreconditionErrorsProps): React.JSX.Element | null {
  const errors = preconditions?.filter((cond) => cond.error);

  if (!errors || errors.length === 0) {
    return null;
  }

  return (
    <div className="pb-2">
      <div className="flex items-center mb-1">
        <Info className="h-3.5 w-3.5 mr-1 text-warning" />
        <span className="text-xs font-semibold text-warning">
          DAGRun Precondition Unmet
        </span>
      </div>
      <div className="space-y-2">
        {errors.map((cond, idx) => (
          <div
            key={idx}
            className="p-1.5 bg-warning-muted border border-warning/20 rounded-md text-xs text-warning font-medium whitespace-normal break-words"
          >
            <div className="mb-0.5 break-words">
              Condition: {cond.condition}
            </div>
            <div className="mb-0.5 break-words">Expected: {cond.expected}</div>
            <div className="break-words">Error: {cond.error}</div>
          </div>
        ))}
      </div>
    </div>
  );
}

function formatDuration(
  startedAt: string | undefined,
  finishedAt: string | undefined
): string {
  if (!startedAt || startedAt === '-') {
    return '-';
  }

  const start = dayjs(startedAt);
  if (!start.isValid()) {
    return '-';
  }

  const end = finishedAt && finishedAt !== '-' ? dayjs(finishedAt) : dayjs();
  if (!end.isValid()) {
    return '-';
  }

  const diff = end.diff(start, 'second');
  if (diff < 0) {
    return '-';
  }

  const hours = Math.floor(diff / 3600);
  const minutes = Math.floor((diff % 3600) / 60);
  const seconds = diff % 60;

  if (hours > 0) {
    return `${hours}h ${minutes}m ${seconds}s`;
  }
  if (minutes > 0) {
    return `${minutes}m ${seconds}s`;
  }
  return `${seconds}s`;
}

function DAGStatusOverview({
  status,
  onViewLog,
}: Props): React.JSX.Element | null {
  const [currentDuration, setCurrentDuration] = useState<string>('-');
  const [copied, setCopied] = useState(false);

  const isRunning = status?.status === Status.Running;

  const calculateDuration = useCallback((): string => {
    return formatDuration(status?.startedAt, status?.finishedAt);
  }, [status?.startedAt, status?.finishedAt]);

  useEffect(() => {
    setCurrentDuration(calculateDuration());

    if (isRunning && status?.startedAt) {
      const intervalId = setInterval(() => {
        setCurrentDuration(calculateDuration());
      }, 1000);
      return () => clearInterval(intervalId);
    }
  }, [isRunning, status?.startedAt, calculateDuration]);

  const nodeStatus = useMemo(
    () =>
      status?.nodes?.reduce<Record<string, number>>((acc, node) => {
        const statusKey = node.statusLabel.toLowerCase().replace(' ', '_');
        acc[statusKey] = (acc[statusKey] || 0) + 1;
        return acc;
      }, {}),
    [status?.nodes]
  );

  const totalNodes = status?.nodes?.length ?? 0;

  const copyRunId = useCallback(async () => {
    if (!status?.dagRunId) return;
    try {
      await navigator.clipboard.writeText(status.dagRunId);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Fallback for older browsers
      const textArea = document.createElement('textarea');
      textArea.value = status.dagRunId;
      document.body.appendChild(textArea);
      textArea.select();
      document.execCommand('copy');
      document.body.removeChild(textArea);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  }, [status?.dagRunId]);

  if (!status) {
    return null;
  }

  return (
    <div className="space-y-2">
      {/* Parameters - Only show when present */}
      {status.params && (
        <div className="flex items-center gap-1.5 text-xs font-mono">
          <Terminal className="h-3 w-3 text-muted-foreground flex-shrink-0" />
          <span className="text-foreground truncate" title={status.params}>
            {status.params}
          </span>
        </div>
      )}

      {/* Timing & Metadata - Compact grid */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
        {status.scheduleTime && (
          <span>
            <span className="text-muted-foreground">Scheduled </span>
            <span className="font-mono text-foreground">
              {formatTimestamp(status.scheduleTime)}
            </span>
          </span>
        )}
        {status.queuedAt && (
          <span>
            <span className="text-muted-foreground">Queued </span>
            <span className="font-mono text-foreground">
              {formatTimestamp(status.queuedAt)}
            </span>
          </span>
        )}
        <span>
          <span className="text-muted-foreground">Started </span>
          <span className="font-mono text-foreground">
            {formatTimestamp(status.startedAt)}
          </span>
        </span>
        <span>
          <span className="text-muted-foreground">Finished </span>
          <span className="font-mono text-foreground">
            {formatTimestamp(status.finishedAt)}
          </span>
        </span>
        <span className="flex items-center gap-1">
          <span className="text-muted-foreground">Duration </span>
          <span className="font-mono font-medium text-foreground">
            {currentDuration}
          </span>
          {isRunning && (
            <span className="inline-block w-1.5 h-1.5 rounded-full bg-success animate-pulse" />
          )}
        </span>
      </div>

      {/* Metadata row */}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
        {status.triggerType && (
          <span>
            <span className="text-muted-foreground">Trigger </span>
            <span className="font-medium text-foreground">
              {triggerTypeLabels[status.triggerType] ?? status.triggerType}
            </span>
          </span>
        )}
        {status.workerId && (
          <span className="truncate max-w-[180px]" title={status.workerId}>
            <span className="text-muted-foreground">Worker </span>
            <span className="font-medium text-foreground">
              {status.workerId}
            </span>
          </span>
        )}
        {status.dagRunId && (
          <button
            onClick={copyRunId}
            className="inline-flex items-center gap-1 font-mono text-muted-foreground hover:text-foreground transition-colors cursor-pointer"
            title={`Click to copy: ${status.dagRunId}`}
          >
            <span>{truncateId(status.dagRunId)}</span>
            {copied ? (
              <Check className="h-3 w-3 text-success" />
            ) : (
              <Copy className="h-3 w-3 opacity-50" />
            )}
          </button>
        )}
        {status.dagRunId && onViewLog && (
          <button
            onClick={() => onViewLog(status.dagRunId)}
            className="ml-auto inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium rounded border border-border bg-card hover:bg-muted transition-colors cursor-pointer"
            title="View Scheduler Log"
          >
            <Terminal className="h-3 w-3" />
            <span>Log</span>
          </button>
        )}
      </div>

      {/* Node Status - Progress bar with inline counts */}
      <div className="flex items-center gap-2 text-xs">
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
                />
              );
            })}
          </div>
        )}
        <span className="text-xs text-muted-foreground tabular-nums">
          {nodeStatus?.succeeded ?? 0}/{totalNodes}
        </span>
      </div>

      {/* Execution status message */}
      {(() => {
        const config = EXECUTION_STATUS_CONFIG.find(
          (c) => c.status === status.status
        );
        if (!config) return null;
        const Icon = config.icon;
        return (
          <div className="flex items-center gap-1 text-xs text-muted-foreground">
            <Icon className={`h-3 w-3 ${config.iconClass}`} />
            <span>{config.message}</span>
          </div>
        );
      })()}

      <PreconditionErrors preconditions={status.preconditions} />
    </div>
  );
}

export default DAGStatusOverview;
