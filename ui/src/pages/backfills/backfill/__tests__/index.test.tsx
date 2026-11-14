// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  BackfillOnFailure,
  BackfillRerun,
  BackfillSlotState,
  BackfillStatus,
  type components,
} from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import BackfillPage from '..';

type Backfill = components['schemas']['Backfill'];
type BackfillSlot = components['schemas']['BackfillSlot'];

const mocks = vi.hoisted(() => ({
  post: vi.fn(),
  sse: vi.fn(),
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ POST: mocks.post }),
  useQuery: vi.fn(),
}));
vi.mock('@/contexts/AuthContext', () => ({
  useCanExecute: () => true,
}));
vi.mock('@/hooks/useBackfillSSE', () => ({
  useBackfillSSE: mocks.sse,
}));

const useQueryMock = useQuery as unknown as ReturnType<typeof vi.fn>;

function backfill(overrides: Partial<Backfill> = {}): Backfill {
  return {
    id: 'bf123456',
    dag: 'etl',
    fileName: 'etl',
    status: BackfillStatus.running,
    submittedAt: '2026-11-01T00:00:00Z',
    actor: 'alice',
    rerun: BackfillRerun.none,
    maxConcurrency: 1,
    onFailure: BackfillOnFailure.continue,
    slotCount: 28,
    counts: { succeeded: 11, failed: 1, running: 1, pending: 15 },
    waiting: 'Waiting: queue at limit, 2 runs ahead',
    ...overrides,
  };
}

const SLOTS: BackfillSlot[] = [
  {
    slot: '2026-10-01T02:00:00Z',
    state: BackfillSlotState.failed,
    runId: 'run-1',
  },
  {
    slot: '2026-10-02T02:00:00Z',
    state: BackfillSlotState.running,
    runId: 'run-2',
    otherDefinition: true,
  },
  { slot: '2026-10-03T02:00:00Z', state: BackfillSlotState.pending },
];

/** The answer to the slot query, which a test may change. */
const slotsAnswer = vi.hoisted(() => ({
  value: null as null | Record<string, unknown>,
}));

function renderPage(data: Backfill, search = '') {
  useQueryMock.mockImplementation((path: string) =>
    path === '/backfills/{backfillId}'
      ? { data, error: undefined, mutate: vi.fn() }
      : (slotsAnswer.value ?? {
          data: { slots: SLOTS, total: SLOTS.length },
          mutate: vi.fn(),
        })
  );
  const appBar = { setTitle: vi.fn(), selectedRemoteNode: 'local' } as never;
  // A new tick renders the page again, as a new answer of a query would.
  const tree = (tick: number) => (
    <MemoryRouter initialEntries={[`/backfills/${data.id}${search}`]}>
      <AppBarContext.Provider value={appBar}>
        <Routes>
          <Route
            path="/backfills/:backfillId"
            element={<BackfillPage key="page" data-tick={tick} />}
          />
        </Routes>
      </AppBarContext.Provider>
    </MemoryRouter>
  );
  let tick = 0;
  const view = render(tree(tick));
  return { rerender: () => view.rerender(tree(++tick)) };
}

function slotQueries(): Array<Record<string, unknown>> {
  return useQueryMock.mock.calls
    .filter((call: unknown[]) => call[0] === '/backfills/{backfillId}/slots')
    .map(
      (call: unknown[]) =>
        (call[1] as { params: { query: Record<string, unknown> } }).params.query
    );
}

