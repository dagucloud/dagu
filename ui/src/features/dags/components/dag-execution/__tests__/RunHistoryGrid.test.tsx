// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { components } from '@/api/v1/schema';
import { RunHistoryGrid } from '../RunHistoryGrid';

type Run = components['schemas']['DAGRunDetails'];

vi.mock('../HistoryTable', () => ({
  default: ({
    dagRuns,
    idx,
    onSelect,
  }: {
    dagRuns: Run[];
    idx: number;
    onSelect: (idx: number) => void;
  }) => (
    <div data-selected={idx} data-testid="grid">
      {dagRuns.map((run, i) => (
        <button key={run.dagRunId} onClick={() => onSelect(i)}>
          {run.dagRunId}
        </button>
      ))}
    </div>
  ),
}));

// The API lists runs newest first.
const runs = ['run-3', 'run-2', 'run-1'].map((dagRunId) => ({ dagRunId }) as Run);

describe('RunHistoryGrid', () => {
  it('shows nothing until there are runs to pick from', () => {
    const { container } = render(
      <RunHistoryGrid
        dagRuns={runs.slice(0, 1)}
        gridData={[]}
        selectedRunId={null}
        onSelect={vi.fn()}
      />
    );

    expect(container).toBeEmptyDOMElement();
  });

  it('lists the runs oldest first and marks the latest one while none is picked', () => {
    render(
      <RunHistoryGrid
        dagRuns={runs}
        gridData={[]}
        selectedRunId={null}
        onSelect={vi.fn()}
      />
    );

    expect(
      screen.getAllByRole('button').map((button) => button.textContent)
    ).toEqual(['run-1', 'run-2', 'run-3']);
    expect(screen.getByTestId('grid')).toHaveAttribute('data-selected', '2');
  });

  it('marks the run that was picked', () => {
    render(
      <RunHistoryGrid
        dagRuns={runs}
        gridData={[]}
        selectedRunId="run-2"
        onSelect={vi.fn()}
      />
    );

    expect(screen.getByTestId('grid')).toHaveAttribute('data-selected', '1');
  });

  it('falls back to the latest run when the picked run is gone', () => {
    render(
      <RunHistoryGrid
        dagRuns={runs}
        gridData={[]}
        selectedRunId="aged-out"
        onSelect={vi.fn()}
      />
    );

    expect(screen.getByTestId('grid')).toHaveAttribute('data-selected', '2');
  });

  it('picks an older run by ID and goes back to the latest run with null', () => {
    const onSelect = vi.fn();
    render(
      <RunHistoryGrid
        dagRuns={runs}
        gridData={[]}
        selectedRunId={null}
        onSelect={onSelect}
      />
    );

    fireEvent.click(screen.getByText('run-1'));
    expect(onSelect).toHaveBeenLastCalledWith('run-1');

    fireEvent.click(screen.getByText('run-3'));
    expect(onSelect).toHaveBeenLastCalledWith(null);
  });

  // The old History tab moved between runs with the arrow keys. They act while
  // the grid has the focus, so that a dialog elsewhere keeps its own keys.
  describe('arrow keys', () => {
    function renderGrid(selectedRunId: string | null, onSelect = vi.fn()) {
      render(
        <RunHistoryGrid
          dagRuns={runs}
          gridData={[]}
          selectedRunId={selectedRunId}
          onSelect={onSelect}
        />
      );
      return onSelect;
    }
    const grid = () => screen.getByRole('group', { name: 'Latest runs' });

    it('moves to the run before with the left key', () => {
      const onSelect = renderGrid('run-2');
      fireEvent.keyDown(grid(), { key: 'ArrowLeft' });
      expect(onSelect).toHaveBeenCalledWith('run-1');
    });

    it('goes back to the latest run with the right key from the one before it', () => {
      const onSelect = renderGrid('run-2');
      fireEvent.keyDown(grid(), { key: 'ArrowRight' });
      expect(onSelect).toHaveBeenCalledWith(null);
    });

    it('stops at the latest run', () => {
      const onSelect = renderGrid(null);
      fireEvent.keyDown(grid(), { key: 'ArrowRight' });
      expect(onSelect).not.toHaveBeenCalled();
    });

    it('stops at the oldest run', () => {
      const onSelect = renderGrid('run-1');
      fireEvent.keyDown(grid(), { key: 'ArrowLeft' });
      expect(onSelect).not.toHaveBeenCalled();
    });

    it('leaves the keys to the rest of the page and to a modifier', () => {
      const onSelect = renderGrid('run-2');
      fireEvent.keyDown(document.body, { key: 'ArrowLeft' });
      fireEvent.keyDown(grid(), { key: 'ArrowLeft', shiftKey: true });
      expect(onSelect).not.toHaveBeenCalled();
    });
  });
});
