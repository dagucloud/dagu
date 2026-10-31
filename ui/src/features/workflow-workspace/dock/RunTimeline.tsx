// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronLeft, FlaskConical, Hourglass, Play } from 'lucide-react';
import {
  memo,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from 'react';
import { createPortal } from 'react-dom';
import { Status, type TriggerType } from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { triggerTypeLabels } from '@/features/dags/components/common/TriggerTypeIndicator';
import { useClock } from '@/hooks/useClock';
import { useElementWidth } from '@/hooks/useElementWidth';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime, formatElapsed } from '@/lib/dagRunTiming';
import dayjs from '@/lib/dayjs';
import { shortRunId } from '@/lib/shortRunId';
import {
  hasContinuousStep,
  runDisplayStatus,
  summaryDisplayStatus,
} from '@/lib/runDisplayStatus';
import { runStatusText } from '@/lib/status-utils';
import { toneClasses } from '@/lib/statusPalette';
import { cn } from '@/lib/utils';
import { useRunStart } from '../bar/RunStartContext';
import type { ApiDAGRunDetails } from '../contracts';
import { useWorkspaceData } from '../data/context';
import { useWorkspaceRun } from '../data';
import {
  RUN_COUNT_KEY,
  SHOW_TESTS_KEY,
  useRunViewSettings,
  useVisibleRuns,
} from '../data/useVisibleRuns';
import { useDock } from './DockContext';
import { DockHeaderToolsContext } from './DockHeaderTools';
import { propsEqual } from './propsEqual';
import { RunTimelineControls } from './RunTimelineControls';
import {
  EXTENT_PX,
  isLongRun,
  LANE_GAP_PX,
  LANE_PX,
  laneBlockHeight,
  MAX_LANES,
} from './runExtents';
import {
  axisClock,
  barHeights,
  barTone,
  BAR_MAX_PX,
  BAR_PX,
  cachedBarPx,
  cachedMsOf,
  countOptions,
  DEFAULT_WIDTH_PX,
  durationAxis,
  durationParts,
  elapsedMsText,
  HISTORY_LIMIT,
  fillBackground,
  isTestRun,
  longRunMedianMs,
  runDurationMs,
  runTime,
  timelineLayout,
  timelineWindow,
  TEST_RUN_HATCH,
  totalDurationMs,
  Y_AXIS_PX,
  type DurationParts,
  type DurationTick,
  type LabelledTick,
  type RunRange,
  type TimelineExtent,
} from './runTimelineModel';

export { RUN_COUNT_KEY, SHOW_TESTS_KEY };

/**
 * The layers of the cached part of a bar or an extent: the page colour at
 * half opacity over the status colour (the same colour, paler), and the hatch
 * of the test runs over it.
 */
export const CACHED_WASH =
  'color-mix(in srgb, var(--background) 55%, transparent)';

// The plot starts below this room; the extents and the axis take the rows
// under the plot.
const PLOT_TOP = 8;
const BASELINE = PLOT_TOP + BAR_MAX_PX;
const TICK_LEN = 4;
// Two lines of labels: the time, and the date under it.
const LABELS_HEIGHT = 3 + 13 + 14;
// The room between the baseline, the extents and the axis line.
const EXTENT_GAP = 3;
// Below this width only the extent of the selected run is drawn.
const NARROW_PX = 480;
// The rows of the pitch of one lane: the lane and the space under it.
const LANE_PITCH = LANE_PX + LANE_GAP_PX;

/**
 * The rows of the track. The block of extents has a fixed height (the lane
 * cap), so that the track keeps its size whatever the overlaps.
 */
function trackMetrics(narrow: boolean) {
  const lanes = narrow ? 1 : MAX_LANES;
  const extentTop = BASELINE + EXTENT_GAP;
  const extentHeight = laneBlockHeight(lanes);
  const axisY = extentTop + extentHeight + EXTENT_GAP;
  return {
    extentTop,
    extentHeight,
    axisY,
    height: axisY + TICK_LEN + LABELS_HEIGHT,
  };
}
// Heights of the bars of the loading skeleton.
const SKELETON_HEIGHTS = [
  18, 30, 12, 44, 22, 36, 14, 50, 26, 20, 40, 16, 32, 24, 46, 10, 34, 28, 42,
  18, 38, 12, 30, 22, 48, 16, 36, 26, 20, 32,
];

/**
 * The time that a bar, an extent or a cursor reads: the fixed time of the
 * host when it has one, else the clock of 1 s while its run runs and the view
 * shows (`tick`). Only the item of a running run renders each second.
 */
function useRunNow(
  run: Pick<ApiDAGRunDetails, 'status'>,
  tick: boolean,
  fixed: number | undefined
): number {
  const second = useClock(
    fixed === undefined && tick && run.status === Status.Running ? 1000 : null,
    (now) => Math.floor(now / 1000)
  );
  return fixed ?? second * 1000;
}

/** The duration of a run as text, or null when the run has none. */
function durationText(run: ApiDAGRunDetails, now: number): string | null {
  if (runDurationMs(run, now) === null) return null;
  return formatElapsed(run.startedAt, run.finishedAt, { live: true, now });
}

/**
 * The duration of a run as text: its own time, or its total time when it
 * has cached time that the timeline counts.
 */
function shownDurationText(
  run: ApiDAGRunDetails,
  parts: DurationParts | null,
  now: number
): string | null {
  if (parts && parts.cached > 0) return elapsedMsText(parts.total);
  return durationText(run, now);
}

