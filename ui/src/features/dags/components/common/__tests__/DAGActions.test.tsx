// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Status, StatusLabel } from '@/api/v1/schema';
import DAGActions from '../DAGActions';
import { AppBarContext } from '@/contexts/AppBarContext';
import { DAGContext } from '@/features/dags/contexts/DAGContext';

const client = vi.hoisted(() => ({ POST: vi.fn(), GET: vi.fn() }));

vi.mock('../../dag-execution', () => ({
  RunProgressModal: ({
    visible,
    dagName,
    dagRunId,
  }: {
    visible: boolean;
    dagName: string;
    dagRunId: string;
  }) =>
    visible ? (
      <div role="dialog" aria-label="Run progress">
        {dagName} {dagRunId}
      </div>
    ) : null,
  StartDAGModal: ({
    visible,
    onSubmit,
  }: {
    visible: boolean;
    onSubmit: (
      params: string,
      id?: string,
      immediate?: boolean
    ) => Promise<void>;
  }) =>
    visible ? (
      <>
        <button onClick={() => void onSubmit('', undefined, true)}>
          Submit start
        </button>
        <button onClick={() => void onSubmit('', undefined, false)}>
          Submit enqueue
        </button>
      </>
    ) : null,
}));

vi.mock('../../../../../contexts/ConfigContext', () => ({
  useConfig: () => ({
    permissions: {
      runDags: true,
    },
  }),
}));

vi.mock('../../../../../hooks/api', () => ({
  useClient: () => client,
  useQuery: () => ({
    data: undefined,
    isLoading: false,
  }),
}));

vi.mock('../../../../../contexts/AuthContext', () => ({
  useCanManageProfiles: () => false,
}));

vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({
    showError: vi.fn(),
  }),
}));

vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({
    showToast: vi.fn(),
  }),
}));

function LocationProbe() {
  const location = useLocation();
  return (
    <output aria-label="Location">{location.pathname + location.search}</output>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  client.GET.mockResolvedValue({ data: { dag: { name: 'example' } } });
  client.POST.mockResolvedValue({ data: { dagRunId: 'created-run' } });
});

describe('DAGActions', () => {
  it.each(['start', 'enqueue'])(
    'shows progress without leaving the page after %s',
    async (action) => {
      render(
        <MemoryRouter initialEntries={['/dags']}>
          <AppBarContext.Provider
            value={{ selectedRemoteNode: 'edge' } as never}
          >
            <LocationProbe />
            <DAGActions
              fileName="example-file"
              dag={{ name: 'example' }}
              displayMode="full"
            />
          </AppBarContext.Provider>
        </MemoryRouter>
      );
      fireEvent.click(screen.getByRole('button', { name: 'Start' }));
      fireEvent.click(screen.getByRole('button', { name: `Submit ${action}` }));
      expect(
        await screen.findByRole('dialog', { name: 'Run progress' })
      ).toHaveTextContent('example created-run');
      expect(screen.getByLabelText('Location')).toHaveTextContent('/dags');
    }
  );

  it('shows progress for DAGs started by a containing panel', async () => {
    const onRunStarted = vi.fn();
    render(
      <MemoryRouter initialEntries={['/dags']}>
        <LocationProbe />
        <DAGContext.Provider
          value={{
            name: 'example',
            fileName: 'example-file',
            refresh: vi.fn(),
            onRunStarted,
          }}
        >
          <DAGActions
            fileName="example-file"
            dag={{ name: 'example' }}
            displayMode="full"
          />
        </DAGContext.Provider>
      </MemoryRouter>
    );
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    fireEvent.click(screen.getByRole('button', { name: 'Submit start' }));
    expect(
      await screen.findByRole('dialog', { name: 'Run progress' })
    ).toHaveTextContent('example created-run');
    expect(onRunStarted).not.toHaveBeenCalled();
    expect(screen.getByLabelText('Location')).toHaveTextContent('/dags');
  });
  it('shows cancel for failed runs with pending auto retries', () => {
    render(
      <DAGActions
        status={{
          name: 'retry-dag',
          dagRunId: 'run-1',
          status: Status.Failed,
          statusLabel: StatusLabel.failed,
          artifactsAvailable: false,
          autoRetryCount: 1,
          autoRetryLimit: 3,
          startedAt: '',
          finishedAt: '',
        }}
        fileName="retry-dag.yaml"
        dag={{ name: 'retry-dag' }}
        displayMode="full"
      />,
      { wrapper: MemoryRouter }
    );

    expect(screen.getByRole('button', { name: 'Cancel' })).toBeEnabled();
  });

  it('disables retry for running DAG executions', () => {
    const view = render(
      <DAGActions
        status={{
          name: 'running-dag',
          dagRunId: 'run-1',
          status: Status.Running,
          statusLabel: StatusLabel.running,
          artifactsAvailable: false,
          autoRetryCount: 0,
          startedAt: '',
          finishedAt: '',
        }}
        fileName="running-dag.yaml"
        dag={{ name: 'running-dag' }}
        displayMode="full"
      />,
      { wrapper: MemoryRouter }
    );

    const queries = within(view.container);

    expect(queries.getByRole('button', { name: 'Retry' })).toBeDisabled();
    expect(queries.getByRole('button', { name: 'Stop' })).toBeEnabled();
  });

  it('disables retry for waiting DAG executions', () => {
    const view = render(
      <DAGActions
        status={{
          name: 'waiting-dag',
          dagRunId: 'run-1',
          status: Status.Waiting,
          statusLabel: StatusLabel.waiting,
          artifactsAvailable: false,
          autoRetryCount: 0,
          startedAt: '',
          finishedAt: '',
        }}
        fileName="waiting-dag.yaml"
        dag={{ name: 'waiting-dag' }}
        displayMode="full"
      />,
      { wrapper: MemoryRouter }
    );

    expect(
      within(view.container).getByRole('button', { name: 'Retry' })
    ).toBeDisabled();
  });

  it('disables retry when there is no DAG run id', () => {
    const view = render(
      <DAGActions
        status={undefined}
        fileName="finished-dag.yaml"
        dag={{ name: 'finished-dag' }}
        displayMode="full"
      />,
      { wrapper: MemoryRouter }
    );

    expect(
      within(view.container).getByRole('button', { name: 'Retry' })
    ).toBeDisabled();
  });

  it('hides whole-run retry for child DAG runs', () => {
    const view = render(
      <DAGActions
        status={{
          name: 'child-dag',
          dagRunId: 'child-run',
          rootDAGRunName: 'root-dag',
          rootDAGRunId: 'root-run',
          status: Status.Failed,
          statusLabel: StatusLabel.failed,
          artifactsAvailable: false,
          autoRetryCount: 0,
          startedAt: '',
          finishedAt: '',
        }}
        fileName="child-dag.yaml"
        dag={{ name: 'child-dag' }}
        displayMode="full"
      />,
      { wrapper: MemoryRouter }
    );

    expect(
      within(view.container).queryByRole('button', { name: 'Retry' })
    ).not.toBeInTheDocument();
  });
});
