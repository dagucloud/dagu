// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { memo, useMemo, useRef, useState, type ReactNode } from 'react';
import type { components } from '@/api/v1/schema';
import { CompactCount } from '@/components/ui/compact-count';
import { RelativeTime } from '@/components/ui/relative-time';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import {
  formatAvailability,
  formatSpanMs,
} from '@/features/workflow-workspace/continuous/format';
import { runHistorySource } from '@/features/workflow-workspace/continuous/sampleSource';
import { useSampleAvailability } from '@/features/workflow-workspace/continuous/useContinuousData';
import { useClock } from '@/hooks/useClock';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { formatCompactCount, formatExactCount } from '@/lib/formatCompactCount';
import { toneClasses } from '@/lib/statusPalette';
import { cn } from '@/lib/utils';
import { ACTIVITY_HEIGHT_PX, ACTIVITY_WIDTH_PX } from './activityBars';
import {
  serviceActivity,
  type ServiceActivity,
  type ServiceCell,
} from './serviceActivity';

type Activity = components['schemas']['DAGListActivity'];

// A slot is at least a minute wide: the strip moves one slot at most each
// minute.
const REFRESH_MS = 60_000;

/** The window as a length of time in the language: "24 hours", "7 days". */
function windowText(seconds: number, locale: string): string {
  const [value, unit] =
    seconds >= 2 * 86_400 && seconds % 86_400 === 0
      ? [seconds / 86_400, 'day']
      : seconds >= 3_600 && seconds % 3_600 === 0
        ? [seconds / 3_600, 'hour']
        : [Math.max(1, Math.round(seconds / 60)), 'minute'];
  return new Intl.NumberFormat(locale, {
    style: 'unit',
    unit,
    unitDisplay: 'long',
  }).format(value as number);
}

/** How long the pointer rests on the strip before its detail opens. */
const DETAIL_DELAY_MS = 400;

interface ServiceActivityStripProps {
  /**
   * The buckets of the window, as the DAG list gives them, and the uptime of
   * the window when the history knows it.
   */
  activity?: Pick<Activity, 'buckets' | 'resolutionSeconds' | 'uptime'>;
  /** The length of the window in seconds. The default is the list window. */
  windowSeconds?: number;
  /** The end of the window in ms. The default is now. */
  until?: number;
  /**
   * The file name of the DAG. With it, the strip opens a detail on hover,
   * focus or click: the uptime and the success rate of 7 days, the runs of the window, the
   * last failure and the longest gap.
   */
  fileName?: string;
  /**
   * The strip fills the width of its cell, as the strip of ActivityBars does.
   * Without it, the strip has the fixed width ACTIVITY_WIDTH_PX.
   */
  fluid?: boolean;
  className?: string;
}

/**
 * The run history of a service DAG in the list: one cell for each bucket of
 * the history, coloured by the share of failed runs, or green where the
 * service was up, then the count of the
 * runs of the window and its uptime, or its success rate when the history has
 * no uptime for the window. A cell is a plain element with a
 * native tooltip, and the strip draws nothing for a single run. It reads only
 * the buckets, so the status of the latest run does not change it.
 */
function ServiceActivityStrip({
  activity,
  windowSeconds,
  until,
  fileName,
  fluid = false,
  className,
}: ServiceActivityStripProps) {
  const { ts, locale } = useI18n();
  const minute = useClock(
    activity && until === undefined ? REFRESH_MS : null,
    (now) => Math.floor(now / REFRESH_MS)
  );
  const buckets = activity?.buckets;
  const resolution = activity?.resolutionSeconds;
  const model = useMemo(
    () =>
      serviceActivity(
        buckets ?? [],
        resolution,
        until ?? minute * REFRESH_MS,
        windowSeconds
      ),
    [buckets, resolution, minute, until, windowSeconds]
  );
  const stripClass = fluid ? 'min-w-0 flex-1' : 'shrink-0';
  if (!activity) {
    return (
      <div
        style={fluid ? undefined : { width: ACTIVITY_WIDTH_PX }}
        className={cn(stripClass, className)}
      />
    );
  }
  const uptime = activity.uptime;
  const figures = {
    window: windowText(model.windowSeconds, locale),
    count: formatExactCount(model.count, locale),
    failures: formatExactCount(model.failures, locale),
    availability: formatAvailability(model.availability, locale),
    uptime: formatAvailability(uptime, locale),
  };
  const summary = ts(
    uptime === undefined
      ? 'Runs in the last {window}: {count}, failed {failures}, success rate {availability}'
      : 'Runs in the last {window}: {count}, failed {failures}, success rate {availability}, uptime {uptime}',
    figures
  );
  const body = (
    <>
      <div
        role="img"
        aria-label={summary}
        data-testid="dag-activity"
        data-runs={model.count}
        style={{
          width: fluid ? undefined : ACTIVITY_WIDTH_PX,
          height: ACTIVITY_HEIGHT_PX + 1,
          gridTemplateColumns: `repeat(${model.cells.length}, minmax(0, 1fr))`,
        }}
        className={cn(
          'relative grid grid-flow-col items-end gap-px border-b border-border',
          stripClass
        )}
      >
        {model.cells.map((cell) => (
          <Cell key={cell.start} cell={cell} title={cellTitle(cell, ts)} />
        ))}
      </div>
      <span
        data-testid="dag-service-figures"
        className="flex shrink-0 flex-col items-end text-[10px] leading-3 tabular-nums text-muted-foreground"
      >
        <CompactCount value={model.count} className="text-foreground/80" />
        <span
          data-testid={
            uptime === undefined
              ? 'dag-service-availability'
              : 'dag-service-uptime'
          }
          title={summary}
          className={cn(model.failures > 0 && 'text-foreground/80')}
        >
          {uptime === undefined
            ? ts('Success rate {value}', { value: figures.availability })
            : ts('Uptime {value}', { value: figures.uptime })}
        </span>
      </span>
    </>
  );
  const rowClass = cn(
    'flex items-center gap-1.5',
    fluid ? 'min-w-0 flex-1' : 'shrink-0',
    className
  );
  if (!fileName) {
    return (
      <div data-testid="dag-service-activity" className={rowClass}>
        {body}
      </div>
    );
  }
  return (
    <StripDetail
      className={rowClass}
      fileName={fileName}
      model={model}
      summary={summary}
      window={windowText(model.windowSeconds, locale)}
    >
      {body}
    </StripDetail>
  );
}

