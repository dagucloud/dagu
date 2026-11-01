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
import { useElementWidth } from '@/hooks/useElementWidth';
import { whenEnabled } from '@/hooks/queryUtils';
import dayjs from '@/lib/dayjs';
import { HATCH_PATTERN } from '@/lib/hatchPattern';
import { shortRunId } from '@/lib/shortRunId';
import { Archive, History } from 'lucide-react';
import { formatStepSpan } from '@/lib/formatStepSpan';
import {
  isActiveNodeStatus,
  nodeStatusText,
  runStatusText,
} from '@/lib/status-utils';
import React, {
  forwardRef,
  useEffect,
  useMemo,
  useRef,
  type KeyboardEvent,
} from 'react';
import { components, NodeStatus, Status } from '../../../../api/v1/schema';
import {
  useOpenSubRun,
  type SubRunStackEntry,
} from '../common/SubRunStackModal';
import {
  buildTimelineRows,
  buildTimeMarkers,
  getSubRunQueryContext,
  getTimelineSubRuns,
  hasTimelineSubRuns,
  MIN_BAR_PX,
  minBarMs,
  placeTimelineRows,
  type PlacedRow,
  type TimelineRow,
} from './timelineItems';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';

/** Width in px of the column of step names: the plot starts after it. */
const LABEL_COLUMN_PX = 130;

/**
 * The left part of the axis, before the start of the run: faint diagonal
 * lines that read as time that the run did not spend (the cached time).
 */
const CACHED_ZONE_PATTERN =
  'repeating-linear-gradient(135deg, var(--border) 0 1px, transparent 1px 6px)';

/**
 * Props for the TimelineChart component
 */
type Props = {
  /** DAG run details containing execution information */
  status: components['schemas']['DAGRunDetails'];
  /**
   * Optional opener for child DAG-run drill-down. Defaults to the
   * SubRunOpenProvider context when rendered inside DAG status.
   */
  onOpenSubRun?: (entry: SubRunStackEntry) => void;
};

function getStatusLabel(row: TimelineRow): string {
  if (row.statusSource === 'dagrun') {
    return runStatusText(row.status as Status);
  }
  return nodeStatusText(row.status as NodeStatus);
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
        bg: 'var(--status-neutral)',
        border: 'var(--status-neutral)',
      }
    );
  }

  return (
    statusColors[row.status as NodeStatus] || {
      bg: 'var(--status-neutral)',
      border: 'var(--status-neutral)',
    }
  );
}

/**
 * The real duration of a row, in the one text of the steps. Both ends are
 * real values: the tooltip never shows the drawn interval.
 */
function rowSpan(row: PlacedRow, belowSecond: string): string | null {
  const active = isActiveTimelineStatus(row);
  return formatStepSpan(
    new Date(row.startMs).toISOString(),
    active ? undefined : new Date(row.endMs).toISOString(),
    { live: active, belowSecond, wholeSecond: row.wholeSecond ?? false }
  );
}

/** The time that a ghost step took in its source run, in the text of the steps. */
function ghostDuration(row: TimelineRow, ts: ReturnType<typeof useI18n>['ts']) {
  const ghost = row.ghost;
  if (!ghost) return null;
  return formatStepSpan(
    new Date(0).toISOString(),
    new Date(ghost.durationMs).toISOString(),
    { belowSecond: ts('<1s'), wholeSecond: true }
  );
}

/** The text of a ghost row: the source run and the time that the step took there. */
function ghostLabel(row: TimelineRow, ts: ReturnType<typeof useI18n>['ts']) {
  const ghost = row.ghost;
  if (!ghost) return null;
  return `${ts('run {id}', { id: shortRunId(ghost.runId) })} · ${ghostDuration(row, ts) ?? ''}`;
}

function isActiveTimelineStatus(row: TimelineRow): boolean {
  if (row.statusSource === 'dagrun') {
    return row.status === Status.Running || row.status === Status.Queued;
  }
  return isActiveNodeStatus(row.status as NodeStatus);
}

type TimelineBarProps = {
  item: TimelineRow;
  leftPercent: number;
  widthPercent: number;
  colors: { bg: string; border: string };
  /** A step that did not run: a dotted hairline, not a bar of its length. */
  skipped?: boolean;
  /** A step reused from another run: a hatched bar of its original length. */
  ghost?: boolean;
  openSubRun?: (entry: SubRunStackEntry) => void;
} & React.ComponentPropsWithoutRef<'div'>;

