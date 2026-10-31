// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Pins of the History grid as it renders today, before the Executions view
// replaces it. parity:history-grid parity:crit-history-grid-order-and-cap

import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { components, NodeStatus, Status } from '@/api/v1/schema';
import HistoryTable from '../HistoryTable';

type DAGRun = components['schemas']['DAGRunDetails'];
type GridItem = components['schemas']['DAGGridItem'];

// The server caps the history at 30 runs (defaultHistoryLimit in dags.go).
const SERVER_HISTORY_LIMIT = 30;

// The times have no offset: the column dates are local, so the tests do not
// depend on the time zone of the machine.
function dagRun(dagRunId: string, startedAt: string): DAGRun {
  return {
    dagRunId,
    name: 'etl',
    status: Status.Success,
    startedAt,
  } as DAGRun;
}

// The caller (DAGExecutionHistory) gives the runs oldest first. Each grid
// history keeps the server order: newest first.
function renderTable(
  dagRuns: DAGRun[],
  gridData: GridItem[],
  idx = dagRuns.length - 1
) {
  const onSelect = vi.fn();
  render(
    <HistoryTable
      dagRuns={dagRuns}
      gridData={gridData}
      onSelect={onSelect}
      idx={idx}
    />
  );
  return { onSelect };
}

function runColumns(): HTMLElement[] {
  // The first header is the step-name column.
  return screen.getAllByRole('columnheader').slice(1);
}

// The text of the day in a run header: its visible or hidden date.
function dayOf(th: HTMLElement): string {
  return (th.firstElementChild ?? th).textContent ?? '';
}

function stepRow(name: string): HTMLElement {
  return screen.getByRole('cell', { name }).closest('tr')!;
}

function statusCells(name: string): HTMLElement[] {
  return within(stepRow(name)).getAllByRole('cell').slice(1);
}

function dotClass(cell: HTMLElement): string {
  return cell.querySelector('div')?.className ?? '';
}

const threeRuns = [
  dagRun('r1', '2026-03-01T10:00:00'),
  dagRun('r2', '2026-03-02T10:00:00'),
  dagRun('r3', '2026-03-03T10:00:00'),
];

