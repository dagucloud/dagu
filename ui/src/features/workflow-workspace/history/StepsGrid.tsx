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
import { isTestRun, runTime } from '../dock/runTimelineModel';
import { entryFill, entryLabel } from './entryLook';
import type { StepGrid, StepGridRow, StepRunEntry } from './stepHistoryModel';

// The width of a run column and of the step name column.
const CELL_CLASS = 'flex h-6 w-4 shrink-0 items-center justify-center';
const NAME_CLASS =
  'sticky left-0 z-[1] flex h-6 w-44 shrink-0 items-center gap-1 border-r border-border bg-card px-3';

export interface StepsGridProps {
  grid: StepGrid;
  /** The runs of the columns, oldest first, as the grid cells are. */
  runs: readonly ApiDAGRunDetails[];
  selectedId: string | null;
  /** Selects the run and the step of a cell. */
  onPick(runId: string, step: string): void;
  /** The step under the pointer or the keyboard focus; null when none. */
  onSpotlight(step: string | null): void;
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
        <span
          className={cn(
            'min-w-0 truncate text-xs',
            row.removed ? 'text-muted-foreground' : 'text-foreground'
          )}
        >
          {row.step}
        </span>
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
              'cursor-pointer outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring',
              col === selectedCol && 'bg-primary/10'
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
 * run, the newest on the right and scrolled into view. The name column and
 * the run row stay in place while the grid scrolls. The grid is one tab
 * stop; the arrow keys, Home and End move in it and Enter selects the run
 * and the step of the cell.
 */
export function StepsGrid({
  grid,
  runs,
  selectedId,
  onPick,
  onSpotlight,
}: StepsGridProps) {
  const { ts } = useI18n();
  const scrollRef = useRef<HTMLDivElement>(null);
  const gridRef = useRef<HTMLDivElement>(null);
  const selectedCol = runs.findIndex((run) => run.dagRunId === selectedId);
  const lastCol = runs.length - 1;
  const [focus, setFocus] = useState<{ row: number; col: number } | null>(null);
  const tab =
    focus && focus.row < grid.rows.length && focus.col <= lastCol
      ? focus
      : { row: 0, col: selectedCol >= 0 ? selectedCol : lastCol };

  // The newest run is in view when the grid opens and when a run comes.
  const newestId = runs[lastCol]?.dagRunId;
  useLayoutEffect(() => {
    const scroller = scrollRef.current;
    if (scroller) scroller.scrollLeft = scroller.scrollWidth;
  }, [newestId]);

  // Stable, so that the memoized rows skip a render for another row.
  const live = useRef({ grid, runs, onPick, onSpotlight });
  live.current = { grid, runs, onPick, onSpotlight };
  const onCell = useCallback(
    (row: number, col: number, kind: 'focus' | 'pick') => {
      const { grid: current, runs: columns } = live.current;
      const step = current.rows[row]?.step;
      const runId = columns[col]?.dagRunId;
      if (step === undefined || runId === undefined) return;
      setFocus({ row, col });
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
      row: Math.min(grid.rows.length - 1, Math.max(0, next.row)),
      col: Math.min(lastCol, Math.max(0, next.col)),
    };
    gridRef.current
      ?.querySelector<HTMLElement>(`[data-cell="${to.row}:${to.col}"]`)
      ?.focus();
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
      // The focused cell scrolls clear of the sticky name column and run row.
      className="h-full min-h-0 scroll-pl-44 scroll-pt-6 overflow-auto"
    >
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
          className="sticky top-0 z-[2] flex min-w-max border-b border-border bg-card"
        >
          <div
            role="columnheader"
            className={cn(
              NAME_CLASS,
              'text-[11px] font-medium text-muted-foreground'
            )}
          >
            {ts('Step')}
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
                aria-label={label}
                title={label}
                aria-selected={col === selectedCol}
                className={cn(
                  CELL_CLASS,
                  col === selectedCol && 'bg-primary/10'
                )}
              >
                <StatusDot
                  kind="run"
                  status={run.status}
                  decorative
                  className="motion-safe:animate-none"
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
          />
        ))}
      </div>
    </div>
  );
}
