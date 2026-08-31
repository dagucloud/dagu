// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { components, QueueType } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import QueueDetailsPage from '..';

const { clientMock, getMock, useQueryMock } = vi.hoisted(() => {
  const get = vi.fn();
  return {
    clientMock: { GET: get },
    getMock: get,
    useQueryMock: vi.fn(),
  };
});

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

function renderPage(queue: components['schemas']['Queue']): void {
  useQueryMock.mockReturnValue({
    data: queue,
    error: null,
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
});