describe('HistoryTable', () => {
  it('puts the columns in the given order, oldest on the left', () => {
    renderTable(threeRuns, [
      {
        name: 'extract',
        history: [NodeStatus.Success, NodeStatus.Failed, NodeStatus.Aborted],
      },
    ]);

    expect(runColumns().map(dayOf)).toEqual(['3/1', '3/2', '3/3']);
    // The row reverses its newest-first history to line up with the columns.
    const cells = statusCells('extract');
    expect(dotClass(cells[0]!)).toContain('bg-[var(--status-aborted)]');
    expect(dotClass(cells[1]!)).toContain('bg-[var(--status-error)]');
    expect(dotClass(cells[2]!)).toContain('bg-[var(--status-success)]');
  });

  // The cells hold NodeStatus: 5 is Skipped (it read as Queued, in indigo).
  // A skipped step has the neutral token of the canvas and of the dock.
  it('colours a skipped step with the neutral status token', () => {
    renderTable(threeRuns, [
      {
        name: 'other',
        history: [NodeStatus.Skipped, NodeStatus.Waiting, NodeStatus.Running],
      },
    ]);

    const cells = statusCells('other');
    expect(dotClass(cells[2]!)).toContain('bg-[var(--status-neutral)]');
    expect(dotClass(cells[1]!)).toContain('bg-[var(--status-warning)]');
    expect(dotClass(cells[0]!)).toContain('bg-[var(--status-running)]');
    expect(dotClass(cells[0]!)).toContain('animate-pulse');
  });

  it('writes a day once, at its first column', () => {
    renderTable(
      [
        dagRun('r1', '2026-03-01T08:00:00'),
        dagRun('r2', '2026-03-01T09:00:00'),
        dagRun('r3', '2026-03-02T09:00:00'),
      ],
      []
    );

    const [first, second, third] = runColumns();
    expect(first!.querySelector('.sr-only')).toBeNull();
    expect(second!.querySelector('.sr-only')).toHaveTextContent('3/1');
    expect(third!.querySelector('.sr-only')).toBeNull();
    expect(dayOf(third!)).toBe('3/2');
  });

  // No run column is blank: each one names its start time, and its tooltip
  // has the full date. The step-name column has a visible name too.
  it('gives every column a header with its start time', () => {
    renderTable(
      [
        dagRun('r1', '2026-03-01T08:05:00'),
        dagRun('r2', '2026-03-01T09:30:00'),
        dagRun('r3', '2026-03-02T21:45:10'),
      ],
      [],
      1
    );

    const columns = runColumns();
    expect(
      columns.map(
        (th) =>
          th.querySelector('[data-testid="history-column-time"]')?.textContent
      )
    ).toEqual(['08:05', '09:30', '21:45']);
    expect(columns.map((th) => th.getAttribute('title'))).toEqual([
      '2026-03-01 08:05:00',
      '2026-03-01 09:30:00',
      '2026-03-02 21:45:10',
    ]);
    const stepName = screen.getAllByRole('columnheader')[0]!;
    expect(stepName).toHaveTextContent('Step Name');
    expect(stepName.querySelector('.sr-only')).toBeNull();
    expect(stepName).toHaveClass('w-px', 'whitespace-nowrap');
  });

  it('renders one column per run up to the server cap of 30', () => {
    const runs = Array.from({ length: SERVER_HISTORY_LIMIT }, (_, i) =>
      dagRun(`r${i}`, '2026-03-01T10:00:00')
    );
    renderTable(runs, [
      {
        name: 'extract',
        history: runs.map(() => NodeStatus.Success),
      },
    ]);

    expect(runColumns()).toHaveLength(SERVER_HISTORY_LIMIT);
    expect(statusCells('extract')).toHaveLength(SERVER_HISTORY_LIMIT);
  });

  it('adds no cap of its own above the server cap', () => {
    const count = SERVER_HISTORY_LIMIT + 1;
    const runs = Array.from({ length: count }, (_, i) =>
      dagRun(`r${i}`, '2026-03-01T10:00:00')
    );
    renderTable(runs, [
      { name: 'extract', history: runs.map(() => NodeStatus.Success) },
    ]);

    expect(runColumns()).toHaveLength(count);
    expect(statusCells('extract')).toHaveLength(count);
  });

  it('renders the handler rows after the steps, as the server orders them', () => {
    renderTable(threeRuns, [
      {
        name: 'extract',
        history: [NodeStatus.Success, NodeStatus.Success, NodeStatus.Success],
      },
      {
        name: 'onSuccess',
        history: [
          NodeStatus.Success,
          NodeStatus.NotStarted,
          NodeStatus.Success,
        ],
      },
      {
        name: 'onExit',
        history: [NodeStatus.Failed, NodeStatus.Success, NodeStatus.Success],
      },
    ]);

    const rows = screen.getAllByRole('row').slice(1);
    expect(
      rows.map((row) => within(row).getAllByRole('cell')[0]!.textContent)
    ).toEqual(['extract', 'onSuccess', 'onExit']);
    // A run where the handler did not start leaves an empty cell.
    const handlerCells = statusCells('onSuccess');
    expect(handlerCells[1]!.querySelector('div')).toBeNull();
    expect(dotClass(statusCells('onExit')[2]!)).toContain(
      'bg-[var(--status-error)]'
    );
  });

  it('selects the column index from a header or a status cell', () => {
    const { onSelect } = renderTable(threeRuns, [
      {
        name: 'extract',
        history: [NodeStatus.Success, NodeStatus.Success, NodeStatus.Success],
      },
    ]);

    fireEvent.click(runColumns()[0]!);
    expect(onSelect).toHaveBeenLastCalledWith(0);

    fireEvent.click(statusCells('extract')[1]!);
    expect(onSelect).toHaveBeenLastCalledWith(1);
  });

  it('marks the selected column in the header and in each row', () => {
    renderTable(
      threeRuns,
      [
        {
          name: 'extract',
          history: [NodeStatus.Success, NodeStatus.Success, NodeStatus.Success],
        },
      ],
      1
    );

    expect(
      runColumns().map((th) => th.classList.contains('bg-muted/50'))
    ).toEqual([false, true, false]);
    expect(
      statusCells('extract').map((td) => td.classList.contains('bg-accent'))
    ).toEqual([false, true, false]);
  });
});
