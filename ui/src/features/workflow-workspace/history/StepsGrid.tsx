// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  memo,
  useCallback,
  useLayoutEffect,
  useRef,
  useState,
  type FocusEvent,
  type KeyboardEvent,
} from 'react';
import { StatusDot } from '@/components/ui/status-dot';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { shortRunId } from '@/lib/shortRunId';
import { runStatusText } from '@/lib/status-utils';
import { cn } from '@/lib/utils';
import type { ApiDAGRunDetails } from '../contracts';
import { sameContent } from '../data/sameContent';
import type { OlderRuns } from '../data/useOlderRuns';
import { isTestRun, runTime } from '../dock/runTimelineModel';
import { entryFill, entryLabel } from './entryLook';
import { OlderRunsStatus, OlderRunsTrigger } from './OlderRuns';
import type { StepGrid, StepGridRow, StepRunEntry } from './stepHistoryModel';

// The width of a run column and of the step name column.
const CELL_CLASS = 'flex h-6 w-4 shrink-0 items-center justify-center';
// The width of a run column in px, as w-4 of CELL_CLASS draws it.
const COLUMN_PX = 16;
// A view this close to the right end follows the newest run.
const END_SLACK_PX = 2;
const NAME_CLASS =
  'sticky left-0 z-[1] flex h-6 w-44 shrink-0 items-center gap-1 border-r border-border bg-card px-3';
const FOCUS_CLASS =
  'cursor-pointer outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring';
// The band of the selected run's column; its header dot is ringed, as the
// run timeline rings the selected bar.
const SELECTED_CLASS = 'bg-primary/20';
// The row of the run headers, above the step rows, in the keyboard moves.
const HEADER_ROW = -1;

export interface StepsGridProps {
  grid: StepGrid;
  /** The runs of the columns, oldest first, as the grid cells are. */
  runs: readonly ApiDAGRunDetails[];
  selectedId: string | null;
  /** Selects the run and the step of a cell. */
  onPick(runId: string, step: string): void;
  /** Selects a run alone, from its column header. */
  onPickRun(runId: string): void;
  /** Shows the history of one step, from a click on its name. */
  onOpenStep(step: string): void;
  /** The step under the pointer or the keyboard focus; null when none. */
  onSpotlight(step: string | null): void;
  /** The older runs that the grid reads at its left end. */
  older?: OlderRuns;
}

/** The mark of one cell: its shape tells the state, its colour the status. */
function CellMark({ entry }: { entry: StepRunEntry }) {
  switch (entry.state) {
    case 'absent':
      return (
        <span className="block w-2 border-t border-dotted border-muted-foreground/70" />
      );
    case 'notStarted':
      return (
        <span className="block size-1.5 rounded-full bg-[var(--status-neutral)]" />
      );
    case 'skipped':
      return (
        <span className="block size-2.5 rounded-[2px] ring-1 ring-inset ring-[var(--status-neutral)]" />
      );
    default: {
      const fill = entryFill(entry, { marked: true, split: 'to top' });
      return (
        <span
          style={fill.style}
          className={cn(
            'block size-2.5 rounded-[2px]',
            fill.className,
            entry.running && 'motion-safe:animate-pulse'
          )}
        />
      );
    }
  }
}

interface GridRowProps {
  row: StepGridRow;
  index: number;
  cells: readonly StepRunEntry[];
  selectedCol: number;
  /** The column of the one cell that Tab reaches, when it is in this row; else -1. */
  tabCol: number;
  onCell(row: number, col: number, kind: 'focus' | 'pick'): void;
  onSpotlight(step: string | null): void;
  onOpenStep(step: string): void;
}

/**
 * A row draws again only when its step or its cells change by content, or
 * when the selection or the tab stop moves in it: the grid builds new rows
 * for each event of the selected run.
 */
function rowEqual(prev: GridRowProps, next: GridRowProps): boolean {
  return (
    prev.index === next.index &&
    prev.selectedCol === next.selectedCol &&
    prev.tabCol === next.tabCol &&
    prev.onCell === next.onCell &&
    prev.onSpotlight === next.onSpotlight &&
    prev.onOpenStep === next.onOpenStep &&
    sameContent(prev.row, next.row) &&
    sameContent(prev.cells, next.cells)
  );
}

