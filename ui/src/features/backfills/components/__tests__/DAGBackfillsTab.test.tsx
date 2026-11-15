// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
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
  // A start calls onStarted, then closes the panel, as the dialog does.
  StartBackfillDialog: ({
    target,
    pickRange,
    onStarted,
    onOpenChange,
  }: {
    target: unknown;
    pickRange?: boolean;
    onStarted: (backfill: { id: string }) => void;
    onOpenChange: (open: boolean) => void;
  }) => (
    <div
      role="dialog"
      aria-label="Backfill"
      data-pick-range={String(pickRange)}
    >
      {JSON.stringify(target)}
      <button
        type="button"
        onClick={() => {
          onStarted({ id: 'bf123456' });
          onOpenChange(false);
        }}
      >
        Start
      </button>
    </div>
  ),
}));

function Location() {
  const location = useLocation();
  return (
    <output aria-label="Location">
      {location.pathname}
      {location.search}
    </output>
  );
}

function Back() {
  const navigate = useNavigate();
  return (
    <button type="button" onClick={() => navigate(-1)}>
      Back
    </button>
  );
}

/** The link the notice of missed slots gives. */
const GAP_URL =
  '/dags/etl?gapFrom=2026-11-01T09:00:00Z&gapTo=2026-11-01T21:00:00Z';

/** Renders the tab at the last of urls, the earlier ones behind it. */
function renderTab(canExecute: boolean, cache = new Map(), ...urls: string[]) {
  const entries = urls.length > 0 ? urls : ['/dags/etl'];
  return render(
    <SWRConfig value={{ provider: () => cache }}>
      <MemoryRouter initialEntries={entries} initialIndex={entries.length - 1}>
        <DAGBackfillsTab dagName="etl" fileName="etl" canExecute={canExecute} />
        <Location />
        <Back />
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

  // The notice of missed slots links the tab with the gap to backfill. The
  // range stays editable, since a gap may hold more slots than one backfill
  // may run.
  it('opens the start panel on a gap the link names', () => {
    renderTab(true, new Map(), GAP_URL);

    const dialog = screen.getByRole('dialog', { name: 'Backfill' });
    expect(dialog).toHaveTextContent(
      '{"kind":"range","from":"2026-11-01T09:00:00Z","to":"2026-11-01T21:00:00Z"}'
    );
    expect(dialog).toHaveAttribute('data-pick-range', 'true');
  });

  // Closing the panel after a start keeps the backfill's page open.
  it('opens the backfill a start from the gap started', () => {
    renderTab(true, new Map(), GAP_URL);

    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    expect(screen.getByRole('status', { name: 'Location' })).toHaveTextContent(
      '/backfills/bf123456'
    );
  });

  // The backfill's page takes the place of the gap's link, so going back
  // returns to the DAG's page rather than to the start panel on the gap.
  it('goes back from a started backfill to the page before the gap', () => {
    renderTab(true, new Map(), '/dags/etl', GAP_URL);

    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));

    expect(screen.getByRole('status', { name: 'Location' }).textContent).toBe(
      '/dags/etl'
    );
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('opens no start panel for a user who may not run the workflow', () => {
    renderTab(false, new Map(), GAP_URL);

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
