// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { NodeStatus, Status } from '@/api/v1/schema';
import type { ApiDAGRunDetails } from '../../contracts';
import { node, run } from '../../data/__tests__/fixtures';
import { StepHistory, type StepHistoryProps } from '../StepHistory';
import { stepHistory } from '../stepHistoryModel';

const NOW = Date.parse('2026-01-15T03:00:00Z');
const gone = { startedAt: '-', finishedAt: '-', doneCount: 0 };

function runAt(
  id: string,
  minute: number,
  a = node('a'),
  status = Status.Success,
  extra: Partial<ApiDAGRunDetails> = {}
): ApiDAGRunDetails {
  const at = `2026-01-15T02:${String(minute).padStart(2, '0')}:00Z`;
  return run(id, [a], status, { startedAt: at, ...extra });
}

// Oldest first: a success of 3 s, a run without the step, a failure, and a
// test run that failed too.
const RUNS = [
  runAt('r1', 1),
  run('r2', [node('other')], Status.Success, {
    startedAt: '2026-01-15T02:02:00Z',
  }),
  runAt(
    'r3',
    3,
    node('a', NodeStatus.Failed, { error: 'exit status 3\ntrace' }),
    Status.Failed
  ),
  runAt('r4', 4, node('a', NodeStatus.Failed), Status.Failed, {
    ephemeral: true,
  }),
];

function renderHistory(over: Partial<StepHistoryProps> = {}) {
  const props: StepHistoryProps = {
    step: 'a',
    entries: stepHistory(RUNS, 'a', NOW),
    selectedId: 'r3',
    onPick: vi.fn(),
    ...over,
  };
  render(<StepHistory {...props} />);
  return props;
}

const bars = () =>
  within(screen.getByRole('listbox', { name: 'Runs' })).getAllByRole('option');
const rows = () =>
  within(screen.getByRole('list', { name: 'Runs of a' }))
    .getAllByRole('listitem')
    .map((item) => item.dataset.testid?.replace('ww-step-history-row-', ''));