type Translate = ReturnType<typeof useI18n>['ts'];

/** The status text of a run that the bars print. */
type StatusTextOf = (run: ApiDAGRunDetails) => string;

const normalStatusText: StatusTextOf = (run) => runStatusText(run.status);

/** The status text of a run, the same in the bar, the extent and the tooltip. */
function runStatusLabel(statusText: string, ts: Translate): string {
  return ts(statusText);
}

function BarTooltip({
  run,
  statusText,
  duration,
  parts,
  extent,
}: {
  run: ApiDAGRunDetails;
  statusText: string;
  duration: string | null;
  /** The own, cached and total time, when the cached time counts. */
  parts?: DurationParts | null;
  extent?: TimelineExtent;
}) {
  const { ts } = useI18n();
  const at = formatAbsoluteTime(runTime(run));
  const queued = run.status === Status.Queued && !run.startedAt;
  return (
    <div className="space-y-0.5" data-testid="ww-run-timeline-tooltip">
      {isTestRun(run) && (
        <div
          data-testid="ww-run-timeline-tooltip-test"
          className="flex items-center gap-1 font-semibold"
        >
          <FlaskConical aria-hidden="true" className="size-3 shrink-0" />
          {ts('Test run, not kept')}
        </div>
      )}
      {at && (
        <div className="font-semibold tabular-nums">
          {queued ? `${ts('Queued')} ` : ''}
          {at}
        </div>
      )}
      <div>{runStatusLabel(statusText, ts)}</div>
      {extent && (
        <div>
          {extent.running
            ? ts('Still running')
            : ts('Ended {time}', {
                time: formatAbsoluteTime(run.finishedAt) ?? '-',
              })}
        </div>
      )}
      {parts && parts.cached > 0 ? (
        <div data-testid="ww-run-timeline-tooltip-total">
          {ts('Total {total} = own {own} + cached {cached}', {
            total: elapsedMsText(parts.total),
            own: elapsedMsText(parts.own),
            cached: elapsedMsText(parts.cached),
          })}
        </div>
      ) : (
        duration && (
          <div>
            {ts('Duration')}:{' '}
            <span className="font-mono tabular-nums">{duration}</span>
          </div>
        )
      )}
      {extent && extent.overlaps.length > 0 && (
        <div>
          {ts(
            extent.overlaps.length === 1
              ? 'overlaps {n} other run'
              : 'overlaps {n} other runs',
            { n: extent.overlaps.length }
          )}
        </div>
      )}
      {extent?.clippedLeft && <div>{ts('Started before this view')}</div>}
      <div className="font-mono text-muted-foreground" title={run.dagRunId}>
        {shortRunId(run.dagRunId)}
      </div>
      {run.triggerType && (
        <div>
          {ts(triggerTypeLabels[run.triggerType] ?? run.triggerType)}
          {run.triggerActor && (
            <span className="font-mono text-muted-foreground">
              {' '}
              ({run.triggerActor})
            </span>
          )}
        </div>
      )}
    </div>
  );
}

const Bar = memo(function Bar({
  run,
  statusText,
  extent,
  height,
  counted,
  fixedNow,
  tick,
  selected,
  focusable,
  newest,
  hit,
  offset,
  onHover,
  onSelect,
}: {
  run: ApiDAGRunDetails;
  statusText: string;
  extent?: TimelineExtent;
  height: number;
  /** True when the cached time counts in the duration. */
  counted: boolean;
  /** The time of the host, or undefined to read the clock. */
  fixedNow: number | undefined;
  /** False while the view is hidden: the clock of a running run stops. */
  tick: boolean;
  selected: boolean;
  focusable: boolean;
  newest: boolean;
  /** The pixel range of the clickable slab. */
  hit: { left: number; right: number };
  /** The pixel of the bar inside its slab. */
  offset: number;
  onHover(runId: string, on: boolean): void;
  onSelect(runId: string): void;
}) {
  const { ts } = useI18n();
  const now = useRunNow(run, tick, fixedNow);
  const parts = durationParts(run, now, counted);
  const cachedPx = cachedBarPx(height, parts);
  const duration = shownDurationText(run, parts, now);
  const tone = barTone(run.status);
  const status = runStatusLabel(statusText, ts);
  const test = isTestRun(run);
  const label = [
    ts('Run {id}', { id: run.dagRunId }),
    test ? ts('Test run') : null,
    status,
    duration,
    newest ? ts('latest') : null,
  ]
    .filter(Boolean)
    .join(', ');
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <div
          role="option"
          aria-selected={selected}
          aria-label={label}
          tabIndex={focusable ? 0 : -1}
          data-testid={`ww-run-bar-${run.dagRunId}`}
          data-run-id={run.dagRunId}
          data-status={run.status}
          data-tone={tone}
          data-latest={newest ? 'true' : undefined}
          data-test-run={test ? 'true' : undefined}
          data-cached={cachedPx > 0 ? 'true' : undefined}
          onClick={() => onSelect(run.dagRunId)}
          onPointerEnter={() => onHover(run.dagRunId, true)}
          onPointerLeave={() => onHover(run.dagRunId, false)}
          onFocus={() => onHover(run.dagRunId, true)}
          onBlur={() => onHover(run.dagRunId, false)}
          onKeyDown={(event) => {
            if (event.key !== 'Enter' && event.key !== ' ') return;
            event.preventDefault();
            onSelect(run.dagRunId);
          }}
          style={{ left: hit.left, width: hit.right - hit.left }}
          className={cn(
            'group absolute top-0 h-full cursor-pointer rounded-sm outline-none',
            'focus-visible:ring-2 focus-visible:ring-ring',
            selected && 'bg-foreground/5'
          )}
        >
          <span
            data-testid="ww-run-bar-fill"
            style={{
              left: offset,
              width: BAR_PX,
              height,
              backgroundImage: fillBackground(
                run,
                tone === 'partial'
                  ? 'linear-gradient(to top, var(--status-success) 50%, var(--status-warning) 50%)'
                  : undefined
              ),
            }}
            className={cn(
              'absolute bottom-0 block rounded-t-[1px] opacity-80 transition-opacity group-hover:opacity-100',
              tone !== 'partial' && toneClasses(tone).dot,
              selected && 'opacity-100 ring-1 ring-primary'
            )}
          >
            {cachedPx > 0 && (
              <span
                data-testid="ww-run-bar-cached"
                aria-hidden="true"
                style={{
                  height: cachedPx,
                  backgroundColor: CACHED_WASH,
                  backgroundImage: TEST_RUN_HATCH,
                }}
                className="absolute inset-x-0 bottom-0 block"
              />
            )}
          </span>
          {test && (
            <FlaskConical
              aria-hidden="true"
              data-testid="ww-run-bar-flask"
              style={{ left: offset + BAR_PX / 2, bottom: height + 1 }}
              className="pointer-events-none absolute size-[9px] -translate-x-1/2 text-foreground/70"
            />
          )}
        </div>
      </TooltipTrigger>
      <TooltipContent side="top" className="max-w-72">
        <BarTooltip
          run={run}
          statusText={statusText}
          duration={duration}
          parts={parts}
          extent={extent}
        />
      </TooltipContent>
    </Tooltip>
  );
}, propsEqual);

