// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * TimelineChart component visualizes the execution timeline of a DAG run.
 *
 * Features:
 * - Clean horizontal bar chart showing step execution
 * - Status-based color coding for each step
 * - Tooltips with step details on hover
 * - Works for both running and completed DAGs
 *
 * @module features/dags/components/visualization
 */
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import dayjs from '@/lib/dayjs';
import { isActiveNodeStatus } from '@/lib/status-utils';
import { useEffect, useMemo } from 'react';
import { components, NodeStatus, Status } from '../../../../api/v1/schema';
import {
  buildTimelineRows,
  getSubRunQueryContext,
  getTimelineSubRuns,
  hasTimelineSubRuns,
  TimelineRow,
} from './timelineItems';

/**
 * Props for the TimelineChart component
 */
type Props = {
  /** DAG run details containing execution information */
  status: components['schemas']['DAGRunDetails'];
};

/** Format for displaying timestamps in tooltips */
const timeFormat = 'HH:mm:ss';

/**
 * Get status label for display
 */
function getNodeStatusLabel(status: NodeStatus): string {
  switch (status) {
    case NodeStatus.NotStarted:
      return 'Not Started';
    case NodeStatus.Running:
      return 'Running';
    case NodeStatus.Retrying:
      return 'Retrying';
    case NodeStatus.Success:
      return 'Success';
    case NodeStatus.Failed:
      return 'Failed';
    case NodeStatus.Aborted:
      return 'Aborted';
    case NodeStatus.Skipped:
      return 'Skipped';
    case NodeStatus.PartialSuccess:
      return 'Partial Success';
    case NodeStatus.Waiting:
      return 'Waiting';
    case NodeStatus.Rejected:
      return 'Rejected';
    default:
      return 'Unknown';
  }
}

/**
 * Get DAG-run status label for display
 */
function getDAGRunStatusLabel(status: Status): string {
  switch (status) {
    case Status.NotStarted:
      return 'Not Started';
    case Status.Running:
      return 'Running';
    case Status.Success:
      return 'Success';
    case Status.Failed:
      return 'Failed';
    case Status.Aborted:
      return 'Aborted';
    case Status.Queued:
      return 'Queued';
    case Status.PartialSuccess:
      return 'Partial Success';
    case Status.Waiting:
      return 'Waiting';
    case Status.Rejected:
      return 'Rejected';
    default:
      return 'Unknown';
  }
}

function getStatusLabel(row: TimelineRow): string {
  if (row.statusSource === 'dagrun') {
    return getDAGRunStatusLabel(row.status as Status);
  }
  return getNodeStatusLabel(row.status as NodeStatus);
}

/**
 * Calculate duration between two timestamps
 */
function calculateDuration(startMs: number, endMs: number): string {
  const durationMs = endMs - startMs;

  if (durationMs < 1000) {
    return `${durationMs}ms`;
  } else if (durationMs < 60000) {
    return `${(durationMs / 1000).toFixed(1)}s`;
  } else if (durationMs < 3600000) {
    const minutes = Math.floor(durationMs / 60000);
    const seconds = Math.floor((durationMs % 60000) / 1000);
    return `${minutes}m ${seconds}s`;
  } else {
    const hours = Math.floor(durationMs / 3600000);
    const minutes = Math.floor((durationMs % 3600000) / 60000);
    return `${hours}h ${minutes}m`;
  }
}

/**
 * Unified status colors matching Graph.tsx and global.css
 */
const statusColors: Record<NodeStatus, { bg: string; border: string }> = {
  [NodeStatus.NotStarted]: {
    bg: 'var(--status-neutral)',
    border: 'var(--status-neutral)',
  },
  [NodeStatus.Running]: {
    bg: 'var(--status-running)',
    border: 'var(--status-running)',
  },
  [NodeStatus.Retrying]: {
    bg: 'var(--status-warning)',
    border: 'var(--status-warning)',
  },
  [NodeStatus.Failed]: {
    bg: 'var(--status-error)',
    border: 'var(--status-error)',
  },
  [NodeStatus.Aborted]: {
    bg: 'var(--status-aborted)',
    border: 'var(--status-aborted)',
  },
  [NodeStatus.Success]: {
    bg: 'var(--status-success)',
    border: 'var(--status-success)',
  },
  [NodeStatus.Skipped]: {
    bg: 'var(--status-neutral)',
    border: 'var(--status-neutral)',
  },
  [NodeStatus.PartialSuccess]: {
    bg: 'var(--status-warning)',
    border: 'var(--status-warning)',
  },
  [NodeStatus.Waiting]: {
    bg: 'var(--status-warning)',
    border: 'var(--status-warning)',
  },
  [NodeStatus.Rejected]: {
    bg: 'var(--status-error)',
    border: 'var(--status-error)',
  },
};

