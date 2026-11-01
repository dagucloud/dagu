// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Calendar,
  Hash,
  Hourglass,
  type LucideIcon,
  Server,
  SlidersHorizontal,
  Timer,
  Zap,
} from 'lucide-react';
import React, { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { components, Status } from '@/api/v1/schema';
import { CopyButton } from '@/components/ui/copy-button';
import RelativeTime from '@/components/ui/relative-time';
import Ticker from '@/components/ui/ticker';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { triggerTypeLabels } from '@/features/dags/components/common/TriggerTypeIndicator';
import { useClock } from '@/hooks/useClock';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime, formatElapsed } from '@/lib/dagRunTiming';
import dayjs from '@/lib/dayjs';
import { cn } from '@/lib/utils';

type RunFields = Pick<
  components['schemas']['DAGRunDetails'],
  | 'dagRunId'
  | 'status'
  | 'startedAt'
  | 'finishedAt'
  | 'queuedAt'
  | 'scheduleTime'
  | 'triggerType'
  | 'triggerActor'
  | 'workerId'
  | 'profileName'
  | 'params'
>;

/** The facts of the row in the workflow bar, in the order that the bar drops them. */
export type InlineFact = 'trigger' | 'started' | 'duration' | 'id';

type Props = {
  dagRun: RunFields;
  /**
   * Called when the Copy run ID button copied the ID, when the parent
   * announces copies itself; otherwise the line announces on its own.
   */
  onRunIdCopied?: () => void;
  /**
   * Keeps the facts on one line that never wraps. The secondary facts are
   * dropped according to the width of the nearest `@container` ancestor, so the
   * parent must be one, and the run ID shortens with an ellipsis. The full
   * facts stay in the run page.
   */
  compact?: boolean;
  /** When set, the run ID is a link to this page, such as the run page. */
  runHref?: string;
  /** In the inline row: takes the place of the # in front of the run ID. */
  idMark?: React.ReactNode;
  /**
   * Shows the Scheduled, Queued, Started and Finished absolute times that are
   * set in a tooltip on Started. Keyboard focus also opens it. Before the run
   * starts, a Queued (or Scheduled) item carries the tooltip instead.
   */
  showTimes?: boolean;
  /**
   * With showTimes, the duration is a row of the times tooltip and not a fact
   * of the line. A narrow line, such as the workflow bar, keeps its room for
   * the run ID.
   */
  durationInTimes?: boolean;
  /**
   * With compact, Trigger and Worker stay on the line, which wraps when the
   * container is narrow. The run page has no other place for them.
   */
  keepSecondary?: boolean;
  /**
   * One row for a bar: Trigger, Started, Duration and the whole run ID with
   * its copy button. Each fact is as wide as its text, with a small gap
   * between facts, and a text that ticks keeps the width of its widest value.
   * The other facts, the parameters and the links stay in the stacked form.
   * Wins over `compact`.
   */
  inline?: boolean;
  /**
   * With `inline`, the facts that the row shows; the others are dropped. The
   * bar decides from its width. The default is every fact.
   */
  shown?: ReadonlySet<InlineFact>;
  /** With `inline`, leaves out the Trigger slot (its place has another fact). */
  omitTrigger?: boolean;
  /** Leaves out the Copy run ID button: the run ID does not last (a test run). */
  hideRunIdCopy?: boolean;
  /**
   * Leaves out the facts that another place shows, so that a tooltip repeats
   * nothing. Applies to the stacked form.
   */
  omit?: ReadonlySet<InlineFact>;
  /**
   * With the stacked form, shows the start as an absolute time, for a place
   * where the relative time is not on screen.
   */
  startedAbsolute?: boolean;
  /** With the stacked form, shows the parameters open: a tooltip cannot open them. */
  paramsOpen?: boolean;
};

/** Every fact of the row. */
export const ALL_INLINE_FACTS: ReadonlySet<InlineFact> = new Set([
  'trigger',
  'started',
  'duration',
  'id',
]);

/** The seconds, minutes or hours that a running duration shows: the widest text of its tier, in ch. */
function durationMinCh(text: string): number | undefined {
  if (/^\d+s$/.test(text)) return 3;
  if (/^\d+m \d+s$/.test(text) || /^\d+h \d+m$/.test(text)) return 7;
  return undefined;
}