/** How an extent looks next to the run in focus (hovered, else selected). */
type ExtentFocus = 'idle' | 'in' | 'faded';

const Extent = memo(function Extent({
  run,
  statusText,
  extent,
  trackWidth,
  counted,
  fixedNow,
  tick,
  long,
  focus,
  selected,
  onHover,
  onSelect,
}: {
  run: ApiDAGRunDetails;
  statusText: string;
  extent: TimelineExtent;
  trackWidth: number;
  /** True when the cached time counts in the duration. */
  counted: boolean;
  /** The time of the host, or undefined to read the clock. */
  fixedNow: number | undefined;
  /** False while the view is hidden: the clock of a running run stops. */
  tick: boolean;
  long: boolean;
  focus: ExtentFocus;
  /** The extent of the selected run: drawn like its vertical bar. */
  selected: boolean;
  onHover(runId: string, on: boolean): void;
  onSelect(runId: string): void;
}) {
  const { ts } = useI18n();
  const now = useRunNow(run, tick, fixedNow);
  const parts = durationParts(run, now, counted);
  const duration = shownDurationText(run, parts, now);
  const tone = barTone(run.status);
  const width = extent.x1 - extent.x0;
  const test = isTestRun(run);
  const label = [
    ts('Run {id}', { id: run.dagRunId }),
    test ? ts('Test run') : null,
    runStatusLabel(statusText, ts),
    duration,
    extent.clippedLeft ? ts('Started before this view') : null,
  ]
    .filter(Boolean)
    .join(', ');
  // The hourglass sits after the end of the extent, or inside it at the edge.
  const hourglassAfter = extent.x1 + 14 <= trackWidth;
  // The selected extent is as high as its lane and has a mark at each end.
  const fillHeight = selected ? LANE_PX : EXTENT_PX;
  const fillLeft = extent.clippedLeft ? 9 : 2;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <div
          role="button"
          tabIndex={-1}
          aria-label={label}
          data-testid={`ww-run-extent-${run.dagRunId}`}
          data-run-id={run.dagRunId}
          data-lane={extent.lane}
          data-focus={focus}
          data-selected={selected ? 'true' : undefined}
          data-clipped={extent.clippedLeft ? 'true' : undefined}
          data-test-run={test ? 'true' : undefined}
          data-cached={extent.cachedPx > 0 ? 'true' : undefined}
          data-overflow={extent.overflow ? 'true' : undefined}
          onClick={() => onSelect(run.dagRunId)}
          onPointerEnter={() => onHover(run.dagRunId, true)}
          onPointerLeave={() => onHover(run.dagRunId, false)}
          style={{
            left: extent.x0 - 2,
            width: width + 4,
            top: extent.lane * LANE_PITCH - 1,
            height: LANE_PITCH,
          }}
          className={cn(
            'group absolute cursor-pointer outline-none',
            selected && 'z-[1]'
          )}
        >
          <span
            data-testid="ww-run-extent-fill"
            style={{
              left: fillLeft,
              right: 2,
              top: (LANE_PITCH - fillHeight) / 2,
              height: fillHeight,
              backgroundImage: fillBackground(
                run,
                tone === 'partial'
                  ? 'linear-gradient(to right, var(--status-success) 50%, var(--status-warning) 50%)'
                  : undefined
              ),
            }}
            className={cn(
              'absolute block overflow-hidden rounded-full transition-opacity',
              tone !== 'partial' && toneClasses(tone).dot,
              focus === 'idle' && 'opacity-60',
              focus === 'faded' && 'opacity-40',
              focus === 'in' &&
                !selected &&
                'opacity-100 ring-1 ring-foreground/60',
              selected && 'opacity-100 ring-1 ring-primary'
            )}
          >
            {extent.cachedPx > 0 && (
              <span
                data-testid="ww-run-extent-cached"
                aria-hidden="true"
                style={{
                  // The fill of a clipped extent starts 7px after its left edge.
                  width: Math.max(
                    0,
                    extent.cachedPx - (extent.clippedLeft ? 7 : 0)
                  ),
                  backgroundColor: CACHED_WASH,
                  backgroundImage: TEST_RUN_HATCH,
                }}
                className="absolute inset-y-0 left-0 block"
              />
            )}
          </span>
          {selected && !extent.clippedLeft && (
            <span
              data-testid="ww-run-extent-start"
              aria-hidden="true"
              style={{ left: fillLeft - 1, top: -1, height: LANE_PITCH + 2 }}
              className="absolute w-px bg-primary"
            />
          )}
          {selected && !extent.running && (
            <span
              data-testid="ww-run-extent-end"
              aria-hidden="true"
              style={{ right: 1, top: -1, height: LANE_PITCH + 2 }}
              className="absolute w-px bg-primary"
            />
          )}
          {extent.clippedLeft && (
            <ChevronLeft
              data-testid="ww-run-extent-clip"
              aria-hidden="true"
              className="absolute left-0 top-1/2 h-2.5 w-2.5 -translate-y-1/2 text-muted-foreground"
            />
          )}
          {extent.overflow && (
            <span
              data-testid="ww-run-extent-overflow"
              aria-hidden="true"
              style={{ left: 0, top: 1, height: LANE_PX }}
              className="absolute w-[2px] rounded-full bg-foreground"
            />
          )}
          {extent.running && (
            <span
              data-testid="ww-run-extent-live"
              aria-hidden="true"
              style={{ top: 1, height: LANE_PX }}
              className={cn(
                'absolute right-0 w-0 border-l border-dashed border-current',
                toneClasses('running').text
              )}
            />
          )}
          {extent.labelled && duration && (
            <span
              data-testid="ww-run-extent-label"
              className="pointer-events-none absolute left-1/2 top-1/2 z-[1] -translate-x-1/2 -translate-y-1/2 whitespace-nowrap rounded-sm bg-muted px-1 font-mono text-[10px] leading-[10px] tabular-nums text-muted-foreground"
            >
              {duration}
            </span>
          )}
          {long && (
            <Hourglass
              role="img"
              aria-label={ts('Long run')}
              data-testid="ww-run-extent-long"
              style={hourglassAfter ? { left: '100%' } : { right: 2 }}
              className="absolute top-1/2 h-3 w-3 -translate-y-1/2 text-foreground"
            />
          )}
        </div>
      </TooltipTrigger>
      <TooltipContent side="top" className="max-w-72">
        <BarTooltip
          run={run}
          statusText={statusText}
          duration={duration}
          parts={parts}
          extent={extent}
        />
      </TooltipContent>
    </Tooltip>
  );
}, propsEqual);

