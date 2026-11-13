// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { DAGSlotStatus } from '@/api/v1/schema';
import { useQuery } from '@/hooks/api';
import type { DAGSlot } from '../../lib/dagSlots';
import { DAGSlotsView } from '../DAGSlotsView';

const h = vi.hoisted(() => ({
  canExecute: true,
  targets: [] as unknown[],
}));

vi.mock('@/hooks/api', () => ({ useQuery: vi.fn() }));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('../StartBackfillDialog', () => ({
  StartBackfillDialog: ({ target }: { target: unknown }) => {
    h.targets.push(target);
    return <div role="dialog">start</div>;
  },
}));

const useQueryMock = useQuery as unknown as ReturnType<typeof vi.fn>;

const SLOTS: DAGSlot[] = [
  {
    slot: '2026-10-01T02:00:00Z',
    status: DAGSlotStatus.succeeded,
    runId: 'run-1',
  },
  { slot: '2026-10-02T02:00:00Z', status: DAGSlotStatus.missing },
  {
    slot: '2026-10-03T02:00:00Z',
    status: DAGSlotStatus.failed,
    runId: 'run-3',
  },
  { slot: '2026-10-04T02:00:00Z', status: DAGSlotStatus.missing },
];

function renderView() {
  useQueryMock.mockReturnValue({ data: { slots: SLOTS }, mutate: vi.fn() });
  render(
    <MemoryRouter>
      <DAGSlotsView fileName="etl" canExecute={h.canExecute} />
    </MemoryRouter>
  );
}

describe('DAGSlotsView', () => {
  beforeEach(() => {
    useQueryMock.mockReset();
    h.canExecute = true;
    h.targets = [];
  });

  it('backfills the slots picked, one by one or by state', () => {
    renderView();

    fireEvent.click(screen.getByRole('button', { name: 'Pick missing' }));
    expect(
      screen
        .getAllByRole('button', { pressed: true, name: /^Slot / })
        .map((cell) => cell.getAttribute('aria-label')?.endsWith('· Missing'))
    ).toEqual([true, true]);
    fireEvent.click(screen.getByRole('button', { name: /· Failed$/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Backfill 3 slots' }));

    expect(h.targets[h.targets.length - 1]).toEqual({
      kind: 'slots',
      slots: [
        '2026-10-02T02:00:00Z',
        '2026-10-03T02:00:00Z',
        '2026-10-04T02:00:00Z',
      ],
    });
  });

  it('backfills the whole range with nothing picked', () => {
    renderView();

    fireEvent.click(
      screen.getByRole('button', { name: 'Backfill this range' })
    );
    expect(h.targets[h.targets.length - 1]).toMatchObject({ kind: 'range' });
  });

  it('reads another range when one is chosen', () => {
    renderView();
    const from = () =>
      (
        useQueryMock.mock.calls[useQueryMock.mock.calls.length - 1]?.[1] as {
          params: { query: { from: string } };
        }
      ).params.query.from;
    const week = from();

    fireEvent.click(screen.getByRole('button', { name: 'Last 30 days' }));
    expect(new Date(from()).getTime()).toBeLessThan(new Date(week).getTime());
  });

  it('links slots to their runs for a user who may not run the DAG', () => {
    h.canExecute = false;
    renderView();

    expect(screen.getByRole('link', { name: /· Failed$/ })).toHaveAttribute(
      'href',
      '/dags/etl?run=run-3'
    );
    expect(screen.queryByRole('button', { name: /Backfill/ })).toBeNull();
  });
});