const dagRunStatusColors: Record<Status, { bg: string; border: string }> = {
  [Status.NotStarted]: {
    bg: 'var(--status-neutral)',
    border: 'var(--status-neutral)',
  },
  [Status.Running]: {
    bg: 'var(--status-running)',
    border: 'var(--status-running)',
  },
  [Status.Failed]: {
    bg: 'var(--status-error)',
    border: 'var(--status-error)',
  },
  [Status.Aborted]: {
    bg: 'var(--status-aborted)',
    border: 'var(--status-aborted)',
  },
  [Status.Success]: {
    bg: 'var(--status-success)',
    border: 'var(--status-success)',
  },
  [Status.Queued]: {
    bg: 'var(--status-neutral)',
    border: 'var(--status-neutral)',
  },
  [Status.PartialSuccess]: {
    bg: 'var(--status-warning)',
    border: 'var(--status-warning)',
  },
  [Status.Waiting]: {
    bg: 'var(--status-warning)',
    border: 'var(--status-warning)',
  },
  [Status.Rejected]: {
    bg: 'var(--status-error)',
    border: 'var(--status-error)',
  },
};

/**
 * Get color for a node status
 */
function getStatusColor(row: TimelineRow): { bg: string; border: string } {
  if (row.statusSource === 'dagrun') {
    return (
      dagRunStatusColors[row.status as Status] || {
        bg: '#6b7280',
        border: '#6b7280',
      }
    );
  }

  return (
    statusColors[row.status as NodeStatus] || {
      bg: '#6b7280',
      border: '#6b7280',
    }
  );
}

function isActiveTimelineStatus(row: TimelineRow): boolean {
  if (row.statusSource === 'dagrun') {
    return row.status === Status.Running || row.status === Status.Queued;
  }
  return isActiveNodeStatus(row.status as NodeStatus);
}

/**
 * TimelineChart component renders a horizontal bar chart showing step execution
 */
