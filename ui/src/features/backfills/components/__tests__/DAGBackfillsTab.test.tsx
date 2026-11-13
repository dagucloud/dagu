// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { DAGBackfillsTab } from '../DAGBackfillsTab';

const h = vi.hoisted(() => ({
  list: {} as { data?: unknown; error?: unknown },
}));

vi.mock('@/hooks/api', () => ({
  useQuery: () => ({ ...h.list, mutate: vi.fn() }),
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('../DAGSlotsView', () => ({
  DAGSlotsView: () => <section aria-label="Slots" />,
}));

function renderTab(canExecute: boolean) {
  render(
    <MemoryRouter>
      <DAGBackfillsTab dagName="etl" fileName="etl" canExecute={canExecute} />
    </MemoryRouter>
  );
}

describe('DAGBackfillsTab', () => {
  beforeEach(() => {
    h.list = {};
  });

  it('offers the slots when the backfills fail to load', () => {
    h.list = { error: { message: 'backfills unavailable' } };
    renderTab(true);

    expect(screen.getByRole('region', { name: 'Slots' })).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent(
      'backfills unavailable'
    );
  });

  it('points a user who may run the workflow to the slots', () => {
    h.list = { data: { backfills: [] } };
    renderTab(true);

    expect(
      screen.getByText(/Start one from the slots above\.$/)
    ).toBeInTheDocument();
  });

  it('tells a user who may not run the workflow only what a backfill is', () => {
    h.list = { data: { backfills: [] } };
    renderTab(false);

    expect(
      screen.getByText('A backfill runs the past slots of this schedule.')
    ).toBeInTheDocument();
  });
});