describe('StepHistory', () => {
  it('sums up the finished runs of the step, without the test run', () => {
    renderHistory();
    const summary = screen.getByTestId('ww-step-history-summary');
    expect(summary).toHaveTextContent('50% succeeded');
    expect(summary).toHaveTextContent('usually 3s');
    expect(
      within(summary).getByTestId('ww-step-history-failing')
    ).toHaveTextContent(/^Failing since /);
    expect(
      within(summary).getByTitle('1 of 2 finished runs succeeded')
    ).toBeInTheDocument();
  });

  it('names one finished run in the singular', () => {
    renderHistory({ entries: stepHistory([RUNS[0]!], 'a', NOW) });
    expect(
      within(screen.getByTestId('ww-step-history-summary')).getByTitle(
        '1 of 1 finished run succeeded'
      )
    ).toBeInTheDocument();
  });

  it('says so when no run of the step has finished', () => {
    renderHistory({ entries: stepHistory([RUNS[1]!], 'a', NOW) });
    expect(screen.getByTestId('ww-step-history-summary')).toHaveTextContent(
      'No finished runs of this step'
    );
  });

  it('draws a bar for each run, oldest first, with the selected one marked', () => {
    renderHistory();
    expect(bars().map((bar) => bar.dataset.state)).toEqual([
      'ran',
      'absent',
      'ran',
      'ran',
    ]);
    expect(bars()[2]).toHaveAttribute('aria-selected', 'true');
    expect(bars()[0]).toHaveAttribute('aria-selected', 'false');
    expect(bars()[1]).toHaveAccessibleName(/Not in this run/);
    // A failure has a mark of its own, not only its colour.
    expect(
      within(bars()[2]!).getByTestId('ww-step-history-failed-mark')
    ).toBeInTheDocument();
    expect(
      within(bars()[0]!).queryByTestId('ww-step-history-failed-mark')
    ).toBeNull();
    expect(screen.getByTestId('ww-step-history-usual')).toBeInTheDocument();
  });

  // The arrow keys move the focus only: a run is selected by Enter.
  it('moves the focus with the arrow keys and selects with Enter', () => {
    const props = renderHistory();
    const listbox = screen.getByRole('listbox', { name: 'Runs' });
    expect(bars()[2]).toHaveAttribute('tabindex', '0');
    bars()[2]!.focus();

    fireEvent.keyDown(listbox, { key: 'ArrowLeft' });
    expect(bars()[1]).toHaveFocus();
    fireEvent.keyDown(listbox, { key: 'Home' });
    expect(bars()[0]).toHaveFocus();
    fireEvent.keyDown(listbox, { key: 'End' });
    expect(bars()[3]).toHaveFocus();
    expect(props.onPick).not.toHaveBeenCalled();

    fireEvent.keyDown(listbox, { key: 'Enter' });
    expect(props.onPick).toHaveBeenCalledWith('r4');
  });

  it('selects the run of a bar on a click', () => {
    const props = renderHistory();
    fireEvent.click(bars()[0]!);
    expect(props.onPick).toHaveBeenCalledWith('r1');
  });

  it('lists the runs newest first, or the failures only', () => {
    renderHistory();
    expect(rows()).toEqual(['r4', 'r3', 'r2', 'r1']);
    const r3 = screen.getByTestId('ww-step-history-row-r3');
    expect(r3).toHaveTextContent('Failed');
    expect(r3).toHaveTextContent('exit status 3');
    expect(r3).not.toHaveTextContent('trace');
    expect(screen.getByTestId('ww-step-history-row-r4')).toHaveTextContent(
      'Test run'
    );

    fireEvent.click(screen.getByRole('button', { name: 'Failures only' }));
    expect(rows()).toEqual(['r4', 'r3']);
    fireEvent.click(screen.getByRole('button', { name: 'All' }));
    expect(rows()).toHaveLength(4);
  });

  it('says that there are no failures to list', () => {
    renderHistory({ entries: stepHistory([RUNS[0]!], 'a', NOW) });
    fireEvent.click(screen.getByRole('button', { name: 'Failures only' }));
    expect(screen.getByText('No failures in these runs')).toBeInTheDocument();
  });

  it('selects the run of a row', () => {
    const props = renderHistory();
    fireEvent.click(
      within(screen.getByTestId('ww-step-history-row-r1')).getByRole('button', {
        name: /Run r1/,
      })
    );
    expect(props.onPick).toHaveBeenCalledWith('r1');
  });

  it('offers the log only where the host can open it, for a step that ran', () => {
    const { unmount } = render(
      <StepHistory
        step="a"
        entries={stepHistory(RUNS, 'a', NOW)}
        selectedId={null}
        onPick={vi.fn()}
      />
    );
    expect(screen.queryByRole('button', { name: /Open the log/ })).toBeNull();
    unmount();

    const props = renderHistory({ onLog: vi.fn() });
    expect(
      screen.getAllByRole('button', { name: /^Open the log of run / })
    ).toHaveLength(3);
    expect(
      within(screen.getByTestId('ww-step-history-row-r2')).queryByRole(
        'button',
        { name: /Open the log/ }
      )
    ).toBeNull();
    fireEvent.click(
      screen.getByRole('button', { name: 'Open the log of run r3' })
    );
    expect(props.onLog).toHaveBeenCalledWith('r3');
  });

  it('notes a continuous step and opens its tab', () => {
    const props = renderHistory({ continuous: true, onContinuous: vi.fn() });
    expect(
      screen.getByText(
        'This step repeats without a limit: one run holds every repeat.'
      )
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Open Continuous' }));
    expect(props.onContinuous).toHaveBeenCalled();
  });

  it('notes that the status of a sub-workflow step is its own', () => {
    renderHistory({ subWorkflow: true });
    expect(
      screen.getByText(
        'The status is the one of this step, not of its sub-runs.'
      )
    ).toBeInTheDocument();
  });

  it('marks a skipped step apart from one that ran', () => {
    const skipped = runAt('s1', 5, node('a', NodeStatus.Skipped, gone));
    renderHistory({ entries: stepHistory([skipped], 'a', NOW) });
    expect(bars()[0]).toHaveAttribute('data-state', 'skipped');
    expect(bars()[0]).toHaveAccessibleName(/Skipped/);
  });
});
