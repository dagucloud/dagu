// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Pins of the History tab as it behaves today, before the Executions view
// replaces it. parity:test-history-tab-untested

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { components, NodeStatus, Status } from '@/api/v1/schema';
import { toMermaidNodeId } from '@/lib/utils';
import DAGExecutionHistory from '../DAGExecutionHistory';

type DAGRun = components['schemas']['DAGRunDetails'];

const queryResult = vi.hoisted(() => ({
  current: { data: undefined as unknown, mutate: () => {} },
}));
const overviewProps = vi.hoisted(() => ({
  current: null as Record<string, unknown> | null,
}));

vi.mock('@/hooks/api', () => ({
  useQuery: () => queryResult.current,
  useClient: () => ({ PATCH: vi.fn() }),
}));

vi.mock('@/hooks/useDAGHistorySSE', () => ({
  useDAGHistorySSE: () => ({}),
}));

vi.mock('@/hooks/useSSECacheSync', () => ({
  sseFallbackOptions: () => ({}),
  useSSECacheSync: () => {},
}));

vi.mock('../../dag-details', () => ({
  DAGStatusOverview: (props: Record<string, unknown>) => {
    overviewProps.current = props;
    return null;
  },
  NodeStatusTable: () => null,
}));

vi.mock('../../step-details', () => ({
  StepDetailsDrawer: () => null,
}));

vi.mock('../../visualization', () => ({
  DAGGraph: ({
    dagRun,
    onRightClickStep,
  }: {
    dagRun: DAGRun;
    onRightClickStep: (id: string) => void;
  }) => (
    <div data-testid="graph" data-run={dagRun.dagRunId}>
      <button
        type="button"
        onClick={() =>
          onRightClickStep(toMermaidNodeId(dagRun.nodes[0]!.step.name))
        }
      >
        right-click step
      </button>
    </div>
  ),
}));

// The real grid, so the columns are the ones a user clicks.
vi.mock('..', async () => {
  const { default: HistoryTable } = await import('../HistoryTable');
  return {
    HistoryTable,
    LogViewer: () => null,
    StatusUpdateModal: ({
      visible,
      step,
    }: {
      visible: boolean;
      step?: { name: string };
    }) =>
      visible ? (
        <div role="dialog" aria-label={`status ${step?.name}`} />
      ) : null,
  };
});

// A local time (no offset), so the column dates do not depend on the time zone.
function dagRun(dagRunId: string, day: number, status = Status.Success) {
  return {
    dagRunId,
    name: 'etl',
    status,
    startedAt: `2026-03-0${day}T10:00:00`,
    rootDAGRunId: dagRunId,
    rootDAGRunName: 'etl',
    nodes: [{ step: { name: 'extract' }, status: NodeStatus.Success }],
  } as unknown as DAGRun;
}

// The server sends the runs newest first.
function serve(runs: DAGRun[]) {
  queryResult.current = {
    data: {
      dagRuns: runs,
      gridData: [
        { name: 'extract', history: runs.map(() => NodeStatus.Success) },
      ],
    },
    mutate: () => {},
  };
}

const newestFirst = () => [dagRun('r3', 3), dagRun('r2', 2), dagRun('r1', 1)];

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="search">{location.search}</output>;
}

// The clamp sets idx at once and writes ?idx through the router. With the
// router's default transitions, the stale ?idx sets idx back before the
// transition commits, and the two effects alternate without end in jsdom.
// Synchronous navigation lets the test pin the state the clamp settles on.
function renderHistory(search = '', extra?: React.ReactNode) {
  return render(
    <MemoryRouter
      initialEntries={[`/dags/etl/history${search}`]}
      unstable_useTransitions={false}
    >
      {extra}
      <DAGExecutionHistory fileName="etl" />
      <LocationProbe />
    </MemoryRouter>
  );
}

function selectedRun(): string | null {
  return screen.getByTestId('graph').getAttribute('data-run');
}

function params(): URLSearchParams {
  return new URLSearchParams(screen.getByTestId('search').textContent ?? '');
}

function runColumns(): HTMLElement[] {
  return screen.getAllByRole('columnheader').slice(1);
}

function pressKey(key: string, target: Element | Document = document) {
  act(() => {
    fireEvent.keyDown(target, { key });
  });
}