/**
 * The strip as a button that opens its detail. The detail reads the
 * availability of the DAG once, when it opens: a closed strip asks for
 * nothing. A click toggles it, as a touch screen has no hover.
 */
function StripDetail({
  className,
  fileName,
  model,
  summary,
  window,
  children,
}: {
  className: string;
  fileName: string;
  model: ServiceActivity;
  /** The name of the button: the figures of the window. */
  summary: string;
  window: string;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  // The tooltip closes at the press of its trigger: the click that follows
  // toggles the state that the press found.
  const openAtPress = useRef<boolean | null>(null);
  return (
    <Tooltip open={open} onOpenChange={setOpen} delayDuration={DETAIL_DELAY_MS}>
      <TooltipTrigger asChild>
        <button
          type="button"
          data-testid="dag-service-activity"
          aria-label={summary}
          aria-expanded={open}
          className={cn(
            className,
            'rounded-sm text-left focus-visible:outline-2 focus-visible:outline-ring'
          )}
          onPointerDown={() => {
            openAtPress.current = open;
          }}
          onClick={(event) => {
            event.stopPropagation();
            // The tooltip would close at this click: the state is ours.
            event.preventDefault();
            // A key opens the detail that the focus already opened: it
            // stays open, and Escape closes it.
            setOpen(event.detail === 0 || !(openAtPress.current ?? open));
            openAtPress.current = null;
          }}
        >
          {children}
        </button>
      </TooltipTrigger>
      <TooltipContent
        side="bottom"
        className="w-56 px-3 py-2"
        // A click inside reads the figures: it does not open the row.
        onClick={(event) => event.stopPropagation()}
      >
        <StripFigures fileName={fileName} model={model} window={window} />
      </TooltipContent>
    </Tooltip>
  );
}

function StripFigures({
  fileName,
  model,
  window,
}: {
  fileName: string;
  model: ServiceActivity;
  window: string;
}) {
  const { ts, locale } = useI18n();
  const source = useMemo(() => runHistorySource(fileName), [fileName]);
  const { data } = useSampleAvailability(source, false);
  const week = data?.windows.find((w) => w.window === '7d');
  return (
    <dl data-testid="dag-service-detail" className="space-y-1">
      <Figure label={ts('Uptime {window}', { window: '7d' })}>
        {data ? formatAvailability(week?.uptime, locale) : '…'}
      </Figure>
      <Figure label={ts('Success rate {window}', { window: '7d' })}>
        {data ? formatAvailability(week?.availability, locale) : '…'}
      </Figure>
      <Figure label={ts('Runs in the last {window}', { window })}>
        <CompactCount value={model.count} />
      </Figure>
      <Figure label={ts('Last failure')}>
        {!data ? (
          '…'
        ) : data.lastFailureAt ? (
          <RelativeTime
            timestamp={data.lastFailureAt}
            absolute={formatAbsoluteTime(data.lastFailureAt) ?? undefined}
          />
        ) : (
          ts('None')
        )}
      </Figure>
      <Figure label={ts('Longest gap')}>
        {model.longestGapSeconds > 0
          ? formatSpanMs(model.longestGapSeconds * 1000)
          : ts('None')}
      </Figure>
    </dl>
  );
}

function Figure({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="tabular-nums">{children}</dd>
    </div>
  );
}

function cellTitle(cell: ServiceCell, ts: ReturnType<typeof useI18n>['ts']) {
  const time = formatAbsoluteTime(cell.start, { seconds: false }) ?? '';
  if (cell.count === 0) {
    return `${time} · ${cell.upMs > 0 ? ts('Up') : ts('No runs')}`;
  }
  return `${time} · ${ts('Runs {count} · Failed {failures}', {
    count: formatCompactCount(cell.count),
    failures: formatCompactCount(cell.failures),
  })}`;
}

function Cell({ cell, title }: { cell: ServiceCell; title: string }) {
  return (
    <span
      title={title}
      data-tone={cell.tone ?? 'empty'}
      style={{ height: cell.tone === 'success' ? '70%' : undefined }}
      className={cn(
        'block min-w-0 rounded-t-sm',
        cell.tone === null
          ? 'h-0.5 bg-border'
          : cn(toneClasses(cell.tone).dot, cell.tone !== 'success' && 'h-full')
      )}
    />
  );
}

export default memo(ServiceActivityStrip);
