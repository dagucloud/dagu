// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { MissedSlotsReason } from '@/api/v1/schema';
import { useMissedSlotsNotice } from '../../hooks/useMissedSlotsNotice';
import { DAGBackfillsTab } from '../DAGBackfillsTab';

const h = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn() }));

vi.mock('@/hooks/api', async () => {
  const { createQueryHook } = await import('swr-openapi');
  return {
    useQuery: createQueryHook({ GET: h.GET } as never, '/'),
    useClient: () => ({ POST: h.POST }),
  };
});
vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: vi.fn() }),
}));
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
  return <output aria-label="Location">{location.pathname}</output>;
}

function renderTab(canExecute: boolean, cache = new Map(), url = '/dags/etl') {
  return render(
    <SWRConfig value={{ provider: () => cache }}>
      <MemoryRouter initialEntries={[url]}>
        <DAGBackfillsTab dagName="etl" fileName="etl" canExecute={canExecute} />
        <Location />
      </MemoryRouter>
    </SWRConfig>
  );
}

const GAP_URL =
  '/dags/etl?gapFrom=2026-11-01T09:00:00Z&gapTo=2026-11-01T21:00:00Z';
const DISMISSAL_PATH = '/dags/{fileName}/missed-slots/dismissal';

// The notice of missed slots over the canvas, as the DAG's page shows it.
function MissedSlotsNotice() {
  const notice = useMissedSlotsNotice('etl', 'local', true);
  return <p aria-label="Notice">{notice?.message}</p>;
}

// The tab with the notice of missed slots that links it, on a gap.
function renderTabWithNotice() {
  h.GET.mockImplementation(async (path: string) =>
    path === '/dags/{fileName}/missed-slots'
      ? {
          data: {
            missedSlots: {
              slots: 12,
              first: '2026-11-01T09:00:00Z',
              last: '2026-11-01T20:00:00Z',
              reasons: [MissedSlotsReason.suspended],
              until: '2026-11-01T20:30:00Z',
            },
          },
        }
      : { data: { backfills: [] } }
  );
  return render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <MemoryRouter initialEntries={[GAP_URL]}>
        <MissedSlotsNotice />
        <DAGBackfillsTab dagName="etl" fileName="etl" canExecute />
        <Location />
      </MemoryRouter>
    </SWRConfig>
  );
}

describe('DAGBackfillsTab', () => {
  beforeEach(() => {
    h.GET.mockReset();
    h.GET.mockResolvedValue({ data: { backfills: [] } });
    h.POST.mockReset();
    h.POST.mockResolvedValue({});
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
    renderTab(
      true,
      new Map(),
      '/dags/etl?gapFrom=2026-11-01T09:00:00Z&gapTo=2026-11-01T21:00:00Z'
    );

    const dialog = screen.getByRole('dialog', { name: 'Backfill' });
    expect(dialog).toHaveTextContent(
      '{"kind":"range","from":"2026-11-01T09:00:00Z","to":"2026-11-01T21:00:00Z"}'
    );
    expect(dialog).toHaveAttribute('data-pick-range', 'true');
  });

  // Closing the panel after a start keeps the backfill's page open.
  it('opens the backfill a start from the gap started', () => {
    renderTab(
      true,
      new Map(),
      '/dags/etl?gapFrom=2026-11-01T09:00:00Z&gapTo=2026-11-01T21:00:00Z'
    );

    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    expect(screen.getByRole('status', { name: 'Location' })).toHaveTextContent(
      '/backfills/bf123456'
    );
  });

  // A backfill started from the notice dismisses it for every user, as
  // Dismiss does; a later period brings a new one.
  it('dismisses the notice a backfill starts from', async () => {
    renderTabWithNotice();
    const notice = screen.getByLabelText('Notice');
    await waitFor(() => expect(notice).toHaveTextContent('12 slots passed'));

    fireEvent.click(screen.getByRole('button', { name: 'Start' }));

    await waitFor(() => expect(notice).toBeEmptyDOMElement());
    expect(h.POST).toHaveBeenCalledWith(DISMISSAL_PATH, {
      params: { path: { fileName: 'etl' }, query: { remoteNode: 'local' } },
    });
  });

  // The backfill has started, so a dismissal that fails is only logged.
  it('keeps the started backfill when the notice is not dismissed', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    h.POST.mockResolvedValue({ error: { message: 'disk full' } });
    renderTabWithNotice();
    const notice = screen.getByLabelText('Notice');
    await waitFor(() => expect(notice).toHaveTextContent('12 slots passed'));

    fireEvent.click(screen.getByRole('button', { name: 'Start' }));

    await waitFor(() => expect(warn).toHaveBeenCalled());
    expect(screen.getByRole('status', { name: 'Location' })).toHaveTextContent(
      '/backfills/bf123456'
    );
    expect(notice).toHaveTextContent('12 slots passed');
    warn.mockRestore();
  });

  // The backfill's page takes the place of the link that opened the panel,
  // so going back returns to the DAG's page rather than to the panel.
  it('goes back from a started backfill to the page before the link', () => {
    function Back() {
      const navigate = useNavigate();
      return (
        <button type="button" onClick={() => navigate(-1)}>
          Back
        </button>
      );
    }
    render(
      <SWRConfig value={{ provider: () => new Map() }}>
        <MemoryRouter
          initialEntries={[
            '/dags/etl',
            '/dags/etl?gapFrom=2026-11-01T09:00:00Z&gapTo=2026-11-01T21:00:00Z',
          ]}
          initialIndex={1}
        >
          <DAGBackfillsTab dagName="etl" fileName="etl" canExecute />
          <Location />
          <Back />
        </MemoryRouter>
      </SWRConfig>
    );

    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));

    expect(screen.getByRole('status', { name: 'Location' })).toHaveTextContent(
      '/dags/etl'
    );
    expect(screen.queryByRole('dialog')).toBeNull();
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