const GridRow = memo(function GridRow({
  row,
  index,
  cells,
  selectedCol,
  tabCol,
  onCell,
  onSpotlight,
  onOpenStep,
}: GridRowProps) {
  const { ts } = useI18n();
  return (
    <div
      role="row"
      aria-rowindex={index + 2}
      data-step={row.step}
      onPointerEnter={() => onSpotlight(row.step)}
      className="group/row flex min-w-max hover:bg-muted/60"
    >
      <div
        role="rowheader"
        title={row.step}
        className={cn(NAME_CLASS, 'group-hover/row:bg-muted')}
      >
        {/* The grid keeps one tab stop: Enter on a cell reaches the step too. */}
        <button
          type="button"
          tabIndex={-1}
          onClick={() => onOpenStep(row.step)}
          className={cn(
            'min-w-0 cursor-pointer truncate text-left text-xs hover:underline',
            row.removed ? 'text-muted-foreground' : 'text-foreground'
          )}
        >
          {row.step}
        </button>
        {row.removed && (
          <span className="sr-only">{ts('not in current definition')}</span>
        )}
      </div>
      {cells.map((entry, col) => {
        const label = entryLabel(ts, entry, row.step);
        return (
          <span
            key={entry.runId}
            role="gridcell"
            tabIndex={col === tabCol ? 0 : -1}
            aria-label={label}
            aria-selected={col === selectedCol}
            title={label}
            data-cell={`${index}:${col}`}
            data-testid={`ww-history-cell-${row.step}-${entry.runId}`}
            data-state={entry.state}
            data-status={entry.status ?? undefined}
            onFocus={() => onCell(index, col, 'focus')}
            onClick={() => onCell(index, col, 'pick')}
            className={cn(
              CELL_CLASS,
              FOCUS_CLASS,
              col === selectedCol && SELECTED_CLASS
            )}
          >
            <CellMark entry={entry} />
          </span>
        );
      })}
    </div>
  );
}, rowEqual);

/**
 * The steps of the workflow by run: a row for each step, a column for each
 * run, the newest on the right and scrolled into view. A view at the right
 * end follows a new run; a view scrolled back stays on its runs, also while
 * older runs come in on the left. The grid reads them when its left end
 * comes into view. The name column and the run row stay in place while the
 * grid scrolls. A run header selects that run alone. The grid is one tab
 * stop; the arrow keys, Home and End move in it, the run row included, and
 * Enter selects the run and the step of the cell, or the run of a header.
 */
