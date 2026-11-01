// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { NodeStatus, Status } from '@/api/v1/schema';
import type { ApiDAGDetails, ApiDAGRunDetails } from '../../contracts';
import { node, run } from '../../data/__tests__/fixtures';
import { StepsGrid, type StepsGridProps } from '../StepsGrid';
import { stepGrid } from '../stepHistoryModel';

const NOW = Date.parse('2026-01-15T03:00:00Z');
const gone = { startedAt: '-', finishedAt: '-', doneCount: 0 };

const DAG = {
  name: 'demo',
  steps: [{ name: 'build' }, { name: 'test', depends: ['build'] }],
} as unknown as ApiDAGDetails;

function runAt(
  id: string,
  minute: number,
  nodes: ApiDAGRunDetails['nodes'],
  status = Status.Success
): ApiDAGRunDetails {
  const at = `2026-01-15T02:${String(minute).padStart(2, '0')}:00Z`;
  return run(id, nodes, status, { startedAt: at });
}

// Oldest first. r2 failed at test; r3 skipped test and still had a step
// that the definition no longer has.
const RUNS = [
  runAt('r1', 1, [node('build'), node('test')]),
  runAt(
    'r2',
    2,
    [node('build'), node('test', NodeStatus.Failed)],
    Status.Failed
  ),
  runAt('r3', 3, [
    node('build'),
    node('test', NodeStatus.Skipped, gone),
    node('lint'),
  ]),
];

function renderGrid(over: Partial<StepsGridProps> = {}) {
  const props: StepsGridProps = {
    grid: stepGrid(DAG, RUNS, NOW),
    runs: RUNS,
    selectedId: 'r2',
    onPick: vi.fn(),
    onPickRun: vi.fn(),
    onOpenStep: vi.fn(),
    onSpotlight: vi.fn(),
    ...over,
  };
  render(<StepsGrid {...props} />);
  return props;
}

const grid = () => screen.getByRole('grid', { name: 'Steps by run' });
const cell = (step: string, runId: string) =>
  screen.getByTestId(`ww-history-cell-${step}-${runId}`);
const runHeader = (runId: string) =>
  screen.getByTestId(`ww-history-run-${runId}`);
/** Presses a key on the focused element, as the keyboard does. */
const press = (key: string) =>
  fireEvent.keyDown(document.activeElement ?? document.body, { key });

describe('StepsGrid', () => {
  it('has a row for each step in execution order, and a removed step last', () => {
    renderGrid();
    const headers = within(grid()).getAllByRole('rowheader');
    expect(headers.map((h) => h.textContent)).toEqual([
      'build',
      'test',
      'lintnot in current definition',
    ]);
    expect(within(grid()).getAllByRole('columnheader')).toHaveLength(4);
  });

  it('names each cell by its step, its run and its state', () => {
    renderGrid();
    expect(cell('test', 'r1')).toHaveAttribute('data-state', 'ran');
    expect(cell('test', 'r2')).toHaveAccessibleName(/^test, Run r2, Failed, /);
    expect(cell('test', 'r3')).toHaveAttribute('data-state', 'skipped');
    expect(cell('test', 'r3')).toHaveAccessibleName(/Skipped/);
    expect(cell('lint', 'r1')).toHaveAttribute('data-state', 'absent');
    expect(cell('lint', 'r1')).toHaveAccessibleName(/Not in this run/);
  });

  it('marks the column of the selected run', () => {
    renderGrid();
    expect(cell('build', 'r2')).toHaveAttribute('aria-selected', 'true');
    expect(cell('build', 'r1')).toHaveAttribute('aria-selected', 'false');
  });

  it('selects the run and the step of a clicked cell', () => {
    const props = renderGrid();
    fireEvent.click(cell('test', 'r1'));
    expect(props.onPick).toHaveBeenCalledWith('r1', 'test');
  });

  it('selects a run alone from its column header', () => {
    const props = renderGrid();
    fireEvent.click(runHeader('r1'));
    expect(props.onPickRun).toHaveBeenCalledWith('r1');
    expect(props.onPick).not.toHaveBeenCalled();
  });

  // The run row is the first row of the keyboard moves.
  it('moves into the run row and selects a run with Enter', () => {
    const props = renderGrid();
    cell('build', 'r2').focus();

    press('ArrowUp');
    expect(runHeader('r2')).toHaveFocus();
    expect(runHeader('r2')).toHaveAttribute('tabindex', '0');
    press('ArrowUp');
    expect(runHeader('r2')).toHaveFocus();
    press('ArrowLeft');
    expect(runHeader('r1')).toHaveFocus();
    press('Enter');
    expect(props.onPickRun).toHaveBeenCalledWith('r1');
    expect(props.onPick).not.toHaveBeenCalled();

    press('ArrowDown');
    expect(cell('build', 'r1')).toHaveFocus();
  });

  // One tab stop: the arrow keys move in two directions, Enter selects.
  // The name opens that step's history and keeps the selected run.
  it('opens the history of a step from its name', () => {
    const props = renderGrid();
    fireEvent.click(within(grid()).getByRole('button', { name: 'test' }));
    expect(props.onOpenStep).toHaveBeenCalledWith('test');
    expect(props.onPick).not.toHaveBeenCalled();
  });

  // A click focuses the name; Enter on it opens the step again and leaves
  // the cell of the tab stop alone.
  it('opens the step with Enter on a clicked name', async () => {
    const user = userEvent.setup();
    const props = renderGrid();
    await user.click(within(grid()).getByRole('button', { name: 'test' }));
    await user.keyboard('{Enter}');
    expect(props.onOpenStep).toHaveBeenCalledTimes(2);
    expect(props.onPick).not.toHaveBeenCalled();
  });

  it('moves with the arrow keys and selects with Enter', () => {
    const props = renderGrid();
    const start = cell('build', 'r2');
    expect(start).toHaveAttribute('tabindex', '0');
    expect(
      within(grid())
        .getAllByRole('gridcell')
        .filter((c) => c.tabIndex === 0)
    ).toHaveLength(1);
    start.focus();

    press('ArrowDown');
    expect(cell('test', 'r2')).toHaveFocus();
    press('ArrowRight');
    expect(cell('test', 'r3')).toHaveFocus();
    press('Home');
    expect(cell('test', 'r1')).toHaveFocus();
    press('ArrowUp');
    expect(cell('build', 'r1')).toHaveFocus();
    expect(cell('build', 'r1')).toHaveAttribute('tabindex', '0');
    expect(props.onPick).not.toHaveBeenCalled();

    press('Enter');
    expect(props.onPick).toHaveBeenCalledWith('r1', 'build');
  });

  it('puts the tab stop on the newest run without a selection', () => {
    renderGrid({ selectedId: null });
    expect(cell('build', 'r3')).toHaveAttribute('tabindex', '0');
  });

  // The canvas lights the node of the row under the pointer; a hover
  // selects nothing.
  it('spotlights the step under the pointer and selects nothing', () => {
    const props = renderGrid();
    fireEvent.pointerEnter(cell('test', 'r1').closest('[role="row"]')!);
    expect(props.onSpotlight).toHaveBeenLastCalledWith('test');
    fireEvent.pointerLeave(grid());
    expect(props.onSpotlight).toHaveBeenLastCalledWith(null);
    expect(props.onPick).not.toHaveBeenCalled();
  });

  it('spotlights the step of the focused cell', () => {
    const props = renderGrid();
    cell('test', 'r2').focus();
    expect(props.onSpotlight).toHaveBeenLastCalledWith('test');
    expect(props.onPick).not.toHaveBeenCalled();
  });
});