/** The duration as text, live while the run runs. */
function useDurationText(dagRun: RunFields): string | null {
  const running = dagRun.status === Status.Running;
  const started = !!parseTime(dagRun.startedAt);
  // The text is the snapshot of the shared clock: a render when it changes.
  return useClock(running ? 1000 : null, (now) =>
    started
      ? (formatElapsed(
          dagRun.startedAt,
          running ? undefined : dagRun.finishedAt,
          { live: true, now }
        ) ?? '-')
      : null
  );
}

/**
 * The duration of the inline row. It owns the tick of a running run, so that
 * the other facts of the row do not render again each second.
 */
function InlineDuration({ dagRun }: { dagRun: RunFields }) {
  const text = useDurationText(dagRun);
  if (text === null) return null;
  const min = durationMinCh(text);
  return (
    <MetaItem icon={Timer} label="Duration" className="shrink-0">
      <span
        className="inline-block tabular-nums"
        style={min ? { minWidth: `${min}ch` } : undefined}
      >
        {text}
      </span>
    </MetaItem>
  );
}

/**
 * In the compact line these facts show when the container is wide, or so narrow
 * that the line wraps and has room: between the two they are the first to go,
 * since the run page has them all.
 */
const SECONDARY = 'hidden @max-3xl:flex @7xl:flex';

/** Queue time shorter than this is noise, not waiting. */
const MIN_QUEUE_WAIT_SECONDS = 1;

function MetaItem({
  icon: Icon,
  mark,
  label,
  children,
  className,
}: {
  icon: LucideIcon;
  /** Takes the place of the icon. */
  mark?: React.ReactNode;
  label: string;
  children: React.ReactNode;
  className?: string;
}) {
  const { ts } = useI18n();
  return (
    <div
      className={cn('flex min-w-0 items-center gap-1.5', className)}
      title={ts(label)}
    >
      {mark ?? <Icon className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />}
      <dt className="sr-only">{ts(label)}</dt>
      <dd className="min-w-0 text-foreground">{children}</dd>
    </div>
  );
}

// The API reports an unset time as "-".
function parseTime(value?: string): dayjs.Dayjs | undefined {
  if (!value || value === '-') return undefined;
  const time = dayjs(value);
  return time.isValid() ? time : undefined;
}

function RunDuration({ dagRun }: { dagRun: RunFields }) {
  if (dagRun.status !== Status.Running) {
    return (
      <>
        {formatElapsed(dagRun.startedAt, dagRun.finishedAt, { live: true }) ??
          '-'}
      </>
    );
  }
  return (
    <Ticker intervalMs={1000}>
      {() => (
        <span>
          {formatElapsed(dagRun.startedAt, undefined, { live: true }) ?? '-'}
        </span>
      )}
    </Ticker>
  );
}

function RunTimes({
  dagRun,
  withDuration,
}: {
  dagRun: RunFields;
  withDuration: boolean;
}) {
  const { ts } = useI18n();
  const times = (
    [
      ['Scheduled', dagRun.scheduleTime],
      ['Queued', dagRun.queuedAt],
      ['Started', dagRun.startedAt],
      ['Finished', dagRun.finishedAt],
    ] as const
  ).flatMap(([label, value]) => {
    const time = formatAbsoluteTime(value);
    return time ? [{ label, time }] : [];
  });
  return (
    <dl className="grid grid-cols-[auto_auto] gap-x-3 gap-y-0.5 text-left">
      {times.map(({ label, time }) => (
        <React.Fragment key={label}>
          <dt className="text-muted-foreground">{ts(label)}</dt>
          <dd className="font-mono tabular-nums">{time}</dd>
        </React.Fragment>
      ))}
      {withDuration && parseTime(dagRun.startedAt) && (
        <>
          <dt className="text-muted-foreground">{ts('Duration')}</dt>
          <dd className="font-mono tabular-nums">
            <RunDuration dagRun={dagRun} />
          </dd>
        </>
      )}
    </dl>
  );
}

function WithRunTimes({
  dagRun,
  enabled,
  withDuration = false,
  children,
}: {
  dagRun: RunFields;
  enabled: boolean;
  withDuration?: boolean;
  children: React.ReactNode;
}) {
  if (!enabled) return <>{children}</>;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          tabIndex={0}
          className="rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          {children}
        </span>
      </TooltipTrigger>
      <TooltipContent side="bottom" align="start">
        <RunTimes dagRun={dagRun} withDuration={withDuration} />
      </TooltipContent>
    </Tooltip>
  );
}

