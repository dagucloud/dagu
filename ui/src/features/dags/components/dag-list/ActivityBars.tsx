// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { memo, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { components } from '@/api/v1/schema';
import { useClock } from '@/hooks/useClock';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { formatDurationMs } from '@/lib/formatDuration';
import { runStatusText } from '@/lib/status-utils';
import { toneClasses } from '@/lib/statusPalette';
import { cn } from '@/lib/utils';
import {
  ACTIVITY_BAR_PX,
  ACTIVITY_HEIGHT_PX,
  ACTIVITY_WIDTH_PX,
  activityBars,
  runsInWindow,
  type ActivityBar,
} from './activityBarsModel';

type Activity = components['schemas']['DAGListActivity'];

// The bars move once a minute: a pixel of a 196 px strip spans about 7
// minutes of the 24 hours.
const REFRESH_MS = 60_000;
/** The height of the strip: its bars and the axis under them. */
const STRIP_HEIGHT_PX = ACTIVITY_HEIGHT_PX + 1;

interface ActivityBarsProps {
  activity?: Activity;
  /**
   * The strip fills the width of its cell and holds as many bars as fit, so
   * that the cell takes the changes of the width of the page. Without it, the
   * strip has the fixed width ACTIVITY_WIDTH_PX.
   */
  fluid?: boolean;
  /** Called when the user clicks the bar of a run. */
  onOpenRun?(runId: string, event: React.MouseEvent): void;
  className?: string;
}

/**
 * The runs of a workflow in the last 24 hours as one thin vertical bar for
 * each run, at the time that it started. The height of a bar is the time of
 * its run, as in the timeline of the workflow page. The strip is cheap on
 * purpose: a list shows hundreds of them, so a bar is a plain element with a
 * native tooltip.
 */
function ActivityBars({
  activity,
  fluid = false,
  onOpenRun,
  className,
}: ActivityBarsProps) {
  const { ts } = useI18n();
  const ref = useRef<HTMLDivElement>(null);
  // A fluid strip draws its bars only once it knows its width: the layout
  // effect measures before the first paint, so a row that mounts (an unfolded
  // group) does not show the bars at a default width and then move them.
  const [measured, setMeasured] = useState<number | null>(null);
  const present = activity !== undefined;
  useLayoutEffect(() => {
    const el = ref.current;
    // A strip without activity draws nothing and needs no width.
    if (!fluid || !present || !el) return;
    const width = Math.floor(el.getBoundingClientRect().width);
    if (width > 0) setMeasured(width);
    if (typeof ResizeObserver === 'undefined') {
      // No layout to measure (a test renderer): the default width stands.
      setMeasured((current) => current ?? ACTIVITY_WIDTH_PX);
      return;
    }
    const observer = new ResizeObserver(([entry]) => {
      const next = Math.floor(entry?.contentRect.width ?? 0);
      if (next > 0) setMeasured(next);
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, [fluid, present]);
  const width = fluid ? (measured ?? ACTIVITY_WIDTH_PX) : ACTIVITY_WIDTH_PX;
  const ready = !fluid || measured !== null;
  const minute = useClock(activity?.runs.length ? REFRESH_MS : null, (now) =>
    Math.floor(now / REFRESH_MS)
  );
  const runs = useMemo(
    () =>
      activity ? runsInWindow(activity.runs, minute * REFRESH_MS) : undefined,
    [activity, minute]
  );
  const bars = useMemo(
    () =>
      runs
        ? activityBars(runs, minute * REFRESH_MS, width).map((bar) => ({
            ...bar,
            title: barTitle(bar, ts),
          }))
        : [],
    [runs, minute, width, ts]
  );
  if (!activity || !runs) {
    // Until the activity arrives, the strip keeps its room and draws nothing:
    // the layout does not move when it arrives, and a bare axis would read as
    // no runs.
    return (
      <div
        ref={ref}
        aria-hidden="true"
        data-testid="dag-activity-pending"
        style={{
          width: fluid ? undefined : ACTIVITY_WIDTH_PX,
          height: STRIP_HEIGHT_PX,
        }}
        className={cn(fluid && 'min-w-0 flex-1', className)}
      />
    );
  }
  const count = runs.length;
  const summary = activity.truncated
    ? ts('{count}+ runs in the last 24 hours', { count })
    : count === 0
      ? ts('No runs in the last 24 hours')
      : count === 1
        ? ts('1 run in the last 24 hours')
        : ts('{count} runs in the last 24 hours', { count });
  return (
    <div
      role="img"
      aria-label={summary}
      title={summary}
      data-testid="dag-activity"
      data-runs={count}
      ref={ref}
      style={{
        width: fluid ? undefined : ACTIVITY_WIDTH_PX,
        height: STRIP_HEIGHT_PX,
      }}
      className={cn(
        'relative',
        fluid ? 'min-w-0 flex-1' : 'shrink-0',
        className
      )}
    >
      {/* The axis of the time, and a very light line at the height that a bar
          can reach: the longest run of the strip. */}
      <span
        aria-hidden="true"
        data-testid="dag-activity-ceiling"
        className="absolute inset-x-0 top-0 border-t border-dashed border-border/40"
      />
      <span
        aria-hidden="true"
        data-testid="dag-activity-axis"
        className="absolute inset-x-0 bottom-0 border-b border-border"
      />
      {ready && (
        <span className="absolute inset-0 block animate-in fade-in-0 duration-300">
          {bars.map((bar) => (
            <Bar
              key={bar.id}
              bar={bar}
              title={bar.title}
              onOpenRun={onOpenRun}
            />
          ))}
        </span>
      )}
    </div>
  );
}

function barTitle(bar: ActivityBar, ts: ReturnType<typeof useI18n>['ts']) {
  const parts = [
    formatAbsoluteTime(bar.startedAt),
    ts(runStatusText(bar.status)),
    formatDurationMs(bar.durationMs),
  ];
  return parts.filter(Boolean).join(' · ');
}

function Bar({
  bar,
  title,
  onOpenRun,
}: {
  bar: ActivityBar;
  title: string;
  onOpenRun?(runId: string, event: React.MouseEvent): void;
}) {
  return (
    <span
      title={title}
      data-run-id={bar.id}
      data-tone={bar.tone}
      onClick={
        onOpenRun
          ? (event) => {
              event.stopPropagation();
              onOpenRun(bar.id, event);
            }
          : undefined
      }
      style={{
        left: bar.left,
        width: ACTIVITY_BAR_PX,
        height: bar.height,
        backgroundImage:
          bar.tone === 'partial'
            ? 'linear-gradient(to top, var(--status-success) 50%, var(--status-warning) 50%)'
            : undefined,
      }}
      className={cn(
        'absolute bottom-px block rounded-t-sm opacity-80 hover:opacity-100',
        bar.tone !== 'partial' && toneClasses(bar.tone).dot,
        onOpenRun && 'cursor-pointer'
      )}
    />
  );
}

export default memo(ActivityBars);