/**
 * Colored execution bar. Openable sub-DAG bars use role="button"
 * (same pattern as AgentTimeline) so positioning stays a div.
 */
const TimelineBar = forwardRef<HTMLDivElement, TimelineBarProps>(
  function TimelineBar(
    {
      item,
      leftPercent,
      widthPercent,
      colors,
      skipped = false,
      ghost = false,
      openSubRun,
      className,
      style,
      onClick,
      onKeyDown,
      ...props
    },
    ref
  ) {
    const canOpen = item.kind === 'subdag' && !!openSubRun && !!item.dagRunId;
    const openName = item.dagName || item.parentStepName || item.label;

    function handleClick(event: React.MouseEvent<HTMLDivElement>) {
      onClick?.(event);
      if (!canOpen || !openSubRun || !item.dagRunId) {
        return;
      }
      openSubRun({
        name: openName,
        dagRunId: item.dagRunId,
      });
    }

    function handleKeyDown(event: KeyboardEvent<HTMLDivElement>) {
      onKeyDown?.(event);
      if (!canOpen || (event.key !== 'Enter' && event.key !== ' ')) {
        return;
      }
      // Prevent Space from scrolling the page when activating the control.
      event.preventDefault();
      if (!openSubRun || !item.dagRunId) {
        return;
      }
      openSubRun({
        name: openName,
        dagRunId: item.dagRunId,
      });
    }

    return (
      <div
        ref={ref}
        data-testid={`timeline-bar-${item.id}`}
        data-ghost={ghost ? 'true' : undefined}
        {...props}
        role={canOpen ? 'button' : undefined}
        tabIndex={canOpen ? 0 : undefined}
        aria-label={
          canOpen && item.dagRunId
            ? `Open ${openName} run ${item.dagRunId}`
            : undefined
        }
        className={`absolute rounded transition-opacity hover:opacity-80 ${
          // The pseudo element widens the 2px hairline to a bar-high target.
          skipped && !ghost
            ? "h-0 border-t-2 border-dotted before:absolute before:inset-x-0 before:-top-2.5 before:h-5 before:content-['']"
            : 'h-5'
        } ${
          canOpen
            ? 'cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary'
            : ''
        } ${className ?? ''}`}
        style={{
          left: `${leftPercent}%`,
          width: `${widthPercent}%`,
          minWidth: `${MIN_BAR_PX}px`,
          ...(skipped && !ghost
            ? { borderTopColor: colors.border }
            : {
                backgroundColor: colors.bg,
                borderLeft: `2px solid ${colors.border}`,
                ...(ghost ? { backgroundImage: HATCH_PATTERN } : {}),
              }),
          ...style,
        }}
        onClick={canOpen ? handleClick : onClick}
        onKeyDown={canOpen ? handleKeyDown : onKeyDown}
      />
    );
  }
);

/**
 * TimelineChart component renders a horizontal bar chart showing step execution
 */
/** The time format of a row: with the date when the row crosses a day. */
function spanFormat(item: { startMs: number; endMs: number }): string {
  return dayjs(item.startMs).isSame(item.endMs, 'day')
    ? 'HH:mm:ss'
    : 'YYYY-MM-DD HH:mm:ss';
}