function TimelineChart({ status }: Props) {
  const remoteNode = useRemoteNode();
  const shouldFetchSubRuns = hasTimelineSubRuns(status);
  const queryContext = getSubRunQueryContext(status);
  const eligibleSubRunIdsKey = useMemo(
    () =>
      (status.nodes || [])
        .flatMap((node) => getTimelineSubRuns(node).map((sr) => sr.dagRunId))
        .join('|'),
    [status.nodes]
  );
  const { data: subRunsData, mutate: refetchSubRuns } = useQuery(
    '/dag-runs/{name}/{dagRunId}/sub-dag-runs',
    whenEnabled(shouldFetchSubRuns, {
      params: {
        path: {
          name: queryContext.rootDagName,
          dagRunId: queryContext.rootDagRunId,
        },
        query: {
          remoteNode,
          parentSubDAGRunId: queryContext.parentSubDAGRunId,
        },
      },
    }),
    {
      refreshInterval:
        shouldFetchSubRuns &&
        (status.status === Status.Running || status.status === Status.Queued)
          ? 3000
          : 0,
    }
  );

  useEffect(() => {
    if (!shouldFetchSubRuns) {
      return;
    }
    refetchSubRuns();
  }, [shouldFetchSubRuns, eligibleSubRunIdsKey, refetchSubRuns]);

  const { items, timelineStart, timelineEnd, timeMarkers } = useMemo(() => {
    const validItems = buildTimelineRows({
      dagRun: status,
      subRunDetails: subRunsData?.subRuns || [],
      nowMs: Date.now(),
    });

    if (validItems.length === 0) {
      return { items: [], timelineStart: 0, timelineEnd: 0, timeMarkers: [] };
    }

    // Calculate timeline bounds
    const minStart = Math.min(...validItems.map((i) => i.startMs));
    const maxEnd = Math.max(...validItems.map((i) => i.endMs));
    const range = maxEnd - minStart;
    const padding = Math.max(range * 0.02, 500);

    const start = minStart - padding;
    const end = maxEnd + padding;

    // Generate time markers
    const totalRange = end - start;
    const markerCount = 5;
    const markers: { position: number; label: string }[] = [];

    for (let i = 0; i <= markerCount; i++) {
      const time = start + (totalRange * i) / markerCount;
      markers.push({
        position: (i / markerCount) * 100,
        label: dayjs(time).format(timeFormat),
      });
    }

    return {
      items: validItems,
      timelineStart: start,
      timelineEnd: end,
      timeMarkers: markers,
    };
  }, [status, subRunsData?.subRuns, timeFormat]);

  // Don't render if there are no items
  if (items.length === 0) {
    return (
      <div className="text-sm text-muted-foreground p-4">
        No step execution data available.
      </div>
    );
  }

  const totalRange = timelineEnd - timelineStart;

  return (
    <div className="w-full bg-card rounded-md border border-border overflow-hidden">
      {/* Time axis header */}
      <div className="relative h-6 bg-muted border-b border-border">
        {timeMarkers.map((marker, idx) => (
          <div
            key={idx}
            className="absolute top-0 h-full flex items-center"
            style={{
              left: `${marker.position}%`,
              transform: 'translateX(-50%)',
            }}
          >
            <span className="text-xs text-muted-foreground font-mono">
              {marker.label}
            </span>
          </div>
        ))}
      </div>

      {/* Timeline rows */}
      <div className="relative">
        {/* Grid lines */}
        <div className="absolute inset-0 pointer-events-none">
          {timeMarkers.map((marker, idx) => (
            <div
              key={idx}
              className="absolute top-0 bottom-0 border-l border-border/30"
              style={{ left: `${marker.position}%` }}
            />
          ))}
        </div>

        {/* Step rows */}
        {items.map((item, idx) => {
          const leftPercent =
            ((item.startMs - timelineStart) / totalRange) * 100;
          const widthPercent = ((item.endMs - item.startMs) / totalRange) * 100;
          const colors = getStatusColor(item);
          const isActive = isActiveTimelineStatus(item);

          return (
            <div
              key={item.id}
              data-testid="timeline-row"
              data-row-id={item.id}
              className={`relative h-8 flex items-center ${
                idx % 2 === 0 ? 'bg-background' : 'bg-muted/20'
              }`}
            >
              {/* Step name label */}
              <div
                className="absolute z-10 text-xs font-medium text-foreground truncate max-w-[120px]"
                style={{ left: item.depth === 0 ? '0.5rem' : '1.5rem' }}
              >
                {item.label}
              </div>

              {/* Timeline bar */}
              <Tooltip>
                <TooltipTrigger asChild>
                  <div
                    data-testid={`timeline-bar-${item.id}`}
                    className={`absolute h-5 rounded cursor-pointer transition-opacity hover:opacity-80 ${
                      isActive ? 'animate-pulse' : ''
                    }`}
                    style={{
                      left: `calc(${leftPercent}% + 130px)`,
                      width: `calc(${Math.max(widthPercent, 0.5)}% - 130px)`,
                      minWidth: '4px',
                      backgroundColor: colors.bg,
                      borderLeft: `2px solid ${colors.border}`,
                    }}
                  />
                </TooltipTrigger>
                <TooltipContent side="top" className="max-w-xs">
                  <div className="space-y-1">
                    <div className="font-semibold">
                      {item.kind === 'subdag'
                        ? `${item.parentStepName} ${item.label}`
                        : item.label}
                    </div>
                    {item.description && (
                      <div className="text-xs text-muted-foreground">
                        {item.description}
                      </div>
                    )}
                    {item.dagName && (
                      <div className="text-xs">DAG: {item.dagName}</div>
                    )}
                    {item.dagRunId && (
                      <div className="text-xs">Run ID: {item.dagRunId}</div>
                    )}
                    {item.params && (
                      <div className="text-xs">Params: {item.params}</div>
                    )}
                    {item.parentStepName && (
                      <div className="text-xs text-muted-foreground">
                        Parent: {item.parentStepName}
                      </div>
                    )}
                    <div className="text-xs">
                      Status:{' '}
                      <span className="font-medium">
                        {getStatusLabel(item)}
                      </span>
                    </div>
                    <div className="text-xs">
                      Duration:{' '}
                      <span className="font-mono">
                        {calculateDuration(item.startMs, item.endMs)}
                      </span>
                    </div>
                    <div className="text-xs text-muted-foreground">
                      {dayjs(item.startMs).format('HH:mm:ss')} →{' '}
                      {dayjs(item.endMs).format('HH:mm:ss')}
                    </div>
                    {item.error && (
                      <div className="text-xs text-destructive">
                        Error: {item.error}
                      </div>
                    )}
                  </div>
                </TooltipContent>
              </Tooltip>
            </div>
          );
        })}
      </div>
    </div>
  );
}

export default TimelineChart;
