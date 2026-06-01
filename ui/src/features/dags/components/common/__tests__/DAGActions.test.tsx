// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Status, StatusLabel } from '@/api/v1/schema';
import DAGActions from '../DAGActions';

vi.mock('../../dag-execution', () => ({
  StartDAGModal: () => null,
}));

vi.mock('../../../../../contexts/ConfigContext', () => ({
  useConfig: () => ({
    permissions: {
      runDags: true,
    },
  }),
}));

vi.mock('../../../../../hooks/api', () => ({
  useClient: () => ({
    POST: vi.fn(),
    GET: vi.fn(),
  }),
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

describe('DAGActions', () => {
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
      />
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
      />
    );

    const queries = within(view.container);

    expect(queries.getByRole('button', { name: 'Retry' })).toBeDisabled();
    expect(queries.getByRole('button', { name: 'Stop' })).toBeEnabled();
  });

  it('disables retry when there is no DAG run id', () => {
    const view = render(
      <DAGActions
        status={undefined}
        fileName="finished-dag.yaml"
        dag={{ name: 'finished-dag' }}
        displayMode="full"
      />
    );

    expect(
      within(view.container).getByRole('button', { name: 'Retry' })
    ).toBeDisabled();
  });
});