function TimelineChart({ status, onOpenSubRun }: Props) {
  const { ts } = useI18n();
  const remoteNode = useRemoteNode();
  const openSubRunFromContext = useOpenSubRun();
  const openSubRun = onOpenSubRun ?? openSubRunFromContext;
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

  // The axis drops the labels that do not fit the width of the chart. The
  // frame is there with and without rows, so the measure never misses it.
  const frameRef = useRef<HTMLDivElement>(null);
  const frameWidth = useElementWidth(frameRef);
  const axisWidth =
    frameWidth === null ? null : Math.max(frameWidth - LABEL_COLUMN_PX, 1);

  const { items, timelineStart, timelineEnd, timeMarkers } = useMemo(() => {
    const realRows = buildTimelineRows({
      dagRun: status,
      subRunDetails: subRunsData?.subRuns || [],
      nowMs: Date.now(),
    });

    if (realRows.length === 0) {
      return { items: [], timelineStart: 0, timelineEnd: 0, timeMarkers: [] };
    }

    // Runs that an older version stored have times with a resolution of one
    // second. The bars are drawn from placed intervals: a step starts after
    // its dependencies, and a step of 0 ms is a thin bar. The axis spans the
    // placed intervals.
    const span = (rows: ReadonlyArray<{ a: number; b: number }>) => {
      const min = Math.min(...rows.map((i) => i.a));
      const max = Math.max(...rows.map((i) => i.b));
      const padding = Math.max((max - min) * 0.02, 500);
      return { start: min - padding, end: max + padding };
    };
    const real = span(realRows.map((i) => ({ a: i.startMs, b: i.endMs })));
    const validItems = placeTimelineRows(
      realRows,
      minBarMs(real.end - real.start, axisWidth ?? undefined)
    );
    const { start, end } = span(
      validItems.map((i) => ({ a: i.shownStartMs, b: i.shownEndMs }))
    );

    const markers = buildTimeMarkers(start, end, axisWidth ?? undefined);

    return {
      items: validItems,
      timelineStart: start,
      timelineEnd: end,
      timeMarkers: markers,
    };
  }, [status, subRunsData?.subRuns, axisWidth]);

  // Don't render if there are no items
  if (items.length === 0) {
    return (
      <div ref={frameRef} className="w-full">
        <div className="text-sm text-muted-foreground p-4">
          <I18nText text={'No step execution data available.'} />
        </div>
      </div>
    );
  }

  const totalRange = timelineEnd - timelineStart;
  const plotArea = { left: `${LABEL_COLUMN_PX}px` };
  // The reused steps stand before the start of the run: the part of the axis
  // on the left of that start is the cached time.
  const runStartMs = items.find((item) => item.ghost)?.ghost?.runStartMs;
  const runStartPercent =
    runStartMs === undefined
      ? null
      : ((runStartMs - timelineStart) / totalRange) * 100;
  const cachedZone = runStartPercent !== null && (
    <div
      data-testid="timeline-cached-zone"
      className="pointer-events-none absolute inset-y-0 left-0 border-r border-foreground/40 opacity-60"
      style={{
        width: `${runStartPercent}%`,
        backgroundImage: CACHED_ZONE_PATTERN,
      }}
    />
  );

  return (
    <div
      ref={frameRef}
      className="w-full bg-card rounded-md border border-border overflow-hidden"
    >
      {/* Time axis header */}
      <div className="relative h-6 bg-muted border-b border-border">
        <div className="absolute inset-y-0 right-0" style={plotArea}>
          {timeMarkers.map((marker, idx) => (
            <div
              key={idx}
              className={`absolute top-0 h-full flex items-center ${
                idx === 0
                  ? 'pl-2'
                  : idx === timeMarkers.length - 1
                    ? 'pr-2'
                    : ''
              }`}
              style={{
                left: `${marker.position}%`,
                // The first label starts at the edge and the last one ends at
                // it: centred, they would be cut by the chart.
                transform:
                  idx === 0
                    ? 'none'
                    : idx === timeMarkers.length - 1
                      ? 'translateX(-100%)'
                      : 'translateX(-50%)',
              }}
            >
              <span className="text-xs text-muted-foreground font-mono">
                {marker.label}
              </span>
            </div>
          ))}
        </div>
      </div>

      {/* The start of the run, when reused steps stand before it */}
      {runStartPercent !== null && runStartMs !== undefined && (
        <div className="relative h-5 border-b border-border bg-muted/40">
          <div className="absolute inset-y-0 right-0" style={plotArea}>
            {cachedZone}
            <span
              data-testid="timeline-run-start"
              // Near the right edge, the label stands before the line.
              style={
                runStartPercent > 60
                  ? { right: `calc(${100 - runStartPercent}% + 6px)` }
                  : { left: `calc(${runStartPercent}% + 6px)` }
              }
              className="absolute top-1/2 -translate-y-1/2 whitespace-nowrap font-mono text-[10px] leading-4 text-muted-foreground"
            >
              {ts('Run starts {time}', {
                time: dayjs(runStartMs).format('HH:mm:ss'),
              })}
            </span>
          </div>
        </div>
      )}

      {/* Timeline rows */}
      <div className="relative">
        {/* Grid lines */}
        <div
          className="absolute inset-y-0 right-0 pointer-events-none"
          style={plotArea}
        >
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
            ((item.shownStartMs - timelineStart) / totalRange) * 100;
          const widthPercent =
            ((item.shownEndMs - item.shownStartMs) / totalRange) * 100;
          // A ghost shows the colour of the step that ran in the other run.
          const colors = item.ghost
            ? statusColors[NodeStatus.Success]
            : getStatusColor(item);

          return (
            <div
              key={item.id}
              data-testid="timeline-row"
              data-row-id={item.id}
              className={`relative h-8 flex items-center [contain:layout_paint] ${
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
              <div className="absolute inset-y-0 right-0" style={plotArea}>
                {cachedZone}
                {item.ghost && (
                  <span
                    data-testid="timeline-ghost-label"
                    // A ghost near the right edge has its label on the left of
                    // the bar, so that the edge does not cut it.
                    style={
                      leftPercent > 60
                        ? { right: `calc(${100 - leftPercent}% + 4px)` }
                        : { left: `calc(${leftPercent}% + 6px)` }
                    }
                    className="pointer-events-none absolute top-1/2 z-10 max-w-[70%] -translate-y-1/2 truncate whitespace-nowrap rounded-sm bg-card/85 px-1 font-mono text-[10px] leading-4 text-foreground"
                  >
                    {item.ghost.source === 'cache' ? (
                      <Archive
                        aria-hidden="true"
                        className="mr-1 inline size-3"
                      />
                    ) : (
                      <History
                        aria-hidden="true"
                        className="mr-1 inline size-3"
                      />
                    )}
                    {ghostLabel(item, ts)}
                  </span>
                )}
                <Tooltip>
                  <TooltipTrigger asChild>
                    <TimelineBar
                      item={item}
                      leftPercent={leftPercent}
                      widthPercent={widthPercent}
                      colors={colors}
                      skipped={
                        item.statusSource === 'node' &&
                        item.status === NodeStatus.Skipped
                      }
                      ghost={item.ghost !== undefined}
                      openSubRun={openSubRun ?? undefined}
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
                        <div className="text-xs">
                          <I18nText text={'DAG:'} /> {item.dagName}
                        </div>
                      )}
                      {item.dagRunId && (
                        <div className="text-xs">
                          <I18nText text={'Run ID:'} /> {item.dagRunId}
                        </div>
                      )}
                      {item.params && (
                        <div className="text-xs">
                          <I18nText text={'Params:'} /> {item.params}
                        </div>
                      )}
                      {item.parentStepName && (
                        <div className="text-xs text-muted-foreground">
                          <I18nText text={'Parent:'} /> {item.parentStepName}
                        </div>
                      )}
                      <div className="text-xs">
                        <I18nText text={'Status:'} />{' '}
                        <span className="font-medium">
                          {item.ghost ? (
                            ts('Reused')
                          ) : (
                            <I18nText text={getStatusLabel(item)} />
                          )}
                        </span>
                      </div>
                      {item.ghost && (
                        <div
                          data-testid="timeline-ghost-source"
                          className="space-y-0.5 text-xs"
                        >
                          <div>
                            {ts('Reused from run {id}', {
                              id: item.ghost.runId,
                            })}
                          </div>
                          {item.ghost.startedAt !== null &&
                            item.ghost.finishedAt !== null && (
                              <div className="text-muted-foreground">
                                {ts('In that run: {start} → {finish}', {
                                  start: dayjs(item.ghost.startedAt).format(
                                    'YYYY-MM-DD HH:mm:ss'
                                  ),
                                  finish: dayjs(item.ghost.finishedAt).format(
                                    'HH:mm:ss'
                                  ),
                                })}
                              </div>
                            )}
                        </div>
                      )}
                      <div className="text-xs">
                        <I18nText text={'Duration:'} />{' '}
                        <span className="font-mono">
                          {item.ghost
                            ? ghostDuration(item, ts)
                            : rowSpan(item, ts('<1s'))}
                        </span>
                      </div>
                      {!item.ghost && (
                        <div className="text-xs text-muted-foreground">
                          {/* A span over more than one day names its dates. */}
                          {dayjs(item.startMs).format(spanFormat(item))} →{' '}
                          {dayjs(item.endMs).format(spanFormat(item))}
                        </div>
                      )}
                      {item.shiftedAfter !== undefined && item.wholeSecond && (
                        <div className="text-xs text-muted-foreground">
                          {ts(
                            'Shown after {step} (times are rounded to the second)',
                            {
                              step: item.shiftedAfter,
                            }
                          )}
                        </div>
                      )}
                      {item.error && (
                        <div className="text-xs text-destructive">
                          <I18nText text={'Error:'} /> {item.error}
                        </div>
                      )}
                    </div>
                  </TooltipContent>
                </Tooltip>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

export default TimelineChart;
