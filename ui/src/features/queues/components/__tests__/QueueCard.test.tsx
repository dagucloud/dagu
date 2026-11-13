// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import {
  type components,
  QueueType,
  RestartPolicy,
  ServiceDAGState,
} from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import QueueList from '../QueueList';

// The strip that shares the state pieces reads service DAGs; the list gets them
// as a prop.
vi.mock('@/features/service-dags/hooks/useServiceDAGs', () => ({
  useServiceDAGs: () => ({ serviceDAGs: [], error: null }),
}));

const queue = (
  name: string,
  type: components['schemas']['Queue']['type'] = QueueType.dag_based
): components['schemas']['Queue'] => ({
  name,
  type,
  maxConcurrency: 1,
  runningCount: 1,
  queuedCount: 0,
  running: [],
});

const dag: components['schemas']['ServiceDAG'] = {
  fileName: 'worker',
  name: 'worker',
  restartPolicy: RestartPolicy.always,
  restartWaitSec: 5,
  queue: 'worker',
  state: ServiceDAGState.running,
  suspended: false,
  consecutiveFailures: 0,
  latestRun: {
    dagRunId: 'run-1',
    status: 1,
    startedAt: '2026-10-20T11:59:00Z',
  },
};

function renderList(
  queues: components['schemas']['Queue'][],
  serviceDAGs: components['schemas']['ServiceDAG'][]
) {
  render(
    <UserPreferencesProvider>
      <I18nProvider>
        <MemoryRouter>
          <QueueList queues={queues} serviceDAGs={serviceDAGs} />
        </MemoryRouter>
      </I18nProvider>
    </UserPreferencesProvider>
  );
}

describe('QueueList service DAGs', () => {
  it('shows the queue of a service DAG as the service DAG, without counts', () => {
    renderList([queue('worker'), queue('batch')], [dag]);

    const card = screen.getByRole('link', { name: /worker/ });
    expect(card).toHaveAttribute('href', '/queues/worker');
    expect(card).toHaveTextContent('Service DAG');
    expect(card).toHaveTextContent('Running');
    expect(card).not.toHaveTextContent('Capacity');
    expect(card).not.toHaveTextContent('Queued');

    // A queue that no service DAG owns keeps its counts.
    expect(screen.getByRole('link', { name: /batch/ })).toHaveTextContent(
      'Capacity'
    );
  });

  it('keeps the queues of service DAGs in a section of their own', () => {
    renderList([queue('worker'), queue('batch')], [dag]);

    const serviceDAGs = screen.getByRole('region', { name: 'Service DAGs' });
    expect(
      within(serviceDAGs).getByRole('link', { name: /worker/ })
    ).toBeVisible();
    expect(
      within(serviceDAGs).queryByRole('link', { name: /batch/ })
    ).toBeNull();

    const queues = screen.getByRole('region', { name: 'Queues' });
    expect(within(queues).getByRole('link', { name: /batch/ })).toBeVisible();
    expect(within(queues).queryByRole('link', { name: /worker/ })).toBeNull();
    expect(
      within(queues).getByRole('heading', { name: 'Queues' })
    ).toBeVisible();
  });

  // The server lists a DAG queue only while a run is alive or waiting.
  it('keeps the card of a service DAG between two runs, as an idle queue', () => {
    renderList([], [dag]);

    const serviceDAGs = screen.getByRole('region', { name: 'Service DAGs' });
    const card = within(serviceDAGs).getByRole('link', { name: /worker/ });
    expect(card).toHaveAttribute('href', '/queues/worker');
    expect(card).toHaveTextContent('Running');
    expect(screen.queryByText('No active queues')).toBeNull();
  });

  it('shows no section titles for queues of one kind', () => {
    renderList([queue('batch')], []);

    expect(screen.queryByRole('region', { name: 'Service DAGs' })).toBeNull();
    expect(screen.queryByRole('heading', { name: 'Queues' })).toBeNull();
  });

  // Its runs enter the shared queue, so no queue is named after the DAG.
  it('draws no queue for a service DAG that sets another queue', () => {
    renderList([queue('services')], [{ ...dag, queue: 'services' }]);

    expect(screen.queryByRole('link', { name: /worker/ })).toBeNull();
    expect(screen.getByRole('link', { name: /services/ })).toHaveTextContent(
      'Capacity'
    );
  });

  it('keeps a global queue that shares its name with a service DAG', () => {
    renderList([queue('worker', QueueType.global)], [dag]);

    // That global queue is the queue of the DAG: one card, with its counts.
    expect(screen.getAllByRole('link', { name: /worker/ })).toHaveLength(1);
    expect(screen.getByRole('link', { name: /worker/ })).toHaveTextContent(
      'Capacity'
    );
  });
});