/** The duration of the run under the pointer, at the top of its bar. */
function CursorDuration({
  run,
  counted,
  fixedNow,
  tick,
  top,
}: {
  run: ApiDAGRunDetails;
  counted: boolean;
  fixedNow: number | undefined;
  tick: boolean;
  top: number;
}) {
  const now = useRunNow(run, tick, fixedNow);
  const text = shownDurationText(run, durationParts(run, now, counted), now);
  if (!text) return null;
  return (
    <span
      data-testid="ww-run-timeline-cursor-duration"
      style={{ top }}
      className="absolute right-0 z-10 whitespace-nowrap rounded-sm bg-foreground px-1.5 font-semibold leading-[17px] text-background"
    >
      {text}
    </span>
  );
}

/**
 * The labels of the duration axis. The ticks change with the runs, so the
 * labels are skipped while the ticks are the same objects.
 */
const YAxisTicks = memo(function YAxisTicks({
  ticks,
}: {
  ticks: readonly DurationTick[];
}) {
  return (
    <>
      <span
        data-testid="ww-run-timeline-ytick"
        data-duration={0}
        style={{ top: BASELINE - 6.5 }}
        className="absolute right-1.5 whitespace-nowrap"
      >
        0
      </span>
      {ticks.map((tick) => (
        <span
          key={tick.v}
          data-testid="ww-run-timeline-ytick"
          data-duration={tick.v}
          style={{ top: BASELINE - tick.y - 6.5 }}
          className="absolute right-1.5 whitespace-nowrap"
        >
          {elapsedMsText(tick.v)}
        </span>
      ))}
    </>
  );
});

/**
 * The grid, the baselines and the labels of the time axis. They depend on
 * the ticks and the track rows only, so a render for the pointer or a clock
 * step that moves no tick does not visit them.
 */
