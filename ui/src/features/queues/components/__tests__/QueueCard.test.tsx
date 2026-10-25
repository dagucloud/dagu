// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import {
  type components,
  QueueType,
  RestartPolicy,
  AlwaysOnDAGState,
} from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import QueueList from '../QueueList';

// The strip that shares the state pieces reads always-on DAGs; the list gets them
// as a prop.
vi.mock('@/features/cockpit/hooks/useAlwaysOnDAGs', () => ({
  useAlwaysOnDAGs: () => ({ alwaysOnDAGs: [], error: null }),
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

const dag: components['schemas']['AlwaysOnDAG'] = {
  fileName: 'worker',
  name: 'worker',
  restartPolicy: RestartPolicy.always,
  restartWaitSec: 5,
  state: AlwaysOnDAGState.running,
  suspended: false,
  latestRun: {
    dagRunId: 'run-1',
    status: 1,
    startedAt: '2026-10-20T11:59:00Z',
  },
};

function renderList(
  queues: components['schemas']['Queue'][],
  alwaysOnDAGs: components['schemas']['AlwaysOnDAG'][]
) {
  render(
    <UserPreferencesProvider>
      <I18nProvider>
        <MemoryRouter>
          <QueueList queues={queues} alwaysOnDAGs={alwaysOnDAGs} />
        </MemoryRouter>
      </I18nProvider>
    </UserPreferencesProvider>
  );
}

describe('QueueList always-on DAGs', () => {
  it('shows the queue of an always-on DAG as the always-on DAG, without counts', () => {
    renderList([queue('worker'), queue('batch')], [dag]);

    const card = screen.getByRole('link', { name: /worker/ });
    expect(card).toHaveAttribute('href', '/queues/worker');
    expect(card).toHaveTextContent('Always on');
    expect(card).toHaveTextContent('Running');
    expect(card).not.toHaveTextContent('Capacity');
    expect(card).not.toHaveTextContent('Queued');

    // A queue that no always-on DAG owns keeps its counts.
    expect(screen.getByRole('link', { name: /batch/ })).toHaveTextContent(
      'Capacity'
    );
  });

  it('keeps the queues of always-on DAGs in a section of their own', () => {
    renderList([queue('worker'), queue('batch')], [dag]);

    const alwaysOn = screen.getByRole('region', { name: 'Always on' });
    expect(within(alwaysOn).getByRole('link', { name: /worker/ })).toBeVisible();
    expect(within(alwaysOn).queryByRole('link', { name: /batch/ })).toBeNull();

    const queues = screen.getByRole('region', { name: 'Queues' });
    expect(within(queues).getByRole('link', { name: /batch/ })).toBeVisible();
    expect(within(queues).queryByRole('link', { name: /worker/ })).toBeNull();
    expect(
      within(queues).getByRole('heading', { name: 'Queues' })
    ).toBeVisible();
  });

  // The server lists a DAG queue only while a run is alive or waiting.
  it('keeps the card of an always-on DAG between two runs, as an idle queue', () => {
    renderList([], [dag]);

    const alwaysOn = screen.getByRole('region', { name: 'Always on' });
    const card = within(alwaysOn).getByRole('link', { name: /worker/ });
    expect(card).toHaveAttribute('href', '/queues/worker');
    expect(card).toHaveTextContent('Running');
    expect(screen.queryByText('No active queues')).toBeNull();
  });

  it('shows no section titles for queues of one kind', () => {
    renderList([queue('batch')], []);

    expect(screen.queryByRole('region', { name: 'Always on' })).toBeNull();
    expect(screen.queryByRole('heading', { name: 'Queues' })).toBeNull();
  });

  it('keeps a global queue that shares its name with an always-on DAG', () => {
    renderList([queue('worker', QueueType.global)], [dag]);

    // That global queue is the queue of the DAG: one card, with its counts.
    expect(screen.getAllByRole('link', { name: /worker/ })).toHaveLength(1);
    expect(screen.getByRole('link', { name: /worker/ })).toHaveTextContent(
      'Capacity'
    );
  });
});
