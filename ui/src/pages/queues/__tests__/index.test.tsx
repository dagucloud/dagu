// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import {
  QueueType,
  RestartPolicy,
  ServiceDAGState,
  type components,
} from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import Queues from '..';

function queue(name: string): components['schemas']['Queue'] {
  return {
    name,
    type: QueueType.dag_based,
    maxConcurrency: 1,
    runningCount: 1,
    queuedCount: 0,
    running: [],
  };
}

const worker: components['schemas']['ServiceDAG'] = {
  fileName: 'worker',
  name: 'worker',
  restartPolicy: RestartPolicy.always,
  restartWaitSec: 5,
  queue: 'worker',
  state: ServiceDAGState.restarting,
  suspended: false,
  consecutiveFailures: 0,
};

vi.mock('@/hooks/api', () => ({
  useQuery: () => ({
    data: { queues: [queue('batch')] },
    error: null,
    isLoading: false,
    mutate: vi.fn(),
  }),
}));

vi.mock('@/hooks/useQueuesListSSE', () => ({
  useQueuesListSSE: () => ({}),
}));

vi.mock('@/hooks/useSSECacheSync', () => ({
  sseFallbackOptions: () => ({}),
  useSSECacheSync: vi.fn(),
}));

// The page reloads its filters whenever the search state changes identity.
const searchState = vi.hoisted(() => ({
  readState: () => undefined,
  writeState: () => {},
}));

vi.mock('@/contexts/SearchStateContext', () => ({
  useSearchState: () => searchState,
}));

const serviceDAGs = vi.hoisted(() => ({ current: [] as unknown[] }));

vi.mock('@/features/service-dags/hooks/useServiceDAGs', () => ({
  useServiceDAGs: () => ({ serviceDAGs: serviceDAGs.current, error: null }),
}));

function renderPage(): void {
  render(
    <UserPreferencesProvider>
      <I18nProvider>
        <MemoryRouter>
          <AppBarContext.Provider
            value={{ selectedRemoteNode: 'local', setTitle: vi.fn() } as never}
          >
            <Queues />
          </AppBarContext.Provider>
        </MemoryRouter>
      </I18nProvider>
    </UserPreferencesProvider>
  );
}

describe('Queues', () => {
  // The queue of a service DAG between two runs is drawn by the page, so the
  // search must filter it as it filters the queues of the server.
  it('leaves the queues of service DAGs out of a search that misses them', () => {
    serviceDAGs.current = [worker];
    renderPage();
    expect(screen.getByRole('link', { name: /worker/ })).toBeVisible();

    fireEvent.change(screen.getByPlaceholderText('Search queues...'), {
      target: { value: 'batch' },
    });

    expect(screen.getByRole('link', { name: /batch/ })).toBeVisible();
    expect(screen.queryByRole('link', { name: /worker/ })).toBeNull();
  });

  // A remote node of an older version lists service DAGs without their queue.
  it('shows the queues of a server that does not report service DAG queues', () => {
    const older: Partial<typeof worker> = { ...worker };
    delete older.queue;
    serviceDAGs.current = [older];

    renderPage();
    fireEvent.change(screen.getByPlaceholderText('Search queues...'), {
      target: { value: 'b' },
    });

    expect(screen.getByRole('link', { name: /batch/ })).toBeVisible();
    expect(screen.queryByRole('link', { name: /worker/ })).toBeNull();
  });

  // The page draws the queue of a service DAG between two runs, which the
  // server does not list, so the count includes it.
  it('counts the queues of service DAGs that it draws', () => {
    serviceDAGs.current = [worker];

    renderPage();
    expect(screen.getByText('2 queues')).toBeVisible();

    fireEvent.change(screen.getByPlaceholderText('Search queues...'), {
      target: { value: 'batch' },
    });
    expect(screen.getByText('1 of 2 queues')).toBeVisible();
  });
});