const TimeAxes = memo(function TimeAxes({
  yTicks,
  ticks,
  axisY,
}: {
  yTicks: readonly DurationTick[];
  ticks: readonly LabelledTick[];
  axisY: number;
}) {
  return (
    <>
      {yTicks.map((tick) => (
        <span
          key={`y${tick.v}`}
          data-testid="ww-run-timeline-ygrid"
          style={{ top: BASELINE - tick.y }}
          className="absolute inset-x-0 h-px bg-foreground/10"
        />
      ))}
      {ticks.map((tick) => (
        <span
          key={`g${tick.v}`}
          data-testid="ww-run-timeline-grid"
          style={{
            left: tick.tickX,
            top: PLOT_TOP,
            height: axisY + TICK_LEN - PLOT_TOP,
          }}
          className="absolute w-px bg-foreground/10"
        />
      ))}
      <span
        style={{ top: BASELINE }}
        className="absolute inset-x-0 h-px bg-muted-foreground/50"
      />
      <span
        style={{ top: axisY }}
        className="absolute inset-x-0 h-px bg-muted-foreground/30"
      />
      {ticks.map((tick) => (
        <span
          key={`l${tick.v}`}
          data-testid="ww-run-timeline-tick"
          data-time={tick.v}
          className="text-[11px] leading-[13px] tabular-nums"
        >
          <span
            style={{
              left: tick.x,
              top: axisY + TICK_LEN + 3,
              transform: 'translateX(-50%)',
            }}
            className="absolute whitespace-nowrap font-medium text-muted-foreground"
          >
            {tick.label}
          </span>
          {tick.context && (
            <span
              style={{
                left: tick.cx,
                top: axisY + TICK_LEN + 3 + 14,
                transform: 'translateX(-50%)',
              }}
              className="absolute whitespace-nowrap font-semibold text-foreground/80"
            >
              {tick.context}
            </span>
          )}
        </span>
      ))}
    </>
  );
});

export interface RunTimelineViewProps {
  /** Every run to draw from, oldest first. */
  runs: readonly ApiDAGRunDetails[];
  loading: boolean;
  /** The run ID of the highlighted bar; null when no bar is. */
  selectedId: string | null;
  /** How many of the newest runs the track shows. */
  count: number;
  /** The counts that the control offers. */
  counts?: readonly number[];
  onSelect(runId: string): void;
  onCountChange(count: number): void;
  /**
   * The time range that picks the runs, or null while the count does. The
   * caller passes the runs of the range; the axis fits `rangeBounds`.
   */
  range?: RunRange | null;
  rangeBounds?: { from: number; to: number } | null;
  /** Offers the time range in the select of the runs. */
  onRangeChange?(range: RunRange): void;
  /** True when the range reaches back further than the runs that are loaded. */
  truncated?: boolean;
  /** The number of runs that the server lists at the most. */
  historyLimit?: number;
  /**
   * The toggle of the cached time shows when a run of the history reused
   * steps. `showCached` is its state: on, a run counts its cached time.
   */
  hasCachedTime?: boolean;
  showCached?: boolean;
  onShowCachedChange?(show: boolean): void;
  canStart?: boolean;
  onStart?(): void;
  /**
   * The toggle of the test runs shows when the history holds some. `showTests`
   * is its state; the runs in `runs` are the ones to draw.
   */
  hasTestRuns?: boolean;
  showTests?: boolean;
  onShowTestsChange?(show: boolean): void;
  /** The filter of the trigger types (see RunTimelineControls). */
  triggerTypes?: readonly TriggerType[];
  hiddenTypes?: readonly TriggerType[];
  onHiddenTypesChange?(types: readonly TriggerType[]): void;
  /**
   * Where the controls go: an element of the dock header (they are drawn
   * there, with a portal), null for nowhere (the header is not ready or the
   * dock is closed), or undefined for the row of the timeline itself.
   */
  controlsHost?: HTMLElement | null;
  /** Time of the render, for the runs that run. Defaults to the clock. */
  now?: number;
  /** The status text that a run shows. Defaults to the API status. */
  statusTextOf?: StatusTextOf;
}

/**
 * The last runs of the workflow on a time axis: one thin vertical bar for
 * each run (never for each step) at the real start time of the run, so that
 * a pause between runs is empty space. The colour of a bar is the status of
 * the run and its height is the wall time, relative to the longest run in
 * view. Under the bars, a thin horizontal extent for each run shows how long
 * the run lasts on the same axis; extents that overlap in time take
 * different lanes (four at the most, in a block of fixed height). The axis has
 * calendar ticks in local time. The track is a listbox: the arrow keys, Home
 * and End move the selection.
 */
