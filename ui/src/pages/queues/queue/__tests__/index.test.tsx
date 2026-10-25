// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { components, QueueType } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import QueueDetailsPage from '..';

const { clientMock, getMock, alwaysOnDAGs, useQueryMock } = vi.hoisted(() => {
  const get = vi.fn();
  return {
    clientMock: { GET: get },
    getMock: get,
    alwaysOnDAGs: { current: [] as unknown[] },
    useQueryMock: vi.fn(),
  };
});

vi.mock('@/features/cockpit/hooks/useAlwaysOnDAGs', () => ({
  useAlwaysOnDAGs: () => ({ alwaysOnDAGs: alwaysOnDAGs.current, error: null }),
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => clientMock,
  useQuery: useQueryMock,
}));

vi.mock('@/features/dag-runs/components/dag-run-details', () => ({
  DAGRunDetailsModal: () => null,
}));

const cappedZeroQueue: components['schemas']['Queue'] = {
  name: 'running-heavy-q',
  type: QueueType.dag_based,
  maxConcurrency: 1,
  runningCount: 0,
  queuedCount: 0,
  queuedCountCapped: true,
  running: [],
};

function renderPage(
  queue: components['schemas']['Queue'],
  { missing = false }: { missing?: boolean } = {}
): void {
  useQueryMock.mockReturnValue({
    data: missing ? undefined : queue,
    error: missing ? { status: 404 } : null,
    isLoading: false,
    isValidating: false,
    mutate: vi.fn(),
  });

  render(
    <MemoryRouter initialEntries={[`/queues/${queue.name}`]}>
      <AppBarContext.Provider
        value={
          {
            selectedRemoteNode: 'local',
            setTitle: vi.fn(),
          } as never
        }
      >
        <Routes>
          <Route path="/queues/:name" element={<QueueDetailsPage />} />
        </Routes>
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

describe('QueueDetailsPage', () => {
  beforeEach(() => {
    getMock.mockReset();
    useQueryMock.mockReset();
    alwaysOnDAGs.current = [];
    getMock.mockResolvedValue({
      data: { items: [] },
      error: undefined,
    });
  });

  it('loads items when a capped lower-bound count is zero', async () => {
    getMock.mockResolvedValueOnce({
      data: { items: [], nextCursor: 'cursor-1' },
      error: undefined,
    });

    renderPage(cappedZeroQueue);

    await waitFor(() => {
      expect(getMock).toHaveBeenCalledWith(
        '/queues/{name}/items',
        expect.any(Object)
      );
    });
    expect(
      await screen.findByText(
        'No visible queued items were returned for this queue.'
      )
    ).toBeVisible();
    expect(
      screen.queryByText('No queued items in this queue.')
    ).not.toBeInTheDocument();

    expect(getMock).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: 'Continue scanning' }));

    await waitFor(() => {
      expect(getMock).toHaveBeenCalledTimes(2);
    });
    expect(getMock).toHaveBeenNthCalledWith(
      2,
      '/queues/{name}/items',
      expect.objectContaining({
        params: expect.objectContaining({
          query: expect.objectContaining({ cursor: 'cursor-1' }),
        }),
      })
    );
  });

  it('does not load items when an exact count is zero', async () => {
    renderPage({ ...cappedZeroQueue, queuedCountCapped: undefined });

    expect(
      await screen.findByText('No queued items in this queue.')
    ).toBeVisible();
    expect(getMock).not.toHaveBeenCalled();
  });

  // An always-on DAG restarts without end; its state says more than its counts.
  it('shows the state of the always-on DAG that owns the queue', async () => {
    alwaysOnDAGs.current = [
      {
        fileName: 'running-heavy-q',
        name: 'running-heavy-q',
        restartPolicy: 'always',
        restartWaitSec: 5,
        state: 'failing',
        suspended: false,
      },
    ];

    renderPage({ ...cappedZeroQueue, queuedCountCapped: undefined });

    const link = await screen.findByRole('link', { name: /Failing/ });
    expect(link).toHaveAttribute('href', '/dags/running-heavy-q');
  });

  // A global queue can carry the name of a DAG; it does not belong to that DAG.
  it('does not take a global queue for the queue of an always-on DAG', async () => {
    alwaysOnDAGs.current = [
      {
        fileName: 'running-heavy-q',
        name: 'running-heavy-q',
        restartPolicy: 'always',
        restartWaitSec: 5,
        state: 'failing',
        suspended: false,
      },
    ];

    renderPage({
      ...cappedZeroQueue,
      type: QueueType.global,
      queuedCountCapped: undefined,
    });

    await screen.findByText('running-heavy-q');
    expect(screen.queryByRole('link', { name: /Failing/ })).toBeNull();
  });

  // The server lists a DAG queue only while a run is alive or waiting. An
  // always-on DAG is between two runs for a few seconds, and its queue is idle,
  // not missing.
  it('shows the queue of an always-on DAG as idle while the server does not list it', async () => {
    alwaysOnDAGs.current = [
      {
        fileName: 'running-heavy-q',
        name: 'running-heavy-q',
        restartPolicy: 'always',
        restartWaitSec: 5,
        state: 'restarting',
        suspended: false,
      },
    ];

    renderPage(cappedZeroQueue, { missing: true });

    expect(screen.queryByText('Queue not found')).toBeNull();
    expect(await screen.findByRole('link', { name: /Restarting/ })).toHaveAttribute(
      'href',
      '/dags/running-heavy-q'
    );
  });

  it('says that the queue is not found when no always-on DAG owns it', () => {
    renderPage(cappedZeroQueue, { missing: true });

    expect(screen.getByText('Queue not found')).toBeInTheDocument();
  });
});
