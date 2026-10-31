// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Info, X } from 'lucide-react';
import { memo, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { Button } from '@/components/ui/button';
import RelativeTime from '@/components/ui/relative-time';
import { StatusDot } from '@/components/ui/status-dot';
import { ToggleButton, ToggleGroup } from '@/components/ui/toggle-group';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { ELAPSED_SLOT, useStepElapsed } from '@/hooks/useStepElapsed';
import { shortRunId } from '@/lib/shortRunId';
import { readableToneText } from '@/lib/statusPalette';
import { cn, FOCUS_RING } from '@/lib/utils';
import { useDockHidden } from '../dock/FreezeWhenHidden';
import { propsEqual } from '../dock/propsEqual';
import {
  barHeights,
  BAR_MAX_PX,
  durationAxis,
  elapsedMsText,
} from '../dock/runTimelineModel';
import {
  entryDurationText,
  entryFill,
  entryLabel,
  entryStateText,
} from './entryLook';
import {
  isFailedEntry,
  summarizeStepHistory,
  type StepHistorySummary,
  type StepRunEntry,
} from './stepHistoryModel';

// One slot for each run, and the bar in it.
const SLOT_PX = 8;
const BAR_W_PX = 6;
// The row under the baseline that marks a failure.
const MARK_ROW_PX = 9;

type Filter = 'all' | 'failures';

export interface StepHistoryProps {
  /** Server name of the step. */
  step: string;
  /** The runs as the step saw them, oldest first. */
  entries: readonly StepRunEntry[];
  selectedId: string | null;
  /** The step repeats without a limit: one run holds all its executions. */
  continuous?: boolean;
  /** The step runs a sub-workflow. */
  subWorkflow?: boolean;
  /** Selects the run, with the step kept. */
  onPick(runId: string): void;
  /** Selects the run and opens the log of the step; absent where no Log tab follows the run. */
  onLog?(runId: string): void;
  /** Opens the Continuous tab; absent where the host has none. */
  onContinuous?(): void;
}

/** The success rate, the usual time and the current failure streak. */
function Summary({ summary }: { summary: StepHistorySummary }) {
  const { ts } = useI18n();
  if (summary.counted === 0) {
    return (
      <p
        data-testid="ww-step-history-summary"
        className="text-xs text-muted-foreground"
      >
        {ts('No finished runs of this step')}
      </p>
    );
  }
  const percent = Math.round((summary.succeeded * 100) / summary.counted);
  return (
    <p
      data-testid="ww-step-history-summary"
      className="flex flex-wrap items-center gap-x-1.5 text-xs"
    >
      <span
        className="font-medium text-foreground"
        title={ts('{succeeded} of {count} finished runs succeeded', {
          succeeded: summary.succeeded,
          count: summary.counted,
        })}
      >
        {ts('{percent}% succeeded', { percent })}
      </span>
      {summary.usualMs !== null && (
        <>
          <span aria-hidden="true" className="text-muted-foreground">
            ·
          </span>
          <span className="text-muted-foreground">
            {ts('usually {duration}', {
              duration: elapsedMsText(summary.usualMs),
            })}
          </span>
        </>
      )}
      {summary.failingSince !== null && (
        <>
          <span aria-hidden="true" className="text-muted-foreground">
            ·
          </span>
          <span
            data-testid="ww-step-history-failing"
            className={cn('font-medium', readableToneText('error'))}
          >
            <I18nTemplate
              text="Failing since {time}"
              values={{
                time: <RelativeTime timestamp={summary.failingSince} />,
              }}
            />
          </span>
        </>
      )}
    </p>
  );
}

/** The mark of a run in the chart: a bar for a step that ran or was reused. */
function ChartMark({
  entry,
  height,
  selected,
}: {
  entry: StepRunEntry;
  height: number;
  selected: boolean;
}) {
  switch (entry.state) {
    case 'absent':
      return (
        <span className="mb-0.5 block h-1.5 border-l border-dotted border-muted-foreground/70" />
      );
    case 'notStarted':
      return (
        <span className="block size-1.5 rounded-full bg-[var(--status-neutral)]" />
      );
    case 'skipped':
      return (
        <span className="block size-1.5 rounded-[1px] ring-1 ring-inset ring-[var(--status-neutral)]" />
      );
    default: {
      const fill = entryFill(entry, { marked: false, split: 'to top' });
      return (
        <span
          data-testid="ww-step-history-bar-fill"
          style={{ ...fill.style, width: BAR_W_PX, height }}
          className={cn(
            'block rounded-t-[1px] opacity-80 transition-opacity group-hover:opacity-100',
            fill.className,
            selected && 'opacity-100 ring-1 ring-primary',
            entry.running && 'motion-safe:animate-pulse'
          )}
        />
      );
    }
  }
}

/**
 * One bar for each run, oldest on the left, with the height of the time
 * that the step took and a dashed line at its usual time. The chart is a
 * listbox: the arrow keys, Home and End move the focus, and Enter selects
 * the run, so that moving adds no history entry.
 */
function Chart({
  entries,
  selectedId,
  usualMs,
  onPick,
}: {
  entries: readonly StepRunEntry[];
  selectedId: string | null;
  usualMs: number | null;
  onPick(runId: string): void;
}) {
  const { ts } = useI18n();
  const listRef = useRef<HTMLDivElement>(null);
  const durations = useMemo(() => entries.map((e) => e.durationMs), [entries]);
  const axis = useMemo(() => durationAxis(durations), [durations]);
  const heights = useMemo(() => barHeights(durations, axis), [durations, axis]);
  const selectedIndex = entries.findIndex((e) => e.runId === selectedId);
  const [focused, setFocused] = useState<number | null>(null);
  const last = entries.length - 1;
  const stop =
    focused !== null && focused <= last
      ? focused
      : selectedIndex >= 0
        ? selectedIndex
        : last;

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) {
      return;
    }
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      const entry = entries[stop];
      if (entry) onPick(entry.runId);
      return;
    }
    const to = {
      ArrowLeft: stop - 1,
      ArrowRight: stop + 1,
      Home: 0,
      End: last,
    }[event.key];
    if (to === undefined) return;
    event.preventDefault();
    const index = Math.min(last, Math.max(0, to));
    setFocused(index);
    const options =
      listRef.current?.querySelectorAll<HTMLElement>('[role="option"]');
    options?.[index]?.focus();
  };

  const usualY = usualMs === null ? null : axis.heightOf(usualMs);
  return (
    <div className="flex items-start gap-2">
      <div
        ref={listRef}
        role="listbox"
        aria-label={ts('Runs')}
        aria-orientation="horizontal"
        data-testid="ww-step-history-chart"
        onKeyDown={onKeyDown}
        className="relative flex min-w-0 overflow-x-auto"
        style={{ height: BAR_MAX_PX + MARK_ROW_PX }}
      >
        {usualY !== null && usualMs !== null && (
          <span
            aria-hidden="true"
            data-testid="ww-step-history-usual"
            title={ts('usually {duration}', {
              duration: elapsedMsText(usualMs),
            })}
            style={{ bottom: MARK_ROW_PX + usualY }}
            className="pointer-events-none absolute inset-x-0 z-[1] border-t border-dashed border-foreground/40"
          />
        )}
        <span
          aria-hidden="true"
          style={{ bottom: MARK_ROW_PX }}
          className="pointer-events-none absolute inset-x-0 h-px bg-muted-foreground/40"
        />
        {entries.map((entry, i) => {
          const label = entryLabel(ts, entry);
          const selected = entry.runId === selectedId;
          return (
            <div
              key={entry.runId}
              role="option"
              aria-selected={selected}
              aria-label={label}
              title={label}
              tabIndex={i === stop ? 0 : -1}
              data-testid={`ww-step-history-bar-${entry.runId}`}
              data-state={entry.state}
              data-status={entry.status ?? undefined}
              onClick={() => onPick(entry.runId)}
              onFocus={() => setFocused(i)}
              style={{ width: SLOT_PX }}
              className={cn(
                'group relative flex h-full shrink-0 cursor-pointer flex-col items-center justify-end rounded-sm outline-none',
                'focus-visible:ring-2 focus-visible:ring-ring',
                selected && 'bg-foreground/5'
              )}
            >
              <ChartMark
                entry={entry}
                height={heights[i]!}
                selected={selected}
              />
              <span
                aria-hidden="true"
                style={{ height: MARK_ROW_PX }}
                className="flex items-center"
              >
                {isFailedEntry(entry) && (
                  <X
                    data-testid="ww-step-history-failed-mark"
                    className={cn('size-2', readableToneText('error'))}
                    strokeWidth={3}
                  />
                )}
              </span>
            </div>
          );
        })}
      </div>
      {axis.max > 0 && (
        <span
          title={ts('Longest')}
          className="shrink-0 text-[10px] tabular-nums text-muted-foreground"
        >
          {elapsedMsText(axis.max)}
        </span>
      )}
    </div>
  );
}