export function RunTimelineView({
  runs: allRuns,
  loading,
  selectedId,
  count,
  counts,
  onSelect,
  onCountChange,
  range = null,
  rangeBounds: rangeEnds = null,
  onRangeChange,
  truncated = false,
  historyLimit = HISTORY_LIMIT,
  hasCachedTime = false,
  showCached = true,
  onShowCachedChange,
  canStart,
  onStart,
  hasTestRuns = false,
  showTests = true,
  onShowTestsChange,
  triggerTypes,
  hiddenTypes,
  onHiddenTypesChange,
  controlsHost,
  now: nowProp,
  statusTextOf = normalStatusText,
}: RunTimelineViewProps) {
  const { ts } = useI18n();
  const frameRef = useRef<HTMLDivElement>(null);
  const measured = useElementWidth(frameRef);
  // A hidden view (the dock is collapsed) measures nothing: it keeps the last
  // width, so that the bars are not laid out again to a default and back.
  const lastWidth = useRef<number | null>(null);
  if (measured !== null) lastWidth.current = measured;
  const width = measured ?? lastWidth.current ?? DEFAULT_WIDTH_PX;
  // The gutter of the duration axis is always reserved: the plot keeps its
  // place and its width, whatever the longest run.
  const plotWidth = Math.max(1, width - Y_AXIS_PX);
  const runs = useMemo(
    () => timelineWindow(allRuns, selectedId, count),
    [allRuns, selectedId, count]
  );
  const live = runs.some((run) => run.status === Status.Running);
  // A null host is a hidden view (the dock is closed): nobody sees the time.
  const tick = live && nowProp === undefined && controlsHost !== null;
  // The layout reads the clock in steps of at most one pixel of the axis
  // (axisClock): the view renders at a step, not each second. The bars read
  // the true time for their texts (useRunNow).
  const spanRef = useRef(0);
  const [mountedAt] = useState(() => Date.now());
  const now = useClock(tick ? 1000 : null, (time) =>
    nowProp !== undefined
      ? nowProp
      : tick
        ? axisClock(time, spanRef.current, plotWidth)
        : mountedAt
  );
  const fixedNow = nowProp;
  // The older runs that the window leaves out: those that last into the
  // view get an extent clipped at the left edge.
  const older = useMemo(() => {
    const first = runs[0];
    const at = first
      ? allRuns.findIndex((run) => run.dagRunId === first.dagRunId)
      : -1;
    return at > 0 ? allRuns.slice(0, at) : [];
  }, [allRuns, runs]);
  // The cached time counts only when the toggle is on: off, every run
  // shows its own time alone.
  const counted = hasCachedTime && showCached;
  const layout = useMemo(
    () =>
      timelineLayout(runs, now, plotWidth, older, {
        cachedOf: counted ? cachedMsOf : undefined,
        range: rangeEnds,
      }),
    [runs, now, plotWidth, older, counted, rangeEnds]
  );
  spanRef.current = layout.t1 - layout.t0;
  const narrow = width < NARROW_PX;
  const metrics = trackMetrics(narrow);
  // A test run never counts toward the median that decides a long run.
  const median = useMemo(
    () => longRunMedianMs(runs, now, counted),
    [runs, now, counted]
  );
  // The bars and the duration axis use the total time of the runs.
  const totals = useMemo(
    () => runs.map((run) => totalDurationMs(run, now, counted)),
    [runs, now, counted]
  );
  const axis = useMemo(() => durationAxis(totals), [totals]);
  const heights = useMemo(() => barHeights(totals, axis), [totals, axis]);
  const selectedIndex = runs.findIndex((run) => run.dagRunId === selectedId);
  // The latest run is the newest one that stays: a test run is never it.
  const newestId = [...allRuns]
    .reverse()
    .find((run) => !isTestRun(run))?.dagRunId;
  const [hoverId, setHoverId] = useState<string | null>(null);
  const hovered = hoverId
    ? runs.findIndex((run) => run.dagRunId === hoverId)
    : -1;
  // The extents of the run in focus and of the runs that overlap it stay
  // strong; the others fade.
  const focusId = hoverId ?? selectedId;
  const focusSet = useMemo(() => {
    const focused = layout.extents.find((e) => e.id === focusId);
    return new Set(focused ? [focused.id, ...focused.overlaps] : []);
  }, [layout.extents, focusId]);
  // Narrow tracks draw the extent of the selected run only, in the top lane.
  const extents = useMemo(
    () =>
      narrow
        ? layout.extents
            .filter((e) => e.id === selectedId)
            .map((e) => ({ ...e, lane: 0, overflow: false }))
        : layout.extents,
    [layout.extents, narrow, selectedId]
  );
  const runOf = (extent: TimelineExtent) =>
    extent.index >= 0
      ? runs[extent.index]
      : older.find((run) => run.dagRunId === extent.id);
  const extentOf = new Map(layout.extents.map((e) => [e.id, e]));

  // A key changes the selection and the focus follows it to the new bar.
  const trackRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const track = trackRef.current;
    if (!track || !track.contains(document.activeElement)) return;
    track.querySelector<HTMLElement>('[role="option"][tabindex="0"]')?.focus();
  }, [selectedId]);

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) {
      return;
    }
    // The keys go through the runs that the track draws, the same list that
    // the arrows of the run chip use.
    const last = runs.length - 1;
    const selectedAt = runs.findIndex((run) => run.dagRunId === selectedId);
    // From no selection, a key starts at the newest bar.
    const from = selectedAt < 0 ? last : selectedAt;
    const to = {
      ArrowLeft: from - 1,
      ArrowRight: from + 1,
      Home: 0,
      End: last,
    }[event.key];
    if (to === undefined) return;
    event.preventDefault();
    const target = runs[Math.min(last, Math.max(0, to))];
    if (target && target.dagRunId !== selectedId) onSelect(target.dagRunId);
  };

  // Stable, so that the memoized bars and extents skip a render of the view.
  const hover = useCallback(
    (id: string, on: boolean) =>
      setHoverId((current) => (on ? id : current === id ? null : current)),
    []
  );
  const onSelectRef = useRef(onSelect);
  onSelectRef.current = onSelect;
  const selectRun = useCallback((id: string) => onSelectRef.current(id), []);
  const moreText = ts(
    layout.hidden === 1 ? '{n} more run overlaps' : '{n} more runs overlap',
    { n: layout.hidden }
  );

  let track;
  if (loading && runs.length === 0) {
    track = (
      <div
        data-testid="ww-run-timeline-loading"
        aria-busy="true"
        aria-label={ts('Loading runs...')}
        role="status"
        className="flex min-w-0 flex-1 items-end gap-[2px]"
        style={{
          height: metrics.height,
          paddingBottom: metrics.height - BASELINE,
        }}
      >
        {SKELETON_HEIGHTS.map((h, i) => (
          <span
            key={i}
            style={{ height: Math.min(BAR_MAX_PX, h) }}
            className="block min-w-[3px] flex-1 animate-pulse rounded-sm bg-muted"
          />
        ))}
      </div>
    );
  } else if (runs.length === 0) {
    track = (
      <div
        data-testid="ww-run-timeline-empty"
        className="flex min-w-0 flex-1 flex-col items-start justify-center gap-1 text-xs text-muted-foreground"
        style={{ height: metrics.height }}
      >
        <p className="font-medium text-foreground">
          {ts(range ? 'No runs in this time range' : 'No runs yet')}
        </p>
        <p className="flex items-center gap-2">
          {ts(
            range
              ? 'Choose a longer range to see more runs.'
              : 'Start the workflow to see its runs here.'
          )}
          {canStart && onStart && (
            <Button type="button" size="xs" onClick={onStart}>
              <Play className="h-3 w-3" aria-hidden="true" />
              {ts('Run…')}
            </Button>
          )}
        </p>
      </div>
    );
  } else {
    const cross = hovered >= 0 ? layout.bars[hovered] : undefined;
    const crossLabel =
      cross === undefined ? '' : dayjs(cross.at).format('HH:mm:ss');
    // The badge is centred on the line and kept inside the track.
    const badgeHalf = crossLabel.length * 3.4 + 6;
    // The duration of the bar under the pointer, at the top of its bar.
    const crossRun = hovered >= 0 ? runs[hovered] : undefined;
    const crossHasDuration =
      !!crossRun && runDurationMs(crossRun, fixedNow ?? Date.now()) !== null;
    const crossY = hovered >= 0 ? BASELINE - heights[hovered]! : 0;
    const axisTitle = ts(
      axis.scale === 'sqrt'
        ? 'Run duration (square root scale)'
        : 'Run duration'
    );
    track = (
      <div
        data-testid="ww-run-timeline-plot"
        className="relative min-w-0 flex-1 select-none"
        style={{ height: metrics.height }}
      >
        {/* The duration axis: its labels stand in a gutter of fixed width,
            left of the canvas. */}
        <Tooltip>
          <TooltipTrigger asChild>
            <div
              role="img"
              aria-label={axisTitle}
              data-testid="ww-run-timeline-yaxis"
              data-scale={axis.scale}
              style={{ width: Y_AXIS_PX }}
              className="absolute inset-y-0 left-0 text-[11px] leading-[13px] tabular-nums text-muted-foreground"
            >
              <YAxisTicks ticks={axis.ticks} />
              {crossRun && (
                <CursorDuration
                  run={crossRun}
                  counted={counted}
                  fixedNow={fixedNow}
                  tick={tick}
                  top={crossY - 8.5}
                />
              )}
            </div>
          </TooltipTrigger>
          <TooltipContent side="right">{axisTitle}</TooltipContent>
        </Tooltip>
        <div
          data-testid="ww-run-timeline-canvas"
          style={{ left: Y_AXIS_PX }}
          className="absolute inset-y-0 right-0"
        >
          {/* The axes: grid lines, baseline, ticks and labels. */}
          <div
            aria-hidden="true"
            className="pointer-events-none absolute inset-0"
          >
            <TimeAxes
              yTicks={axis.ticks}
              ticks={layout.ticks}
              axisY={metrics.axisY}
            />
            {/* A running run reaches now: the line of now crosses the plot. */}
            {layout.nowX !== null && (
              <span
                data-testid="ww-run-timeline-now"
                style={{
                  left: layout.nowX,
                  top: PLOT_TOP,
                  height: metrics.axisY - PLOT_TOP,
                }}
                className="absolute border-l border-dashed border-muted-foreground/60"
              />
            )}
            {/* The cursor: a dashed line and the time of the run under it. */}
            {cross && (
              <>
                <span
                  data-testid="ww-run-timeline-cursor"
                  style={{
                    left: cross.centre,
                    top: PLOT_TOP,
                    height: metrics.axisY + TICK_LEN - PLOT_TOP,
                  }}
                  className="absolute border-l border-dashed border-foreground/55"
                />
                {crossHasDuration && (
                  <span
                    data-testid="ww-run-timeline-cursor-level"
                    style={{ left: 0, width: cross.centre, top: crossY }}
                    className="absolute border-t border-dashed border-foreground/55"
                  />
                )}
                <span
                  data-testid="ww-run-timeline-cursor-time"
                  style={{
                    left: Math.max(
                      badgeHalf,
                      Math.min(plotWidth - badgeHalf, cross.centre)
                    ),
                    top: metrics.axisY + TICK_LEN + 2,
                    transform: 'translateX(-50%)',
                  }}
                  className="absolute z-10 whitespace-nowrap rounded-sm bg-foreground px-1.5 text-[11px] font-semibold leading-[17px] tabular-nums text-background"
                >
                  {crossLabel}
                </span>
              </>
            )}
          </div>
          <div
            ref={trackRef}
            role="listbox"
            aria-label={ts('Runs')}
            aria-orientation="horizontal"
            data-testid="ww-run-timeline-track"
            onKeyDown={onKeyDown}
            style={{ top: PLOT_TOP, height: BAR_MAX_PX }}
            className="absolute inset-x-0"
          >
            {runs.map((run, i) => {
              const bar = layout.bars[i]!;
              return (
                <Bar
                  key={run.dagRunId}
                  run={run}
                  statusText={statusTextOf(run)}
                  extent={extentOf.get(run.dagRunId)}
                  height={heights[i]!}
                  counted={counted}
                  fixedNow={fixedNow}
                  tick={tick}
                  selected={i === selectedIndex}
                  // Tab reaches one bar: the selected one, else the newest.
                  focusable={
                    selectedIndex < 0
                      ? i === runs.length - 1
                      : i === selectedIndex
                  }
                  newest={run.dagRunId === newestId}
                  hit={{ left: bar.hitLeft, right: bar.hitRight }}
                  offset={bar.left - bar.hitLeft}
                  onHover={hover}
                  onSelect={selectRun}
                />
              );
            })}
          </div>
          {/* The extents: one thin segment for each run, in lanes. The block
            keeps its height whatever the overlaps. */}
          <div
            role="group"
            aria-label={ts('Run durations')}
            data-testid="ww-run-extents"
            style={{ top: metrics.extentTop, height: metrics.extentHeight }}
            className="absolute inset-x-0"
          >
            {extents.map((extent) => {
              const extentRun = runOf(extent);
              if (!extentRun) return null;
              return (
                <Extent
                  key={extent.id}
                  run={extentRun}
                  statusText={statusTextOf(extentRun)}
                  extent={extent}
                  trackWidth={plotWidth}
                  counted={counted}
                  fixedNow={fixedNow}
                  tick={tick}
                  long={
                    !isTestRun(extentRun) &&
                    isLongRun(extent.end - extent.start, median)
                  }
                  focus={
                    focusSet.size === 0
                      ? 'idle'
                      : focusSet.has(extent.id)
                        ? 'in'
                        : 'faded'
                  }
                  selected={extent.id === selectedId}
                  onHover={hover}
                  onSelect={selectRun}
                />
              );
            })}
          </div>
          {!narrow && layout.hidden > 0 && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Badge
                  size="sm"
                  variant="outline"
                  tabIndex={0}
                  data-testid="ww-run-timeline-more"
                  aria-label={moreText}
                  style={{ top: metrics.axisY + TICK_LEN + 3 + 14 }}
                  className="absolute right-0 z-10 bg-background tabular-nums"
                >
                  +{layout.hidden}
                </Badge>
              </TooltipTrigger>
              <TooltipContent side="top">{moreText}</TooltipContent>
            </Tooltip>
          )}
        </div>
      </div>
    );
  }

  const options = useMemo(
    () => counts ?? countOptions(allRuns.length),
    [counts, allRuns.length]
  );

  const controls = (
    <RunTimelineControls
      count={count}
      counts={options}
      onCountChange={onCountChange}
      range={range}
      onRangeChange={onRangeChange}
      truncatedNote={
        truncated
          ? ts('Only the last {n} runs are loaded', { n: historyLimit })
          : null
      }
      hasTestRuns={hasTestRuns}
      showTests={showTests}
      onShowTestsChange={onShowTestsChange}
      hasCachedTime={hasCachedTime}
      showCached={showCached}
      onShowCachedChange={onShowCachedChange}
      triggerTypes={triggerTypes}
      hiddenTypes={hiddenTypes}
      onHiddenTypesChange={onHiddenTypesChange}
    />
  );

  return (
    <div
      data-testid="ww-run-timeline"
      className="flex shrink-0 flex-col border-b border-border px-3 pb-1 pt-1"
    >
      <h3 className="sr-only">{ts('Run timeline')}</h3>
      {controlsHost === undefined
        ? controls
        : controlsHost && createPortal(controls, controlsHost)}
      <div ref={frameRef} className="flex min-w-0 flex-1">
        {track}
      </div>
    </div>
  );
}

