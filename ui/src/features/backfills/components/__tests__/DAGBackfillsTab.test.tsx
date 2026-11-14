// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { DAGBackfillsTab } from '../DAGBackfillsTab';

const h = vi.hoisted(() => ({ GET: vi.fn() }));

vi.mock('@/hooks/api', async () => {
  const { createQueryHook } = await import('swr-openapi');
  return { useQuery: createQueryHook({ GET: h.GET } as never, '/') };
});
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('../DAGSlotsView', () => ({
  DAGSlotsView: () => <section aria-label="Slots" />,
}));
vi.mock('../StartBackfillDialog', () => ({
  StartBackfillDialog: ({ target }: { target: unknown }) => (
    <div role="dialog" aria-label="Backfill">
      {JSON.stringify(target)}
    </div>
  ),
}));

function renderTab(canExecute: boolean, cache = new Map(), url = '/dags/etl') {
  return render(
    <SWRConfig value={{ provider: () => cache }}>
      <MemoryRouter initialEntries={[url]}>
        <DAGBackfillsTab dagName="etl" fileName="etl" canExecute={canExecute} />
      </MemoryRouter>
    </SWRConfig>
  );
}

describe('DAGBackfillsTab', () => {
  beforeEach(() => {
    h.GET.mockReset();
    h.GET.mockResolvedValue({ data: { backfills: [] } });
  });

  it('offers the slots when the backfills fail to load', async () => {
    h.GET.mockResolvedValue({ error: { message: 'backfills unavailable' } });
    renderTab(true);

    expect(screen.getByRole('region', { name: 'Slots' })).toBeInTheDocument();
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'backfills unavailable'
    );
  });

  // The notice of missed slots links the tab with the gap to backfill.
  it('opens the start panel on a gap the link names', () => {
    renderTab(
      true,
      new Map(),
      '/dags/etl?gapFrom=2026-11-01T09:00:00Z&gapTo=2026-11-01T21:00:00Z'
    );

    expect(screen.getByRole('dialog', { name: 'Backfill' })).toHaveTextContent(
      '{"kind":"range","from":"2026-11-01T09:00:00Z","to":"2026-11-01T21:00:00Z"}'
    );
  });

  it('opens no start panel for a user who may not run the workflow', () => {
    renderTab(
      false,
      new Map(),
      '/dags/etl?gapFrom=2026-11-01T09:00:00Z&gapTo=2026-11-01T21:00:00Z'
    );

    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('points a user who may run the workflow to the slots', async () => {
    renderTab(true);

    expect(
      await screen.findByText(/Start one from the slots above\.$/)
    ).toBeInTheDocument();
  });

  it('tells a user who may not run the workflow only what a backfill is', async () => {
    renderTab(false);

    expect(
      await screen.findByText(
        'A backfill runs the past slots of this schedule.'
      )
    ).toBeInTheDocument();
  });

  // A backfill started from the tab opens its own page; back on the tab, the
  // list has it, however soon the user returns.
  it('reads the list again each time the tab opens', async () => {
    const cache = new Map();
    const provider = () => cache;
    const host = (open: boolean) => (
      <SWRConfig value={{ provider }}>
        <MemoryRouter>
          {open && <DAGBackfillsTab dagName="etl" fileName="etl" canExecute />}
        </MemoryRouter>
      </SWRConfig>
    );
    const view = render(host(true));
    await screen.findByText('No backfills yet.');
    view.rerender(host(false));

    view.rerender(host(true));
    await screen.findByText('No backfills yet.');
    // Once per opening.
    await waitFor(() => expect(h.GET).toHaveBeenCalledTimes(2));
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(h.GET).toHaveBeenCalledTimes(2);
  });
});