/** The time of a step that ran ticks while it runs; a reused one is the time of its source. */
function EntryDuration({ entry }: { entry: StepRunEntry }) {
  const hidden = useDockHidden();
  const own = useStepElapsed(
    entry.state === 'ran' && entry.status !== null
      ? {
          status: entry.status,
          startedAt: entry.startedAt ?? undefined,
          finishedAt: entry.finishedAt ?? undefined,
        }
      : null,
    !hidden
  );
  const text = entry.state === 'ran' ? own : entryDurationText(entry);
  return (
    <span className={cn(ELAPSED_SLOT, 'shrink-0 text-right')}>{text}</span>
  );
}

interface HistoryRowProps {
  entry: StepRunEntry;
  selected: boolean;
  onPick(runId: string): void;
  onLog?(runId: string): void;
}

const HistoryRow = memo(function HistoryRow({
  entry,
  selected,
  onPick,
  onLog,
}: HistoryRowProps) {
  const { ts } = useI18n();
  return (
    <li
      data-testid={`ww-step-history-row-${entry.runId}`}
      data-state={entry.state}
      className={cn(
        'flex min-h-7 items-center gap-1 px-3 text-xs hover:bg-muted/60',
        selected && 'bg-primary/10 hover:bg-primary/15'
      )}
    >
      <button
        type="button"
        aria-current={selected ? 'true' : undefined}
        aria-label={entryLabel(ts, entry)}
        onClick={() => onPick(entry.runId)}
        className={cn(
          'flex min-w-0 flex-1 items-center gap-2 rounded-sm py-1 text-left',
          FOCUS_RING
        )}
      >
        {entry.state === 'absent' ? (
          <span
            aria-hidden="true"
            className="block w-2 shrink-0 border-t border-dotted border-muted-foreground/70"
          />
        ) : (
          <StatusDot
            kind="step"
            status={entry.status!}
            reused={entry.reuseOutcome ?? undefined}
            decorative
          />
        )}
        <span className="w-28 shrink-0 truncate text-foreground">
          {ts(entryStateText(entry))}
        </span>
        <RelativeTime
          compact
          ago
          stable
          timestamp={entry.at}
          className="w-16 shrink-0 text-muted-foreground"
        />
        <EntryDuration entry={entry} />
        <span className="w-16 shrink-0 font-mono text-[11px] text-muted-foreground">
          {shortRunId(entry.runId)}
        </span>
        {entry.testRun && (
          <span className="shrink-0 text-[11px] text-muted-foreground">
            {ts('Test run')}
          </span>
        )}
        {entry.error && (
          <span
            title={entry.error}
            className={cn('min-w-0 flex-1 truncate', readableToneText('error'))}
          >
            {entry.error}
          </span>
        )}
      </button>
      {onLog && entry.state === 'ran' && (
        <Button
          type="button"
          variant="ghost"
          size="xs"
          aria-label={ts('Open the log of run {id}', {
            id: shortRunId(entry.runId),
          })}
          onClick={() => onLog(entry.runId)}
        >
          {ts('Log')}
        </Button>
      )}
    </li>
  );
}, propsEqual);