/** The run timeline of the selected workflow: the history, and the selection (?run). */
export function RunTimeline() {
  const run = useWorkspaceRun();
  // The test runs are in the history only when the request asks for them; the
  // list is read again while one exists, as no event tells that it is gone.
  // The navigation (chip arrows, keys) reads the same visible runs.
  const visible = useVisibleRuns({ enabled: true, pollTestRuns: true });
  const [settings, actions] = useRunViewSettings();
  const dock = useDock();
  const headerTools = useContext(DockHeaderToolsContext);
  // The controls go to the dock header while the dock is open.
  const controlsHost =
    headerTools === undefined ? undefined : dock.open ? headerTools : null;
  const start = useRunStart();
  const loading = visible.isLoading;

  const { dagData } = useWorkspaceData('RunTimeline');
  const continuous = hasContinuousStep(dagData.dag?.steps);
  const selected = run.details;
  // A bar knows its summary only: the definition decides, and the selected
  // run also has its steps.
  const statusTextOf = useCallback<StatusTextOf>(
    (item) =>
      (selected && item.dagRunId === selected.dagRunId
        ? runDisplayStatus(selected)
        : summaryDisplayStatus(item.status, continuous)
      ).text,
    [selected, continuous]
  );
  const inRange = settings.mode === 'range';

  return (
    <RunTimelineView
      runs={visible.pool}
      loading={loading}
      selectedId={visible.selectedId}
      // A range shows every run of the pool: the window is as long as it.
      count={inRange ? visible.pool.length : settings.count}
      statusTextOf={statusTextOf}
      onSelect={(id) => run.select(id, { replace: true })}
      onCountChange={actions.setCount}
      range={inRange ? settings.range : null}
      rangeBounds={visible.range}
      onRangeChange={actions.setRange}
      truncated={visible.truncated}
      canStart={start.canStart}
      onStart={() => start.requestStart()}
      hasTestRuns={visible.hasTestRuns}
      showTests={settings.showTests}
      onShowTestsChange={actions.setShowTests}
      triggerTypes={visible.triggerTypes}
      hiddenTypes={settings.hiddenTypes}
      onHiddenTypesChange={actions.setHiddenTypes}
      controlsHost={controlsHost}
      hasCachedTime={visible.hasCachedTime}
      showCached={settings.showCached}
      onShowCachedChange={actions.setShowCached}
    />
  );
}
