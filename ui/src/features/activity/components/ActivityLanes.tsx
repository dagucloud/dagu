// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { Link } from 'react-router-dom';
import { Status } from '@/api/v1/schema';
import { buildDAGRunPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import { useOpenPage } from '@/features/dag-runs/lib/useOpenPage';
import { formatDurationMs } from '@/lib/formatDuration';
import dayjs from '@/lib/dayjs';
import { runStatusText } from '@/lib/status-utils';
import { cn } from '@/lib/utils';
import { useI18n } from '@/i18n/I18nProvider';
import {
  type ActivityLane,
  type ActivityRun,
  barSpan,
  groupLanes,
  oldestStart,
  percentOf,
  runEnd,
  sortLanes,
  type TimeRange,
  timeTicks,
} from '../lib/activityModel';
import { executionsURL } from '../lib/activityLinks';

type Props = {
  /**
   * Loaded runs, newest first, followed by runs still going from before the
   * window.
   */
  runs: ActivityRun[];
  /** Runs in progress, which rank their workflow's lane higher. */
  active: ActivityRun[];
  /** The activity window, which workflow links cover. */
  window: TimeRange;
  /** Set when older runs exist; the time before the loaded runs is shaded. */
  hasMore: boolean;
  range: TimeRange;
  nowSeconds: number;
  remoteNode: string;
  tzOffsetSeconds?: number;
  /** Narrows the window to a range dragged across the lanes. */
  onZoom: (range: TimeRange) => void;
};

const MARK_COLOR: Partial<Record<Status, string>> = {
  [Status.Failed]: 'var(--status-error)',
  [Status.Rejected]: 'var(--status-error)',
  [Status.Aborted]: 'var(--status-aborted)',
  [Status.PartialSuccess]: 'var(--status-warning)',
  [Status.Running]: 'var(--status-running)',
  [Status.Waiting]: 'var(--status-running)',
  [Status.Queued]: 'var(--status-neutral)',
};

const PROBLEM_STATUSES = new Set<Status>([
  Status.Failed,
  Status.Rejected,
  Status.Aborted,
  Status.PartialSuccess,
]);

type MarkSize = { minWidth: number; inset: number };

/**
 * A run mark's narrowest width and its space above and below inside the lane,
 * in pixels. Problem runs draw wider and taller so they stand out.
 */
const MARK: MarkSize = { minWidth: 2, inset: 8 };
const PROBLEM_MARK: MarkSize = { minWidth: 3, inset: 2 };

function markSize(run: ActivityRun): MarkSize {
  return PROBLEM_STATUSES.has(run.status) ? PROBLEM_MARK : MARK;
}

/** Pixels a pointer may travel before a press becomes a zoom drag. */
const DRAG_THRESHOLD_PX = 6;
/** How close, in pixels, a pointer must be to a run to pick it. */
const MARK_HIT_PX = 6;
/** Tick labels closer than this to an axis edge would be clipped. */
const TICK_EDGE_PERCENT = 3;

type Hover = {
  lane: string;
  clientX: number;
  clientY: number;
  run?: ActivityRun;
};

type Drag = { lane: string; startX: number; currentX: number; rect: DOMRect };

// A pointer inside a drawn mark picks it, the topmost one where marks
// overlap. Runs come oldest first, so later runs are drawn on top and win
// ties. Elsewhere in the lane, the nearest mark by x within MARK_HIT_PX wins.
function runAt(
  runs: ActivityRun[],
  range: TimeRange,
  nowSeconds: number,
  rect: DOMRect,
  clientX: number,
  clientY: number
): ActivityRun | undefined {
  let inside: ActivityRun | undefined;
  let nearest: ActivityRun | undefined;
  let nearestDistance = MARK_HIT_PX;
  for (const run of runs) {
    const { left, right } = barSpan(run, range, nowSeconds);
    const { minWidth, inset } = markSize(run);
    const x0 = rect.left + (left / 100) * rect.width;
    const x1 = Math.max(rect.left + (right / 100) * rect.width, x0 + minWidth);
    const distance =
      clientX < x0 ? x0 - clientX : clientX > x1 ? clientX - x1 : 0;
    if (
      distance === 0 &&
      clientY >= rect.top + inset &&
      clientY <= rect.bottom - inset
    ) {
      inside = run;
    }
    if (distance <= nearestDistance) {
      nearest = run;
      nearestDistance = distance;
    }
  }
  return inside ?? nearest;
}

function timeAt(range: TimeRange, rect: DOMRect, clientX: number): number {
  const ratio = Math.min(1, Math.max(0, (clientX - rect.left) / rect.width));
  return range.from + ratio * (range.to - range.from);
}

/** One row per workflow with a mark for each loaded run. */
export function ActivityLanes({
  runs,
  active,
  window: activityWindow,
  hasMore,
  range,
  nowSeconds,
  remoteNode,
  tzOffsetSeconds = 0,
  onZoom,
}: Props): React.ReactElement {
  const { ts } = useI18n();
  const openPage = useOpenPage();
  const axisRef = React.useRef<HTMLDivElement>(null);
  const [axisWidth, setAxisWidth] = React.useState(0);
  const [hover, setHover] = React.useState<Hover | null>(null);
  const [drag, setDrag] = React.useState<Drag | null>(null);
  const [focusedRun, setFocusedRun] = React.useState<{
    lane: string;
    index: number;
  } | null>(null);
  const lanes = React.useMemo(
    () => sortLanes(groupLanes(runs), active),
    [runs, active]
  );
  // Older pages hold only runs created in the window, so runs still going
  // from before it do not move the unloaded boundary.
  const loadedFrom = hasMore
    ? oldestStart(
        runs.filter((run) => (run.startedAt ?? 0) >= activityWindow.from)
      )
    : undefined;
  const unloadedPercent =
    loadedFrom === undefined
      ? 0
      : Math.min(100, Math.max(0, percentOf(range, loadedFrom)));

  React.useLayoutEffect(() => {
    const element = axisRef.current;
    if (!element) return;
    const update = () => setAxisWidth(element.clientWidth);
    update();
    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(update);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  const ticks = timeTicks(range, axisWidth, tzOffsetSeconds).filter((tick) => {
    const percent = percentOf(range, tick);
    return percent >= TICK_EDGE_PERCENT && percent <= 100 - TICK_EDGE_PERCENT;
  });
  const formatTime = (seconds: number) =>
    dayjs
      .unix(seconds)
      .utcOffset(tzOffsetSeconds / 60)
      .format('HH:mm');
  const nowPercent = percentOf(range, nowSeconds);

  // A plain open goes to the run on its workflow page; a modifier opens a new tab.
  const openRun = (name: string, run: ActivityRun, newTab: boolean) => {
    openPage(
      buildDAGRunPageURL({
        rootDAGRunName: name,
        rootDAGRunId: run.dagRunId,
        remoteNode,
      }),
      { ctrlKey: newTab }
    );
  };

  const sortedRuns = (workflow: ActivityLane) =>
    workflow.runs
      .filter(
        (run) =>
          run.startedAt !== undefined &&
          run.startedAt <= range.to &&
          runEnd(run, nowSeconds) >= range.from
      )
      .sort((left, right) => (left.startedAt ?? 0) - (right.startedAt ?? 0));

  const handlePointerMove = (
    workflow: ActivityLane,
    event: React.PointerEvent<HTMLDivElement>
  ) => {
    const rect = event.currentTarget.getBoundingClientRect();
    if (drag && drag.lane === workflow.name) {
      setDrag({ ...drag, currentX: event.clientX });
      return;
    }
    setHover({
      lane: workflow.name,
      clientX: event.clientX,
      clientY: rect.top,
      run: runAt(
        sortedRuns(workflow),
        range,
        nowSeconds,
        rect,
        event.clientX,
        event.clientY
      ),
    });
  };

  const handlePointerUp = (
    workflow: ActivityLane,
    event: React.PointerEvent<HTMLDivElement>
  ) => {
    const current = drag;
    setDrag(null);
    if (!current) return;
    if (Math.abs(current.currentX - current.startX) >= DRAG_THRESHOLD_PX) {
      const a = timeAt(range, current.rect, current.startX);
      const b = timeAt(range, current.rect, current.currentX);
      onZoom({
        from: Math.floor(Math.min(a, b)),
        to: Math.ceil(Math.max(a, b)),
      });
      return;
    }
    const rect = event.currentTarget.getBoundingClientRect();
    const run = runAt(
      sortedRuns(workflow),
      range,
      nowSeconds,
      rect,
      event.clientX,
      event.clientY
    );
    if (run) {
      openRun(workflow.name, run, event.metaKey || event.ctrlKey);
    }
  };

  const handleKeyDown = (
    workflow: ActivityLane,
    event: React.KeyboardEvent<HTMLDivElement>
  ) => {
    const laneRuns = sortedRuns(workflow);
    if (laneRuns.length === 0) return;
    const current =
      focusedRun?.lane === workflow.name ? focusedRun.index : laneRuns.length;
    let next = current;
    if (event.key === 'ArrowRight')
      next = Math.min(laneRuns.length - 1, current + 1);
    else if (event.key === 'ArrowLeft') next = Math.max(0, current - 1);
    else if (event.key === 'End') next = laneRuns.length - 1;
    else if (event.key === 'Home') next = 0;
    else if (event.key === 'Enter' && focusedRun?.lane === workflow.name) {
      const run = laneRuns[current];
      if (run) openRun(workflow.name, run, event.metaKey || event.ctrlKey);
      return;
    } else if (event.key === 'Escape') {
      setFocusedRun(null);
      setHover(null);
      return;
    } else return;
    event.preventDefault();
    if (next === current && focusedRun?.lane === workflow.name) return;
    const run = laneRuns[next]!;
    const rect = event.currentTarget.getBoundingClientRect();
    setFocusedRun({ lane: workflow.name, index: next });
    setHover({
      lane: workflow.name,
      clientX:
        rect.left + (barSpan(run, range, nowSeconds).left / 100) * rect.width,
      clientY: rect.top,
      run,
    });
  };

  // A running run shows how long it has run so far.
  const hoverSeconds =
    hover?.run?.startedAt !== undefined
      ? runEnd(hover.run, nowSeconds) - hover.run.startedAt
      : 0;

  const tooltip = hover ? (
    <div
      role="tooltip"
      className="pointer-events-none fixed z-50 -translate-x-1/2 -translate-y-full rounded-md border border-border bg-popover px-2.5 py-1.5 text-xs text-popover-foreground shadow-md"
      style={{ left: hover.clientX, top: hover.clientY - 6 }}
    >
      <div className="font-medium">{hover.lane}</div>
      {hover.run ? (
        <div className="text-muted-foreground tabular-nums">
          {ts(runStatusText(hover.run.status))} ·{' '}
          {formatTime(hover.run.startedAt ?? 0)}
          {hoverSeconds > 0
            ? ` · ${formatDurationMs(hoverSeconds * 1000)}`
            : ''}
        </div>
      ) : null}
    </div>
  ) : null;

  return (
    <div className="min-w-0">
      <div className="grid grid-cols-1 border-b border-border sm:grid-cols-[minmax(7rem,13rem)_1fr]">
        <div className="hidden sm:block" />
        <div
          ref={axisRef}
          className="relative h-6 overflow-hidden text-[11px] text-muted-foreground tabular-nums"
          aria-hidden="true"
        >
          {ticks.map((tick) => (
            <span
              key={tick}
              className="absolute top-1 -translate-x-1/2 whitespace-nowrap"
              style={{ left: `${percentOf(range, tick)}%` }}
            >
              {formatTime(tick)}
            </span>
          ))}
        </div>
      </div>
      <ul aria-label={ts('Runs by workflow')}>
        {lanes.map((workflow) => {
          const visibleRuns = sortedRuns(workflow);
          const focused =
            focusedRun?.lane === workflow.name
              ? visibleRuns[focusedRun.index]
              : undefined;
          const dragRect =
            drag?.lane === workflow.name
              ? {
                  left: Math.min(drag.startX, drag.currentX) - drag.rect.left,
                  width: Math.abs(drag.currentX - drag.startX),
                }
              : null;
          return (
            <li
              key={workflow.name}
              className="grid grid-cols-1 items-stretch border-b border-border/60 last:border-b-0 sm:grid-cols-[minmax(7rem,13rem)_1fr]"
              style={{
                contentVisibility: 'auto',
                containIntrinsicSize: 'auto 2.25rem',
              }}
            >
              <div className="flex min-w-0 items-center gap-2 pt-1.5 pr-3 sm:py-1.5">
                <Link
                  to={executionsURL({
                    name: workflow.name,
                    ...activityWindow,
                  })}
                  className="truncate text-sm font-medium text-foreground hover:underline"
                  title={workflow.name}
                >
                  {workflow.name}
                </Link>
                {workflow.failedCount > 0 ? (
                  <span className="shrink-0 text-xs font-medium text-error tabular-nums">
                    {ts('{failed} of {count} failed', {
                      failed: workflow.failedCount,
                      count: workflow.runs.length,
                    })}
                  </span>
                ) : (
                  <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
                    {ts(
                      workflow.runs.length === 1
                        ? '{count} run'
                        : '{count} runs',
                      { count: workflow.runs.length }
                    )}
                  </span>
                )}
              </div>
              <div
                role="group"
                tabIndex={0}
                aria-label={ts(
                  '{name}: {count} runs. Use the arrow keys to move between runs.',
                  { name: workflow.name, count: workflow.runs.length }
                )}
                className="relative h-9 cursor-pointer touch-pan-y select-none outline-none focus-visible:ring-1 focus-visible:ring-ring"
                onPointerDown={(event) => {
                  if (event.button !== 0) return;
                  event.currentTarget.setPointerCapture?.(event.pointerId);
                  setDrag({
                    lane: workflow.name,
                    startX: event.clientX,
                    currentX: event.clientX,
                    rect: event.currentTarget.getBoundingClientRect(),
                  });
                }}
                onPointerMove={(event) => handlePointerMove(workflow, event)}
                onPointerUp={(event) => handlePointerUp(workflow, event)}
                onPointerCancel={() => setDrag(null)}
                onPointerLeave={() => {
                  if (!drag) setHover(null);
                }}
                onKeyDown={(event) => handleKeyDown(workflow, event)}
                onBlur={() => {
                  setFocusedRun(null);
                  setHover(null);
                }}
              >
                {unloadedPercent > 0 && (
                  <span
                    aria-hidden="true"
                    data-testid="unloaded-runs"
                    className="absolute inset-y-0 left-0 bg-muted/60"
                    style={{ width: `${unloadedPercent}%` }}
                  />
                )}
                {visibleRuns.map((run) => {
                  const { left, right } = barSpan(run, range, nowSeconds);
                  const { minWidth, inset } = markSize(run);
                  return (
                    <span
                      key={run.dagRunId}
                      aria-hidden="true"
                      className={cn(
                        // The card-coloured ring leaves a gap between
                        // back-to-back runs.
                        'absolute rounded-sm ring-1 ring-card',
                        focused === run &&
                          'ring-2 ring-ring ring-offset-1 ring-offset-background'
                      )}
                      style={{
                        left: `${left}%`,
                        width: `${Math.max(0, right - left)}%`,
                        minWidth,
                        top: inset,
                        bottom: inset,
                        backgroundColor:
                          MARK_COLOR[run.status] ?? 'var(--status-success)',
                      }}
                    />
                  );
                })}
                {nowPercent >= 0 && nowPercent <= 100 && (
                  <span
                    aria-hidden="true"
                    className="absolute inset-y-0 w-px bg-primary/60"
                    style={{ left: `${nowPercent}%` }}
                  />
                )}
                {dragRect && dragRect.width >= DRAG_THRESHOLD_PX && (
                  <span
                    aria-hidden="true"
                    className="absolute inset-y-0 bg-primary/15 ring-1 ring-primary/40"
                    style={{ left: dragRect.left, width: dragRect.width }}
                  />
                )}
              </div>
            </li>
          );
        })}
      </ul>
      {tooltip}
    </div>
  );
}