describe('DAGExecutionHistory', () => {
  beforeEach(() => {
    serve(newestFirst());
    overviewProps.current = null;
  });

  // parity:history-grid
  it('puts the oldest run in the left column', () => {
    renderHistory();

    expect(runColumns().map((th) => th.firstElementChild?.textContent)).toEqual(
      ['3/1', '3/2', '3/3']
    );
  });

  it('selects the last column, the newest run, without ?idx', () => {
    renderHistory();

    expect(selectedRun()).toBe('r3');
    expect(runColumns()[2]).toHaveClass('bg-muted/50');
    expect(params().has('idx')).toBe(false);
  });

  it('selects the reversed column n for ?idx=n', () => {
    renderHistory('?idx=0');
    expect(selectedRun()).toBe('r1');
  });

  it('selects the middle column for ?idx=1', () => {
    renderHistory('?idx=1');
    expect(selectedRun()).toBe('r2');
  });

  it('clamps an out-of-range idx and writes it back', () => {
    renderHistory('?idx=9');

    expect(selectedRun()).toBe('r3');
    expect(params().get('idx')).toBe('2');
    // The clamp writes only idx.
    expect(params().has('dagRunId')).toBe(false);
  });

  it('clamps a negative idx to the oldest run', () => {
    renderHistory('?idx=-4');

    expect(selectedRun()).toBe('r1');
    expect(params().get('idx')).toBe('0');
  });

  it('does not select by ?dagRunId alone', () => {
    renderHistory('?dagRunId=r1');

    expect(selectedRun()).toBe('r3');
    expect(params().has('idx')).toBe(false);
    expect(params().get('dagRunId')).toBe('r1');
  });

  it('writes idx, dagRunId and dagRunName on a column click', () => {
    renderHistory();

    fireEvent.click(runColumns()[0]!);

    expect(selectedRun()).toBe('r1');
    expect(params().get('idx')).toBe('0');
    expect(params().get('dagRunId')).toBe('r1');
    expect(params().get('dagRunName')).toBe('etl');
  });

  it('moves the selection with ArrowLeft and ArrowRight', () => {
    renderHistory();

    pressKey('ArrowLeft');
    expect(selectedRun()).toBe('r2');
    expect(params().get('idx')).toBe('1');
    expect(params().get('dagRunId')).toBe('r2');

    pressKey('ArrowLeft');
    pressKey('ArrowLeft');
    expect(selectedRun()).toBe('r1');

    pressKey('ArrowRight');
    expect(selectedRun()).toBe('r2');
    pressKey('ArrowRight');
    pressKey('ArrowRight');
    expect(selectedRun()).toBe('r3');
    expect(params().get('idx')).toBe('2');
  });

  // Today the document listener does not look at the focus, so the arrows
  // that move a text cursor also move the run. parity:chg-history-keys
  it('still moves on ArrowRight when a text input has focus', () => {
    renderHistory('?idx=0', <input type="text" aria-label="Filter" />);
    const input = screen.getByRole('textbox', { name: 'Filter' });
    input.focus();

    pressKey('ArrowRight', input);

    expect(selectedRun()).toBe('r2');
  });

  // parity:history-status-overview
  it('gives the selected run to the full status overview', () => {
    renderHistory('?idx=1');

    expect(overviewProps.current?.status).toMatchObject({ dagRunId: 'r2' });
    expect(overviewProps.current?.showRunMeta).toBeUndefined();
  });

  // Right-click on a step opens the status dialog unless the run is Running
  // or NotStarted: Queued and Waiting runs are not blocked.
  // parity:chg-history-status-gate
  describe('status update gate', () => {
    it.each([
      ['Running', Status.Running],
      ['NotStarted', Status.NotStarted],
    ])('blocks the dialog on a %s run', (_, status) => {
      serve([dagRun('r1', 1, status)]);
      renderHistory();

      fireEvent.click(
        within(screen.getByTestId('graph')).getByRole('button', {
          name: 'right-click step',
        })
      );

      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    it.each([
      ['Success', Status.Success],
      ['Failed', Status.Failed],
      ['Aborted', Status.Aborted],
      ['Queued', Status.Queued],
      ['PartialSuccess', Status.PartialSuccess],
      ['Waiting', Status.Waiting],
      ['Rejected', Status.Rejected],
    ])('opens the dialog on a %s run', (_, status) => {
      serve([dagRun('r1', 1, status)]);
      renderHistory();

      fireEvent.click(
        within(screen.getByTestId('graph')).getByRole('button', {
          name: 'right-click step',
        })
      );

      expect(
        screen.getByRole('dialog', { name: 'status extract' })
      ).toBeInTheDocument();
    });
  });
});