describe('BackfillPage', () => {
  beforeEach(() => {
    // The status of a running backfill animates and reads the reduced-motion
    // preference.
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: vi.fn().mockImplementation((query: string) => ({
        matches: false,
        media: query,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      })),
    });
    useQueryMock.mockReset();
    mocks.post.mockReset();
    mocks.sse.mockReset();
    slotsAnswer.value = null;
    mocks.sse.mockReturnValue({
      data: null,
      isConnected: true,
      isConnecting: false,
      shouldUseFallback: false,
    });
  });

  it('says where an open backfill stands and follows it live', () => {
    renderPage(backfill());

    expect(
      screen.getByText('Running: 12 of 28 slots done, 1 failed')
    ).toBeInTheDocument();
    expect(
      screen.getByText('Waiting: queue at limit, 2 runs ahead')
    ).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'View runs' })).toHaveAttribute(
      'href',
      '/dag-runs?backfillId=bf123456&remoteNode=local'
    );
    expect(screen.getByRole('link', { name: 'etl' })).toHaveAttribute(
      'href',
      '/dags/etl?remoteNode=local'
    );
    expect(mocks.sse).toHaveBeenLastCalledWith('bf123456', true, 'local');
  });

  it('shows each slot by state and links a slot with a run to it', () => {
    renderPage(backfill());

    expect(screen.getByRole('link', { name: /· Failed$/ })).toHaveAttribute(
      'href',
      '/dag-runs/etl/run-1?remoteNode=local'
    );
    expect(screen.getByRole('img', { name: /· Pending$/ })).toBeInTheDocument();
  });

  it('marks a slot whose run ran an older definition', () => {
    renderPage(backfill());

    expect(
      screen.getByRole('link', { name: /· Running · Older definition$/ })
    ).toHaveAttribute('href', '/dag-runs/etl/run-2?remoteNode=local');
    expect(
      screen.queryByRole('link', { name: /· Failed · Older definition$/ })
    ).not.toBeInTheDocument();
  });

  it('lists the slots of a state picked in the legend', () => {
    renderPage(backfill());

    fireEvent.click(
      within(screen.getByRole('list', { name: 'Slot states' })).getByRole(
        'button',
        { name: /Failed/ }
      )
    );
    expect(slotQueries().slice(-1)[0]).toMatchObject({
      state: BackfillSlotState.failed,
      offset: 0,
    });
  });

  it('cancels with the runs in progress when asked to', async () => {
    mocks.post.mockResolvedValue({
      data: backfill({ status: BackfillStatus.cancelled }),
    });
    renderPage(backfill());

    fireEvent.click(screen.getByRole('button', { name: 'Cancel backfill' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('checkbox'));
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Cancel backfill' })
    );

    await waitFor(() =>
      expect(mocks.post).toHaveBeenCalledWith(
        '/backfills/{backfillId}/cancel',
        {
          params: {
            path: { backfillId: 'bf123456' },
            query: { remoteNode: 'local' },
          },
          body: { stopRunning: true },
        }
      )
    );
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('reads an ended backfill once and offers no cancel', async () => {
    renderPage(
      backfill({
        status: BackfillStatus.succeeded,
        counts: { succeeded: 28 },
        waiting: undefined,
      })
    );

    expect(screen.getByText('Succeeded: all 28 slots')).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Cancel backfill' })
    ).toBeNull();
    expect(screen.queryByRole('button', { name: 'Resume' })).toBeNull();
    await waitFor(() =>
      expect(mocks.sse).toHaveBeenLastCalledWith('bf123456', false, 'local')
    );
  });

  it('reads a backfill on the node that the link names', () => {
    renderPage(backfill(), '?remoteNode=edge');

    expect(slotQueries().slice(-1)[0]).toMatchObject({ remoteNode: 'edge' });
    expect(mocks.sse).toHaveBeenLastCalledWith('bf123456', true, 'edge');
    expect(screen.getByRole('link', { name: 'View runs' })).toHaveAttribute(
      'href',
      '/dag-runs?backfillId=bf123456&remoteNode=edge'
    );
    expect(screen.getByRole('link', { name: 'etl' })).toHaveAttribute(
      'href',
      '/dags/etl?remoteNode=edge'
    );
  });

  it('moves back to the last page when the slots of a state shrink', async () => {
    slotsAnswer.value = { data: { slots: SLOTS, total: 450 }, mutate: vi.fn() };
    const view = renderPage(backfill());
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(slotQueries().slice(-1)[0]).toMatchObject({ offset: 400 });

    slotsAnswer.value = { data: { slots: SLOTS, total: 190 }, mutate: vi.fn() };
    view.rerender();
    await waitFor(() =>
      expect(slotQueries().slice(-1)[0]).toMatchObject({ offset: 0 })
    );
  });

  it('reports slots that fail to load, with a retry', () => {
    const retry = vi.fn();
    slotsAnswer.value = {
      data: undefined,
      error: new Error('boom'),
      mutate: retry,
    };
    renderPage(backfill());

    expect(screen.getByRole('alert')).toHaveTextContent(
      'Failed to load the slots'
    );
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(retry).toHaveBeenCalled();
  });
});