function queueWaitSeconds(queuedAt?: string, startedAt?: string): number {
  const queued = parseTime(queuedAt);
  const started = parseTime(startedAt);
  if (!queued || !started) return 0;
  return started.diff(queued, 'second');
}

/**
 * One line of what a run is: how it started, when, for how long, where, and
 * its ID. Both run and workflow headers use it, so each fact shows once.
 */
export function RunMetaLine({
  dagRun,
  onRunIdCopied,
  compact = false,
  keepSecondary = false,
  runHref,
  idMark,
  showTimes = false,
  durationInTimes = false,
  inline = false,
  shown = ALL_INLINE_FACTS,
  omitTrigger = false,
  hideRunIdCopy = false,
  omit,
  startedAbsolute = false,
  paramsOpen = false,
}: Props): React.ReactElement {
  const { ts } = useI18n();
  // The screen reader hears the copy for 2 s, as long as the check mark shows.
  const [announced, setAnnounced] = useState(false);
  useEffect(() => {
    if (!announced) return;
    const timer = window.setTimeout(() => setAnnounced(false), 2000);
    return () => window.clearTimeout(timer);
  }, [announced]);
  const started = parseTime(dagRun.startedAt) !== undefined;
  const durationOnLine = !(showTimes && durationInTimes);
  const queueWait = queueWaitSeconds(dagRun.queuedAt, dagRun.startedAt);
  const waitingTime = started
    ? undefined
    : parseTime(dagRun.queuedAt)
      ? ({ label: 'Queued', icon: Hourglass, value: dagRun.queuedAt } as const)
      : parseTime(dagRun.scheduleTime)
        ? ({
            label: 'Scheduled',
            icon: Calendar,
            value: dagRun.scheduleTime,
          } as const)
        : undefined;
  // The tooltip shows the absolute times, so the native title would repeat it.
  const absolute = showTimes ? '' : undefined;

  if (inline) {
    const waiting = waitingTime;
    return (
      <dl
        data-testid="run-facts-inline"
        className="flex flex-nowrap items-center gap-x-1.5 whitespace-nowrap text-xs text-muted-foreground"
      >
        {!omitTrigger && shown.has('trigger') && (
          <MetaItem icon={Zap} label="Trigger" className="shrink-0">
            {dagRun.triggerType
              ? ts(triggerTypeLabels[dagRun.triggerType] ?? dagRun.triggerType)
              : ''}
          </MetaItem>
        )}
        {shown.has('started') && (
          <MetaItem
            icon={started ? Calendar : (waiting?.icon ?? Calendar)}
            label={started ? 'Started' : (waiting?.label ?? 'Started')}
            className="shrink-0"
          >
            <RelativeTime
              timestamp={started ? dagRun.startedAt : waiting?.value}
              fallback="--"
              compact
              ago
              stable
            />
          </MetaItem>
        )}
        {shown.has('duration') && <InlineDuration dagRun={dagRun} />}
        {shown.has('id') && (
          <MetaItem
            icon={Hash}
            mark={idMark}
            label="Run ID"
            className="shrink-0 [&>dd]:flex [&>dd]:items-center [&>dd]:gap-0.5"
          >
            {/* The whole ID, never cut: when the bar lacks room, the other
                facts go first, then the whole row. */}
            <code className="font-mono">{dagRun.dagRunId}</code>
            {!hideRunIdCopy && (
              <CopyButton
                size="sm"
                text={dagRun.dagRunId}
                label={ts('Copy run ID')}
                className="shrink-0"
                onCopied={onRunIdCopied}
              />
            )}
          </MetaItem>
        )}
      </dl>
    );
  }

  return (
    <div className="flex min-w-0 flex-col gap-2">
      <dl
        className={cn(
          'flex items-center text-sm text-muted-foreground',
          compact && !keepSecondary
            ? 'flex-nowrap gap-x-4 overflow-hidden'
            : 'flex-wrap gap-x-5 gap-y-2'
        )}
      >
        {dagRun.triggerType && !omit?.has('trigger') && (
          <MetaItem
            icon={Zap}
            label="Trigger"
            className={compact && !keepSecondary ? SECONDARY : undefined}
          >
            {ts(triggerTypeLabels[dagRun.triggerType] ?? dagRun.triggerType)}
            {dagRun.triggerActor && (
              <span className="text-muted-foreground">
                {' '}
                · {dagRun.triggerActor}
              </span>
            )}
          </MetaItem>
        )}
        {started && (
          <>
            {!omit?.has('started') && (
              <MetaItem
                icon={Calendar}
                label="Started"
                className={compact ? 'shrink-0' : undefined}
              >
                <WithRunTimes
                  dagRun={dagRun}
                  enabled={showTimes}
                  withDuration={!durationOnLine}
                >
                  {startedAbsolute ? (
                    <span className="tabular-nums">
                      {formatAbsoluteTime(dagRun.startedAt)}
                    </span>
                  ) : (
                    <RelativeTime
                      timestamp={dagRun.startedAt}
                      fallback="--"
                      compact={compact}
                      ago={compact}
                      absolute={absolute}
                    />
                  )}
                </WithRunTimes>
              </MetaItem>
            )}
            {durationOnLine && !omit?.has('duration') && (
              <MetaItem
                icon={Timer}
                label="Duration"
                className={compact ? 'shrink-0' : undefined}
              >
                <span className="tabular-nums">
                  <RunDuration dagRun={dagRun} />
                </span>
              </MetaItem>
            )}
          </>
        )}
        {showTimes && waitingTime && (
          <MetaItem
            icon={waitingTime.icon}
            label={waitingTime.label}
            className={compact ? 'shrink-0' : undefined}
          >
            <WithRunTimes dagRun={dagRun} enabled>
              <RelativeTime
                timestamp={waitingTime.value}
                fallback="--"
                compact={compact}
                ago={compact}
                absolute={absolute}
              />
            </WithRunTimes>
          </MetaItem>
        )}
        {queueWait >= MIN_QUEUE_WAIT_SECONDS && (
          <MetaItem
            icon={Hourglass}
            label="Queue wait"
            className={compact && !keepSecondary ? SECONDARY : undefined}
          >
            <span className="tabular-nums">
              {formatElapsed(dagRun.queuedAt, dagRun.startedAt) ?? '-'}
            </span>
          </MetaItem>
        )}
        {dagRun.workerId && (
          <MetaItem
            icon={Server}
            label="Worker"
            className={compact && !keepSecondary ? SECONDARY : undefined}
          >
            <span className="font-mono text-xs">{dagRun.workerId}</span>
          </MetaItem>
        )}
        {dagRun.profileName && (
          <MetaItem
            icon={SlidersHorizontal}
            label="Profile"
            className={compact && !keepSecondary ? SECONDARY : undefined}
          >
            <span className="font-mono text-xs">{dagRun.profileName}</span>
          </MetaItem>
        )}
        {!omit?.has('id') && (
          <MetaItem
            icon={Hash}
            label="Run ID"
            className={compact ? 'min-w-0 shrink' : undefined}
          >
            {/* In the compact line a block box takes the width of its fact, so
                the ID ends with an ellipsis instead of a clipped glyph. */}
            <span
              className={cn(
                'items-center gap-1',
                compact ? 'flex min-w-0' : 'inline-flex'
              )}
            >
              <code
                className={cn(
                  'font-mono text-xs',
                  compact ? 'min-w-0 truncate' : 'break-all'
                )}
              >
                {runHref ? (
                  <Link
                    to={runHref}
                    className="text-primary hover:underline"
                    title={ts('Open run')}
                    aria-label={ts('Open run {dagRunId}', {
                      dagRunId: dagRun.dagRunId,
                    })}
                  >
                    {dagRun.dagRunId}
                  </Link>
                ) : (
                  dagRun.dagRunId
                )}
              </code>
              {!hideRunIdCopy && (
                <CopyButton
                  size="sm"
                  text={dagRun.dagRunId}
                  label={ts('Copy run ID')}
                  className="shrink-0"
                  onCopied={onRunIdCopied ?? (() => setAnnounced(true))}
                />
              )}
              {!onRunIdCopied && !hideRunIdCopy && (
                <span className="sr-only" aria-live="polite">
                  {announced ? (
                    <I18nText text={'Run ID copied to clipboard'} />
                  ) : (
                    ''
                  )}
                </span>
              )}
            </span>
          </MetaItem>
        )}
      </dl>
      {dagRun.params && (
        <details open={paramsOpen} className="min-w-0 text-xs">
          <summary className="cursor-pointer font-medium text-muted-foreground hover:text-foreground">
            <I18nText text={'Parameters'} />
          </summary>
          <div className="mt-1.5 max-h-[120px] overflow-y-auto rounded-md border bg-accent px-3 py-1.5 font-mono text-foreground">
            {dagRun.params}
          </div>
        </details>
      )}
    </div>
  );
}