/**
 * The runs of one step: how often it succeeds, how long it usually takes and
 * since when it fails; a bar for each run of the run timeline; and the runs,
 * newest first, with all of them or the failures only. A pick selects the
 * run and keeps the step.
 */
export function StepHistory({
  step,
  entries,
  selectedId,
  continuous = false,
  subWorkflow = false,
  onPick,
  onLog,
  onContinuous,
}: StepHistoryProps) {
  const { ts } = useI18n();
  const [filter, setFilter] = useState<Filter>('all');
  const summary = useMemo(() => summarizeStepHistory(entries), [entries]);
  const newestFirst = useMemo(() => [...entries].reverse(), [entries]);
  const shown =
    filter === 'failures' ? newestFirst.filter(isFailedEntry) : newestFirst;

  return (
    <section
      aria-label={ts('History of {step}', { step })}
      data-testid="ww-step-history"
      data-step={step}
      className="flex h-full min-h-0 flex-col"
    >
      <div className="shrink-0 space-y-2 border-b border-border px-3 py-2">
        <Summary summary={summary} />
        {continuous && (
          <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <Info aria-hidden="true" className="size-3.5 shrink-0" />
            <span>
              {ts(
                'This step repeats without a limit: one run holds every repeat.'
              )}
            </span>
            {onContinuous && (
              <Button
                type="button"
                variant="ghost"
                size="xs"
                onClick={onContinuous}
              >
                {ts('Open Continuous')}
              </Button>
            )}
          </p>
        )}
        {subWorkflow && (
          <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <Info aria-hidden="true" className="size-3.5 shrink-0" />
            <span>
              {ts('The status is the one of this step, not of its sub-runs.')}
            </span>
          </p>
        )}
        <Chart
          entries={entries}
          selectedId={selectedId}
          usualMs={summary.usualMs}
          onPick={onPick}
        />
      </div>
      <div className="flex shrink-0 items-center px-3 py-1">
        <ToggleGroup aria-label={ts('Show')}>
          <ToggleButton
            value="all"
            groupValue={filter}
            onClick={() => setFilter('all')}
            className={cn('h-6 px-2', FOCUS_RING)}
          >
            {ts('All')}
          </ToggleButton>
          <ToggleButton
            value="failures"
            groupValue={filter}
            onClick={() => setFilter('failures')}
            className={cn('h-6 px-2', FOCUS_RING)}
          >
            {ts('Failures only')}
          </ToggleButton>
        </ToggleGroup>
      </div>
      {shown.length === 0 ? (
        <p className="px-3 py-2 text-xs text-muted-foreground">
          {ts('No failures in these runs')}
        </p>
      ) : (
        <ul
          aria-label={ts('Runs of {step}', { step })}
          className="min-h-0 flex-1 overflow-auto pb-1"
        >
          {shown.map((entry) => (
            <HistoryRow
              key={entry.runId}
              entry={entry}
              selected={entry.runId === selectedId}
              onPick={onPick}
              onLog={onLog}
            />
          ))}
        </ul>
      )}
    </section>
  );
}
