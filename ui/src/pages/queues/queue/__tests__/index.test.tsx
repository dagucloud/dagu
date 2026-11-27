// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { components, QueueType, Status, StatusLabel } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import QueueDetailsPage from '..';

const { clientMock, getMock, serviceDAGs, useQueryMock } = vi.hoisted(() => {
  const get = vi.fn();
  return {
    clientMock: { GET: get },
    getMock: get,
    serviceDAGs: { current: [] as unknown[] },
    useQueryMock: vi.fn(),
  };
});

vi.mock('@/features/service-dags/hooks/useServiceDAGs', () => ({
  useServiceDAGs: () => ({ serviceDAGs: serviceDAGs.current, error: null }),
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => clientMock,
  useQuery: useQueryMock,
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

const queuedRun: components['schemas']['DAGRunSummary'] = {
  name: 'queued-dag',
  dagRunId: 'queued-1',
  status: Status.Queued,
  statusLabel: StatusLabel.queued,
  startedAt: '',
  finishedAt: '',
  artifactsAvailable: false,
  autoRetryCount: 0,
};

const activeQueue: components['schemas']['Queue'] = {
  ...cappedZeroQueue,
  queuedCountCapped: undefined,
  runningCount: 1,
  queuedCount: 1,
  running: [
    {
      ...queuedRun,
      name: 'running-dag',
      dagRunId: 'running-1',
      status: Status.Running,
      statusLabel: StatusLabel.running,
    },
  ],
};

function renderPage(
  queue: components['schemas']['Queue'],
  {
    missing = false,
    runDags = true,
  }: { missing?: boolean; runDags?: boolean } = {}
) {
  useQueryMock.mockReturnValue({
    data: missing ? undefined : queue,
    error: missing ? { status: 404 } : null,
    isLoading: false,
    isValidating: false,
    mutate: vi.fn(),
  });

  const router = (
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
          <Route path="/dags/queued-dag" element={<p>Queued run details</p>} />
        </Routes>
      </AppBarContext.Provider>
    </MemoryRouter>
  );

  const page = (canRunDags: boolean) => (
    <ConfigContext.Provider
      value={
        {
          authMode: 'basic',
          permissions: { runDags: canRunDags, writeDags: false },
          tzOffsetInSec: 0,
        } as Config
      }
    >
      {router}
    </ConfigContext.Provider>
  );
  const { rerender } = render(page(runDags));
  return (canRunDags: boolean) => rerender(page(canRunDags));
}

describe('QueueDetailsPage', () => {
  beforeEach(() => {
    vi.stubGlobal('matchMedia', () => ({
      matches: true,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }));
    getMock.mockReset();
    useQueryMock.mockReset();
    serviceDAGs.current = [];
    getMock.mockResolvedValue({
      data: { items: [] },
      error: undefined,
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('keeps queue details readable without permission to run DAGs', async () => {
    const user = userEvent.setup();
    getMock.mockResolvedValue({ data: { items: [queuedRun] } });
    renderPage(activeQueue, { runDags: false });

    expect(await screen.findByText('queued-1')).toBeVisible();
    expect(screen.getByText('running-1')).toBeVisible();
    expect(screen.getByText('Running (1)')).toBeVisible();
    expect(screen.getByText('Queued (1)')).toBeVisible();
    expect(screen.getByText('1 loaded')).toBeVisible();
    expect(screen.getByRole('link', { name: 'Back' })).toHaveAttribute(
      'href',
      '/queues'
    );
    expect(
      screen.queryByRole('button', { name: 'Dequeue selected' })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Select all loaded' })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Clear selection' })
    ).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

    await user.click(screen.getByRole('combobox', { name: 'Backfill runs' }));
    await user.click(
      screen.getByRole('option', { name: 'Backfill runs only' })
    );
    expect(getMock).toHaveBeenLastCalledWith(
      '/queues/{name}/items',
      expect.objectContaining({
        params: expect.objectContaining({
          query: expect.objectContaining({ backfill: 'only' }),
        }),
      })
    );

    await user.click(await screen.findByRole('button', { name: /queued-dag/ }));
    expect(screen.getByText('Queued run details')).toBeVisible();
  });

  it('allows selection and confirmation only while run permission is enabled', async () => {
    const user = userEvent.setup();
    getMock.mockResolvedValue({ data: { items: [queuedRun] } });
    const setRunDags = renderPage(activeQueue);

    const checkbox = await screen.findByRole('checkbox', {
      name: 'Select queued-dag queued-1',
    });
    const dequeue = screen.getByRole('button', { name: 'Dequeue selected' });
    expect(dequeue).toBeDisabled();
    await user.click(checkbox);
    expect(checkbox).toBeChecked();
    expect(screen.getByText('1 selected of 1 loaded')).toBeVisible();
    await user.click(dequeue);
    const dialog = screen.getByRole('dialog', { name: 'Dequeue selected' });
    expect(within(dialog).getByText('queued-1')).toBeVisible();
    expect(
      within(dialog).getByRole('button', { name: 'Dequeue 1 Run' })
    ).toBeEnabled();

    setRunDags(false);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(
      screen.queryByText('1 selected of 1 loaded')
    ).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();

    setRunDags(true);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Dequeue selected' })
    ).toBeDisabled();
    expect(getMock).not.toHaveBeenCalledWith(
      '/dag-runs/{name}/{dagRunId}/dequeue',
      expect.anything()
    );
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

  // A service DAG restarts without end; its state says more than its counts.
  it('shows the state of the service DAG that owns the queue', async () => {
    serviceDAGs.current = [
      {
        fileName: 'running-heavy-q',
        name: 'running-heavy-q',
        restartPolicy: 'always',
        restartWaitSec: 5,
        queue: 'running-heavy-q',
        state: 'failing',
        suspended: false,
        consecutiveFailures: 0,
      },
    ];

    renderPage({ ...cappedZeroQueue, queuedCountCapped: undefined });

    const link = await screen.findByRole('link', { name: /Failing/ });
    expect(link).toHaveAttribute('href', '/dags/running-heavy-q');
  });

  // A global queue can carry the name of a DAG; it does not belong to that DAG.
  it('does not take a global queue for the queue of a service DAG', async () => {
    serviceDAGs.current = [
      {
        fileName: 'running-heavy-q',
        name: 'running-heavy-q',
        restartPolicy: 'always',
        restartWaitSec: 5,
        queue: 'running-heavy-q',
        state: 'failing',
        suspended: false,
        consecutiveFailures: 0,
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

  // The server lists a DAG queue only while a run is alive or waiting. A
  // service DAG is between two runs for a few seconds, and its queue is idle,
  // not missing.
  it('shows the queue of a service DAG as idle while the server does not list it', async () => {
    serviceDAGs.current = [
      {
        fileName: 'running-heavy-q',
        name: 'running-heavy-q',
        restartPolicy: 'always',
        restartWaitSec: 5,
        queue: 'running-heavy-q',
        state: 'restarting',
        suspended: false,
        consecutiveFailures: 0,
      },
    ];

    renderPage(cappedZeroQueue, { missing: true });

    expect(screen.queryByText('Queue not found')).toBeNull();
    expect(
      await screen.findByRole('link', { name: /Restarting/ })
    ).toHaveAttribute('href', '/dags/running-heavy-q');
  });

  // A DAG that sets another queue runs there, so no queue is named after it.
  it('says that the queue is not found when its service DAG sets another queue', () => {
    serviceDAGs.current = [
      {
        fileName: 'running-heavy-q',
        name: 'running-heavy-q',
        restartPolicy: 'always',
        restartWaitSec: 5,
        queue: 'services',
        state: 'restarting',
        suspended: false,
        consecutiveFailures: 0,
      },
    ];

    renderPage(cappedZeroQueue, { missing: true });

    expect(screen.getByText('Queue not found')).toBeInTheDocument();
  });

  it('says that the queue is not found when no service DAG owns it', () => {
    renderPage(cappedZeroQueue, { missing: true });

    expect(screen.getByText('Queue not found')).toBeInTheDocument();
  });
});