export function StepsGrid({
  grid,
  runs,
  selectedId,
  onPick,
  onPickRun,
  onOpenStep,
  onSpotlight,
  older,
}: StepsGridProps) {
  const { ts } = useI18n();
  const scrollRef = useRef<HTMLDivElement>(null);
  const gridRef = useRef<HTMLDivElement>(null);
  const selectedCol = runs.findIndex((run) => run.dagRunId === selectedId);
  const lastCol = runs.length - 1;
  // The focus names its run: a new run moves the columns under it.
  const [focus, setFocus] = useState<{ row: number; runId: string } | null>(
    null
  );
  const focusCol = focus
    ? runs.findIndex((run) => run.dagRunId === focus.runId)
    : -1;
  const tab =
    focus && focus.row < grid.rows.length && focusCol >= 0
      ? { row: focus.row, col: focusCol }
      : {
          // Without step rows, the run row holds the tab stop.
          row: grid.rows.length > 0 ? 0 : HEADER_ROW,
          col: selectedCol >= 0 ? selectedCol : lastCol,
        };

  // The run at the left edge of a view scrolled back, and how far into its
  // column the view starts; null for a view at the right end, which follows
  // the newest run. A focused cell stays in view instead: a selection can
  // move the window.
  const anchor = useRef<{ runId: string; offset: number } | null>(null);
  const newestId = runs[lastCol]?.dagRunId;
  const oldestId = runs[0]?.dagRunId;
  useLayoutEffect(() => {
    const scroller = scrollRef.current;
    if (!scroller) return;
    const active = document.activeElement;
    if (active instanceof HTMLElement && gridRef.current?.contains(active)) {
      active.scrollIntoView?.({ block: 'nearest', inline: 'nearest' });
      return;
    }
    const at = anchor.current;
    const col = at ? runs.findIndex((run) => run.dagRunId === at.runId) : -1;
    if (!at) scroller.scrollLeft = scroller.scrollWidth;
    else if (col >= 0) scroller.scrollLeft = col * COLUMN_PX + at.offset;
    // Runs come and go at the two ends only.
  }, [newestId, oldestId]);
  const onScroll = () => {
    const scroller = scrollRef.current;
    if (!scroller) return;
    const left = scroller.scrollLeft;
    if (scroller.scrollWidth - scroller.clientWidth - left <= END_SLACK_PX) {
      anchor.current = null;
      return;
    }
    const col = Math.floor(left / COLUMN_PX);
    const runId = runs[col]?.dagRunId;
    anchor.current = runId
      ? { runId, offset: left - col * COLUMN_PX }
      : anchor.current;
  };

  // Stable, so that the memoized rows skip a render for another row.
  const live = useRef({ grid, runs, onPick, onPickRun, onSpotlight });
  live.current = { grid, runs, onPick, onPickRun, onSpotlight };
  const onCell = useCallback(
    (row: number, col: number, kind: 'focus' | 'pick') => {
      const { grid: current, runs: columns } = live.current;
      const runId = columns[col]?.dagRunId;
      if (runId === undefined) return;
      if (row === HEADER_ROW) {
        setFocus({ row, runId });
        if (kind === 'pick') live.current.onPickRun(runId);
        else live.current.onSpotlight(null);
        return;
      }
      const step = current.rows[row]?.step;
      if (step === undefined) return;
      setFocus({ row, runId });
      if (kind === 'pick') live.current.onPick(runId, step);
      else live.current.onSpotlight(step);
    },
    []
  );
  const spotlight = useCallback(
    (step: string | null) => live.current.onSpotlight(step),
    []
  );

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) {
      return;
    }
    // The keys act on the focused cell or run header; a clicked step name
    // keeps its own.
    const role = (event.target as HTMLElement).getAttribute('role');
    if (role !== 'gridcell' && role !== 'columnheader') return;
    const { row, col } = tab;
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      onCell(row, col, 'pick');
      return;
    }
    const next = {
      ArrowLeft: { row, col: col - 1 },
      ArrowRight: { row, col: col + 1 },
      ArrowUp: { row: row - 1, col },
      ArrowDown: { row: row + 1, col },
      Home: { row, col: 0 },
      End: { row, col: lastCol },
    }[event.key];
    if (!next) return;
    event.preventDefault();
    const to = {
      row: Math.min(grid.rows.length - 1, Math.max(HEADER_ROW, next.row)),
      col: Math.min(lastCol, Math.max(0, next.col)),
    };
    gridRef.current
      ?.querySelector<HTMLElement>(`[data-cell="${to.row}:${to.col}"]`)
      ?.focus();
    // Left and Right go through the history: the run of the column becomes
    // the selected run, and the grid stays.
    const runId = runs[to.col]?.dagRunId;
    if ((event.key === 'ArrowLeft' || event.key === 'ArrowRight') && runId) {
      if (to.col !== col) onPickRun(runId);
    }
  };

  const onBlur = (event: FocusEvent<HTMLDivElement>) => {
    if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
      onSpotlight(null);
    }
  };

  return (
    <div
      ref={scrollRef}
      data-testid="ww-steps-grid"
      onScroll={onScroll}
      // The focused cell scrolls clear of the sticky name column and run row.
      className="relative h-full min-h-0 scroll-pl-44 scroll-pt-6 overflow-auto"
    >
      {older && (
        <OlderRunsTrigger
          older={older}
          root={scrollRef}
          edge="left"
          className="pointer-events-none absolute left-0 top-0 h-px w-px"
        />
      )}
      <div
        ref={gridRef}
        role="grid"
        aria-label={ts('Steps by run')}
        aria-rowcount={grid.rows.length + 1}
        aria-colcount={runs.length + 1}
        onKeyDown={onKeyDown}
        onBlur={onBlur}
        onPointerLeave={() => onSpotlight(null)}
        className="min-w-max pb-1"
      >
        <div
          role="row"
          aria-rowindex={1}
          onPointerEnter={() => onSpotlight(null)}
          className="sticky top-0 z-[2] flex min-w-max border-b border-border bg-card"
        >
          <div
            role="columnheader"
            className={cn(
              NAME_CLASS,
              'justify-between text-[11px] font-medium text-muted-foreground'
            )}
          >
            {ts('Step')}
            {older && <OlderRunsStatus older={older} compact />}
          </div>
          {runs.map((run, col) => {
            const label = [
              ts('Run {id}', { id: shortRunId(run.dagRunId) }),
              ts(runStatusText(run.status)),
              isTestRun(run) ? ts('Test run') : null,
              formatAbsoluteTime(runTime(run)),
            ]
              .filter(Boolean)
              .join(', ');
            return (
              <div
                key={run.dagRunId}
                role="columnheader"
                tabIndex={tab.row === HEADER_ROW && col === tab.col ? 0 : -1}
                aria-label={label}
                title={label}
                aria-selected={col === selectedCol}
                data-cell={`${HEADER_ROW}:${col}`}
                data-testid={`ww-history-run-${run.dagRunId}`}
                onFocus={() => onCell(HEADER_ROW, col, 'focus')}
                onClick={() => onCell(HEADER_ROW, col, 'pick')}
                className={cn(
                  CELL_CLASS,
                  FOCUS_CLASS,
                  'hover:bg-muted',
                  col === selectedCol && SELECTED_CLASS
                )}
              >
                <StatusDot
                  kind="run"
                  status={run.status}
                  decorative
                  className={cn(
                    'motion-safe:animate-none',
                    col === selectedCol &&
                      'ring-2 ring-primary ring-offset-1 ring-offset-card'
                  )}
                />
              </div>
            );
          })}
        </div>
        {grid.rows.map((row, index) => (
          <GridRow
            key={row.step}
            row={row}
            index={index}
            cells={grid.cells[index]!}
            selectedCol={selectedCol}
            tabCol={index === tab.row ? tab.col : -1}
            onCell={onCell}
            onSpotlight={spotlight}
            onOpenStep={onOpenStep}
          />
        ))}
      </div>
    </div>
  );
}
